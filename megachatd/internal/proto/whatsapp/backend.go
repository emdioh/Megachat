package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
)

// nowFunc is the package clock (tests replace it).
var nowFunc = time.Now

// Tunables from whatsapp.py.
const (
	// sendDedupWindowMs is _SEND_DEDUP_WINDOW_MS.
	sendDedupWindowMs = 5000
	// echoMatchWindowMs is protocols.db._ECHO_MATCH_WINDOW_MS (10 min), typed below.
	echoMatchWindowMs int64 = 600_000
	// connectTimeout bounds Connect's wait for the first Connected event
	// (Python waited up to 40 s for the WAHA session to become WORKING).
	connectTimeout = 40 * time.Second
	// mediaPrefetchWorkers bounds the concurrent background media downloads.
	mediaPrefetchWorkers = 2
	resyncStagger        = 500 * time.Millisecond
)

// Errors.
var (
	ErrNotConnected  = errors.New("WhatsApp backend not connected")
	ErrNotPaired     = errors.New("WhatsApp is not linked: scan the QR code first")
	ErrInvalidTarget = errors.New("WhatsApp: invalid contact or message id")
)

// Config is the configuration the backend needs. *config.Config implements it.
type Config interface {
	DataDir() string
	MediaDir() string
	LocationThumbsDir() string
	AddressBookTTLS() int
	WALidCacheTTLDays() int
	WALidMapFile() string
	WAResyncLimit() int
	WAResyncMaxChats() int
	WAResyncWorkers() int
	WAResyncDownloadMedia() bool
	WALazyMediaNegativeTTLS() int
	// Browser identity announced to WhatsApp ("" keeps the backend default).
	WhatsAppBrowser() string
	WhatsAppBrowserVersion() string
	WhatsAppBrowserOS() string
}

// Store is the slice of internal/store the backend persists through.
// *store.Store implements it.
type Store interface {
	LoadCache(ctx context.Context, protocol string) (map[string][]store.Message, error)
	AddMessage(ctx context.Context, m store.NewMessage) (id int64, inserted bool, err error)
	UpdateMessageID(ctx context.Context, protocol, contact, text string, isMine bool, timestamp int64, msgID string) (bool, error)
	UpdateMessageAttachmentID(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID string, expected *string) (bool, error)
	UpdateMessageAttachmentInfo(ctx context.Context, protocol, contact, msgID string, timestamp int64, info string) (bool, error)
	UpdateMessageMediaIdentity(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID, msgType string, mediaKind *string) (bool, error)
	MessageMediaRow(ctx context.Context, protocol, contact, msgID string) (*store.MediaRow, error)
	FillMessageQuoteFields(ctx context.Context, protocol, contact, msgID string, timestamp int64, q store.QuoteFill) (bool, error)
	MarkAsRead(ctx context.Context, protocol, contact string, extraKeys ...string) error
	UpdateMessageStatus(ctx context.Context, timestamp int64, status, protocol, contact string, text *string, expected []string) (bool, error)
	UpdateMessageStatusByID(ctx context.Context, msgID, status, protocol string, contact *string) (bool, error)
	UpdateMessageText(ctx context.Context, u store.TextUpdate) (bool, error)
	ApplyReactionDelta(ctx context.Context, d store.ReactionDelta) (bool, error)
	ReactionsForContact(ctx context.Context, protocol, contact string) ([]store.Reaction, error)
	ResolveReactionTargetRow(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64) (*store.ReactionTarget, error)
}

// BatchSlotMerger is an OPTIONAL store extension for _merge_batch_slot
// (UPDATE messages SET batch_id=?, batch_index=? WHERE protocol=? AND
// contact_number=? AND msg_id=? AND batch_id IS NULL). Without it the slot
// merge is skipped.
type BatchSlotMerger interface {
	MergeBatchSlot(ctx context.Context, protocol, contact, msgID, batchID string, batchIndex *int64) (bool, error)
}

// FailedRowReuser is an OPTIONAL store extension for _reuse_failed_db_row
// (*store.Store implements it). Without it the reuse fallback is skipped.
type FailedRowReuser interface {
	ReuseFailedRow(ctx context.Context, protocol, contact, text string, ts int64, msgID, status string) (*store.ReusedRow, error)
}

// Options configures New.
type Options struct {
	Config Config // required unless every path override below is set
	Store  Store  // optional: without it nothing is persisted
	Logger *slog.Logger

	// Overrides of the Config values (tests).
	DataDir           string
	MediaDir          string
	LocationThumbsDir string
	DBPath            string // whatsmeow device store; default <DataDir>/whatsmeow.db
	LIDMapPath        string
	AddressBookTTL    time.Duration
	LIDCacheTTL       time.Duration
	ResyncLimit       int
	ResyncMaxChats    int
	ResyncWorkers     int
	LazyMediaNegTTL   time.Duration

	// Client injects an already built client (tests): the device store is
	// then never opened and nothing connects to the network.
	Client waClient
}

// Backend is the WhatsApp backend.
type Backend struct {
	cfg Config
	st  Store
	log *slog.Logger

	browser browserIdentity

	dataDir, mediaDir, thumbsDir, dbPath, lidMapPath string
	bookTTL, lidTTL, lazyNegTTL                      time.Duration
	resyncLimit, resyncMaxChats, resyncWorkers       int
	resyncDownloadMedia                              bool

	// Client lifecycle (lifeMu). cli is nil until ensureClient ran.
	lifeMu      sync.Mutex
	cli         waClient
	db          *sql.DB
	container   *sqlstore.Container
	handlerID   uint32
	injected    bool
	closed      chan struct{}
	connectedAt time.Time
	discMu      sync.Mutex
	discAt      time.Time

	connected atomic.Bool // authenticated and the websocket is up
	stopped   atomic.Bool // Disconnect called (guard: "stopped")
	loggedOut atomic.Bool // the phone unlinked this device
	started   atomic.Bool // Connect completed once

	pairMu   sync.Mutex
	pairQR   string    // latest QR payload while pairing
	pairAt   time.Time // when pairQR was issued
	pairing  bool      // a QR channel is active
	pairDone chan struct{}

	queue *eventQueue

	// Contacts (cmu).
	cmu                sync.RWMutex
	contacts           []*proto.Contact
	byJID              map[string]*proto.Contact
	chats              map[string]*chatMeta
	knownGroups        map[string]*proto.Contact
	addressBook        []proto.Contact
	addressBookT       time.Time
	addressBookS       bool
	readOnPhone        map[string]bool
	presenceSubscribed map[string]bool

	// LID map (lidMu).
	lidMu           sync.Mutex
	lidMap          map[string]lidEntry
	lidLoaded       bool
	lidStarted      atomic.Bool
	warm            atomic.Bool // the post-ready work (LID warm-up, presence) ran
	lidCooldown     map[string]time.Time
	lidStoreForTest lidStore // overrides the device LID store (tests)

	contactStoreForTest contactStore // overrides the device contact table (tests)

	// Message cache and ingest state (ingestMu).
	ingestMu sync.Mutex
	cache    map[string][]*entry
	seen     map[seenKey]struct{}

	// Media (mediaMu).
	mediaMu    sync.Mutex
	mediaInfo  mediaInfoStore
	lazyFlight map[string]*lazyCall
	lazyFailed map[string]time.Time
	prefetch   chan struct{}

	unfetchable    map[string]bool
	histWait       map[string]chan struct{}
	histAsked      map[string]time.Time
	historyTimeout time.Duration
	lastUnread     time.Time

	guard *Guard
}

type seenKey struct{ contact, id, text, att string }

// chatMeta is what WAHA's /chats returned for a chat: there is no chat list in
// the WhatsApp protocol, so the backend accumulates it from the message cache,
// the joined groups, history sync and live traffic.
type chatMeta struct {
	Name     string
	LastTS   int64
	Unread   int
	Archived bool
}

// New builds a WhatsApp backend. Nothing connects until Connect.
func New(o Options) *Backend {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	b := &Backend{
		cfg: o.Config, st: o.Store, log: o.Logger,
		browser: resolveBrowserIdentity(o.Config, o.Logger),
		dataDir: o.DataDir, mediaDir: o.MediaDir, thumbsDir: o.LocationThumbsDir, dbPath: o.DBPath,
		lidMapPath: o.LIDMapPath, bookTTL: o.AddressBookTTL, lidTTL: o.LIDCacheTTL,
		resyncLimit: o.ResyncLimit, resyncMaxChats: o.ResyncMaxChats, resyncWorkers: o.ResyncWorkers,
		lazyNegTTL: o.LazyMediaNegTTL,
		closed:     make(chan struct{}), queue: newEventQueue(),
		byJID: map[string]*proto.Contact{}, chats: map[string]*chatMeta{}, knownGroups: map[string]*proto.Contact{},
		readOnPhone: map[string]bool{}, lidCooldown: map[string]time.Time{},
		cache: map[string][]*entry{}, seen: map[seenKey]struct{}{},
		lazyFlight: map[string]*lazyCall{}, lazyFailed: map[string]time.Time{},
		historyTimeout: historyWait,
		prefetch:       make(chan struct{}, mediaPrefetchWorkers),
		unfetchable:    map[string]bool{}, histWait: map[string]chan struct{}{}, histAsked: map[string]time.Time{},
	}
	if c := o.Config; c != nil {
		if b.dataDir == "" {
			b.dataDir = c.DataDir()
		}
		if b.mediaDir == "" {
			b.mediaDir = c.MediaDir()
		}
		if b.thumbsDir == "" {
			b.thumbsDir = c.LocationThumbsDir()
		}
		if b.lidMapPath == "" {
			b.lidMapPath = c.WALidMapFile()
		}
		if b.bookTTL == 0 {
			b.bookTTL = time.Duration(c.AddressBookTTLS()) * time.Second
		}
		if b.lidTTL == 0 {
			b.lidTTL = time.Duration(c.WALidCacheTTLDays()) * 24 * time.Hour
		}
		if b.resyncLimit == 0 {
			b.resyncLimit = c.WAResyncLimit()
		}
		if b.resyncMaxChats == 0 {
			b.resyncMaxChats = c.WAResyncMaxChats()
		}
		if b.resyncWorkers == 0 {
			b.resyncWorkers = c.WAResyncWorkers()
		}
		if b.lazyNegTTL == 0 {
			b.lazyNegTTL = time.Duration(c.WALazyMediaNegativeTTLS()) * time.Second
		}
		b.resyncDownloadMedia = c.WAResyncDownloadMedia()
	}
	if b.bookTTL == 0 {
		b.bookTTL = 300 * time.Second
	}
	if b.lidTTL == 0 {
		b.lidTTL = 30 * 24 * time.Hour
	}
	if b.resyncLimit <= 0 {
		b.resyncLimit = 30
	}
	if b.resyncMaxChats == 0 && o.Config == nil {
		b.resyncMaxChats = 20
	}
	if b.resyncWorkers <= 0 {
		b.resyncWorkers = 2
	}
	if b.lazyNegTTL == 0 {
		b.lazyNegTTL = 30 * time.Second
	}
	if b.dbPath == "" && b.dataDir != "" {
		b.dbPath = filepath.Join(b.dataDir, "whatsmeow.db")
	}
	if b.lidMapPath == "" && b.dataDir != "" {
		b.lidMapPath = filepath.Join(b.dataDir, "wa_lid_map.json")
	}
	if b.mediaDir == "" && b.dataDir != "" {
		b.mediaDir = filepath.Join(b.dataDir, "whatsapp-media")
	}
	if b.thumbsDir == "" && b.dataDir != "" {
		b.thumbsDir = filepath.Join(b.dataDir, "location-thumbs")
	}
	b.mediaInfo = newMediaInfoStore()
	if o.Client != nil {
		b.cli, b.injected = o.Client, true
		b.handlerID = b.cli.AddEventHandler(b.handleEvent)
		b.connected.Store(b.cli.IsConnected() && b.cli.IsLoggedIn())
		b.started.Store(true)
	}
	b.guard = newGuard(b, GuardConfigFrom(o.Config))
	return b
}

// Protocol implements proto.Backend.
func (b *Backend) Protocol() string { return proto.ProtocolWhatsApp }

// IsConnected implements proto.Backend (Python is_connected: connect()
// completed and the session is up).
func (b *Backend) IsConnected() bool { return b.connected.Load() && !b.stopped.Load() }

// emit queues a backend event.
func (b *Backend) emit(typ, contactID string, payload map[string]any) {
	b.queue.put(proto.Event{Type: typ, Protocol: proto.ProtocolWhatsApp, ContactID: contactID, Payload: payload})
}

// enqueueEvent is _enqueue_event.
func (b *Backend) enqueueEvent(ev proto.Event) { b.queue.put(ev) }

// Receive implements proto.Backend.
func (b *Backend) Receive(ctx context.Context) (<-chan proto.Event, error) {
	b.lifeMu.Lock()
	closed := b.closed
	b.lifeMu.Unlock()
	out := make(chan proto.Event)
	go func() {
		defer close(out)
		for {
			ev, ok := b.queue.get(ctx, closed)
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				b.queue.putFront(ev)
				return
			case <-closed:
				b.queue.putFront(ev)
				return
			}
		}
	}()
	return out, nil
}

// PollOnce drains the pending events without blocking (poll_once).
func (b *Backend) PollOnce() []proto.Event { return b.queue.drain() }

// ---- client lifecycle --------------------------------------------------------------

// ensureClient opens the device store and builds the whatsmeow client once.
func (b *Backend) ensureClient(ctx context.Context) (waClient, error) {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	return b.ensureClientLocked(ctx)
}

func (b *Backend) ensureClientLocked(ctx context.Context) (waClient, error) {
	if b.cli != nil {
		return b.cli, nil
	}
	if b.dbPath == "" {
		return nil, errors.New("whatsapp: no device store path configured")
	}
	applyBrowserIdentity(b.browser)
	if b.db == nil {
		db, container, dev, err := openStore(ctx, b.dbPath, b.log)
		if err != nil {
			return nil, err
		}
		b.db, b.container = db, container
		cli := whatsmeow.NewClient(dev, slogAdapter{l: b.log, mod: "client"})
		b.cli = realClient{cli}
	} else {
		dev := b.container.NewDevice()
		b.cli = realClient{whatsmeow.NewClient(dev, slogAdapter{l: b.log, mod: "client"})}
	}
	b.handlerID = b.cli.AddEventHandler(b.handleEvent)
	b.loggedOut.Store(false)
	return b.cli, nil
}

// rebuildClientLocked replaces a client whose device was logged out by a fresh
// unpaired one (whatsmeow deletes the device row on logout).
func (b *Backend) rebuildClientLocked(ctx context.Context) (waClient, error) {
	if b.injected {
		return b.cli, nil
	}
	if b.cli != nil {
		b.cli.RemoveEventHandler(b.handlerID)
		b.cli.Disconnect()
		b.cli = nil
	}
	if b.container == nil {
		return b.ensureClientLocked(ctx)
	}
	return b.ensureClientLocked(ctx)
}

// currentClient returns the client without creating it.
func (b *Backend) currentClient() waClient {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	return b.cli
}

func (b *Backend) requireClient() (waClient, error) {
	cli := b.currentClient()
	if cli == nil || !b.connected.Load() {
		return nil, ErrNotConnected
	}
	return cli, nil
}

// paired reports whether the device is linked to an account.
func paired(cli waClient) bool {
	d := cli.Device()
	return d != nil && d.ID != nil
}

// NeedsPairing implements proto.Pairer: the device is not linked (or the phone
// unlinked it). It opens the device store lazily, like Python's REST probe.
func (b *Backend) NeedsPairing() bool {
	if b.loggedOut.Load() {
		return true
	}
	cli, err := b.ensureClient(context.Background())
	if err != nil {
		b.log.Debug("WhatsApp device store unavailable", "err", err)
		return false
	}
	return !paired(cli)
}

// Connect implements proto.Backend: it opens the device store, connects the
// websocket, waits for the session to become ready and loads the contacts.
// An unlinked device is not an error: Connect returns nil and the backend
// waits for the QR flow (NeedsPairing is true).
func (b *Backend) Connect(ctx context.Context) error {
	b.stopped.Store(false)
	b.lifeMu.Lock()
	select {
	case <-b.closed:
		b.closed = make(chan struct{}) // reconnect after a Disconnect
	default:
	}
	b.lifeMu.Unlock()
	b.loadProtocolCache(ctx)
	cli, err := b.ensureClient(ctx)
	if err != nil {
		return err
	}
	if !paired(cli) {
		b.connected.Store(false)
		return nil
	}
	b.attachStores(cli)
	if !cli.IsConnected() {
		if err := cli.Connect(); err != nil {
			return err
		}
	}
	ready := b.waitReady(ctx, cli, connectTimeout)
	if !ready {
		b.log.Warn("WhatsApp session did not become ready in time; continuing")
	}
	b.connected.Store(cli.IsConnected() && cli.IsLoggedIn())
	b.started.Store(true)
	b.warm.Store(ready)
	b.afterConnect(ctx, cli, ready)
	return nil
}

// WaitReady implements core.ReadyWaiter: the boot wait (Python polled the
// WAHA session until WORKING). It waits up to timeout for the client to be
// connected and logged in. When the session only became ready after Connect
// gave up waiting, the work Python did after WORKING runs now: the contact
// load, the LID warm-up (-> lid_warmup_done) and the presence announcement.
func (b *Backend) WaitReady(ctx context.Context, timeout time.Duration) bool {
	cli, err := b.ensureClient(ctx)
	if err != nil || !paired(cli) {
		return false
	}
	if !b.waitReady(ctx, cli, timeout) {
		return false
	}
	b.connected.Store(true)
	if !b.warm.Swap(true) {
		b.loadContacts(ctx)
		b.warmup(ctx, cli)
	}
	return true
}

// ReloadContacts implements core.ContactsReloader (_load_contacts).
func (b *Backend) ReloadContacts(ctx context.Context) { b.loadContacts(ctx) }

// waitReady polls until the client is connected and logged in.
func (b *Backend) waitReady(ctx context.Context, cli waClient, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cli.IsConnected() && cli.IsLoggedIn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// afterConnect is the tail of connect_sync: contacts, LID resolver, presence.
func (b *Backend) afterConnect(ctx context.Context, cli waClient, ready bool) {
	b.loadContacts(ctx)
	b.guard.noteConnected()
	if ready {
		b.warmup(ctx, cli)
	}
}

// warmup is what connect_sync did once the session was WORKING: start the LID
// resolver (idempotent) and announce presence.
func (b *Backend) warmup(ctx context.Context, cli waClient) {
	b.StartLIDResolver()
	// Announcing "available" is what makes WhatsApp deliver typing updates, but
	// it also mutes the phone's push notifications: only when the typing
	// indicator is enabled (WAHA_PRESENCE_SUBSCRIBE), like Python.
	if presenceEnabled() {
		if err := cli.SendPresence(ctx, "available"); err != nil {
			b.log.Debug("WhatsApp presence available failed", "err", err)
		}
	}
}

// Disconnect implements proto.Backend.
func (b *Backend) Disconnect(ctx context.Context) error {
	b.stopped.Store(true)
	b.connected.Store(false)
	b.guard.Stop()
	b.cancelPairing()
	b.lifeMu.Lock()
	cli := b.cli
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	b.lifeMu.Unlock()
	if cli != nil {
		cli.Disconnect()
	}
	b.mediaMu.Lock()
	for _, c := range b.lazyFlight {
		c.finish("")
	}
	b.mediaMu.Unlock()
	return nil
}

// Close releases the device store (after Disconnect).
func (b *Backend) Close() error {
	_ = b.Disconnect(context.Background())
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	if b.db != nil {
		err := b.db.Close()
		b.db = nil
		return err
	}
	return nil
}

// restart reconnects the websocket keeping the linked device (the guard's
// restart_session: POST /restart keeps auth).
func (b *Backend) restart(ctx context.Context) bool {
	cli := b.currentClient()
	if cli == nil || !paired(cli) {
		return false
	}
	b.stopped.Store(false)
	cli.Disconnect()
	if err := cli.Connect(); err != nil {
		b.log.Warn("WhatsApp reconnect failed", "err", err)
		return false
	}
	return true
}

// EnsureMediaDir implements media.MediaDirProvider (_ensure_media_dir).
func (b *Backend) EnsureMediaDir() (string, error) {
	dir := b.mediaDir
	if dir == "" {
		return "", errors.New("whatsapp: no media dir configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// OwnJID is the linked account's phone JID in WAHA form ("" if unlinked).
func (b *Backend) ownIDs() (pn, lid string) {
	cli := b.currentClient()
	if cli == nil {
		return "", ""
	}
	d := cli.Device()
	if d == nil {
		return "", ""
	}
	if d.ID != nil {
		pn = WAHAFromJID(*d.ID)
	}
	if !d.LID.IsEmpty() {
		lid = WAHAFromJID(d.LID)
	}
	return pn, lid
}

// disconnectedSince is when the websocket was last seen going down (zero while up).
func (b *Backend) disconnectedSince() time.Time {
	b.discMu.Lock()
	defer b.discMu.Unlock()
	return b.discAt
}

func (b *Backend) setDisconnected(down bool) {
	b.discMu.Lock()
	defer b.discMu.Unlock()
	if !down {
		b.discAt = time.Time{}
	} else if b.discAt.IsZero() {
		b.discAt = nowFunc()
	}
}
