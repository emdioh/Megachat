package gmail

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/vanng822/go-premailer/premailer"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// inlineCSS is css_inline.CSSInliner(inline_style_tags=False,
// keep_style_tags=False, keep_link_tags=False, keep_at_rules=False,
// extra_css=css).inline(raw): the document's own <style> blocks are dropped
// (their rules reach us already filtered, as css, one "sel, sel{decls}" per
// line) and css is applied to the elements as style attributes.
//
// Failure model, as in the Python code: css-inline skips a rule whose
// selector it cannot parse and still inlines the rest (here: cascadia
// rejects it and the rule is skipped). Any other failure, including a panic
// in the CSS or HTML libraries, is an error, and the caller then sanitises the
// document without inlining, which is what _scan_inlined_html does when
// css-inline raises.
//
// go-premailer does the inlining, but its own selector handling is not
// css-inline's: it counts specificity on the selector text (an attribute
// selector ranks below a class, a pseudo-class counts for nothing), leaves
// rules containing "*" or a comma inside :is()/:not() in a <style> block and
// splits selector lists on every comma. So the matching and the cascade order
// are done here, with cascadia's real selector engine and specificity, and
// premailer receives one rule per (rule, selector) whose selector is a marker
// attribute already set on the matching elements, sorted so that its own
// ordering (important declarations last, then source order) is the CSS
// cascade. An element's own style attribute is set aside during the run and
// merged back afterwards, because premailer would let it override !important
// rules; the property order of the result is css-inline's.
func inlineCSS(raw string, css string) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("inline css: %v", r)
		}
	}()
	if strings.TrimSpace(css) == "" {
		return "", errors.New("no css")
	}
	rules := parseInlineRules(css)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return "", err
	}
	// The document's <style> elements stay in the tree while the selectors are
	// matched (they count for :first-child and friends, as in css-inline) and
	// are told apart from ours so premailer ignores them; they go afterwards.
	doc.Find("style").SetAttr("data-premailer", "ignore")
	var elements []*html.Node
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		n := s.Nodes[0]
		kept := n.Attr[:0] // the markers must not come from the document
		for _, a := range n.Attr {
			if !strings.HasPrefix(a.Key, markPrefix) {
				kept = append(kept, a)
			}
		}
		n.Attr = kept
		elements = append(elements, n)
	})

	// Rules in cascade order (stable: source order breaks ties).
	sort.SliceStable(rules, func(i, j int) bool { return specLess(rules[i].spec, rules[j].spec) })
	matched := map[*html.Node][]int{}
	var sheet strings.Builder
	for i, r := range rules {
		tag := fmt.Sprintf("%sr%d", markPrefix, i)
		hit := false
		for _, n := range elements {
			if n.Type == html.ElementNode && r.sel.Match(n) {
				n.Attr = append(n.Attr, html.Attribute{Key: tag})
				matched[n] = append(matched[n], i)
				hit = true
			}
		}
		if hit {
			fmt.Fprintf(&sheet, "[%s]{%s}\n", tag, r.decls)
		}
	}
	if sheet.Len() == 0 {
		doc.Find("style").Remove()
		return renderDoc(doc)
	}

	// Set the style attributes aside (renamed in place, so they keep their position).
	for _, n := range elements {
		for i := range n.Attr {
			if n.Attr[i].Key == "style" {
				n.Attr[i].Key = markInline
			}
		}
	}
	style := &html.Node{Type: html.ElementNode, DataAtom: atom.Style, Data: "style"}
	style.AppendChild(&html.Node{Type: html.TextNode, Data: sheet.String()}) // not SetText: it escapes
	doc.Find("head").First().AppendNodes(style)
	renameForeignVoids(doc) // premailer serialises the document too
	pr := premailer.NewPremailer(doc, premailer.NewOptions(
		premailer.WithCssToAttributes(false),
		premailer.WithKeepBangImportant(true),
	))
	if _, err := pr.Transform(); err != nil {
		return "", err
	}
	doc.Find("style").Remove() // the document's own and whatever premailer left over

	for _, n := range elements {
		var own string
		ownAt, ruledAt := -1, -1
		var ruled map[string]styleDecl
		for i, a := range n.Attr {
			switch a.Key {
			case markInline:
				ownAt, own = i, a.Val
			case "style":
				ruledAt = i
				ruled = map[string]styleDecl{}
				for _, d := range parseStyleDecls(a.Val) {
					ruled[d.name] = d
				}
			}
		}
		// Properties in the order css-inline gives them: the first declaration wins the position.
		var base []styleDecl
		seen := map[string]bool{}
		for _, ri := range matched[n] {
			for _, name := range rules[ri].names {
				if d, ok := ruled[name]; ok && !seen[name] {
					seen[name] = true
					base = append(base, d)
				}
			}
		}
		val := own
		if len(base) > 0 {
			val = mergeStyles(base, own)
		}
		kept := n.Attr[:0]
		for i, a := range n.Attr {
			switch {
			case i == ownAt:
				kept = append(kept, html.Attribute{Key: "style", Val: val})
			case i == ruledAt:
				if ownAt < 0 && len(base) > 0 {
					kept = append(kept, html.Attribute{Key: "style", Val: val})
				}
			case !strings.HasPrefix(a.Key, markPrefix):
				kept = append(kept, a)
			}
		}
		n.Attr = kept
		if ownAt < 0 && ruledAt < 0 && len(base) > 0 {
			n.Attr = append(n.Attr, html.Attribute{Key: "style", Val: val})
		}
	}
	return renderDoc(doc)
}

var renderVoid = setOf("area base basefont bgsound br col command embed frame hr img input keygen link meta param source track wbr")

// renderDoc serialises doc. html.Render refuses a void-named element that has
// children, which only exists in foreign content (<svg><input>x</svg>); the
// sanitiser drops that content anyway, so the element is renamed first.
func renderDoc(doc *goquery.Document) (string, error) {
	renameForeignVoids(doc)
	return doc.Html()
}

func renameForeignVoids(doc *goquery.Document) {
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && renderVoid[n.Data] && n.FirstChild != nil {
			n.Data, n.DataAtom = "x-"+n.Data, 0
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc.Get(0))
}

const (
	markPrefix = "data-mc-"
	markInline = markPrefix + "inline"
)

type inlineRule struct {
	sel   cascadia.Sel
	spec  [3]int
	decls string
	names []string // declared properties, in order, lowercase
}

func specLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// parseInlineRules reads css (one "sel, sel{decls}" per line, as produced by
// filterStylesheet); a selector cascadia cannot parse drops just that selector.
func parseInlineRules(css string) []inlineRule {
	var rules []inlineRule
	for _, line := range strings.Split(css, "\n") {
		open := strings.IndexByte(line, '{')
		if open < 0 || !strings.HasSuffix(line, "}") {
			continue
		}
		decls := line[open+1 : len(line)-1]
		var names []string
		for _, d := range parseStyleDecls(decls) {
			names = append(names, d.name)
		}
		if len(names) == 0 {
			continue
		}
		for _, text := range splitSelectorList(line[:open]) {
			group, err := cascadia.ParseGroup(text)
			if err != nil {
				continue
			}
			for _, sel := range group {
				if sel.PseudoElement() != "" {
					continue
				}
				rules = append(rules, inlineRule{sel: sel, spec: sel.Specificity(), decls: decls, names: names})
			}
		}
	}
	return rules
}

// splitSelectorList splits on the commas outside parentheses, brackets and strings.
func splitSelectorList(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"', '\'':
			q := s[i]
			for i++; i < len(s) && s[i] != q; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

type styleDecl struct {
	name, value string
	important   bool
}

func parseStyleDecls(text string) []styleDecl {
	var out []styleDecl
	for _, d := range parseDeclarationList(lexCSS(text)) {
		var b strings.Builder
		for _, t := range d.value {
			b.WriteString(t.serialize())
		}
		out = append(out, styleDecl{d.lower, strings.TrimSpace(b.String()), d.important})
	}
	return out
}

// mergeStyles adds an element's own style attribute to the declarations the
// rules produced. As in css-inline each own declaration is (re)appended at its
// position, and an !important rule beats an own declaration that is not.
func mergeStyles(base []styleDecl, own string) string {
	decls := append([]styleDecl(nil), base...)
	for _, d := range parseStyleDecls(own) {
		for i := range decls {
			if decls[i].name == d.name {
				if decls[i].important && !d.important {
					d = decls[i]
				}
				decls = append(decls[:i], decls[i+1:]...)
				break
			}
		}
		decls = append(decls, d)
	}
	parts := make([]string, len(decls))
	for i, d := range decls {
		parts[i] = d.name + ":" + d.value
		if d.important {
			parts[i] += " !important"
		}
	}
	return strings.Join(parts, ";")
}
