package gmail

import (
	"math"
	"math/big"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Port of the tinycss2-based style allow-list of server/gmail.py.

const (
	styleMaxLen        = 1024
	styleSerializedMax = 2048
	styleMaxDecls      = 32
)

var (
	globalKeywords = map[string]bool{"inherit": true, "initial": true, "unset": true, "revert": true}
	colorFunctions = map[string]bool{"rgb": true, "rgba": true, "hsl": true, "hsla": true}
	rejectedTypes  = map[string]bool{"at-keyword": true, "bad-string": true, "bad-url": true,
		"[] block": true, "{} block": true, "url": true}
	rejectedLiterals = map[string]bool{"!": true, ";": true, "<": true, ">": true, "{": true, "}": true,
		"[": true, "]": true, ":": true}
	hexColorRE = regexp.MustCompile(`^(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
)

func ratFrom(s string) *big.Rat {
	s = strings.TrimPrefix(s, "+")
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil
	}
	if neg {
		r.Neg(r)
	}
	return r
}

func ratInt(n int64) *big.Rat { return new(big.Rat).SetInt64(n) }

var absUnits = map[string]*big.Rat{
	"px": ratInt(1),
	"pt": big.NewRat(4, 3),
	"pc": ratInt(16),
	"in": ratInt(96),
	"cm": new(big.Rat).Quo(ratInt(96), ratFrom("2.54")),
	"mm": new(big.Rat).Quo(ratInt(96), ratFrom("25.4")),
	"q":  new(big.Rat).Quo(ratInt(96), ratFrom("101.6")),
}

func textIsSafe(text string) bool {
	if strings.ContainsAny(text, "<>") {
		return false
	}
	for _, r := range text {
		switch {
		case r < 0x20:
			if r != '\t' && r != '\n' && r != '\r' && r != '\f' {
				return false
			}
		case r < 0x7f:
		case r < 0xa0:
			return false
		default:
			if r >= 0xfdd0 && r <= 0xfdef || r&0xfffe == 0xfffe {
				return false
			}
			if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cs, r) {
				return false
			}
		}
	}
	return utf8.ValidString(text)
}

func isFinite(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

func tokenIsSafe(t ctok) bool {
	switch t.typ {
	case "whitespace":
		return true
	case "ident":
		return !globalKeywords[t.lower] && !strings.HasPrefix(t.lower, "--") && textIsSafe(t.value)
	case "string", "hash":
		return textIsSafe(t.value)
	case "literal":
		return !rejectedLiterals[t.value] && textIsSafe(t.value)
	case "number", "percentage":
		return isFinite(t.num)
	case "dimension":
		return isFinite(t.num) && textIsSafe(t.unit)
	case "function":
		if !colorFunctions[t.lower] {
			return false
		}
		return tokensAreSafe(t.args)
	}
	return false
}

func tokensAreSafe(ts []ctok) bool {
	for _, t := range ts {
		if !tokenIsSafe(t) {
			return false
		}
	}
	return true
}

func stripWS(ts []ctok) []ctok {
	s, e := 0, len(ts)
	for s < e && ts[s].typ == "whitespace" {
		s++
	}
	for e > s && ts[e-1].typ == "whitespace" {
		e--
	}
	return ts[s:e]
}

func splitWS(ts []ctok) [][]ctok {
	var out [][]ctok
	for _, t := range ts {
		if t.typ != "whitespace" {
			out = append(out, []ctok{t})
		}
	}
	return out
}

func splitLiteral(ts []ctok, lit string) [][]ctok {
	var parts [][]ctok
	var cur []ctok
	for _, t := range ts {
		if t.typ == "literal" && t.value == lit {
			parts = append(parts, cur)
			cur = nil
		} else {
			cur = append(cur, t)
		}
	}
	return append(parts, cur)
}

func singleToken(ts []ctok) (ctok, bool) {
	s := stripWS(ts)
	if len(s) != 1 {
		return ctok{}, false
	}
	return s[0], true
}

func finiteNumber(t ctok) (float64, bool) {
	if t.typ != "number" && t.typ != "percentage" {
		return 0, false
	}
	return t.num, isFinite(t.num)
}

func dimensionPx(t ctok) *big.Rat {
	if t.typ != "dimension" {
		return nil
	}
	f, ok := absUnits[strings.ToLower(t.unit)]
	if !ok || !isFinite(t.num) {
		return nil
	}
	n := ratFrom(t.rep)
	if n == nil {
		return nil
	}
	return n.Mul(n, f)
}

type lengthOpts struct {
	min, max    *big.Rat
	percent     bool
	pmin, pmax  float64
	auto        bool
	percentSet  bool
	percentMinS bool
}

func newLen(min, max int64) lengthOpts {
	return lengthOpts{min: ratInt(min), max: ratInt(max), pmin: 0, pmax: 100}
}

func lengthValue(t ctok, o lengthOpts) (string, bool) {
	switch t.typ {
	case "ident":
		if o.auto && t.lower == "auto" {
			return "auto", true
		}
	case "dimension":
		px := dimensionPx(t)
		if px == nil || px.Cmp(o.min) < 0 || px.Cmp(o.max) > 0 {
			return "", false
		}
		return t.serialize(), true
	case "number":
		v, ok := finiteNumber(t)
		if !ok || v != 0 {
			return "", false
		}
		if o.min.Sign() > 0 || o.max.Sign() < 0 {
			return "", false
		}
		return t.serialize(), true
	case "percentage":
		if !o.percent {
			return "", false
		}
		v, ok := finiteNumber(t)
		if !ok || v < o.pmin || v > o.pmax {
			return "", false
		}
		return t.serialize(), true
	}
	return "", false
}

type validator func([]ctok) (string, bool)

func colorFunction(t ctok) (string, bool) {
	parts := splitLiteral(t.args, ",")
	name := t.lower
	expected := 3
	if name == "rgba" || name == "hsla" {
		expected = 4
	}
	if len(parts) != expected {
		return "", false
	}
	var values []string
	for pos, part := range parts {
		c, ok := singleToken(part)
		if !ok {
			return "", false
		}
		if pos == 3 {
			v, ok := finiteNumber(c)
			if !ok {
				return "", false
			}
			switch c.typ {
			case "percentage":
				if v < 0 || v > 100 {
					return "", false
				}
			case "number":
				if v < 0 || v > 1 {
					return "", false
				}
			default:
				return "", false
			}
			values = append(values, c.serialize())
			continue
		}
		if name == "rgb" || name == "rgba" {
			v, ok := finiteNumber(c)
			if !ok {
				return "", false
			}
			switch c.typ {
			case "percentage":
				if v < 0 || v > 100 {
					return "", false
				}
			case "number":
				if v < 0 || v > 255 {
					return "", false
				}
			default:
				return "", false
			}
			values = append(values, c.serialize())
			continue
		}
		if pos == 0 {
			if _, ok := finiteNumber(c); !ok || c.typ != "number" {
				return "", false
			}
			values = append(values, c.serialize())
		} else {
			v, ok := finiteNumber(c)
			if !ok || c.typ != "percentage" || v < 0 || v > 100 {
				return "", false
			}
			values = append(values, c.serialize())
		}
	}
	return name + "(" + strings.Join(values, ", ") + ")", true
}

func parseColor(t ctok) (string, bool) {
	switch t.typ {
	case "ident":
		if _, ok := namedColors[t.lower]; ok || t.lower == "currentcolor" {
			return t.serialize(), true
		}
	case "hash":
		if hexColorRE.MatchString(t.value) {
			return t.serialize(), true
		}
	case "function":
		if colorFunctions[t.lower] {
			return colorFunction(t)
		}
	}
	return "", false
}

func validateColor(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	return parseColor(t)
}

func keywordValidator(choices ...string) validator {
	set := map[string]bool{}
	for _, c := range choices {
		set[c] = true
	}
	return func(ts []ctok) (string, bool) {
		t, ok := singleToken(ts)
		if !ok || t.typ != "ident" || !set[t.lower] {
			return "", false
		}
		return t.serialize(), true
	}
}

func validateFontFamily(ts []ctok) (string, bool) {
	families := splitLiteral(ts, ",")
	if len(families) < 1 || len(families) > 8 {
		return "", false
	}
	var rendered []string
	for _, fam := range families {
		var ser, vals []string
		for _, t := range fam {
			if t.typ == "whitespace" {
				continue
			}
			if t.typ != "ident" && t.typ != "string" {
				return "", false
			}
			if !textIsSafe(t.value) || utf8.RuneCountInString(t.value) > 128 {
				return "", false
			}
			ser = append(ser, t.serialize())
			vals = append(vals, t.value)
		}
		if len(vals) == 0 || utf8.RuneCountInString(strings.Join(vals, " ")) > 128 {
			return "", false
		}
		rendered = append(rendered, strings.Join(ser, " "))
	}
	return strings.Join(rendered, ", "), true
}

func relativeValue(t ctok, emMin, emMax, pMin, pMax float64) (string, bool) {
	if t.typ == "dimension" && strings.ToLower(t.unit) == "em" {
		if !isFinite(t.num) {
			return "", false
		}
		if emMin <= t.num && t.num <= emMax {
			return t.serialize(), true
		}
		return "", false
	}
	if t.typ == "percentage" {
		if v, ok := finiteNumber(t); ok && pMin <= v && v <= pMax {
			return t.serialize(), true
		}
	}
	return "", false
}

func isEmOrPercent(t ctok) bool {
	return t.typ == "percentage" || t.typ == "dimension" && strings.ToLower(t.unit) == "em"
}

func validateFontSize(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if isEmOrPercent(t) {
		return relativeValue(t, 0.5, 2.5, 50, 250)
	}
	if t.typ == "ident" {
		switch t.lower {
		case "xx-small", "x-small", "small", "medium", "large", "x-large", "xx-large":
			return t.serialize(), true
		}
		return "", false
	}
	return lengthValue(t, newLen(0, 48))
}

func validateFontWeight(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if t.typ == "ident" {
		switch t.lower {
		case "normal", "bold", "bolder", "lighter":
			return t.serialize(), true
		}
		return "", false
	}
	if t.typ == "number" {
		v, ok := finiteNumber(t)
		if !ok || v < 100 || v > 900 || !t.isInt {
			return "", false
		}
		return t.serialize(), true
	}
	return "", false
}

func validateLineHeight(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if t.typ == "ident" && t.lower == "normal" {
		return "normal", true
	}
	if t.typ == "number" {
		v, ok := finiteNumber(t)
		if !ok || v < 1 || v > 3 {
			return "", false
		}
		return t.serialize(), true
	}
	if isEmOrPercent(t) {
		return relativeValue(t, 1, 3, 100, 300)
	}
	return lengthValue(t, newLen(1, 200))
}

func validateLetterSpacing(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if t.typ == "ident" && t.lower == "normal" {
		return "normal", true
	}
	if t.typ == "number" {
		if v, ok := finiteNumber(t); ok && v == 0 {
			return t.serialize(), true
		}
		return "", false
	}
	if t.typ != "dimension" {
		return "", false
	}
	px := dimensionPx(t)
	if px == nil || new(big.Rat).Abs(px).Cmp(ratInt(8)) > 0 {
		return "", false
	}
	return t.serialize(), true
}

func validateVerticalAlign(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if t.typ == "ident" {
		switch t.lower {
		case "baseline", "top", "middle", "bottom", "sub", "super", "text-top", "text-bottom":
			return t.serialize(), true
		}
		return "", false
	}
	return lengthValue(t, newLen(-128, 128))
}

func validateSize(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	o := newLen(0, 1600)
	o.percent, o.auto = true, true
	return lengthValue(t, o)
}

func validateMaxSize(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	if t.typ == "ident" && t.lower == "none" {
		return "none", true
	}
	o := newLen(0, 1600)
	o.percent = true
	return lengthValue(t, o)
}

func validateTextIndent(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	o := newLen(-256, 256)
	o.percent, o.pmin, o.pmax = true, -100, 100
	return lengthValue(t, o)
}

func validateBackgroundRepeat(ts []ctok) (string, bool) {
	var values []string
	for _, c := range splitWS(ts) {
		if c[0].typ != "ident" {
			return "", false
		}
		values = append(values, c[0].lower)
	}
	rep := func(v string) bool { return v == "repeat" || v == "no-repeat" || v == "space" || v == "round" }
	if len(values) == 1 && (rep(values[0]) || values[0] == "repeat-x" || values[0] == "repeat-y") {
		return values[0], true
	}
	if len(values) == 2 && rep(values[0]) && rep(values[1]) {
		return values[0] + " " + values[1], true
	}
	return "", false
}

func validateBackgroundPosition(ts []ctok) (string, bool) {
	comps := splitWS(ts)
	if len(comps) < 1 || len(comps) > 2 {
		return "", false
	}
	var out []string
	for _, c := range comps {
		t := c[0]
		if t.typ == "ident" {
			switch t.lower {
			case "left", "right", "top", "bottom", "center":
				out = append(out, t.lower)
				continue
			}
		}
		o := newLen(-1600, 1600)
		o.percent, o.pmin = true, 0
		v, ok := lengthValue(t, o)
		if !ok {
			return "", false
		}
		out = append(out, v)
	}
	return strings.Join(out, " "), true
}

func validateBackgroundSize(ts []ctok) (string, bool) {
	comps := splitWS(ts)
	if len(comps) == 1 && comps[0][0].typ == "ident" {
		switch k := comps[0][0].lower; k {
		case "auto", "cover", "contain":
			return k, true
		}
		return "", false
	}
	if len(comps) < 1 || len(comps) > 2 {
		return "", false
	}
	var out []string
	for _, c := range comps {
		o := newLen(0, 1600)
		o.percent, o.auto = true, true
		v, ok := lengthValue(c[0], o)
		if !ok {
			return "", false
		}
		out = append(out, v)
	}
	return strings.Join(out, " "), true
}

func validateOpacity(ts []ctok) (string, bool) {
	t, ok := singleToken(ts)
	if !ok {
		return "", false
	}
	v, ok := finiteNumber(t)
	if !ok {
		return "", false
	}
	limit := 1.0
	if t.typ == "percentage" {
		limit = 100
	}
	if v >= 0 && v <= limit {
		return t.serialize(), true
	}
	return "", false
}

func multiLength(ts []ctok, maxItems int, per func(ctok) (string, bool)) (string, bool) {
	comps := splitWS(ts)
	if len(comps) < 1 || len(comps) > maxItems {
		return "", false
	}
	var out []string
	for _, c := range comps {
		v, ok := per(c[0])
		if !ok {
			return "", false
		}
		out = append(out, v)
	}
	return strings.Join(out, " "), true
}

func validateMargin(ts []ctok) (string, bool) {
	return multiLength(ts, 4, func(t ctok) (string, bool) {
		o := newLen(-256, 256)
		o.percent, o.pmin, o.pmax, o.auto = true, -100, 100, true
		return lengthValue(t, o)
	})
}

func validatePadding(ts []ctok) (string, bool) {
	return multiLength(ts, 4, func(t ctok) (string, bool) {
		o := newLen(0, 256)
		o.percent = true
		return lengthValue(t, o)
	})
}

func borderWidthValue(t ctok) (string, bool) {
	if t.typ == "ident" {
		switch t.lower {
		case "thin", "medium", "thick":
			return t.serialize(), true
		}
	}
	return lengthValue(t, newLen(0, 8))
}

func validateBorderWidth(ts []ctok) (string, bool) { return multiLength(ts, 4, borderWidthValue) }

var borderStyles = map[string]bool{"none": true, "hidden": true, "dotted": true, "dashed": true,
	"solid": true, "double": true, "groove": true, "ridge": true, "inset": true, "outset": true}

func validateBorderStyle(ts []ctok) (string, bool) {
	return multiLength(ts, 4, func(t ctok) (string, bool) {
		if t.typ != "ident" || !borderStyles[t.lower] {
			return "", false
		}
		return t.serialize(), true
	})
}

func validateBorderColor(ts []ctok) (string, bool) { return multiLength(ts, 4, parseColor) }

func validateBorder(ts []ctok) (string, bool) {
	comps := splitWS(ts)
	if len(comps) < 1 || len(comps) > 3 {
		return "", false
	}
	var foundW, foundS, foundC bool
	var out []string
	for _, c := range comps {
		t := c[0]
		if w, ok := borderWidthValue(t); ok {
			if foundW {
				return "", false
			}
			foundW = true
			out = append(out, w)
			continue
		}
		if t.typ == "ident" && borderStyles[t.lower] {
			if foundS {
				return "", false
			}
			foundS = true
			out = append(out, t.serialize())
			continue
		}
		if col, ok := parseColor(t); ok {
			if foundC {
				return "", false
			}
			foundC = true
			out = append(out, col)
			continue
		}
		return "", false
	}
	return strings.Join(out, " "), true
}

func validateBorderSpacing(ts []ctok) (string, bool) {
	return multiLength(ts, 2, func(t ctok) (string, bool) { return lengthValue(t, newLen(0, 32)) })
}

func validateTextDecoration(ts []ctok) (string, bool) {
	comps := splitWS(ts)
	if len(comps) < 1 || len(comps) > 3 {
		return "", false
	}
	var out []string
	for _, c := range comps {
		t := c[0]
		if t.typ != "ident" {
			return "", false
		}
		switch t.lower {
		case "none", "underline", "overline", "line-through":
		default:
			return "", false
		}
		out = append(out, t.serialize())
	}
	for _, v := range out {
		if v == "none" && len(out) > 1 {
			return "", false
		}
	}
	return strings.Join(out, " "), true
}

func radiusValue(t ctok) (string, bool) {
	switch t.typ {
	case "number":
		if v, ok := finiteNumber(t); ok && v == 0 {
			return t.serialize(), true
		}
	case "dimension":
		px := dimensionPx(t)
		if px != nil && px.Sign() >= 0 && px.Cmp(ratInt(64)) <= 0 {
			return t.serialize(), true
		}
	case "percentage":
		if v, ok := finiteNumber(t); ok && v >= 0 && v <= 100 {
			return t.serialize(), true
		}
	}
	return "", false
}

func radiusGroup(ts []ctok, max int) (string, bool) { return multiLength(ts, max, radiusValue) }

func validateBorderRadius(ts []ctok) (string, bool) {
	parts := splitLiteral(ts, "/")
	if len(parts) > 2 {
		return "", false
	}
	var out []string
	for _, p := range parts {
		g, ok := radiusGroup(p, 4)
		if !ok {
			return "", false
		}
		out = append(out, g)
	}
	return strings.Join(out, " / "), true
}

func validateBorderRadiusLonghand(ts []ctok) (string, bool) {
	for _, t := range ts {
		if t.typ == "literal" && t.value == "/" {
			return "", false
		}
	}
	return radiusGroup(ts, 2)
}

var styleValidators = map[string]validator{}

func init() {
	v := styleValidators
	v["color"] = validateColor
	v["background-color"] = validateColor
	v["font-family"] = validateFontFamily
	v["font-size"] = validateFontSize
	v["font-style"] = keywordValidator("normal", "italic", "oblique")
	v["font-weight"] = validateFontWeight
	v["font-variant"] = keywordValidator("normal", "small-caps")
	v["line-height"] = validateLineHeight
	v["letter-spacing"] = validateLetterSpacing
	v["text-align"] = keywordValidator("left", "right", "center", "justify", "start", "end")
	v["text-decoration"] = validateTextDecoration
	v["text-transform"] = keywordValidator("none", "capitalize", "uppercase", "lowercase")
	v["white-space"] = keywordValidator("normal", "pre-wrap", "pre-line")
	v["word-break"] = keywordValidator("normal", "break-all", "break-word")
	v["overflow-wrap"] = keywordValidator("break-word", "anywhere")
	for _, p := range []string{"margin", "margin-top", "margin-right", "margin-bottom", "margin-left"} {
		v[p] = validateMargin
	}
	for _, p := range []string{"padding", "padding-top", "padding-right", "padding-bottom", "padding-left"} {
		v[p] = validatePadding
	}
	v["border"] = validateBorder
	v["border-width"] = validateBorderWidth
	v["border-style"] = validateBorderStyle
	v["border-color"] = validateBorderColor
	v["border-spacing"] = validateBorderSpacing
	v["border-radius"] = validateBorderRadius
	v["border-collapse"] = keywordValidator("collapse", "separate")
	v["table-layout"] = keywordValidator("auto", "fixed")
	v["vertical-align"] = validateVerticalAlign
	for _, p := range []string{"width", "height", "min-width", "min-height"} {
		v[p] = validateSize
	}
	v["max-width"] = validateMaxSize
	v["max-height"] = validateMaxSize
	v["background"] = validateColor
	v["text-indent"] = validateTextIndent
	v["list-style-type"] = keywordValidator("none", "disc", "circle", "square", "decimal",
		"decimal-leading-zero", "lower-alpha", "upper-alpha", "lower-latin", "upper-latin",
		"lower-roman", "upper-roman")
	v["direction"] = keywordValidator("ltr", "rtl")
	v["overflow"] = keywordValidator("visible", "hidden", "auto")
	v["visibility"] = keywordValidator("visible", "hidden")
	v["opacity"] = validateOpacity
	v["background-repeat"] = validateBackgroundRepeat
	v["background-position"] = validateBackgroundPosition
	v["background-size"] = validateBackgroundSize
	v["float"] = keywordValidator("left", "right", "none")
	v["display"] = keywordValidator("block", "inline", "inline-block", "none", "list-item", "table",
		"table-row", "table-cell", "table-row-group", "table-header-group", "table-footer-group",
		"table-column", "table-column-group", "inline-table")
	for _, side := range []string{"top", "right", "bottom", "left"} {
		v["border-"+side] = validateBorder
		v["border-"+side+"-width"] = validateBorderWidth
		v["border-"+side+"-style"] = validateBorderStyle
		v["border-"+side+"-color"] = validateBorderColor
	}
	for _, c := range []string{"top-left", "top-right", "bottom-right", "bottom-left"} {
		v["border-"+c+"-radius"] = validateBorderRadiusLonghand
	}
}

var styleAliases = map[string]string{"background": "background-color"}

func aliasName(n string) string {
	if a, ok := styleAliases[n]; ok {
		return a
	}
	return n
}

// sanitizeStyle re-serialises a style attribute from the allow-list; ok=false
// means "drop the attribute" (_sanitize_style returning None).
func sanitizeStyle(value string) (string, bool) {
	if value == "" || utf8.RuneCountInString(value) > styleMaxLen || !textIsSafe(value) {
		return "", false
	}
	decls := parseDeclarationList(lexCSS(value))
	var rendered []string
	count := 0
	for _, d := range decls {
		count++
		if count > styleMaxDecls {
			return "", false
		}
		val := validatorFor(d.lower)
		if val == nil {
			continue
		}
		toks := stripWS(d.value)
		if len(toks) == 0 || !tokensAreSafe(toks) {
			continue
		}
		ser, ok := val(toks)
		if !ok {
			continue
		}
		rendered = append(rendered, aliasName(d.lower)+":"+ser)
	}
	if len(rendered) == 0 {
		return "", false
	}
	result := strings.Join(rendered, ";")
	if utf8.RuneCountInString(result) > styleSerializedMax || !textIsSafe(result) {
		return "", false
	}
	return result, true
}

func validatorFor(name string) validator { return styleValidators[name] }
