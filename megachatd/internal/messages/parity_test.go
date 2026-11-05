package messages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// repoRoot finds the Megachat checkout (the directory holding server/messages.py).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Skip("no working directory")
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "server", "messages.py")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("Python server/messages.py not found; parity test skipped")
		}
		dir = parent
	}
}

func contactSpec(m map[string][]proto.Contact) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for p, list := range m {
		for _, c := range list {
			out[p] = append(out[p], map[string]any{
				"id": c.ID, "display_name": c.DisplayName, "protocol": c.Protocol, "extras": c.Extras,
			})
		}
	}
	return out
}

// runPython runs testdata/parity.py and returns the raw JSON per call name.
func runPython(t *testing.T, e *env, calls []map[string]any) map[string]json.RawMessage {
	t.Helper()
	root := repoRoot(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	probe := exec.Command(py, "-I", "-c", fmt.Sprintf("import sys; sys.path.insert(0, %q); import server.messages", root))
	probe.Dir = t.TempDir()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("python cannot import server.messages: %v\n%s", err, out)
	}
	spec := map[string]any{
		"db":                 e.cfg.DBFile(),
		"cache_dir":          e.cfg.CacheDir(),
		"signal_attachments": e.cfg.SignalCLIAttachmentsDir(),
		"lazy":               e.cfg.WALazyMedia(),
		"contacts":           contactSpec(e.contacts),
		"address_book":       contactSpec(e.addressBook),
		"jid_phone":          e.jidPhone,
		"calls":              calls,
	}
	specPath := filepath.Join(t.TempDir(), "spec.json")
	data, _ := json.Marshal(spec)
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "parity.py"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, "-I", script, root, specPath)
	cmd.Dir = t.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("parity.py: %v\n%s", err, stderr.String())
	}
	var res map[string]json.RawMessage
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("parity.py output: %v\n%.500s", err, out)
	}
	return res
}

// diffJSON reports the first differing element of two JSON arrays/objects.
func diffJSON(want, got []byte) string {
	var w, g any
	_ = json.Unmarshal(want, &w)
	_ = json.Unmarshal(got, &g)
	if wa, ok := w.([]any); ok {
		ga, _ := g.([]any)
		if len(wa) != len(ga) {
			return fmt.Sprintf("length: python %d, go %d", len(wa), len(ga))
		}
		for i := range wa {
			wb, _ := json.Marshal(wa[i])
			gb, _ := json.Marshal(ga[i])
			if !bytes.Equal(wb, gb) {
				return fmt.Sprintf("element %d\npython: %s\ngo:     %s", i, want, got)
			}
		}
	}
	return fmt.Sprintf("python: %s\ngo:     %s", want, got)
}

// lidPlaceholderFields are the only wire fields that may differ between the
// Python row builder and the Go one. The divergence is deliberate and pinned
// by mc-zao.3.5.3/5.7: for an unresolved "@lid" group sender the Go resolver
// shows the annotated placeholder (core.UnresolvedLIDName) while Python shows
// the bare LID digits, which would read like a phone number the LID is not.
var lidPlaceholderFields = map[string]bool{"sender": true, "quote_author": true}

// lidPlaceholderDiverges matches exactly the allowed pair: Python's bare LID
// digits against the Go placeholder naming those same digits. Anything else
// (a placeholder where Python resolved a name, a different number, a raw @lid)
// is not on the allowlist and fails the byte comparison.
func lidPlaceholderDiverges(py, goV any) bool {
	digits, ok := py.(string)
	if !ok || digits == "" || strings.ContainsAny(digits, "@+ ") {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	g, ok := goV.(string)
	return ok && g == core.UnresolvedLIDName(digits)
}

// jsonValue is a decoded JSON value that keeps object key order. The @lid
// allowlist below rewrites the Python payload and re-encodes it, and
// encoding/json would sort the keys of a map: that would quietly drop key order
// (part of the wire bytes) from the comparison.
type jsonValue struct {
	kind   byte // '{', '[' or 0 for a scalar
	fields []jsonField
	elems  []*jsonValue
	leaf   any // string, json.Number, bool, nil
}

type jsonField struct {
	key string
	val *jsonValue
}

func decodeJSONValue(t *testing.T, raw []byte) *jsonValue {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJSONToken(dec)
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func decodeJSONToken(dec *json.Decoder) (*jsonValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return &jsonValue{leaf: tok}, nil
	}
	v := &jsonValue{kind: byte(delim)}
	for dec.More() {
		if delim == '{' {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			val, err := decodeJSONToken(dec)
			if err != nil {
				return nil, err
			}
			v.fields = append(v.fields, jsonField{key: kt.(string), val: val})
			continue
		}
		val, err := decodeJSONToken(dec)
		if err != nil {
			return nil, err
		}
		v.elems = append(v.elems, val)
	}
	if _, err := dec.Token(); err != nil { // closing delimiter
		return nil, err
	}
	return v, nil
}

func (v *jsonValue) encode(dst *bytes.Buffer) {
	switch v.kind {
	case '{':
		dst.WriteByte('{')
		for i, f := range v.fields {
			if i > 0 {
				dst.WriteByte(',')
			}
			appendJSONString(dst, f.key)
			dst.WriteByte(':')
			f.val.encode(dst)
		}
		dst.WriteByte('}')
	case '[':
		dst.WriteByte('[')
		for i, e := range v.elems {
			if i > 0 {
				dst.WriteByte(',')
			}
			e.encode(dst)
		}
		dst.WriteByte(']')
	default:
		if s, ok := v.leaf.(string); ok {
			appendJSONString(dst, s)
			return
		}
		b, _ := json.Marshal(v.leaf)
		dst.Write(b)
	}
}

func appendJSONString(dst *bytes.Buffer, s string) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	dst.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

func (v *jsonValue) bytes() []byte {
	var buf bytes.Buffer
	v.encode(&buf)
	return buf.Bytes()
}

// rewriteLIDPlaceholders applies the allowlist to the decoded Python payload:
// only the fields in lidPlaceholderFields are touched, and only where
// lidPlaceholderDiverges holds. Every other field keeps Python's bytes.
func rewriteLIDPlaceholders(py, goV *jsonValue) {
	switch {
	case py.kind == '{' && goV.kind == '{':
		for i := range py.fields {
			field := &py.fields[i]
			for j := range goV.fields {
				gf := &goV.fields[j]
				if gf.key != field.key {
					continue
				}
				if lidPlaceholderFields[field.key] && lidPlaceholderDiverges(field.val.leaf, gf.val.leaf) {
					field.val = gf.val
				} else {
					rewriteLIDPlaceholders(field.val, gf.val)
				}
				break
			}
		}
	case py.kind == '[' && goV.kind == '[' && len(py.elems) == len(goV.elems):
		for i := range py.elems {
			rewriteLIDPlaceholders(py.elems[i], goV.elems[i])
		}
	}
}

// allowLIDPlaceholder returns the Python payload with the mc-zao.3.5.3/5.7
// divergence rewritten, ready for the byte-for-byte comparison. The Python
// payload must round-trip through this test's encoder unchanged; otherwise the
// rewrite could hide an encoding difference and the test refuses to guess.
func allowLIDPlaceholder(t *testing.T, want, got []byte) []byte {
	t.Helper()
	w := decodeJSONValue(t, want)
	g := decodeJSONValue(t, got)
	if reencoded := w.bytes(); !bytes.Equal(reencoded, want) {
		t.Fatalf("python payload does not round-trip byte for byte through this test's encoder; refusing to apply the @lid allowlist\npython:    %s\nre-encoded: %s", want, reencoded)
	}
	rewriteLIDPlaceholders(w, g)
	return w.bytes()
}

func compareCalls(t *testing.T, e *env, calls []map[string]any) {
	t.Helper()
	py := runPython(t, e, calls)
	for _, c := range calls {
		name := c["name"].(string)
		t.Run(name, func(t *testing.T) {
			var opName string
			params := map[string]any{}
			for k, v := range c {
				if k != "name" && k != "op" {
					params[k] = v
				}
			}
			switch c["op"] {
			case "page":
				opName = api.OpMessagesPage
			case "window":
				opName = api.OpMessagesWindow
			case "snapshot":
				opName = api.OpMessagesSnapshot
			}
			got := e.mustCall(opName, params)
			want, ok := py[name]
			if !ok {
				t.Fatalf("python returned no %q", name)
			}
			if want = allowLIDPlaceholder(t, want, got); !bytes.Equal(want, got) {
				t.Errorf("wire mismatch %s\n%s", name, diffJSON(want, got))
			}
		})
	}
}

// TestParityMessagesPage runs the Python row builder and the Go one on the same
// fixture database and compares the JSON byte for byte.
func TestParityMessagesPage(t *testing.T) {
	compareCalls(t, seedPageEnv(t, nil), pageCalls)
}

// TestParityMessagesPageEagerMedia is the same with lazy WhatsApp media off.
func TestParityMessagesPageEagerMedia(t *testing.T) {
	e := seedPageEnv(t, map[string]string{"WA_LAZY_MEDIA": "0"})
	compareCalls(t, e, []map[string]any{
		{"name": "wa_lid", "op": "page", "protocol": "whatsapp", "contact_id": waLid},
		{"name": "wa_group", "op": "page", "protocol": "whatsapp", "contact_id": waGrp},
	})
}
