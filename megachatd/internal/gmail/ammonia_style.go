package gmail

import (
	"math"
	"strconv"
	"strings"
)

// ammoniaStyle reproduces what nh3 (ammonia with filter_style_properties)
// does to a style attribute after the attribute filter accepted it: every
// declaration is parsed and written again by the cssparser crate. Compared
// with the tinycss2-based validators' output that changes
//   - the separator of top-level tokens: always one space, so a comma list
//     reads "Arial , sans-serif" (inside functions it stays "rgb(1, 2, 3)");
//   - numbers: shortest f32 form with at most 6 significant digits, a number
//     written with a fraction or an exponent keeps ".0" when integral
//     (".5" -> "0.5", "1e2" -> "100.0", "1.40" -> "1.4"), a '+' sign is kept.
func ammoniaStyle(value string) string {
	var out []string
	for _, d := range parseDeclarationList(lexCSS(value)) {
		var parts []string
		for _, t := range d.value {
			if t.typ != "whitespace" {
				parts = append(parts, ammoniaToken(t, false))
			}
		}
		out = append(out, d.lower+":"+strings.Join(parts, " "))
	}
	return strings.Join(out, ";")
}

func ammoniaToken(t ctok, inner bool) string {
	switch t.typ {
	case "number":
		return cssparserNumber(t)
	case "percentage":
		return cssparserNumber(t) + "%"
	case "dimension":
		if t.unit == "e" || strings.HasPrefix(t.unit, "e-") {
			return t.serialize()
		}
		return cssparserNumber(t) + serializeIdentifier(t.unit)
	case "function":
		var b strings.Builder
		b.WriteString(serializeIdentifier(t.value) + "(")
		space := false
		for _, a := range t.args {
			switch {
			case a.typ == "whitespace":
				space = true
			case a.typ == "literal" && a.value == ",":
				b.WriteString(", ")
				space = false
			default:
				if space && !strings.HasSuffix(b.String(), "(") && !strings.HasSuffix(b.String(), ", ") {
					b.WriteByte(' ')
				}
				space = false
				b.WriteString(ammoniaToken(a, true))
			}
		}
		b.WriteString(")")
		return b.String()
	}
	return t.serialize()
}

// cssparserNumber is cssparser's serialize_number for a numeric token.
func cssparserNumber(t ctok) string {
	sign := ""
	if strings.HasPrefix(t.rep, "+") {
		sign = "+"
	}
	if t.isInt {
		return sign + strconv.FormatInt(int64(t.num), 10)
	}
	f := float64(float32(t.num))
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return sign + "0.0"
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 6, 64), 64)
	s := strconv.FormatFloat(r, 'f', -1, 64)
	if f == math.Trunc(f) {
		s += ".0"
	}
	return sign + s
}
