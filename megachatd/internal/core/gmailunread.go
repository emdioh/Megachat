package core

import (
	"context"
	"sync"
	"time"
)

// Port of the bus/unread plumbing of server/gmail_unread.py. The IMAP status
// fetch is injected (GmailFetch); this file only polls it, detects changes and
// publishes the gmail_unread web-push frame.

// GmailStatus is what a status fetch returns. UIDNext nil = unknown.
type GmailStatus struct {
	Unread  int
	UIDNext *int64
}

// GmailFetch reads the current unread status.
type GmailFetch func(ctx context.Context) (GmailStatus, error)

// GmailUnreadEventType is the web-push frame type.
const GmailUnreadEventType = "gmail_unread"

// GmailUnreadWatcher polls the unread count; goroutine-free until Start.
type GmailUnreadWatcher struct {
	publish  func(frameType string, payload any)
	fetch    GmailFetch
	enabled  func() bool
	interval time.Duration

	mu      sync.Mutex
	last    *GmailStatus
	wake    chan struct{}
	started bool
	stopCh  chan struct{}
}

// NewGmailUnreadWatcher creates a watcher. intervalS <= 0 disables polling.
func NewGmailUnreadWatcher(publish func(string, any), fetch GmailFetch, intervalS float64, enabled func() bool) *GmailUnreadWatcher {
	return &GmailUnreadWatcher{
		publish: publish, fetch: fetch, enabled: enabled,
		interval: time.Duration(intervalS * float64(time.Second)),
		wake:     make(chan struct{}, 1), stopCh: make(chan struct{}),
	}
}

// Start launches the poll loop once; a no-op when polling is disabled.
func (w *GmailUnreadWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.interval <= 0 || w.started {
		return
	}
	w.started = true
	stopCh := w.stopCh
	go func() {
		for {
			w.CheckOnce(ctx)
			t := time.NewTimer(w.interval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-stopCh:
				t.Stop()
				return
			case <-w.wake:
				t.Stop()
			case <-t.C:
			}
		}
	}()
}

// Stop ends the poll loop.
func (w *GmailUnreadWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		w.started = false
		close(w.stopCh)
		w.stopCh = make(chan struct{})
	}
}

// Poke asks for an immediate re-check (after mark read / delete).
func (w *GmailUnreadWatcher) Poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Snapshot is the last status seen.
func (w *GmailUnreadWatcher) Snapshot() (GmailStatus, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		return GmailStatus{}, false
	}
	return *w.last, true
}

// CheckOnce fetches the status and publishes it when it changed. It never
// fails: errors are swallowed.
func (w *GmailUnreadWatcher) CheckOnce(ctx context.Context) {
	if w.enabled != nil && !w.enabled() {
		return
	}
	st, err := w.fetch(ctx)
	if err != nil {
		return
	}
	w.mu.Lock()
	prev := w.last
	if prev != nil && prev.Unread == st.Unread && equalPtr(prev.UIDNext, st.UIDNext) {
		w.mu.Unlock()
		return
	}
	cp := st
	w.last = &cp
	w.mu.Unlock()
	newMail := prev != nil && st.UIDNext != nil && prev.UIDNext != nil && *st.UIDNext > *prev.UIDNext
	w.publish(GmailUnreadEventType, map[string]any{"unread": st.Unread, "new_mail": newMail})
}

func equalPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
