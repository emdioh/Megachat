package linking

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

const (
	// QRRefreshAfter: WhatsApp and Telegram tokens expire after about a minute.
	QRRefreshAfter = 60 * time.Second
	// SessionMaxAge: a session older than this is terminated by the next start.
	SessionMaxAge = 900 * time.Second
)

var waScanStates = map[string]bool{"scan_qr": true, "scan_qr_code": true, "unpaired": true, "pending": true}

// LinkError: a link step failed; the message is shown to the user as is.
type LinkError struct{ Msg string }

func (e *LinkError) Error() string { return e.Msg }

// LinkUnavailable: the protocol's backend is missing or not configured.
type LinkUnavailable struct{ Msg string }

func (e *LinkUnavailable) Error() string { return e.Msg }

// NoLinkInProgress: poll for a protocol with no started link.
type NoLinkInProgress struct{ Msg string }

func (e *NoLinkInProgress) Error() string { return e.Msg }

func linkErr(format string, a ...any) error { return &LinkError{Msg: fmt.Sprintf(format, a...)} }

func qr(kind, data string) api.LinkQrOut { return api.LinkQrOut{Kind: kind, Data: data} }

func qrFromWaha(q QR, missing string) api.LinkQrOut {
	switch {
	case q.OK && q.PNG != nil:
		return qr(api.QRKindPNG, base64.StdEncoding.EncodeToString(q.PNG))
	case q.OK:
		return qr(api.QRKindText, q.Text)
	}
	return qr(api.QRKindText, missing)
}

// qrFromTelegram: the backend reports INFO:/ERROR: strings or a URL.
func qrFromTelegram(raw string) api.LinkQrOut {
	switch {
	case strings.HasPrefix(raw, "INFO:"):
		return qr(api.QRKindInfo, raw[5:])
	case strings.HasPrefix(raw, "ERROR:"):
		// Python raw[7:] skips the space after the colon.
		if len(raw) >= 7 {
			return qr(api.QRKindError, raw[7:])
		}
		return qr(api.QRKindError, "")
	}
	return qr(api.QRKindURL, raw)
}

// WhatsAppStatusIsLinked requires both WORKING state and an identity.
func WhatsAppStatusIsLinked(status string, hasIdentity bool) bool {
	return strings.ToLower(status) == "working" && hasIdentity
}

type session struct {
	protocol   string
	phone      string
	deviceName string
	created    time.Time

	mu             sync.Mutex
	qrStarted      time.Time
	proc           LinkProcess
	accountsBefore map[string]bool
	twofaShown     bool
}

// Service holds the link sessions, one per protocol. It is passive: it owns no
// timers; the frontend polls.
type Service struct {
	d   Deps
	cli SignalCLI
	// Now is the clock (tests).
	Now func() time.Time

	mu       sync.Mutex
	sessions map[string]*session
}

// New builds a Service.
func New(d Deps) *Service {
	cli := d.SignalCLI
	if cli == nil && d.Config != nil {
		cli = NewExecCLI(d.Config.SignalCLIBinDir())
	}
	return &Service{d: d, cli: cli, Now: time.Now, sessions: map[string]*session{}}
}

// IsActive reports whether a link flow is open for protocol (no I/O).
func (s *Service) IsActive(protocol string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[protocol]
	return ok
}

func (s *Service) backend(protocol string) proto.Backend {
	if s.d.Backends == nil {
		return nil
	}
	b, ok := s.d.Backends.Get(protocol)
	if !ok {
		return nil
	}
	return b
}

// ---- status ---------------------------------------------------------------

// Status: per protocol configured/account/linked, in memory only.
func (s *Service) Status() api.LinkStatusOut {
	out := map[string]api.LinkProtocolStatus{}
	sig := s.backend(api.ProtocolSignal)
	account := ""
	if sig != nil {
		if sa, ok := sig.(SignalAccount); ok {
			account = sa.UserNumber()
		} else if s.d.Config != nil {
			account = s.d.Config.SignalUserNumber()
		}
	}
	out[api.ProtocolSignal] = api.LinkProtocolStatus{Available: sig != nil, Account: account, Linked: api.Ptr(account != "")}
	out[api.ProtocolWhatsApp] = api.LinkProtocolStatus{Available: s.backend(api.ProtocolWhatsApp) != nil}
	tg := s.backend(api.ProtocolTelegram)
	st := api.LinkProtocolStatus{Available: tg != nil}
	if tg != nil {
		if p, ok := tg.(proto.Pairer); ok {
			st.Linked = api.Ptr(!p.NeedsPairing())
		}
	}
	out[api.ProtocolTelegram] = st
	return api.LinkStatusOut{Protocols: out}
}

// Required: protocols whose session needs a new pairing QR.
func (s *Service) Required() api.LinkRequiredOut {
	need := false
	if s.d.LinkRequired != nil {
		need = s.d.LinkRequired()
	}
	return api.LinkRequiredOut{Protocols: map[string]bool{api.ProtocolWhatsApp: need}}
}

// ---- sessions ------------------------------------------------------------

func (s *Service) reap() {
	cutoff := s.Now().Add(-SessionMaxAge)
	s.mu.Lock()
	var old []*session
	for p, sess := range s.sessions {
		if sess.created.Before(cutoff) {
			old = append(old, sess)
			delete(s.sessions, p)
		}
	}
	s.mu.Unlock()
	for _, sess := range old {
		slog.Info("Reaping abandoned link session", "protocol", sess.protocol)
		terminate(sess)
	}
}

func terminate(sess *session) {
	sess.mu.Lock()
	proc := sess.proc
	sess.proc = nil
	sess.mu.Unlock()
	if proc == nil {
		return
	}
	if done, _ := proc.Exited(); !done {
		slog.Info("Killing signal-cli link subprocess")
		if err := proc.Terminate(); err != nil {
			slog.Debug("Failed to terminate link subprocess", "err", err)
		}
	}
}

func (s *Service) session(protocol string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[protocol]
}

// Cancel stops a link in progress (kills the signal-cli subprocess).
func (s *Service) Cancel(protocol string) {
	s.mu.Lock()
	sess := s.sessions[protocol]
	delete(s.sessions, protocol)
	s.mu.Unlock()
	if sess != nil {
		terminate(sess)
	}
}

// CancelAll cancels every session.
func (s *Service) CancelAll() {
	s.mu.Lock()
	var all []*session
	for p, sess := range s.sessions {
		all = append(all, sess)
		delete(s.sessions, p)
	}
	s.mu.Unlock()
	for _, sess := range all {
		terminate(sess)
	}
}

// ---- start ---------------------------------------------------------------

// Start begins linking protocol and returns what to show. It blocks (signal-cli
// startup, WAHA and Telegram requests). A link already open is cancelled first.
func (s *Service) Start(ctx context.Context, protocol, phone, deviceName string) (api.LinkQrOut, error) {
	s.reap()
	now := s.Now()
	sess := &session{protocol: protocol, phone: phone, deviceName: deviceName, created: now, qrStarted: now}
	s.mu.Lock()
	previous := s.sessions[protocol]
	s.sessions[protocol] = sess
	s.mu.Unlock()
	if previous != nil {
		terminate(previous)
	}
	var out api.LinkQrOut
	var err error
	switch protocol {
	case api.ProtocolSignal:
		out, err = s.startSignal(ctx, sess)
	case api.ProtocolWhatsApp:
		out, err = s.startWhatsApp(ctx)
	case api.ProtocolTelegram:
		out, err = s.startTelegram(ctx)
	default:
		err = linkErr("Unknown protocol %q", protocol)
	}
	if err != nil {
		s.mu.Lock()
		if s.sessions[protocol] == sess {
			delete(s.sessions, protocol)
		}
		s.mu.Unlock()
		terminate(sess)
		return api.LinkQrOut{}, err
	}
	return out, nil
}

var signalLinkRE = regexp.MustCompile(`((?:sgnl|signal)://link[^\s]*)`)

func (s *Service) startSignal(ctx context.Context, sess *session) (api.LinkQrOut, error) {
	if s.backend(api.ProtocolSignal) == nil {
		return api.LinkQrOut{}, &LinkUnavailable{"Signal backend not available: set \"signal\": {\"enabled\": true} in config.json (or SIGNAL_ENABLED=1) and restart the server"}
	}
	if s.cli == nil {
		return api.LinkQrOut{}, linkErr("signal-cli not found in ./bin/")
	}
	sess.accountsBefore = map[string]bool{}
	if accts, err := s.cli.ListAccounts(ctx); err == nil {
		for _, a := range accts {
			sess.accountsBefore[a] = true
		}
	} else {
		slog.Debug("Could not snapshot Signal accounts", "err", err)
	}
	proc, err := s.cli.StartLink(sess.deviceName)
	if err != nil {
		return api.LinkQrOut{}, err
	}
	// Registered before the blocking read, so a cancel can end it.
	sess.mu.Lock()
	sess.proc = proc
	sess.mu.Unlock()
	if s.session(api.ProtocolSignal) != sess { // cancelled while starting
		terminate(sess)
		return api.LinkQrOut{}, linkErr("Link cancelled")
	}
	for {
		line, ok := proc.ReadLine()
		if !ok {
			break
		}
		if m := signalLinkRE.FindStringSubmatch(line); m != nil {
			proc.Drain()
			slog.Info("Signal link URL found")
			return qr(api.QRKindURL, m[1]), nil
		}
		l := strings.ToLower(line)
		if strings.Contains(l, "error") || strings.Contains(l, "cannot") {
			slog.Warn("signal-cli reported an error during linking")
		}
	}
	return api.LinkQrOut{}, linkErr("Could not find Signal link URL in signal-cli output. Is signal-cli configured correctly?")
}

const (
	whatsAppUnavailable = "WhatsApp backend not available: set \"whatsapp\": {\"enabled\": true} in config.json (or WHATSAPP_ENABLED=1) and restart the server"
	telegramUnavailable = "Telegram backend not available: set \"telegram\": {\"enabled\": true} in config.json (or TELEGRAM_ENABLED=1, with telegram.api_id and telegram.api_hash) and restart the server"
)

func (s *Service) startWhatsApp(ctx context.Context) (api.LinkQrOut, error) {
	be := s.backend(api.ProtocolWhatsApp)
	if be == nil {
		return api.LinkQrOut{}, &LinkUnavailable{whatsAppUnavailable}
	}
	rest, ok := be.(WhatsAppLinker)
	if !ok {
		return s.startPairer(ctx, be, whatsAppUnavailable)
	}
	status, ident, err := rest.SessionStatus(ctx)
	if err != nil {
		status, ident = "", false
	}
	st := strings.ToLower(status)
	if WhatsAppStatusIsLinked(st, ident) {
		return qr(api.QRKindInfo, "WhatsApp is already linked and working ✅\nNo QR needed."), nil
	}
	if st == "failed" || st == "stopped" || st == "stop" || st == "" {
		slog.Info("WhatsApp session dead, restarting for fresh QR", "status", st)
		q, err := rest.FreshPairingQR(ctx, true)
		if err == nil && q.OK {
			return qrFromWaha(q, ""), nil
		}
		return api.LinkQrOut{}, linkErr("Failed to restart session (status was: %s)", st)
	}
	if q, err := rest.PairingQR(ctx); err == nil && q.OK {
		return qrFromWaha(q, ""), nil
	}
	if q, err := rest.FreshPairingQR(ctx, false); err == nil && q.OK {
		return qrFromWaha(q, ""), nil
	}
	shown := st
	if shown == "" {
		shown = "unknown"
	}
	return api.LinkQrOut{}, linkErr("No QR available (session status: %s). The session may already be linked.", shown)
}

// startPairer is the generic flow over proto.Pairer (backends without the
// WAHA wrapper).
func (s *Service) startPairer(ctx context.Context, be proto.Backend, unavailable string) (api.LinkQrOut, error) {
	p, ok := be.(proto.Pairer)
	if !ok {
		return api.LinkQrOut{}, &LinkUnavailable{unavailable}
	}
	if !p.NeedsPairing() {
		return qr(api.QRKindInfo, "Already linked and working ✅\nNo QR needed."), nil
	}
	raw, err := p.PairingQR(ctx)
	if err != nil {
		return api.LinkQrOut{}, err
	}
	if raw == "" {
		return api.LinkQrOut{}, linkErr("No QR available. The session may already be linked.")
	}
	return qr(api.QRKindText, raw), nil
}

func (s *Service) startTelegram(ctx context.Context) (api.LinkQrOut, error) {
	be := s.backend(api.ProtocolTelegram)
	if be == nil {
		return api.LinkQrOut{}, &LinkUnavailable{telegramUnavailable}
	}
	p, ok := be.(proto.Pairer)
	if !ok {
		return api.LinkQrOut{}, &LinkUnavailable{telegramUnavailable}
	}
	raw, err := p.PairingQR(ctx)
	if err != nil {
		return api.LinkQrOut{}, err
	}
	if raw == "" {
		raw = "ERROR: No QR data"
	}
	q := qrFromTelegram(raw)
	if q.Kind == api.QRKindError {
		return api.LinkQrOut{}, linkErr("%s", q.Data)
	}
	return q, nil
}

// ---- poll ------------------------------------------------------------------

func progress(state string, needs2fa bool, q *api.LinkQrOut) api.LinkPollOut {
	return api.LinkPollOut{State: state, Needs2FA: needs2fa, QR: q}
}

// Poll reports the progress of the started link. It may block (WAHA/Telegram).
func (s *Service) Poll(ctx context.Context, protocol string) (api.LinkPollOut, error) {
	sess := s.session(protocol)
	if sess == nil {
		return api.LinkPollOut{}, &NoLinkInProgress{"No link in progress"}
	}
	switch protocol {
	case api.ProtocolSignal:
		return s.pollSignal(sess), nil
	case api.ProtocolWhatsApp:
		return s.pollWhatsApp(ctx, sess), nil
	}
	return s.pollTelegram(ctx, sess), nil
}

func (s *Service) pollSignal(sess *session) api.LinkPollOut {
	sess.mu.Lock()
	proc := sess.proc
	sess.mu.Unlock()
	if proc == nil {
		return progress(api.LinkStateWaiting, false, nil)
	}
	done, code := proc.Exited()
	if done && code == 0 {
		return progress(api.LinkStateFinalize, false, nil)
	}
	return progress(api.LinkStateWaiting, false, nil)
}

func (s *Service) pollWhatsApp(ctx context.Context, sess *session) api.LinkPollOut {
	be := s.backend(api.ProtocolWhatsApp)
	if be == nil {
		return progress(api.LinkStateWaiting, false, nil)
	}
	rest, ok := be.(WhatsAppLinker)
	if !ok {
		if p, ok := be.(proto.Pairer); ok && !p.NeedsPairing() {
			return progress(api.LinkStateDone, false, nil)
		}
		return progress(api.LinkStateWaiting, false, nil)
	}
	status, ident, err := rest.SessionStatus(ctx)
	if err != nil {
		status, ident = "", false
	}
	st := strings.ToLower(status)
	if WhatsAppStatusIsLinked(st, ident) {
		return progress(api.LinkStateDone, false, nil)
	}
	sess.mu.Lock()
	age := s.Now().Sub(sess.qrStarted)
	sess.mu.Unlock()
	if age < QRRefreshAfter || !waScanStates[st] {
		return progress(api.LinkStateWaiting, false, nil)
	}
	slog.Info("WhatsApp QR expired, refreshing...")
	q, err := rest.FreshPairingQR(ctx, false)
	if err != nil {
		slog.Error("Failed to refresh WhatsApp QR", "err", err)
		return progress(api.LinkStateWaiting, false, nil)
	}
	out := qrFromWaha(q, "ERROR: No fresh QR available")
	sess.mu.Lock()
	sess.qrStarted = s.Now()
	sess.mu.Unlock()
	return progress(api.LinkStateWaiting, false, &out)
}

func (s *Service) pollTelegram(ctx context.Context, sess *session) api.LinkPollOut {
	be := s.backend(api.ProtocolTelegram)
	if be == nil {
		return progress(api.LinkStateWaiting, false, nil)
	}
	if be.IsConnected() {
		return progress(api.LinkStateDone, false, nil)
	}
	needs2fa := false
	sess.mu.Lock()
	if tl, ok := be.(TelegramLogin); ok && tl.Needs2FA() && !sess.twofaShown {
		sess.twofaShown = true
		needs2fa = true
	}
	age := s.Now().Sub(sess.qrStarted)
	sess.mu.Unlock()
	var out *api.LinkQrOut
	if age >= QRRefreshAfter {
		slog.Info("Telegram QR expired, refreshing...")
		if p, ok := be.(proto.Pairer); ok {
			raw, err := p.PairingQR(ctx)
			if err != nil {
				slog.Error("Failed to refresh Telegram QR", "err", err)
			} else {
				if raw == "" {
					raw = "ERROR: No QR data"
				}
				q := qrFromTelegram(raw)
				out = &q
				sess.mu.Lock()
				sess.twofaShown = false
				sess.qrStarted = s.Now()
				sess.mu.Unlock()
			}
		}
	}
	return progress(api.LinkStateWaiting, needs2fa, out)
}

// ---- finish ----------------------------------------------------------------

var phoneStripRE = regexp.MustCompile(`[\s()\-]`)

// ResolveLinkedSignalAccount picks the freshly linked account without touching
// the configuration.
func ResolveLinkedSignalAccount(ctx context.Context, cli SignalCLI, cfg Config, phone string, previous map[string]bool) (string, error) {
	accounts, err := cli.ListAccounts(ctx)
	if err != nil {
		return "", err
	}
	var fresh []string
	for _, a := range accounts {
		if !previous[a] {
			fresh = append(fresh, a)
		}
	}
	normalized := phoneStripRE.ReplaceAllString(phone, "")
	var account string
	switch {
	case len(fresh) == 1:
		account = fresh[0]
	case contains(accounts, normalized):
		account = normalized
	case len(accounts) == 1:
		account = accounts[0]
	case len(accounts) == 0:
		return "", errors.New("signal-cli did not report a linked account")
	default:
		return "", errors.New("multiple linked accounts found; select the account number")
	}
	if envAcc := cfg.Env("SIGNAL_USER_NUMBER"); envAcc != "" && envAcc != account {
		return "", errors.New("SIGNAL_USER_NUMBER selects a different account; unset it and retry")
	}
	return account, nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// PersistSignalAccount atomically saves signal.user_number into config.json,
// preserving the other settings. An invalid or non-object existing file is
// refused. (Go writes keys sorted; Python kept insertion order.)
func PersistSignalAccount(configFile, account string) error {
	cfg := map[string]any{}
	if data, err := os.ReadFile(configFile); err == nil {
		var loaded any
		if err := json.Unmarshal(data, &loaded); err != nil {
			return errors.New("existing config.json is invalid or unreadable")
		}
		obj, ok := loaded.(map[string]any)
		if !ok {
			return errors.New("existing config.json must contain a JSON object")
		}
		cfg = obj
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing config.json is invalid or unreadable")
	}
	sec, ok := cfg["signal"].(map[string]any)
	if !ok || sec == nil {
		sec = map[string]any{}
		cfg["signal"] = sec
	}
	sec["user_number"] = account
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(configFile), ".config.json.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if _, err := os.Stat(name); err == nil {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(name, 0o644)
	return os.Rename(name, configFile)
}

// FinalizeSignal finds the new account, saves it and activates it on the
// Signal backend. Blocks (signal-cli listAccounts).
func (s *Service) FinalizeSignal(ctx context.Context, phone string) (string, error) {
	if s.cli == nil {
		return "", errors.New("signal-cli not found in ./bin/")
	}
	previous := map[string]bool{}
	if sess := s.session(api.ProtocolSignal); sess != nil {
		previous = sess.accountsBefore
	}
	account, err := ResolveLinkedSignalAccount(ctx, s.cli, s.d.Config, phone, previous)
	if err != nil {
		return "", err
	}
	be := s.backend(api.ProtocolSignal)
	var sa SignalAccount
	if be != nil {
		sa, _ = be.(SignalAccount)
	}
	if sa != nil {
		if cur := sa.UserNumber(); cur != "" && cur != account {
			return "", errors.New("switching Signal accounts in an active profile is not supported")
		}
	}
	if err := PersistSignalAccount(s.d.Config.ConfigFile(), account); err != nil {
		return "", err
	}
	if sa != nil {
		sa.SetUserNumber(account)
	}
	s.Cancel(api.ProtocolSignal)
	return account, nil
}

// Submit2FA finishes the Telegram login with the password (false = wrong).
func (s *Service) Submit2FA(ctx context.Context, password string) (bool, error) {
	be := s.backend(api.ProtocolTelegram)
	if be == nil {
		return false, &LinkUnavailable{telegramUnavailable}
	}
	tf, ok := be.(proto.TwoFactor)
	if !ok {
		return false, &LinkUnavailable{telegramUnavailable}
	}
	return tf.Complete2FA(ctx, password)
}
