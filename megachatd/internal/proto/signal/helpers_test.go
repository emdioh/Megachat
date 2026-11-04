package signal

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

const testNumber = "+390000000000"

type testCfg struct{ dir string }

func (c testCfg) SignalUserNumber() string        { return testNumber }
func (c testCfg) SignalCLIBinDir() string         { return filepath.Join(c.dir, "bin") }
func (c testCfg) SignalCLIAttachmentsDir() string { return filepath.Join(c.dir, "attachments") }
func (c testCfg) SignalCLIAvatarsDir() string     { return filepath.Join(c.dir, "avatars") }
func (c testCfg) QuoteThumbsDir() string          { return filepath.Join(c.dir, "quote-thumbs") }
func (c testCfg) AddressBookTTLS() int            { return 300 }

type rpcCall struct {
	Method string
	Params map[string]any
}

type rpcFail string

// fakeDaemon is an httptest stand-in for the signal-cli HTTP daemon.
type fakeDaemon struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []rpcCall
	handlers map[string]func(params map[string]any) any
	sseQuery []string
	sse      chan string
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{handlers: map[string]func(map[string]any) any{}, sse: make(chan string, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/rpc", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		d.mu.Lock()
		d.calls = append(d.calls, rpcCall{req.Method, req.Params})
		h := d.handlers[req.Method]
		d.mu.Unlock()
		var res any = map[string]any{}
		if h != nil {
			res = h(req.Params)
		}
		w.Header().Set("Content-Type", "application/json")
		if f, ok := res.(rpcFail); ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -1, "message": string(f)}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	})
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.sseQuery = append(d.sseQuery, r.URL.RawQuery)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, ":\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case s := <-d.sse:
				_, _ = io.WriteString(w, "event:receive\ndata:"+s+"\n\n")
				w.(http.Flusher).Flush()
			}
		}
	})
	d.srv = httptest.NewServer(mux)
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDaemon) handle(method string, h func(map[string]any) any) {
	d.mu.Lock()
	d.handlers[method] = h
	d.mu.Unlock()
}

func (d *fakeDaemon) callsOf(method string) []rpcCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []rpcCall
	for _, c := range d.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (d *fakeDaemon) pushEnvelope(t *testing.T, env map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"envelope": env})
	d.sse <- string(b)
}

var twoContacts = []any{
	map[string]any{"number": "+391234567890", "name": "Mario", "uuid": "uuid-123"},
	map[string]any{"number": "+391111111111", "givenName": "Luigi", "uuid": "uuid-456"},
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type harness struct {
	b   *Backend
	st  *store.Store
	cfg testCfg
	d   *fakeDaemon
}

// newHarness builds a backend wired to a real store and a fake daemon. The
// backend is NOT connected; tests call connect() or use setContacts.
func newHarness(t *testing.T, mut ...func(*Options)) *harness {
	t.Helper()
	cfg := testCfg{dir: t.TempDir()}
	st, err := store.Open(filepath.Join(cfg.dir, "messages.db"), store.WithLogger(quietLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := newFakeDaemon(t)
	d.handle("listContacts", func(map[string]any) any { return twoContacts })
	o := Options{
		Config: cfg, Store: st, Logger: quietLogger(),
		DaemonURL: d.srv.URL + "/api/v1/rpc", SSEURL: d.srv.URL + "/api/v1/events",
		ProbeAttempts: 2, ProbeInterval: 5 * time.Millisecond, SSERetryDelay: 10 * time.Millisecond,
	}
	for _, m := range mut {
		m(&o)
	}
	b := New(o)
	return &harness{b: b, st: st, cfg: cfg, d: d}
}

func (h *harness) connect(t *testing.T) {
	t.Helper()
	if err := h.b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.b.Disconnect(context.Background()) })
}

// offline sets contacts without a daemon round trip and marks the daemon up.
func (h *harness) offline(cs ...proto.Contact) {
	h.b.setContacts(cs)
	h.b.useDaemon.Store(true)
}

func contact(id, name, aci string) proto.Contact { return newContact(id, name, aci) }

func recv(t *testing.T, ch <-chan proto.Event) proto.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
	return proto.Event{}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeCLI(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/fake-signal-cli.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func jsonEnv(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}
