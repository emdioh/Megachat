package linking

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

var (
	_ Config   = (*config.Config)(nil)
	_ Backends = (*proto.Manager)(nil)
)

func init() { slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) }

// ---- fakes -------------------------------------------------------------

type fakeProc struct {
	mu         sync.Mutex
	lines      []string
	exit       *int
	terminated int
	termErr    error
}

func (p *fakeProc) ReadLine() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.lines) == 0 {
		return "", false
	}
	l := p.lines[0]
	p.lines = p.lines[1:]
	return l, true
}
func (p *fakeProc) Drain() {}
func (p *fakeProc) Exited() (bool, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exit == nil {
		return false, 0
	}
	return true, *p.exit
}
func (p *fakeProc) Terminate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminated++
	return p.termErr
}
func (p *fakeProc) setExit(c int) { p.mu.Lock(); p.exit = &c; p.mu.Unlock() }
func (p *fakeProc) terminatedN() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminated
}

type fakeCLI struct {
	accounts    []string
	listErr     error
	procs       []*fakeProc
	started     []string // device names
	onStart     func()
	startReturn LinkProcess
}

func (c *fakeCLI) ListAccounts(context.Context) ([]string, error) { return c.accounts, c.listErr }
func (c *fakeCLI) StartLink(name string) (LinkProcess, error) {
	c.started = append(c.started, name)
	if c.onStart != nil {
		c.onStart()
	}
	if c.startReturn != nil {
		return c.startReturn, nil
	}
	p := c.procs[0]
	c.procs = c.procs[1:]
	return p, nil
}

func lineProc(lines ...string) *fakeProc { return &fakeProc{lines: lines} }

type signalBackend struct {
	proto.Backend
	number string
}

func (b *signalBackend) UserNumber() string     { return b.number }
func (b *signalBackend) SetUserNumber(n string) { b.number = n }

type bmap map[string]proto.Backend

func (m bmap) Get(p string) (proto.Backend, bool) { b, ok := m[p]; return b, ok }

type testEnv struct {
	dir string
	cfg *config.Config
	cli *fakeCLI
}

func newTest(t *testing.T, env map[string]string) *testEnv {
	dir := t.TempDir()
	return &testEnv{dir: dir, cfg: config.NewWith(dir, dir, env), cli: &fakeCLI{}}
}

func (e *testEnv) service(b bmap) *Service {
	return New(Deps{Backends: b, Config: e.cfg, SignalCLI: e.cli})
}

// ---- persistence ----------------------------------------------------------

func TestPersistsOnlyLinkedAccountAndPreservesConfig(t *testing.T) {
	e := newTest(t, nil)
	cf := e.cfg.ConfigFile()
	os.WriteFile(cf, []byte(`{"web": {"token": "existing"}}`), 0o600)
	e.cli.accounts = []string{"+391111111111", "+392222222222"}
	acc, err := ResolveLinkedSignalAccount(context.Background(), e.cli, e.cfg, "+39 222 222 2222", nil)
	if err != nil || acc != "+392222222222" {
		t.Fatalf("%q %v", acc, err)
	}
	if err := PersistSignalAccount(cf, acc); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(cf)
	json.Unmarshal(b, &got)
	want := map[string]any{"web": map[string]any{"token": "existing"}, "user_number": "+392222222222"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	left, _ := filepath.Glob(filepath.Join(e.dir, ".config.json.*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestResolveLinkedSignalAccountRules(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		accounts []string
		previous map[string]bool
		phone    string
		env      map[string]string
		want     string
		err      string
	}{
		{"single account needs no phone", []string{"+391111111111"}, nil, "", nil, "+391111111111", ""},
		{"new account from snapshot", []string{"+391111111111", "+392222222222"}, map[string]bool{"+391111111111": true}, "", nil, "+392222222222", ""},
		{"new account overrides stale phone", []string{"+391111111111", "+392222222222"}, map[string]bool{"+391111111111": true}, "+391111111111", nil, "+392222222222", ""},
		{"multiple without match", []string{"+391111111111", "+392222222222"}, nil, "", nil, "", "multiple linked accounts"},
		{"env conflict", []string{"+391111111111"}, nil, "", map[string]string{"SIGNAL_USER_NUMBER": "+390000000000"}, "", "SIGNAL_USER_NUMBER selects a different account"},
		{"single matching snapshot", []string{"+391111111111"}, map[string]bool{"+391111111111": true}, "", nil, "+391111111111", ""},
		{"none", nil, nil, "", nil, "", "did not report a linked account"},
	}
	for _, c := range cases {
		e := newTest(t, c.env)
		e.cli.accounts = c.accounts
		got, err := ResolveLinkedSignalAccount(ctx, e.cli, e.cfg, c.phone, c.previous)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
	e := newTest(t, nil)
	e.cli.listErr = errors.New("signal-cli could not list the linked accounts")
	if _, err := ResolveLinkedSignalAccount(ctx, e.cli, e.cfg, "", nil); err == nil {
		t.Error("list failure must propagate")
	}
}

func TestPersistRefusesBadConfigAndCreatesMissing(t *testing.T) {
	dir := t.TempDir()
	cf := filepath.Join(dir, "config.json")
	os.WriteFile(cf, []byte("{not json"), 0o600)
	if err := PersistSignalAccount(cf, "+39111"); err == nil || !strings.Contains(err.Error(), "invalid or unreadable") {
		t.Errorf("invalid: %v", err)
	}
	os.WriteFile(cf, []byte("[1,2]"), 0o600)
	if err := PersistSignalAccount(cf, "+39111"); err == nil || !strings.Contains(err.Error(), "must contain a JSON object") {
		t.Errorf("non-object: %v", err)
	}
	os.Remove(cf)
	if err := PersistSignalAccount(cf, "+39111"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cf)
	if !strings.Contains(string(b), `"user_number": "+39111"`) {
		t.Errorf("%s", b)
	}
	// a replace failure leaves no temp file: target is a directory
	d2 := t.TempDir()
	target := filepath.Join(d2, "config.json")
	os.Mkdir(target, 0o755)
	os.WriteFile(filepath.Join(target, "x"), nil, 0o600) // non-empty: rename over it fails
	if err := PersistSignalAccount(target, "+39111"); err == nil {
		t.Error("expected failure")
	}
	if left, _ := filepath.Glob(filepath.Join(d2, ".config.json.*")); len(left) != 0 {
		t.Errorf("leftovers %v", left)
	}
}

func TestParseAccounts(t *testing.T) {
	got := ParseAccounts("Account: +391111111111\n+392222222222 x\n+391111111111\n+1234 +12345678901234567\n")
	if !reflect.DeepEqual(got, []string{"+391111111111", "+392222222222"}) {
		t.Fatalf("%v", got)
	}
}

// ---- status -------------------------------------------------------------

type pairBackend struct {
	proto.Backend
	needs bool
}

func (b *pairBackend) NeedsPairing() bool                        { return b.needs }
func (b *pairBackend) PairingQR(context.Context) (string, error) { return "", nil }

func TestStatusReportsConfigurationWithoutIO(t *testing.T) {
	e := newTest(t, nil)
	s := e.service(bmap{
		"signal":   &signalBackend{fake.New("signal"), "+391111111111"},
		"whatsapp": fake.New("whatsapp"),
		"telegram": &pairBackend{fake.New("telegram"), true},
	})
	st := s.Status().Protocols
	want := map[string]api.LinkProtocolStatus{
		"signal":   {Available: true, Account: "+391111111111", Linked: api.Ptr(true)},
		"whatsapp": {Available: true},
		"telegram": {Available: true, Linked: api.Ptr(false)},
	}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("%+v", st)
	}
	st = e.service(bmap{"signal": &signalBackend{fake.New("signal"), ""}}).Status().Protocols
	if st["signal"].Linked == nil || *st["signal"].Linked || st["whatsapp"].Available || st["telegram"].Available || st["telegram"].Linked != nil {
		t.Fatalf("%+v", st)
	}
	b, _ := json.Marshal(st["whatsapp"])
	if string(b) != `{"available":false,"account":"","linked":null}` {
		t.Fatalf("%s", b)
	}
}

// ---- signal flow -----------------------------------------------------------

func TestSignalStartExtractsLinkAndNamesDevice(t *testing.T) {
	e := newTest(t, nil)
	e.cli.procs = []*fakeProc{lineProc("some error occurred", "sgnl://linkdevice?uuid=abc", "more")}
	q, err := e.service(bmap{}).Start(context.Background(), "signal", "", "My Laptop")
	if err != nil || q != (api.LinkQrOut{Kind: "url", Data: "sgnl://linkdevice?uuid=abc"}) {
		t.Fatalf("%+v %v", q, err)
	}
	if !reflect.DeepEqual(e.cli.started, []string{"My Laptop"}) {
		t.Fatal(e.cli.started)
	}
}

func TestSignalSnapshotFailureDoesNotBlockQR(t *testing.T) {
	e := newTest(t, nil)
	e.cli.listErr = errors.New("boom")
	e.cli.procs = []*fakeProc{lineProc("sgnl://linkdevice?uuid=abc")}
	s := e.service(bmap{})
	q, err := s.Start(context.Background(), "signal", "", "d")
	if err != nil || q.Data != "sgnl://linkdevice?uuid=abc" {
		t.Fatal(q, err)
	}
	if len(s.session("signal").accountsBefore) != 0 {
		t.Fatal("snapshot must be empty")
	}
}

func TestSignalNoLinkURLKillsProcess(t *testing.T) {
	e := newTest(t, nil)
	p := lineProc("no link here")
	e.cli.procs = []*fakeProc{p}
	s := e.service(bmap{})
	_, err := s.Start(context.Background(), "signal", "", "d")
	var le *LinkError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "Could not find Signal link URL") {
		t.Fatalf("%v", err)
	}
	if p.terminatedN() != 1 || s.IsActive("signal") {
		t.Fatal("process not killed / session left")
	}
}

func TestSignalPollFollowsSubprocess(t *testing.T) {
	e := newTest(t, nil)
	p := lineProc("sgnl://linkdevice?uuid=abc")
	e.cli.procs = []*fakeProc{p}
	s := e.service(bmap{})
	s.Start(context.Background(), "signal", "", "d")
	poll := func() api.LinkPollOut {
		o, err := s.Poll(context.Background(), "signal")
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if poll().State != "waiting" {
		t.Fatal("running -> waiting")
	}
	p.setExit(1)
	if poll().State != "waiting" {
		t.Fatal("failed -> waiting")
	}
	p.setExit(0)
	if got := poll(); !reflect.DeepEqual(got, api.LinkPollOut{State: "finalize"}) {
		t.Fatalf("%+v", got)
	}
	_, err := s.Poll(context.Background(), "whatsapp")
	var nl *NoLinkInProgress
	if !errors.As(err, &nl) || err.Error() != "No link in progress" {
		t.Fatalf("%v", err)
	}
}

func TestSignalCancelBehaviour(t *testing.T) {
	e := newTest(t, nil)
	running := lineProc("sgnl://linkdevice?uuid=1")
	running.termErr = errors.New("kill failed")
	e.cli.procs = []*fakeProc{running}
	s := e.service(bmap{})
	s.Start(context.Background(), "signal", "", "d")
	s.Cancel("signal") // terminate failure is survived
	s.Cancel("signal") // nothing left: no-op
	if running.terminatedN() != 1 || s.IsActive("signal") {
		t.Fatal("cancel")
	}
	done := lineProc("sgnl://linkdevice?uuid=1")
	done.setExit(0)
	e.cli.procs = []*fakeProc{done}
	s.Start(context.Background(), "signal", "", "d")
	s.Cancel("signal")
	if done.terminatedN() != 0 {
		t.Fatal("an exited process must not be terminated")
	}
}

func TestSignalCancelWhileStartingDoesNotLeak(t *testing.T) {
	e := newTest(t, nil)
	p := &fakeProc{}
	s := e.service(bmap{})
	e.cli.startReturn = p
	e.cli.onStart = func() { s.Cancel("signal") }
	_, err := s.Start(context.Background(), "signal", "", "d")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("%v", err)
	}
	if p.terminatedN() != 1 {
		t.Fatal("process leaked")
	}
}

func TestNewStartReplacesPreviousAndReapsAbandoned(t *testing.T) {
	e := newTest(t, nil)
	first, second := lineProc("sgnl://linkdevice?uuid=1"), lineProc("sgnl://linkdevice?uuid=2")
	e.cli.procs = []*fakeProc{first, second}
	s := e.service(bmap{})
	s.Start(context.Background(), "signal", "", "d")
	s.Start(context.Background(), "signal", "", "d")
	if first.terminatedN() != 1 || second.terminatedN() != 0 {
		t.Fatal("replace")
	}
	// abandoned: a start 16 minutes later reaps the leftovers of other protocols
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.session("signal").created = now.Add(-SessionMaxAge - time.Minute)
	third := lineProc("sgnl://linkdevice?uuid=3")
	e.cli.procs = []*fakeProc{third}
	s.Start(context.Background(), "signal", "", "d")
	if second.terminatedN() != 1 {
		t.Fatal("abandoned session not terminated")
	}
	s.CancelAll()
	if third.terminatedN() != 1 || s.IsActive("signal") {
		t.Fatal("cancel_all")
	}
}

func TestFinalizeUpdatesBackendAndPersists(t *testing.T) {
	e := newTest(t, nil)
	be := &signalBackend{fake.New("signal"), ""}
	p := lineProc("sgnl://linkdevice?uuid=1")
	p.setExit(0)
	e.cli.procs = []*fakeProc{p}
	e.cli.accounts = []string{"+391111111111"}
	s := e.service(bmap{"signal": be})
	s.Start(context.Background(), "signal", "", "d")
	e.cli.accounts = []string{"+391111111111", "+392222222222"}
	acc, err := s.FinalizeSignal(context.Background(), "")
	if err != nil || acc != "+392222222222" || be.number != acc {
		t.Fatalf("%q %v %q", acc, err, be.number)
	}
	if s.IsActive("signal") {
		t.Fatal("session must end")
	}
	b, _ := os.ReadFile(e.cfg.ConfigFile())
	if !strings.Contains(string(b), "+392222222222") {
		t.Fatal(string(b))
	}
}

func TestFinalizeRejectsSwitchBeforePersisting(t *testing.T) {
	e := newTest(t, nil)
	be := &signalBackend{fake.New("signal"), "+390000000000"}
	e.cli.accounts = []string{"+391111111111"}
	_, err := e.service(bmap{"signal": be}).FinalizeSignal(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "switching Signal accounts") {
		t.Fatalf("%v", err)
	}
	if _, serr := os.Stat(e.cfg.ConfigFile()); serr == nil {
		t.Fatal("config persisted despite the refusal")
	}
	// no backend at all still persists
	acc, err := e.service(bmap{}).FinalizeSignal(context.Background(), "")
	if err != nil || acc != "+391111111111" {
		t.Fatal(acc, err)
	}
}

func TestFailedFinalizeKeepsSession(t *testing.T) {
	e := newTest(t, nil)
	p := lineProc("sgnl://linkdevice?uuid=1")
	e.cli.procs = []*fakeProc{p}
	s := e.service(bmap{"signal": &signalBackend{fake.New("signal"), ""}})
	s.Start(context.Background(), "signal", "", "d")
	e.cli.accounts = nil
	if _, err := s.FinalizeSignal(context.Background(), ""); err == nil {
		t.Fatal("expected failure")
	}
	if !s.IsActive("signal") {
		t.Fatal("session dropped after a failed finalize")
	}
}

// The two signal-cli failures Python distinguishes: find_signal_cli() absent
// (FileNotFoundError "signal-cli not found in ./bin/") and a failed
// listAccounts (RuntimeError "signal-cli could not list the linked accounts").
// link.* maps both to backend_error keeping the message as is; the contract
// parity suite pins the same pair (tests/contract/test_parity.py).
func TestSignalCLIErrorMessages(t *testing.T) {
	if _, err := NewExecCLI(t.TempDir()).ListAccounts(context.Background()); err == nil ||
		err.Error() != "signal-cli not found in ./bin/" {
		t.Fatalf("no bin dir: %v", err)
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "signal-cli-0.13.9", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "signal-cli")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecCLI(dir).ListAccounts(context.Background()); err == nil ||
		err.Error() != "signal-cli could not list the linked accounts" {
		t.Fatalf("failing listAccounts: %v", err)
	}

	// link.finalize without a CLI: Python's message, code backend_error.
	_, err := New(Deps{}).FinalizeSignal(context.Background(), "")
	if err == nil || err.Error() != "signal-cli not found in ./bin/" {
		t.Fatalf("finalize without cli: %v", err)
	}
	var oe *ops.Error
	if !errors.As(mapError(err), &oe) || oe.Code != api.CodeBackendError || oe.Message != err.Error() {
		t.Fatalf("finalize wire error: %#v", mapError(err))
	}
}

// ---- whatsapp ----------------------------------------------------------------

type waLinker struct {
	proto.Backend
	mu       sync.Mutex
	status   string
	ident    bool
	cur      QR
	fresh    QR
	freshErr error
	calls    []string
}

func (w *waLinker) SessionStatus(context.Context) (string, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, w.ident, nil
}
func (w *waLinker) PairingQR(context.Context) (QR, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, "current")
	return w.cur, nil
}
func (w *waLinker) FreshPairingQR(_ context.Context, reset bool) (QR, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if reset {
		w.calls = append(w.calls, "fresh+reset")
	} else {
		w.calls = append(w.calls, "fresh")
	}
	return w.fresh, w.freshErr
}

func waService(t *testing.T, w *waLinker) *Service {
	return newTest(t, nil).service(bmap{"whatsapp": w})
}

func TestWhatsAppStart(t *testing.T) {
	ctx := context.Background()
	w := &waLinker{Backend: fake.New("whatsapp"), status: "WORKING", ident: true}
	q, err := waService(t, w).Start(ctx, "whatsapp", "", "")
	if err != nil || q.Kind != "info" || !strings.Contains(q.Data, "already linked") || len(w.calls) != 0 {
		t.Fatalf("%+v %v", q, err)
	}
	// working without identity is not linked
	w = &waLinker{Backend: fake.New("whatsapp"), status: "WORKING", cur: QR{PNG: []byte("png"), OK: true}}
	q, _ = waService(t, w).Start(ctx, "whatsapp", "", "")
	if q.Kind != "png" || q.Data != "cG5n" {
		t.Fatalf("%+v", q)
	}
	w = &waLinker{Backend: fake.New("whatsapp"), status: "scan_qr_code", cur: QR{Text: "raw-qr", OK: true}}
	q, _ = waService(t, w).Start(ctx, "whatsapp", "", "")
	if q != (api.LinkQrOut{Kind: "text", Data: "raw-qr"}) {
		t.Fatalf("%+v", q)
	}
	// dead session restarts with reset
	w = &waLinker{Backend: fake.New("whatsapp"), status: "failed", fresh: QR{PNG: []byte("x"), OK: true}}
	q, err = waService(t, w).Start(ctx, "whatsapp", "", "")
	if err != nil || q.Kind != "png" || !reflect.DeepEqual(w.calls, []string{"fresh+reset"}) {
		t.Fatalf("%+v %v %v", q, err, w.calls)
	}
	w = &waLinker{Backend: fake.New("whatsapp"), status: ""}
	_, err = waService(t, w).Start(ctx, "whatsapp", "", "")
	if err == nil || !strings.Contains(err.Error(), "Failed to restart session (status was: )") {
		t.Fatalf("%v", err)
	}
	// falls back to a fresh QR without reset
	w = &waLinker{Backend: fake.New("whatsapp"), status: "scan_qr_code", fresh: QR{Text: "t", OK: true}}
	q, _ = waService(t, w).Start(ctx, "whatsapp", "", "")
	if q.Data != "t" || !reflect.DeepEqual(w.calls, []string{"current", "fresh"}) {
		t.Fatalf("%+v %v", q, w.calls)
	}
	w = &waLinker{Backend: fake.New("whatsapp"), status: "starting"}
	_, err = waService(t, w).Start(ctx, "whatsapp", "", "")
	if err == nil || !strings.Contains(err.Error(), "No QR available (session status: starting)") {
		t.Fatalf("%v", err)
	}
	_, err = newTest(t, nil).service(bmap{}).Start(ctx, "whatsapp", "", "")
	var lu *LinkUnavailable
	if !errors.As(err, &lu) || err.Error() != "WhatsApp backend not available" {
		t.Fatalf("%v", err)
	}
}

func TestWhatsAppPoll(t *testing.T) {
	ctx := context.Background()
	w := &waLinker{Backend: fake.New("whatsapp"), status: "scan_qr_code", cur: QR{Text: "t", OK: true}, fresh: QR{Text: "new", OK: true}}
	s := waService(t, w)
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.Start(ctx, "whatsapp", "", "")
	poll := func() api.LinkPollOut { o, _ := s.Poll(ctx, "whatsapp"); return o }

	if o := poll(); o.State != "waiting" || o.QR != nil {
		t.Fatalf("not renewed before a minute: %+v", o)
	}
	now = now.Add(61 * time.Second)
	o := poll()
	if o.QR == nil || o.QR.Data != "new" || !reflect.DeepEqual(w.calls[len(w.calls)-1], "fresh") {
		t.Fatalf("%+v %v", o, w.calls)
	}
	if poll().QR != nil {
		t.Fatal("renewal must restart the minute")
	}
	// other states: no renewal
	w.status = "starting"
	now = now.Add(2 * time.Minute)
	if poll().QR != nil {
		t.Fatal("renewed in a non-scan state")
	}
	// missing fresh QR is shown as text
	w.status, w.fresh = "scan_qr_code", QR{}
	if o := poll(); o.QR == nil || *o.QR != (api.LinkQrOut{Kind: "text", Data: "ERROR: No fresh QR available"}) {
		t.Fatalf("%+v", o)
	}
	// renewal failure retries on the next poll
	now = now.Add(2 * time.Minute)
	w.freshErr = errors.New("net")
	if o := poll(); o.QR != nil {
		t.Fatal("failed renewal must not show a QR")
	}
	w.freshErr, w.fresh = nil, QR{Text: "again", OK: true}
	if o := poll(); o.QR == nil || o.QR.Data != "again" {
		t.Fatalf("%+v", o)
	}
	// done requires WORKING and an identity
	w.status = "working"
	if poll().State != "waiting" {
		t.Fatal("working without identity")
	}
	w.ident = true
	if poll().State != "done" {
		t.Fatal("done")
	}
}

func TestWhatsAppGenericPairerFallback(t *testing.T) {
	ctx := context.Background()
	fb := fake.New("whatsapp")
	fb.SetPairing(true, "wa://qr-data")
	s := newTest(t, nil).service(bmap{"whatsapp": fb})
	q, err := s.Start(ctx, "whatsapp", "", "")
	if err != nil || q != (api.LinkQrOut{Kind: "text", Data: "wa://qr-data"}) {
		t.Fatalf("%+v %v", q, err)
	}
	if o, _ := s.Poll(ctx, "whatsapp"); o.State != "waiting" {
		t.Fatalf("%+v", o)
	}
	fb.SetPairing(false, "")
	if o, _ := s.Poll(ctx, "whatsapp"); o.State != "done" {
		t.Fatalf("%+v", o)
	}
}

// ---- telegram ------------------------------------------------------------------

type tgBackend struct {
	*fake.Backend
	mu    sync.Mutex
	qr    string
	needs bool
	conn  bool
}

func (t *tgBackend) PairingQR(context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.qr, nil
}
func (t *tgBackend) NeedsPairing() bool { return true }
func (t *tgBackend) Needs2FA() bool     { t.mu.Lock(); defer t.mu.Unlock(); return t.needs }
func (t *tgBackend) IsConnected() bool  { t.mu.Lock(); defer t.mu.Unlock(); return t.conn }

func TestTelegramFlow(t *testing.T) {
	ctx := context.Background()
	tg := &tgBackend{Backend: fake.New("telegram"), qr: "tg://login?token=abc"}
	s := newTest(t, nil).service(bmap{"telegram": tg})
	now := time.Now()
	s.Now = func() time.Time { return now }
	q, err := s.Start(ctx, "telegram", "", "")
	if err != nil || q != (api.LinkQrOut{Kind: "url", Data: "tg://login?token=abc"}) {
		t.Fatalf("%+v %v", q, err)
	}
	poll := func() api.LinkPollOut { o, _ := s.Poll(ctx, "telegram"); return o }
	if o := poll(); o.State != "waiting" || o.Needs2FA || o.QR != nil {
		t.Fatalf("%+v", o)
	}
	// 2FA is reported once
	tg.needs = true
	if !poll().Needs2FA || poll().Needs2FA {
		t.Fatal("needs_2fa must be reported exactly once")
	}
	// QR renewal after a minute; INFO/ERROR prefixes
	now = now.Add(61 * time.Second)
	tg.qr = "tg://login?token=new"
	o := poll()
	if o.QR == nil || o.QR.Data != "tg://login?token=new" || o.Needs2FA {
		t.Fatalf("%+v", o)
	}
	// the renewal re-armed the report: shown again on the next poll
	if !poll().Needs2FA {
		t.Fatal("2FA must be reported again after a QR renewal")
	}
	now = now.Add(61 * time.Second)
	tg.qr = "INFO: scan in app"
	if o := poll(); o.QR == nil || *o.QR != (api.LinkQrOut{Kind: "info", Data: " scan in app"}) {
		t.Fatalf("%+v", o)
	}
	tg.conn = true
	if poll().State != "done" {
		t.Fatal("done")
	}
}

func TestTelegramStartErrors(t *testing.T) {
	ctx := context.Background()
	tg := &tgBackend{Backend: fake.New("telegram"), qr: "ERROR: API id missing"}
	_, err := newTest(t, nil).service(bmap{"telegram": tg}).Start(ctx, "telegram", "", "")
	var le *LinkError
	if !errors.As(err, &le) || err.Error() != "API id missing" {
		t.Fatalf("%v", err)
	}
	tg.qr = ""
	_, err = newTest(t, nil).service(bmap{"telegram": tg}).Start(ctx, "telegram", "", "")
	if err == nil || err.Error() != "No QR data" {
		t.Fatalf("%v", err)
	}
	_, err = newTest(t, nil).service(bmap{}).Start(ctx, "telegram", "", "")
	var lu *LinkUnavailable
	if !errors.As(err, &lu) {
		t.Fatalf("%v", err)
	}
}

// ---- operations ------------------------------------------------------------------

func call(t *testing.T, reg *ops.Registry, name string, p any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := reg.Call(context.Background(), name, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m, nil
}

func wantErr(t *testing.T, err error, code, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s %q", code, msg)
	}
	oe := ops.AsError(err)
	if oe.Code != code || oe.Message != msg {
		t.Fatalf("got %s %q want %s %q", oe.Code, oe.Message, code, msg)
	}
}

func TestOperations(t *testing.T) {
	e := newTest(t, nil)
	p := lineProc("sgnl://linkdevice?uuid=abc")
	e.cli.procs = []*fakeProc{p}
	reconnected := []string{}
	reg := ops.NewRegistry()
	RegisterOps(reg, Deps{
		Backends:     bmap{"signal": &signalBackend{fake.New("signal"), ""}},
		Config:       e.cfg,
		SignalCLI:    e.cli,
		Reconnect:    func(p []string) { reconnected = p },
		LinkRequired: func() bool { return true },
	})
	for _, n := range []string{"link.status", "link.required", "link.start", "link.poll", "link.finalize", "link.submit_2fa", "link.cancel", "link.reconnect"} {
		if !reg.Has(n) {
			t.Fatalf("%s not registered", n)
		}
	}
	out, _ := call(t, reg, "link.required", map[string]any{})
	if !reflect.DeepEqual(out, map[string]any{"protocols": map[string]any{"whatsapp": true}}) {
		t.Fatalf("%v", out)
	}
	out, err := call(t, reg, "link.start", map[string]any{"protocol": "signal"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, map[string]any{"qr": map[string]any{"kind": "url", "data": "sgnl://linkdevice?uuid=abc"}}) {
		t.Fatalf("%v", out)
	}
	if e.cli.started[0] != "Signal-TUI-Client" {
		t.Fatalf("default device name: %v", e.cli.started)
	}
	out, _ = call(t, reg, "link.poll", map[string]any{"protocol": "signal"})
	if !reflect.DeepEqual(out, map[string]any{"state": "waiting", "needs_2fa": false, "qr": nil}) {
		t.Fatalf("%v", out)
	}
	_, err = call(t, reg, "link.poll", map[string]any{"protocol": "telegram"})
	wantErr(t, err, api.CodeConflict, "No link in progress")
	out, _ = call(t, reg, "link.cancel", map[string]any{"protocol": "signal"})
	if out["ok"] != true || p.terminatedN() != 1 {
		t.Fatalf("%v", out)
	}
	_, err = call(t, reg, "link.cancel", map[string]any{"protocol": "signal"}) // idempotent
	if err != nil {
		t.Fatal(err)
	}
	// error mapping
	_, err = call(t, reg, "link.start", map[string]any{"protocol": "whatsapp"})
	wantErr(t, err, api.CodeBackendDown, "WhatsApp backend not available")
	e.cli.procs = []*fakeProc{lineProc("nothing")}
	_, err = call(t, reg, "link.start", map[string]any{"protocol": "signal"})
	wantErr(t, err, api.CodeBackendError, "Could not find Signal link URL in signal-cli output. Is signal-cli configured correctly?")
	_, err = call(t, reg, "link.submit_2fa", map[string]any{"password": "x"})
	wantErr(t, err, api.CodeBackendDown, "Telegram backend not available")
	// validation
	_, err = call(t, reg, "link.start", map[string]any{"protocol": "matrix"})
	wantErr(t, err, api.CodeInvalidRequest, "Invalid request")
	_, err = call(t, reg, "link.start", map[string]any{})
	wantErr(t, err, api.CodeInvalidRequest, "Invalid request")
	_, err = call(t, reg, "link.start", map[string]any{"protocol": "signal", "phone": 5})
	wantErr(t, err, api.CodeInvalidRequest, "Invalid request")
	_, err = call(t, reg, "link.finalize", map[string]any{"protocol": "whatsapp"})
	wantErr(t, err, api.CodeInvalidRequest, "Invalid request")
	// reconnect
	out, _ = call(t, reg, "link.reconnect", map[string]any{"protocols": []string{"signal", "telegram"}})
	if out["ok"] != true || !reflect.DeepEqual(reconnected, []string{"signal", "telegram"}) {
		t.Fatalf("%v %v", out, reconnected)
	}
	reg2 := ops.NewRegistry()
	RegisterOps(reg2, Deps{Backends: bmap{}, Config: e.cfg, SignalCLI: e.cli})
	_, err = call(t, reg2, "link.reconnect", map[string]any{"protocols": []string{"signal"}})
	wantErr(t, err, api.CodeNotSupported, "This server cannot reconnect backends")
	out, _ = call(t, reg2, "link.required", map[string]any{})
	if out["protocols"].(map[string]any)["whatsapp"] != false {
		t.Fatalf("%v", out)
	}
}

func TestTelegram2FAOp(t *testing.T) {
	e := newTest(t, nil)
	tg := fake.New("telegram")
	tg.SetTwoFactorPassword("secret")
	reg := ops.NewRegistry()
	RegisterOps(reg, Deps{Backends: bmap{"telegram": tg}, Config: e.cfg, SignalCLI: e.cli})
	out, err := call(t, reg, "link.submit_2fa", map[string]any{"password": "wrong"})
	if err != nil || out["ok"] != false {
		t.Fatalf("%v %v", out, err)
	}
	out, _ = call(t, reg, "link.submit_2fa", map[string]any{"password": "secret"})
	if out["ok"] != true {
		t.Fatalf("%v", out)
	}
}
