package core

import (
	"context"
	"sync"
	"time"
)

// Port of server/address_book_refresh.py: periodic + lazy (scoped) refresh of
// the dynamic address book on a single worker.
type addressBookRefresher struct {
	c       *Core
	mu      sync.Mutex
	pending map[string]bool
	wake    chan struct{}
}

func newAddressBookRefresher(c *Core) *addressBookRefresher {
	return &addressBookRefresher{c: c, pending: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// schedule requests a scoped refresh after an unresolved message (throttled
// by the worker cooldown).
func (r *addressBookRefresher) schedule(protocol string) {
	r.mu.Lock()
	r.pending[protocol] = true
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *addressBookRefresher) stop() {}

func (r *addressBookRefresher) start(ctx context.Context) {
	r.c.goroutine(func() { r.loop(ctx) })
}

func (r *addressBookRefresher) loop(ctx context.Context) {
	interval := time.Duration(r.c.cfg.DynamicRefreshIntervalS()) * time.Second
	cooldown := time.Duration(r.c.cfg.DynamicRefreshCooldownS()) * time.Second
	lastPeriodic := time.Now()
	// Re-armed "cooldown ago" so the very first lazy refresh is never throttled.
	lastLazy := lastPeriodic.Add(-cooldown)
	for {
		remaining := interval - time.Since(lastPeriodic)
		if remaining < 0 {
			remaining = 0
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-r.wake:
			timer.Stop()
		}
		now := time.Now()
		if now.Sub(lastPeriodic) >= interval {
			lastPeriodic, lastLazy = now, now
			r.run(ctx, nil)
			continue
		}
		r.mu.Lock()
		if now.Sub(lastLazy) < cooldown {
			r.mu.Unlock()
			// Throttled: wait out the cooldown before honouring the request.
			select {
			case <-ctx.Done():
				return
			case <-time.After(lastLazy.Add(cooldown).Sub(now)):
			}
			select {
			case r.wake <- struct{}{}:
			default:
			}
			continue
		}
		lastLazy = now
		var protocols []string
		for p := range r.pending {
			protocols = append(protocols, p)
		}
		r.pending = map[string]bool{}
		r.mu.Unlock()
		if len(protocols) > 0 {
			r.run(ctx, protocols)
		} else {
			r.run(ctx, nil)
		}
	}
}

func (r *addressBookRefresher) run(ctx context.Context, protocols []string) {
	defer func() {
		if rec := recover(); rec != nil {
			r.c.log.Debug("dynamic refresh worker error", "err", rec)
		}
	}()
	for p, res := range r.c.mgr.RefreshContacts(ctx, protocols, true) {
		if res.Errors != "" {
			r.c.log.Warn("dynamic refresh failed", "protocol", p, "err", res.Errors)
		}
	}
}
