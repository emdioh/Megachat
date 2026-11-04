// Package telegram is the Go port of protocols/telegram.py: the Telegram
// backend, built on gotd/td (MTProto) instead of Telethon.
//
// Besides proto.Backend it implements the capability interfaces Python's
// TelegramBackend has (AttachmentSender, BatchAttachmentSender (albums),
// AttachmentResolver, AttachmentChunker, SentMirror, Editor, Reactor,
// ReactionLister, AddressBook, ContactRefresher, ContactFinder, Pairer
// (QR login), TwoFactor, HistoryFetcher), plus Archiver (not in Python), the
// core hooks (core.MessageIngester, core.ReceiptProcessor,
// core.ContactRegistrar), media.ProfilePhotoProvider and
// linking.TelegramLogin.
//
// The user's existing Telethon session (SQLite ".session" file or a
// StringSession) is converted into a gotd session stored next to it (see
// session.go); the Telethon file is never modified.
//
// Contact ids and cache keys follow Telethon's "marked" peer ids exactly as
// Python stored them: users keep their id, legacy groups are -id and channels
// -1000000000000-id.
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"github.com/gotd/td/tg"
)

// Tunables from telegram.py.
const (
	maxCachePerContact     = 50
	incomingDedupWindowMs  = 2000
	availableReactionsTTL  = 600 * time.Second
	attachmentSendTimeout  = 120 * time.Second
	tgrefPrefix            = "tgref:"
	dialogLimit            = 200
	defaultAddressBookTTLs = 300
	reactionNotifyEnvVar   = "TELEGRAM_REACTIONS_NOTIFY"
	maxAvatarBytes         = 5 * 1024 * 1024
)

// Errors.
var (
	ErrNotConnected   = errors.New("Telegram backend not connected")
	ErrNotConfigured  = errors.New("telegram: API id/hash not configured")
	ErrNotAuthorized  = errors.New("telegram: not authorised, QR pairing required")
	ErrInvalidContact = errors.New("Invalid Telegram contact id")
	ErrInvalidReplyID = errors.New("Invalid Telegram reply message id")
	ErrInvalidMessage = errors.New("Invalid Telegram contact/message id")
)

// Config is the configuration the backend needs. *config.Config implements it.
type Config interface {
	TelegramAPIID() int
	TelegramAPIHash() string
	TelegramSessionPath() string
	TelegramMediaDir() string
	AddressBookTTLS() int
}

// Store is the slice of internal/store the backend persists through.
// *store.Store implements it.
type Store interface {
	LoadCache(ctx context.Context, protocol string) (map[string][]store.Message, error)
	AddMessage(ctx context.Context, m store.NewMessage) (id int64, inserted bool, err error)
	UpdateMessageID(ctx context.Context, protocol, contact, text string, isMine bool, timestamp int64, msgID string) (bool, error)
	UpdateMessageAttachmentID(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID string, expected *string) (bool, error)
	UpdateMessageAttachmentInfo(ctx context.Context, protocol, contact, msgID string, timestamp int64, info string) (bool, error)
	MarkAsRead(ctx context.Context, protocol, contact string, extraKeys ...string) error
	UpdateMessageStatusByID(ctx context.Context, msgID, status, protocol string, contact *string) (bool, error)
	UpdateMessageText(ctx context.Context, u store.TextUpdate) (bool, error)
	ReplaceReactionsSnapshot(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64, entries []store.ReactionEntry, ts int64) (bool, error)
	ReactionsForContact(ctx context.Context, protocol, contact string) ([]store.Reaction, error)
	ResolveReactionTargetRow(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64) (*store.ReactionTarget, error)
}

// BatchSlotMerger is an OPTIONAL store extension for _merge_batch_slot (Python
// ran raw SQL: UPDATE messages SET batch_id=?, batch_index=? WHERE protocol=?
// AND contact_number=? AND msg_id=? AND batch_id IS NULL). It reports whether
// a row was touched. Without it the slot merge is skipped.
type BatchSlotMerger interface {
	MergeBatchSlot(ctx context.Context, protocol, contact, msgID, batchID string, batchIndex *int64) (bool, error)
}

// Options configures New.
type Options struct {
	Config Config // required
	Store  Store  // optional: without it nothing is persisted
	Logger *slog.Logger

	// Overrides of the Config values (tests).
	APIID          int
	APIHash        string
	SessionPath    string
	MediaDir       string
	AddressBookTTL time.Duration

	// API injects an already connected client (tests); the MTProto client is
	// then never started.
	API *tg.Client
	// Self is the account user used with API.
	Self *tg.User

	Clock func() time.Time
}

// Backend is the Telegram backend.
type Backend struct {
	cfg   Config
	st    Store
	log   *slog.Logger
	clock func() time.Time

	apiID       int
	apiHash     string
	sessionPath string
	mediaDir    string
	bookTTL     time.Duration

	// Lifecycle (lifeMu): the run goroutine owns the MTProto client.
	lifeMu sync.Mutex
	run    *runState
	closed chan struct{}
	peers  *peerStore
	states *stateStorage
	imp    *ImportedSession

	apiMu sync.RWMutex
	api   *tg.Client
	self  *tg.User

	connected    atomic.Bool
	unauthorised atomic.Bool
	needs2FA     atomic.Bool
	login        loginState

	queue *eventQueue

	// Contacts (cmu).
	cmu          sync.RWMutex
	contacts     []*proto.Contact
	byID         map[int64]*proto.Contact
	addressBook  []proto.Contact
	addressBookT time.Time
	addressBookS bool

	// Message cache and ingest state (ingestMu).
	ingestMu sync.Mutex
	cache    map[string][]*entry
	seen     map[string]struct{}

	reactMu     sync.Mutex
	reactions   []string
	reactionsTS time.Time
}

// New builds a Telegram backend.
func New(o Options) *Backend {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	b := &Backend{
		cfg: o.Config, st: o.Store, log: o.Logger, clock: o.Clock,
		apiID: o.APIID, apiHash: o.APIHash, sessionPath: o.SessionPath, mediaDir: o.MediaDir,
		bookTTL: o.AddressBookTTL,
		queue:   newEventQueue(), closed: make(chan struct{}),
		byID: map[int64]*proto.Contact{}, cache: map[string][]*entry{}, seen: map[string]struct{}{},
	}
	if o.Config != nil {
		if b.apiID == 0 {
			b.apiID = o.Config.TelegramAPIID()
		}
		if b.apiHash == "" {
			b.apiHash = o.Config.TelegramAPIHash()
		}
		if b.sessionPath == "" {
			b.sessionPath = o.Config.TelegramSessionPath()
		}
		if b.mediaDir == "" {
			b.mediaDir = o.Config.TelegramMediaDir()
		}
		if b.bookTTL == 0 {
			b.bookTTL = time.Duration(o.Config.AddressBookTTLS()) * time.Second
		}
	}
	if b.bookTTL == 0 {
		b.bookTTL = defaultAddressBookTTLs * time.Second
	}
	b.peers = newPeerStore(peersPath(b.sessionPath))
	if o.API != nil {
		b.api, b.self = o.API, o.Self
		b.connected.Store(true)
	}
	return b
}

// Protocol implements proto.Backend.
func (b *Backend) Protocol() string { return proto.ProtocolTelegram }

// IsConnected implements proto.Backend.
func (b *Backend) IsConnected() bool { return b.connected.Load() }

func (b *Backend) getAPI() (*tg.Client, error) {
	b.apiMu.RLock()
	defer b.apiMu.RUnlock()
	if b.api == nil || !b.connected.Load() {
		return nil, ErrNotConnected
	}
	return b.api, nil
}

func (b *Backend) selfUser() *tg.User {
	b.apiMu.RLock()
	defer b.apiMu.RUnlock()
	return b.self
}

func (b *Backend) mediaDirPath() string { return b.mediaDir }

// ---- events -----------------------------------------------------------------------

func (b *Backend) emit(typ, contactID string, payload map[string]any) {
	b.queue.put(proto.Event{Type: typ, Protocol: proto.ProtocolTelegram, ContactID: contactID, Payload: payload})
}

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

// ---- helpers -------------------------------------------------------------------

func cloneContact(c proto.Contact) proto.Contact {
	m := make(map[string]any, len(c.Extras))
	for k, v := range c.Extras {
		m[k] = v
	}
	c.Extras = m
	return c
}

func cloneContacts(in []proto.Contact) []proto.Contact {
	out := make([]proto.Contact, len(in))
	for i, c := range in {
		out[i] = cloneContact(c)
	}
	return out
}

func nowMs() int64 { return time.Now().UnixMilli() }

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func int64OrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func getStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case string:
		return t != ""
	}
	return false
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case string:
		return toInt(t)
	}
	return 0, false
}

func trimmedNonEmpty(s string) bool { return strings.TrimSpace(s) != "" }
