package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// waClient is the slice of *whatsmeow.Client the backend uses. The real client
// satisfies it through realClient; tests inject a fake (nothing here touches
// the network).
type waClient interface {
	AddEventHandler(h whatsmeow.EventHandler) uint32
	RemoveEventHandler(id uint32) bool
	Connect() error
	Disconnect()
	Logout(ctx context.Context) error
	IsConnected() bool
	IsLoggedIn() bool
	GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)

	// App state (contact names, archive flags, chats read on the phone).
	FetchAppState(ctx context.Context, name appstate.WAPatchName, fullSync, onlyIfNotSynced bool) error
	EnableAppStateEvents()

	SendMessage(ctx context.Context, to types.JID, msg *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
	SendPeerMessage(ctx context.Context, msg *waE2E.Message) (whatsmeow.SendResponse, error)
	Upload(ctx context.Context, plaintext []byte, appInfo whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)
	GenerateMessageID() types.MessageID
	BuildEdit(chat types.JID, id types.MessageID, newContent *waE2E.Message) *waE2E.Message
	BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message
	BuildHistorySyncRequest(last *types.MessageInfo, count int) *waE2E.Message

	MarkRead(ctx context.Context, ids []types.MessageID, ts time.Time, chat, sender types.JID, extra ...types.ReceiptType) error
	SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error
	SendPresence(ctx context.Context, state types.Presence) error
	SubscribePresence(ctx context.Context, jid types.JID) error
	SendAppState(ctx context.Context, patch appstate.PatchInfo) error

	GetProfilePictureInfo(ctx context.Context, jid types.JID, params *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error)
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	GetJoinedGroups(ctx context.Context) ([]*types.GroupInfo, error)
	ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)

	// Device is the client's device store (own JID/LID, LID map, contacts).
	Device() *store.Device
}

// realClient adapts *whatsmeow.Client (whose Store is a field) to waClient.
type realClient struct{ *whatsmeow.Client }

func (c realClient) Device() *store.Device { return c.Client.Store }

// FetchAppState proxies whatsmeow's app state sync.
func (c realClient) FetchAppState(ctx context.Context, name appstate.WAPatchName, fullSync, onlyIfNotSynced bool) error {
	return c.Client.FetchAppState(ctx, name, fullSync, onlyIfNotSynced)
}

// EnableAppStateEvents makes a full app state resync dispatch the individual
// events (Archive, MarkChatAsRead, Contact, ...); whatsmeow drops them by
// default, which would hide the archive/read state of a fresh process.
func (c realClient) EnableAppStateEvents() { c.Client.EmitAppStateEventsOnFullSync = true }

// slogAdapter routes whatsmeow's logger into slog.
type slogAdapter struct {
	l   *slog.Logger
	mod string
}

func (a slogAdapter) log() *slog.Logger {
	if a.mod == "" {
		return a.l
	}
	return a.l.With("mod", a.mod)
}
func (a slogAdapter) Warnf(msg string, args ...any)  { a.log().Warn(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Errorf(msg string, args ...any) { a.log().Error(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Infof(msg string, args ...any)  { a.log().Debug(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Debugf(msg string, args ...any) {}
func (a slogAdapter) Sub(module string) waLog.Logger { return slogAdapter{l: a.l, mod: module} }

// browserIdentity is the client identity announced to WhatsApp at
// registration: the phone labels the linked device from the platform type and
// OS ("Chrome (Linux)") and the QR client type is derived from the platform
// type too.
type browserIdentity struct {
	OS      string
	Browser string
	Version string
}

// defaultBrowserIdentity mimics a recent desktop Chrome instead of
// whatsmeow's defaults (Os "whatsmeow", PlatformType UNKNOWN). It is
// overridable from config.json (whatsapp_browser, whatsapp_browser_version,
// whatsapp_browser_os). Bump the version when the string falls too far behind
// Chrome stable (https://chromereleases.googleblog.com/).
var defaultBrowserIdentity = browserIdentity{OS: "Linux", Browser: "Chrome", Version: "157.0.8103.39"}

// browserPlatforms maps the configured browser name to WhatsApp's platform
// enum; only the browsers WhatsApp itself lists are accepted.
var browserPlatforms = map[string]waCompanionReg.DeviceProps_PlatformType{
	"chrome":  waCompanionReg.DeviceProps_CHROME,
	"firefox": waCompanionReg.DeviceProps_FIREFOX,
	"edge":    waCompanionReg.DeviceProps_EDGE,
	"opera":   waCompanionReg.DeviceProps_OPERA,
	"safari":  waCompanionReg.DeviceProps_SAFARI,
	"ie":      waCompanionReg.DeviceProps_IE,
	"desktop": waCompanionReg.DeviceProps_DESKTOP,
	"android": waCompanionReg.DeviceProps_ANDROID_PHONE,
}

// resolveBrowserIdentity starts from the default and applies the configured
// overrides; an unknown browser name is reported and ignored.
func resolveBrowserIdentity(cfg Config, log *slog.Logger) browserIdentity {
	id := defaultBrowserIdentity
	if cfg == nil {
		return id
	}
	if v := cfg.WhatsAppBrowser(); v != "" {
		if _, ok := browserPlatforms[strings.ToLower(v)]; ok {
			id.Browser = v
		} else {
			log.Warn("whatsapp: unknown whatsapp_browser, using Chrome", "value", v)
		}
	}
	if v := cfg.WhatsAppBrowserVersion(); v != "" {
		id.Version = v
	}
	if v := cfg.WhatsAppBrowserOS(); v != "" {
		id.OS = v
	}
	return id
}

// applyBrowserIdentity overrides whatsmeow's process-wide client payload and
// device props, which are read when the registration payload and the QR codes
// are generated. It only affects a new pairing: the linked-device label is
// fixed server-side at registration time.
func applyBrowserIdentity(id browserIdentity) {
	platform, ok := browserPlatforms[strings.ToLower(id.Browser)]
	if !ok {
		platform = waCompanionReg.DeviceProps_CHROME
	}
	store.DeviceProps.Os = proto.String(id.OS)
	store.DeviceProps.PlatformType = platform.Enum()
	if store.BaseClientPayload.WebInfo == nil {
		store.BaseClientPayload.WebInfo = &waWa6.ClientPayload_WebInfo{}
	}
	store.BaseClientPayload.WebInfo.Browser = proto.String(id.Browser)
	store.BaseClientPayload.WebInfo.BrowserVersion = proto.String(id.Version)
}

// openStore opens the whatsmeow device store on a separate SQLite file next
// to the message cache (modernc.org/sqlite, foreign keys on) and returns the
// container and the (first or new) device.
func openStore(ctx context.Context, path string, log *slog.Logger) (*sql.DB, *sqlstore.Container, *store.Device, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("whatsapp: open device store: %w", err)
	}
	container := sqlstore.NewWithDB(db, "sqlite3", slogAdapter{l: log, mod: "store"})
	if err := container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("whatsapp: upgrade device store: %w", err)
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("whatsapp: load device: %w", err)
	}
	return db, container, dev, nil
}
