package signal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientCall(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("echo", func(p map[string]any) any { return []any{"ok"} })
	d.handle("boom", func(p map[string]any) any { return rpcFail("nope") })
	c := NewClient(d.srv.URL+"/api/v1/rpc", nil)

	r := c.Call(context.Background(), "echo", nil)
	if !r.OK() || !r.HasResult || r.Result.([]any)[0] != "ok" {
		t.Fatalf("%+v", r)
	}
	if r := c.Call(context.Background(), "boom", nil); r.OK() || r.Err != "nope" {
		t.Fatalf("%+v", r)
	}
	// transport error -> Err, never a panic
	bad := NewClient("http://127.0.0.1:1/x", nil)
	if r := bad.Call(context.Background(), "echo", nil); r.OK() || r.Err == "" {
		t.Fatalf("%+v", r)
	}
	// ids increment, params default to {}
	calls := d.callsOf("echo")
	if len(calls) != 1 || len(calls[0].Params) != 0 {
		t.Fatalf("%+v", calls)
	}
}

func TestClientHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) }))
	defer srv.Close()
	r := NewClient(srv.URL, nil).Call(context.Background(), "m", nil)
	if r.OK() || !strings.Contains(r.Err, "500") {
		t.Fatalf("%+v", r)
	}
}

func TestListContacts(t *testing.T) {
	d := newFakeDaemon(t)
	d.handle("listContacts", func(map[string]any) any { return []any{map[string]any{"number": "+39"}} })
	c := NewClient(d.srv.URL+"/api/v1/rpc", nil)
	got := c.ListContacts(context.Background())
	if len(got) != 1 || got[0]["number"] != "+39" {
		t.Fatalf("%v", got)
	}
	d.handle("listContacts", func(map[string]any) any { return rpcFail("timeout") })
	if got := c.ListContacts(context.Background()); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestSendParamsWire(t *testing.T) {
	qts, qa, qm := int64(1234), "+391234567890", ""
	p := SendParams{Message: "ciao", Recipient: "+391234567890", QuoteTimestamp: &qts, QuoteAuthor: &qa,
		QuoteMessage: &qm, QuoteAttachments: []string{"image/png:photo.png:/tmp/photo.png"}}
	w := p.wire()
	if w["quoteMessage"] != "" || w["quoteTimestamp"] != int64(1234) {
		t.Fatalf("%v", w)
	}
	if w["quoteAttachments"].([]string)[0] != "image/png:photo.png:/tmp/photo.png" {
		t.Fatalf("%v", w)
	}
	if r := w["recipient"].([]string); len(r) != 1 || r[0] != "+391234567890" {
		t.Fatalf("%v", w)
	}
	bare := SendParams{Message: "ciao", Recipient: "x"}.wire()
	for _, k := range []string{"quoteAttachments", "quoteTimestamp", "quoteAuthor", "quoteMessage", "editTimestamp", "attachments", "timestamp"} {
		if _, ok := bare[k]; ok {
			t.Fatalf("%s must be omitted: %v", k, bare)
		}
	}
}

func TestSendReactionRPC(t *testing.T) {
	d := newFakeDaemon(t)
	c := NewClient(d.srv.URL+"/api/v1/rpc", nil)
	d.handle("sendReaction", func(map[string]any) any {
		return map[string]any{"results": []any{map[string]any{"type": "NETWORK_FAILURE"}, map[string]any{"type": "SUCCESS"}}}
	})
	if !c.SendReaction(context.Background(), "+39", "👍", "+39", 5) {
		t.Fatal("expected success")
	}
	p := d.callsOf("sendReaction")[0].Params
	if p["emoji"] != "👍" || p["targetAuthor"] != "+39" {
		t.Fatalf("%v", p)
	}
	d.handle("sendReaction", func(map[string]any) any { return map[string]any{"results": []any{}} })
	if c.SendReaction(context.Background(), "+39", "👍", "+39", 5) {
		t.Fatal("no SUCCESS -> false")
	}
	d.handle("sendReaction", func(map[string]any) any { return rpcFail("x") })
	if c.SendReaction(context.Background(), "+39", "👍", "+39", 5) {
		t.Fatal("error -> false")
	}
}

func sseServer(t *testing.T, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, url string) []map[string]any {
	var mu sync.Mutex
	var got []map[string]any
	ListenEvents(context.Background(), nil, url, "+391234567890", 5*time.Second, func(m map[string]any) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	})
	return got
}

func TestListenEvents(t *testing.T) {
	t.Run("yields envelope", func(t *testing.T) {
		srv := sseServer(t, "event:receive\ndata:{\"envelope\":{\"source\":\"+39\"}}\n\n:keep-alive\n")
		got := collect(t, srv.URL)
		if len(got) != 1 || got[0]["envelope"].(map[string]any)["source"] != "+39" {
			t.Fatalf("%v", got)
		}
	})
	t.Run("skips keepalive and handles CRLF", func(t *testing.T) {
		srv := sseServer(t, ":\r\n:\r\nevent:receive\r\ndata: {\"envelope\":{\"source\":\"+39\"}}\r\n\r\n")
		if got := collect(t, srv.URL); len(got) != 1 {
			t.Fatalf("%v", got)
		}
	})
	t.Run("connection error", func(t *testing.T) {
		if got := collect(t, "http://127.0.0.1:1"); len(got) != 0 {
			t.Fatalf("%v", got)
		}
	})
	t.Run("bad json skipped", func(t *testing.T) {
		srv := sseServer(t, "event:receive\ndata:not-valid-json\n\nevent:receive\ndata:{\"envelope\":{\"source\":\"+39\"}}\n\n")
		if got := collect(t, srv.URL); len(got) != 1 {
			t.Fatalf("%v", got)
		}
	})
	t.Run("multi-line data is joined", func(t *testing.T) {
		srv := sseServer(t, "data:{\"envelope\":\ndata:{\"source\":\"+39\"}}\n\n")
		if got := collect(t, srv.URL); len(got) != 1 {
			t.Fatalf("%v", got)
		}
	})
	t.Run("account query is not escaped", func(t *testing.T) {
		var q string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { q = r.URL.RawQuery }))
		defer srv.Close()
		collect(t, srv.URL)
		if q != "account=+391234567890" {
			t.Fatal(q)
		}
	})
	t.Run("idle stream times out", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer srv.Close()
		start := time.Now()
		ListenEvents(context.Background(), nil, srv.URL, "+39", 100*time.Millisecond, func(map[string]any) {})
		if time.Since(start) > 3*time.Second {
			t.Fatal("did not time out")
		}
	})
}

func TestFindSignalCLI(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindSignalCLI(filepath.Join(dir, "none")); err != ErrCLINotFound {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "signal-cli-0.13", "bin", "signal-cli")
	writeFile(t, exe, "#!/bin/sh\n")
	if _, err := FindSignalCLI(dir); err != ErrCLINotFound {
		t.Fatalf("non-executable must not be found: %v", err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := FindSignalCLI(dir); err != nil || got != exe {
		t.Fatal(got, err)
	}
}
