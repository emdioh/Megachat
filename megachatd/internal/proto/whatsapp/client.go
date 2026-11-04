package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
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
