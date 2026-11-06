// Package linking is the Go port of server/link.py and server/ops/link.py:
// the device-link service (Signal via a signal-cli subprocess, WhatsApp via
// WAHA, Telegram via QR login) and the link.* operations.
//
// QR payloads are returned raw (kind url/text/png/info/error); rendering
// url/text into a PNG is done only by the HTTP adapter (CONTRACTS.md, quirk 10)
// and needs a QR encoder, which is not part of this package.
package linking

import (
	"context"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Backends looks a protocol backend up. *proto.Manager implements it.
type Backends interface {
	Get(protocol string) (proto.Backend, bool)
}

// Config is the configuration the link flows need. *config.Config implements
// it.
type Config interface {
	// ConfigFile is the path of config.json (signal.user_number is persisted there).
	ConfigFile() string
	// Env returns an environment variable or "".
	Env(name string) string
	// SignalUserNumber is the configured Signal account ("" if none).
	SignalUserNumber() string
	// SignalCLIBinDir is <project>/bin (signal-cli-*/bin/signal-cli).
	SignalCLIBinDir() string
}

// SignalCLI runs signal-cli. The default implementation executes the binary
// found in Config.SignalCLIBinDir().
type SignalCLI interface {
	// ListAccounts runs `signal-cli listAccounts` and returns the E.164 numbers.
	ListAccounts(ctx context.Context) ([]string, error)
	// StartLink runs `signal-cli link -n <deviceName>`. The process must outlive
	// ctx (it runs until the user scanned the QR or the link is cancelled).
	StartLink(deviceName string) (LinkProcess, error)
}

// LinkProcess is a running `signal-cli link`.
type LinkProcess interface {
	// ReadLine returns the next line of combined stdout/stderr; ok is false at EOF.
	ReadLine() (line string, ok bool)
	// Drain consumes the rest of the output in the background.
	Drain()
	// Exited reports whether the process ended, and its exit code.
	Exited() (done bool, code int)
	// Terminate asks the process to stop (SIGTERM). Safe on a dead process.
	Terminate() error
}

// Deps are the collaborators of RegisterOps / New.
type Deps struct {
	Backends Backends
	Config   Config
	// SignalCLI is optional (default: exec).
	SignalCLI SignalCLI
	// Reconnect restarts the given backends without blocking (link.reconnect).
	// nil -> link.reconnect answers not_supported.
	Reconnect func(protocols []string)
	// LinkRequired reports that the WhatsApp session guard needs a new QR
	// (link.required). nil -> false.
	LinkRequired func() bool
}

// WhatsAppLinker is the WAHA REST wrapper of the whatsapp backend (optional
// capability; without it the generic proto.Pairer is used).
type WhatsAppLinker interface {
	// SessionStatus returns WAHA's session state ("working", "scan_qr_code",
	// "failed", ...; "" if unknown) and whether it reports an identity (`me`).
	SessionStatus(ctx context.Context) (status string, hasIdentity bool, err error)
	// PairingQR is the current QR (get_pairing_qr), FreshPairingQR a brand-new
	// one (get_fresh_pairing_qr, optionally resetting the session).
	PairingQR(ctx context.Context) (QR, error)
	FreshPairingQR(ctx context.Context, reset bool) (QR, error)
}

// QR is what WAHA answers: PNG bytes (current versions) or text (older ones).
// OK is false when there is no QR.
type QR struct {
	PNG  []byte
	Text string
	OK   bool
}

// TelegramLogin exposes the Telegram login state that proto.Backend does not
// (optional; IsConnected() is the fallback for "login completed").
type TelegramLogin interface {
	// Needs2FA reports that the account asks for its password.
	Needs2FA() bool
}

// SignalAccount is implemented by the Signal backend (signal.user_number).
type SignalAccount interface {
	UserNumber() string
	SetUserNumber(string)
}
