package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

// waBackend adds the WhatsApp startup hooks to the fake backend.
type waBackend struct {
	*fake.Backend
	readyAfter int32 // WaitReady succeeds on this call (0: never)
	waits      atomic.Int32
	reloads    atomic.Int32
	reloadWith []proto.Contact // contacts the Nth reload makes visible
}

func (w *waBackend) WaitReady(ctx context.Context, d time.Duration) bool {
	n := w.waits.Add(1)
	return w.readyAfter > 0 && n >= w.readyAfter
}

func (w *waBackend) ReloadContacts(ctx context.Context) {
	if w.reloads.Add(1) == 2 {
		w.SetContacts(w.reloadWith...)
	}
}

type tgBackend struct {
	*fake.Backend
	fetched atomic.Int32
}

func (t *tgBackend) FetchRecentHistory(ctx context.Context, limit int) int {
	t.fetched.Store(int32(limit))
	return 3
}

// collect runs Start and returns "type[:text]" of the events until stop matches.
func collect(t *testing.T, e *env, stop func(bus.Event) bool) []string {
	t.Helper()
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.core.Stop(bg) })
	var out []string
	waitFor(t, e.sub, func(ev bus.Event) bool {
		if ev.Payload["protocol"] == "signal" {
			return false // the Signal worker interleaves
		}
		s := string(ev.Type)
		if ev.Type == bus.BackendStatus {
			s += ":" + fmt.Sprint(ev.Payload["text"])
		}
		out = append(out, s)
		return stop(ev)
	})
	return out
}

func newWAEnv(t *testing.T, wa *waBackend, opts ...Option) *env {
	t.Helper()
	e := newEnvOpts(t, opts, "signal")
	if err := e.mgr.Register(wa); err != nil {
		t.Fatal(err)
	}
	return e
}

func has(got []string, s string) bool {
	for _, g := range got {
		if g == s {
			return true
		}
	}
	return false
}

func TestWhatsAppBootWaitThenReady(t *testing.T) {
	wa := &waBackend{Backend: fake.New("whatsapp"), readyAfter: 3}
	wa.SetContacts(contact("whatsapp", "39@c.us", "Bob", nil))
	e := newWAEnv(t, wa, WithStartupTimings(time.Second, time.Millisecond, time.Second, time.Millisecond))
	got := collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendReady && ev.Payload["protocol"] == "whatsapp"
	})
	if wa.waits.Load() != 3 {
		t.Fatalf("WaitReady calls = %d", wa.waits.Load())
	}
	// Python order: the contacts status precedes backend_ready.
	n := len(got)
	if got[n-2] != "backend_status:✅ WhatsApp: 1 contatti" || got[n-1] != "backend_ready" {
		t.Fatalf("order: %v", got)
	}
}

func TestWhatsAppBootWaitTimeoutPublishesDone(t *testing.T) {
	wa := &waBackend{Backend: fake.New("whatsapp")} // never ready
	e := newWAEnv(t, wa, WithStartupTimings(30*time.Millisecond, 5*time.Millisecond, time.Second, time.Millisecond))
	var ready bool
	collect(t, e, func(ev bus.Event) bool {
		if ev.Type == bus.BackendReady && ev.Payload["protocol"] == "whatsapp" {
			ready = true
		}
		return ev.Type == bus.BackendDone && ev.Payload["protocol"] == "whatsapp"
	})
	if ready {
		t.Fatal("backend_ready published after a boot-wait timeout")
	}
}

func TestWhatsAppBootWaitStopsOnPairing(t *testing.T) {
	wa := &waBackend{Backend: fake.New("whatsapp")}
	wa.SetPairing(true, "qr")
	e := newWAEnv(t, wa)
	collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendDone && ev.Payload["protocol"] == "whatsapp"
	})
	if wa.waits.Load() != 0 {
		t.Fatal("must not wait while pairing is required")
	}
}

func TestWhatsAppContactPollThenReady(t *testing.T) {
	wa := &waBackend{Backend: fake.New("whatsapp"), readyAfter: 1,
		reloadWith: []proto.Contact{contact("whatsapp", "39@c.us", "Bob", nil)}}
	e := newWAEnv(t, wa, WithStartupTimings(time.Second, time.Millisecond, time.Second, time.Millisecond))
	got := collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendStatus && ev.Payload["protocol"] == "whatsapp" && ev.Payload["text"] == ""
	})
	pos := map[string]int{}
	for i, g := range got {
		pos[g] = i
	}
	order := []string{"backend_status:🔄 WhatsApp: sync contatti...", "backend_status:📥 WhatsApp: 1 contatti caricati",
		"backend_status:⏳ WhatsApp: sync cronologia...", "backend_status:"}
	last := -1
	for _, s := range order {
		i, ok := pos[s]
		if !ok || i < last {
			t.Fatalf("%q missing or out of order in %v", s, got)
		}
		last = i
	}
	if wa.reloads.Load() < 2 {
		t.Fatalf("reloads = %d", wa.reloads.Load())
	}
}

func TestWhatsAppContactPollTimeout(t *testing.T) {
	wa := &waBackend{Backend: fake.New("whatsapp"), readyAfter: 1}
	e := newWAEnv(t, wa, WithStartupTimings(time.Second, time.Millisecond, 20*time.Millisecond, time.Millisecond))
	got := collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendDone && ev.Payload["protocol"] == "whatsapp"
	})
	if !has(got, "backend_status:⚠️ WhatsApp: timeout contatti") {
		t.Fatalf("no timeout status in %v", got)
	}
}

func TestTelegramStartupHistorySync(t *testing.T) {
	tg := &tgBackend{Backend: fake.New(api.ProtocolTelegram)}
	tg.SetContacts(contact("telegram", "42", "Ann", nil))
	e := newEnv(t, "signal")
	_ = e.mgr.Register(tg)
	got := collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendReady && ev.Payload["protocol"] == "telegram"
	})
	if tg.fetched.Load() != 20 {
		t.Fatalf("FetchRecentHistory limit = %d", tg.fetched.Load())
	}
	if got[len(got)-2] != "backend_status:⏳ Telegram: sync cronologia..." {
		t.Fatalf("history status must come right before backend_ready: %v", got)
	}
}

func TestTelegramNoContactsSkipsHistorySync(t *testing.T) {
	tg := &tgBackend{Backend: fake.New(api.ProtocolTelegram)}
	e := newEnv(t, "signal")
	_ = e.mgr.Register(tg)
	collect(t, e, func(ev bus.Event) bool {
		return ev.Type == bus.BackendReady && ev.Payload["protocol"] == "telegram"
	})
	if tg.fetched.Load() != 0 {
		t.Fatal("history synced without contacts")
	}
}
