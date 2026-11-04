package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

// Compile-time: the real config and store satisfy the backend's interfaces.
var (
	_ Config = (*config.Config)(nil)
	_ Store  = (*store.Store)(nil)
)

type handlerFn func(req bin.Encoder) (bin.Encoder, error)

// fakeInvoker is a tg.Invoker answering by request type; no network.
type fakeInvoker struct {
	mu       sync.Mutex
	handlers map[string]handlerFn
	calls    []bin.Encoder
}

func newFake() *fakeInvoker { return &fakeInvoker{handlers: map[string]handlerFn{}} }

func (f *fakeInvoker) on(req bin.Encoder, h handlerFn) {
	f.handlers[fmt.Sprintf("%T", req)] = h
}

func (f *fakeInvoker) reply(req bin.Encoder, res bin.Encoder) {
	f.on(req, func(bin.Encoder) (bin.Encoder, error) { return res, nil })
}

func (f *fakeInvoker) count(req bin.Encoder) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if fmt.Sprintf("%T", c) == fmt.Sprintf("%T", req) {
			n++
		}
	}
	return n
}

func (f *fakeInvoker) last(req bin.Encoder) bin.Encoder {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out bin.Encoder
	for _, c := range f.calls {
		if fmt.Sprintf("%T", c) == fmt.Sprintf("%T", req) {
			out = c
		}
	}
	return out
}

func (f *fakeInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	h := f.handlers[fmt.Sprintf("%T", in)]
	f.mu.Unlock()
	if h == nil {
		return fmt.Errorf("fakeInvoker: unexpected request %T", in)
	}
	res, err := h(in)
	if err != nil {
		return err
	}
	var buf bin.Buffer
	if err := res.Encode(&buf); err != nil {
		return err
	}
	return out.Decode(&buf)
}

type env struct {
	b     *Backend
	f     *fakeInvoker
	st    *store.Store
	dir   string
	media string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, map[string]string{"XDG_DATA_HOME": filepath.Join(dir, "xdg")})
	st, err := store.Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := newFake()
	media := filepath.Join(dir, "telegram-media")
	b := New(Options{
		Config: cfg, Store: st, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		APIID: 1, APIHash: "h", MediaDir: media,
		API: tg.NewClient(f), Self: &tg.User{ID: 1000, FirstName: "Me", Self: true},
	})
	return &env{b: b, f: f, st: st, dir: dir, media: media}
}

func (e *env) contact(id int64, name string) {
	c := userToContact(&tg.User{ID: id, FirstName: name})
	e.b.RegisterContact(c)
	e.b.peers.set(id, 99)
}

func drain(b *Backend) []string {
	var out []string
	for _, ev := range b.PollOnce() {
		out = append(out, ev.Type+":"+ev.ContactID)
	}
	return out
}
