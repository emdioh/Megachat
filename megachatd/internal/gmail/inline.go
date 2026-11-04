package gmail

import (
	"bytes"
	"errors"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// A small CSS inliner (css-inline equivalent: inline_style_tags=False,
// keep_style_tags=False, extra_css=...). go-premailer is not usable here (its
// goquery / cascadia / vanng822-css dependencies are not in go.sum); this
// supports the selector subset email CSS uses: type, universal, #id, .class,
// [attr], [attr=v] [~= |= ^= $= *=], :first-child, :last-child, and the
// descendant, child, adjacent and general-sibling combinators.

type simpleSel struct {
	tag     string
	id      string
	classes []string
	attrs   []attrSel
	first   bool
	last    bool
}

type attrSel struct {
	name, op, val string
}

type selStep struct {
	comb string // combinator to the previous compound: "", " ", ">", "+", "~"
	sel  simpleSel
}

type cssRule struct {
	steps     []selStep
	spec      [3]int
	order     int
	decls     []inlineDecl
	important map[string]bool
}

type inlineDecl struct {
	name, value string
	important   bool
}

var errBadSelector = errors.New("unsupported selector")

func parseSelector(s string) ([]selStep, [3]int, error) {
	var steps []selStep
	var spec [3]int
	s = strings.TrimSpace(s)
	i := 0
	comb := ""
	for i < len(s) {
		// combinators
		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r' || s[j] == '\f') {
			j++
		}
		if j > i && len(steps) > 0 {
			comb = " "
		}
		i = j
		if i < len(s) && strings.IndexByte(">+~", s[i]) >= 0 {
			comb = string(s[i])
			i++
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
				i++
			}
		}
		if i >= len(s) {
			break
		}
		var sel simpleSel
		start := i
		readName := func() string {
			b := i
			for i < len(s) && (isNameChar(rune(s[i])) || s[i] == '\\') {
				i++
			}
			return s[b:i]
		}
		if s[i] == '*' {
			i++
		} else if isNameStart(rune(s[i])) {
			sel.tag = strings.ToLower(readName())
			spec[2]++
		}
		for i < len(s) {
			switch s[i] {
			case '#':
				i++
				sel.id = readName()
				spec[0]++
			case '.':
				i++
				sel.classes = append(sel.classes, readName())
				spec[1]++
			case '[':
				end := strings.IndexByte(s[i:], ']')
				if end < 0 {
					return nil, spec, errBadSelector
				}
				body := s[i+1 : i+end]
				i += end + 1
				a := attrSel{}
				if k := strings.IndexAny(body, "=~|^$*"); k >= 0 {
					a.name = strings.TrimSpace(body[:k])
					rest := body[k:]
					if strings.HasPrefix(rest, "=") {
						a.op, rest = "=", rest[1:]
					} else if len(rest) >= 2 && rest[1] == '=' {
						a.op, rest = rest[:2], rest[2:]
					} else {
						return nil, spec, errBadSelector
					}
					a.val = strings.Trim(strings.TrimSpace(rest), `"'`)
				} else {
					a.name = strings.TrimSpace(body)
				}
				sel.attrs = append(sel.attrs, a)
				spec[1]++
			case ':':
				rest := strings.ToLower(s[i:])
				switch {
				case strings.HasPrefix(rest, ":first-child"):
					sel.first = true
					i += len(":first-child")
				case strings.HasPrefix(rest, ":last-child"):
					sel.last = true
					i += len(":last-child")
				default:
					return nil, spec, errBadSelector
				}
				spec[1]++
			default:
				goto done
			}
		}
	done:
		if i == start {
			return nil, spec, errBadSelector
		}
		steps = append(steps, selStep{comb: comb, sel: sel})
		comb = ""
	}
	if len(steps) == 0 {
		return nil, spec, errBadSelector
	}
	return steps, spec, nil
}

func attrOf(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val, true
		}
	}
	return "", false
}

func (sel simpleSel) matches(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if sel.tag != "" && strings.ToLower(n.Data) != sel.tag {
		return false
	}
	if sel.id != "" {
		if v, _ := attrOf(n, "id"); v != sel.id {
			return false
		}
	}
	if len(sel.classes) > 0 {
		v, _ := attrOf(n, "class")
		have := strings.Fields(v)
		for _, c := range sel.classes {
			found := false
			for _, h := range have {
				if h == c {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	for _, a := range sel.attrs {
		v, ok := attrOf(n, a.name)
		if !ok {
			return false
		}
		switch a.op {
		case "=":
			ok = v == a.val
		case "~=":
			ok = false
			for _, f := range strings.Fields(v) {
				if f == a.val {
					ok = true
				}
			}
		case "|=":
			ok = v == a.val || strings.HasPrefix(v, a.val+"-")
		case "^=":
			ok = a.val != "" && strings.HasPrefix(v, a.val)
		case "$=":
			ok = a.val != "" && strings.HasSuffix(v, a.val)
		case "*=":
			ok = a.val != "" && strings.Contains(v, a.val)
		}
		if !ok {
			return false
		}
	}
	if sel.first && prevElement(n) != nil {
		return false
	}
	if sel.last && nextElement(n) != nil {
		return false
	}
	return true
}

func prevElement(n *html.Node) *html.Node {
	for p := n.PrevSibling; p != nil; p = p.PrevSibling {
		if p.Type == html.ElementNode {
			return p
		}
	}
	return nil
}

func nextElement(n *html.Node) *html.Node {
	for p := n.NextSibling; p != nil; p = p.NextSibling {
		if p.Type == html.ElementNode {
			return p
		}
	}
	return nil
}

func matchSteps(steps []selStep, i int, n *html.Node) bool {
	if !steps[i].sel.matches(n) {
		return false
	}
	if i == 0 {
		return true
	}
	switch steps[i].comb {
	case ">":
		return n.Parent != nil && matchSteps(steps, i-1, n.Parent)
	case "+":
		p := prevElement(n)
		return p != nil && matchSteps(steps, i-1, p)
	case "~":
		for p := prevElement(n); p != nil; p = prevElement(p) {
			if matchSteps(steps, i-1, p) {
				return true
			}
		}
		return false
	default:
		for a := n.Parent; a != nil; a = a.Parent {
			if matchSteps(steps, i-1, a) {
				return true
			}
		}
		return false
	}
}

// parseInlineDecls parses "a:b;c:d !important" text (already filtered).
func parseInlineDecls(text string) []inlineDecl {
	var out []inlineDecl
	for _, d := range parseDeclarationList(lexCSS(text)) {
		var b strings.Builder
		for _, t := range d.value {
			b.WriteString(t.serialize())
		}
		out = append(out, inlineDecl{name: d.lower, value: strings.TrimSpace(b.String()), important: d.important})
	}
	return out
}

// parseFilteredSheet reads the output of filterStylesheet.
func parseFilteredSheet(css string) []cssRule {
	var rules []cssRule
	order := 0
	for _, line := range strings.Split(css, "\n") {
		open := strings.IndexByte(line, '{')
		if open < 0 || !strings.HasSuffix(line, "}") {
			continue
		}
		decls := parseInlineDecls(line[open+1 : len(line)-1])
		for _, sel := range splitTopLevelCommas(line[:open]) {
			steps, spec, err := parseSelector(sel)
			if err != nil {
				continue
			}
			rules = append(rules, cssRule{steps: steps, spec: spec, order: order, decls: decls})
			order++
		}
	}
	return rules
}

func specLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// inlineCSS parses raw as an HTML document, applies css (the output of
// filterStylesheet) as style attributes, drops <style> elements and returns
// the serialised document.
func inlineCSS(raw string, css string) (string, error) {
	rules := parseFilteredSheet(css)
	if len(rules) == 0 {
		return "", errors.New("no usable rules")
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", err
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].spec != rules[j].spec {
			return specLess(rules[i].spec, rules[j].spec)
		}
		return rules[i].order < rules[j].order
	})
	type styled struct {
		order []string
		vals  map[string]inlineDecl
	}
	byNode := map[*html.Node]*styled{}
	var nodes []*html.Node
	var visit func(n *html.Node)
	var styleNodes []*html.Node
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if strings.ToLower(n.Data) == "style" {
				styleNodes = append(styleNodes, n)
			} else {
				nodes = append(nodes, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	apply := func(st *styled, d inlineDecl) {
		old, exists := st.vals[d.name]
		if exists && old.important && !d.important {
			return
		}
		if !exists {
			st.order = append(st.order, d.name)
		}
		st.vals[d.name] = d
	}
	for _, n := range nodes {
		for _, r := range rules {
			if !matchSteps(r.steps, len(r.steps)-1, n) {
				continue
			}
			st := byNode[n]
			if st == nil {
				st = &styled{vals: map[string]inlineDecl{}}
				byNode[n] = st
			}
			for _, d := range r.decls {
				apply(st, d)
			}
		}
	}
	for n, st := range byNode {
		existing, _ := attrOf(n, "style")
		for _, d := range parseInlineDecls(existing) {
			apply(st, d)
		}
		var parts []string
		for _, name := range st.order {
			d := st.vals[name]
			parts = append(parts, name+":"+d.value)
		}
		val := strings.Join(parts, ";")
		set := false
		for i := range n.Attr {
			if strings.EqualFold(n.Attr[i].Key, "style") {
				n.Attr[i].Val = val
				set = true
			}
		}
		if !set {
			n.Attr = append(n.Attr, html.Attribute{Key: "style", Val: val})
		}
	}
	for _, sn := range styleNodes {
		if sn.Parent != nil {
			sn.Parent.RemoveChild(sn)
		}
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}
