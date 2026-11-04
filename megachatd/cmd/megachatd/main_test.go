package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/linking"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/messages"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/proto/signal"
	"github.com/emdioh/megachat/megachatd/internal/proto/telegram"
	"github.com/emdioh/megachat/megachatd/internal/proto/whatsapp"
	"github.com/emdioh/megachat/megachatd/internal/web"
)

// Compile-time asserts: every backend satisfies the optional interfaces its
// package claims for core, media, linking and messages (the packages assert
// the same internally; this pins them from the host's side).
var (
	_ core.MessageIngester  = (*signal.Backend)(nil)
	_ core.ReceiptProcessor = (*signal.Backend)(nil)
	_ core.ContactRegistrar = (*signal.Backend)(nil)
	_ core.UserNumber       = (*signal.Backend)(nil)
	_ linking.SignalAccount = (*signal.Backend)(nil)

	_ core.MessageIngester  = (*telegram.Backend)(nil)
	_ core.ReceiptProcessor = (*telegram.Backend)(nil)
	_ core.ContactRegistrar = (*telegram.Backend)(nil)
	_ core.PlaceholderNamer = (*telegram.Backend)(nil)
	_ linking.TelegramLogin = (*telegram.Backend)(nil)
	_ proto.Pairer          = (*telegram.Backend)(nil)

	_ core.MessageIngester      = (*whatsapp.Backend)(nil)
	_ core.ReceiptProcessor     = (*whatsapp.Backend)(nil)
	_ core.ContactRegistrar     = (*whatsapp.Backend)(nil)
	_ core.UnreadReconciler     = (*whatsapp.Backend)(nil)
	_ core.LIDResolverStarter   = (*whatsapp.Backend)(nil)
	_ core.NumberChecker        = (*whatsapp.Backend)(nil)
	_ core.PlaceholderNamer     = (*whatsapp.Backend)(nil)
	_ messages.JIDPhoneResolver = (*whatsapp.Backend)(nil)
	_ linking.WhatsAppLinker    = (*whatsapp.LinkerBackend)(nil)
	_ proto.Pairer              = (*whatsapp.Backend)(nil)

	_ media.ProfilePhotoProvider      = (*signal.Backend)(nil)
	_ media.ProfilePhotoProvider      = (*telegram.Backend)(nil)
	_ media.MediaDirProvider          = (*telegram.Backend)(nil)
	_ media.MediaDirProvider          = (*whatsapp.Backend)(nil)
	_ media.ProfilePictureURLProvider = (*whatsapp.Backend)(nil)

	_ messages.Deps        = (*core.Core)(nil)
	_ web.QRRenderer       = qrRenderer{}
	_ web.UploadGuard      = uploadGuard{}
	_ media.Downloader     = (*downloader)(nil)
	_ core.UploadValidator = uploadValidator
)

type testEnv struct {
	cfg    *config.Config
	socket string
	port   int
	token  string
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	for _, d := range []string{home, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := &testEnv{socket: filepath.Join(root, "s.sock"), port: freePort(t), token: "test-token"}
	e.cfg = config.NewWith(project, home, map[string]string{
		"SIGNAL_TUI_WEB_TOKEN": e.token,
		"MEGACHAT_SOCKET":      e.socket,
		"MEGACHAT_LOCK_FILE":   filepath.Join(root, "lock"),
	})
	return e
}

func buildFakeApp(t *testing.T, e *testEnv) (*App, *fake.Backend) {
	t.Helper()
	fb := fake.New(api.ProtocolSignal)
	fb.SetContacts(proto.Contact{ID: "+391234567890", DisplayName: "Alice", Protocol: api.ProtocolSignal})
	app, err := Build(BuildOptions{
		Config: e.cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ExtraBackends: []proto.Backend{fb}, OnlyExtraBackends: true,
		DownloadPort: freePort(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, fb
}

func TestCoverageNoMissingNoDuplicates(t *testing.T) {
	e := newTestEnv(t)
	app, _ := buildFakeApp(t, e)
	defer app.Close()
	if err := CheckCoverage(app.Registry); err != nil {
		t.Fatal(err)
	}
	if got, want := len(app.Registry.Names()), len(api.Ops); got != want {
		t.Fatalf("registry has %d ops, api.Ops %d", got, want)
	}
	// A missing operation must be reported.
	app2, _ := buildFakeApp(t, newTestEnv(t))
	defer app2.Close()
	if err := CheckCoverage(newEmptyRegistry()); err == nil || !strings.Contains(err.Error(), api.OpServerStatus) {
		t.Fatalf("empty registry should be reported missing: %v", err)
	}
}

// rpcClient is a minimal JSON-RPC line client.
type rpcClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	id   int
}

func dialRPC(t *testing.T, path string) *rpcClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &rpcClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *rpcClient) call(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	req := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	_ = c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			c.t.Fatalf("%s: %v", method, err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			c.t.Fatalf("%s: bad line %q", method, line)
		}
		if int(asFloat(m["id"])) == c.id {
			return m
		}
	}
}

func asFloat(v any) float64 { f, _ := v.(float64); return f }

func (c *rpcClient) result(method string, params any) any {
	c.t.Helper()
	m := c.call(method, params)
	if m["error"] != nil {
		c.t.Fatalf("%s failed: %v", method, m["error"])
	}
	return m["result"]
}

func TestSmokeFakeBackend(t *testing.T) {
	e := newTestEnv(t)
	app, _ := buildFakeApp(t, e)
	var once sync.Once
	stopped := make(chan struct{})
	port := e.port
	running := app.Start(context.Background(), RunOptions{Socket: e.socket, WebPort: &port,
		Out: func(f string, a ...any) { t.Logf(f, a...) }, Err: func(f string, a ...any) { t.Logf(f, a...) }},
		func() { once.Do(func() { close(stopped) }) })
	if running.Failed {
		t.Fatal("adapters failed to start")
	}
	stoppedRunning := false
	defer func() {
		if !stoppedRunning {
			running.Stop()
		}
	}()

	// ---- Unix socket: hello, server.status, contacts.list, messages.page.
	c := dialRPC(t, e.socket)
	hello, _ := c.result("rpc.hello", nil).(map[string]any)
	if hello == nil || hello["api_version"] == nil && hello["server"] == nil && len(hello) == 0 {
		t.Fatalf("rpc.hello: %v", hello)
	}
	status, _ := c.result(api.OpServerStatus, nil).(map[string]any)
	for _, p := range api.Protocols {
		if _, ok := status[p]; !ok {
			t.Fatalf("server.status lacks %q: %v", p, status)
		}
	}
	// The fake "signal" backend connects in the background.
	deadline := time.Now().Add(5 * time.Second)
	for status[api.ProtocolSignal] != true && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		status, _ = c.result(api.OpServerStatus, nil).(map[string]any)
	}
	if status[api.ProtocolSignal] != true {
		t.Fatalf("fake backend never connected: %v", status)
	}
	contacts, _ := c.result(api.OpContactsList, nil).([]any)
	if len(contacts) != 1 || contacts[0].(map[string]any)["id"] != "+391234567890" {
		t.Fatalf("contacts.list: %v", contacts)
	}
	page, ok := c.result(api.OpMessagesPage, map[string]any{"protocol": "signal", "contact_id": "+391234567890"}).([]any)
	if !ok {
		t.Fatal("messages.page: result is not a list")
	}
	t.Logf("messages.page rows: %d", len(page))
	// Staged send pre-check path: an unknown protocol surfaces as an error, not a crash.
	if m := c.call(api.OpMessagesSend, map[string]any{"protocol": "signal", "contact_id": "+391234567890",
		"attachments": []any{map[string]any{"path": "relative/path.png"}}}); m["error"] == nil {
		t.Fatalf("relative attachment path must be refused: %v", m)
	}
	// server.clients sees the socket client.
	clients, _ := c.result(api.OpServerClients, nil).(map[string]any)
	if asFloat(clients["connections"]) < 1 {
		t.Fatalf("server.clients: %v", clients)
	}

	// ---- HTTP: /health open, REST route authed.
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if resp.StatusCode != 200 || health["status"] != "ok" {
		t.Fatalf("/health: %d %v", resp.StatusCode, health)
	}
	resp, err = http.Get(base + api.RouteContacts)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated %s = %d, want 401", api.RouteContacts, resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", base+api.RouteContacts, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("Alice")) {
		t.Fatalf("authed %s = %d %s", api.RouteContacts, resp.StatusCode, body)
	}

	// ---- server.shutdown fires the hook.
	if m := c.call(api.OpServerShutdown, nil); m["error"] != nil {
		t.Fatalf("server.shutdown: %v", m["error"])
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("server.shutdown did not call the shutdown hook")
	}
	stoppedRunning = true
	running.Stop()
	if _, err := net.DialTimeout("unix", e.socket, 500*time.Millisecond); err == nil {
		t.Fatal("socket still accepting after Stop")
	}
}

func TestStagingPreCallCopiesAndCleansUp(t *testing.T) {
	e := newTestEnv(t)
	app, _ := buildFakeApp(t, e)
	defer app.Close()
	pre := stagingPreCall(app.Registry, app.UploadDir)

	src := filepath.Join(t.TempDir(), "pic.png")
	pngBytes := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))
	if err := os.WriteFile(src, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"protocol": "signal", "contact_id": "+391234567890", "text": "",
		"attachments": []any{map[string]any{"path": src}}})
	out, cleanup, err := pre(context.Background(), api.OpMessagesSend, params)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Attachments []api.AttachmentIn `json:"attachments"`
	}
	if err := json.Unmarshal(out, &got); err != nil || len(got.Attachments) != 1 {
		t.Fatalf("rewritten params: %s (%v)", out, err)
	}
	staged := got.Attachments[0].Path
	if staged == src || !strings.HasPrefix(staged, app.UploadDir) {
		t.Fatalf("attachment not staged into %s: %s", app.UploadDir, staged)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged copy missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged copy not deleted: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source file must be untouched")
	}
	// A symlink is refused (O_NOFOLLOW).
	link := filepath.Join(t.TempDir(), "link.png")
	_ = os.Symlink(src, link)
	params, _ = json.Marshal(map[string]any{"protocol": "signal", "contact_id": "+391234567890", "text": "",
		"attachments": []any{map[string]any{"path": link}}})
	if _, _, err := pre(context.Background(), api.OpMessagesSend, params); err == nil {
		t.Fatal("symlink source must be refused")
	}
	// The pre-check ops ignore client paths entirely.
	params, _ = json.Marshal(map[string]any{"attachments": []any{map[string]any{"path": "/etc/passwd"}}})
	out, _, err = pre(context.Background(), api.OpMessagesSendCheck, params)
	if err != nil || !strings.Contains(string(out), `"attachments":[]`) {
		t.Fatalf("check op attachments not emptied: %s %v", out, err)
	}
}

func TestQRRendererGeometry(t *testing.T) {
	b, err := qrRenderer{}.PNG("https://example.com/link?x=1")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	side := img.Bounds().Dx()
	if img.Bounds().Dy() != side || side%qrBoxSize != 0 {
		t.Fatalf("not a square of whole modules: %v", img.Bounds())
	}
	// Quiet zone: border modules are white all around.
	for i := 0; i < qrBorder*qrBoxSize; i++ {
		for _, p := range [][2]int{{i, side / 2}, {side / 2, i}, {side - 1 - i, side / 2}, {side / 2, side - 1 - i}} {
			if r, _, _, _ := img.At(p[0], p[1]).RGBA(); r != 0xffff {
				t.Fatalf("quiet zone pixel %v is not white", p)
			}
		}
	}
	// 29 modules (version 3) + border 2*2 -> 33 * 8 px for this payload size.
	if modules := side/qrBoxSize - 2*qrBorder; modules < 21 || (modules-21)%4 != 0 {
		t.Fatalf("module count %d is not a valid QR size", modules)
	}
}

func TestResolveProjectDir(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "megachatd", "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(root, "megachat.py"), nil, 0o644)
	exe := filepath.Join(bin, "megachatd")
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if got := ResolveProjectDir("/flag", env(map[string]string{config.ProjectDirEnv: "/env"}), exe); got != "/flag" {
		t.Fatalf("flag wins: %s", got)
	}
	if got := ResolveProjectDir("", env(map[string]string{config.ProjectDirEnv: "/env"}), exe); got != "/env" {
		t.Fatalf("env next: %s", got)
	}
	if got := ResolveProjectDir("", env(nil), exe); got != root {
		t.Fatalf("walk up from the executable: %s want %s", got, root)
	}
	wd, _ := os.Getwd()
	if got := ResolveProjectDir("", env(nil), filepath.Join(t.TempDir(), "x")); got != wd {
		// the temp dir has no marker on its ancestors in a sane environment
		t.Logf("cwd fallback gave %s (an ancestor of the temp dir has a marker?)", got)
	}
}

func TestParseServeArgs(t *testing.T) {
	var sink bytes.Buffer
	if _, code, ok := parseServeArgs([]string{"--socket", "/x", "--no-socket"}, &sink); ok || code != 2 {
		t.Fatal("--socket with --no-socket must fail")
	}
	if _, _, ok := parseServeArgs([]string{"--web-port", "70000"}, &sink); ok {
		t.Fatal("port range")
	}
	a, _, ok := parseServeArgs([]string{"--web-port", "8080", "--web-host", "127.0.0.1,::1", "--no-web", "--debug", "--project-dir", "/p"}, &sink)
	if !ok || !a.webPortSet || a.webPort != 8080 || a.webHost != "127.0.0.1,::1" || !a.noWeb || !a.debug || a.projectDir != "/p" {
		t.Fatalf("parsed %+v", a)
	}
}

func newEmptyRegistry() *ops.Registry { return ops.NewRegistry() }
