package gmail

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A compact CSS Syntax Level 3 tokenizer with the token model of tinycss2
// (component values: functions and blocks are nested).

type ctok struct {
	typ   string // whitespace ident function hash string number percentage dimension literal url bad-url bad-string at-keyword "() block" "[] block" "{} block"
	value string // ident/string/hash/literal text, function name, dimension unit is in unit
	lower string
	num   float64
	rep   string // numeric representation
	isInt bool
	unit  string
	args  []ctok // function arguments / block content
	src   string // source text (for selector serialisation)
}

func isNameStart(r rune) bool {
	return r == '_' || r >= 0x80 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
func isNameChar(r rune) bool { return isNameStart(r) || r == '-' || r >= '0' && r <= '9' }
func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
func isCSSWS(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

type cssLexer struct {
	s   string
	pos int
}

func (l *cssLexer) peekRune(off int) (rune, int) {
	if l.pos+off >= len(l.s) {
		return -1, 0
	}
	r, n := utf8.DecodeRuneInString(l.s[l.pos+off:])
	return r, n
}

func (l *cssLexer) validEscapeAt(p int) bool {
	return p+1 < len(l.s) && l.s[p] == '\\' && l.s[p+1] != '\n'
}

// consumeEscape consumes after the backslash.
func (l *cssLexer) consumeEscape() rune {
	if l.pos >= len(l.s) {
		return 0xFFFD
	}
	c := l.s[l.pos]
	if isHex(c) {
		start := l.pos
		for l.pos < len(l.s) && l.pos-start < 6 && isHex(l.s[l.pos]) {
			l.pos++
		}
		v, _ := strconv.ParseUint(l.s[start:l.pos], 16, 32)
		if l.pos < len(l.s) && isCSSWS(l.s[l.pos]) {
			if l.s[l.pos] == '\r' && l.pos+1 < len(l.s) && l.s[l.pos+1] == '\n' {
				l.pos++
			}
			l.pos++
		}
		if v == 0 || v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
			return 0xFFFD
		}
		return rune(v)
	}
	r, n := utf8.DecodeRuneInString(l.s[l.pos:])
	l.pos += n
	return r
}

func (l *cssLexer) startsIdent(p int) bool {
	if p >= len(l.s) {
		return false
	}
	c := l.s[p]
	switch {
	case c == '-':
		if p+1 >= len(l.s) {
			return false
		}
		n := l.s[p+1]
		if n == '-' {
			return true
		}
		r, _ := utf8.DecodeRuneInString(l.s[p+1:])
		return isNameStart(r) || l.validEscapeAt(p+1)
	case c == '\\':
		return l.validEscapeAt(p)
	}
	r, _ := utf8.DecodeRuneInString(l.s[p:])
	return isNameStart(r)
}

func (l *cssLexer) consumeName() string {
	var b strings.Builder
	for l.pos < len(l.s) {
		r, n := utf8.DecodeRuneInString(l.s[l.pos:])
		switch {
		case isNameChar(r):
			b.WriteRune(r)
			l.pos += n
		case r == '\\' && l.validEscapeAt(l.pos):
			l.pos++
			b.WriteRune(l.consumeEscape())
		default:
			return b.String()
		}
	}
	return b.String()
}

func (l *cssLexer) startsNumber(p int) bool {
	if p >= len(l.s) {
		return false
	}
	c := l.s[p]
	if c == '+' || c == '-' {
		p++
		if p >= len(l.s) {
			return false
		}
		c = l.s[p]
	}
	if c >= '0' && c <= '9' {
		return true
	}
	return c == '.' && p+1 < len(l.s) && l.s[p+1] >= '0' && l.s[p+1] <= '9'
}

func (l *cssLexer) consumeNumber() (rep string, val float64, isInt bool) {
	start := l.pos
	isInt = true
	if l.pos < len(l.s) && (l.s[l.pos] == '+' || l.s[l.pos] == '-') {
		l.pos++
	}
	digits := func() {
		for l.pos < len(l.s) && l.s[l.pos] >= '0' && l.s[l.pos] <= '9' {
			l.pos++
		}
	}
	digits()
	if l.pos+1 < len(l.s) && l.s[l.pos] == '.' && l.s[l.pos+1] >= '0' && l.s[l.pos+1] <= '9' {
		isInt = false
		l.pos++
		digits()
	}
	if l.pos < len(l.s) && (l.s[l.pos] == 'e' || l.s[l.pos] == 'E') {
		p := l.pos + 1
		if p < len(l.s) && (l.s[p] == '+' || l.s[p] == '-') {
			p++
		}
		if p < len(l.s) && l.s[p] >= '0' && l.s[p] <= '9' {
			isInt = false
			l.pos = p
			digits()
		}
	}
	rep = l.s[start:l.pos]
	val, _ = strconv.ParseFloat(rep, 64) // ErrRange keeps +-Inf like float()
	if math.IsNaN(val) {
		val = 0
	}
	return
}

// tokens lexes until EOF (closer==0) or the matching closer byte.
func (l *cssLexer) tokens(closer byte) []ctok {
	var out []ctok
	for l.pos < len(l.s) {
		start := l.pos
		c := l.s[l.pos]
		if closer != 0 && c == closer {
			l.pos++
			return out
		}
		switch {
		case isCSSWS(c):
			for l.pos < len(l.s) && isCSSWS(l.s[l.pos]) {
				l.pos++
			}
			out = append(out, ctok{typ: "whitespace", value: " ", src: l.s[start:l.pos]})
		case c == '/' && strings.HasPrefix(l.s[l.pos:], "/*"):
			end := strings.Index(l.s[l.pos+2:], "*/")
			if end < 0 {
				l.pos = len(l.s)
			} else {
				l.pos += 2 + end + 2
			}
		case c == '<' && strings.HasPrefix(l.s[l.pos:], "<!--"), c == '-' && strings.HasPrefix(l.s[l.pos:], "-->"):
			n := 3 // CDO / CDC are single literal tokens
			if c == '<' {
				n = 4
			}
			l.pos += n
			out = append(out, ctok{typ: "literal", value: l.s[start:l.pos], src: l.s[start:l.pos]})
		case c == '"' || c == '\'':
			out = append(out, l.consumeString(c))
		case c == '#':
			l.pos++
			r, _ := l.peekRune(0)
			if l.pos < len(l.s) && (isNameChar(r) || l.validEscapeAt(l.pos)) {
				name := l.consumeName()
				out = append(out, ctok{typ: "hash", value: name, lower: strings.ToLower(name), src: l.s[start:l.pos]})
			} else {
				out = append(out, ctok{typ: "literal", value: "#", src: "#"})
			}
		case c >= '0' && c <= '9' || (c == '+' || c == '-' || c == '.') && l.startsNumber(l.pos):
			rep, val, isInt := l.consumeNumber()
			t := ctok{rep: rep, num: val, isInt: isInt}
			switch {
			case l.pos < len(l.s) && l.s[l.pos] == '%':
				l.pos++
				t.typ = "percentage"
			case l.startsIdent(l.pos):
				t.typ = "dimension"
				t.unit = l.consumeName()
			default:
				t.typ = "number"
			}
			t.src = l.s[start:l.pos]
			out = append(out, t)
		case l.startsIdent(l.pos):
			name := l.consumeName()
			if l.pos < len(l.s) && l.s[l.pos] == '(' {
				l.pos++
				lname := strings.ToLower(name)
				if lname == "url" {
					// unquoted url( ... ) is a url token; quoted is a function
					p := l.pos
					for p < len(l.s) && isCSSWS(l.s[p]) {
						p++
					}
					if p >= len(l.s) || (l.s[p] != '"' && l.s[p] != '\'') {
						out = append(out, l.consumeURL(start))
						continue
					}
				}
				args := l.tokens(')')
				out = append(out, ctok{typ: "function", value: name, lower: lname, args: args, src: l.s[start:l.pos]})
			} else {
				out = append(out, ctok{typ: "ident", value: name, lower: strings.ToLower(name), src: l.s[start:l.pos]})
			}
		case c == '@':
			l.pos++
			if l.startsIdent(l.pos) {
				name := l.consumeName()
				out = append(out, ctok{typ: "at-keyword", value: name, lower: strings.ToLower(name), src: l.s[start:l.pos]})
			} else {
				out = append(out, ctok{typ: "literal", value: "@", src: "@"})
			}
		case c == '(' || c == '[' || c == '{':
			l.pos++
			cl := map[byte]byte{'(': ')', '[': ']', '{': '}'}[c]
			args := l.tokens(cl)
			typ := map[byte]string{'(': "() block", '[': "[] block", '{': "{} block"}[c]
			out = append(out, ctok{typ: typ, args: args, src: l.s[start:l.pos]})
		default:
			r, n := utf8.DecodeRuneInString(l.s[l.pos:])
			l.pos += n
			out = append(out, ctok{typ: "literal", value: string(r), src: l.s[start:l.pos]})
		}
	}
	return out
}

func (l *cssLexer) consumeString(q byte) ctok {
	start := l.pos
	l.pos++
	var b strings.Builder
	for l.pos < len(l.s) {
		c := l.s[l.pos]
		switch {
		case c == q:
			l.pos++
			return ctok{typ: "string", value: b.String(), src: l.s[start:l.pos]}
		case c == '\n':
			return ctok{typ: "bad-string", src: l.s[start:l.pos]}
		case c == '\\':
			if l.pos+1 >= len(l.s) {
				l.pos++
				continue
			}
			if l.s[l.pos+1] == '\n' {
				l.pos += 2
				continue
			}
			l.pos++
			b.WriteRune(l.consumeEscape())
		default:
			r, n := utf8.DecodeRuneInString(l.s[l.pos:])
			b.WriteRune(r)
			l.pos += n
		}
	}
	return ctok{typ: "string", value: b.String(), src: l.s[start:]}
}

func (l *cssLexer) consumeURL(start int) ctok {
	bad := false
	for l.pos < len(l.s) {
		c := l.s[l.pos]
		l.pos++
		if c == ')' {
			break
		}
		if c == '"' || c == '\'' || c == '(' {
			bad = true
		}
		if c == '\\' && l.pos < len(l.s) {
			l.pos++
		}
	}
	t := ctok{typ: "url", src: l.s[start:l.pos]}
	if bad {
		t.typ = "bad-url"
	}
	return t
}

func lexCSS(s string) []ctok {
	l := &cssLexer{s: s}
	return l.tokens(0)
}

// ---- serialisation (tinycss2 serialize_identifier / string) ----------------------

func serializeName(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 0x80 || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r <= 0x1f || r == 0x7f:
			b.WriteString("\\" + strings.ToUpper(strconv.FormatInt(int64(r), 16)) + " ")
		default:
			b.WriteByte('\\')
			b.WriteRune(r)
		}
	}
	return b.String()
}

func serializeIdentifier(v string) string {
	if v == "" {
		return ""
	}
	if v == "-" {
		return `\-`
	}
	if strings.HasPrefix(v, "--") {
		return "--" + serializeName(v[2:])
	}
	prefix := ""
	if v[0] == '-' {
		prefix, v = "-", v[1:]
	}
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return prefix + "\\" + strings.ToUpper(strconv.FormatInt(int64(v[0]), 16)) + " " + serializeName(v[1:])
	}
	return prefix + serializeName(v)
}

func serializeString(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\A `)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (t ctok) serialize() string {
	switch t.typ {
	case "ident":
		return serializeIdentifier(t.value)
	case "string":
		return serializeString(t.value)
	case "hash":
		return "#" + serializeName(t.value)
	case "number":
		return t.rep
	case "percentage":
		return t.rep + "%"
	case "dimension":
		if t.unit == "e" || strings.HasPrefix(t.unit, "e-") {
			return t.rep + `\65 ` + serializeIdentifier(t.unit[1:])
		}
		return t.rep + serializeIdentifier(t.unit)
	case "literal":
		return t.value
	case "whitespace":
		return " "
	}
	return t.src
}

// ---- declarations ---------------------------------------------------------------

type cssDecl struct {
	name      string // as written
	lower     string
	value     []ctok
	important bool
}

// parseDeclarationList is tinycss2.parse_declaration_list restricted to
// declarations (errors and at-rules are skipped).
func parseDeclarationList(toks []ctok) []cssDecl {
	var out []cssDecl
	start := 0
	flush := func(chunk []ctok) {
		i := 0
		for i < len(chunk) && chunk[i].typ == "whitespace" {
			i++
		}
		chunk = chunk[i:]
		if len(chunk) == 0 || chunk[0].typ != "ident" {
			return
		}
		name := chunk[0]
		j := 1
		for j < len(chunk) && chunk[j].typ == "whitespace" {
			j++
		}
		if j >= len(chunk) || chunk[j].typ != "literal" || chunk[j].value != ":" {
			return
		}
		val := chunk[j+1:]
		d := cssDecl{name: name.value, lower: name.lower, value: val}
		// !important: the last two non-whitespace tokens.
		k := len(val)
		for k > 0 && val[k-1].typ == "whitespace" {
			k--
		}
		if k >= 2 && val[k-1].typ == "ident" && val[k-1].lower == "important" {
			m := k - 2
			for m >= 0 && val[m].typ == "whitespace" {
				m--
			}
			if m >= 0 && val[m].typ == "literal" && val[m].value == "!" {
				d.important = true
				d.value = val[:m]
			}
		}
		out = append(out, d)
	}
	for i, t := range toks {
		if t.typ == "literal" && t.value == ";" {
			flush(toks[start:i])
			start = i + 1
		}
	}
	flush(toks[start:])
	return out
}
