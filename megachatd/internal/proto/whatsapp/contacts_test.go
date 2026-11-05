package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/types"
)

func chat(id, name string, ts int64) *proto.Contact {
	return newContact(id, name, map[string]any{"jid": id, "last_message_ts": ts})
}

func setContacts(b *Backend, cs ...*proto.Contact) {
	b.cmu.Lock()
	b.contacts = cs
	b.rebuildAliasesLocked()
	b.cmu.Unlock()
}

// lidCacheEntry reads the persisted LID cache entry of a lid (tests only).
func lidCacheEntry(b *Backend, lid string) (lidEntry, bool) {
	b.lidMu.Lock()
	defer b.lidMu.Unlock()
	b.lidCacheLoadLocked()
	e, ok := b.lidMap[lid]
	return e, ok
}

// ---- _dedup_book_contacts (tests/test_address_book.py::TestWADedup) -----------------------------

func TestDedupBookContacts(t *testing.T) {
	type r = bookRaw
	cases := []struct {
		name string
		in   []r
		want []bookEntry
	}{
		{"duplicates collapse", []r{{ID: "393331234567@c.us", Name: "Mario"}, {ID: "393331234567@c.us", Name: "Mario"}},
			[]bookEntry{{Phone: "393331234567", Name: "Mario"}}},
		{"name beats pushname", []r{{ID: "393331234567@c.us", PushName: "MarioP"}, {ID: "393331234567@c.us", Name: "Mario Rossi"}},
			[]bookEntry{{Phone: "393331234567", Name: "Mario Rossi"}}},
		{"c.us tiebreak", []r{{ID: "393331234567@s.whatsapp.net", Name: "OldName"}, {ID: "393331234567@c.us", Name: "NewName"}},
			[]bookEntry{{Phone: "393331234567", Name: "NewName"}}},
		{"discards groups, broadcast, newsletters and no digits", []r{
			{ID: "123456789@g.us", Name: "Group"}, {ID: "status@broadcast", Name: "Status"}, {ID: "abc@newsletter", Name: "News"},
			{ID: "no-digits-here", Name: "NoDigits"}, {ID: "393331234567@c.us", Name: "Mario"}},
			[]bookEntry{{Phone: "393331234567", Name: "Mario"}}},
		{"lid row uses the explicit number and keeps the lid", []r{{ID: "220988985864200@lid", Number: "393331234567", Name: "Mario Rossi"}},
			[]bookEntry{{Phone: "393331234567", Name: "Mario Rossi", LID: "220988985864200@lid"}}},
		{"lid row without number keeps the lid digits", []r{{ID: "220988985864200@lid", Name: "Mario"}},
			[]bookEntry{{Phone: "220988985864200", Name: "Mario", LID: "220988985864200@lid"}}},
		{"lid and c.us collapse and keep the lid", []r{
			{ID: "220988985864200@lid", Number: "393331234567", Name: "Mario"}, {ID: "393331234567@c.us", Name: "Mario"}},
			[]bookEntry{{Phone: "393331234567", Name: "Mario", LID: "220988985864200@lid"}}},
		{"c.us row with a lid number keeps the real phone", []r{
			{ID: "491621098189@c.us", Number: "101649158803580", Name: "Praful Mechanda", PushName: "Praful"},
			{ID: "491621098189@c.us", Number: "491621098189", Name: "Praful Mechanda", PushName: "Praful"}},
			[]bookEntry{{Phone: "491621098189", Name: "Praful Mechanda", PushName: "Praful"}}},
	}
	for _, c := range cases {
		got := dedupBookContacts(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %+v, want %+v", c.name, got[i], c.want[i])
			}
		}
	}
}

func TestAddressBookNameMap(t *testing.T) {
	book := []proto.Contact{
		*newContact("393331234567@c.us", "Mario Rossi", map[string]any{"source": "wa_book", "phone": "393331234567", "lid": "139153@lid"}),
		*newContact("393339999999@c.us", "Luigi", map[string]any{"source": "wa_chats", "phone": "393339999999"}),
		*newContact("393338888888@c.us", "393338888888", map[string]any{"source": "wa_book", "phone": "393338888888"}),
		*newContact("1@lid", "Solo Lid", map[string]any{"source": "wa_book", "lid": "1@lid"}),
	}
	got := buildAddressBookNameMap(book)
	want := map[string]string{"393331234567": "Mario Rossi", "139153@lid": "Mario Rossi", "1@lid": "Solo Lid"}
	if len(got) != len(want) {
		t.Fatalf("name map = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("name map[%q] = %q, want %q", k, got[k], v)
		}
	}
	cs := []*proto.Contact{
		newContact("393331234567@c.us", "393331234567", map[string]any{"phone": "393331234567"}),
		newContact("9@lid", "9@lid", map[string]any{"lid": "139153@lid"}),
	}
	if n := applyAddressBookNames(cs, got); n != 2 || cs[0].DisplayName != "Mario Rossi" || cs[1].DisplayName != "Mario Rossi" {
		t.Errorf("apply names: n=%d %v %v", n, cs[0].DisplayName, cs[1].DisplayName)
	}
	if cachedAddressBookName(book, "393331234567", "") != "Mario Rossi" || cachedAddressBookName(book, "", "1@lid") != "Solo Lid" ||
		cachedAddressBookName(book, "5", "") != "" || cachedAddressBookName(nil, "393331234567", "") != "" {
		t.Error("cachedAddressBookName")
	}
}

// ---- the persistent @lid cache ----------------------------------------------------------------------

func TestLIDCachePersistenceAndTTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wa_lid_map.json")
	mk := func() *Backend { return New(Options{DataDir: dir, LIDMapPath: path, Logger: quietLogger()}) }
	now := time.Now().Unix()

	b1 := mk()
	b1.lidCacheLoad()
	b1.lidMu.Lock()
	b1.lidMap["220988985864200@lid"] = lidEntry{Phone: "393331234567", Name: "Mario", ResolvedAt: now}
	b1.lidMap["fresh@lid"] = lidEntry{Phone: "391234567890", ResolvedAt: now}
	b1.lidMap["stale@lid"] = lidEntry{Phone: "391234567890", ResolvedAt: now - 31*86400}
	b1.lidMap["neg_fresh@lid"] = lidEntry{ResolvedAt: now}
	b1.lidMap["neg_stale@lid"] = lidEntry{ResolvedAt: now - 25*3600}
	b1.lidMu.Unlock()
	b1.lidCacheSave()

	// The file keeps the Python layout so the learned mappings carry over.
	raw, _ := os.ReadFile(path)
	if want := `"version":1`; !contains(string(raw), want) || !contains(string(raw), `"resolved_at"`) || !contains(string(raw), `"neg_fresh@lid":{"name":null,"phone":null`) {
		t.Errorf("file layout: %s", raw)
	}

	b2 := mk()
	if got := b2.lidLookup("220988985864200@lid"); got != "393331234567" {
		t.Errorf("roundtrip lookup = %q", got)
	}
	if b2.lidLookup("fresh@lid") != "391234567890" || b2.lidLookup("stale@lid") != "" {
		t.Error("positive TTL is 30 days")
	}
	if e, ok := lidCacheEntry(b2, "neg_fresh@lid"); !ok || e.Phone != "" || e.ResolvedAt == 0 {
		t.Error("a fresh negative entry survives the round trip")
	}
	// lidCandidates is the production consumer of the negative TTL: a fresh
	// negative entry is not retried, an expired one and an unknown lid are.
	setContacts(b2,
		newContact("neg_fresh@lid", "x", nil),
		newContact("neg_stale@lid", "x", nil),
		newContact("unknown@lid", "x", nil))
	cands := map[string]bool{}
	for _, id := range b2.lidCandidates() {
		cands[id] = true
	}
	if cands["neg_fresh@lid"] {
		t.Error("the 24 h negative TTL must suppress a retry")
	}
	if !cands["neg_stale@lid"] || !cands["unknown@lid"] {
		t.Errorf("expired negatives and unknown lids must be retried: %v", cands)
	}
	if b2.lidName("220988985864200@lid") != "Mario" {
		t.Error("name kept")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestLIDCacheCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wa_lid_map.json")
	os.WriteFile(path, []byte("{not valid json"), 0o644)
	b := New(Options{DataDir: dir, LIDMapPath: path, Logger: quietLogger()})
	if b.lidLookup("x@lid") != "" || len(b.lidMap) != 0 {
		t.Error("a corrupt file must start empty")
	}
}

func TestLIDLookupUsesDeviceTable(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	if got := e.b.lidLookup("220988985864200@lid"); got != "393331234567" {
		t.Errorf("device table lookup = %q", got)
	}
	// ...and the answer is remembered in the JSON cache.
	e.b.lidMu.Lock()
	ent := e.b.lidMap["220988985864200@lid"]
	e.b.lidMu.Unlock()
	if ent.Phone != "393331234567" {
		t.Errorf("cache entry = %+v", ent)
	}
	if got := e.b.phoneToLID("393331234567"); got != "220988985864200@lid" {
		t.Errorf("reverse lookup = %q", got)
	}
	if e.b.phoneToLID("999") != "" || e.b.phoneToLID("") != "" {
		t.Error("unknown phone")
	}
}

// ---- _jid_to_phone (messages.JIDPhoneResolver) ----------------------------------------------------------

func TestJIDToPhone(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	cases := map[string]string{
		"393331234567@c.us":           "393331234567",
		"393331234567@s.whatsapp.net": "393331234567",
		"220988985864200@lid":         "393331234567",
		"123456789@g.us":              "",
		"not-a-jid":                   "",
		"":                            "",
		"999@lid":                     "",
		"status@broadcast":            "",
	}
	for in, want := range cases {
		if got := e.b.JIDToPhone(in); got != want {
			t.Errorf("JIDToPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- the device store (modernc.org/sqlite + sqlstore) ------------------------------------------------------

func TestOpenStoreAndBulkLIDImport(t *testing.T) {
	dir := t.TempDir()
	db, container, dev, err := openStore(context.Background(), filepath.Join(dir, "whatsmeow.db"), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if container == nil || dev == nil || dev.ID != nil {
		t.Fatalf("a fresh store yields an unpaired device: %+v", dev)
	}
	if _, err := db.Exec(`INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ('220988985864200','393331234567'), ('278255131766315','393339999999')`); err != nil {
		t.Fatal(err)
	}
	b := New(Options{DataDir: dir, LIDMapPath: filepath.Join(dir, "lid.json"), Logger: quietLogger()})
	b.db = db
	if n := b.lidBulkImport(context.Background()); n != 2 {
		t.Fatalf("imported %d", n)
	}
	if b.JIDToPhone("220988985864200@lid") != "393331234567" || b.JIDToPhone("278255131766315@lid") != "393339999999" {
		t.Error("imported mappings")
	}
	if n := b.lidBulkImport(context.Background()); n != 0 {
		t.Errorf("a second import changes nothing, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "lid.json")); err != nil {
		t.Error("the cache is saved once after the import")
	}
}

func TestNeedsPairingOnFreshStore(t *testing.T) {
	dir := t.TempDir()
	b := New(Options{DataDir: dir, Logger: quietLogger()})
	defer b.Close()
	if !b.NeedsPairing() {
		t.Fatal("a store without a device needs pairing")
	}
	if _, err := os.Stat(filepath.Join(dir, "whatsmeow.db")); err != nil {
		t.Errorf("the device store should live in the data dir: %v", err)
	}
	// Connect on an unpaired device is not an error: it waits for the QR flow.
	if err := b.Connect(context.Background()); err != nil || b.IsConnected() {
		t.Errorf("connect while unpaired: err=%v connected=%v", err, b.IsConnected())
	}
	if st, me, ok := b.SessionInfo(); st != "SCAN_QR_CODE" || me || !ok {
		t.Errorf("session info = %q %v %v", st, me, ok)
	}
}

// ---- list_address_book_sync merges (tests/test_address_book.py::TestWAMerge) ----------------------------------------

func TestAddressBookMergesBookAndChats(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	setContacts(e.b, chat("393331234567@c.us", "393331234567", 12345))
	got, _ := e.b.ListAddressBook(ctx, true)
	if len(got) != 1 {
		t.Fatalf("book = %+v", got)
	}
	c := got[0]
	if c.ID != "393331234567@c.us" || c.Extras["phone"] != "393331234567" || c.Extras["is_chat_active"] != true ||
		c.Extras["address_book"] != true || c.Extras["source"] != "wa_book" || c.LastMessageTS() != 12345 || c.DisplayName != "Mario Rossi" {
		t.Fatalf("merged contact: %+v", c)
	}
}

func TestAddressBookLIDRowKeepsAlias(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	e.book.put(types.NewJID("220988985864200", types.HiddenUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	got, _ := e.b.ListAddressBook(ctx, true)
	if len(got) != 1 || got[0].Extras["lid"] != "220988985864200@lid" || got[0].Extras["phone"] != "393331234567" || got[0].Extras["source"] != "wa_book" {
		t.Fatalf("book = %+v", got)
	}
}

func TestAddressBookChatNotInBookAndGroups(t *testing.T) {
	e := newEnv(t)
	setContacts(e.b, chat("393331111111@c.us", "Luigi", 1), chat("120363000000000000@g.us", "Famiglia", 5))
	got, _ := e.b.ListAddressBook(ctx, true)
	byID := map[string]proto.Contact{}
	for _, c := range got {
		byID[c.ID] = c
	}
	if c := byID["393331111111@c.us"]; c.Extras["phone"] != "393331111111" || c.Extras["is_chat_active"] != true || c.Extras["source"] != "wa_chats" {
		t.Errorf("chat not in book: %+v", c)
	}
	if c := byID["120363000000000000@g.us"]; c.Extras["source"] != "wa_chats" || c.Extras["address_book"] != true {
		t.Errorf("group: %+v", c)
	}
	// The group registry is durable and feeds find_contact.
	setContacts(e.b)
	if g, ok := e.b.FindContact("120363000000000000@g.us"); !ok || g.DisplayName != "Famiglia" {
		t.Errorf("known group lookup: %+v %v", g, ok)
	}
}

func TestAddressBookLIDResolvedFromCacheMergesAndUnresolvedStandalone(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	setContacts(e.b, chat("220988985864200@lid", "220988985864200@lid", 7), chat("77@lid", "77@lid", 3))
	got, _ := e.b.ListAddressBook(ctx, true)
	byID := map[string]proto.Contact{}
	for _, c := range got {
		byID[c.ID] = c
	}
	// The merged contact takes the id of the active chat (continuity with the cache).
	m, ok := byID["220988985864200@lid"]
	if !ok || m.DisplayName != "Mario Rossi" || m.Extras["phone"] != "393331234567" || m.Extras["lid"] != "220988985864200@lid" {
		t.Fatalf("merged lid chat: %+v", m)
	}
	if u := byID["77@lid"]; u.Extras["lid_unresolved"] != true {
		t.Errorf("unresolved lid must be standalone: %+v", u)
	}
	if _, dup := byID["393331234567@c.us"]; dup {
		t.Error("the book twin must not appear twice")
	}
}

func TestAddressBookEmptyFetchKeepsPreviousBookEntries(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	first, _ := e.b.ListAddressBook(ctx, true)
	if len(first) != 1 {
		t.Fatal(first)
	}
	e.book.clear() // a successful but entirely empty fetch
	second, _ := e.b.ListAddressBook(ctx, true)
	if len(second) != 1 || second[0].DisplayName != "Mario Rossi" {
		t.Fatalf("B3: an empty fetch never removes wa_book entries: %+v", second)
	}
}

func TestAddressBookTTL(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario"})
	e.b.ListAddressBook(ctx, true)
	e.book.put(types.NewJID("393339999999", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Luigi"})
	if got, _ := e.b.ListAddressBook(ctx, false); len(got) != 1 {
		t.Error("within the TTL the cached snapshot is served")
	}
	if got, _ := e.b.ListAddressBook(ctx, true); len(got) != 2 {
		t.Error("force bypasses the TTL")
	}
}

func TestFindAddressBookContactFallbackByPhone(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	setContacts(e.b, chat("220988985864200@lid", "x", 1))
	e.b.ListAddressBook(ctx, true)
	// The id was rewritten to @lid by the merge; the @c.us id still finds the book entry.
	c, ok := e.b.findAddressBookContact("393331234567@c.us")
	if !ok || c.ID != "220988985864200@lid" || c.DisplayName != "Mario Rossi" {
		t.Fatalf("find by phone: %+v %v", c, ok)
	}
	if _, ok := e.b.findAddressBookContact("+393331234567@c.us"); ok {
		t.Error("only the canonical <digits>@c.us id falls back")
	}
	// find_contact reaches the same chat through the alias.
	if c, ok := e.b.FindContact("393331234567@c.us"); !ok || c.ID != "220988985864200@lid" {
		t.Errorf("find_contact alias: %+v %v", c, ok)
	}
}

func TestLIDResolverRunAppliesNamesAndSignalsWarmup(t *testing.T) {
	e := newEnv(t)
	e.lids.put("1", "393331234567")
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	c1 := chat("1@lid", "1@lid", 1)
	c1.Extras["phone"] = "393331234567"
	setContacts(e.b, c1, chat("2@lid", "2@lid", 1))
	e.b.lidResolverRun()
	if c, _ := e.b.FindContact("1@lid"); c.DisplayName != "Mario Rossi" {
		t.Errorf("the book name must be re-applied, got %q", c.DisplayName)
	}
	evs := drain(e.b)
	if len(evs) == 0 || evs[len(evs)-1].Type != proto.EventLidWarmupDone {
		t.Fatalf("lid_warmup_done must close the run: %+v", evs)
	}
	// The unresolvable lid got a negative cache entry (retried after 24 h).
	if e, ok := lidCacheEntry(e.b, "2@lid"); !ok || e.Phone != "" {
		t.Error("negative entry expected")
	}
}

// ---- contact registry: aliases, fold, register ---------------------------------------------------------------

func TestRegisterContactFoldsIdentityTwins(t *testing.T) {
	e := newEnv(t)
	e.lids.put("220988985864200", "393331234567")
	// A ghost @c.us is registered first, then the active @lid chat of the same person arrives.
	ghost := newContact("393331234567@c.us", "Mario", map[string]any{"phone": "393331234567", "ghost": true})
	if !e.b.RegisterContact(*ghost) {
		t.Fatal("ghost should be appended")
	}
	active := newContact("220988985864200@lid", "220988985864200@lid", map[string]any{"source": "wa_chats", "is_chat_active": true, "lid": "220988985864200@lid", "last_message_ts": int64(9)})
	if !e.b.RegisterContact(*active) {
		t.Fatal("active chat should be appended")
	}
	list, _ := e.b.ListContacts(ctx)
	if len(list) != 1 || list[0].ID != "220988985864200@lid" {
		t.Fatalf("the active chat wins the fold: %+v", list)
	}
	if list[0].DisplayName != "Mario" || list[0].Extras["phone"] != "393331234567" {
		t.Errorf("metadata merged from the loser: %+v", list[0])
	}
	if c, ok := e.b.FindContact("393331234567@c.us"); !ok || c.ID != "220988985864200@lid" {
		t.Errorf("the loser id is an alias of the survivor: %+v %v", c, ok)
	}
	evs := drain(e.b)
	var merged *proto.Event
	for i := range evs {
		if evs[i].Type == proto.EventContactUpdate && evs[i].Payload["merged_into"] != nil {
			merged = &evs[i]
		}
	}
	if merged == nil || merged.ContactID != "393331234567@c.us" || merged.Payload["merged_into"] != "220988985864200@lid" {
		t.Fatalf("merged_into event: %+v", evs)
	}
	// Registering a known contact again is not an append.
	if e.b.RegisterContact(*active) {
		t.Error("known contact appended twice")
	}
}

func TestRegisterLosingIncomingIsAliasedToSurvivor(t *testing.T) {
	e := newEnv(t)
	e.lids.put("1", "393331234567")
	active := newContact("393331234567@c.us", "Mario", map[string]any{"source": "wa_chats", "is_chat_active": true, "phone": "393331234567", "last_message_ts": int64(50)})
	e.b.RegisterContact(*active)
	ghost := newContact("1@lid", "1@lid", map[string]any{"lid": "1@lid", "ghost": true})
	if e.b.RegisterContact(*ghost) {
		t.Fatal("the losing twin must not be appended")
	}
	list, _ := e.b.ListContacts(ctx)
	if len(list) != 1 || list[0].ID != "393331234567@c.us" {
		t.Fatalf("list = %+v", list)
	}
	if c, ok := e.b.FindContact("1@lid"); !ok || c.ID != "393331234567@c.us" {
		t.Errorf("lid alias: %+v %v", c, ok)
	}
}

func TestContactStorageKeys(t *testing.T) {
	c := func(id string, ex map[string]any) proto.Contact { return *newContact(id, "x", ex) }
	cases := []struct {
		c    proto.Contact
		want []string
	}{
		{c("120363@g.us", map[string]any{"phone": "1"}), []string{"120363@g.us"}},
		{c("1@lid", map[string]any{"phone": "393331234567"}), []string{"1@lid", "393331234567@c.us"}},
		{c("393331234567@c.us", map[string]any{"phone": "393331234567", "lid": "1@lid"}), []string{"393331234567@c.us", "1@lid"}},
		{c("1@lid", nil), []string{"1@lid"}},
		{c("nodomain", nil), []string{"nodomain"}},
	}
	for _, tc := range cases {
		got := contactStorageKeys(tc.c)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %v want %v", tc.c.ID, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: %v want %v", tc.c.ID, got, tc.want)
			}
		}
	}
}

func TestApplyContactLIDResolution(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(*newContact("205@lid", "205@lid", map[string]any{"source": "wa_chats", "is_chat_active": true}))
	drain(e.b)
	e.lids.put("205", "393331112222")
	e.b.scheduleContactLIDResolve("205@lid")
	c, _ := e.b.FindContact("205@lid")
	if c.Extras["phone"] != "393331112222" || c.DisplayName != "+393331112222" {
		t.Fatalf("resolved contact: %+v", c)
	}
	evs := drain(e.b)
	if len(evs) == 0 || evs[0].Type != proto.EventContactUpdate || evs[0].Payload["phone"] != "393331112222" {
		t.Fatalf("contact_update: %+v", evs)
	}
	// A readable name is not overwritten.
	e.b.cmu.Lock()
	e.b.byJID["205@lid"].DisplayName = "Anna"
	e.b.cmu.Unlock()
	e.b.scheduleContactLIDResolve("205@lid")
	if c, _ := e.b.FindContact("205@lid"); c.DisplayName != "Anna" {
		t.Errorf("name overwritten: %q", c.DisplayName)
	}
}

func TestPlaceholderName(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	e.b.ListAddressBook(ctx, true)
	if got := e.b.PlaceholderName("393331234567@c.us"); got != "Mario Rossi" {
		t.Errorf("placeholder by phone = %q", got)
	}
	if got := e.b.PlaceholderName("555@c.us"); got != "" {
		t.Errorf("unknown = %q", got)
	}
}

// ---- load / refresh ------------------------------------------------------------------------------------------

func TestLoadContactsFromCacheGroupsAndContactTable(t *testing.T) {
	e := newEnv(t)
	e.fc.joined = []*types.GroupInfo{{JID: types.NewJID("120363000000000000", types.GroupServer), GroupName: types.GroupName{Name: "Famiglia"}}}
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	e.b.IngestMessage(ctx, "393331234567@c.us", map[string]any{"id": "false_393331234567@c.us_A1", "text": "ciao", "is_mine": false}, 4_000)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	list, _ := e.b.ListContacts(ctx)
	byID := map[string]proto.Contact{}
	for _, c := range list {
		byID[c.ID] = c
	}
	m := byID["393331234567@c.us"]
	if m.DisplayName != "Mario Rossi" || m.Extras["phone"] != "393331234567" || m.LastMessageTS() != 4000 {
		t.Errorf("chat contact: %+v", m)
	}
	if g := byID["120363000000000000@g.us"]; g.DisplayName != "Famiglia" {
		t.Errorf("group contact: %+v", g)
	}
}

func TestLoadContactsLIDWithoutPhone(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "139153@lid", map[string]any{"id": "false_139153@lid_A1", "text": "x", "is_mine": false}, 1)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	c, ok := e.b.FindContact("139153@lid")
	if !ok || c.Extras["lid"] != "139153@lid" {
		t.Fatalf("lid contact: %+v", c)
	}
	if _, has := c.Extras["phone"]; has {
		t.Error("an unresolved lid has no phone")
	}
}

func TestChatDisplayFallbackOnlyRewritesLID(t *testing.T) {
	for id, want := range map[string]string{
		"99326135144475@lid":      "Contatto WhatsApp (ID 99326135144475)",
		"@lid":                    "@lid", // no local part: nothing readable to show
		"393331234567@c.us":       "393331234567@c.us",
		"120363000000000000@g.us": "120363000000000000@g.us",
		"":                        "",
	} {
		if got := chatDisplayFallback(id); got != want {
			t.Errorf("chatDisplayFallback(%q) = %q, want %q", id, got, want)
		}
	}
}

// An unresolved @lid nobody can name (no LID map entry, no phone-keyed contact,
// no device contact for the LID JID) must not reach the wire as the raw
// "<n>@lid" identifier nor as bare LID digits, which read like a phone number
// the LID is not: it renders as a clearly marked unresolved name.
func TestLoadContactsUnresolvedLIDShowsAnnotatedFallback(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "99326135144475@lid", map[string]any{"id": "false_99326135144475@lid_A1", "text": "x", "is_mine": false}, 1)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	c, ok := e.b.FindContact("99326135144475@lid")
	if !ok || c.Extras["lid"] != "99326135144475@lid" {
		t.Fatalf("lid contact: %+v", c)
	}
	want := "Contatto WhatsApp (ID 99326135144475)"
	if c.DisplayName != want {
		t.Errorf("an unresolved lid renders as %q, want %q", c.DisplayName, want)
	}
	// The address book keeps the same annotated name and flags it as unresolved.
	book, _ := e.b.ListAddressBook(ctx, true)
	for _, b := range book {
		if b.ID == "99326135144475@lid" && (b.DisplayName != want || b.Extras["lid_unresolved"] != true) {
			t.Errorf("address book entry: %+v", b)
		}
	}
	// The annotated name is a placeholder, not a name: once the mapping and the
	// rubrica name arrive the very next load replaces it.
	e.lids.put("99326135144475", "393331234567")
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Praful Shankar"})
	e.b.loadContacts(ctx)
	if c, ok := e.b.FindContact("99326135144475@lid"); !ok || c.DisplayName != "Praful Shankar" || c.Extras["phone"] != "393331234567" {
		t.Fatalf("the annotated fallback must stay replaceable: %+v ok=%v", c, ok)
	}
}

// A LID whose phone mapping is known but that has no name still shows the
// phone: the annotated fallback never hides a usable number.
func TestLoadContactsMappedLIDWithoutNameShowsPhone(t *testing.T) {
	e := newEnv(t)
	e.lids.put("99326135144475", "393331234567")
	e.b.IngestMessage(ctx, "99326135144475@lid", map[string]any{"id": "false_99326135144475@lid_A1", "text": "x", "is_mine": false}, 1)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	if c, ok := e.b.FindContact("99326135144475@lid"); !ok || c.DisplayName != "+393331234567" {
		t.Fatalf("a mapped lid without a name shows the phone: %+v ok=%v", c, ok)
	}
}

// The device contact for the LID JID carries the push/full name even when
// nothing maps the LID to a phone: that name beats the annotated fallback. A row
// that only carries a push name is usable too, the same rule rawAddressBook
// applies to the address book rows.
func TestLoadContactsLIDTakesDeviceContactName(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("99326135144475", types.HiddenUserServer), types.ContactInfo{PushName: "Praful"})
	if got := e.b.nameForChat(ctx, "99326135144475@lid"); got != "Praful" {
		t.Fatalf("nameForChat = %q, want the device push name", got)
	}
	e.b.IngestMessage(ctx, "99326135144475@lid", map[string]any{"id": "false_99326135144475@lid_A1", "text": "x", "is_mine": false}, 1)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	if c, ok := e.b.FindContact("99326135144475@lid"); !ok || c.DisplayName != "Praful" {
		t.Fatalf("the device contact name must win over the annotated fallback: %+v ok=%v", c, ok)
	}
}

// A registry name that is just the raw id (seeded before the contact row
// existed) is retried on the next refresh instead of sticking forever.
func TestRefreshChatRegistryNamesChatsStuckOnTheirRawID(t *testing.T) {
	e := newEnv(t)
	e.book.put(types.NewJID("99326135144475", types.HiddenUserServer), types.ContactInfo{Found: true, FullName: "Praful Shankar"})
	e.b.noteChat("99326135144475@lid", 1, "99326135144475@lid")
	if err := e.b.refreshChatRegistry(ctx, false); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rows := e.b.chatRows(); len(rows) != 1 || rows[0].Name != "Praful Shankar" {
		t.Fatalf("chat rows: %+v", rows)
	}
}

func TestRefreshContactsReportsNewAndRenamed(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "393331234567@c.us", map[string]any{"id": "false_393331234567@c.us_A1", "text": "x", "is_mine": false}, 1)
	e.b.loadProtocolCache(ctx)
	e.b.loadContacts(ctx)
	drain(e.b)
	// A new chat and a new saved name appear.
	e.b.IngestMessage(ctx, "393339999999@c.us", map[string]any{"id": "false_393339999999@c.us_A2", "text": "y", "is_mine": false}, 2)
	e.b.loadProtocolCache(ctx)
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	res := e.b.RefreshContacts(ctx, true)
	if res.Errors != "" || res.Protocol != "whatsapp" || len(res.NewContacts) != 1 || res.NewContacts[0].ID != "393339999999@c.us" ||
		len(res.RenamedContacts) != 1 || res.RenamedContacts[0].DisplayName != "Mario Rossi" {
		t.Fatalf("refresh result: %+v", res)
	}
	var updates int
	for _, ev := range e.b.PollOnce() {
		switch ev.Type {
		case proto.EventContactUpdate:
			updates++
		case proto.EventLidWarmupDone: // the background resolver's own signal
		default:
			t.Errorf("unexpected event %+v", ev)
		}
	}
	if updates != 2 {
		t.Errorf("one contact_update per new/renamed contact, got %d", updates)
	}
	e.b.connected.Store(false)
	if res := e.b.RefreshContacts(ctx, true); res.Errors != "not connected" {
		t.Errorf("not connected: %+v", res)
	}
}

func TestAnonymousContactsHaveNoSideEffects(t *testing.T) {
	e := newEnv(t)
	if got, _ := e.b.ListContacts(ctx); len(got) != 0 {
		t.Error("no contacts yet")
	}
	if _, ok := e.b.FindContact("1@c.us"); ok {
		t.Error("unknown contact")
	}
}

// waitLidWarmups drains events until want lid_warmup_done events arrived.
func waitLidWarmups(t *testing.T, e *env, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	got := 0
	for got < want && time.Now().Before(deadline) {
		for _, ev := range e.b.PollOnce() {
			if ev.Type == proto.EventLidWarmupDone {
				got++
			}
		}
		if got < want {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got != want {
		t.Fatalf("lid_warmup_done events = %d, want %d", got, want)
	}
}

func TestLIDResolverRerunsAfterThrottleAndAppliesBookName(t *testing.T) {
	e := newEnv(t)
	e.lids.put("900", "393331234567")
	e.lids.put("902", "393331110000")
	e.book.put(types.NewJID("393331234567", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Mario Rossi"})
	e.book.put(types.NewJID("393331110000", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Anna Bianchi"})
	// The name snapshot applyContactLIDResolution reads.
	e.b.listAddressBook(ctx, true, false)
	setContacts(e.b, chat("900@lid", "900@lid", 1))

	e.b.StartLIDResolver()
	waitLidWarmups(t, e, 1)
	if c, _ := e.b.FindContact("900@lid"); c.Extras["phone"] != "393331234567" || c.DisplayName != "Mario Rossi" {
		t.Fatalf("first run must resolve the known mapping: %+v", c)
	}
	// Inside the throttle window the trigger is a no-op.
	e.b.StartLIDResolver()
	time.Sleep(30 * time.Millisecond)
	if evs := drain(e.b); len(evs) != 0 {
		t.Fatalf("a throttled run must not emit events: %+v", evs)
	}
	// Once the window elapsed, the resolver runs again and applies the links
	// learned in the meantime (this is the "<n>@lid" name bug).
	e.b.lidLastRunMu.Lock()
	e.b.lidLastRun = nowFunc().Add(-lidResolveInterval - time.Second)
	e.b.lidLastRunMu.Unlock()
	setContacts(e.b, chat("900@lid", "900@lid", 1), chat("902@lid", "902@lid", 1))
	e.b.StartLIDResolver()
	waitLidWarmups(t, e, 1)
	c, ok := e.b.FindContact("902@lid")
	if !ok || c.Extras["phone"] != "393331110000" || c.Extras["lid"] != "902@lid" || c.DisplayName != "Anna Bianchi" {
		t.Fatalf("second run must apply the learned mapping and the book name: %+v ok=%v", c, ok)
	}
}

// A contact that already carries the phone extra (RefreshContacts fills it
// from the device table) but still shows the raw "<n>@lid" name must get the
// address-book or "+phone" name from a resolver pass.
func TestLIDResolverNamesAlreadyPhonedContacts(t *testing.T) {
	e := newEnv(t)
	e.lids.put("902", "393331110000")
	e.lids.put("903", "393331110001")
	e.book.put(types.NewJID("393331110000", types.DefaultUserServer), types.ContactInfo{Found: true, FullName: "Anna Bianchi"})
	e.b.listAddressBook(ctx, true, false)
	setContacts(e.b,
		newContact("902@lid", "902@lid", map[string]any{"phone": "393331110000", "lid": "902@lid", "source": "wa_chats"}),
		newContact("903@lid", "903@lid", map[string]any{"phone": "393331110001", "lid": "903@lid", "source": "wa_chats"}))
	e.b.lidResolveCandidates()
	if c, _ := e.b.FindContact("902@lid"); c.DisplayName != "Anna Bianchi" {
		t.Errorf("the mapped contact must take the address-book name, got %q", c.DisplayName)
	}
	// No book row: the phone fallback, never the raw lid.
	if c, _ := e.b.FindContact("903@lid"); c.DisplayName != "+393331110001" {
		t.Errorf("the phone fallback must replace the raw lid, got %q", c.DisplayName)
	}
	// Idempotent: a second pass does not rename or duplicate.
	c, _ := e.b.FindContact("902@lid")
	e.b.lidResolveCandidates()
	if again, _ := e.b.FindContact("902@lid"); again.DisplayName != c.DisplayName || again.Extras["phone"] != "393331110000" {
		t.Errorf("second pass changed the contact: %+v -> %+v", c, again)
	}
}

// The same for the register path: a contact carrying the annotated fallback
// whose mapping is already known gets the phone applied immediately.
func TestScheduleContactLIDResolveReplacesAnnotatedFallback(t *testing.T) {
	e := newEnv(t)
	e.lids.put("905", "393331110003")
	setContacts(e.b,
		newContact("905@lid", "Contatto WhatsApp (ID 905)", map[string]any{"lid": "905@lid", "source": "wa_chats"}))
	e.b.scheduleContactLIDResolve("905@lid")
	if c, _ := e.b.FindContact("905@lid"); c.DisplayName != "+393331110003" {
		t.Errorf("the annotated fallback must not block the resolution, got %q", c.DisplayName)
	}
}

// The annotated unresolved name is a placeholder too: it must not freeze a
// contact that later gains its phone mapping.
func TestLIDResolverReplacesAnnotatedFallback(t *testing.T) {
	e := newEnv(t)
	e.lids.put("904", "393331110002")
	setContacts(e.b,
		newContact("904@lid", "Contatto WhatsApp (ID 904)", map[string]any{"phone": "393331110002", "lid": "904@lid", "source": "wa_chats"}))
	e.b.lidResolveCandidates()
	if c, _ := e.b.FindContact("904@lid"); c.DisplayName != "+393331110002" {
		t.Errorf("the annotated fallback must stay replaceable, got %q", c.DisplayName)
	}
}
