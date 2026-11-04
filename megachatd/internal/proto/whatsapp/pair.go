package whatsapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/linking"
	"go.mau.fi/whatsmeow"
)

// qrWait is how long PairingQR waits for the first QR code of a new pairing.
const qrWait = 15 * time.Second

// ErrAlreadyLinked is returned by the pairing calls when the device is paired.
var ErrAlreadyLinked = errors.New("WhatsApp is already linked")

// PairingQR implements proto.Pairer (get_pairing_qr): the current QR payload
// (the text WhatsApp's "link a device" screen scans), "" when there is no QR.
// The first call starts a pairing session through whatsmeow's QR channel;
// codes rotate by themselves (the first lives ~60 s, the next ones ~20 s).
func (b *Backend) PairingQR(ctx context.Context) (string, error) {
	if err := b.startPairing(ctx); err != nil {
		if errors.Is(err, ErrAlreadyLinked) {
			return "", nil
		}
		return "", err
	}
	return b.waitQR(ctx), nil
}

// waitQR returns the current code, waiting briefly for the first one.
func (b *Backend) waitQR(ctx context.Context) string {
	deadline := time.Now().Add(qrWait)
	for {
		b.pairMu.Lock()
		code, active := b.pairQR, b.pairing
		b.pairMu.Unlock()
		if code != "" || !active || time.Now().After(deadline) {
			return code
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// startPairing opens the pairing session (idempotent while one is active).
func (b *Backend) startPairing(ctx context.Context) error {
	b.pairMu.Lock()
	if b.pairing {
		b.pairMu.Unlock()
		return nil
	}
	b.pairMu.Unlock()

	b.lifeMu.Lock()
	var cli waClient
	var err error
	if b.loggedOut.Load() {
		cli, err = b.rebuildClientLocked(ctx)
	} else {
		cli, err = b.ensureClientLocked(ctx)
	}
	b.lifeMu.Unlock()
	if err != nil {
		return err
	}
	if paired(cli) {
		return ErrAlreadyLinked
	}
	b.stopped.Store(false)
	pctx, cancel := context.WithCancel(context.Background())
	ch, err := cli.GetQRChannel(pctx)
	if err != nil {
		cancel()
		if errors.Is(err, whatsmeow.ErrQRStoreContainsID) {
			return ErrAlreadyLinked
		}
		return err
	}
	b.pairMu.Lock()
	b.pairing, b.pairQR = true, ""
	done := make(chan struct{})
	b.pairDone = done
	b.pairMu.Unlock()
	if !cli.IsConnected() {
		if err := cli.Connect(); err != nil {
			cancel()
			b.pairMu.Lock()
			b.pairing = false
			b.pairMu.Unlock()
			return err
		}
	}
	go b.consumeQR(pctx, cancel, cli, ch, done)
	return nil
}

// consumeQR tracks the QR channel until it closes.
func (b *Backend) consumeQR(ctx context.Context, cancel context.CancelFunc, cli waClient, ch <-chan whatsmeow.QRChannelItem, done chan struct{}) {
	defer func() {
		cancel()
		b.pairMu.Lock()
		b.pairing = false
		b.pairQR = ""
		b.pairMu.Unlock()
		close(done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-ch:
			if !ok {
				return
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				b.pairMu.Lock()
				b.pairQR, b.pairAt = item.Code, nowFunc()
				b.pairMu.Unlock()
			case whatsmeow.QRChannelSuccess.Event:
				b.log.Info("WhatsApp QR pairing succeeded")
				b.loggedOut.Store(false)
				go b.finishPairing(cli)
				return
			case whatsmeow.QRChannelEventError:
				b.log.Warn("WhatsApp QR pairing error", "err", item.Error)
				return
			default: // timeout / unexpected state / client outdated / scanned without multidevice
				b.log.Info("WhatsApp QR pairing ended", "event", item.Event)
				return
			}
		}
	}
}

// finishPairing waits for the post-pairing reconnect and loads the contacts
// (the core's Reconnect repeats this idempotently).
func (b *Backend) finishPairing(cli waClient) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if b.waitReady(ctx, cli, connectTimeout) {
		b.connected.Store(true)
		b.started.Store(true)
		b.loadProtocolCache(ctx)
		b.warm.Store(true)
		b.afterConnect(ctx, cli, true)
	}
}

// cancelPairing aborts an active pairing session.
func (b *Backend) cancelPairing() {
	b.pairMu.Lock()
	done := b.pairDone
	active := b.pairing
	b.pairMu.Unlock()
	if active {
		if cli := b.currentClient(); cli != nil {
			cli.Disconnect()
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// ---- linking.WhatsAppLinker ----------------------------------------------------------

// LinkerBackend wraps the backend so that it satisfies linking.WhatsAppLinker
// (the QR flow of the old WAHA REST wrapper). PairingQR has a different
// signature there than in proto.Pairer, so the two views cannot live on one
// type: register *Backend with the proto.Manager and hand a LinkerBackend to
// linking.Deps.Backends (Backend.AsLinker()).
type LinkerBackend struct{ *Backend }

// AsLinker returns the linking view of the backend.
func (b *Backend) AsLinker() *LinkerBackend { return &LinkerBackend{b} }

var _ linking.WhatsAppLinker = (*LinkerBackend)(nil)

// SessionStatus returns the session state ("working", "scan_qr_code",
// "failed", "stopped", "starting") and whether it has an identity.
func (l *LinkerBackend) SessionStatus(ctx context.Context) (string, bool, error) {
	if l.currentClient() == nil {
		if _, err := l.ensureClient(ctx); err != nil {
			return "", false, err
		}
	}
	st, me, ok := l.SessionInfo()
	if !ok {
		return "", false, errors.New("whatsapp: session state unavailable")
	}
	st = strings.ToLower(st)
	// An active pairing session is a "scan_qr_code" session.
	l.pairMu.Lock()
	pairing := l.pairing
	l.pairMu.Unlock()
	if pairing && !me {
		st = "scan_qr_code"
	}
	return st, me, nil
}

// PairingQR is get_pairing_qr: the current QR as text.
func (l *LinkerBackend) PairingQR(ctx context.Context) (linking.QR, error) {
	code, err := l.Backend.PairingQR(ctx)
	if err != nil {
		return linking.QR{}, err
	}
	return linking.QR{Text: code, OK: code != ""}, nil
}

// FreshPairingQR is get_fresh_pairing_qr: a brand-new QR. With reset a dead
// session is restarted first: a paired-but-failed session is reconnected (the
// pairing is never dropped), an unpaired or logged-out one gets a new pairing
// session.
func (l *LinkerBackend) FreshPairingQR(ctx context.Context, reset bool) (linking.QR, error) {
	if cli := l.currentClient(); cli != nil && paired(cli) && !l.loggedOut.Load() {
		if reset {
			l.Backend.restart(ctx)
		}
		return linking.QR{}, nil // linked: no QR to show
	}
	// A running pairing session rotates its codes by itself; a finished one
	// (timeout) is reopened.
	if reset {
		l.cancelPairing()
	}
	code, err := l.Backend.PairingQR(ctx)
	if err != nil {
		return linking.QR{}, err
	}
	return linking.QR{Text: code, OK: code != ""}, nil
}
