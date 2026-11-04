package gmail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

const maxMIMEDepth = 64

var (
	headerFoldRE  = regexp.MustCompile(`\r?\n[ \t]`)
	headerBreakRE = regexp.MustCompile(`[\r\n]+`)
)

// unfoldHeader unfolds a header value onto one line (unfold_header).
func unfoldHeader(v string) string {
	// Python: remove CRLF before whitespace (keeping the whitespace), then
	// turn any other line break run into a single space.
	v = headerFoldRE.ReplaceAllStringFunc(v, func(m string) string { return m[len(m)-1:] })
	return headerBreakRE.ReplaceAllString(v, " ")
}

func charsetReader(label string, input io.Reader) (io.Reader, error) {
	enc, err := lookupCharset(label)
	if err != nil {
		// Unknown charsets decode as UTF-8 with replacement (Python tolerates).
		b, _ := io.ReadAll(input)
		return strings.NewReader(strings.ToValidUTF8(string(b), "�")), nil
	}
	b, _ := io.ReadAll(input)
	return strings.NewReader(enc.NewDecoder().String(string(b))), nil
}

func lookupCharset(label string) (encoding.Encoding, error) {
	label = strings.TrimSpace(strings.Trim(label, `"'`))
	if label == "" {
		return nil, io.EOF
	}
	return htmlindex.Get(label)
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// decodeMIMEHeader decodes RFC 2047 words, tolerating unknown charsets, and
// unfolds the result (decode_mime_header).
func decodeMIMEHeader(v string) string {
	if v == "" {
		return ""
	}
	out, err := wordDecoder.DecodeHeader(v)
	if err != nil {
		out = v
	}
	return unfoldHeader(strings.ToValidUTF8(out, "�"))
}

// parseDateTS converts an RFC 5322 Date header to epoch seconds (_date_to_ts).
func parseDateTS(v string) *int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	t, err := mail.ParseDate(v)
	if err != nil {
		// A few lenient fallbacks Python's parsedate_tz accepts.
		for _, layout := range []string{
			"Mon, 2 Jan 2006 15:04:05 MST", "2 Jan 2006 15:04:05 MST",
			"Mon, 2 Jan 2006 15:04:05", "2 Jan 2006 15:04:05",
			time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC,
		} {
			if t2, e := time.Parse(layout, v); e == nil {
				t, err = t2, nil
				break
			}
		}
		if err != nil {
			return nil
		}
	}
	ts := t.Unix()
	return &ts
}

// ---- MIME tree -----------------------------------------------------------------

type mimePart struct {
	Header   textproto.MIMEHeader
	Type     string // lowercase "type/subtype"
	Params   map[string]string
	Disp     string // lowercase disposition token ("" if none)
	DispPars map[string]string
	Body     []byte // raw body (transfer-encoded)
	Children []*mimePart
}

func (p *mimePart) get(name string) string {
	return p.Header.Get(name)
}

func (p *mimePart) maintype() string {
	t, _, _ := strings.Cut(p.Type, "/")
	return t
}

func (p *mimePart) subtype() string {
	_, s, _ := strings.Cut(p.Type, "/")
	return s
}

// isMultipart mirrors email.message.Message.is_multipart(): multipart/* and
// message/rfc822 containers have children.
func (p *mimePart) isMultipart() bool { return len(p.Children) > 0 }

// filename mirrors Message.get_filename(): Content-Disposition filename, then
// Content-Type name.
func (p *mimePart) filename() string {
	if v, ok := p.DispPars["filename"]; ok {
		return v
	}
	if v, ok := p.Params["name"]; ok {
		return v
	}
	return ""
}

func (p *mimePart) isAttachment() bool {
	return p.filename() != "" || p.Disp == "attachment"
}

// parseMIME parses a full message (or sub-message) into a part tree.
func parseMIME(raw []byte, depth int) *mimePart {
	hdr, body := splitHeaderBody(raw)
	p := &mimePart{Header: hdr, Body: body}
	ct := hdr.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil && mt == "" {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
		params = map[string]string{}
		if mt != "" && !strings.Contains(mt, "/") {
			mt = ""
		}
	}
	if mt == "" {
		mt = "text/plain"
	}
	p.Type = strings.ToLower(mt)
	if params == nil {
		params = map[string]string{}
	}
	p.Params = params
	if cd := hdr.Get("Content-Disposition"); cd != "" {
		d, dp, derr := mime.ParseMediaType(cd)
		if d == "" && derr != nil {
			d = strings.ToLower(strings.TrimSpace(strings.SplitN(cd, ";", 2)[0]))
			dp = map[string]string{}
		}
		p.Disp = strings.ToLower(d)
		if dp == nil {
			dp = map[string]string{}
		}
		p.DispPars = dp
	} else {
		p.DispPars = map[string]string{}
	}
	if depth >= maxMIMEDepth {
		return p
	}
	switch {
	case strings.HasPrefix(p.Type, "multipart/"):
		if b := params["boundary"]; b != "" {
			for _, chunk := range splitMultipart(body, b) {
				p.Children = append(p.Children, parseMIME(chunk, depth+1))
			}
		}
	case p.Type == "message/rfc822":
		p.Children = []*mimePart{parseMIME(body, depth+1)}
	}
	return p
}

func splitHeaderBody(raw []byte) (textproto.MIMEHeader, []byte) {
	// Header ends at the first blank line (CRLFCRLF or LFLF).
	idx, sepLen := -1, 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			if i+1 < len(raw) && raw[i+1] == '\n' {
				idx, sepLen = i, 2
				break
			}
			if i+2 < len(raw) && raw[i+1] == '\r' && raw[i+2] == '\n' {
				idx, sepLen = i, 3
				break
			}
		}
	}
	// A message that starts with a blank line has no headers.
	if len(raw) > 0 && raw[0] == '\n' {
		return textproto.MIMEHeader{}, raw[1:]
	}
	if len(raw) > 1 && raw[0] == '\r' && raw[1] == '\n' {
		return textproto.MIMEHeader{}, raw[2:]
	}
	headBytes, body := raw, []byte(nil)
	if idx >= 0 {
		headBytes, body = raw[:idx+1], raw[idx+sepLen:]
	}
	return parseHeaderBlock(headBytes), body
}

// parseHeaderBlock is a lenient header parser (no error on bad lines).
func parseHeaderBlock(b []byte) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var key, val string
	flush := func() {
		if key != "" {
			k := textproto.CanonicalMIMEHeaderKey(key)
			h[k] = append(h[k], strings.TrimLeft(val, " \t"))
		}
		key, val = "", ""
	}
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if (ln[0] == ' ' || ln[0] == '\t') && key != "" {
			val += "\n" + ln
			continue
		}
		flush()
		k, v, ok := strings.Cut(ln, ":")
		if !ok || strings.ContainsAny(k, " \t") && strings.TrimSpace(k) != k && false {
			continue
		}
		key, val = strings.TrimSpace(k), v
	}
	flush()
	return h
}

// splitMultipart returns the raw parts between boundary delimiters; a missing
// closing delimiter is tolerated (the rest is the last part).
func splitMultipart(body []byte, boundary string) [][]byte {
	delim := []byte("--" + boundary)
	var parts [][]byte
	pos := 0
	started := false
	var cur int
	for pos <= len(body) {
		// Find the next delimiter at a line start.
		i := indexLineStart(body, delim, pos)
		if i < 0 {
			if started {
				parts = append(parts, trimPartEnd(body[cur:]))
			}
			break
		}
		lineEnd := bytes.IndexByte(body[i:], '\n')
		next := len(body)
		if lineEnd >= 0 {
			next = i + lineEnd + 1
		}
		if started {
			parts = append(parts, trimPartEnd(body[cur:i]))
		}
		tail := body[i+len(delim):]
		if bytes.HasPrefix(tail, []byte("--")) {
			break
		}
		started = true
		cur = next
		pos = next
	}
	return parts
}

func trimPartEnd(b []byte) []byte {
	// The line break before a delimiter belongs to the delimiter.
	if bytes.HasSuffix(b, []byte("\r\n")) {
		return b[:len(b)-2]
	}
	if bytes.HasSuffix(b, []byte("\n")) {
		return b[:len(b)-1]
	}
	return b
}

func indexLineStart(body, delim []byte, from int) int {
	for from <= len(body) {
		i := bytes.Index(body[from:], delim)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || body[i-1] == '\n' {
			return i
		}
		from = i + 1
	}
	return -1
}

// ---- payload decoding -----------------------------------------------------------

func (p *mimePart) cte() string {
	return strings.ToLower(strings.TrimSpace(p.get("Content-Transfer-Encoding")))
}

// decoded returns the transfer-decoded payload (get_payload(decode=True)).
func (p *mimePart) decoded() []byte {
	return decodeTransfer(p.Body, p.cte())
}

func decodeTransfer(body []byte, cte string) []byte {
	switch cte {
	case "base64":
		clean := make([]byte, 0, len(body))
		for _, c := range body {
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' {
				clean = append(clean, c)
			}
		}
		for len(clean)%4 != 0 {
			if len(clean)%4 == 1 {
				clean = clean[:len(clean)-1]
				continue
			}
			clean = append(clean, '=')
		}
		out, err := base64.StdEncoding.DecodeString(string(clean))
		if err != nil {
			return nil
		}
		return out
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return lenientQP(body)
		}
		return out
	}
	return body
}

func lenientQP(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); i++ {
		if b[i] == '=' {
			if i+1 < len(b) && (b[i+1] == '\n') {
				i++
				continue
			}
			if i+2 < len(b) && b[i+1] == '\r' && b[i+2] == '\n' {
				i += 2
				continue
			}
			if i+2 < len(b) {
				if v, err := strconv.ParseUint(string(b[i+1:i+3]), 16, 8); err == nil {
					out = append(out, byte(v))
					i += 2
					continue
				}
			}
		}
		out = append(out, b[i])
	}
	return out
}

// text decodes the payload to a string with the declared charset
// (_payload_text): unknown charsets and invalid bytes become U+FFFD.
func (p *mimePart) text() string {
	data := p.decoded()
	return decodeCharset(data, p.Params["charset"])
}

func decodeCharset(data []byte, charset string) string {
	if charset != "" {
		if enc, err := lookupCharset(charset); err == nil {
			if s, err := enc.NewDecoder().String(string(data)); err == nil {
				return s
			}
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

// encodedSize is the transfer-encoded wire size (_encoded_size): for
// base64 / quoted-printable the raw body with every LF counted as CRLF, for
// identity encodings the decoded length.
func (p *mimePart) encodedSize() int {
	switch p.cte() {
	case "base64", "quoted-printable":
		n := len(p.Body)
		n += bytes.Count(p.Body, []byte("\n")) - bytes.Count(p.Body, []byte("\r\n"))
		return n
	}
	return len(p.Body)
}

// walk visits the part and all descendants depth first.
func (p *mimePart) walk(fn func(*mimePart)) {
	fn(p)
	for _, c := range p.Children {
		c.walk(fn)
	}
}

// ---- text extraction --------------------------------------------------------------

func rawHTML(root *mimePart) string {
	var parts []string
	root.walk(func(p *mimePart) {
		if p.isMultipart() || p.isAttachment() {
			return
		}
		if p.Type == "text/html" {
			parts = append(parts, p.text())
		}
	})
	return strings.Join(parts, "\n")
}

func extractText(root *mimePart) string {
	var plain, htmls []string
	root.walk(func(p *mimePart) {
		if p.isMultipart() || p.isAttachment() {
			return
		}
		switch p.Type {
		case "text/plain":
			plain = append(plain, strings.TrimSpace(p.text()))
		case "text/html":
			htmls = append(htmls, p.text())
		}
	})
	return joinText(plain, htmls)
}

func joinText(plain, htmls []string) string {
	var keep []string
	for _, s := range plain {
		if s != "" {
			keep = append(keep, s)
		}
	}
	if text := strings.Join(keep, "\n"); text != "" {
		return text
	}
	keep = keep[:0]
	for _, h := range htmls {
		if c := htmlToText(h); c != "" {
			keep = append(keep, c)
		}
	}
	return strings.Join(keep, "\n")
}

var textBlockTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`address article aside blockquote br dd div dl dt fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 header hr li main nav ol p pre section table tbody td tfoot th thead tr ul`) {
		textBlockTags[t] = true
	}
}

var spaceRunRE = regexp.MustCompile(`[ \t\f\v]+`)

// htmlToText converts an HTML fragment to plain text (html_to_text).
func htmlToText(src string) string {
	if src == "" {
		return ""
	}
	var parts []string
	skip := 0
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			switch {
			case tag == "script" || tag == "style" || tag == "head":
				skip++
			case skip > 0:
			case tag == "li":
				parts = append(parts, "\n- ")
			case textBlockTags[tag]:
				parts = append(parts, "\n")
			}
		case html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if tag == "script" || tag == "style" || tag == "head" {
				continue
			}
			if skip == 0 && textBlockTags[tag] {
				parts = append(parts, "\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			switch {
			case tag == "script" || tag == "style" || tag == "head":
				if skip > 0 {
					skip--
				}
			case skip > 0:
			case textBlockTags[tag]:
				parts = append(parts, "\n")
			}
		case html.TextToken:
			if skip == 0 {
				parts = append(parts, string(z.Text()))
			}
		}
	}
	raw := strings.Join(parts, "")
	var out []string
	blank := 0
	for _, line := range pySplitLines(raw) {
		line = strings.TrimSpace(spaceRunRE.ReplaceAllString(line, " "))
		if line != "" {
			blank = 0
			out = append(out, line)
		} else {
			blank++
			if blank == 1 {
				out = append(out, "")
			}
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// pySplitLines is str.splitlines().
func pySplitLines(s string) []string {
	var lines []string
	rs := []rune(s)
	var cur []rune
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			fallthrough
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, string(cur))
			cur = cur[:0]
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

// ---- attachments ---------------------------------------------------------------

type attachment struct {
	Index       int
	Filename    string
	ContentType string
	Size        int
	ContentID   string
	Inline      bool
	Data        []byte
}

func extractAttachments(root *mimePart) []attachment {
	var atts []attachment
	root.walk(func(p *mimePart) {
		if p.isMultipart() {
			return
		}
		fn := p.filename()
		if fn == "" && p.Disp != "attachment" && (p.get("Content-ID") == "" || p.maintype() == "text") {
			return
		}
		safe := sanitizeFilename(decodeMIMEHeader(fn))
		if safe == "" {
			safe = "attachment-" + strconv.Itoa(len(atts))
		}
		cid := strings.Trim(strings.TrimSpace(decodeMIMEHeader(p.get("Content-ID"))), "<>")
		atts = append(atts, attachment{
			Index:       len(atts),
			Filename:    safe,
			ContentType: p.Type,
			Size:        p.encodedSize(),
			ContentID:   cid,
			Data:        p.decoded(),
		})
	})
	return atts
}

var cidRefRE = regexp.MustCompile(`(?i)cid:([^\s"'<>()]+)`)

// markInlineImages flags image parts that the HTML shows via cid: references.
func markInlineImages(atts []attachment, rawHTML string) {
	refs := map[string]bool{}
	for _, m := range cidRefRE.FindAllStringSubmatch(rawHTML, -1) {
		refs[strings.ToLower(strings.Trim(pyUnquote(m[1]), "<>"))] = true
	}
	for i := range atts {
		cid := strings.ToLower(atts[i].ContentID)
		atts[i].Inline = cid != "" && refs[cid] && strings.HasPrefix(atts[i].ContentType, "image/")
	}
}

// pyUnquote is urllib.parse.unquote (invalid sequences stay, UTF-8 replace).
func pyUnquote(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b []byte
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
	return strings.ToValidUTF8(string(b), "�")
}

// ---- parsed message -----------------------------------------------------------------

type parsedMessage struct {
	From, To, Cc, Subject string
	DateTS                *int64
	MessageID             string
	InReplyTo             string
	References            string
	Text                  string
	HTML                  string
	Attachments           []attachment
}

func (s *Service) parseFull(raw []byte, folder string, uid int64) *parsedMessage {
	root := parseMIME(raw, 0)
	rawH := rawHTML(root)
	atts := extractAttachments(root)
	markInlineImages(atts, rawH)
	var cids map[string]string
	if folder != "" && uid > 0 {
		cids = s.cidImageURLs(atts, folder, uid)
	}
	h := root.Header
	return &parsedMessage{
		From:        decodeMIMEHeader(h.Get("From")),
		To:          decodeMIMEHeader(h.Get("To")),
		Cc:          decodeMIMEHeader(h.Get("Cc")),
		Subject:     decodeMIMEHeader(h.Get("Subject")),
		DateTS:      parseDateTS(h.Get("Date")),
		MessageID:   strings.TrimSpace(decodeMIMEHeader(h.Get("Message-Id"))),
		InReplyTo:   strings.TrimSpace(decodeMIMEHeader(h.Get("In-Reply-To"))),
		References:  strings.TrimSpace(decodeMIMEHeader(h.Get("References"))),
		Text:        extractText(root),
		HTML:        s.sanitizeHTML(rawH, cids),
		Attachments: atts,
	}
}

type summary struct {
	From, To, Subject string
	DateTS            *int64
	HasAttachments    bool
}

func hasAttachmentHint(root *mimePart) bool {
	ct := strings.ToLower(strings.TrimSpace(root.get("Content-Type")))
	return strings.HasPrefix(ct, "multipart/mixed") || strings.HasPrefix(ct, "multipart/related") ||
		root.filename() != "" || root.Disp == "attachment"
}

func parseSummary(raw []byte) summary {
	root := parseMIME(raw, maxMIMEDepth) // headers only; never walk bodies
	h := root.Header
	return summary{
		From:           decodeMIMEHeader(h.Get("From")),
		To:             decodeMIMEHeader(h.Get("To")),
		Subject:        decodeMIMEHeader(h.Get("Subject")),
		DateTS:         parseDateTS(h.Get("Date")),
		HasAttachments: hasAttachmentHint(root),
	}
}

// ---- addresses --------------------------------------------------------------------

type addrPair struct{ Name, Email string }

var (
	angleRE = regexp.MustCompile(`<([^<>]*)>`)
)

// getAddresses is email.utils.getaddresses, lenient.
func getAddresses(values []string) []addrPair {
	var out []addrPair
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if list, err := mail.ParseAddressList(v); err == nil {
			for _, a := range list {
				out = append(out, addrPair{a.Name, a.Address})
			}
			continue
		}
		for _, piece := range splitTopLevelCommas(v) {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			if a, err := mail.ParseAddress(piece); err == nil {
				out = append(out, addrPair{a.Name, a.Address})
				continue
			}
			if m := angleRE.FindStringSubmatch(piece); m != nil {
				name := strings.TrimSpace(strings.Trim(strings.TrimSpace(piece[:strings.Index(piece, "<")]), `"`))
				out = append(out, addrPair{name, strings.TrimSpace(m[1])})
				continue
			}
			out = append(out, addrPair{"", piece})
		}
	}
	return out
}

func splitTopLevelCommas(s string) []string {
	var parts []string
	depth, inQ, esc := 0, false, false
	start := 0
	for i, r := range s {
		switch {
		case esc:
			esc = false
		case r == '\\' && inQ:
			esc = true
		case r == '"':
			inQ = !inQ
		case !inQ && r == '(':
			depth++
		case !inQ && r == ')' && depth > 0:
			depth--
		case !inQ && depth == 0 && r == ',':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// ---- filenames ---------------------------------------------------------------------

var (
	fnBadRE   = regexp.MustCompile(`[^\p{L}\p{N}_ .()\-]`)
	fnSpaceRE = regexp.MustCompile(`\s+`)
)

// sanitizeFilename is filename_utils.sanitize_filename (max 255 chars).
func sanitizeFilename(name string) string {
	n := strings.ReplaceAll(nfkc(name), "\\", "/")
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	s := fnBadRE.ReplaceAllString(n, "_")
	s = fnSpaceRE.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .")
	if utf8.RuneCountInString(s) <= 255 {
		return s
	}
	suffix := ""
	if i := strings.LastIndex(s, "."); i > 0 {
		suffix = s[i:]
		if strings.Trim(suffix, ".") == "" || len(suffix) > 255 {
			suffix = ""
		}
	}
	r := []rune(s)
	cut := strings.TrimRight(string(r[:255-utf8.RuneCountInString(suffix)]), " .")
	return cut + suffix
}
