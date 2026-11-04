package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/emdioh/megachat/megachatd/internal/bus"
)

func wsURL(e *env, path string) string { return "ws" + strings.TrimPrefix(e.ts.URL, "http") + path }

func dial(t *testing.T, e *env, path string, hdr http.Header, protos ...string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, resp, err := websocket.Dial(ctx, wsURL(e, path), &websocket.DialOptions{HTTPHeader: hdr, Subprotocols: protos})
	if c != nil {
		t.Cleanup(func() { c.CloseNow() })
	}
	return c, resp, err
}

func bearer() http.Header { return http.Header{"Authorization": {"Bearer " + testToken}} }

func readFrame(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

// nextEvent skips heartbeats.
func nextEvent(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f := readFrame(t, c); f["type"] != "heartbeat" {
			return f
		}
	}
	t.Fatal("only heartbeats")
	return nil
}

func tokenProtocol(tok string) string {
	return "signal-tui-token." + base64.RawURLEncoding.EncodeToString([]byte(tok))
}

func TestWSAuth(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/ws", "/ws/v2"} {
		_, resp, err := dial(t, e, path, nil)
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Errorf("%s unauthenticated: %v %v", path, err, resp)
		}
		_, resp, err = dial(t, e, path, http.Header{"Authorization": {"Bearer wrong"}})
		if err == nil || resp.StatusCode != 403 {
			t.Errorf("%s wrong token: %v", path, err)
		}
		_, resp, err = dial(t, e, path, nil, "signal-tui-bearer", tokenProtocol("wrong"))
		if err == nil || resp.StatusCode != 403 {
			t.Errorf("%s wrong subprotocol token: %v", path, err)
		}
		_, resp, err = dial(t, e, path, nil, "signal-tui-bearer", "signal-tui-token.@@@")
		if err == nil || resp.StatusCode != 403 {
			t.Errorf("%s bad base64: %v", path, err)
		}
		// header form: accepted, no subprotocol echoed
		c, _, err := dial(t, e, path, bearer())
		if err != nil || c.Subprotocol() != "" {
			t.Errorf("%s header auth: %v %q", path, err, c.Subprotocol())
		}
		// browser form: signal-tui-bearer echoed (padding optional)
		for _, tok := range []string{tokenProtocol(testToken), tokenProtocol(testToken) + "="} {
			c, _, err = dial(t, e, path, nil, "signal-tui-bearer", tok)
			if err != nil || c.Subprotocol() != "signal-tui-bearer" {
				t.Errorf("%s subprotocol auth: %v", path, err)
			}
		}
	}
	// invalid since: refused before upgrade
	for _, q := range []string{"?since=abc", "?since=-1", "?since="} {
		if _, resp, err := dial(t, e, "/ws/v2"+q, bearer()); err == nil || resp.StatusCode != 403 {
			t.Errorf("since%s: %v", q, err)
		}
	}
}

func TestWSNoAuth(t *testing.T) {
	e := newEnv(t, WithNoAuth())
	if _, _, err := dial(t, e, "/ws", nil); err != nil {
		t.Error(err)
	}
	if _, _, err := dial(t, e, "/ws/v2", nil); err != nil {
		t.Error(err)
	}
}

func TestLegacyFramesInOrderAndHeartbeat(t *testing.T) {
	e := newEnv(t)
	c, _, err := dial(t, e, "/ws", bearer())
	if err != nil {
		t.Fatal(err)
	}
	if f := readFrame(t, c); len(f) != 1 || f["type"] != "heartbeat" {
		t.Fatalf("first frame %v", f)
	}
	e.bus.Publish(bus.BackendStatus, map[string]any{"protocol": "signal", "text": "x"}) // no frame
	e.bus.Publish(bus.Typing, map[string]any{"protocol": "signal", "contact_id": "+39", "action": "STARTED"})
	e.bus.Publish(bus.Message, map[string]any{"protocol": "signal", "contact_id": "+39",
		"message": map[string]any{"id": "m1", "is_mine": false}, "timestamp": 7, "added": false, "changed": false})
	e.bus.Publish(bus.Message, map[string]any{"protocol": "signal", "contact_id": "+39",
		"message": map[string]any{"id": "m2", "is_mine": true}, "timestamp": 8, "added": true, "changed": false})
	e.bus.Publish(bus.WebPush, map[string]any{"event": map[string]any{"type": "send_retry", "payload": map[string]any{"attempt": 2}}})
	want := []string{
		`{"type":"typing","payload":{"protocol":"signal","contact_id":"+39","action":"STARTED"}}`,
		`{"type":"message","payload":{"id":"m2","protocol":"signal","contact_id":"+39","timestamp":8,"is_mine":true}}`,
		`{"type":"send_retry","payload":{"attempt":2}}`,
	}
	for _, w := range want {
		got, _ := json.Marshal(nextEvent(t, c))
		jsonEq(t, got, w)
	}
	// legacy socket never replays old events
	c2, _, _ := dial(t, e, "/ws", bearer())
	if f := nextEventOrHeartbeat(t, c2); f["type"] != "heartbeat" {
		t.Errorf("replayed: %v", f)
	}
}

func nextEventOrHeartbeat(t *testing.T, c *websocket.Conn) map[string]any { return readFrame(t, c) }

func TestClientCount(t *testing.T) {
	e := newEnv(t)
	if e.srv.ClientCount() != 0 {
		t.Fatal("not zero")
	}
	c1, _, _ := dial(t, e, "/ws", bearer())
	c2, _, _ := dial(t, e, "/ws/v2", bearer())
	readFrame(t, c1)
	readFrame(t, c2)
	if n := e.srv.ClientCount(); n != 2 {
		t.Errorf("count %d", n)
	}
	c1.Close(websocket.StatusNormalClosure, "")
	c2.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for e.srv.ClientCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.srv.ClientCount(); n != 0 {
		t.Errorf("count after close %d", n)
	}
}

func typing(e *env, who string) bus.Event {
	return e.bus.Publish(bus.Typing, map[string]any{"protocol": "signal", "contact_id": who, "action": "STARTED"})
}

func seqs(frames []map[string]any) (out []int) {
	for _, f := range frames {
		out = append(out, int(f["seq"].(float64)))
	}
	return
}

func events(t *testing.T, c *websocket.Conn, n int) (out []map[string]any) {
	for len(out) < n {
		out = append(out, nextEvent(t, c))
	}
	return
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestV2LiveWireFrames(t *testing.T) {
	e := newEnv(t)
	c, _, _ := dial(t, e, "/ws/v2", bearer())
	if f := readFrame(t, c); len(f) != 1 || f["type"] != "heartbeat" {
		t.Fatal(f)
	}
	e.bus.Publish(bus.BackendStatus, map[string]any{"protocol": "signal", "text": "x"})
	typing(e, "+39")
	e.bus.Publish(bus.WebPush, map[string]any{"event": map[string]any{"type": "message", "payload": map[string]any{}}})
	fr := events(t, c, 3)
	types := []string{"backend_status", "typing", "web_push"}
	for i, f := range fr {
		if f["type"] != types[i] || int(f["seq"].(float64)) != i+1 {
			t.Errorf("frame %d: %v", i, f)
		}
		if _, ok := f["ts"].(float64); !ok || f["payload"] == nil || len(f) != 4 {
			t.Errorf("shape %v", f)
		}
	}
	if fr[2]["payload"].(map[string]any)["event"].(map[string]any)["type"] != "message" {
		t.Error("web_push payload altered")
	}
}

func TestV2SinceQueryReplaysThenLive(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 4; i++ {
		typing(e, "c"+strconv.Itoa(i))
	}
	c, _, _ := dial(t, e, "/ws/v2?since=2", bearer())
	live := typing(e, "live")
	got := seqs(events(t, c, 3))
	if !eqInts(got, []int{3, 4, int(live.Seq)}) {
		t.Errorf("seqs %v", got)
	}
}

func TestV2SubscribeFrame(t *testing.T) {
	e := newEnv(t, WithTimers(50*time.Millisecond, 400*time.Millisecond))
	for i := 0; i < 3; i++ {
		typing(e, "c")
	}
	c, _, _ := dial(t, e, "/ws/v2", bearer())
	ctx := context.Background()
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"subscribe","since":"1"}`))
	got := seqs(events(t, c, 2))
	typing(e, "next")
	got = append(got, seqs(events(t, c, 1))...)
	if !eqInts(got, []int{2, 3, 4}) {
		t.Errorf("seqs %v", got)
	}
}

func TestV2InvalidSubscribeAnswersErrorAndIsIgnored(t *testing.T) {
	e := newEnv(t, WithTimers(time.Hour, 300*time.Millisecond))
	typing(e, "old")
	c, _, _ := dial(t, e, "/ws/v2", bearer())
	ctx := context.Background()
	for _, bad := range []string{
		`{"type":"subscribe","since":true}`, `{"type":"subscribe","since":-1}`,
		`{"type":"subscribe","since":1.5}`, `{"type":"subscribe","since":"x"}`,
	} {
		_ = c.Write(ctx, websocket.MessageText, []byte(bad))
		f := readFrame(t, c)
		b, _ := json.Marshal(f)
		jsonEq(t, b, `{"type":"error","payload":{"reason":"invalid_subscribe"}}`)
	}
	// ignored silently: non-subscribe, non-JSON, other types
	for _, ign := range []string{`{"type":"hello"}`, `not json`, `[1]`} {
		_ = c.Write(ctx, websocket.MessageText, []byte(ign))
	}
	typing(e, "live")
	f := readFrame(t, c)
	if f["type"] != "typing" || f["payload"].(map[string]any)["contact_id"] != "live" {
		t.Errorf("%v", f)
	}
}

func TestV2GapWhenSinceFellOutOfBuffer(t *testing.T) {
	d := newStub()
	b := bus.New(bus.WithBufferSize(3))
	s := NewServer(nil, d, b, WithToken(testToken), WithTimers(50*time.Millisecond, 50*time.Millisecond))
	e := &env{srv: s, d: d, bus: b}
	ts := newTS(t, s)
	e.ts = ts
	for i := 0; i < 10; i++ {
		typing(e, "c")
	}
	c, _, _ := dial(t, e, "/ws/v2?since=2", bearer())
	fr := events(t, c, 4)
	if fr[0]["type"] != "gap" || fr[0]["payload"].(map[string]any)["reason"] != "buffer" ||
		int(fr[0]["payload"].(map[string]any)["since"].(float64)) != 2 {
		t.Errorf("%v", fr[0])
	}
	if !eqInts(seqs(fr[1:]), []int{8, 9, 10}) {
		t.Errorf("%v", seqs(fr))
	}
}

func TestSocketsWithoutBusHeartbeat(t *testing.T) {
	s := NewServer(nil, newStub(), nil, WithToken(testToken), WithTimers(30*time.Millisecond, 30*time.Millisecond))
	e := &env{srv: s, ts: newTS(t, s)}
	for _, p := range []string{"/ws", "/ws/v2"} {
		c, _, err := dial(t, e, p, bearer())
		if err != nil {
			t.Fatal(err)
		}
		if f := readFrame(t, c); f["type"] != "heartbeat" {
			t.Error(f)
		}
	}
}

func TestListenAndServeAndShutdown(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	d := newStub()
	b := bus.New()
	defer b.Close()
	s := NewServer(nil, d, b, WithToken(testToken), WithHost("127.0.0.1"), WithPort(port),
		WithUploadDir(t.TempDir()), WithStaticFS(testStatic()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	var resp *http.Response
	var err error
	for i := 0; i < 100; i++ {
		if resp, err = http.Get(url + "/health"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err)
	}
	var h map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&h)
	if int(h["port"].(float64)) != port || h["auth_required"] != true {
		t.Error(h)
	}
	wc, _, err := websocket.Dial(context.Background(), "ws://127.0.0.1:"+strconv.Itoa(port)+"/ws/v2",
		&websocket.DialOptions{HTTPHeader: bearer()})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}
	rctx, rc := context.WithTimeout(context.Background(), 2*time.Second)
	defer rc()
	for {
		if _, _, err := wc.Read(rctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusGoingAway {
				t.Errorf("ws close status %v", err)
			}
			break
		}
	}
}

func TestListenRequiresToken(t *testing.T) {
	s := NewServer(nil, newStub(), nil)
	if err := s.ListenAndServe(context.Background()); err == nil {
		t.Error("expected token error")
	}
}

func TestParseHosts(t *testing.T) {
	cases := map[string][]string{
		"127.0.0.1":                   {"127.0.0.1"},
		"127.0.0.1,192.168.1.10":      {"127.0.0.1", "192.168.1.10"},
		" 127.0.0.1 , ::1 ,127.0.0.1": {"127.0.0.1", "::1"},
		"":                            {"0.0.0.0"},
	}
	for in, want := range cases {
		got := ParseHosts(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%q: %v", in, got)
		}
	}
}
