package core

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// ---- optional backend capabilities known to the core ----------------------
//
// The proto package only declares the remote-facing contract. These small
// interfaces cover the Python backend methods the core also calls
// (ingest_message, process_receipt, register_contact, ...). A backend may
// implement any of them; the core has a store-based fallback for the first
// two and a core-local registry for the third.

// IngestResult is the outcome of ingesting one message (shared with backends).
type IngestResult = proto.IngestResult

const (
	IngestUnchanged = proto.IngestUnchanged
	IngestAdded     = proto.IngestAdded
	IngestChanged   = proto.IngestChanged
)

// MessageIngester stores a backend message (ingest_message).
type MessageIngester interface {
	IngestMessage(ctx context.Context, contactID string, msg map[string]any, ts int64) (IngestResult, error)
}

// ReceiptProcessor applies a receipt event and returns the updated rows
// (process_receipt). payload is the raw event payload.
type ReceiptProcessor interface {
	ProcessReceipt(ctx context.Context, contactID string, payload map[string]any) ([]map[string]any, error)
}

// ContactRegistrar makes a contact known to the backend (register_contact).
type ContactRegistrar interface {
	RegisterContact(c proto.Contact) bool
}

// UnreadReconciler realigns the unread badges with the phone (WhatsApp).
type UnreadReconciler interface {
	ReconcileUnread(ctx context.Context)
}

// LIDResolverStarter starts the opportunistic WhatsApp LID resolver.
type LIDResolverStarter interface {
	StartLIDResolver()
}

// NumberChecker asks the remote whether a phone number exists on WhatsApp.
type NumberChecker interface {
	CheckNumberExists(ctx context.Context, phone string) (bool, error)
}

// PlaceholderNamer gives a cached display name for an unknown contact id
// (resolve_placeholder_name); "" = unknown.
type PlaceholderNamer interface {
	PlaceholderName(contactID string) string
}

// ReadyWaiter is the WhatsApp boot wait: it blocks up to timeout until the
// session is connected and logged in (Python polled the WAHA session for
// WORKING) and runs the post-ready work (LID warm-up) the first time.
type ReadyWaiter interface {
	WaitReady(ctx context.Context, timeout time.Duration) bool
}

// ContactsReloader re-reads the backend's contacts (WhatsApp _load_contacts).
type ContactsReloader interface {
	ReloadContacts(ctx context.Context)
}

// RecentHistoryFetcher is Telegram's startup history sync
// (fetch_recent_history): it returns the number of messages ingested.
type RecentHistoryFetcher interface {
	FetchRecentHistory(ctx context.Context, limit int) int
}

// Startup timings of the connect workers (server/core.py).
const (
	// WABootReadyTimeout / WABootPollInterval are WA_BOOT_READY_TIMEOUT_S and
	// WA_BOOT_POLL_INTERVAL_S.
	WABootReadyTimeout = 300 * time.Second
	WABootPollInterval = 2500 * time.Millisecond
	// WAContactsTimeout / WAContactsPollInterval bound _poll_wa_contacts.
	WAContactsTimeout      = 120 * time.Second
	WAContactsPollInterval = 2 * time.Second
	// telegramStartupHistory is the per-chat limit of the startup history sync.
	telegramStartupHistory = 20
)

// UserNumber is the Signal account number (reaction target author).
type UserNumber interface {
	UserNumber() string
}

// ---- Core ---------------------------------------------------------------------

// Core owns the bus, store, backend manager and config.
type Core struct {
	cfg *config.Config
	st  *store.Store
	mgr *proto.Manager
	bus *bus.Bus
	log *slog.Logger

	sendReg *SendRegistry
	logbuf  *LogBuffer

	validator UploadValidator
	sleep     func(ctx context.Context, d time.Duration) error
	rng       *rand.Rand
	nowMs     func() int64

	mu         sync.Mutex
	registered map[string]proto.Contact // cache_key -> contact (ghosts/book-only)
	recency    map[string]int64         // cache_key -> last_message_ts

	runMu   sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	stopped bool

	ab      *addressBookRefresher
	gmail   *GmailUnreadWatcher
	gmailFn GmailFetch

	retention       *int
	scopes          []MediaScope
	skipMaintenance bool
	autoConnect     func(protocol string) bool

	bootTimeout, bootPoll         time.Duration
	contactsTimeout, contactsPoll time.Duration
}

// Option configures New.
type Option func(*Core)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(c *Core) { c.log = l } }

// WithLogBuffer sets the buffer behind server.logs / server.logs_clear.
func WithLogBuffer(b *LogBuffer) Option { return func(c *Core) { c.logbuf = b } }

// WithUploadValidator replaces the attachment validator used by messages.send.
func WithUploadValidator(v UploadValidator) Option { return func(c *Core) { c.validator = v } }

// WithSleep replaces the retry back-off sleep (tests).
func WithSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(c *Core) { c.sleep = f }
}

// WithRand sets the jitter source (tests).
func WithRand(r *rand.Rand) Option { return func(c *Core) { c.rng = r } }

// WithSendRegistry replaces the idempotency registry (tests).
func WithSendRegistry(r *SendRegistry) Option { return func(c *Core) { c.sendReg = r } }

// WithGmailFetch enables the Gmail unread watcher with the given fetcher.
func WithGmailFetch(f GmailFetch) Option { return func(c *Core) { c.gmailFn = f } }

// WithRetention overrides the per-contact retention limit read from config.
func WithRetention(limit int) Option { return func(c *Core) { c.retention = &limit } }

// WithMediaScopes overrides the media prune scopes (tests).
func WithMediaScopes(s []MediaScope) Option { return func(c *Core) { c.scopes = s } }

// WithAutoConnect gates the connect worker of each backend: when f returns
// false the backend is left alone and only backend_done is published (Python:
// Signal is registered always but connected only when signal_enabled()).
func WithAutoConnect(f func(protocol string) bool) Option { return func(c *Core) { c.autoConnect = f } }

// WithoutStartupMaintenance skips store.RunStartupMaintenance in Start.
func WithoutStartupMaintenance() Option { return func(c *Core) { c.skipMaintenance = true } }

// WithStartupTimings overrides the WhatsApp boot wait (timeout, poll) and the
// contact poll (timeout, poll); tests use short values.
func WithStartupTimings(bootTimeout, bootPoll, contactsTimeout, contactsPoll time.Duration) Option {
	return func(c *Core) {
		c.bootTimeout, c.bootPoll, c.contactsTimeout, c.contactsPoll = bootTimeout, bootPoll, contactsTimeout, contactsPoll
	}
}

// New builds a Core. Nothing runs until Start.
func New(cfg *config.Config, st *store.Store, mgr *proto.Manager, b *bus.Bus, opts ...Option) *Core {
	c := &Core{
		cfg: cfg, st: st, mgr: mgr, bus: b,
		log:        slog.Default(),
		sendReg:    NewSendRegistry(),
		logbuf:     NewLogBuffer(),
		validator:  DefaultUploadValidator,
		registered: map[string]proto.Contact{},
		recency:    map[string]int64{},
		nowMs:      func() int64 { return time.Now().UnixMilli() },

		bootTimeout: WABootReadyTimeout, bootPoll: WABootPollInterval,
		contactsTimeout: WAContactsTimeout, contactsPoll: WAContactsPollInterval,
	}
	c.sleep = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, o := range opts {
		o(c)
	}
	c.ab = newAddressBookRefresher(c)
	if c.gmailFn != nil {
		c.gmail = NewGmailUnreadWatcher(c.PublishWebPush, c.gmailFn, cfg.GmailUnreadPollS(), cfg.GmailEnabled)
	}
	return c
}

// Bus, Store, Manager, Config expose the owned components.
func (c *Core) Bus() *bus.Bus           { return c.bus }
func (c *Core) Store() *store.Store     { return c.st }
func (c *Core) Manager() *proto.Manager { return c.mgr }
func (c *Core) Config() *config.Config  { return c.cfg }

// SendRegistry returns the text-send idempotency registry.
func (c *Core) SendRegistry() *SendRegistry { return c.sendReg }

// LogBuffer returns the in-memory log ring.
func (c *Core) LogBuffer() *LogBuffer { return c.logbuf }

// Gmail returns the unread watcher (nil without WithGmailFetch). Gmail ops
// call Poke on it after changing the unread count.
func (c *Core) Gmail() *GmailUnreadWatcher { return c.gmail }

// ---- publishing ----------------------------------------------------------------

// Publish puts an event on the bus.
func (c *Core) Publish(t bus.EventType, payload map[string]any) bus.Event {
	return c.bus.Publish(t, payload)
}

// PublishWebPush publishes a legacy /ws frame {"type","payload"} as a
// web_push bus event (ChatAPI.publish_web_push).
func (c *Core) PublishWebPush(frameType string, payload any) {
	c.bus.Publish(bus.WebPush, map[string]any{"event": map[string]any{
		"type": frameType, "payload": jsonable(payload),
	}})
}

func (c *Core) status(proto, text string, persistent bool) {
	c.bus.Publish(bus.BackendStatus, toMap(api.BackendStatusPayload{Protocol: proto, Text: text, Persistent: persistent}))
}

func (c *Core) done(proto string) {
	c.bus.Publish(bus.BackendDone, toMap(api.BackendDonePayload{Protocol: proto, OK: false}))
}

// ---- lifecycle -------------------------------------------------------------------

// Start runs the startup DB maintenance, the event pump, the backend connect
// workers, the address-book refresher and the Gmail watcher. It returns once
// they are launched; the connect workers run in the background.
func (c *Core) Start(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if c.started {
		return nil
	}
	c.started = true
	if !c.skipMaintenance {
		if err := c.st.RunStartupMaintenance(ctx, false); err != nil {
			c.log.Debug("startup DB maintenance failed", "err", err)
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	c.goroutine(func() { c.pump(runCtx) })
	for _, b := range c.mgr.All() {
		p := b.Protocol()
		c.goroutine(func() { c.connect(runCtx, p) })
	}
	c.ab.start(runCtx)
	if c.gmail != nil {
		c.gmail.Start(runCtx)
	}
	return nil
}

func (c *Core) goroutine(f func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		f()
	}()
}

// Stop stops the workers, disconnects the backends and prunes the cache
// (retention + media). It runs once; later calls are no-ops.
func (c *Core) Stop(ctx context.Context) error {
	c.runMu.Lock()
	if c.stopped {
		c.runMu.Unlock()
		return nil
	}
	c.stopped = true
	cancel := c.cancel
	c.runMu.Unlock()

	if c.gmail != nil {
		c.gmail.Stop()
	}
	if cancel != nil {
		cancel()
	}
	c.ab.stop()
	c.wg.Wait()
	err := c.mgr.Stop(ctx)
	// Prune once no other event can arrive (bug #51).
	if _, perr := c.Prune(ctx); perr != nil {
		c.log.Error("cache prune on exit failed", "err", perr)
	}
	return err
}

// Reconnect (re)connects the given protocols' backends (called after a
// device-link flow, ChatCore.reconnect).
func (c *Core) Reconnect(ctx context.Context, protocols ...string) {
	for _, p := range protocols {
		if _, ok := c.mgr.Get(p); !ok {
			continue
		}
		c.goroutine(func() { c.connect(c.runContext(ctx), p) })
	}
}

func (c *Core) runContext(fallback context.Context) context.Context {
	// Connect workers outlive the caller's request context.
	return context.WithoutCancel(fallback)
}

// connect is the connect worker of one backend: it publishes the progress
// events and the backend_ready / backend_done outcome.
func (c *Core) connect(ctx context.Context, protocol string) {
	b, ok := c.mgr.Get(protocol)
	if !ok {
		return
	}
	if pr, ok := b.(proto.Pairer); ok && pr.NeedsPairing() {
		c.log.Info("pairing required, skipping auto-connect", "protocol", protocol)
		c.done(protocol)
		return
	}
	if c.autoConnect != nil && !c.autoConnect(protocol) {
		// ChatCore.start: Signal auto-connect is skipped until an account is configured.
		c.log.Info("auto-connect disabled", "protocol", protocol)
		c.done(protocol)
		return
	}
	c.bus.Publish(bus.BackendConnecting, toMap(api.BackendConnectingPayload{Protocol: protocol}))
	label := protocolLabel(protocol)
	c.status(protocol, "⏳ "+label+": "+connectingText(protocol), true)
	if err := c.mgr.StartBackend(ctx, protocol); err != nil {
		c.log.Warn("backend connect failed", "protocol", protocol, "err", err)
		c.status(protocol, failText(protocol, err), true)
		c.done(protocol)
		return
	}
	if protocol == api.ProtocolWhatsApp && !c.waitWABoot(ctx, b, protocol) {
		return
	}
	contacts, _ := b.ListContacts(ctx)
	switch protocol {
	case api.ProtocolSignal:
		c.ready(ctx, b, protocol)
		c.status(protocol, "💡 Select a contact to view chat", false)
		c.status(protocol, "✅ Signal: daemon attivo", false)
	case api.ProtocolWhatsApp:
		if len(contacts) == 0 {
			// Server-side contact sync still in progress (_poll_wa_contacts).
			if !c.pollWAContacts(ctx, b, protocol) {
				return
			}
			c.ready(ctx, b, protocol)
			c.status(protocol, "⏳ WhatsApp: sync cronologia...", true)
			c.resyncWAHistory(ctx, b, protocol, false)
			c.status(protocol, "", false)
			return
		}
		c.status(protocol, fmt.Sprintf("✅ WhatsApp: %d contatti", len(contacts)), false)
		c.ready(ctx, b, protocol)
		c.resyncWAHistory(ctx, b, protocol, true)
	case api.ProtocolTelegram:
		if hf, ok := b.(RecentHistoryFetcher); ok && len(contacts) > 0 {
			c.status(protocol, "⏳ Telegram: sync cronologia...", true)
			n := hf.FetchRecentHistory(ctx, telegramStartupHistory)
			c.log.Info("Telegram startup history synced", "messages", n)
		}
		c.ready(ctx, b, protocol)
	default:
		c.ready(ctx, b, protocol)
	}
}

// ready is ChatCore._ready: recover the recency, then publish backend_ready.
func (c *Core) ready(ctx context.Context, b proto.Backend, protocol string) {
	c.syncRecency(ctx, protocol)
	contacts, _ := b.ListContacts(ctx)
	c.bus.Publish(bus.BackendReady, toMap(api.BackendReadyPayload{Protocol: protocol, Contacts: len(contacts)}))
}

// resyncWAHistory is _resync_wa_history: best effort, reports the chat count
// when report is set (the contact-poll path reports through its own status).
func (c *Core) resyncWAHistory(ctx context.Context, b proto.Backend, protocol string, report bool) {
	hr, ok := b.(proto.HistoryResyncer)
	if !ok {
		return
	}
	if n, err := hr.ResyncHistory(ctx, 0); err == nil && n > 0 && report {
		c.status(protocol, fmt.Sprintf("✅ WhatsApp: cronologia sincronizzata per %d chat", n), false)
	}
}

// waitWABoot is connect_whatsapp_boot's bounded wait: poll until the session
// is connected and logged in (WAHA WORKING), stopping early when it asks for
// pairing. It reports false when the connect must be abandoned (backend_done
// already published, or the run was cancelled).
func (c *Core) waitWABoot(ctx context.Context, b proto.Backend, protocol string) bool {
	w, ok := b.(ReadyWaiter)
	if !ok {
		return true
	}
	deadline := time.Now().Add(c.bootTimeout)
	for time.Now().Before(deadline) {
		if pr, ok := b.(proto.Pairer); ok && pr.NeedsPairing() {
			c.log.Info("pairing required, skip auto-connect", "protocol", protocol)
			c.done(protocol)
			return false
		}
		if w.WaitReady(ctx, c.bootPoll) {
			c.log.Info("WhatsApp session ready, continuing connect")
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
	c.log.Warn("WhatsApp session not ready after boot wait, auto-connect cancelled", "timeout", c.bootTimeout)
	c.done(protocol)
	return false
}

// pollWAContacts is _poll_wa_contacts: wait (bounded) for the contacts to
// show up. It reports false on timeout (backend_done published) or cancel.
func (c *Core) pollWAContacts(ctx context.Context, b proto.Backend, protocol string) bool {
	c.status(protocol, "🔄 WhatsApp: sync contatti...", true)
	deadline := time.Now().Add(c.contactsTimeout)
	for polls := 1; time.Now().Before(deadline); polls++ {
		if err := c.sleep(ctx, c.contactsPoll); err != nil {
			return false
		}
		if r, ok := b.(ContactsReloader); ok {
			r.ReloadContacts(ctx)
		}
		if cs, _ := b.ListContacts(ctx); len(cs) > 0 {
			c.log.Info("WhatsApp contacts synced", "polls", polls, "contacts", len(cs))
			c.status(protocol, fmt.Sprintf("📥 WhatsApp: %d contatti caricati", len(cs)), false)
			return true
		}
	}
	c.log.Warn("WhatsApp contact sync timeout", "timeout", c.contactsTimeout)
	c.status(protocol, "⚠️ WhatsApp: timeout contatti", true)
	c.done(protocol)
	return false
}

func protocolLabel(p string) string {
	switch p {
	case api.ProtocolSignal:
		return "Signal"
	case api.ProtocolWhatsApp:
		return "WhatsApp"
	case api.ProtocolTelegram:
		return "Telegram"
	}
	return p
}

func connectingText(p string) string {
	switch p {
	case api.ProtocolSignal:
		return "avvio daemon..."
	case api.ProtocolWhatsApp:
		return "connessione..."
	}
	return "connecting..."
}

func failText(p string, err error) string {
	switch p {
	case api.ProtocolSignal:
		return fmt.Sprintf("❌ Signal: errore — %v", err)
	case api.ProtocolWhatsApp:
		return fmt.Sprintf("❌ WhatsApp: non disponibile — %v", err)
	}
	return fmt.Sprintf("❌ %s: %v", protocolLabel(p), err)
}

// pump consumes Manager.Events and publishes the processed events.
func (c *Core) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.mgr.Events():
			c.PumpEvent(ctx, ev)
		}
	}
}

// PumpEvent processes one backend event and publishes the results (exported
// for tests and for hosts that feed events themselves).
func (c *Core) PumpEvent(ctx context.Context, ev proto.Event) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Debug("event processing panicked", "err", r)
		}
	}()
	results, err := c.Process(ctx, ev)
	if err != nil {
		c.log.Debug("event processing failed", "err", err)
		return
	}
	for _, r := range results {
		c.bus.Publish(r.Type, r.Payload)
	}
}

// ---- maintenance --------------------------------------------------------------------

// Prune applies the retention limit and removes orphan media files. It
// returns the number of message rows deleted.
func (c *Core) Prune(ctx context.Context) (int, error) {
	limit := c.cfg.MessageRetentionPerContact()
	if c.retention != nil {
		limit = *c.retention
	}
	deleted, err := c.st.PruneCache(ctx, limit, 0)
	if err != nil {
		return deleted, err
	}
	scopes := c.scopes
	if scopes == nil {
		scopes = c.DefaultMediaScopes()
	}
	rep := PruneMedia(ctx, c.st, MediaPruneOptions{Scopes: scopes}, c.log)
	c.log.Info("media prune", "orphans", rep.TotalOrphans(), "mb_freed", float64(rep.TotalBytes())/(1024*1024))
	return deleted, nil
}
