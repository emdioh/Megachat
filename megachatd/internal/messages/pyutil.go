package messages

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Small helpers that reproduce the exact Python semantics the row builder
// depends on (str.strip, pathlib, urllib.parse.quote, re's \w and \b).

// pySpaceClass is the body of a regexp character class equal to Python's
// str \s (Unicode whitespace): Go's \s is ASCII only.
const pySpaceClass = `\s\x{1c}-\x{1f}\x{85}\p{Z}`

// isPySpace is str.isspace for one rune.
func isPySpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pyStrip is str.strip().
func pyStrip(s string) string { return strings.TrimFunc(s, isPySpace) }

// hasPySpace reports whether s contains a Python whitespace character.
func hasPySpace(s string) bool { return strings.IndexFunc(s, isPySpace) >= 0 }

// isWordRune is Python's \w for str patterns (alphanumeric or underscore).
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// followedByBoundary reports whether position end of s is a regex \b given
// that the character before it is a word character.
func followedByBoundary(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	return !isWordRune(r)
}

// containsWordSuffix is re.search(re.escape(ext) + r"\b", s) for an
// extension ending in a word character.
func containsWordSuffix(s, ext string) bool {
	from := 0
	for {
		i := strings.Index(s[from:], ext)
		if i < 0 {
			return false
		}
		if followedByBoundary(s, from+i+len(ext)) {
			return true
		}
		from += i + 1
	}
}

// pyName is PurePosixPath(p).name ("" for ".", "/" and "").
func pyName(p string) string {
	name := ""
	for _, part := range strings.Split(p, "/") {
		if part != "" && part != "." {
			name = part
		}
	}
	return name
}

// pySuffix is PurePosixPath(p).suffix.
func pySuffix(p string) string {
	name := pyName(p)
	i := strings.LastIndex(name, ".")
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// pyParent is PurePosixPath(p).parent for an absolute, clean path.
func pyParent(p string) string { return filepath.Dir(p) }

// urlQuoteSafeEmpty is urllib.parse.quote(s, safe="").
func urlQuoteSafeEmpty(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '-', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// mediaURL builds api/media/<proto>/<quoted segments>?w=96.
func mediaURL(proto, value string) string {
	segs := strings.Split(value, "/")
	for i, s := range segs {
		segs[i] = urlQuoteSafeEmpty(s)
	}
	return "api/media/" + proto + "/" + strings.Join(segs, "/") + "?w=96"
}

// resolvePath is pathlib.Path(p).resolve() (non strict): absolute, symlinks
// of the longest existing prefix followed, the rest appended lexically.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest := ""
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// truthy is Python truthiness for a decoded JSON-ish value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// pyStr is str(v) for the scalar values extras hold; falsy values -> "".
func pyStr(v any) string {
	if !truthy(v) {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
