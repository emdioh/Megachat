package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// qrWaitTimeout is the QR scan wait of get_pairing_qr (120 s).
const qrWaitTimeout = 120 * time.Second

// runState is one run of the MTProto client (the Python event-loop thread).
type runState struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	outcome    chan struct{} // closed when the first outcome is known
	outcomeErr error
	once       sync.Once

	loggedIn     chan struct{} // closed when the login completed
	loggedInOnce sync.Once

	client *telegram.Client
	imp    *ImportedSession
	tokens chan struct{} // UpdateLoginToken signals
}

func (r *runState) signal(err error) {
	r.once.Do(func() {
		r.outcomeErr = err
		close(r.outcome)
	})
}

func (r *runState) markLoggedIn() { r.loggedInOnce.Do(func() { close(r.loggedIn) }) }

// loginState is the interactive-login state (QR waiter, phone/code).
type loginState struct {
	mu       sync.Mutex
	qrCancel context.CancelFunc
	phone    string
	codeHash string
}

// ---- Connect / Disconnect --------------------------------------------------------------

// Connect implements proto.Backend: it starts the MTProto client from the
// (imported) session, checks the authorisation and loads the dialogs. A
// session that is not authorised yields ErrNotAuthorized; the client then
// stays up so the QR / phone login can complete (see PairingQR).
func (b *Backend) Connect(ctx context.Context) error {
	b.lifeMu.Lock()
	b.apiMu.RLock()
	injected := b.api != nil && b.run == nil
	b.apiMu.RUnlock()
	b.lifeMu.Unlock()
	if injected {
		b.loadProtocolCache(ctx)
		return nil
	}
	if b.apiID == 0 || b.apiHash == "" {
		return ErrNotConfigured
	}
	r, err := b.ensureRun(ctx)
	if err != nil {
		return err
	}
	select {
	case <-r.outcome:
		return r.outcomeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ensureRun returns the live run, starting one when needed.
func (b *Backend) ensureRun(ctx context.Context) (*runState, error) {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	if b.run != nil {
		select {
		case <-b.run.done:
			b.run = nil
		default:
			return b.run, nil
		}
	}
	select {
	case <-b.closed:
		b.closed = make(chan struct{})
	default:
	}
	storage, imp, imported, err := ImportSession(ctx, b.sessionPath)
	if err != nil {
		return nil, fmt.Errorf("telegram: session import failed: %w", err)
	}
	if imported {
		b.log.Info("Telegram: imported the Telethon session", "kind", imp.Kind, "dc", imp.Data.DC)
	}
	if imp != nil {
		b.peers.merge(imp.Peers)
		b.peers.flush()
	}
	b.imp = imp
	b.states = newStateStorage(strings.TrimSuffix(b.sessionPath, ".session") + ".gotd-state.json")

	b.loadProtocolCache(ctx)

	rctx, cancel := context.WithCancel(context.Background())
	r := &runState{
		ctx: rctx, cancel: cancel, done: make(chan struct{}),
		outcome: make(chan struct{}), loggedIn: make(chan struct{}),
		tokens: make(chan struct{}, 1), imp: imp,
	}
	disp := tg.NewUpdateDispatcher()
	b.registerHandlers(&disp)
	disp.OnLoginToken(func(ctx context.Context, e tg.Entities, u *tg.UpdateLoginToken) error {
		select {
		case r.tokens <- struct{}{}:
		default:
		}
		return nil
	})
	mgr := updates.New(updates.Config{
		Handler: disp,
		Storage: b.states,
		OnChannelTooLong: func(channelID int64) {
			b.log.Debug("Telegram: channel gap too long", "channel", channelID)
		},
		OnLoadUserStateFailed: func() { go b.catchUp() },
	})
	r.client = telegram.NewClient(b.apiID, b.apiHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  mgr,
	})
	b.run = r
	go b.runClient(r, mgr)
	return r, nil
}

// catchUp refetches recent history after the update state was lost.
func (b *Backend) catchUp() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b.FetchRecentHistory(ctx, 20)
}

func (b *Backend) runClient(r *runState, mgr *updates.Manager) {
	defer close(r.done)
	defer func() {
		b.connected.Store(false)
		b.apiMu.Lock()
		b.api = nil
		b.apiMu.Unlock()
		r.signal(errors.New("telegram: client stopped"))
	}()
	err := r.client.Run(r.ctx, func(ctx context.Context) error {
		api := r.client.API()
		b.apiMu.Lock()
		b.api = api
		b.apiMu.Unlock()

		st, err := r.client.Auth().Status(ctx)
		if err != nil {
			r.signal(err)
			return err
		}
		if !st.Authorized {
			b.log.Info("Telegram: not authorised, waiting for QR pairing")
			b.unauthorised.Store(true)
			r.signal(ErrNotAuthorized)
			select {
			case <-r.loggedIn:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		b.unauthorised.Store(false)
		self, err := r.client.Self(ctx)
		if err != nil {
			r.signal(err)
			return err
		}
		b.apiMu.Lock()
		b.self = self
		b.apiMu.Unlock()
		b.states.seed(self.ID, r.imp)

		b.connected.Store(true)
		if err := b.configureReactionNotify(ctx, api); err != nil {
			b.log.Debug("Telegram reactions notify configuration failed", "err", err)
		}
		b.loadContacts(ctx, api)
		b.cmu.RLock()
		n := len(b.contacts)
		b.cmu.RUnlock()
		b.log.Info("Telegram: connected", "contacts", n)
		b.peers.flush()
		r.signal(nil)
		return mgr.Run(ctx, api, self.ID, updates.AuthOptions{})
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		b.log.Warn("Telegram client stopped", "err", err)
	}
	b.peers.flush()
}

// configureReactionNotify is _configure_reaction_notify.
func (b *Backend) configureReactionNotify(ctx context.Context, api *tg.Client) error {
	switch strings.ToLower(strings.TrimSpace(envOr(reactionNotifyEnvVar, "1"))) {
	case "1", "true", "yes", "on":
	default:
		return nil
	}
	s, err := api.AccountGetReactionsNotifySettings(ctx)
	if err != nil {
		return err
	}
	if cur, ok := s.GetMessagesNotifyFrom(); ok {
		if _, all := cur.(*tg.ReactionNotificationsFromAll); all {
			return nil
		}
	}
	upd := *s
	upd.SetMessagesNotifyFrom(&tg.ReactionNotificationsFromAll{})
	_, err = api.AccountSetReactionsNotifySettings(ctx, upd)
	return err
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// Disconnect implements proto.Backend.
func (b *Backend) Disconnect(ctx context.Context) error {
	b.lifeMu.Lock()
	r := b.run
	b.run = nil
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	b.lifeMu.Unlock()
	b.connected.Store(false)
	b.login.mu.Lock()
	if b.login.qrCancel != nil {
		b.login.qrCancel()
		b.login.qrCancel = nil
	}
	b.login.mu.Unlock()
	if r != nil {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
	}
	b.peers.flush()
	return nil
}

// ---- pairing / login -------------------------------------------------------------------

func (b *Backend) sessionExists() bool {
	if _, err := os.Stat(gotdSessionPath(b.sessionPath)); err == nil {
		return true
	}
	_, err := os.Stat(b.sessionPath)
	return err == nil
}

// NeedsPairing implements proto.Pairer: configured, not connected, and either
// no session at all or a session the server rejected. No network I/O.
func (b *Backend) NeedsPairing() bool {
	if b.apiID == 0 || b.apiHash == "" {
		return false
	}
	if b.connected.Load() {
		return false
	}
	return b.unauthorised.Load() || !b.sessionExists()
}

// loginRun returns the running client once it is up and (still) unauthorised;
// ("", nil run) with authorised=true when already logged in.
func (b *Backend) loginRun(ctx context.Context) (r *runState, authorised bool, err error) {
	r, err = b.ensureRun(ctx)
	if err != nil {
		return nil, false, err
	}
	select {
	case <-r.outcome:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	if r.outcomeErr == nil {
		return r, true, nil
	}
	if errors.Is(r.outcomeErr, ErrNotAuthorized) {
		return r, false, nil
	}
	return nil, false, r.outcomeErr
}

// PairingQR implements proto.Pairer (get_pairing_qr): the tg://login?token=
// URL, "INFO: Already logged in", or "ERROR: <reason>". A background waiter
// completes the login when the QR is scanned.
func (b *Backend) PairingQR(ctx context.Context) (string, error) {
	if b.apiID == 0 || b.apiHash == "" {
		return "", nil
	}
	r, authorised, err := b.loginRun(ctx)
	if err != nil {
		return "ERROR: " + err.Error(), nil
	}
	if authorised {
		return "INFO: Already logged in", nil
	}
	api := r.client.API()
	qr := qrlogin.NewQR(api, b.apiID, b.apiHash, qrlogin.Options{})
	tok, err := qr.Export(ctx)
	if err != nil {
		b.log.Error("Telegram QR login start failed", "err", err)
		return "ERROR: " + err.Error(), nil
	}
	if tok.Empty() {
		// Already accepted: import right away.
		go b.waitQR(r, qr, true)
		return "INFO: Already logged in", nil
	}
	b.login.mu.Lock()
	if b.login.qrCancel != nil {
		b.login.qrCancel() // a previous attempt is superseded
	}
	wctx, cancel := context.WithCancel(r.ctx)
	b.login.qrCancel = cancel
	b.login.mu.Unlock()
	go b.waitQRCtx(wctx, r, qr, false)
	return tok.URL(), nil
}

func (b *Backend) waitQR(r *runState, qr qrlogin.QR, immediate bool) {
	b.waitQRCtx(r.ctx, r, qr, immediate)
}

// waitQRCtx waits for the scan (UpdateLoginToken), then imports the token.
func (b *Backend) waitQRCtx(ctx context.Context, r *runState, qr qrlogin.QR, immediate bool) {
	if !immediate {
		select {
		case <-r.tokens:
		case <-time.After(qrWaitTimeout):
			b.log.Info("Telegram QR login: timeout (120s), will refresh")
			return
		case <-ctx.Done():
			return
		}
	}
	_, err := qr.Import(ctx)
	switch {
	case err == nil:
		b.log.Info("Telegram QR login: scan completed successfully")
		r.markLoggedIn()
	case tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
		b.log.Info("Telegram QR login: 2FA required, waiting for password")
		b.needs2FA.Store(true)
	default:
		b.log.Error("Telegram QR login wait failed", "err", err)
	}
}

// Needs2FA implements linking.TelegramLogin.
func (b *Backend) Needs2FA() bool { return b.needs2FA.Load() }

// Complete2FA implements proto.TwoFactor (complete_2fa): false on failure. A
// wrong password keeps the 2FA step open so it can be retried.
func (b *Backend) Complete2FA(ctx context.Context, password string) (bool, error) {
	b.lifeMu.Lock()
	r := b.run
	b.lifeMu.Unlock()
	if !b.needs2FA.Load() || r == nil {
		return false, nil
	}
	if _, err := r.client.Auth().Password(ctx, password); err != nil {
		b.log.Error("Telegram 2FA sign_in failed", "err", err)
		if !errors.Is(err, auth.ErrPasswordInvalid) && !tgerr.Is(err, "PASSWORD_HASH_INVALID") {
			b.needs2FA.Store(false)
		}
		return false, nil
	}
	b.log.Info("Telegram 2FA: sign_in succeeded")
	b.needs2FA.Store(false)
	r.markLoggedIn()
	return true, nil
}

// SendCode starts the phone login (linking.TelegramLogin): the code is sent
// to the phone/app.
func (b *Backend) SendCode(ctx context.Context, phone string) error {
	r, authorised, err := b.loginRun(ctx)
	if err != nil {
		return err
	}
	if authorised {
		return nil
	}
	sent, err := r.client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	sc, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return fmt.Errorf("telegram: unexpected send code result %T", sent)
	}
	b.login.mu.Lock()
	b.login.phone, b.login.codeHash = phone, sc.PhoneCodeHash
	b.login.mu.Unlock()
	return nil
}

// SignInCode completes the phone login with the received code. needs2FA is
// true when the account also needs its password (then call Complete2FA).
func (b *Backend) SignInCode(ctx context.Context, code string) (needs2FA bool, err error) {
	b.lifeMu.Lock()
	r := b.run
	b.lifeMu.Unlock()
	b.login.mu.Lock()
	phone, hash := b.login.phone, b.login.codeHash
	b.login.mu.Unlock()
	if r == nil || phone == "" || hash == "" {
		return false, errors.New("telegram: no login in progress")
	}
	_, err = r.client.Auth().SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		b.needs2FA.Store(true)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	r.markLoggedIn()
	return false, nil
}

var _ proto.Backend = (*Backend)(nil)
