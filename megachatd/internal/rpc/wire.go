package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

const hexDigits = "0123456789abcdef"

// marshal encodes v like Python's json.dumps(v, separators=(",", ":"),
// ensure_ascii=True, allow_nan=False): compact, no HTML escaping, every
// non-ASCII character (and DEL) as \uXXXX with surrogate pairs.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return asciiOnly(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// asciiOnly rewrites the strings of valid compact JSON so it is pure ASCII.
func asciiOnly(in []byte) []byte {
	clean := true
	for _, c := range in {
		if c >= 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return in
	}
	out := make([]byte, 0, len(in)+len(in)/8)
	inString := false
	for i := 0; i < len(in); {
		c := in[i]
		switch {
		case !inString:
			if c == '"' {
				inString = true
			}
			out = append(out, c)
			i++
		case c == '\\':
			out = append(out, c)
			if i+1 < len(in) {
				out = append(out, in[i+1])
			}
			i += 2
		case c == '"':
			inString = false
			out = append(out, c)
			i++
		case c < 0x7f:
			out = append(out, c)
			i++
		default: // DEL or multi-byte
			r, size := rune(c), 1
			if c >= 0x80 {
				r, size = utf8.DecodeRune(in[i:])
			}
			i += size
			out = appendEscapedRune(out, r)
		}
	}
	return out
}

func appendEscapedRune(out []byte, r rune) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		out = appendU(out, 0xd800+(r>>10))
		return appendU(out, 0xdc00+(r&0x3ff))
	}
	return appendU(out, r)
}

func appendU(out []byte, r rune) []byte {
	return append(out, '\\', 'u',
		hexDigits[(r>>12)&0xf], hexDigits[(r>>8)&0xf], hexDigits[(r>>4)&0xf], hexDigits[r&0xf])
}

// formatFloat renders f like Python's repr(float).
func formatFloat(f float64) string {
	abs := f
	if abs < 0 {
		abs = -abs
	}
	if abs == 0 || (abs >= 1e-4 && abs < 1e16) {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.ContainsAny(s, ".") {
			s += ".0"
		}
		return s
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

// jsonFloat marshals as a Python float.
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	return []byte(formatFloat(float64(f))), nil
}

// idJSON is the id member of a response: precomputed JSON text.
// "null" for an unknown id.
type idJSON = json.RawMessage

var nullID = idJSON("null")

// parseID validates a request id (string, integer, or - as Python's
// isinstance(True, int) lets through when echoing "Too many requests" - a
// boolean) and returns its canonical JSON form. Callers that must reject
// booleans check isBoolID.
func parseID(raw json.RawMessage) (idJSON, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false
	}
	switch c := raw[0]; {
	case c == '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false
		}
		b, err := marshal(s)
		return b, err == nil
	case c == '-' || (c >= '0' && c <= '9'):
		if !isIntegerLiteral(raw) {
			return nil, false
		}
		n, ok := new(big.Int).SetString(string(raw), 10)
		if !ok {
			return nil, false
		}
		return idJSON(n.String()), true
	case string(raw) == "true" || string(raw) == "false":
		return raw, true
	}
	return nil, false
}

func isBoolID(id idJSON) bool { return string(id) == "true" || string(id) == "false" }

func isIntegerLiteral(raw []byte) bool {
	for i, c := range raw {
		if c == '-' && i == 0 && len(raw) > 1 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// pyRepr approximates Python's repr() of a str (used in "Unknown method 'x'").
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + string(hexDigits[r>>4]) + string(hexDigits[r&0xf]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// -- response frames ----------------------------------------------------------

type errorBody struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    *api.ErrorData `json:"data,omitempty"`
}

type errorFrame struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      idJSON    `json:"id"`
	Error   errorBody `json:"error"`
}

func errorLine(id idJSON, code int, message string, data *api.ErrorData) []byte {
	if id == nil {
		id = nullID
	}
	b, err := marshal(errorFrame{api.JSONRPCVersion, id, errorBody{code, message, data}})
	if err != nil { // cannot happen: only strings and ints
		b = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"Internal error"}}`)
	}
	return append(b, '\n')
}

func resultLine(id idJSON, result []byte) []byte {
	out := make([]byte, 0, len(result)+len(id)+40)
	out = append(out, `{"jsonrpc":"2.0","id":`...)
	out = append(out, id...)
	out = append(out, `,"result":`...)
	out = append(out, result...)
	out = append(out, '}', '\n')
	return out
}
