package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var bg = context.Background()

type env struct {
	t     *testing.T
	core  *Core
	st    *store.Store
	mgr   *proto.Manager
	bus   *bus.Bus
	reg   *ops.Registry
	cfg   *config.Config
	fakes map[string]*fake.Backend
	sub   *bus.Subscription
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newEnv builds a core over a temp store with a fake backend per protocol.
func newEnv(t *testing.T, protocols ...string) *env {
	t.Helper()
	if len(protocols) == 0 {
		protocols = []string{api.ProtocolSignal, api.ProtocolWhatsApp, api.ProtocolTelegram}
	}
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, map[string]string{})
	st, err := store.Open(filepath.Join(dir, "messages.db"), store.WithLogger(quietLog()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := proto.New(proto.WithLogger(quietLog()))
	fakes := map[string]*fake.Backend{}
	for _, p := range protocols {
		f := fake.New(p)
		fakes[p] = f
		if err := mgr.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	b := bus.New(bus.WithLogger(quietLog()))
	c := New(cfg, st, mgr, b, WithLogger(quietLog()),
		WithSleep(func(ctx context.Context, d time.Duration) error { return nil }))
	reg := ops.NewRegistry()
	c.RegisterOps(reg)
	e := &env{t: t, core: c, st: st, mgr: mgr, bus: b, reg: reg, cfg: cfg, fakes: fakes}
	e.sub = b.Subscribe()
	return e
}

// call runs an op with a JSON params string and returns the result's wire JSON.
func (e *env) call(name, params string) (string, error) {
	e.t.Helper()
	res, err := e.reg.Call(bg, name, json.RawMessage(params))
	if err != nil {
		return "", err
	}
	b, merr := json.Marshal(res)
	if merr != nil {
		e.t.Fatalf("marshal %s result: %v", name, merr)
	}
	return string(b), nil
}

func (e *env) mustCall(name, params string) string {
	e.t.Helper()
	out, err := e.call(name, params)
	if err != nil {
		e.t.Fatalf("%s(%s): %v", name, params, err)
	}
	return out
}

func (e *env) callErr(name, params string) *ops.Error {
	e.t.Helper()
	_, err := e.call(name, params)
	if err == nil {
		e.t.Fatalf("%s(%s): expected an error", name, params)
	}
	var oe *ops.Error
	if !errors.As(err, &oe) {
		e.t.Fatalf("%s: not an *ops.Error: %v", name, err)
	}
	return oe
}

// events drains the bus subscription as (type, payload JSON) pairs.
func (e *env) events() []evt {
	var out []evt
	for _, ev := range e.sub.Drain() {
		b, _ := json.Marshal(ev.Payload)
		out = append(out, evt{string(ev.Type), string(b)})
	}
	return out
}

type evt struct{ Type, Payload string }

func (e *env) webPushes() []string {
	var out []string
	for _, ev := range e.events() {
		if ev.Type == "web_push" {
			out = append(out, ev.Payload)
		}
	}
	return out
}

func wantJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %q", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %q", want)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", gb, wb)
	}
}

func (e *env) fake(p string) *fake.Backend { return e.fakes[p] }

func contact(p, id, name string, extras map[string]any) proto.Contact {
	return proto.Contact{ID: id, DisplayName: name, Protocol: p, Extras: extras}
}

// recorder wraps a fake with a sync-safe counter for concurrent tests.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() int { c.mu.Lock(); defer c.mu.Unlock(); c.n++; return c.n }
