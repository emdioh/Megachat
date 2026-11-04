package gmail

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Port of _pre_scan_html and helpers: a linear, quote-aware scan with
// complexity caps, image/background deferral and quote markers.

const (
	maxHTMLChars       = 1_000_000
	htmlMaxTags        = 8_000
	htmlMaxStyleAttrs  = 2_000
	htmlMaxStyleDecls  = 12_800
	htmlMaxTextChars   = 1_000_000
	htmlMaxOutputChars = 1_000_000
	htmlMaxDepth       = 128
	htmlMaxAttrsPerTag = 256
	htmlMaxAttrLen     = 16_384
	attrValueMaxLen    = 2_048
	imageURLMaxLen     = 4_096
)

type scannedAttr struct {
	Name, Lower string
	Value       *string
	Quote       string
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func findTagEnd(raw string, start int) int {
	var quote byte
	for i := start; i < len(raw); i++ {
		c := raw[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return len(raw)
}

// nameRuneAt reports whether the rune at i is str.isalnum() or ':' / '-'.
func nameRuneAt(s string, i int) (bool, int) {
	r, n := utf8.DecodeRuneInString(s[i:])
	if r == ':' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsNumber(r) {
		return true, n
	}
	return false, n
}

func parseTag(raw string, start int) (name string, attrs []scannedAttr, end int, selfClosing bool, ok bool) {
	length := len(raw)
	idx := start
	nameStart := idx
	for idx < length {
		if okc, n := nameRuneAt(raw, idx); okc {
			idx += n
		} else {
			break
		}
	}
	if idx == nameStart {
		return
	}
	name = strings.ToLower(raw[nameStart:idx])
	for idx < length {
		for idx < length && isSpaceByte(raw[idx]) {
			idx++
		}
		if idx >= length {
			return "", nil, 0, false, false
		}
		if raw[idx] == '>' {
			return name, attrs, idx + 1, selfClosing, true
		}
		if raw[idx] == '/' && idx+1 < length && raw[idx+1] == '>' {
			return name, attrs, idx + 2, true, true
		}
		attrStart := idx
		for idx < length && !isSpaceByte(raw[idx]) && raw[idx] != '/' && raw[idx] != '>' && raw[idx] != '=' {
			idx++
		}
		if idx == attrStart {
			idx++
			continue
		}
		attrName := raw[attrStart:idx]
		for idx < length && isSpaceByte(raw[idx]) {
			idx++
		}
		var value *string
		quote := ""
		if idx < length && raw[idx] == '=' {
			idx++
			for idx < length && isSpaceByte(raw[idx]) {
				idx++
			}
			if idx < length && (raw[idx] == '"' || raw[idx] == '\'') {
				quote = string(raw[idx])
				idx++
				vs := idx
				ve := strings.Index(raw[idx:], quote)
				if ve < 0 {
					return "", nil, 0, false, false
				}
				v := raw[vs : idx+ve]
				value = &v
				idx = idx + ve + 1
			} else {
				vs := idx
				for idx < length && !isSpaceByte(raw[idx]) && raw[idx] != '>' {
					idx++
				}
				v := raw[vs:idx]
				value = &v
			}
		}
		attrs = append(attrs, scannedAttr{Name: attrName, Lower: strings.ToLower(attrName), Value: value, Quote: quote})
	}
	return "", nil, 0, false, false
}

func findRawTextClose(raw string, start int, name string) int {
	idx := start
	length := len(raw)
	for {
		i := strings.Index(raw[idx:], "</")
		if i < 0 {
			return -1
		}
		idx += i
		ns := idx + 2
		ne := ns
		for ne < length {
			if okc, n := nameRuneAt(raw, ne); okc {
				ne += n
			} else {
				break
			}
		}
		if strings.ToLower(raw[ns:ne]) == name && (ne >= length || strings.IndexByte(" \t\n\r\f/>", raw[ne]) >= 0) {
			return idx
		}
		idx += 2
	}
}

func attrVal(a scannedAttr) string {
	if a.Value == nil {
		return ""
	}
	return *a.Value
}

func renderImgTag(attrs []scannedAttr, selfClosing bool) string {
	hasDataSrc := false
	for _, a := range attrs {
		if a.Lower == "data-src" {
			hasDataSrc = true
		}
	}
	var b strings.Builder
	b.WriteString("<img")
	for _, a := range attrs {
		name := a.Name
		if a.Lower == "src" && !hasDataSrc {
			name = "data-src"
		}
		quote := a.Quote
		var value *string
		if a.Value != nil {
			v := *a.Value
			if quote == "" {
				v = strings.ReplaceAll(v, `"`, "&quot;")
				quote = `"`
			}
			value = &v
		}
		b.WriteByte(' ')
		b.WriteString(name)
		if value != nil {
			b.WriteString("=" + quote + *value + quote)
		}
	}
	if selfClosing {
		b.WriteString("/>")
	} else {
		b.WriteString(">")
	}
	return b.String()
}

var bodyColorRE = regexp.MustCompile(`^(?:#[0-9a-fA-F]{3}(?:[0-9a-fA-F]{3})?|[A-Za-z]{1,20})$`)

// styleDeclarationCount is an upper bound of the declarations in a style value.
func styleDeclarationCount(v string) int {
	n := 0
	for _, part := range strings.Split(html.UnescapeString(v), ";") {
		if strings.TrimFunc(part, unicode.IsSpace) != "" {
			n++
		}
	}
	return n
}

func renderBodyTag(attrs []scannedAttr) (tag string, addedStyle bool, decls int) {
	var declarations []string
	existing := ""
	for _, a := range attrs {
		value := strings.TrimSpace(attrVal(a))
		switch {
		case a.Lower == "style":
			existing = strings.ReplaceAll(value, `"`, "&quot;")
		case (a.Lower == "bgcolor" || a.Lower == "text") && bodyColorRE.MatchString(value):
			prop := "color"
			if a.Lower == "bgcolor" {
				prop = "background-color"
			}
			declarations = append(declarations, prop+":"+value)
		}
	}
	if existing != "" {
		declarations = append(declarations, existing)
	}
	if len(declarations) == 0 {
		return "<div>", false, 0
	}
	style := strings.Join(declarations, ";")
	return `<div style="` + style + `">`, existing == "", styleDeclarationCount(style)
}

var (
	styleBackgroundURLRE = regexp.MustCompile(`(?i)background(?:-image)?\s*:[^;]*?url\(\s*(?:'(https?://[^'")\s]+)'|"(https?://[^'")\s]+)"|(https?://[^'")\s]+))\s*\)`)
	safeBackgroundURLRE  = regexp.MustCompile(`(?i)^https?://[^\s"'<>()]+$`)
	backgroundTags       = map[string]bool{"table": true, "td": true, "th": true, "div": true}
)

func backgroundURL(name string, attrs []scannedAttr) (string, bool) {
	if !backgroundTags[name] {
		return "", false
	}
	values := map[string]string{}
	for _, a := range attrs {
		values[a.Lower] = strings.TrimSpace(attrVal(a))
	}
	if _, has := values["data-bg"]; has {
		return "", false
	}
	cand := values["background"]
	if cand == "" {
		if m := styleBackgroundURLRE.FindStringSubmatch(values["style"]); m != nil {
			cand = m[1] + m[2] + m[3]
		}
	}
	if cand == "" || utf8.RuneCountInString(cand) > imageURLMaxLen {
		return "", false
	}
	if safeBackgroundURLRE.MatchString(cand) {
		return cand, true
	}
	return "", false
}

func quoteMarker(name string, attrs []scannedAttr) (string, bool) {
	if name != "div" && name != "blockquote" {
		return "", false
	}
	values := map[string]string{}
	for _, a := range attrs {
		values[a.Lower] = strings.TrimSpace(attrVal(a))
	}
	if _, has := values["data-quote"]; has {
		return "", false
	}
	if name == "blockquote" {
		if strings.ToLower(values["type"]) == "cite" {
			return "block", true
		}
		return "", false
	}
	for _, c := range strings.Fields(values["class"]) {
		if c == "gmail_quote" || c == "yahoo_quoted" {
			return "block", true
		}
	}
	switch strings.ToLower(values["id"]) {
	case "divrplyfwdmsg", "appendonsend":
		return "after", true
	}
	return "", false
}

var (
	voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
		"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}
	optionalEndTags = map[string]bool{"caption": true, "colgroup": true, "dd": true, "dt": true, "li": true,
		"optgroup": true, "option": true, "p": true, "rp": true, "rt": true, "tbody": true, "td": true,
		"tfoot": true, "th": true, "thead": true, "tr": true}
	rawTextTags = map[string]bool{"script": true, "style": true, "textarea": true, "title": true}
)

// preScanHTML returns "" when a cap is exceeded (fail closed). styles, when
// non-nil, collects the contents of <style> elements.
func preScanHTML(raw string, styles *[]string) string {
	length := len(raw)
	var out strings.Builder
	idx := 0
	tagCount, styleCount, styleDecls, textChars, depth := 0, 0, 0, 0, 0
	addText := func(n int) bool {
		textChars += n
		return textChars <= htmlMaxTextChars
	}
	for idx < length {
		if raw[idx] != '<' {
			end := strings.IndexByte(raw[idx:], '<')
			if end < 0 {
				end = length
			} else {
				end += idx
			}
			if !addText(utf8.RuneCountInString(raw[idx:end])) {
				return ""
			}
			out.WriteString(raw[idx:end])
			idx = end
			continue
		}
		if strings.HasPrefix(raw[idx:], "<!--") {
			end := strings.Index(raw[idx+4:], "-->")
			if end < 0 {
				end = length
			} else {
				end = idx + 4 + end + 3
			}
			out.WriteString(raw[idx:end])
			idx = end
			continue
		}
		if strings.HasPrefix(raw[idx:], "<![CDATA[") {
			end := strings.Index(raw[idx+9:], "]]>")
			if end < 0 {
				end = length
			} else {
				end = idx + 9 + end + 3
			}
			n := utf8.RuneCountInString(raw[idx:end]) - 12
			if n < 0 {
				n = 0
			}
			if !addText(n) {
				return ""
			}
			out.WriteString(raw[idx:end])
			idx = end
			continue
		}
		if strings.HasPrefix(raw[idx:], "<!") || strings.HasPrefix(raw[idx:], "<?") {
			end := findTagEnd(raw, idx+2)
			out.WriteString(raw[idx:end])
			idx = end
			continue
		}
		closing := strings.HasPrefix(raw[idx:], "</")
		off := 1
		if closing {
			off = 2
		}
		name, attrs, end, selfClosing, ok := parseTag(raw, idx+off)
		if !ok {
			if !addText(1) {
				return ""
			}
			out.WriteByte('<')
			idx++
			continue
		}
		if !closing {
			if len(attrs) > htmlMaxAttrsPerTag {
				return ""
			}
			for _, a := range attrs {
				al := utf8.RuneCountInString(a.Name)
				if a.Value != nil {
					al += 1 + utf8.RuneCountInString(*a.Value)
				}
				if al > htmlMaxAttrLen {
					return ""
				}
				if a.Lower == "style" {
					styleCount++
					if styleCount > htmlMaxStyleAttrs {
						return ""
					}
					if name != "body" {
						styleDecls += styleDeclarationCount(attrVal(a))
						if styleDecls > htmlMaxStyleDecls {
							return ""
						}
					}
				}
			}
			if name != "tbody" {
				tagCount++
				if tagCount > htmlMaxTags {
					return ""
				}
				if !voidTags[name] && !optionalEndTags[name] {
					depth++
					if depth > htmlMaxDepth {
						return ""
					}
				}
			}
			cut := min(1+len(name), end-idx)
			switch {
			case name == "img":
				hasDataSrc, hasSrc := false, false
				for _, a := range attrs {
					if a.Lower == "data-src" {
						hasDataSrc = true
					}
					if a.Lower == "src" {
						hasSrc = true
					}
				}
				if hasSrc && !hasDataSrc {
					out.WriteString(renderImgTag(attrs, selfClosing))
				} else {
					out.WriteString(raw[idx:end])
				}
			case func() bool { _, ok := quoteMarker(name, attrs); return ok }():
				marker, _ := quoteMarker(name, attrs)
				tag := raw[idx:end]
				out.WriteString(tag[:cut] + ` data-quote="` + marker + `"` + tag[cut:])
			case name == "body":
				bodyTag, added, decls := renderBodyTag(attrs)
				if added {
					styleCount++
					if styleCount > htmlMaxStyleAttrs {
						return ""
					}
				}
				styleDecls += decls
				if styleDecls > htmlMaxStyleDecls {
					return ""
				}
				out.WriteString(bodyTag)
			case func() bool { _, ok := backgroundURL(name, attrs); return ok }():
				bg, _ := backgroundURL(name, attrs)
				tag := raw[idx:end]
				out.WriteString(tag[:cut] + ` data-bg="` + bg + `"` + tag[cut:])
			default:
				out.WriteString(raw[idx:end])
			}
			idx = end
			if rawTextTags[name] {
				closeStart := findRawTextClose(raw, idx, name)
				if closeStart < 0 {
					if !addText(utf8.RuneCountInString(raw[idx:])) {
						return ""
					}
					out.WriteString(raw[idx:])
					idx = length
				} else {
					if !addText(utf8.RuneCountInString(raw[idx:closeStart])) {
						return ""
					}
					if name == "style" && styles != nil {
						*styles = append(*styles, raw[idx:closeStart])
					}
					out.WriteString(raw[idx:closeStart])
					_, _, closeEnd, _, cok := parseTag(raw, closeStart+2)
					if !cok {
						idx = closeStart
					} else {
						out.WriteString(raw[closeStart:closeEnd])
						if !optionalEndTags[name] && depth > 0 {
							depth--
						}
						idx = closeEnd
					}
				}
			}
			continue
		}
		if name == "body" {
			out.WriteString("</div>")
		} else {
			out.WriteString(raw[idx:end])
		}
		if !voidTags[name] && !optionalEndTags[name] && depth > 0 {
			depth--
		}
		idx = end
	}
	return out.String()
}
