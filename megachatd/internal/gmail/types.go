package gmail

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// Limits (server/gmail.py module constants).
const (
	maxTextLength           = 64 * 1024
	maxAttachments          = 10
	maxAttachmentBytes      = 25 << 20
	maxTotalAttachmentBytes = 25 << 20
	maxDownloadBytes        = 50 * 1024 * 1024
	defaultLimit            = 50
	maxLimit                = 200
	maxBulkDelete           = 200
	listWindow              = 200
	listCacheTTL            = 15 * time.Second
	inboxCacheMaxEntries    = 16
	poolMaxConns            = 4
	poolAcquireTimeout      = 2 * time.Second
	poolMaxIdle             = 600 * time.Second
	poolNoopIdle            = 30 * time.Second
)

// Image proxy limits (ops/gmail.py).
const (
	imageProxyMaxBytes  = 8 * 1024 * 1024
	imageProxyMaxPixels = 25_000_000
)

// Config is the slice of *config.Config this package reads (every Gmail
// setting is read at call time, as the Python helpers do).
type Config interface {
	GmailEnabled() bool
	GmailAddress() string
	GmailAppPassword() string
	GmailIMAPHost() string
	GmailIMAPPort() int
	GmailSMTPHost() string
	GmailSMTPPort() int
	GmailFolder() string
	GmailTimeoutS() int
	GmailHTMLStyles() bool
	GmailHTMLInlineCSS() bool
	GmailImagePrefetch() bool
	GmailUnreadPollS() float64
	CacheDir() string
	WebToken() string
}

// Bus is what the unread watcher publishes gmail_unread events on.
// *bus.Bus implements it.
type Bus interface {
	Publish(t bus.EventType, payload map[string]any) bus.Event
}

// ImageFetcher is media.LinkPreviewer.FetchExternalImage: the SSRF-safe
// raster image fetcher. A nil body means rejected.
type ImageFetcher interface {
	FetchExternalImage(ctx context.Context, rawURL string, maxBytes int64, maxPixels int64) ([]byte, string)
}

// Deps are the collaborators of RegisterOps.
type Deps struct {
	Config Config
	Bus    Bus          // optional: gmail_unread events
	Images ImageFetcher // optional: without it remote images are unavailable
	Logger *slog.Logger // optional
	// TLS is the TLS configuration for IMAPS and SMTP STARTTLS; nil uses the
	// defaults (system roots, ServerName = host). Tests inject a trust-all one.
	TLS *tls.Config
	// Service lets a caller share one Service (for example the core's unread
	// watcher) instead of having RegisterOps build its own. Optional.
	Service *Service
}

// Errors (server/gmail.py exception classes).
type notConfiguredError struct{}

func (notConfiguredError) Error() string { return "Gmail non configurato" }

// validationError is invalid client input; Status 413 maps to too_large.
type validationError struct {
	Msg    string
	Status int
}

func (e *validationError) Error() string { return e.Msg }

func validation(msg string) error { return &validationError{Msg: msg, Status: 400} }
func tooLarge(msg string) error   { return &validationError{Msg: msg, Status: 413} }

type notFoundError struct{ Msg string }

func (e *notFoundError) Error() string { return e.Msg }

func notFound(msg string) error { return &notFoundError{Msg: msg} }

type upstreamError struct{ Msg string }

func (e *upstreamError) Error() string { return e.Msg }

func upstream(msg string) error { return &upstreamError{Msg: msg} }

// ErrNotConfigured is returned (wrapped) when address / password are missing.
var ErrNotConfigured = errors.New("Gmail non configurato")

// settings is the per-call snapshot (GmailSettings).
type settings struct {
	Address  string
	Password string
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	Folder   string
	Timeout  time.Duration
}

// AttachmentDownload is fetch_attachment's result.
type AttachmentDownload struct {
	Filename    string
	ContentType string
	Data        []byte
}

// UnreadStatus is fetch_unread_status's result; UIDNext nil = unknown.
type UnreadStatus struct {
	Unread  int
	UIDNext *int64
}
