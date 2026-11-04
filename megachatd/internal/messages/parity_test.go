package messages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
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
			if !bytes.Equal(want, got) {
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
