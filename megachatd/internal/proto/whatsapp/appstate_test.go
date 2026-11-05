package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

// fakeChatSettings is a whatsmeow store.ChatSettingsStore backed by a map.
type fakeChatSettings struct {
	mu sync.Mutex
	m  map[string]types.LocalChatSettings
}

func newFakeChatSettings() *fakeChatSettings {
	return &fakeChatSettings{m: map[string]types.LocalChatSettings{}}
}

func (f *fakeChatSettings) set(chat types.JID, s types.LocalChatSettings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s.Found = true
	f.m[chat.String()] = s
}

func (f *fakeChatSettings) PutMutedUntil(_ context.Context, chat types.JID, until time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.m[chat.String()]
	s.Found, s.MutedUntil = true, until
	f.m[chat.String()] = s
	return nil
}

func (f *fakeChatSettings) PutPinned(_ context.Context, chat types.JID, pinned bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.m[chat.String()]
	s.Found, s.Pinned = true, pinned
	f.m[chat.String()] = s
	return nil
}

func (f *fakeChatSettings) PutArchived(_ context.Context, chat types.JID, archived bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.m[chat.String()]
	s.Found, s.Archived = true, archived
	f.m[chat.String()] = s
	return nil
}

func (f *fakeChatSettings) PutWASARootSecretID(context.Context, types.JID, types.MessageID) error {
	return nil
}

func (f *fakeChatSettings) GetWASARootSecretID(context.Context, types.JID) (types.MessageID, error) {
	return "", nil
}

func (f *fakeChatSettings) GetChatSettings(_ context.Context, chat types.JID) (types.LocalChatSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[chat.String()], nil
}

// archivedDeviceStore opens a paired whatsmeow device store and marks the given
// chats archived the way the phone does (whatsmeow_chat_settings).
func archivedDeviceStore(t *testing.T, chats ...string) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, container, _, err := openStore(context.Background(), filepath.Join(dir, "whatsmeow.db"), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dev := container.NewDevice()
	// whatsmeow stores the device JID as our_jid (with the AD suffix), so the
	// rows carry the same form as newFakeClient's Store.ID.
	own := types.NewADJID("390000000000", 0, 7)
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{}, AccountSignature: make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	if err := container.PutDevice(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	for _, chat := range chats {
		if _, err := db.Exec(`INSERT INTO whatsmeow_chat_settings (our_jid, chat_jid, archived) VALUES (?, ?, true)`, own.String(), chat); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// appStateCalls snapshots the FetchAppState invocations.
func (f *fakeClient) appStateCalls() []appStateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]appStateCall(nil), f.appState...)
}

func TestAppStateSyncFullThenIncremental(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.b.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(e.b.dataDir, appStateMarker)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker must not exist before the first run")
	}
	e.b.appStateSync()
	calls := e.fc.appStateCalls()
	want := []appStateCall{
		{Name: appstate.WAPatchCriticalUnblockLow, Full: true, OnlyIfNotSynced: false},
		{Name: appstate.WAPatchRegularLow, Full: true, OnlyIfNotSynced: false},
	}
	if len(calls) != len(want) {
		t.Fatalf("first run calls = %+v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("first run call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not written inside the data dir: %v", err)
	}
	// The marker makes the next run incremental: only fetch what the store has
	// not seen yet.
	e.b.appStateSync()
	calls = e.fc.appStateCalls()
	if len(calls) != 2*len(want) {
		t.Fatalf("after the second run calls = %+v", calls)
	}
	for i, w := range want {
		got := calls[len(want)+i]
		if got.Name != w.Name || got.Full || !got.OnlyIfNotSynced {
			t.Errorf("second run call %d = %+v", i, got)
		}
	}
	// The marker never lives outside the data dir.
	info, err := os.Stat(marker)
	if err != nil || info.IsDir() {
		t.Fatalf("marker: %v", err)
	}
}

func TestAppStateSyncFlushesUnreadDespiteThrottle(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.b.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chat := "393331234567@c.us"
	if _, err := e.b.IngestMessage(ctx, chat, map[string]any{"id": "false_393331234567@c.us_A1", "text": "x", "is_mine": false}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.LoadCache(ctx, "whatsapp"); err != nil {
		t.Fatal(err)
	}
	// The window was just spent (e.g. a contacts.list right before the
	// markChatAsRead mutations of the app-state sync arrived).
	e.b.cmu.Lock()
	e.b.lastUnread = nowFunc()
	e.b.readOnPhone[chat] = 0
	e.b.cmu.Unlock()
	e.b.ReconcileUnread(ctx)
	if rows := dbRows(t, e, chat); rows[0].Read {
		t.Fatal("the throttled path must skip a just-spent window")
	}
	e.b.appStateSync()
	if rows := dbRows(t, e, chat); !rows[0].Read {
		t.Error("the app-state tail must flush unread even right after the throttle window was spent")
	}
}

// TestSyncArchiveFlagsSeedsChatsWithoutHistory covers the restart path: the
// archived chats only exist in whatsmeow_chat_settings, with no local message
// and no contact of their own.
func TestSyncArchiveFlagsSeedsChatsWithoutHistory(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	e.lids.put("157968947515605", "393339999999")
	e.b.db = archivedDeviceStore(t,
		"393331234567@s.whatsapp.net", // named by the contact table
		"157968947515605@lid",         // named by the LID map, "+phone" fallback
		"214340544290860@lid",         // unknown, name stays empty
		"120363194870061140@g.us",
		"status@broadcast",
	)
	e.b.syncArchiveFlags(context.Background())

	e.b.cmu.RLock()
	chats := map[string]chatMeta{}
	for id, m := range e.b.chats {
		chats[id] = *m
	}
	e.b.cmu.RUnlock()
	cases := map[string]string{
		"393331234567@c.us":       "Mario Rossi",
		"157968947515605@lid":     "+393339999999",
		"214340544290860@lid":     "",
		"120363194870061140@g.us": "",
	}
	for id, name := range cases {
		m, ok := chats[id]
		if !ok {
			t.Errorf("%s: an archived chat without history must be registered", id)
			continue
		}
		if !m.Archived {
			t.Errorf("%s: must stay archived", id)
		}
		if m.LastTS != 0 {
			t.Errorf("%s: last ts = %d, want 0", id, m.LastTS)
		}
		if m.Name != name {
			t.Errorf("%s: name = %q, want %q", id, m.Name, name)
		}
	}
	if _, ok := chats["status@broadcast"]; ok {
		t.Error("broadcast chats must not enter the registry")
	}
	// The archived-only chats end up in the contact list the wire serves.
	e.b.loadContacts(context.Background())
	for _, id := range []string{"393331234567@c.us", "157968947515605@lid", "214340544290860@lid", "120363194870061140@g.us"} {
		c, ok := e.b.FindContact(id)
		if !ok {
			t.Errorf("%s: missing from the contact list", id)
			continue
		}
		if !exBool(&c, "archived") {
			t.Errorf("%s: contact is not archived", id)
		}
	}
}

func TestSyncArchiveFlagsSeedKeepsExistingChat(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "393472482603@c.us", map[string]any{"id": "false_393472482603@c.us_A1", "text": "x", "is_mine": false}, 4_000)
	e.b.loadProtocolCache(ctx)
	e.b.db = archivedDeviceStore(t, "393472482603@s.whatsapp.net")
	e.b.syncArchiveFlags(context.Background())
	e.b.loadContacts(ctx)
	list, _ := e.b.ListContacts(ctx)
	n := 0
	for _, c := range list {
		if c.ID == "393472482603@c.us" {
			n++
			if !exBool(&c, "archived") || c.LastMessageTS() != 4_000 {
				t.Errorf("merged chat lost its data: %+v", c)
			}
		}
	}
	if n != 1 {
		t.Errorf("contacts for the chat = %d, want 1", n)
	}
}

// putTestDevice registers another paired device in the same whatsmeow.db, so
// chat-settings rows for its account satisfy the our_jid foreign key.
func putTestDevice(t *testing.T, db *sql.DB, own types.JID) {
	t.Helper()
	c := sqlstore.NewWithDB(db, "sqlite3", slogAdapter{l: quietLogger(), mod: "store"})
	dev := c.NewDevice()
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{}, AccountSignature: make([]byte, 64),
		AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	if err := c.PutDevice(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
}

// TestSyncArchiveFlagsScopesToCurrentDevice covers a whatsmeow.db reused across
// pairings (or holding several accounts): chat-settings rows keyed by another
// own JID must not be seeded into this account's registry.
func TestSyncArchiveFlagsScopesToCurrentDevice(t *testing.T) {
	e := newEnv(t)
	db := archivedDeviceStore(t, "393331234567@s.whatsapp.net")
	other := types.NewADJID("391111111111", 0, 9)
	putTestDevice(t, db, other)
	if _, err := db.Exec(`INSERT INTO whatsmeow_chat_settings (our_jid, chat_jid, archived) VALUES (?, ?, true)`,
		other.String(), "393339999999@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	e.b.db = db
	e.b.syncArchiveFlags(context.Background())

	e.b.cmu.RLock()
	mine, mineOK := e.b.chats["393331234567@c.us"]
	_, theirs := e.b.chats["393339999999@c.us"]
	e.b.cmu.RUnlock()
	if !mineOK || !mine.Archived {
		t.Error("the current account's archived chat must be seeded")
	}
	if theirs {
		t.Error("an archived chat of another account must not be seeded")
	}
}

// TestSyncArchiveFlagsSkipsUnpairedDevice asserts the seed is a no-op before
// pairing: there is no our_jid to scope the rows by yet.
func TestSyncArchiveFlagsSkipsUnpairedDevice(t *testing.T) {
	e := newEnv(t)
	e.b.db = archivedDeviceStore(t, "393331234567@s.whatsapp.net")
	e.fc.dev.ID = nil
	e.b.syncArchiveFlags(context.Background())
	e.b.cmu.RLock()
	_, ok := e.b.chats["393331234567@c.us"]
	e.b.cmu.RUnlock()
	if ok {
		t.Error("an unpaired device must not seed archived chats")
	}
}

func TestSyncArchiveFlagsSeedsFromChatSettings(t *testing.T) {
	e := newEnv(t)
	cs := newFakeChatSettings()
	e.fc.dev.ChatSettings = cs
	archived := "393331234567@c.us"
	cs.set(types.NewJID("393331234567", types.DefaultUserServer), types.LocalChatSettings{Archived: true})
	e.b.noteChat(archived, 0, "")
	e.b.noteChat("393339999999@c.us", 0, "")
	e.b.syncArchiveFlags(context.Background())
	e.b.cmu.RLock()
	got := e.b.chats[archived].Archived
	unknown := e.b.chats["393339999999@c.us"].Archived
	e.b.cmu.RUnlock()
	if !got {
		t.Error("the archived flag of the whatsmeow chat settings must seed the chat")
	}
	if unknown {
		t.Error("a chat without settings must not be archived")
	}
}

// unreadByMsgID maps each stored message id of a chat to its unread flag.
func unreadByMsgID(t *testing.T, e *env, chat string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range dbRows(t, e, chat) {
		if r.MsgID != nil {
			out[*r.MsgID] = !r.Read
		}
	}
	return out
}

// seedIncoming stores one incoming message per timestamp and returns the ids.
func seedIncoming(t *testing.T, e *env, chat string, times ...int64) []string {
	t.Helper()
	ids := make([]string, len(times))
	for i, ts := range times {
		ids[i] = fmt.Sprintf("false_%s_M%d", chat, i)
		if _, err := e.b.IngestMessage(ctx, chat, map[string]any{"id": ids[i], "text": ids[i], "is_mine": false}, ts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.st.LoadCache(ctx, "whatsapp"); err != nil {
		t.Fatal(err)
	}
	return ids
}

// markReadEvent builds the full-sync markChatAsRead snapshot the phone emits.
func markReadEvent(j types.JID, read bool, cutoff int64, withRange bool) *events.MarkChatAsRead {
	act := &waSyncAction.MarkChatAsReadAction{Read: gproto.Bool(read)}
	if withRange {
		act.MessageRange = &waSyncAction.SyncActionMessageRange{LastMessageTimestamp: gproto.Int64(cutoff)}
	}
	return &events.MarkChatAsRead{JID: j, Action: act, FromFullSync: true}
}

func TestMarkChatAsReadCutoff(t *testing.T) {
	chat := "393331234567@c.us"
	j := types.NewJID("393331234567", types.DefaultUserServer)

	t.Run("read up to the boundary only", func(t *testing.T) {
		e := newEnv(t)
		ids := seedIncoming(t, e, chat, 1000, 5000)
		e.b.handleEvent(markReadEvent(j, true, 3000, true))
		e.b.reconcileUnread(ctx, true)
		unread := unreadByMsgID(t, e, chat)
		if unread[ids[0]] {
			t.Error("a message older than the phone's boundary must be marked read")
		}
		if !unread[ids[1]] {
			t.Error("a message newer than the phone's boundary must stay unread")
		}
	})

	t.Run("explicit unread changes nothing", func(t *testing.T) {
		e := newEnv(t)
		ids := seedIncoming(t, e, chat, 1000, 5000)
		e.b.handleEvent(markReadEvent(j, false, 5000, true))
		e.b.cmu.RLock()
		_, recorded := e.b.readOnPhone[chat]
		e.b.cmu.RUnlock()
		if recorded {
			t.Error("an unread action carries no trustworthy boundary and must not be queued")
		}
		e.b.reconcileUnread(ctx, true)
		unread := unreadByMsgID(t, e, chat)
		if !unread[ids[0]] || !unread[ids[1]] {
			t.Error("a read=false action must leave the chat untouched")
		}
	})

	t.Run("no message range reads the whole chat", func(t *testing.T) {
		e := newEnv(t)
		ids := seedIncoming(t, e, chat, 1000, 5000)
		e.b.handleEvent(markReadEvent(j, true, 0, false))
		e.b.reconcileUnread(ctx, true)
		unread := unreadByMsgID(t, e, chat)
		if unread[ids[0]] || unread[ids[1]] {
			t.Error("an unbounded read action marks the whole chat read")
		}
	})

	t.Run("the largest cutoff wins", func(t *testing.T) {
		e := newEnv(t)
		ids := seedIncoming(t, e, chat, 1000, 4000, 7000)
		e.b.handleEvent(markReadEvent(j, true, 7000, true))
		e.b.handleEvent(markReadEvent(j, true, 3000, true))
		e.b.cmu.RLock()
		cutoff := e.b.readOnPhone[chat]
		e.b.cmu.RUnlock()
		if cutoff != 7000 {
			t.Fatalf("a later smaller cutoff must not shrink the boundary: %d", cutoff)
		}
		e.b.reconcileUnread(ctx, true)
		unread := unreadByMsgID(t, e, chat)
		// With the smaller 3000 boundary (a "min wins" bug), M1 and M2 would
		// stay unread; the boundary is inclusive, so 7000 reads all three.
		if unread[ids[0]] || unread[ids[1]] || unread[ids[2]] {
			t.Errorf("largest cutoff must be applied: %v", unread)
		}
	})
}

// TestChatListStateFromAppStateAndContacts reproduces the three chat-list
// symptoms (archived chats in the main list, inflated unread badge, "<n>@lid"
// names) against a fresh process and drives the production heal paths: the
// whatsmeow chat-settings seed, the full-sync app-state events and the
// appStateSync tail.
func TestChatListStateFromAppStateAndContacts(t *testing.T) {
	e := newEnv(t)
	group := "120363194870061140@g.us"
	lid := "157968947515605@lid"
	phone := "393331234567"

	cs := newFakeChatSettings()
	cs.set(types.NewJID("120363194870061140", types.GroupServer), types.LocalChatSettings{Archived: true})
	e.fc.dev.ChatSettings = cs
	e.book.put(types.NewJID(phone, types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	e.lids.put("157968947515605", phone)

	e.b.noteChat(group, 2000, "Archived Group")
	e.b.noteChat(lid, 4000, "")
	e.b.RegisterContact(*newContact(group, "Archived Group", map[string]any{"source": "wa_chats"}))
	e.b.RegisterContact(*newContact(lid, lid, map[string]any{"source": "wa_chats"}))
	seedIncoming(t, e, lid, 3000, 4000)
	unreadBefore, err := e.st.UnreadCounts(ctx)
	if err != nil || unreadBefore[store.ContactKey{Protocol: "whatsapp", Contact: lid}] != 2 {
		t.Fatalf("seed unread: %v err=%v", unreadBefore, err)
	}

	pass := func() {
		// The pre-existing archive flags of the device store.
		e.b.syncArchiveFlags(ctx)
		// The mutations a full app-state sync re-emits.
		e.b.handleEvent(&events.Archive{JID: types.NewJID("120363194870061140", types.GroupServer),
			Action: &waSyncAction.ArchiveChatAction{Archived: gproto.Bool(true)}, FromFullSync: true})
		e.b.handleEvent(markReadEvent(types.NewJID("157968947515605", types.HiddenUserServer), true, 4000, true))
		// appStateSync's tail (the resolver's own background pass is throttled
		// so this test stays deterministic; the steps still run synchronously).
		e.b.lidLastRunMu.Lock()
		e.b.lidLastRun = nowFunc()
		e.b.lidLastRunMu.Unlock()
		e.b.RefreshContacts(ctx, true)
		e.b.lidBulkImport(ctx)
		e.b.lidResolveCandidates()
		e.b.reconcileUnread(ctx, true)
	}
	pass()

	// 1) Archived: the group is flagged, the LID chat is not.
	groupC, ok := e.b.FindContact(group)
	if !ok || !exBool(&groupC, "archived") {
		t.Fatalf("the archived group must carry extras.archived: %+v ok=%v", groupC, ok)
	}
	lidC, ok := e.b.FindContact(lid)
	if !ok {
		t.Fatal("the LID chat must be a known contact")
	}
	if exBool(&lidC, "archived") {
		t.Errorf("the LID chat must stay out of the archive: %+v", lidC)
	}

	// 2) Identity: no "<n>@lid" name, phone and lid extras applied.
	if strings.HasSuffix(lidC.DisplayName, "@lid") || lidC.DisplayName != "Mario Rossi" {
		t.Errorf("the LID contact must take the address-book name, got %q", lidC.DisplayName)
	}
	if exStr(&lidC, "phone") != phone || exStr(&lidC, "lid") != lid {
		t.Errorf("the LID contact must carry phone+lid extras: %+v", lidC.Extras)
	}

	// 3) Unread: the phone's read snapshot healed the badge.
	if counts, err := e.st.UnreadCounts(ctx); err != nil || len(counts) != 0 {
		t.Errorf("unread must be flushed: %v err=%v", counts, err)
	}

	// 4) Idempotent second pass: same contacts, no duplicate, same state.
	total := func() int { cs, _ := e.b.ListContacts(ctx); return len(cs) }
	before := total()
	pass()
	if after := total(); after != before {
		t.Errorf("a second pass must not create contacts: %d -> %d", before, after)
	}
	n := 0
	contacts, _ := e.b.ListContacts(ctx)
	for _, c := range contacts {
		if c.ID == lid {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the LID contact exists %d times", n)
	}
	if c, _ := e.b.FindContact(lid); c.DisplayName != "Mario Rossi" || exStr(&c, "phone") != phone {
		t.Errorf("second pass changed the LID contact: %+v", c)
	}
	if g, _ := e.b.FindContact(group); !exBool(&g, "archived") {
		t.Errorf("second pass lost the archive flag: %+v", g)
	}
}
