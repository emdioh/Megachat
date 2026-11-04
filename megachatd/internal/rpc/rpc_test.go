package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// stubDispatcher is a ops.Registry with test operations.
func newRegistry(t testing.TB) (*ops.Registry, chan struct{}) {
	t.Helper()
	r := ops.NewRegistry()
	block := make(chan struct{})
	reg := func(name string, h ops.Handler) { r.Register(ops.Op{Name: name, Handler: h}) }
	reg("test.echo", func(ctx context.Context, p json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		return map[string]any{"echo": m}, nil
	})
	reg("test.fail", func(ctx context.Context, p json.RawMessage) (any, error) {
		return nil, &ops.Error{Code: api.CodeConflict, Message: "Nope è", Details: map[string]any{"k": 1}}
	})
	reg("test.invalid", func(ctx context.Context, p json.RawMessage) (any, error) {
		return nil, &ops.Error{Code: api.CodeInvalidRequest, Message: "Invalid request", Details: []any{
			map[string]any{"type": "missing", "loc": []any{"protocol"}, "msg": "Field required", "input": map[string]any{}, "url": "x"}}}
	})
	reg("test.boom", func(ctx context.Context, p json.RawMessage) (any, error) {
		return nil, fmt.Errorf("secret detail")
	})
	reg("test.panic", func(ctx context.Context, p json.RawMessage) (any, error) { panic("x") })
	reg("test.block", func(ctx context.Context, p json.RawMessage) (any, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return map[string]any{"done": true}, nil
	})
	reg("test.big", func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]any{"s": strings.Repeat("a", 3<<20)}, nil
	})
	reg("test.nodetails", func(ctx context.Context, p json.RawMessage) (any, error) {
		return nil, ops.Errorf(api.CodeNotFound, "gone")
	})
	return r, block
}

type env struct {
	t    *testing.T
	s    *Server
	bus  *bus.Bus
	path string
	reg  *ops.Registry
	blk  chan struct{}
	done chan error
}

func shortDir(t testing.TB) string {
	d, err := os.MkdirTemp("", "rpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func newEnv(t *testing.T, opts ...Option) *env {
	t.Helper()
	reg, blk := newRegistry(t)
	b := bus.New()
	path := filepath.Join(shortDir(t), "s", "server.sock")
	s := NewServer(path, reg, b, opts...)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, s: s, bus: b, path: path, reg: reg, blk: blk, done: make(chan error, 1)}
	go func() { e.done <- s.Serve(context.Background()) }()
	t.Cleanup(func() {
		select {
		case <-blk:
		default:
			close(blk)
		}
		s.Close()
	})
	return e
}

type client struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

func (e *env) dial() *client {
	nc, err := net.Dial("unix", e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { nc.Close() })
	return &client{e.t, nc, bufio.NewReaderSize(nc, 1<<20)}
}

func (c *client) sendRaw(b string) {
	c.t.Helper()
	if _, err := c.nc.Write([]byte(b)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) send(v any) {
	b, _ := json.Marshal(v)
	c.sendRaw(string(b) + "\n")
}

func (c *client) line(d time.Duration) (string, error) {
	_ = c.nc.SetReadDeadline(time.Now().Add(d))
	return c.r.ReadString('\n')
}

func (c *client) recvLine() string {
	c.t.Helper()
	l, err := c.line(5 * time.Second)
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	return strings.TrimSuffix(l, "\n")
}

func (c *client) recv() map[string]any {
	c.t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(c.recvLine()), &m); err != nil {
		c.t.Fatal(err)
	}
	return m
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return c.recv()
}

func errCode(m map[string]any) int {
	e, _ := m["error"].(map[string]any)
	f, _ := e["code"].(float64)
	return int(f)
}

func TestHelloPingAndCalls(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	c.sendRaw(`{"jsonrpc":"2.0","id":"a","method":"rpc.hello"}` + "\n")
	got := c.recvLine()
	if !strings.HasPrefix(got, `{"jsonrpc":"2.0","id":"a","result":{"api_version":1,"server_version":"0.1.0","operations":["test.big","test.block","test.boom"`) {
		t.Fatalf("hello: %s", got)
	}
	r := c.call("server.ping", nil)
	if r["result"].(map[string]any)["pong"] != true {
		t.Fatal(r)
	}
	// string id, null params
	c.sendRaw(`{"jsonrpc":"2.0","id":"x","method":"test.echo","params":null}` + "\n")
	if got := c.recvLine(); got != `{"jsonrpc":"2.0","id":"x","result":{"echo":{}}}` {
		t.Fatal(got)
	}
}

func TestErrorMapping(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	r := c.recvLineFor(`{"jsonrpc":"2.0","id":1,"method":"test.fail"}`)
	if r != `{"jsonrpc":"2.0","id":1,"error":{"code":-32005,"message":"Nope \u00e8","data":{"code":"conflict","message":"Nope \u00e8","details":{"k":1}}}}` {
		t.Fatal(r)
	}
	r = c.recvLineFor(`{"jsonrpc":"2.0","id":1,"method":"test.invalid"}`)
	if !strings.Contains(r, `"details":[{"type":"missing","loc":["protocol"],"msg":"Field required"}]`) || !strings.Contains(r, `-32602`) {
		t.Fatal(r)
	}
	r = c.recvLineFor(`{"jsonrpc":"2.0","id":1,"method":"test.nodetails"}`)
	if !strings.Contains(r, `"data":{"code":"not_found","message":"gone","details":null}`) {
		t.Fatal(r)
	}
	for _, m := range []string{"test.boom", "test.panic"} {
		r = c.recvLineFor(`{"jsonrpc":"2.0","id":1,"method":"` + m + `"}`)
		if r != `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"Internal error","data":{"code":"internal","message":"Internal error"}}}` {
			t.Fatal(r)
		}
	}
	r = c.recvLineFor(`{"jsonrpc":"2.0","id":1,"method":"nope'x"}`)
	if r != `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Unknown method \"nope'x\"","data":{"code":"method_not_found","message":"Unknown method \"nope'x\""}}}` {
		t.Fatal(r)
	}
	for code, rpc := range api.RPCCodeFor {
		if rpc > -32001 && rpc != -32602 && rpc != -32603 {
			t.Fatal(code, rpc)
		}
	}
}

func (c *client) recvLineFor(req string) string {
	c.sendRaw(req + "\n")
	return c.recvLine()
}

func TestParamsMustBeObject(t *testing.T) {
	c := newEnv(t).dial()
	r := c.call("test.echo", []any{1})
	if errCode(r) != -32602 {
		t.Fatal(r)
	}
}

func TestMalformedAndInvalidEnvelopes(t *testing.T) {
	c := newEnv(t).dial()
	c.sendRaw("{not json\n")
	r := c.recv()
	if errCode(r) != -32700 || r["id"] != nil {
		t.Fatal(r)
	}
	c.sendRaw("\xff\xfe\n")
	if errCode(c.recv()) != -32700 {
		t.Fatal("utf8")
	}
	c.sendRaw("\n\n  \n")
	if c.call("server.ping", nil)["result"] == nil {
		t.Fatal("ping")
	}
	for _, m := range []string{
		`[]`, `[{"jsonrpc":"2.0","id":1,"method":"server.ping"}]`, `"text"`, `5`,
		`{"id":1,"method":"server.ping"}`,
		`{"jsonrpc":"2.0","id":1.5,"method":"server.ping"}`,
		`{"jsonrpc":"2.0","id":true,"method":"server.ping"}`,
		`{"jsonrpc":"2.0","id":null,"method":"server.ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":5}`,
		`{"jsonrpc":"2.0","id":1,"method":"server.ping","timeout":-1}`,
		`{"jsonrpc":"2.0","id":1,"method":"server.ping","timeout":"5"}`,
		`{"jsonrpc":"2.0","id":1,"method":"server.ping","timeout":0}`,
		`{"jsonrpc":"2.0","id":1,"method":"server.ping","timeout":1000000}`,
		`{"jsonrpc":"2.0","method":"server.ping","timeout":1e999}`,
	} {
		c.sendRaw(m + "\n")
		r := c.recv()
		if errCode(r) != -32600 {
			t.Fatalf("%s -> %v", m, r)
		}
		if c.call("server.ping", nil)["result"] == nil {
			t.Fatal("ping after", m)
		}
	}
	c.sendRaw(`{"jsonrpc":"2.0","id":1,"method":"server.ping","timeout":1000000}` + "\n")
	if got := c.recvLine(); !strings.Contains(got, `"message":"timeout must be a number in (0, 600]"`) {
		t.Fatal(got)
	}
	c.sendRaw(`{"jsonrpc":"2.0","id":123456789012345678901234567890,"method":"server.ping"}` + "\n")
	if got := c.recvLine(); !strings.Contains(got, `"id":123456789012345678901234567890,`) {
		t.Fatal(got)
	}
}

func TestNotificationNotAnswered(t *testing.T) {
	e := newEnv(t)
	hit := make(chan struct{}, 1)
	e.reg.Register(ops.Op{Name: "test.hit", Handler: func(context.Context, json.RawMessage) (any, error) {
		hit <- struct{}{}
		return map[string]any{}, nil
	}})
	c := e.dial()
	c.sendRaw(`{"jsonrpc":"2.0","method":"test.hit"}` + "\n")
	select {
	case <-hit:
	case <-time.After(3 * time.Second):
		t.Fatal("not executed")
	}
	// notification with error: params error / unknown method not answered
	c.sendRaw(`{"jsonrpc":"2.0","method":"test.hit","params":[1]}` + "\n")
	c.sendRaw(`{"jsonrpc":"2.0","method":"nope"}` + "\n")
	// envelope error still answered with null id
	c.sendRaw(`{"jsonrpc":"1.0","method":"test.hit"}` + "\n")
	r := c.recv()
	if errCode(r) != -32600 || r["id"] != nil {
		t.Fatal(r)
	}
	if c.call("server.ping", nil)["result"] == nil {
		t.Fatal("ping")
	}
}

func TestOversizeLineClosesConnection(t *testing.T) {
	e := newEnv(t, WithMaxLineBytes(1000))
	c := e.dial()
	go func() { _, _ = c.nc.Write(bytes.Repeat([]byte("x"), 5000)) }()
	r := c.recv()
	if errCode(r) != -32004 || r["id"] != nil ||
		r["error"].(map[string]any)["message"] != "Request line exceeds 1000 bytes" {
		t.Fatal(r)
	}
	if _, err := c.line(3 * time.Second); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	// exactly at the limit is fine
	c2 := e.dial()
	pad := strings.Repeat(" ", 1000-len(`{"jsonrpc":"2.0","id":1,"method":"server.ping"}`))
	c2.sendRaw(`{"jsonrpc":"2.0","id":1,"method":"server.ping"}` + pad + "\n")
	if c2.recv()["result"] == nil {
		t.Fatal("limit line")
	}
}

func TestLargeResponseAndRequest(t *testing.T) {
	c := newEnv(t).dial()
	r := c.call("test.big", nil)
	if len(r["result"].(map[string]any)["s"].(string)) != 3<<20 {
		t.Fatal("big")
	}
	big := strings.Repeat("b", 2<<20)
	r = c.call("test.echo", map[string]any{"s": big})
	if r["result"].(map[string]any)["echo"].(map[string]any)["s"] != big {
		t.Fatal("echo")
	}
}

func TestHalfClosedClientStillAnswered(t *testing.T) {
	c := newEnv(t).dial()
	c.sendRaw(`{"jsonrpc":"2.0","id":1,"method":"test.big"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"server.ping"}` + "\n" + `{"jsonrpc":"2.0","id":3,"meth`)
	_ = c.nc.(*net.UnixConn).CloseWrite()
	seen := map[float64]bool{}
	for i := 0; i < 2; i++ {
		seen[c.recv()["id"].(float64)] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatal(seen)
	}
	if _, err := c.line(3 * time.Second); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestOutOfOrderAndOtherConnectionsNotBlocked(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "test.block"})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "server.ping"})
	if r := c.recv(); r["id"].(float64) != 2 {
		t.Fatal(r)
	}
	if e.dial().call("server.ping", nil)["result"] == nil {
		t.Fatal("other conn")
	}
	close(e.blk)
	if r := c.recv(); r["id"].(float64) != 1 {
		t.Fatal(r)
	}
}

func TestTimeoutKeepsOperationRunning(t *testing.T) {
	e := newEnv(t)
	finished := make(chan struct{})
	e.reg.Register(ops.Op{Name: "test.slow", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		time.Sleep(400 * time.Millisecond)
		close(finished)
		return map[string]any{"late": true}, nil
	}})
	c := e.dial()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "test.slow", "timeout": 0.1})
	got := c.recvLine()
	want := `{"jsonrpc":"2.0","id":7,"error":{"code":-32020,"message":"test.slow timed out after 0.1s","data":{"code":"timeout","message":"test.slow timed out"}}}`
	if got != want {
		t.Fatal(got)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("operation was cancelled")
	}
	// the late result is discarded
	if _, err := c.line(300 * time.Millisecond); err == nil {
		t.Fatal("late result delivered")
	}
	if c.call("server.ping", nil)["result"] == nil {
		t.Fatal("ping")
	}
}

func TestDefaultTimeoutApplies(t *testing.T) {
	e := newEnv(t, WithDefaultTimeout(150*time.Millisecond))
	c := e.dial()
	r := c.call("test.block", nil)
	if errCode(r) != -32020 || !strings.Contains(r["error"].(map[string]any)["message"].(string), "timed out after 0.15s") {
		t.Fatal(r)
	}
}

func TestTooManyRequestsInFlight(t *testing.T) {
	e := newEnv(t, WithMaxInflight(2))
	c := e.dial()
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "test.block"})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "test.block"})
	time.Sleep(100 * time.Millisecond)
	c.send(map[string]any{"jsonrpc": "2.0", "id": "three", "method": "server.ping"})
	got := c.recvLine()
	if got != `{"jsonrpc":"2.0","id":"three","error":{"code":-32030,"message":"Too many requests in flight"}}` {
		t.Fatal(got)
	}
	close(e.blk)
	c.recv()
	c.recv()
	if c.call("server.ping", nil)["result"] == nil {
		t.Fatal("recovered")
	}
}

func TestTooManyConnections(t *testing.T) {
	e := newEnv(t, WithMaxConnections(2))
	a, b := e.dial(), e.dial()
	a.call("server.ping", nil)
	b.call("server.ping", nil)
	if e.s.ClientCount() != 2 {
		t.Fatal(e.s.ClientCount())
	}
	c := e.dial()
	got := c.recvLine()
	if got != `{"jsonrpc":"2.0","id":null,"error":{"code":-32031,"message":"Too many connections"}}` {
		t.Fatal(got)
	}
	if _, err := c.line(2 * time.Second); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	b.nc.Close()
	waitFor(t, func() bool { return e.s.ClientCount() == 1 })
	if e.dial().call("server.ping", nil)["result"] == nil {
		t.Fatal("slot freed")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func subscribe(c *client, params any) map[string]any {
	return c.call("events.subscribe", params)
}

func TestSubscribeLiveAndNoEventsBefore(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	e.bus.Publish(bus.Message, map[string]any{"n": 0})
	if _, err := c.line(100 * time.Millisecond); err == nil {
		t.Fatal("event before subscribe")
	}
	r := subscribe(c, nil)
	if r["result"].(map[string]any)["subscribed"] != true || r["result"].(map[string]any)["last_seq"].(float64) != 1 {
		t.Fatal(r)
	}
	e.bus.Publish(bus.Message, map[string]any{"text": "café \U0001f600", "a": 1})
	got := c.recvLine()
	if !strings.HasPrefix(got, `{"jsonrpc":"2.0","method":"event","params":{"seq":2,"type":"message","payload":{"a":1,"text":"caf\u00e9 \ud83d\ude00"},"ts":`) {
		t.Fatal(got)
	}
	for _, ch := range got {
		if ch > 126 {
			t.Fatalf("non-ascii: %q", ch)
		}
	}
	// unsubscribe is idempotent and stops events
	if c.call("events.unsubscribe", nil)["result"].(map[string]any)["subscribed"] != false {
		t.Fatal("unsub")
	}
	c.call("events.unsubscribe", nil)
	e.bus.Publish(bus.Message, nil)
	if _, err := c.line(150 * time.Millisecond); err == nil {
		t.Fatal("event after unsubscribe")
	}
	// resubscribe restarts the stream
	subscribe(c, map[string]any{"since": 0})
	for want := 1; want <= 3; want++ {
		ev := c.recv()
		if ev["params"].(map[string]any)["seq"].(float64) != float64(want) {
			t.Fatal(ev)
		}
	}
}

func TestSubscribeSinceGapAndClamp(t *testing.T) {
	reg, _ := newRegistry(t)
	b := bus.New(bus.WithBufferSize(3))
	path := filepath.Join(shortDir(t), "s.sock")
	s := NewServer(path, reg, b)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go s.Serve(context.Background())
	defer s.Close()
	for i := 1; i <= 6; i++ {
		b.Publish(bus.Message, map[string]any{"i": i})
	}
	nc, _ := net.Dial("unix", path)
	defer nc.Close()
	c := &client{t, nc, bufio.NewReader(nc)}
	subscribe(c, map[string]any{"since": 1})
	gap := c.recv()["params"].(map[string]any)
	if gap["type"] != "gap" || gap["seq"].(float64) != 6 ||
		gap["payload"].(map[string]any)["reason"] != "buffer" || gap["payload"].(map[string]any)["since"].(float64) != 1 {
		t.Fatal(gap)
	}
	for want := 4; want <= 6; want++ {
		if c.recv()["params"].(map[string]any)["seq"].(float64) != float64(want) {
			t.Fatal("replay")
		}
	}
	// since beyond last seq is clamped: no replay
	subscribe(c, json.RawMessage(`{"since":99999999999999999999}`))
	b.Publish(bus.Message, nil)
	if c.recv()["params"].(map[string]any)["seq"].(float64) != 7 {
		t.Fatal("clamp")
	}
}

func TestSubscribeRejectsBadSince(t *testing.T) {
	c := newEnv(t).dial()
	for _, v := range []string{`-1`, `true`, `"5"`, `1.5`, `1e2`, `[1]`} {
		c.sendRaw(`{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"since":` + v + `}}` + "\n")
		r := c.recv()
		if errCode(r) != -32602 || r["error"].(map[string]any)["message"] != "since must be an integer >= 0" {
			t.Fatal(v, r)
		}
	}
}

func TestSubscribeWithoutBus(t *testing.T) {
	reg, _ := newRegistry(t)
	s := NewServer(filepath.Join(shortDir(t), "n.sock"), reg, nil)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go s.Serve(context.Background())
	defer s.Close()
	nc, _ := net.Dial("unix", s.Path())
	defer nc.Close()
	c := &client{t, nc, bufio.NewReader(nc)}
	r := subscribe(c, nil)
	if errCode(r) != -32007 || r["error"].(map[string]any)["data"].(map[string]any)["message"] != "This server has no event bus" {
		t.Fatal(r)
	}
}

func TestSlowConsumerOverflowBecomesGap(t *testing.T) {
	reg, _ := newRegistry(t)
	b := bus.New(bus.WithBufferSize(5), bus.WithSubscriberQueueSize(5))
	s := NewServer(filepath.Join(shortDir(t), "o.sock"), reg, b)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go s.Serve(context.Background())
	defer s.Close()
	// Subscribe directly through a blocked-pump simulation: a bus subscription
	// that is never drained overflows into a gap; the adapter forwards it.
	sub := b.Subscribe()
	for i := 0; i < 20; i++ {
		b.Publish(bus.Message, nil)
	}
	evs := sub.Drain()
	if evs[0].Type != bus.Gap || evs[0].Payload["reason"] != bus.GapReasonOverflow {
		t.Fatalf("%v", evs)
	}
}

func TestHeartbeatWhenIdle(t *testing.T) {
	e := newEnv(t, WithHeartbeatInterval(100*time.Millisecond))
	c := e.dial()
	subscribe(c, nil)
	r := c.recv()
	if r["method"] != "heartbeat" || r["params"].(map[string]any)["ts"] == nil {
		t.Fatal(r)
	}
	if _, ok := r["id"]; ok {
		t.Fatal("heartbeat has id")
	}
}

func TestSubscriptionClosedWhenClientDisconnects(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	subscribe(c, nil)
	c.nc.Close()
	waitFor(t, func() bool { return e.s.ClientCount() == 0 })
	e.bus.Publish(bus.Message, nil) // must not block or panic
}

func TestEventsFlowWhileCallBlocked(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	subscribe(c, nil)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 5, "method": "test.block"})
	time.Sleep(50 * time.Millisecond)
	e.bus.Publish(bus.Message, nil)
	if c.recv()["method"] != "event" {
		t.Fatal("event")
	}
}

func TestPermissionsAndRuntimeFile(t *testing.T) {
	e := newEnv(t)
	fi, err := os.Stat(e.path)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatal(fi, err)
	}
	di, _ := os.Stat(filepath.Dir(e.path))
	if di.Mode().Perm() != 0o700 {
		t.Fatal(di.Mode())
	}
	rt := filepath.Join(filepath.Dir(e.path), "server.json")
	ri, err := os.Stat(rt)
	if err != nil || ri.Mode().Perm() != 0o600 {
		t.Fatal(ri, err)
	}
	b, _ := os.ReadFile(rt)
	var rf api.RuntimeFile
	if err := json.Unmarshal(b, &rf); err != nil || rf.PID != os.Getpid() || rf.Socket != e.path ||
		rf.APIVersion != 1 || rf.Version != "0.1.0" || rf.StartedAt <= 0 {
		t.Fatal(string(b), err)
	}
	e.s.Close()
	e.s.Close() // idempotent
	if _, err := os.Lstat(e.path); !os.IsNotExist(err) {
		t.Fatal("socket left")
	}
	if _, err := os.Stat(rt); !os.IsNotExist(err) {
		t.Fatal("runtime left")
	}
	select {
	case err := <-e.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestLooseDirectoryAndUmask(t *testing.T) {
	dir := filepath.Join(shortDir(t), "megachat")
	os.Mkdir(dir, 0o755)
	os.Chmod(dir, 0o755)
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	reg, _ := newRegistry(t)
	s := NewServer(filepath.Join(dir, "server.sock"), reg, nil)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	di, _ := os.Stat(dir)
	si, _ := os.Stat(filepath.Join(dir, "server.sock"))
	if di.Mode().Perm() != 0o700 || si.Mode().Perm() != 0o600 {
		t.Fatal(di.Mode(), si.Mode())
	}
}

func TestStaleSocketCleanedLiveRefusedFileKept(t *testing.T) {
	reg, _ := newRegistry(t)
	dir := shortDir(t)
	path := filepath.Join(dir, "a.sock")
	// stale: bind and abandon without unlinking
	ln, _ := net.Listen("unix", path)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o600)
	s := NewServer(path, reg, nil)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go s.Serve(context.Background())
	// live: a second server must refuse and leave the first alone
	s2 := NewServer(path, reg, nil)
	if err := s2.Listen(); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatal(err)
	}
	if nc, err := net.Dial("unix", path); err != nil {
		t.Fatal(err)
	} else {
		nc.Close()
	}
	s.Close()
	// regular file is never removed
	f := filepath.Join(dir, "plain.sock")
	os.WriteFile(f, []byte("x"), 0o600)
	s3 := NewServer(f, reg, nil)
	if err := s3.Listen(); err == nil || !strings.Contains(err.Error(), "not our socket") {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("file removed")
	}
}

func TestStopLeavesSuccessorsFilesAlone(t *testing.T) {
	reg, _ := newRegistry(t)
	dir := shortDir(t)
	path := filepath.Join(dir, "a.sock")
	s := NewServer(path, reg, nil)
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	ln, _ := net.Listen("unix", path)
	defer ln.Close()
	s.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("successor socket removed")
	}
}

func TestTooLongPathAndShadowing(t *testing.T) {
	reg, _ := newRegistry(t)
	s := NewServer(filepath.Join(shortDir(t), strings.Repeat("x", 120), "s.sock"), reg, nil)
	if err := s.Listen(); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatal(err)
	}
	reg.Register(ops.Op{Name: "server.ping", Handler: nil})
	s = NewServer(filepath.Join(shortDir(t), "s.sock"), reg, nil)
	if err := s.Listen(); err == nil || !strings.Contains(err.Error(), "shadow") {
		t.Fatal(err)
	}
}

func TestClientCounterAndShutdownDropsConnections(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	c.call("server.ping", nil)
	if n := e.reg.ClientCounts()["socket"]; n != 1 {
		t.Fatal(n)
	}
	e.s.Close()
	if _, err := c.line(2 * time.Second); err != io.EOF && !strings.Contains(fmt.Sprint(err), "reset") {
		t.Fatalf("got %v", err)
	}
	if _, ok := e.reg.ClientCounts()["socket"]; ok {
		t.Fatal("counter kept")
	}
}

func TestServeStopsOnContext(t *testing.T) {
	reg, _ := newRegistry(t)
	s := NewServer(filepath.Join(shortDir(t), "c.sock"), reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	waitFor(t, func() bool { _, err := os.Stat(s.Path()); return err == nil })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no stop")
	}
}

func TestPreCallHook(t *testing.T) {
	var cleaned sync.WaitGroup
	cleaned.Add(1)
	e := newEnv(t, WithPreCall(func(ctx context.Context, m string, p json.RawMessage) (json.RawMessage, func(), error) {
		if m == "test.echo" {
			return json.RawMessage(`{"staged":true}`), cleaned.Done, nil
		}
		return p, nil, nil
	}))
	r := e.dial().call("test.echo", map[string]any{"a": 1})
	if r["result"].(map[string]any)["echo"].(map[string]any)["staged"] != true {
		t.Fatal(r)
	}
	cleaned.Wait()
}

func TestLock(t *testing.T) {
	lock := filepath.Join(shortDir(t), "l.lock")
	rel, err := AcquireLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	// our own pid holding it is "alive"; simulate another live pid (parent).
	os.WriteFile(lock, []byte(fmt.Sprint(os.Getppid())), 0o644)
	if _, err := AcquireLock(lock); err == nil {
		t.Fatal("expected lock held")
	}
	os.WriteFile(lock, []byte("999999999"), 0o644) // dead pid
	rel2, err := AcquireLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	rel2()
	rel()
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("lock left")
	}
}

func TestASCIIEncoding(t *testing.T) {
	b, err := marshal(map[string]any{"s": "aé\U0001f600\u007f<>& \n\"\\"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"a\u00e9\ud83d\ude00\u007f<>&\u2028\n\"\\"}`
	if string(b) != want {
		t.Fatalf("%s != %s", b, want)
	}
	if got := formatFloat(1700000000.5); got != "1700000000.5" || formatFloat(2) != "2.0" || formatFloat(1e-7) != "1e-07" {
		t.Fatal(got, formatFloat(1e-7))
	}
	if gfmt(120) != "120" || gfmt(0.5) != "0.5" || gfmt(1e-5) != "1e-05" {
		t.Fatal(gfmt(120), gfmt(0.5), gfmt(1e-5))
	}
}
