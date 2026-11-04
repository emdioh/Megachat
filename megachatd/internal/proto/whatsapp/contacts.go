package whatsapp

import (
	"context"
	"sort"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/types"
)

// ---- contact helpers ----------------------------------------------------------------

func exStr(c *proto.Contact, key string) string {
	if c == nil {
		return ""
	}
	switch v := c.Extras[key].(type) {
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
	}
	return ""
}

func exBool(c *proto.Contact, key string) bool { return c != nil && truthy(c.Extras[key]) }

func lastTS(c *proto.Contact) int64 { return c.LastMessageTS() }

func setLastTS(c *proto.Contact, ts int64) {
	if c.Extras == nil {
		c.Extras = map[string]any{}
	}
	c.Extras["last_message_ts"] = ts
}

func newContact(id, name string, extras map[string]any) *proto.Contact {
	if extras == nil {
		extras = map[string]any{}
	}
	return &proto.Contact{ID: id, DisplayName: name, Protocol: proto.ProtocolWhatsApp, Extras: extras}
}

func cloneContact(c *proto.Contact) proto.Contact {
	out := *c
	out.Extras = make(map[string]any, len(c.Extras))
	for k, v := range c.Extras {
		out.Extras[k] = v
	}
	return out
}

func cloneContacts(in []*proto.Contact) []proto.Contact {
	out := make([]proto.Contact, len(in))
	for i, c := range in {
		out[i] = cloneContact(c)
	}
	return out
}

func copyContactWith(c proto.Contact, extra map[string]any) proto.Contact {
	out := c
	out.Extras = make(map[string]any, len(c.Extras)+len(extra))
	for k, v := range c.Extras {
		out.Extras[k] = v
	}
	for k, v := range extra {
		out.Extras[k] = v
	}
	return out
}

// contactStorageKeys is models.contact_storage_keys: every messages.contact_number
// of a contact (canonical id first, then its @c.us / @lid twins).
func contactStorageKeys(c proto.Contact) []string {
	cid := c.ID
	if cid == "" || !strings.Contains(cid, "@") || strings.HasSuffix(cid, "@g.us") {
		return []string{cid}
	}
	keys := []string{cid}
	lid := exStr(&c, "lid")
	phone := exStr(&c, "phone")
	if strings.HasSuffix(cid, "@lid") {
		if phone != "" {
			keys = append(keys, phone+"@c.us")
		}
	} else {
		if lid != "" && strings.HasSuffix(lid, "@lid") {
			keys = append(keys, lid)
		}
		if phone != "" && !strings.HasSuffix(cid, "@c.us") && !containsStr(keys, phone+"@c.us") {
			keys = append(keys, phone+"@c.us")
		}
	}
	var out []string
	for _, k := range keys {
		if !containsStr(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// looksLikePhone is _looks_like_phone: name is the digit-only fallback of phone.
func looksLikePhone(name, phone string) bool {
	if name == "" || phone == "" {
		return false
	}
	nd, pd := jidDigits(name), jidDigits(phone)
	return nd != "" && pd != "" && nd == pd
}

// isPlaceholderDisplayName is _is_placeholder_display_name: a replaceable
// fallback, never a real name.
func isPlaceholderDisplayName(display, contactID, phone string) bool {
	if display == "" || display == contactID {
		return true
	}
	if strings.HasSuffix(contactID, "@lid") {
		if head, _, _ := strings.Cut(contactID, "@"); display == head {
			return true
		}
	}
	if phone == "" {
		phone = jidDigits(contactID)
	}
	return looksLikePhone(display, phone)
}

// ---- device stores -------------------------------------------------------------------

// contactStore is the contact table of the device store (store.ContactStore, a subset).
type contactStore interface {
	GetContact(ctx context.Context, user types.JID) (types.ContactInfo, error)
	GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error)
}

func (b *Backend) deviceContacts() contactStore {
	if b.contactStoreForTest != nil {
		return b.contactStoreForTest
	}
	cli := b.currentClient()
	if cli == nil {
		return nil
	}
	if d := cli.Device(); d != nil && d.Contacts != nil {
		return d.Contacts
	}
	return nil
}

// attachStores is a hook for connect: nothing to cache, the stores are read
// through the device on demand.
func (b *Backend) attachStores(cli waClient) {}

// contactBestName is the display name whatsmeow knows for a user JID: the saved
// (address book) name first, then the push name.
func contactBestName(ci types.ContactInfo) string {
	for _, n := range []string{ci.FullName, ci.FirstName, ci.PushName, ci.BusinessName} {
		if strings.TrimSpace(n) != "" {
			return n
		}
	}
	return ""
}

// nameForChat looks a chat name up in the contact table (zero network).
func (b *Backend) nameForChat(ctx context.Context, wahaID string) string {
	cs := b.deviceContacts()
	if cs == nil || strings.HasSuffix(wahaID, "@g.us") {
		return ""
	}
	try := func(j types.JID) string {
		if ci, err := cs.GetContact(ctx, j); err == nil && ci.Found {
			return contactBestName(ci)
		}
		return ""
	}
	j, err := JIDFromWAHA(wahaID)
	if err != nil {
		return ""
	}
	if n := try(j); n != "" {
		return n
	}
	if strings.HasSuffix(wahaID, "@lid") {
		if phone := b.lidLookup(wahaID); phone != "" {
			return try(types.NewJID(phone, types.DefaultUserServer))
		}
	} else if lid := b.phoneToLID(jidDigits(wahaID)); lid != "" {
		if lj, err := JIDFromWAHA(lid); err == nil {
			return try(lj)
		}
	}
	return ""
}

// ---- chat registry (the WAHA /chats replacement) -----------------------------------------

// noteChat records activity in a chat (called under no lock).
func (b *Backend) noteChat(chat string, ts int64, name string) {
	if chat == "" || isBroadcastJID(chat) {
		return
	}
	b.cmu.Lock()
	m := b.chats[chat]
	if m == nil {
		m = &chatMeta{}
		b.chats[chat] = m
	}
	if ts > m.LastTS {
		m.LastTS = ts
	}
	if name != "" && (m.Name == "" || m.Name == chat) {
		m.Name = name
	}
	b.cmu.Unlock()
}

// refreshChatRegistry rebuilds the chat registry from the message cache, the
// contact table and (when online) the joined groups.
func (b *Backend) refreshChatRegistry(ctx context.Context, online bool) error {
	b.ingestMu.Lock()
	last := map[string]int64{}
	for c, msgs := range b.cache {
		var m int64
		for _, e := range msgs {
			if e.Timestamp > m {
				m = e.Timestamp
			}
		}
		last[c] = m
	}
	b.ingestMu.Unlock()
	for c, ts := range last {
		if isBroadcastJID(c) || !strings.Contains(c, "@") {
			continue
		}
		b.noteChat(c, ts, "")
	}

	var gerr error
	if online {
		if cli := b.currentClient(); cli != nil && b.connected.Load() {
			groups, err := cli.GetJoinedGroups(ctx)
			if err != nil {
				gerr = err
			}
			for _, g := range groups {
				id := WAHAFromJID(g.JID)
				b.noteChat(id, 0, g.Name)
				b.cmu.Lock()
				if m := b.chats[id]; m != nil && g.Name != "" {
					m.Name = g.Name
				}
				b.cmu.Unlock()
			}
		}
	}
	// Names from the contact table for chats that have none yet.
	b.cmu.RLock()
	var need []string
	for id, m := range b.chats {
		if m.Name == "" {
			need = append(need, id)
		}
	}
	b.cmu.RUnlock()
	for _, id := range need {
		if n := b.nameForChat(ctx, id); n != "" {
			b.cmu.Lock()
			if m := b.chats[id]; m != nil && m.Name == "" {
				m.Name = n
			}
			b.cmu.Unlock()
		}
	}
	return gerr
}

// chatRow is a normalised chat (rest._normalize_chat).
type chatRow struct {
	ID       string
	Name     string
	LastTS   int64
	Archived bool
	Unread   int
}

func (b *Backend) chatRows() []chatRow {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	rows := make([]chatRow, 0, len(b.chats))
	for id, m := range b.chats {
		name := m.Name
		if name == "" {
			name = id
		}
		rows = append(rows, chatRow{ID: id, Name: name, LastTS: m.LastTS, Archived: m.Archived, Unread: m.Unread})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastTS != rows[j].LastTS {
			return rows[i].LastTS > rows[j].LastTS
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

// ---- contacts -------------------------------------------------------------------------

// contactPhone is _contact_phone / _jid_to_phone for the contact loader.
func (b *Backend) contactPhone(jid string) string { return b.JIDToPhone(jid) }

// buildContactsFromChats is _build_contacts_from_raw.
func (b *Backend) buildContactsFromChats(rows []chatRow) []*proto.Contact {
	var out []*proto.Contact
	for _, r := range rows {
		if r.ID == "" || isBroadcastJID(r.ID) {
			continue
		}
		extras := map[string]any{
			"jid":             r.ID,
			"last_message_ts": r.LastTS,
			"source":          "wa_chats",
			"is_chat_active":  true,
			"archived":        r.Archived,
		}
		if phone := b.contactPhone(r.ID); phone != "" {
			extras["phone"] = phone
		}
		if strings.HasSuffix(r.ID, "@lid") {
			extras["lid"] = r.ID
		}
		name := r.Name
		if name == "" {
			name = r.ID
			if p := exStr(&proto.Contact{Extras: extras}, "phone"); p != "" {
				name = "+" + p
			}
		}
		out = append(out, newContact(r.ID, name, extras))
	}
	return out
}

// loadContacts is _load_contacts.
func (b *Backend) loadContacts(ctx context.Context) {
	b.lidCacheLoad()
	if err := b.refreshChatRegistry(ctx, true); err != nil {
		b.log.Debug("WhatsApp joined groups unavailable", "err", err)
	}
	fresh := b.buildContactsFromChats(b.chatRows())
	b.cmu.Lock()
	b.contacts = fresh
	merged := b.foldContactsLocked()
	b.rebuildAliasesLocked()
	b.cmu.Unlock()
	for _, m := range merged {
		b.enqueueEvent(b.mergedContactEvent(m.loser, m.winner))
	}
	book := b.listAddressBook(ctx, false, true)
	b.cmu.Lock()
	applyAddressBookNames(b.contacts, buildAddressBookNameMap(book))
	b.cmu.Unlock()
}

// ListContacts implements proto.Backend.
func (b *Backend) ListContacts(ctx context.Context) ([]proto.Contact, error) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	return cloneContacts(b.contacts), nil
}

// ---- identity fold (@c.us <-> @lid) -----------------------------------------------------

type mergedPair struct{ loser, winner *proto.Contact }

// contactDedupPhone is _contact_dedup_phone: the phone used for the identity
// fold, from the JID itself (never extras["phone"]).
func (b *Backend) contactDedupPhone(c *proto.Contact) string {
	cid := c.ID
	if cid == "" || !strings.Contains(cid, "@") || strings.HasSuffix(cid, "@g.us") {
		return ""
	}
	if strings.HasSuffix(cid, "@c.us") || strings.HasSuffix(cid, "@s.whatsapp.net") {
		return jidDigits(cid)
	}
	if strings.HasSuffix(cid, "@lid") {
		return b.lidLookup(cid)
	}
	return ""
}

// foldMatchKeys is _fold_match_keys.
func (b *Backend) foldMatchKeys(c *proto.Contact) map[string]bool {
	keys := map[string]bool{}
	if p := b.contactDedupPhone(c); p != "" {
		keys[p] = true
	}
	if l := exStr(c, "lid"); l != "" {
		keys[l] = true
	}
	if strings.HasSuffix(c.ID, "@lid") {
		keys[c.ID] = true
	}
	return keys
}

func keysIntersect(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func isActiveChat(c *proto.Contact) bool {
	return exBool(c, "is_chat_active") || exStr(c, "source") == "wa_chats"
}

// winner is _winner: strict precedence (1) active chat, (2) newer last
// message, (3) has cached history, (4) @lid, (5) a.
func (b *Backend) winner(a, c *proto.Contact) *proto.Contact {
	aa, ca := isActiveChat(a), isActiveChat(c)
	if aa != ca {
		if aa {
			return a
		}
		return c
	}
	if lastTS(a) != lastTS(c) {
		if lastTS(a) > lastTS(c) {
			return a
		}
		return c
	}
	b.ingestMu.Lock()
	_, ac := b.cache[a.ID]
	_, cc := b.cache[c.ID]
	b.ingestMu.Unlock()
	if ac != cc {
		if ac {
			return a
		}
		return c
	}
	al, cl := strings.HasSuffix(a.ID, "@lid"), strings.HasSuffix(c.ID, "@lid")
	if al != cl {
		if al {
			return a
		}
		return c
	}
	return a
}

// foldByIdentity is _fold_by_identity: present twins of incoming.
func (b *Backend) foldByIdentity(incoming *proto.Contact) []*proto.Contact {
	keys := b.foldMatchKeys(incoming)
	if len(keys) == 0 {
		return nil
	}
	var out []*proto.Contact
	for _, c := range b.contacts {
		if c.ID != incoming.ID && keysIntersect(b.foldMatchKeys(c), keys) {
			out = append(out, c)
		}
	}
	return out
}

// mergeContactMetadata is _merge_contact_metadata (never copies "ghost").
func mergeContactMetadata(target, source *proto.Contact) {
	if isPlaceholderDisplayName(target.DisplayName, target.ID, exStr(target, "phone")) {
		if sn := source.DisplayName; sn != "" && !isPlaceholderDisplayName(sn, source.ID, exStr(source, "phone")) {
			target.DisplayName = sn
		}
	}
	if target.Extras == nil {
		target.Extras = map[string]any{}
	}
	for _, k := range []string{"phone", "lid"} {
		if exStr(target, k) == "" && exStr(source, k) != "" {
			target.Extras[k] = source.Extras[k]
		}
	}
	if lastTS(source) > lastTS(target) {
		setLastTS(target, lastTS(source))
	}
	// A chat is one identity across its @c.us/@lid twins: if either was
	// archived, the merge keeps it archived (a lost flag moved archived chats
	// back to the main list).
	if exBool(source, "archived") && !exBool(target, "archived") {
		target.Extras["archived"] = true
	}
}

// repointLocked is _repoint_contact_locked.
func (b *Backend) repointLocked(removed, survivor *proto.Contact) {
	if removed != survivor {
		kept := b.contacts[:0:0]
		for _, c := range b.contacts {
			if c != removed {
				kept = append(kept, c)
			}
		}
		b.contacts = kept
	}
	for k, v := range b.byJID {
		if v == removed {
			b.byJID[k] = survivor
		}
	}
}

// mergedContactEvent is _merged_contact_event.
func (b *Backend) mergedContactEvent(removed, survivor *proto.Contact) proto.Event {
	var phone any
	if p := exStr(survivor, "phone"); p != "" {
		phone = p
	}
	return proto.Event{Type: proto.EventContactUpdate, Protocol: proto.ProtocolWhatsApp, ContactID: removed.ID, Payload: map[string]any{
		"merged_into": survivor.ID, "contact": cloneContact(survivor),
		"display_name": survivor.DisplayName, "phone": phone,
	}}
}

// foldContactsLocked is _fold_contacts_locked: the post-pass identity fold.
func (b *Backend) foldContactsLocked() []mergedPair {
	var merged []mergedPair
	var survivors []*proto.Contact
	for _, contact := range append([]*proto.Contact(nil), b.contacts...) {
		keys := b.foldMatchKeys(contact)
		var existing, win *proto.Contact
		if len(keys) > 0 {
			for _, ex := range survivors {
				if keysIntersect(b.foldMatchKeys(ex), keys) {
					existing = ex
					win = b.winner(ex, contact)
					break
				}
			}
		}
		if win == nil {
			survivors = append(survivors, contact)
			continue
		}
		loser := existing
		if win == existing {
			loser = contact
		} else {
			kept := survivors[:0:0]
			for _, s := range survivors {
				if s != existing {
					kept = append(kept, s)
				}
			}
			survivors = append(kept, contact)
		}
		mergeContactMetadata(win, loser)
		merged = append(merged, mergedPair{loser, win})
	}
	if len(merged) > 0 {
		b.contacts = survivors
		for _, m := range merged {
			b.repointLocked(m.loser, m.winner)
		}
	}
	return merged
}

// rebuildAliasesLocked is _rebuild_jid_aliases: byJID = {id: contact} plus the
// bidirectional @lid <-> @c.us aliases (no orphan entries).
func (b *Backend) rebuildAliasesLocked() {
	b.byJID = make(map[string]*proto.Contact, len(b.contacts))
	for _, c := range b.contacts {
		b.byJID[c.ID] = c
	}
	for _, c := range b.contacts {
		switch {
		case strings.HasSuffix(c.ID, "@c.us") || strings.HasSuffix(c.ID, "@s.whatsapp.net"):
			b.registerLIDAliasLocked(c)
		case strings.HasSuffix(c.ID, "@lid"):
			phone := exStr(c, "phone")
			if phone == "" {
				phone = b.lidLookup(c.ID)
			}
			if phone != "" {
				if _, ok := b.byJID[phone+"@c.us"]; !ok {
					b.byJID[phone+"@c.us"] = c
				}
			}
		}
	}
}

// registerLIDAliasLocked is _register_lid_alias: lid -> the @c.us contact.
func (b *Backend) registerLIDAliasLocked(c *proto.Contact) {
	if !strings.HasSuffix(c.ID, "@c.us") {
		return
	}
	phone := exStr(c, "phone")
	if phone == "" {
		return
	}
	if lid := b.phoneToLID(phone); lid != "" {
		if _, ok := b.byJID[lid]; !ok {
			b.byJID[lid] = c
		}
	}
}

// identify is _identify_contact: a known contact for the id, or nil.
func (b *Backend) identify(jid string) *proto.Contact {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	return b.byJID[jid]
}

// FindContact implements proto.ContactFinder (find_contact): exact id in the
// list, the bidirectional alias, the address book, and finally the known
// groups. Memory only.
func (b *Backend) FindContact(contactID string) (proto.Contact, bool) {
	b.cmu.RLock()
	for _, c := range b.contacts {
		if c.ID == contactID {
			out := cloneContact(c)
			b.cmu.RUnlock()
			return out, true
		}
	}
	if c := b.byJID[contactID]; c != nil {
		for _, x := range b.contacts {
			if x == c {
				out := cloneContact(c)
				b.cmu.RUnlock()
				return out, true
			}
		}
	}
	b.cmu.RUnlock()
	if c, ok := b.findAddressBookContact(contactID); ok {
		return c, true
	}
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	if g := b.knownGroups[contactID]; g != nil {
		return cloneContact(g), true
	}
	return proto.Contact{}, false
}

// PlaceholderName implements core.PlaceholderNamer (resolve_placeholder_name):
// the cached address-book name for an unknown id, "" when none.
func (b *Backend) PlaceholderName(contactID string) string {
	var phone, lid string
	if strings.HasSuffix(contactID, "@lid") {
		lid = contactID
		b.lidMu.Lock()
		b.lidCacheLoadLocked()
		phone = b.lidMap[contactID].Phone
		b.lidMu.Unlock()
	} else if strings.Contains(contactID, "@") {
		phone = jidDigits(strings.SplitN(contactID, "@", 2)[0])
	}
	b.cmu.RLock()
	book := b.addressBook
	b.cmu.RUnlock()
	return cachedAddressBookName(book, phone, lid)
}

// RegisterContact implements core.ContactRegistrar (register_contact):
// open-or-create with the @c.us <-> @lid identity fold. It reports whether the
// contact was appended.
func (b *Backend) RegisterContact(contact proto.Contact) bool {
	b.lidCacheLoad()
	c := cloneContact(&contact)
	cp := &c
	var events []proto.Event
	appended := false
	b.cmu.Lock()
	known := false
	for _, ex := range b.contacts {
		if ex.CacheKey() == cp.CacheKey() {
			known = true
			break
		}
	}
	if known {
		b.registerLIDAliasLocked(cp)
	} else {
		losers := b.foldByIdentity(cp)
		var losing *proto.Contact
		for _, l := range losers {
			if b.winner(cp, l) == l {
				losing = l
				break
			}
		}
		if losing != nil {
			b.repointLocked(cp, losing)
			b.byJID[cp.ID] = losing
			mergeContactMetadata(losing, cp)
			events = append(events, b.mergedContactEvent(cp, losing))
		} else {
			for _, l := range losers {
				b.repointLocked(l, cp)
				b.byJID[l.ID] = cp
				mergeContactMetadata(cp, l)
				events = append(events, b.mergedContactEvent(l, cp))
			}
			b.contacts = append(b.contacts, cp)
			b.byJID[cp.ID] = cp
			appended = true
		}
		b.registerLIDAliasLocked(cp)
	}
	b.cmu.Unlock()
	b.scheduleContactLIDResolve(cp.ID)
	for _, ev := range events {
		b.enqueueEvent(ev)
	}
	return appended
}

// scheduleContactLIDResolve is _schedule_contact_lid_resolve: the LID table is
// local, so the resolution runs inline. A resolvable @lid whose contact is
// missing or unreadable gets its phone/name applied.
func (b *Backend) scheduleContactLIDResolve(jid string) {
	if !strings.HasSuffix(jid, "@lid") {
		return
	}
	phone := b.lidLookup(jid)
	if phone == "" {
		return
	}
	target := b.identify(jid)
	display := ""
	if target != nil {
		display = target.DisplayName
	}
	head, _, _ := strings.Cut(jid, "@")
	if target == nil || display == "" || strings.Contains(display, "@") || display == head {
		b.applyContactLIDResolution(jid, phone)
	}
}

// applyContactLIDResolution is _apply_contact_lid_resolution: single mutation
// point for a resolved @lid (extras, identity fold, contact_update).
func (b *Backend) applyContactLIDResolution(jid, phone string) {
	b.lidCacheLoad()
	b.cmu.RLock()
	book := b.addressBook
	b.cmu.RUnlock()
	nameBook := cachedAddressBookName(book, phone, jid)
	newName := nameBook
	if newName == "" {
		newName = b.lidName(jid)
	}
	if newName == "" && phone != "" {
		newName = "+" + phone
	}

	b.cmu.Lock()
	target := b.byJID[jid]
	if target == nil {
		for _, c := range b.contacts {
			if c.ID == jid {
				target = c
				break
			}
		}
	}
	created := target == nil
	b.cmu.Unlock()
	if created {
		name := newName
		if name == "" {
			name = jid
		}
		b.RegisterContact(*newContact(jid, name, nil))
	}

	var merged []proto.Event
	b.cmu.Lock()
	target = b.byJID[jid]
	if target == nil {
		target = newContact(jid, jid, nil)
	}
	var listed *proto.Contact
	for _, c := range b.contacts {
		if c.ID == target.ID {
			listed = c
			break
		}
	}
	if listed == nil {
		b.contacts = append(b.contacts, target)
	} else {
		target = listed
	}
	if _, ok := b.byJID[jid]; !ok {
		b.byJID[jid] = target
	}
	if target.Extras == nil {
		target.Extras = map[string]any{}
	}
	target.Extras["phone"], target.Extras["lid"] = phone, jid
	if created {
		target.Extras["is_chat_active"], target.Extras["source"] = true, "wa_chats"
	}
	for _, loser := range b.foldByIdentity(target) {
		b.repointLocked(loser, target)
		b.byJID[loser.ID] = target
		mergeContactMetadata(target, loser)
		merged = append(merged, b.mergedContactEvent(loser, target))
	}
	if isPlaceholderDisplayName(target.DisplayName, jid, phone) && newName != "" {
		target.DisplayName = newName
	}
	snapshot := cloneContact(target)
	b.cmu.Unlock()

	var nn any
	if newName != "" {
		nn = newName
	}
	b.enqueueEvent(proto.Event{Type: proto.EventContactUpdate, Protocol: proto.ProtocolWhatsApp, ContactID: snapshot.ID,
		Payload: map[string]any{"phone": phone, "display_name": nn, "contact": snapshot}})
	for _, ev := range merged {
		b.enqueueEvent(ev)
	}
}

// contactUpdateEvent is base._contact_update_event.
func contactUpdateEvent(c *proto.Contact) proto.Event {
	var phone any
	if p := exStr(c, "phone"); p != "" {
		phone = p
	}
	return proto.Event{Type: proto.EventContactUpdate, Protocol: proto.ProtocolWhatsApp, ContactID: c.ID,
		Payload: map[string]any{"display_name": c.DisplayName, "phone": phone, "contact": cloneContact(c)}}
}

// mergeContactsInPlaceLocked is _merge_contacts_in_place: it keeps object
// identity, preserves ghosts and returns (new, dropped).
func (b *Backend) mergeContactsInPlaceLocked(fresh []*proto.Contact) (newC, dropped []*proto.Contact, evs []proto.Event) {
	freshBy := map[string]*proto.Contact{}
	var order []string
	for _, c := range fresh {
		if _, dup := freshBy[c.ID]; !dup {
			order = append(order, c.ID)
		}
		freshBy[c.ID] = c
	}
	var kept []*proto.Contact
	for _, c := range b.contacts {
		f := freshBy[c.ID]
		if f == nil {
			if exBool(c, "ghost") {
				kept = append(kept, c)
			} else {
				dropped = append(dropped, c)
			}
			continue
		}
		delete(freshBy, c.ID)
		delete(c.Extras, "ghost")
		c.Extras["is_chat_active"] = true
		if f.DisplayName != "" && f.DisplayName != c.ID && f.DisplayName != c.DisplayName {
			c.DisplayName = f.DisplayName
		}
		if lastTS(f) > lastTS(c) {
			setLastTS(c, lastTS(f))
		}
		for _, k := range []string{"phone", "lid", "archived"} {
			if v, ok := f.Extras[k]; ok {
				c.Extras[k] = v
			}
		}
		kept = append(kept, c)
	}
	for _, id := range order {
		if f, ok := freshBy[id]; ok {
			newC = append(newC, f)
			kept = append(kept, f)
		}
	}
	b.contacts = kept
	merged := b.foldContactsLocked()
	for _, m := range merged {
		dropped = append(dropped, m.loser)
	}
	if len(merged) > 0 {
		alive := map[*proto.Contact]bool{}
		for _, c := range b.contacts {
			alive[c] = true
		}
		var nn []*proto.Contact
		for _, c := range newC {
			if alive[c] {
				nn = append(nn, c)
			}
		}
		newC = nn
	}
	b.rebuildAliasesLocked()
	for _, m := range merged {
		evs = append(evs, b.mergedContactEvent(m.loser, m.winner))
	}
	return newC, dropped, evs
}

// RefreshContacts implements proto.ContactRefresher (refresh_contacts_sync).
// A failed fetch goes in Errors and nothing is mutated.
func (b *Backend) RefreshContacts(ctx context.Context, force bool) proto.AddressBookRefreshResult {
	res := proto.AddressBookRefreshResult{Protocol: proto.ProtocolWhatsApp}
	if !b.connected.Load() {
		res.Errors = "not connected"
		return res
	}
	b.cmu.RLock()
	before := make(map[string]string, len(b.contacts))
	for _, c := range b.contacts {
		before[c.ID] = c.DisplayName
	}
	b.cmu.RUnlock()
	if err := b.refreshChatRegistry(ctx, true); err != nil {
		res.Errors = "fetch /chats failed"
		return res
	}
	fresh := b.buildContactsFromChats(b.chatRows())
	b.lidCacheLoad()
	b.cmu.Lock()
	newC, _, evs := b.mergeContactsInPlaceLocked(fresh)
	b.cmu.Unlock()
	for _, ev := range evs {
		b.enqueueEvent(ev)
	}
	book := b.listAddressBook(ctx, force, true)
	b.cmu.Lock()
	applyAddressBookNames(b.contacts, buildAddressBookNameMap(book))
	var renamed []*proto.Contact
	for _, c := range b.contacts {
		if old, ok := before[c.ID]; ok && old != c.DisplayName {
			renamed = append(renamed, c)
		}
	}
	for _, c := range newC {
		b.enqueueEvent(contactUpdateEvent(c))
	}
	for _, c := range renamed {
		b.enqueueEvent(contactUpdateEvent(c))
	}
	res.NewContacts = cloneContacts(newC)
	res.RenamedContacts = cloneContacts(renamed)
	b.cmu.Unlock()
	return res
}

// reapplyAddressBookNames re-applies the book names after the LID resolver ran.
func (b *Backend) reapplyAddressBookNames() {
	book := b.listAddressBook(context.Background(), true, false)
	b.cmu.Lock()
	applyAddressBookNames(b.contacts, buildAddressBookNameMap(book))
	b.cmu.Unlock()
}

// setChatArchivedLocal is _apply_chat_archived: flags the contact and notifies.
func (b *Backend) setChatArchivedLocal(contactID string, archived bool) {
	b.cmu.Lock()
	if m := b.chats[contactID]; m != nil {
		m.Archived = archived
	} else {
		b.chats[contactID] = &chatMeta{Archived: archived}
	}
	c := b.byJID[contactID]
	if c == nil {
		b.cmu.Unlock()
		return
	}
	if exBool(c, "archived") == archived {
		b.cmu.Unlock()
		return
	}
	c.Extras["archived"] = archived
	ev := contactUpdateEvent(c)
	b.cmu.Unlock()
	b.enqueueEvent(ev)
}
