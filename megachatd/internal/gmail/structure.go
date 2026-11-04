package gmail

import (
	"errors"
	"net/textproto"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// BODYSTRUCTURE partial fetching: the port of server/gmail.py's
// _fetch_structure / _index_bodystructure / _fetch_part_payloads. A message
// is opened with one FETCH of BODYSTRUCTURE plus the few headers needed, then
// one FETCH of the text parts (and the small inline images); the attachments
// are fetched one part at a time. Anything unexpected falls back to the full
// BODY.PEEK[] fetch.

const (
	maxRenderParts         = 8
	maxBodystructureDepth  = 64
	maxBodystructureNodes  = 10_000
	inlinePrefetchTotalMax = 6 * 1024 * 1024
)

// errPartFetch is a logically completed partial fetch that must fall back to
// the full fetch (_PartFetchError).
var errPartFetch = errors.New("partial fetch unusable")

// structureHeaders is _STRUCTURE_FETCH_ITEM's header section.
var structureHeaders = headerFieldsSection("FROM", "TO", "CC", "SUBJECT", "DATE", "MESSAGE-ID", "IN-REPLY-TO", "REFERENCES")

// partInfo is a leaf MIME part of the BODYSTRUCTURE (_MimePart).
type partInfo struct {
	path       []int
	maintype   string
	subtype    string
	params     map[string]string
	disp       string
	dispParams map[string]string
	contentID  string // decoded, without <>
	hasCID     bool
	encoding   string
	octets     int
	filename   string
}

func (p *partInfo) contentType() string { return p.maintype + "/" + p.subtype }

// partMap is _PartMap.
type partMap struct {
	render      []*partInfo
	attachments []*partInfo
}

type structRecord struct {
	uid     int64
	headers []byte
	index   *partMap
}

// ---- RFC 2231 parameters -------------------------------------------------------

var (
	rfc2231RE     = regexp.MustCompile(`(?s)^([^']*)'([^']*)'(.*)$`)
	rfc2231ContRE = regexp.MustCompile(`(?s)^(.*)\*(\d+)(\*)?$`)
	mimeTokenRE   = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+\\-.^_`{|}~]+$")
	charsetNameRE = regexp.MustCompile(`^[A-Za-z0-9._\-]+$`)
)

// unquoteToBytes is urllib.parse.unquote_to_bytes.
func unquoteToBytes(s string) []byte {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b = append(b, byte(v))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return b
}

func decodeParam(value string, encoded bool) string {
	if value == "" {
		return ""
	}
	text := value
	if encoded {
		charset := ""
		if m := rfc2231RE.FindStringSubmatch(text); m != nil {
			charset, text = m[1], m[3]
		}
		return decodeMIMEHeader(decodeCharset(unquoteToBytes(text), charset))
	}
	return decodeMIMEHeader(text)
}

// paramsGet is _params_get: a parameter with RFC 2231 continuations combined.
func paramsGet(params map[string]string, name string) string {
	lowered := strings.ToLower(name)
	type chunk struct {
		n       int
		encoded bool
		value   string
	}
	var numbered []chunk
	for k, v := range params {
		k = strings.ToLower(k)
		if m := rfc2231ContRE.FindStringSubmatch(k); m != nil && m[1] == lowered {
			n, _ := strconv.Atoi(m[2])
			numbered = append(numbered, chunk{n, m[3] != "", v})
		}
	}
	if len(numbered) > 0 {
		sort.Slice(numbered, func(i, j int) bool {
			if numbered[i].n != numbered[j].n {
				return numbered[i].n < numbered[j].n
			}
			return !numbered[i].encoded && numbered[j].encoded
		})
		charset := ""
		var chunks []byte
		for off, c := range numbered {
			text := c.value
			if off == 0 && c.encoded {
				if m := rfc2231RE.FindStringSubmatch(text); m != nil {
					charset, text = m[1], m[3]
				}
			}
			if c.encoded {
				chunks = append(chunks, unquoteToBytes(text)...)
			} else {
				for _, r := range text {
					if r > 0xff {
						chunks = append(chunks, '?')
					} else {
						chunks = append(chunks, byte(r))
					}
				}
			}
		}
		return decodeMIMEHeader(decodeCharset(chunks, charset))
	}
	lowerMap := make(map[string]string, len(params))
	for k, v := range params {
		lowerMap[strings.ToLower(k)] = v
	}
	if v, ok := lowerMap[lowered+"*"]; ok {
		return decodeParam(v, true)
	}
	return decodeParam(lowerMap[lowered], false)
}

// ---- indexing --------------------------------------------------------------------

type indexer struct {
	nodes int
	pm    partMap
	bad   bool
}

func dispOf(bs imap.BodyStructure) (string, map[string]string) {
	if d := bs.Disposition(); d != nil {
		return strings.ToLower(d.Value), d.Params
	}
	return "", nil
}

func newPartInfo(sp *imap.BodyStructureSinglePart, path []int) *partInfo {
	disp, dparams := dispOf(sp)
	p := &partInfo{
		path:     append([]int(nil), path...),
		maintype: strings.ToLower(sp.Type), subtype: strings.ToLower(sp.Subtype),
		params: sp.Params, disp: disp, dispParams: dparams,
		hasCID:   sp.ID != "",
		encoding: strings.ToLower(sp.Encoding),
		octets:   int(sp.Size),
	}
	p.contentID = strings.Trim(strings.TrimSpace(decodeMIMEHeader(sp.ID)), "<>")
	p.filename = paramsGet(dparams, "filename")
	if p.filename == "" {
		p.filename = paramsGet(sp.Params, "name")
	}
	return p
}

func isRFC822(bs imap.BodyStructure) (*imap.BodyStructureSinglePart, imap.BodyStructure) {
	sp, ok := bs.(*imap.BodyStructureSinglePart)
	if !ok || sp.MessageRFC822 == nil || !strings.EqualFold(sp.Type, "message") || !strings.EqualFold(sp.Subtype, "rfc822") {
		return nil, nil
	}
	if sp.MessageRFC822.BodyStructure == nil {
		return nil, nil
	}
	return sp, sp.MessageRFC822.BodyStructure
}

func (ix *indexer) descend(bs imap.BodyStructure, path []int, depth int) {
	ix.nodes++
	if depth > maxBodystructureDepth || ix.nodes > maxBodystructureNodes {
		ix.bad = true
		return
	}
	if mp, ok := bs.(*imap.BodyStructureMultiPart); ok {
		for i, c := range mp.Children {
			ix.descend(c, append(path[:len(path):len(path)], i+1), depth+1)
		}
		return
	}
	if _, child := isRFC822(bs); child != nil {
		ix.descendEmbedded(child, path, depth)
		return
	}
	sp, ok := bs.(*imap.BodyStructureSinglePart)
	if !ok {
		ix.bad = true
		return
	}
	p := newPartInfo(sp, path)
	if p.filename != "" || p.disp == "attachment" || (p.hasCID && p.maintype != "text") {
		ix.pm.attachments = append(ix.pm.attachments, p)
	} else if p.maintype == "text" && (p.subtype == "plain" || p.subtype == "html") {
		ix.pm.render = append(ix.pm.render, p)
	}
}

// descendEmbedded handles the body of a message/rfc822 part: a multipart is
// flattened into the numbering of its parent, anything else is N.1.
func (ix *indexer) descendEmbedded(child imap.BodyStructure, path []int, depth int) {
	if mp, ok := child.(*imap.BodyStructureMultiPart); ok {
		for i, n := range mp.Children {
			ix.descend(n, append(path[:len(path):len(path)], i+1), depth+1)
		}
		return
	}
	ix.descend(child, append(path[:len(path):len(path)], 1), depth+1)
}

// indexBodystructure is _index_bodystructure; nil when the structure exceeds
// the parser limits.
func indexBodystructure(root imap.BodyStructure) *partMap {
	ix := &indexer{}
	switch {
	case isMulti(root):
		for i, c := range root.(*imap.BodyStructureMultiPart).Children {
			ix.descend(c, []int{i + 1}, 1)
		}
	default:
		if _, child := isRFC822(root); child != nil {
			if mp, ok := child.(*imap.BodyStructureMultiPart); ok {
				for i, n := range mp.Children {
					ix.descend(n, []int{1, i + 1}, 1)
				}
			} else {
				ix.descend(child, []int{1, 1}, 1)
			}
		} else {
			ix.descend(root, []int{1}, 0)
		}
	}
	if ix.bad {
		return nil
	}
	return &ix.pm
}

func isMulti(bs imap.BodyStructure) bool {
	_, ok := bs.(*imap.BodyStructureMultiPart)
	return ok
}

// ---- fetching -----------------------------------------------------------------------

// fetchStructure is _fetch_structure: nil (and no error) means "fall back to
// the full fetch"; an error means the connection is unusable.
func (s *Service) fetchStructure(c *imapConn, uid int64) (*structRecord, error) {
	items, err := c.fetch("", uidOne(uid), &imap.FetchOptions{
		UID: true, Flags: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{structureHeaders},
	})
	if err != nil {
		if isUpstream(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec fetched
	found := false
	for _, it := range items {
		if it.UID != uid {
			return nil, nil // a response for another UID: not trustworthy
		}
		rec, found = it, true
	}
	if !found || rec.buf.BodyStructure == nil {
		return nil, nil
	}
	hdr, ok := rec.lookup(structureHeaders)
	if !ok {
		return nil, nil
	}
	idx := indexBodystructure(rec.buf.BodyStructure)
	if idx == nil {
		return nil, nil
	}
	return &structRecord{uid: uid, headers: hdr, index: idx}, nil
}

func isUpstream(err error) bool {
	var u *upstreamError
	return errors.As(err, &u)
}

func pathKey(path []int) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// fetchPartPayloads is _fetch_part_payloads: one UID FETCH of BODY.PEEK[n]
// sections, keyed by part number. errPartFetch when the response is not
// exactly what was asked for.
func (s *Service) fetchPartPayloads(c *imapConn, uid int64, parts []*partInfo) (map[string][]byte, error) {
	if len(parts) == 0 {
		return map[string][]byte{}, nil
	}
	var sections []*imap.FetchItemBodySection
	seen := map[string]bool{}
	for _, p := range parts {
		if len(p.path) == 0 {
			return nil, errPartFetch
		}
		for _, n := range p.path {
			if n < 1 {
				return nil, errPartFetch
			}
		}
		if k := pathKey(p.path); !seen[k] {
			seen[k] = true
			sections = append(sections, partSection(p.path))
		}
	}
	items, err := c.fetch("", uidOne(uid), &imap.FetchOptions{UID: true, BodySection: sections})
	if err != nil {
		if isUpstream(err) {
			return nil, errPartFetch
		}
		return nil, err
	}
	if len(items) != 1 || items[0].UID != uid {
		return nil, errPartFetch
	}
	out := map[string][]byte{}
	for _, sec := range sections {
		b, ok := items[0].lookup(sec)
		if !ok {
			return nil, errPartFetch
		}
		out[pathKey(sec.Part)] = b
	}
	if len(items[0].buf.BodySection) != len(sections) {
		return nil, errPartFetch
	}
	return out, nil
}

// fetchRenderParts fetches the text parts plus extra parts in one UID FETCH.
func (s *Service) fetchRenderParts(c *imapConn, uid int64, pm *partMap, extra []*partInfo) (map[string][]byte, error) {
	if len(pm.render) > maxRenderParts {
		return nil, errPartFetch
	}
	sel := append(append([]*partInfo(nil), pm.render...), extra...)
	return s.fetchPartPayloads(c, uid, sel)
}

// ---- decoding fetched parts -------------------------------------------------------------

// miniPart is _mini_part_message: the fetched body with just enough headers
// (type, charset, transfer encoding) to decode it.
func miniPart(p *partInfo, payload []byte) (*mimePart, error) {
	if !mimeTokenRE.MatchString(p.maintype) || !mimeTokenRE.MatchString(p.subtype) {
		return nil, errPartFetch
	}
	params := map[string]string{}
	if cs := strings.TrimSpace(paramsGet(p.params, "charset")); cs != "" && charsetNameRE.MatchString(cs) {
		params["charset"] = cs
	}
	hdr := textproto.MIMEHeader{}
	if enc := strings.TrimSpace(strings.ToLower(p.encoding)); enc != "" {
		if !mimeTokenRE.MatchString(enc) {
			return nil, errPartFetch
		}
		hdr.Set("Content-Transfer-Encoding", enc)
	}
	return &mimePart{Header: hdr, Type: p.contentType(), Params: params, DispPars: map[string]string{}, Body: payload}, nil
}

func decodeFetchedText(p *partInfo, payload []byte) (string, error) {
	mp, err := miniPart(p, payload)
	if err != nil {
		return "", err
	}
	return mp.text(), nil
}

func decodeFetchedBytes(p *partInfo, payload []byte) ([]byte, error) {
	mp, err := miniPart(p, payload)
	if err != nil {
		return nil, err
	}
	return mp.decoded(), nil
}

func safePartFilename(p *partInfo, index int) string {
	if n := sanitizeFilename(p.filename); n != "" {
		return n
	}
	return "attachment-" + strconv.Itoa(index)
}

// inlineImageCandidates is _inline_image_candidates: small images with a
// Content-ID, chosen from the BODYSTRUCTURE alone.
func inlineImageCandidates(pm *partMap) []*partInfo {
	var sel []*partInfo
	total := 0
	for _, p := range pm.attachments {
		if p.maintype != "image" || p.contentID == "" || p.disp == "attachment" ||
			p.octets > inlinePrefetchPartMax || total+p.octets > inlinePrefetchTotalMax {
			continue
		}
		sel = append(sel, p)
		total += p.octets
	}
	return sel
}

// assembleDetail is _assemble_detail.
func (s *Service) assembleDetail(uid int64, rec *structRecord, payloads map[string][]byte, folder string) (*parsedMessage, error) {
	h := parseMIME(rec.headers, maxMIMEDepth).Header
	var plain, htmls []string
	for _, p := range rec.index.render {
		payload, ok := payloads[pathKey(p.path)]
		if !ok {
			continue
		}
		text, err := decodeFetchedText(p, payload)
		if err != nil {
			return nil, err
		}
		if p.subtype == "plain" {
			plain = append(plain, strings.TrimSpace(text))
		} else {
			htmls = append(htmls, text)
		}
	}
	atts := make([]attachment, len(rec.index.attachments))
	for i, p := range rec.index.attachments {
		atts[i] = attachment{
			Index: i, Filename: safePartFilename(p, i), ContentType: p.contentType(),
			Size: p.octets, ContentID: p.contentID,
		}
	}
	rawH := strings.Join(htmls, "\n")
	markInlineImages(atts, rawH)
	var cids map[string]string
	if folder != "" {
		cids = s.cidImageURLs(atts, folder, uid)
	}
	return &parsedMessage{
		From: decodeMIMEHeader(h.Get("From")), To: decodeMIMEHeader(h.Get("To")),
		Cc: decodeMIMEHeader(h.Get("Cc")), Subject: decodeMIMEHeader(h.Get("Subject")),
		DateTS:     parseDateTS(h.Get("Date")),
		MessageID:  strings.TrimSpace(decodeMIMEHeader(h.Get("Message-Id"))),
		InReplyTo:  strings.TrimSpace(decodeMIMEHeader(h.Get("In-Reply-To"))),
		References: strings.TrimSpace(decodeMIMEHeader(h.Get("References"))),
		Text:       joinText(plain, htmls), HTML: s.sanitizeHTML(rawH, cids), Attachments: atts,
	}, nil
}

// cacheInlineImages caches the prefetched payloads of the images marked inline.
func (s *Service) cacheInlineImages(folder string, uid int64, pm *partMap, atts []attachment, payloads map[string][]byte) {
	for _, a := range atts {
		if !a.Inline {
			continue
		}
		payload, ok := payloads[pathKey(pm.attachments[a.Index].path)]
		if !ok {
			continue
		}
		data, err := decodeFetchedBytes(pm.attachments[a.Index], payload)
		if err != nil {
			continue
		}
		s.inlineCachePut(folder, uid, a.Index, data)
	}
}
