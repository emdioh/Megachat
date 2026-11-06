package whatsapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

// The guard tests are the port of tests/test_wa_session_guard.py: deterministic,
// no sleeps, an injectable clock.

type fakeSession struct {
	mu       sync.Mutex
	infos    []*sessInfo // consumed first
	def      *sessInfo
	restarts int
	restart  func() bool
	calls    int
}

type sessInfo struct {
	status string
	me     bool
}

var (
	sWorking  = &sessInfo{"WORKING", true}
	sFailed   = &sessInfo{"FAILED", false}
	sQR       = &sessInfo{"SCAN_QR_CODE", false}
	sStarting = &sessInfo{"STARTING", false}
)

func (f *fakeSession) SessionInfo() (string, bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	cur := f.def
	if len(f.infos) > 0 {
		cur, f.infos = f.infos[0], f.infos[1:]
	}
	if cur == nil {
		return "", false, false
	}
	return cur.status, cur.me, true
}

func (f *fakeSession) RestartSession() bool {
	f.mu.Lock()
	f.restarts++
	fn := f.restart
	f.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return true
}

type gEnv struct {
	g         *Guard
	s         *fakeSession
	clock     float64
	statuses  []string
	persist   []bool
	reconnect int
	linkReq   []bool
	linking   bool
	rmu       sync.Mutex
}

func newGuardEnv(t *testing.T, mutate func(*GuardConfig)) *gEnv {
	t.Helper()
	cfg := DefaultGuardConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	e := &gEnv{s: &fakeSession{def: sWorking}, clock: 1000}
	e.g = NewGuard(e.s, cfg, GuardHooks{
		Status: func(text string, persistent bool) {
			e.statuses = append(e.statuses, text)
			e.persist = append(e.persist, persistent)
		},
		Reconnect:    func() { e.rmu.Lock(); e.reconnect++; e.rmu.Unlock() },
		LinkRequired: func(r bool) { e.linkReq = append(e.linkReq, r) },
		Linking:      func() bool { return e.linking },
	}, quietLogger())
	e.g.Clock = func() float64 { return e.clock }
	return e
}

func (e *gEnv) tick(info *sessInfo, advance float64) float64 {
	e.clock += advance
	e.s.def = info
	return e.g.Tick()
}

func (e *gEnv) drain() []string {
	out := e.statuses
	e.statuses, e.persist = nil, nil
	return out
}

func (e *gEnv) failAndRestart() {
	e.tick(sFailed, 0)
	e.tick(sFailed, 10)
}

func (e *gEnv) reconnects() int { e.rmu.Lock(); defer e.rmu.Unlock(); return e.reconnect }

func TestGuardFailedRestartsOnceThenRecovers(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.tick(sWorking, 0) // UNKNOWN -> HEALTHY, no reconnect
	if e.reconnects() != 0 {
		t.Fatal("no reconnect on first WORKING")
	}
	e.tick(sFailed, 1)
	if e.g.State() != GuardSuspect || e.s.restarts != 0 {
		t.Fatalf("state=%s restarts=%d", e.g.State(), e.s.restarts)
	}
	e.tick(sFailed, 10)
	if e.s.restarts != 1 || e.g.State() != GuardRecovering || e.g.attempts != 1 {
		t.Fatalf("restart: state=%s restarts=%d attempts=%d", e.g.State(), e.s.restarts, e.g.attempts)
	}
	if got := e.drain(); len(got) != 1 || got[0] != "🔄 WhatsApp: sessione FAILED, riavvio (1/3)…" {
		t.Fatalf("statuses: %v", got)
	}
	e.tick(sStarting, 2)
	if e.s.restarts != 1 {
		t.Fatal("STARTING during a recovery does not restart again")
	}
	e.tick(sWorking, 3)
	if e.g.State() != GuardHealthy {
		t.Fatal(e.g.State())
	}
	time.Sleep(20 * time.Millisecond) // the reconnect hook runs on its own goroutine
	if e.reconnects() != 1 {
		t.Errorf("reconnects = %d", e.reconnects())
	}
	if got := e.drain(); len(got) != 1 || got[0] != "✅ WhatsApp: sessione ripristinata" || e.persist != nil {
		t.Fatalf("statuses: %v", got)
	}
	e.tick(sWorking, 100)
	if e.g.attempts != 1 {
		t.Error("attempts reset only after the stable period")
	}
	e.tick(sWorking, 600)
	if e.g.attempts != 0 {
		t.Error("attempts must reset after 600 s of WORKING")
	}
}

func TestGuardQRAfterRestartNeedsPairingNoMoreRestarts(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.failAndRestart()
	e.drain()
	for i := 0; i < 4; i++ {
		e.tick(sQR, 60)
	}
	if e.g.State() != GuardNeedsPairing || e.s.restarts != 1 || !e.g.NeedsPairing() {
		t.Fatalf("state=%s restarts=%d", e.g.State(), e.s.restarts)
	}
	if got := e.drain(); len(got) != 1 || got[0] != "📱 WhatsApp: ricollegamento necessario (QR) — Ctrl+L" {
		t.Fatalf("statuses: %v", got)
	}
	// The web banner is told once when entering the state, and again when leaving.
	if len(e.linkReq) != 1 || !e.linkReq[0] {
		t.Fatalf("link_required pushes: %v", e.linkReq)
	}
	e.tick(sWorking, 1)
	if len(e.linkReq) != 2 || e.linkReq[1] {
		t.Fatalf("link_required cleared: %v", e.linkReq)
	}
}

func TestGuardBackoffCapAndGaveUp(t *testing.T) {
	e := newGuardEnv(t, func(c *GuardConfig) { c.BackoffS = []float64{100}; c.MaxRestartsPerHour = 2 })
	e.failAndRestart()
	if e.s.restarts != 1 {
		t.Fatal(e.s.restarts)
	}
	// The restart "failed": FAILED again after the recovery poll -> backoff 100 s.
	e.tick(sFailed, 6)
	if e.g.State() != GuardSuspect {
		t.Fatal(e.g.State())
	}
	e.tick(sFailed, 50)
	if e.s.restarts != 1 {
		t.Fatal("still inside the backoff")
	}
	e.tick(sFailed, 60)
	if e.s.restarts != 2 {
		t.Fatal("second restart after the backoff")
	}
	e.drain()
	e.tick(sFailed, 6)
	e.tick(sFailed, 101) // backoff elapsed but the cap (2/h) is reached
	if e.s.restarts != 2 || e.g.State() != GuardGaveUp {
		t.Fatalf("restarts=%d state=%s", e.s.restarts, e.g.State())
	}
	got := e.drain()
	if len(got) == 0 || got[len(got)-1] != "❌ WhatsApp: sessione FAILED, riavvio automatico sospeso (limite raggiunto)" {
		t.Fatalf("statuses: %v", got)
	}
	e.tick(sFailed, 500)
	if e.s.restarts != 2 || len(e.drain()) != 0 {
		t.Fatal("GAVE_UP is sticky and silent")
	}
	e.tick(sWorking, 1)
	if e.g.State() != GuardHealthy {
		t.Fatal("WORKING clears GAVE_UP")
	}
}

func TestGuardGaveUpClearedByManual(t *testing.T) {
	e := newGuardEnv(t, func(c *GuardConfig) { c.MaxRestartsPerHour = 0 })
	e.tick(sFailed, 0)
	e.tick(sFailed, 10)
	if e.g.State() != GuardGaveUp || e.s.restarts != 0 {
		t.Fatalf("state=%s restarts=%d", e.g.State(), e.s.restarts)
	}
	e.g.NoteManual()
	if e.g.State() != GuardUnknown || e.g.failedSince != nil || e.g.nextAttempt != e.clock+guardManualCooldownS {
		t.Fatalf("manual: state=%s next=%v", e.g.State(), e.g.nextAttempt)
	}
}

func TestGuardRestartWindowPrunesOldRestarts(t *testing.T) {
	e := newGuardEnv(t, func(c *GuardConfig) { c.MaxRestartsPerHour = 1; c.BackoffS = []float64{1} })
	e.failAndRestart()
	e.tick(sWorking, 1)
	e.tick(sFailed, guardRestartWindowS)
	e.tick(sFailed, 10)
	if e.s.restarts != 2 {
		t.Fatalf("an old restart leaves the window, restarts=%d", e.s.restarts)
	}
}

func TestGuardRestartFailureCountsAndBacksOff(t *testing.T) {
	for name, fn := range map[string]func() bool{
		"refused": func() bool { return false },
		"panic":   func() bool { panic(errors.New("boom")) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newGuardEnv(t, nil)
			e.s.restart = fn
			e.failAndRestart()
			if e.s.restarts != 1 || e.g.attempts != 1 || e.g.State() != GuardSuspect || e.g.nextAttempt != e.clock+5 {
				t.Fatalf("restarts=%d attempts=%d state=%s next=%v", e.s.restarts, e.g.attempts, e.g.State(), e.g.nextAttempt-e.clock)
			}
			e.tick(sFailed, 1)
			if e.s.restarts != 1 {
				t.Fatal("inside the backoff")
			}
			e.tick(sFailed, 5)
			if e.s.restarts != 2 || e.g.nextAttempt != e.clock+30 {
				t.Fatalf("second attempt uses the next backoff step: restarts=%d next=%v", e.s.restarts, e.g.nextAttempt-e.clock)
			}
		})
	}
}

func TestGuardRestartTimeoutWhileStarting(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.failAndRestart()
	e.tick(sStarting, 60)
	if e.g.State() != GuardRecovering {
		t.Fatal(e.g.State())
	}
	e.tick(sStarting, 61)
	if e.g.State() != GuardSuspect {
		t.Fatal("restart timeout")
	}
}

func TestGuardUnreachableAlertsOnceThenRecovers(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.tick(sWorking, 0)
	e.drain()
	for i := 0; i < 8; i++ {
		e.s.infos = append(e.s.infos, nil)
		e.clock += 10
		e.g.Tick()
	}
	if e.g.State() != GuardUnreachable || e.s.restarts != 0 {
		t.Fatalf("state=%s", e.g.State())
	}
	if got := e.drain(); len(got) != 1 || got[0] != "⚠️ WhatsApp: non raggiungibile" {
		t.Fatalf("statuses: %v", got)
	}
	e.tick(sWorking, 10)
	time.Sleep(20 * time.Millisecond)
	if e.g.State() != GuardHealthy || e.reconnects() != 1 {
		t.Fatalf("state=%s reconnects=%d", e.g.State(), e.reconnects())
	}
}

func TestGuardNonRestartableStates(t *testing.T) {
	cases := []struct {
		info  *sessInfo
		state string
	}{
		{sQR, GuardNeedsPairing},
		{sStarting, GuardUnknown},
		{&sessInfo{"STOPPED", false}, GuardStopped},
		{&sessInfo{"WEIRD", false}, GuardUnknown},
	}
	for _, c := range cases {
		e := newGuardEnv(t, nil)
		for i := 0; i < 3; i++ {
			e.tick(c.info, 100)
		}
		if e.s.restarts != 0 || e.g.State() != c.state {
			t.Errorf("%s: restarts=%d state=%s want %s", c.info.status, e.s.restarts, e.g.State(), c.state)
		}
		if c.state == GuardStopped {
			if got := e.drain(); len(got) != 1 || got[0] != "⏹️ WhatsApp: sessione ferma" {
				t.Errorf("stopped statuses: %v", got)
			}
		}
	}
}

func TestGuardLinkFlowBlocksRestart(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.linking = true
	e.tick(sFailed, 0)
	e.tick(sFailed, 100)
	if e.s.restarts != 0 || e.g.State() != GuardUnknown || len(e.drain()) != 0 {
		t.Fatal("the link flow owns the session")
	}
}

func TestGuardWorkingWithoutIdentityIsTransient(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.tick(&sessInfo{"WORKING", false}, 0)
	if e.g.State() != GuardUnknown {
		t.Fatal(e.g.State())
	}
}

func TestGuardSingleFlight(t *testing.T) {
	e := newGuardEnv(t, nil)
	e.g.mu.Lock()
	delay := e.g.Tick()
	e.g.mu.Unlock()
	if delay != 60 || e.s.calls != 0 {
		t.Fatalf("a concurrent tick returns the poll interval and does nothing (delay=%v calls=%d)", delay, e.s.calls)
	}
}

func TestGuardLoopStartsStopsAndSurvivesPanics(t *testing.T) {
	e := newGuardEnv(t, func(c *GuardConfig) { c.PollS = 0.01 })
	var mu sync.Mutex
	calls := 0
	e.s.restart = nil
	src := &countingSource{inner: e.s, calls: &calls, mu: &mu, panicFirst: true}
	g := NewGuard(src, GuardConfig{Enabled: true, PollS: 0.01, RestartTimeoutS: 120, MaxRestartsPerHour: 3, BackoffS: []float64{5}, StableResetS: 600, FailedGraceS: 10, UnreachableAfter: 5}, GuardHooks{}, quietLogger())
	g.Start(context.Background())
	g.Start(context.Background()) // idempotent
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	g.Stop()
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 3 {
		t.Fatalf("the loop must survive a panicking tick, calls=%d", n)
	}
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	after := calls
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != after {
		t.Error("the loop must stop")
	}
}

type countingSource struct {
	inner      SessionSource
	calls      *int
	mu         *sync.Mutex
	panicFirst bool
}

func (c *countingSource) SessionInfo() (string, bool, bool) {
	c.mu.Lock()
	*c.calls++
	first := *c.calls == 1 && c.panicFirst
	c.mu.Unlock()
	if first {
		panic("x")
	}
	return c.inner.SessionInfo()
}
func (c *countingSource) RestartSession() bool { return c.inner.RestartSession() }

func TestGuardDisabled(t *testing.T) {
	e := newGuardEnv(t, func(c *GuardConfig) { c.Enabled = false })
	e.g.Start(context.Background())
	if e.g.cancel != nil {
		t.Error("a disabled guard does not run")
	}
}

func TestGuardConfigDefaultsFromConfig(t *testing.T) {
	cfg := config.NewWith(t.TempDir(), t.TempDir(), map[string]string{})
	got := GuardConfigFrom(cfg)
	want := DefaultGuardConfig()
	if got.Enabled != want.Enabled || got.PollS != want.PollS || got.RestartTimeoutS != want.RestartTimeoutS ||
		got.MaxRestartsPerHour != want.MaxRestartsPerHour || got.StableResetS != want.StableResetS ||
		got.FailedGraceS != want.FailedGraceS || got.UnreachableAfter != want.UnreachableAfter ||
		len(got.BackoffS) != 4 || got.BackoffS[0] != 5 || got.BackoffS[3] != 300 {
		t.Fatalf("config defaults %+v != %+v", got, want)
	}
	if g := GuardConfigFrom(nil); g.PollS != 60 || !g.Enabled {
		t.Errorf("nil config -> defaults: %+v", g)
	}
	over := config.NewWith(t.TempDir(), t.TempDir(), map[string]string{"WHATSAPP_SESSION_BACKOFF_S": "1, 2", "WHATSAPP_SESSION_GUARD": "off"})
	if g := GuardConfigFrom(over); g.Enabled || len(g.BackoffS) != 2 || g.BackoffS[1] != 2 {
		t.Errorf("overrides: %+v", g)
	}
}

// ---- the backend as a session source -------------------------------------------------------------

func TestBackendSessionInfo(t *testing.T) {
	e := newEnv(t)
	check := func(want string, me bool) {
		t.Helper()
		st, hasMe, ok := e.b.SessionInfo()
		if st != want || hasMe != me || !ok {
			t.Errorf("SessionInfo = %q %v %v, want %q %v", st, hasMe, ok, want, me)
		}
	}
	check("WORKING", true)
	// Disconnected: STARTING (whatsmeow reconnects by itself), FAILED after a while.
	e.b.connected.Store(false)
	e.b.setDisconnected(true)
	check("STARTING", true)
	e.b.discMu.Lock()
	e.b.discAt = nowFunc().Add(-time.Minute)
	e.b.discMu.Unlock()
	check("FAILED", true)
	// Logged out by the phone: a new QR is needed, never a restart.
	e.b.loggedOut.Store(true)
	check("SCAN_QR_CODE", false)
	e.b.loggedOut.Store(false)
	e.b.stopped.Store(true)
	check("STOPPED", true)
}

func TestBackendRestartSessionKeepsPairing(t *testing.T) {
	e := newEnv(t)
	if !e.b.RestartSession() {
		t.Fatal("restart")
	}
	if e.fc.disc != 1 || e.fc.connects != 1 {
		t.Errorf("disconnect=%d connect=%d", e.fc.disc, e.fc.connects)
	}
	if e.fc.dev.ID == nil {
		t.Error("the pairing must be kept")
	}
	e.fc.dev.ID = nil
	if e.b.RestartSession() {
		t.Error("an unpaired device cannot be restarted")
	}
}

func TestEventsDriveConnectionState(t *testing.T) {
	e := newEnv(t)
	e.b.handleEvent(&eventsDisconnected)
	if e.b.IsConnected() {
		t.Error("Disconnected")
	}
	e.b.handleEvent(&eventsConnected)
	if !e.b.IsConnected() {
		t.Error("Connected")
	}
	e.b.handleEvent(&eventsLoggedOut)
	if e.b.IsConnected() || !e.b.NeedsPairing() {
		t.Error("LoggedOut must require a new QR")
	}
}
