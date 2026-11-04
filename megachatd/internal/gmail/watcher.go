package gmail

import (
	"context"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// Watcher polls the unread status and publishes gmail_unread events when the
// count or UIDNEXT changes (server/gmail_unread.py). The core may instead
// drive its own watcher from Service.FetchUnreadStatus.
type Watcher struct {
	svc      *Service
	bus      Bus
	interval time.Duration

	mu      sync.Mutex
	last    *UnreadStatus
	wake    chan struct{}
	stop    chan struct{}
	started bool
}

// NewWatcher creates a watcher; interval <= 0 disables polling.
func NewWatcher(s *Service, b Bus, interval time.Duration) *Watcher {
	return &Watcher{svc: s, bus: b, interval: interval, wake: make(chan struct{}, 1), stop: make(chan struct{})}
}

// Start launches the poll loop once.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.interval <= 0 || w.started {
		return
	}
	w.started = true
	stop := w.stop
	go func() {
		for {
			w.CheckOnce(ctx)
			t := time.NewTimer(w.interval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-stop:
				t.Stop()
				return
			case <-w.wake:
				t.Stop()
			case <-t.C:
			}
		}
	}()
}

// Stop ends the loop.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		w.started = false
		close(w.stop)
		w.stop = make(chan struct{})
	}
}

// Poke asks for an immediate re-check.
func (w *Watcher) Poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Latest is the last status seen.
func (w *Watcher) Latest() (UnreadStatus, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		return UnreadStatus{}, false
	}
	return *w.last, true
}

// CheckOnce fetches and publishes on change; it never fails.
func (w *Watcher) CheckOnce(ctx context.Context) {
	if !w.svc.cfg.GmailEnabled() {
		return
	}
	st, err := w.svc.FetchUnreadStatus(ctx, nil)
	if err != nil {
		return
	}
	w.mu.Lock()
	prev := w.last
	if prev != nil && prev.Unread == st.Unread && eqPtr(prev.UIDNext, st.UIDNext) {
		w.mu.Unlock()
		return
	}
	cp := st
	w.last = &cp
	w.mu.Unlock()
	newMail := prev != nil && st.UIDNext != nil && prev.UIDNext != nil && *st.UIDNext > *prev.UIDNext
	if w.bus != nil {
		w.bus.Publish(bus.GmailUnread, map[string]any{"unread": st.Unread, "new_mail": newMail})
	}
}

func eqPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
