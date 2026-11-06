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
// by mc-zao.3.5.3/5.7/5.8: for an unresolved "@lid" group sender the Go side
// shows the annotated placeholder (core.UnresolvedLIDName) while Python shows
// the bare LID digits (messages.page's resolver fallback) or the raw
// "<n>@lid" JID (messages.window/messages.snapshot, verbatim cache rows). Either
// way the LID digits would read like a phone number the LID is not. A legacy
// raw "<n>@lid" cache row may also carry the name window/snapshot resolve at
// display time (mc-zao.5.8): Python never resolves those rows, so that name is
// accepted only where Python itself kept the raw JID.
var lidPlaceholderFields = map[string]bool{"sender": true, "quote_author": true}

// unresolvedLIDPrefix is what every core.UnresolvedLIDName value starts with: a
// value prefixed by it is a placeholder, never a resolved name. Derived from
// core so the allowlist cannot drift from the placeholder format.
var unresolvedLIDPrefix = strings.TrimSuffix(core.UnresolvedLIDName(""), ")")

// lidHead is the LID user named by a Python value that spells an unresolved
// "@lid" sender: the bare digits of messages.page's fallback, or the raw
// "<digits>@lid" JID that messages.window/snapshot return verbatim (mc-zao.5.8).
// A "<digits>:<device>@lid" head yields the bare user part, exactly as the Go
// placeholder does. A resolved name, another JID shape or another value is not
// on the allowlist.
func lidHead(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	head, cut := strings.CutSuffix(s, "@lid")
	if !cut {
		head = s
	}
	if head == "" {
		return "", false
	}
	parts := strings.Split(head, ":")
	if len(parts) > 2 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	return parts[0], true
}

// lidPlaceholderDiverges matches exactly the allowed pairs: Python naming an
// unresolved LID (bare digits, or the raw "<n>[@<device>]@lid" JID) against the
// Go placeholder naming that same user, and — only for the raw JID, which
// Python never resolves — against the name the display-time resolver found in
// window/snapshot (mc-zao.5.8). Anything else (a placeholder where Python
// resolved a name, a name over Python's bare digits, a different number, any
// other raw JID) is not on the allowlist and fails the byte comparison.
func lidPlaceholderDiverges(py, goV any) bool {
	head, ok := lidHead(py)
	if !ok {
		return false
	}
	g, ok := goV.(string)
	if !ok || g == "" {
		return false
	}
	if g == core.UnresolvedLIDName(head) {
		return true
	}
	s, _ := py.(string)
	if !strings.HasSuffix(s, "@lid") {
		return false
	}
	// Python kept the raw JID: Go may have resolved it to a name (never to a
	// JID, never to a placeholder for other digits).
	return !strings.Contains(g, "@") && !strings.HasPrefix(g, unresolvedLIDPrefix)
}

// TestLIDPlaceholderAllowlist pins the shape of the allowlist: the Python
// spellings of an unresolved LID (messages.page's bare digits, and the raw
// "<n>@lid" cache row of window/snapshot) may differ only as described above,
// and only a raw JID may be replaced by a resolved name (mc-zao.5.8).
func TestLIDPlaceholderAllowlist(t *testing.T) {
	for _, c := range []struct {
		name string
		py   any
		goV  any
		want bool
	}{
		{"page bare digits", "111", "Contatto WhatsApp (ID 111)", true},
		{"window raw lid", "111@lid", "Contatto WhatsApp (ID 111)", true},
		{"device suffix", "111:2@lid", "Contatto WhatsApp (ID 111)", true},
		{"bare digits with device", "111:2", "Contatto WhatsApp (ID 111)", true},
		{"window raw lid resolved", "111@lid", "Ann", true},
		{"device suffix resolved", "111:2@lid", "Ann", true},
		{"different digits", "112@lid", "Contatto WhatsApp (ID 111)", false},
		{"different digits bare", "112", "Contatto WhatsApp (ID 111)", false},
		{"resolved name", "Ann", "Contatto WhatsApp (ID 111)", false},
		{"name over bare digits", "111", "Ann", false},
		{"name over device digits", "111:2", "Ann", false},
		{"raw lid, other placeholder", "111@lid", "Contatto WhatsApp (ID 112)", false},
		{"raw lid vs raw lid", "111@lid", "222@lid", false},
		{"c.us jid", "111@c.us", "Contatto WhatsApp (ID 111)", false},
		{"c.us jid resolved", "111@c.us", "Ann", false},
		{"nested suffix", "111@lid@lid", "Contatto WhatsApp (ID 111)", false},
		{"empty head", "@lid", "Contatto WhatsApp (ID )", false},
		{"not a string", 111, "Contatto WhatsApp (ID 111)", false},
		{"both names", "Ann", "Ann", false},
	} {
		if got := lidPlaceholderDiverges(c.py, c.goV); got != c.want {
			t.Errorf("%s: lidPlaceholderDiverges(%v, %v) = %v, want %v", c.name, c.py, c.goV, got, c.want)
		}
	}
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
	e := seedPageEnv(t, map[string]string{"WHATSAPP_LAZY_MEDIA": "0"})
	compareCalls(t, e, []map[string]any{
		{"name": "wa_lid", "op": "page", "protocol": "whatsapp", "contact_id": waLid},
		{"name": "wa_group", "op": "page", "protocol": "whatsapp", "contact_id": waGrp},
	})
}
