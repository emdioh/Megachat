package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Guard states (server/wa_session_guard.py).
const (
	GuardUnknown      = "unknown"
	GuardHealthy      = "healthy"
	GuardSuspect      = "suspect"
	GuardRecovering   = "recovering"
	GuardNeedsPairing = "needs_pairing"
	GuardStopped      = "stopped"
	GuardUnreachable  = "unreachable"
	GuardGaveUp       = "gave_up"
)

// Guard timing constants.
const (
	// guardRecoveryPollS: poll interval (s) while a restart is in flight.
	guardRecoveryPollS = 5.0
	// guardManualCooldownS: pause (s) of automatic restarts after a manual
	// link/reconnect.
	guardManualCooldownS = 60.0
	// guardRestartWindowS: sliding window (s) of the restarts-per-hour cap.
	guardRestartWindowS = 3600.0
	// disconnectedFailedAfter: how long a disconnected websocket is STARTING
	// (whatsmeow reconnects by itself) before it counts as FAILED.
	disconnectedFailedAfter = 30 * time.Second
)

// GuardConfig are the tunables (protocols/config.py get_wa_session_*).
type GuardConfig struct {
	Enabled            bool
	PollS              float64
	RestartTimeoutS    float64
	MaxRestartsPerHour int
	BackoffS           []float64
	StableResetS       float64
	FailedGraceS       float64
	UnreachableAfter   int
}

// DefaultGuardConfig returns the Python defaults.
func DefaultGuardConfig() GuardConfig {
	return GuardConfig{
		Enabled: true, PollS: 60, RestartTimeoutS: 120, MaxRestartsPerHour: 3,
		BackoffS: []float64{5, 30, 120, 300}, StableResetS: 600, FailedGraceS: 10, UnreachableAfter: 5,
	}
}

// GuardSettings is implemented by *config.Config.
type GuardSettings interface {
	WASessionGuard() bool
	WASessionPollS() int
	WASessionRestartTimeoutS() int
	WASessionMaxRestartsPerHour() int
	WASessionBackoffS() []int
	WASessionStableResetS() int
	WASessionFailedGraceS() int
	WASessionUnreachableAfter() int
}

// GuardConfigFrom reads the guard settings from the configuration (defaults
// when cfg does not provide them).
func GuardConfigFrom(cfg any) GuardConfig {
	g := DefaultGuardConfig()
	s, ok := cfg.(GuardSettings)
	if !ok || s == nil {
		return g
	}
	g.Enabled = s.WASessionGuard()
	g.PollS = float64(s.WASessionPollS())
	g.RestartTimeoutS = float64(s.WASessionRestartTimeoutS())
	g.MaxRestartsPerHour = s.WASessionMaxRestartsPerHour()
	g.BackoffS = g.BackoffS[:0:0]
	for _, n := range s.WASessionBackoffS() {
		g.BackoffS = append(g.BackoffS, float64(n))
	}
	g.StableResetS = float64(s.WASessionStableResetS())
	g.FailedGraceS = float64(s.WASessionFailedGraceS())
	g.UnreachableAfter = s.WASessionUnreachableAfter()
	return g
}

// SessionSource is what the guard watches (the backend).
type SessionSource interface {
	// SessionInfo returns the session state in WAHA's vocabulary ("WORKING",
	// "FAILED", "STOPPED", "STARTING", "SCAN_QR_CODE"), whether it has an
	// identity (WAHA's `me`), and ok=false when the state is unobtainable.
	SessionInfo() (status string, hasMe bool, ok bool)
	// RestartSession reconnects keeping the pairing; true when accepted.
	RestartSession() bool
}

// GuardHooks connect the guard to its host (ChatCore in Python).
type GuardHooks struct {
	// Status publishes a backend_status line (ChatCore._status).
	Status func(text string, persistent bool)
	// Reconnect re-runs the backend connect (connect_whatsapp).
	Reconnect func()
	// LinkRequired publishes the web "link_required" push.
	LinkRequired func(required bool)
	// Linking reports that a link flow owns the session (api.link.is_active).
	Linking func() bool
}

// Guard is the port of WhatsAppSessionGuardMixin: it polls the session, restarts
// a FAILED one with bounded backoff, never restarts during QR/link/stopped
// states, reports an unreachable session and reconnects the backend once the
// session is WORKING again. The decision logic is the pure step Tick with an
// injectable clock; Run is a thin loop around it. The pairing state ("logged
// out": the phone unlinked this device) maps to SCAN_QR_CODE: no restart, a QR
// is needed.
type Guard struct {
	src   SessionSource
	cfg   GuardConfig
	hooks GuardHooks
	log   *slog.Logger
	// Clock returns monotonic seconds.
	Clock func() float64

	mu           sync.Mutex // single-flight for the tick
	state        string
	failedSince  *float64
	unreachable  int
	attempts     int
	restarts     []float64
	nextAttempt  float64
	restartAt    *float64
	healthySince *float64
	restarting   bool

	runMu  sync.Mutex
	cancel context.CancelFunc
	wake   chan struct{}
	proto  string
}

func newGuard(b *Backend, cfg GuardConfig) *Guard {
	start := time.Now()
	g := &Guard{
		src: b, cfg: cfg, log: b.log, state: GuardUnknown, wake: make(chan struct{}, 1), proto: "whatsapp",
		Clock: func() float64 { return time.Since(start).Seconds() },
	}
	return g
}

// Guard returns the backend's session guard.
func (b *Backend) Guard() *Guard { return b.guard }

// NewGuard builds a guard over any session source (tests, other hosts).
func NewGuard(src SessionSource, cfg GuardConfig, hooks GuardHooks, log *slog.Logger) *Guard {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	return &Guard{src: src, cfg: cfg, hooks: hooks, log: log, state: GuardUnknown, wake: make(chan struct{}, 1),
		proto: "whatsapp", Clock: func() float64 { return time.Since(start).Seconds() }}
}

// SetHooks installs the host hooks (call before Start).
func (g *Guard) SetHooks(h GuardHooks) {
	g.mu.Lock()
	g.hooks = h
	g.mu.Unlock()
}

// State returns the current guard state.
func (g *Guard) State() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// NeedsPairing reports that the session waits for a QR scan (drives the web
// banner and linking.Deps.LinkRequired).
func (g *Guard) NeedsPairing() bool { return g.State() == GuardNeedsPairing }

// Start runs the guard loop until Stop or ctx is cancelled (idempotent; a
// no-op when disabled).
func (g *Guard) Start(ctx context.Context) {
	if !g.cfg.Enabled {
		g.log.Info("WA session guard not started (disabled)")
		return
	}
	g.runMu.Lock()
	defer g.runMu.Unlock()
	if g.cancel != nil {
		return
	}
	rctx, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	go g.run(rctx)
}

// Stop stops the loop.
func (g *Guard) Stop() {
	g.runMu.Lock()
	defer g.runMu.Unlock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

func (g *Guard) run(ctx context.Context) {
	for ctx.Err() == nil {
		delay := g.safeTick()
		t := time.NewTimer(time.Duration(delay * float64(time.Second)))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-g.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func (g *Guard) safeTick() (delay float64) {
	defer func() {
		if r := recover(); r != nil {
			g.log.Debug("WA session guard tick failed", "err", r)
			delay = g.cfg.PollS
		}
	}()
	return g.Tick()
}

func (g *Guard) kick() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// noteConnected / noteDisconnected / noteLoggedOut are the backend's push
// signals (whatsmeow events replace WAHA's polling for transitions).
func (g *Guard) noteConnected()    { g.kick() }
func (g *Guard) noteDisconnected() { g.kick() }
func (g *Guard) noteLoggedOut()    { g.kick() }

func (g *Guard) status(text string, persistent bool) {
	if g.hooks.Status != nil {
		g.hooks.Status(text, persistent)
	}
}

func (g *Guard) publishLinkRequired(required bool) {
	if g.hooks.LinkRequired != nil {
		g.hooks.LinkRequired(required)
	}
}

// NoteManual is note_wa_session_manual: a user link/reconnect happened; pause
// restarts for a minute and clear the sticky states.
func (g *Guard) NoteManual() {
	g.mu.Lock()
	g.nextAttempt = g.Clock() + guardManualCooldownS
	wasPairing := false
	if g.state == GuardGaveUp || g.state == GuardNeedsPairing {
		wasPairing = g.state == GuardNeedsPairing
		g.state = GuardUnknown
		g.failedSince = nil
	}
	g.mu.Unlock()
	if wasPairing {
		g.publishLinkRequired(false)
	}
}

// Tick is one observation + decision (wa_session_guard_tick); it returns the
// seconds until the next tick.
func (g *Guard) Tick() float64 {
	poll := g.cfg.PollS
	if !g.mu.TryLock() {
		return poll // single-flight
	}
	defer g.mu.Unlock()
	return g.step(poll)
}

func (g *Guard) pollFor(poll float64) float64 {
	if g.state == GuardRecovering {
		return guardRecoveryPollS
	}
	return poll
}

func (g *Guard) step(poll float64) float64 {
	before := g.state == GuardNeedsPairing
	defer func() {
		if after := g.state == GuardNeedsPairing; after != before {
			g.publishLinkRequired(after)
		}
	}()
	return g.decide(poll)
}

func (g *Guard) decide(poll float64) float64 {
	now := g.Clock()
	status, hasMe, ok := g.src.SessionInfo()
	if !ok {
		return g.onUnreachable(now, poll)
	}
	g.unreachable = 0
	status = strings.ToUpper(status)
	if g.hooks.Linking != nil && g.hooks.Linking() {
		return poll // the link flow owns the session: hands off
	}
	if status == "WORKING" {
		return g.onWorking(now, poll, hasMe)
	}
	g.healthySince = nil
	switch status {
	case "SCAN_QR_CODE", "SCAN_QR":
		return g.onQR(poll)
	case "STOPPED":
		return g.onStopped(poll)
	}
	if g.state == GuardRecovering && (status == "STARTING" || status == "PENDING" || status == "FAILED") {
		return g.onRecovering(now, status)
	}
	if status == "FAILED" {
		return g.onFailed(now, poll)
	}
	return poll // STARTING / PENDING outside a recovery, or unknown
}

func (g *Guard) onUnreachable(now, poll float64) float64 {
	g.unreachable++
	if g.unreachable >= g.cfg.UnreachableAfter && g.state != GuardUnreachable {
		wasRecovering := g.state == GuardRecovering
		g.state = GuardUnreachable
		g.restarting = false
		g.log.Warn("WA session guard: WhatsApp unreachable")
		g.status("⚠️ WhatsApp: non raggiungibile", true)
		if wasRecovering {
			return poll
		}
	} else if g.state == GuardRecovering {
		if g.restartAt != nil && now-*g.restartAt >= g.cfg.RestartTimeoutS {
			return g.restartFailed(now, poll)
		}
	}
	return g.pollFor(poll)
}

func (g *Guard) onWorking(now, poll float64, hasMe bool) float64 {
	if !hasMe {
		return poll // transient: WORKING but no account yet
	}
	previous := g.state
	g.state = GuardHealthy
	g.failedSince = nil
	g.restarting = false
	g.restartAt = nil
	if previous != GuardUnknown && previous != GuardHealthy {
		g.log.Info("WA session guard: session WORKING again", "was", previous)
		g.status("✅ WhatsApp: sessione ripristinata", false)
		if g.hooks.Reconnect != nil {
			go g.hooks.Reconnect()
		}
	}
	if g.healthySince == nil {
		t := now
		g.healthySince = &t
	}
	if now-*g.healthySince >= g.cfg.StableResetS {
		g.attempts = 0
	}
	return poll
}

func (g *Guard) onQR(poll float64) float64 {
	g.restarting = false
	g.failedSince = nil
	if g.state != GuardNeedsPairing {
		g.state = GuardNeedsPairing
		g.log.Warn("WA session guard: QR pairing required, no restart")
		g.status("📱 WhatsApp: ricollegamento necessario (QR) — Ctrl+L", true)
	}
	return poll
}

func (g *Guard) onStopped(poll float64) float64 {
	g.restarting = false
	g.failedSince = nil
	if g.state != GuardStopped {
		g.state = GuardStopped
		g.log.Warn("WA session guard: session STOPPED, never auto-started")
		g.status("⏹️ WhatsApp: sessione ferma", true)
	}
	return poll
}

func (g *Guard) onRecovering(now float64, status string) float64 {
	poll := g.cfg.PollS
	started := now
	if g.restartAt != nil {
		started = *g.restartAt
	}
	elapsed := now - started
	if elapsed >= g.cfg.RestartTimeoutS || (status == "FAILED" && elapsed >= guardRecoveryPollS) {
		return g.restartFailed(now, poll)
	}
	return guardRecoveryPollS
}

func (g *Guard) backoff() float64 {
	if len(g.cfg.BackoffS) == 0 {
		return 5
	}
	idx := g.attempts - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(g.cfg.BackoffS)-1 {
		idx = len(g.cfg.BackoffS) - 1
	}
	return g.cfg.BackoffS[idx]
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (g *Guard) restartFailed(now, poll float64) float64 {
	g.restarting = false
	g.restartAt = nil
	g.state = GuardSuspect
	// The next FAILED counts as already confirmed (grace elapsed).
	fs := now - g.cfg.FailedGraceS
	g.failedSince = &fs
	g.nextAttempt = now + g.backoff()
	g.log.Warn("WA session guard: restart failed", "attempt", g.attempts, "retry_in_s", g.nextAttempt-now)
	return minF(poll, maxF(1, g.nextAttempt-now))
}

func (g *Guard) onFailed(now, poll float64) float64 {
	if g.state == GuardGaveUp {
		return poll // sticky until WORKING or a manual action
	}
	grace := g.cfg.FailedGraceS
	if g.failedSince == nil {
		t := now
		g.failedSince = &t
		g.state = GuardSuspect
		g.log.Warn("WA session guard: session FAILED, confirming")
		if grace > 0 {
			return maxF(grace, 1)
		}
	}
	remaining := grace - (now - *g.failedSince)
	if remaining > 0 {
		return maxF(remaining, 1)
	}
	if now < g.nextAttempt {
		return minF(poll, maxF(1, g.nextAttempt-now))
	}
	cap := g.cfg.MaxRestartsPerHour
	kept := g.restarts[:0:0]
	for _, t := range g.restarts {
		if now-t < guardRestartWindowS {
			kept = append(kept, t)
		}
	}
	g.restarts = kept
	if len(g.restarts) >= cap {
		g.state = GuardGaveUp
		g.log.Warn("WA session guard: restart cap reached", "cap", cap)
		g.status("❌ WhatsApp: sessione FAILED, riavvio automatico sospeso (limite raggiunto)", true)
		return poll
	}
	if g.restarting {
		return guardRecoveryPollS
	}
	return g.restart(now, poll, cap)
}

func (g *Guard) restart(now, poll float64, cap int) float64 {
	g.restarting = true
	g.attempts++
	g.restarts = append(g.restarts, now)
	t := now
	g.restartAt = &t
	g.state = GuardRecovering
	g.nextAttempt = now + g.backoff()
	n := len(g.restarts)
	g.log.Warn("WA session guard: restarting FAILED session", "n", n, "cap", cap)
	g.status(fmt.Sprintf("🔄 WhatsApp: sessione FAILED, riavvio (%d/%d)…", n, cap), true)
	accepted := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				g.log.Warn("WA session guard: restart call raised", "err", r)
			}
		}()
		accepted = g.src.RestartSession()
	}()
	if !accepted {
		return g.restartFailed(now, poll)
	}
	return guardRecoveryPollS
}

// ---- backend as a session source -----------------------------------------------------

// SessionInfo implements SessionSource: whatsmeow's state in WAHA's vocabulary.
func (b *Backend) SessionInfo() (string, bool, bool) {
	cli := b.currentClient()
	if cli == nil {
		if b.dbPath == "" {
			return "", false, false
		}
		return "STARTING", false, true
	}
	d := cli.Device()
	hasMe := d != nil && d.ID != nil
	switch {
	case b.stopped.Load():
		return "STOPPED", hasMe, true
	case b.loggedOut.Load() || !hasMe:
		return "SCAN_QR_CODE", false, true
	case b.connected.Load() && cli.IsConnected() && cli.IsLoggedIn():
		return "WORKING", true, true
	}
	if at := b.disconnectedSince(); !at.IsZero() && nowFunc().Sub(at) > disconnectedFailedAfter {
		return "FAILED", hasMe, true
	}
	return "STARTING", hasMe, true
}

// RestartSession implements SessionSource (restart keeps the pairing).
func (b *Backend) RestartSession() bool { return b.restart(context.Background()) }
