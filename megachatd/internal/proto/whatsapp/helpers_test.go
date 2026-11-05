package whatsapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

// fakeClient is a waClient that never touches the network.
type fakeClient struct {
	mu        sync.Mutex
	dev       *wastore.Device
	connected bool
	loggedIn  bool
	handler   whatsmeow.EventHandler

	sent      []sentMsg
	peerSent  []*waE2E.Message
	uploads   [][]byte
	reads     []readCall
	chatPres  []types.ChatPresence
	appStates []appstate.PatchInfo
	sendErr   error
	download  map[string][]byte
	qr        chan whatsmeow.QRChannelItem
	appState  []appStateCall
	connects  int
	disc      int
	joined    []*types.GroupInfo
	idCounter int
	onWhats   map[string]bool
	picture   *types.ProfilePictureInfo
	pictureEr error
}

type sentMsg struct {
	To    types.JID
	Msg   *waE2E.Message
	Extra whatsmeow.SendRequestExtra
}

type readCall struct {
	IDs    []types.MessageID
	Chat   types.JID
	Sender types.JID
}

func newFakeClient() *fakeClient {
	own := types.NewADJID("390000000000", 0, 7)
	return &fakeClient{
		dev:       &wastore.Device{ID: &own, LID: types.NewJID("555555", types.HiddenUserServer)},
		connected: true, loggedIn: true, download: map[string][]byte{}, onWhats: map[string]bool{},
	}
}

func (f *fakeClient) AddEventHandler(h whatsmeow.EventHandler) uint32 { f.handler = h; return 1 }
func (f *fakeClient) RemoveEventHandler(uint32) bool                  { f.handler = nil; return true }
func (f *fakeClient) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	f.connected = true
	return nil
}
func (f *fakeClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disc++
	f.connected = false
}
func (f *fakeClient) Logout(context.Context) error { return nil }
func (f *fakeClient) IsConnected() bool            { f.mu.Lock(); defer f.mu.Unlock(); return f.connected }
func (f *fakeClient) IsLoggedIn() bool             { return f.loggedIn }

// appStateCall is one FetchAppState invocation.
type appStateCall struct {
	Name            appstate.WAPatchName
	Full            bool
	OnlyIfNotSynced bool
}

func (f *fakeClient) FetchAppState(_ context.Context, name appstate.WAPatchName, fullSync, onlyIfNotSynced bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appState = append(f.appState, appStateCall{name, fullSync, onlyIfNotSynced})
	return nil
}

func (f *fakeClient) EnableAppStateEvents() {}

func (f *fakeClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dev.ID != nil {
		return nil, whatsmeow.ErrQRStoreContainsID
	}
	f.qr = make(chan whatsmeow.QRChannelItem, 8)
	return f.qr, nil
}
func (f *fakeClient) SendMessage(_ context.Context, to types.JID, m *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return whatsmeow.SendResponse{}, f.sendErr
	}
	var e whatsmeow.SendRequestExtra
	if len(extra) > 0 {
		e = extra[0]
	}
	f.sent = append(f.sent, sentMsg{to, m, e})
	return whatsmeow.SendResponse{ID: e.ID, Timestamp: time.Now()}, nil
}
func (f *fakeClient) SendPeerMessage(_ context.Context, m *waE2E.Message) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peerSent = append(f.peerSent, m)
	return whatsmeow.SendResponse{}, nil
}
func (f *fakeClient) Upload(_ context.Context, data []byte, _ whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, data)
	return whatsmeow.UploadResponse{URL: "https://mmg.example/x", DirectPath: "/x", MediaKey: []byte("k"),
		FileEncSHA256: []byte("e"), FileSHA256: []byte("s"), FileLength: uint64(len(data))}, nil
}
func (f *fakeClient) Download(_ context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.download[string(msg.GetDirectPath())]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeClient) GenerateMessageID() types.MessageID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idCounter++
	return types.MessageID("3EB0A1B2C3D4E5F6" + string(rune('A'+f.idCounter)))
}
func (f *fakeClient) BuildEdit(chat types.JID, id types.MessageID, nc *waE2E.Message) *waE2E.Message {
	return (&whatsmeow.Client{}).BuildEdit(chat, id, nc)
}
func (f *fakeClient) BuildReaction(chat, sender types.JID, id types.MessageID, r string) *waE2E.Message {
	return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key: (&waCommonKey{chat: chat, sender: sender, id: id}).key(), Text: gproto.String(r)}}
}
func (f *fakeClient) BuildHistorySyncRequest(last *types.MessageInfo, count int) *waE2E.Message {
	return (&whatsmeow.Client{}).BuildHistorySyncRequest(last, count)
}
func (f *fakeClient) MarkRead(_ context.Context, ids []types.MessageID, _ time.Time, chat, sender types.JID, _ ...types.ReceiptType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, readCall{ids, chat, sender})
	return nil
}
func (f *fakeClient) SendChatPresence(_ context.Context, _ types.JID, s types.ChatPresence, _ types.ChatPresenceMedia) error {
	f.chatPres = append(f.chatPres, s)
	return nil
}
func (f *fakeClient) SendPresence(context.Context, types.Presence) error { return nil }
func (f *fakeClient) SubscribePresence(context.Context, types.JID) error { return nil }
func (f *fakeClient) SendAppState(_ context.Context, p appstate.PatchInfo) error {
	f.appStates = append(f.appStates, p)
	return nil
}
func (f *fakeClient) GetProfilePictureInfo(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
	return f.picture, f.pictureEr
}
func (f *fakeClient) IsOnWhatsApp(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	out := make([]types.IsOnWhatsAppResponse, 0, len(phones))
	for _, p := range phones {
		out = append(out, types.IsOnWhatsAppResponse{Query: p, IsIn: f.onWhats[p]})
	}
	return out, nil
}
func (f *fakeClient) GetJoinedGroups(context.Context) ([]*types.GroupInfo, error) {
	return f.joined, nil
}
func (f *fakeClient) ParseWebMessage(chat types.JID, w *waWeb.WebMessageInfo) (*events.Message, error) {
	k := w.GetKey()
	info := types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: k.GetFromMe(), IsGroup: chat.Server == types.GroupServer},
		ID:            types.MessageID(k.GetID()), Timestamp: time.Unix(int64(w.GetMessageTimestamp()), 0), PushName: w.GetPushName(),
	}
	if info.IsGroup && k.GetParticipant() != "" {
		info.Sender, _ = types.ParseJID(k.GetParticipant())
	} else if !info.IsFromMe {
		info.Sender = chat
	}
	return &events.Message{Info: info, Message: w.GetMessage(), RawMessage: w.GetMessage()}, nil
}
func (f *fakeClient) Device() *wastore.Device { return f.dev }

// ---- fakes of the device tables ----------------------------------------------------------

type fakeLIDs struct {
	mu sync.Mutex
	pn map[string]string // lid user -> pn user
}

func newFakeLIDs() *fakeLIDs { return &fakeLIDs{pn: map[string]string{}} }

func (f *fakeLIDs) put(lid, pn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pn[lid] = pn
}

func (f *fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pn[lid.User]; ok {
		return types.NewJID(p, types.DefaultUserServer), nil
	}
	return types.EmptyJID, nil
}
func (f *fakeLIDs) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for l, p := range f.pn {
		if p == pn.User {
			return types.NewJID(l, types.HiddenUserServer), nil
		}
	}
	return types.EmptyJID, nil
}
func (f *fakeLIDs) PutLIDMapping(_ context.Context, lid, pn types.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pn[lid.User] = pn.User
	return nil
}

type fakeContacts struct {
	mu sync.Mutex
	m  map[types.JID]types.ContactInfo
}

func (f *fakeContacts) put(j types.JID, info types.ContactInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[j] = info
}

func (f *fakeContacts) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = map[types.JID]types.ContactInfo{}
}

func (f *fakeContacts) GetContact(_ context.Context, j types.JID) (types.ContactInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[j], nil
}
func (f *fakeContacts) GetAllContacts(context.Context) (map[types.JID]types.ContactInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[types.JID]types.ContactInfo, len(f.m))
	for k, v := range f.m {
		out[k] = v
	}
	return out, nil
}

// ---- environment -----------------------------------------------------------------------------

type env struct {
	b    *Backend
	fc   *fakeClient
	st   *store.Store
	cfg  *config.Config
	dir  string
	lids *fakeLIDs
	book *fakeContacts
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, map[string]string{})
	st, err := store.Open(filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fc := newFakeClient()
	b := New(Options{Config: cfg, Store: st, Logger: quietLogger(), Client: fc})
	b.connected.Store(true)
	b.historyTimeout = 40 * time.Millisecond
	e := &env{b: b, fc: fc, st: st, cfg: cfg, dir: dir, lids: newFakeLIDs(), book: &fakeContacts{m: map[types.JID]types.ContactInfo{}}}
	b.lidStoreForTest = e.lids
	b.contactStoreForTest = e.book
	// The background LID resolver writes the cache inside the temp dir: let it
	// finish before TempDir's cleanup removes the directory.
	t.Cleanup(func() { b.lidRunWG.Wait(); b.unreadHealWG.Wait() })
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	return e
}

// drain returns "type:contact" of the pending events.
func drain(b *Backend) []proto.Event { return b.PollOnce() }

func jid(user, server string) types.JID { return types.NewJID(user, server) }

type waCommonKey struct {
	chat, sender types.JID
	id           types.MessageID
}

func (k *waCommonKey) key() *waCommon.MessageKey {
	m := &waCommon.MessageKey{
		FromMe: gproto.Bool(true), ID: gproto.String(string(k.id)), RemoteJID: gproto.String(k.chat.String()),
	}
	if !k.sender.IsEmpty() {
		m.FromMe = gproto.Bool(false)
		if k.chat.Server == types.GroupServer {
			m.Participant = gproto.String(k.sender.String())
		}
	}
	return m
}

func errNotSet() error { return whatsmeow.ErrProfilePictureNotSet }

var (
	eventsDisconnected = events.Disconnected{}
	eventsConnected    = events.Connected{}
	eventsLoggedOut    = events.LoggedOut{}
)

func reactionMsg(target string, fromMe bool, emoji string) *waE2E.Message {
	return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key:  &waCommon.MessageKey{ID: gproto.String(target), FromMe: gproto.Bool(fromMe)},
		Text: gproto.String(emoji)}}
}

func editMsg(target, newText string) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key:           &waCommon.MessageKey{ID: gproto.String(target)},
		EditedMessage: &waE2E.Message{Conversation: gproto.String(newText)}}}
}

// countingDownload wraps the fake client's Download to count and delay calls.
type countingClient struct {
	*fakeClient
	mu2   sync.Mutex
	count int
	delay time.Duration
}

func (c *countingClient) Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error) {
	c.mu2.Lock()
	c.count++
	c.mu2.Unlock()
	time.Sleep(c.delay)
	return c.fakeClient.Download(ctx, m)
}

func (c *countingClient) downloads() int { c.mu2.Lock(); defer c.mu2.Unlock(); return c.count }
