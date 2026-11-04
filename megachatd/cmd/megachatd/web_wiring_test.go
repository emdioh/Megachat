package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/web"
)

type recDispatcher struct {
	mu      sync.Mutex
	results map[string]any
	last    map[string]json.RawMessage
}

func (d *recDispatcher) Call(_ context.Context, name string, params json.RawMessage) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.last[name] = append(json.RawMessage(nil), params...)
	if r, ok := d.results[name]; ok {
		return r, nil
	}
	return map[string]any{"ok": true}, nil
}
func (d *recDispatcher) Has(string) bool { return true }
func (d *recDispatcher) Names() []string { return nil }

var _ ops.Dispatcher = (*recDispatcher)(nil)

// wiredWeb is a web server wired with the production adapters (the real
// upload guard and QR renderer), as cmd/megachatd/app.go does.
func wiredWeb(t *testing.T) (*httptest.Server, *recDispatcher) {
	t.Helper()
	d := &recDispatcher{results: map[string]any{}, last: map[string]json.RawMessage{}}
	b := bus.New()
	cfg := config.NewWith(t.TempDir(), t.TempDir(), nil)
	s := web.NewServer(cfg, d, b, web.WithToken("tok"),
		web.WithStaticFS(fstest.MapFS{"index.html": {Data: []byte("x")}}),
		web.WithUploadDir(t.TempDir()), web.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		web.WithUploadGuard(uploadGuard{}), web.WithQRRenderer(qrRenderer{}))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); b.Close() })
	return ts, d
}

func TestLinkQRRouteRendersPNG(t *testing.T) {
	ts, d := wiredWeb(t)
	d.results["link.start"] = map[string]any{"qr": map[string]any{"kind": "url", "data": "https://example.com/x"}}
	req, _ := http.NewRequest("POST", ts.URL+"/api/link/start", strings.NewReader(`{"protocol":"signal"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		QR struct{ Kind, Data string } `json:"qr"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.QR.Kind != "png" {
		t.Fatalf("qr kind %q", out.QR.Kind)
	}
	raw, err := base64.StdEncoding.DecodeString(out.QR.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
}

// An oversize image is rejected by the real media guard while the upload is
// still being copied (413 -> a rejected attachment, nothing handed on).
func TestSendRejectsOversizeMediaMidUpload(t *testing.T) {
	ts, d := wiredWeb(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h2 := textproto.MIMEHeader{}
	h2.Set("Content-Disposition", `form-data; name="file"; filename="ok.png"`)
	h2.Set("Content-Type", "image/png")
	pw2, _ := w.CreatePart(h2)
	_, _ = pw2.Write([]byte("\x89PNG\r\n\x1a\nsmall"))
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="big.png"`)
	h.Set("Content-Type", "image/png")
	pw, _ := w.CreatePart(h)
	_, _ = pw.Write([]byte("\x89PNG\r\n\x1a\n"))
	_, _ = pw.Write(make([]byte, 21<<20)) // > 20 MiB image limit
	_ = w.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/send", &buf)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var sent struct {
		Attachments []struct {
			Path     string
			Rejected *string
		}
	}
	d.mu.Lock()
	raw := d.last["messages.send"]
	d.mu.Unlock()
	if err := json.Unmarshal(raw, &sent); err != nil || len(sent.Attachments) != 2 {
		t.Fatalf("status %d body %s send %s", resp.StatusCode, body, raw)
	}
	if sent.Attachments[1].Rejected == nil || sent.Attachments[1].Path != "" {
		t.Errorf("oversize image not rejected: %s", raw)
	}
	if sent.Attachments[0].Rejected != nil {
		t.Errorf("small image rejected: %s", raw)
	}
}
