package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

const testToken = "tok-123"

type callRec struct {
	Name   string
	Params json.RawMessage
}

type stub struct {
	mu      sync.Mutex
	calls   []callRec
	results map[string]any
	errs    map[string]error
}

func newStub() *stub { return &stub{results: map[string]any{}, errs: map[string]error{}} }

func (s *stub) Call(_ context.Context, name string, params json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, callRec{name, append(json.RawMessage(nil), params...)})
	if e := s.errs[name]; e != nil {
		return nil, e
	}
	if r, ok := s.results[name]; ok {
		return r, nil
	}
	return map[string]any{"ok": true}, nil
}
func (s *stub) Has(string) bool { return true }
func (s *stub) Names() []string { return nil }
func (s *stub) last() callRec   { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[len(s.calls)-1] }
func (s *stub) names() (out []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		out = append(out, c.Name)
	}
	return
}

var _ ops.Dispatcher = (*stub)(nil)

func testStatic() fstest.MapFS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<html>spa</html>")},
		"app.js":     {Data: []byte("console.log(1)")},
		"icon.svg":   {Data: []byte("<svg/>")},
	}
}

type env struct {
	srv *Server
	d   *stub
	bus *bus.Bus
	ts  *httptest.Server
}

func newEnv(t *testing.T, opts ...Option) *env {
	t.Helper()
	d := newStub()
	b := bus.New()
	cfg := config.NewWith(t.TempDir(), t.TempDir(), nil)
	all := append([]Option{WithToken(testToken), WithStaticFS(testStatic()),
		WithUploadDir(t.TempDir()), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))), WithTimers(50*time.Millisecond, 50*time.Millisecond)}, opts...)
	s := NewServer(cfg, d, b, all...)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); b.Close() })
	return &env{s, d, b, ts}
}

func (e *env) do(t *testing.T, method, path, body string, hdr ...string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func jsonEq(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("bad json %q: %v", got, err)
	}
	_ = json.Unmarshal([]byte(want), &w)
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("params = %s, want %s", gb, wb)
	}
}

func newTS(t *testing.T, s *Server) *httptest.Server {
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}
