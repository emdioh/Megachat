package gmail

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML sanitising: the Go counterpart of sanitize_html. nh3/ammonia is
// replaced by an x/net/html DOM pass (attribute filter, clean-content tags,
// comments) + a bluemonday policy (tag / attribute / URL-scheme allow-lists)
// + a final serialiser that reproduces ammonia's escaping and link rel.

var allowedHTMLTags = setOf("a abbr b blockquote br caption center cite code col colgroup dd del div dl dt em figcaption figure font h1 h2 h3 h4 h5 h6 hr i img ins li mark ol p pre q s small span strike strong sub sup table tbody td tfoot th thead tr u ul")

var cleanContentTags = setOf("base embed form head iframe link math meta noscript object script style svg template title")

var allowedHTMLAttributes = map[string][]string{
	"*":          {"dir", "style"},
	"a":          {"href", "title"},
	"blockquote": {"data-quote"},
	"font":       {"color", "face", "size"},
	"col":        {"align", "bgcolor", "span", "valign", "width"},
	"colgroup":   {"align", "bgcolor", "span", "valign", "width"},
	"div":        {"data-bg", "data-quote"},
	"img":        {"align", "alt", "data-src", "height", "width"},
	"li":         {"value"},
	"ol":         {"start"},
	"table":      {"align", "bgcolor", "data-bg", "border", "cellpadding", "cellspacing", "height", "width"},
	"td":         {"align", "bgcolor", "data-bg", "colspan", "height", "rowspan", "valign", "width"},
	"th":         {"align", "bgcolor", "data-bg", "colspan", "height", "rowspan", "scope", "valign", "width"},
	"tr":         {"align", "bgcolor", "height", "valign"},
}

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func attrAllowed(tag, attr string) bool {
	for _, a := range allowedHTMLAttributes["*"] {
		if a == attr {
			return true
		}
	}
	for _, a := range allowedHTMLAttributes[tag] {
		if a == attr {
			return true
		}
	}
	return false
}

// relCarrier stands for the rel="noopener noreferrer" of a link while the
// attributes go through bluemonday, so that it keeps its place among them.
const relCarrier = "data-mc-rel"

var (
	policyOnce sync.Once
	htmlPolicy *bluemonday.Policy
)

func policy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.NewPolicy()
		var tags []string
		for t := range allowedHTMLTags {
			tags = append(tags, t)
		}
		p.AllowElements(tags...)
		p.AllowNoAttrs().OnElements(tags...)
		p.AllowAttrs(allowedHTMLAttributes["*"]...).Globally()
		for tag, attrs := range allowedHTMLAttributes {
			if tag != "*" {
				p.AllowAttrs(attrs...).OnElements(tag)
			}
		}
		p.AllowAttrs(relCarrier).OnElements("a")
		p.AllowURLSchemes("http", "https", "mailto")
		p.AllowRelativeURLs(false)
		p.RequireParseableURLs(true)
		var skip []string
		for t := range cleanContentTags {
			skip = append(skip, t)
		}
		p.SkipElementsContent(skip...)
		htmlPolicy = p
	})
	return htmlPolicy
}

// ---- legacy attribute validators ---------------------------------------------------

var (
	intRE       = regexp.MustCompile(`^-?[0-9]+$`)
	pctRE       = regexp.MustCompile(`^([0-9]+)%$`)
	hexBgRE     = regexp.MustCompile(`^#[0-9a-fA-F]{3}(?:[0-9a-fA-F]{3})?$`)
	fontFaceRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,63}$`)
	fontSizeRE  = regexp.MustCompile(`^[+-]?[1-7]$`)
	pyStripChar = " \t\n\r\f\v\x1c\x1d\x1e\x1f\x85                 　"
)

func pyStrip(s string) string { return strings.Trim(s, pyStripChar) }

type attrValidator func(string) (string, bool)

func legacyInt(value string, min, max int) (string, bool) {
	value = pyStrip(value)
	if !intRE.MatchString(value) {
		return "", false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return "", false
	}
	return strconv.Itoa(n), true
}

func legacySize(value string, max int) (string, bool) {
	value = pyStrip(value)
	if m := pctRE.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 0 || n > 100 {
			return "", false
		}
		return strconv.Itoa(n) + "%", true
	}
	return legacyInt(value, 0, max)
}

func legacyBgcolor(value string) (string, bool) {
	value = pyStrip(value)
	if hexBgRE.MatchString(value) {
		return strings.ToLower(value), true
	}
	if _, ok := namedColors[strings.ToLower(value)]; ok {
		return strings.ToLower(value), true
	}
	return "", false
}

func legacyFontFace(value string) (string, bool) {
	fams := strings.Split(value, ",")
	if len(fams) < 1 || len(fams) > 8 {
		return "", false
	}
	for i, f := range fams {
		f = pyStrip(f)
		if !fontFaceRE.MatchString(f) {
			return "", false
		}
		fams[i] = f
	}
	return strings.Join(fams, ", "), true
}

func legacyFontSize(value string) (string, bool) {
	value = pyStrip(value)
	return value, fontSizeRE.MatchString(value)
}

func legacyEnum(choices ...string) attrValidator {
	return func(v string) (string, bool) {
		v = strings.ToLower(pyStrip(v))
		for _, c := range choices {
			if c == v {
				return v, true
			}
		}
		return "", false
	}
}

func intV(min, max int) attrValidator {
	return func(v string) (string, bool) { return legacyInt(v, min, max) }
}
func sizeV(max int) attrValidator {
	return func(v string) (string, bool) { return legacySize(v, max) }
}

var legacyAttrValidators = map[[2]string]attrValidator{}

func init() {
	align := legacyEnum("left", "right", "center", "justify")
	valign := legacyEnum("top", "middle", "bottom", "baseline")
	set := func(tag, attr string, v attrValidator) { legacyAttrValidators[[2]string{tag, attr}] = v }
	set("table", "border", intV(0, 50))
	set("table", "cellpadding", intV(0, 50))
	set("table", "cellspacing", intV(0, 50))
	set("table", "bgcolor", legacyBgcolor)
	set("table", "width", sizeV(10000))
	set("table", "height", sizeV(2000))
	set("table", "align", align)
	set("tr", "bgcolor", legacyBgcolor)
	set("tr", "height", sizeV(2000))
	set("tr", "align", align)
	set("tr", "valign", valign)
	for _, c := range []string{"td", "th"} {
		set(c, "bgcolor", legacyBgcolor)
		set(c, "height", sizeV(2000))
		set(c, "align", align)
		set(c, "valign", valign)
		set(c, "colspan", intV(1, 100))
		set(c, "rowspan", intV(1, 100))
		set(c, "width", sizeV(10000))
	}
	set("th", "scope", legacyEnum("row", "col", "rowgroup", "colgroup"))
	for _, c := range []string{"col", "colgroup"} {
		set(c, "span", intV(1, 100))
		set(c, "width", sizeV(10000))
		set(c, "bgcolor", legacyBgcolor)
		set(c, "align", align)
		set(c, "valign", valign)
	}
	set("ol", "start", intV(-10000, 10000))
	set("li", "value", intV(-10000, 10000))
	set("img", "width", sizeV(10000))
	set("img", "height", sizeV(2000))
	set("img", "align", legacyEnum("left", "right", "top", "middle", "bottom"))
	set("font", "color", legacyBgcolor)
	set("font", "face", legacyFontFace)
	set("font", "size", legacyFontSize)
	set("div", "data-quote", legacyEnum("block", "after"))
	set("blockquote", "data-quote", legacyEnum("block", "after"))
}

var globalAttrValidators = map[string]attrValidator{"dir": legacyEnum("ltr", "rtl", "auto")}

// ---- image src filter ---------------------------------------------------------------

var safeImageDataRE = regexp.MustCompile(`(?i)^data:image/(?:png|jpe?g|gif|webp);base64,`)

func (s *Service) filterImageSrc(value string, cids map[string]string) (string, bool) {
	value = pyStrip(value)
	if len(value) >= 4 && strings.EqualFold(value[:4], "cid:") {
		cid := strings.ToLower(strings.Trim(pyUnquote(value[4:]), "<>"))
		cid = pyStrip(cid)
		v, ok := cids[cid]
		return v, ok
	}
	if m := signedImageRE.FindStringSubmatch(value); m != nil {
		if s.verifyImageToken(m[1]+"."+m[2]) != "" {
			return strings.TrimLeft(value, "/"), true
		}
		return "", false
	}
	if safeImageDataRE.MatchString(value) {
		return value, true
	}
	if utf8.RuneCountInString(value) > imageURLMaxLen {
		return "", false
	}
	low := strings.ToLower(value)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		return s.signImageURL(value), true
	}
	return "", false
}

// htmlAttributeFilter is _html_attribute_filter (nh3 attribute_filter).
func (s *Service) htmlAttributeFilter(tag, attr, value string, styleCache map[string]*string, cids map[string]string) (string, bool) {
	tag, attr = strings.ToLower(tag), strings.ToLower(attr)
	if attr == "style" {
		if !s.cfg.GmailHTMLStyles() {
			return "", false
		}
		if v, ok := styleCache[value]; ok {
			if v == nil {
				return "", false
			}
			return *v, true
		}
		out, ok := sanitizeStyle(value)
		if ok {
			out = ammoniaStyle(out)
			styleCache[value] = &out
		} else {
			styleCache[value] = nil
		}
		return out, ok
	}
	v := legacyAttrValidators[[2]string{tag, attr}]
	if v == nil {
		v = globalAttrValidators[attr]
	}
	if v != nil {
		return v(value)
	}
	if attr == "data-bg" {
		return s.filterImageSrc(value, cids)
	}
	if tag == "img" && attr == "data-src" {
		return s.filterImageSrc(value, cids)
	}
	if utf8.RuneCountInString(value) > attrValueMaxLen {
		return "", false
	}
	return value, true
}

// ---- stylesheet filter ------------------------------------------------------------------

var statefulSelectorRE = regexp.MustCompile(`(?i)::|:(?:hover|focus|focus-within|focus-visible|active|visited|target|checked)\b`)

const (
	inlineCSSMaxChars = 65_536
	inlineCSSMaxRules = 1_000
)

var (
	sheetCacheMu sync.Mutex
	sheetCache   = map[string]string{}
)

// filterStylesheet reduces <style> CSS to plain rules with allow-listed
// declarations (_filter_stylesheet).
func filterStylesheet(css string) string {
	if css == "" || utf8.RuneCountInString(css) > inlineCSSMaxChars {
		return ""
	}
	sheetCacheMu.Lock()
	if v, ok := sheetCache[css]; ok {
		sheetCacheMu.Unlock()
		return v
	}
	sheetCacheMu.Unlock()
	out := filterStylesheetUncached(css)
	sheetCacheMu.Lock()
	if len(sheetCache) >= 32 {
		sheetCache = map[string]string{}
	}
	sheetCache[css] = out
	sheetCacheMu.Unlock()
	return out
}

func filterStylesheetUncached(css string) string {
	toks := lexCSS(css)
	var rendered []string
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t.typ == "whitespace" || t.typ == "literal" && (t.value == "<!--" || t.value == "-->") {
			i++
			continue
		}
		if t.typ == "at-keyword" {
			// consume the at-rule: up to ';' or a {} block
			for i < len(toks) {
				tt := toks[i]
				i++
				if tt.typ == "{} block" || tt.typ == "literal" && tt.value == ";" {
					break
				}
			}
			continue
		}
		// qualified rule: prelude up to a {} block
		start := i
		for i < len(toks) && toks[i].typ != "{} block" {
			i++
		}
		if i >= len(toks) {
			break
		}
		prelude, block := toks[start:i], toks[i]
		i++
		if len(rendered) >= inlineCSSMaxRules {
			break
		}
		var selectors []string
		for _, part := range splitLiteral(prelude, ",") {
			var b strings.Builder
			for _, pt := range part {
				b.WriteString(pt.src)
			}
			sel := strings.TrimSpace(b.String())
			if sel != "" && !statefulSelectorRE.MatchString(sel) {
				selectors = append(selectors, sel)
			}
		}
		if len(selectors) == 0 {
			continue
		}
		var decls []string
		for _, d := range parseDeclarationList(block.args) {
			val := validatorFor(d.lower)
			vt := stripWS(d.value)
			if val == nil || len(vt) == 0 || !tokensAreSafe(vt) {
				continue
			}
			ser, ok := val(vt)
			if !ok {
				continue
			}
			imp := ""
			if d.important {
				imp = " !important"
			}
			decls = append(decls, aliasName(d.lower)+":"+ser+imp)
		}
		if len(decls) > 0 {
			rendered = append(rendered, strings.Join(selectors, ", ")+"{"+strings.Join(decls, ";")+"}")
		}
	}
	return strings.Join(rendered, "\n")
}

// ---- sanitize ---------------------------------------------------------------------------------

// sanitizeHTML returns a conservative, script-free HTML fragment ("" when the
// input is unusable or any cap is exceeded).
func (s *Service) sanitizeHTML(raw string, cids map[string]string) string {
	if raw == "" || utf8.RuneCountInString(raw) > maxHTMLChars || !utf8.ValidString(raw) {
		return ""
	}
	var styles []string
	scanned := preScanHTML(raw, &styles)
	if scanned == "" {
		return ""
	}
	if len(styles) > 0 {
		if inlined := s.scanInlinedHTML(raw, styles); inlined != "" {
			if res := s.cleanScanned(inlined, cids); res != "" {
				return res
			}
		}
	}
	return s.cleanScanned(scanned, cids)
}

// scanInlinedHTML applies the <style> CSS as inline styles and scans again;
// "" when inlining is disabled or fails.
func (s *Service) scanInlinedHTML(raw string, styles []string) string {
	if !s.cfg.GmailHTMLInlineCSS() || !s.cfg.GmailHTMLStyles() {
		return ""
	}
	css := filterStylesheet(strings.Join(styles, "\n"))
	if css == "" {
		return ""
	}
	inlined, err := inlineCSS(raw, css)
	if err != nil || utf8.RuneCountInString(inlined) > maxHTMLChars {
		return ""
	}
	return preScanHTML(inlined, nil)
}

func (s *Service) cleanScanned(scanned string, cids map[string]string) string {
	res, ok := s.cleanScannedErr(scanned, cids)
	if !ok {
		return ""
	}
	return res
}

func (s *Service) cleanScannedErr(scanned string, cids map[string]string) (string, bool) {
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(scanned), ctx)
	if err != nil {
		return "", false
	}
	cache := map[string]*string{}
	var buf bytes.Buffer
	for _, n := range nodes {
		for _, c := range s.filterNode(n, cache, cids) {
			if err := html.Render(&buf, c); err != nil {
				return "", false
			}
		}
	}
	pre := buf.String()
	cleaned := policy().Sanitize(pre)
	result := serializeAmmonia(cleaned, tagAttrOrder(pre))
	if !utf8.ValidString(result) || utf8.RuneCountInString(result) > htmlMaxOutputChars {
		return "", false
	}
	if preScanHTML(result, nil) == "" {
		return "", false
	}
	return result, true
}

// filterNode returns the node(s) that replace n: itself (filtered), its
// filtered children (disallowed tag) or nothing.
func (s *Service) filterNode(n *html.Node, cache map[string]*string, cids map[string]string) []*html.Node {
	switch n.Type {
	case html.TextNode:
		return []*html.Node{n}
	case html.ElementNode:
	default:
		return nil // comments, doctype, processing instructions
	}
	tag := strings.ToLower(n.Data)
	if n.Namespace != "" || cleanContentTags[tag] {
		return nil
	}
	var kids []*html.Node
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		kids = append(kids, s.filterNode(c, cache, cids)...)
		c = next
	}
	if !allowedHTMLTags[tag] {
		for _, k := range kids {
			if k.Parent != nil {
				k.Parent.RemoveChild(k)
			}
		}
		return kids
	}
	// ammonia drops the attributes the attribute filter rejects with
	// Vec::swap_remove, from the last to the first, on the list that already
	// ends with the rel it adds to links: a rejected attribute is replaced by
	// the last one, which is where the attribute order of its output comes from.
	type entry struct {
		attr html.Attribute
		keep bool
	}
	var ents []entry
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" || !attrAllowed(tag, key) {
			continue
		}
		v, ok := s.htmlAttributeFilter(tag, key, a.Val, cache, cids)
		ents = append(ents, entry{html.Attribute{Key: key, Val: v}, ok})
	}
	if tag == "a" {
		ents = append(ents, entry{html.Attribute{Key: relCarrier, Val: "1"}, true})
	}
	for i := len(ents) - 1; i >= 0; i-- {
		if !ents[i].keep {
			last := len(ents) - 1
			ents[i] = ents[last]
			ents = ents[:last]
		}
	}
	attrs := make([]html.Attribute, len(ents))
	for i, e := range ents {
		attrs[i] = e.attr
	}
	n.Attr = attrs
	for _, k := range kids {
		if k.Parent != nil {
			k.Parent.RemoveChild(k)
		}
		n.AppendChild(k)
	}
	return []*html.Node{n}
}

// tagAttrs is the attribute names of one start tag, in document order.
type tagAttrs struct {
	name string
	keys []string
}

// tagAttrOrder lists the start tags of src with their attribute names.
func tagAttrOrder(src string) []tagAttrs {
	var out []tagAttrs
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			t := z.Token()
			ta := tagAttrs{name: t.Data}
			for _, a := range t.Attr {
				ta.keys = append(ta.keys, a.Key)
			}
			out = append(out, ta)
		}
	}
}

// serializeAmmonia re-serialises sanitised HTML the way ammonia/html5ever
// does: void elements without a slash, attributes always double quoted with
// &amp; &nbsp; &quot; escaped, text with &amp; &nbsp; &lt; &gt; escaped and
// rel="noopener noreferrer" on every <a>.
func serializeAmmonia(src string, order []tagAttrs) string {
	var out strings.Builder
	z := html.NewTokenizer(strings.NewReader(src))
	tagIdx := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		t := z.Token()
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			// bluemonday writes attributes sorted by name; ammonia keeps their order.
			if tagIdx < len(order) && order[tagIdx].name == t.Data {
				pos := map[string]int{}
				for i, k := range order[tagIdx].keys {
					if _, ok := pos[k]; !ok {
						pos[k] = i
					}
				}
				sort.SliceStable(t.Attr, func(i, j int) bool { return pos[t.Attr[i].Key] < pos[t.Attr[j].Key] })
			}
			tagIdx++
		}
		switch tt {
		case html.TextToken:
			out.WriteString(escapeText(t.Data))
		case html.StartTagToken, html.SelfClosingTagToken:
			out.WriteString("<" + t.Data)
			hasRel := false
			for _, a := range t.Attr {
				if a.Key == relCarrier {
					hasRel = true
					out.WriteString(` rel="noopener noreferrer"`)
					continue
				}
				out.WriteString(" " + a.Key + `="` + escapeAttr(a.Val) + `"`)
			}
			if t.Data == "a" && !hasRel {
				out.WriteString(` rel="noopener noreferrer"`)
			}
			out.WriteString(">")
		case html.EndTagToken:
			if !voidTags[t.Data] {
				out.WriteString("</" + t.Data + ">")
			}
		}
	}
	return out.String()
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", " ", "&nbsp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", " ", "&nbsp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }
