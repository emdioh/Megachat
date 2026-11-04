package whatsapp

import (
	"context"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/types"
)

// bookRaw is one raw address-book row (a WAHA contact dict): ID is the JID,
// Name the saved (address book) name, PushName the contact's own name,
// Number an explicit phone and LID an explicit linked id.
type bookRaw struct {
	ID       string
	Name     string
	PushName string
	Number   string
	LID      string
}

// bookEntry is a deduplicated address-book row.
type bookEntry struct {
	Phone, Name, PushName, LID string
}

// dedupBookContacts is _dedup_book_contacts: dedup by phone number.
//
// The key is the digits of the phone. For phone jids ("@c.us" /
// "@s.whatsapp.net") the jid is authoritative; the explicit number is only
// used for "@lid" rows (whose id is the linked identifier). Broadcasts,
// newsletters and groups are dropped. Among duplicates of the same number the
// winner has (1) a non-empty name over only-pushname, (2) the "@c.us" domain,
// (3) first occurrence. A "@lid" id or explicit lid is carried in LID so the
// group sender resolver can map a raw "@lid" author to its name.
func dedupBookContacts(raw []bookRaw) []bookEntry {
	type snap struct {
		jid string
		e   bookEntry
		lid string
	}
	byPhone := map[string]*snap{}
	var order []string
	for _, r := range raw {
		jid := r.ID
		if jid == "" {
			continue
		}
		if strings.Contains(jid, "@broadcast") || strings.Contains(jid, "@newsletter") || strings.HasSuffix(jid, "@g.us") {
			continue
		}
		var digits string
		if strings.HasSuffix(jid, "@c.us") || strings.HasSuffix(jid, "@s.whatsapp.net") {
			digits = jidDigits(jid)
		} else if r.Number != "" {
			digits = jidDigits(r.Number)
		} else {
			digits = jidDigits(jid)
		}
		if digits == "" {
			continue
		}
		lid := r.LID
		if strings.HasSuffix(jid, "@lid") {
			lid = jid
		}
		cur := byPhone[digits]
		if cur == nil {
			byPhone[digits] = &snap{jid, bookEntry{Phone: digits, Name: r.Name, PushName: r.PushName}, lid}
			order = append(order, digits)
			continue
		}
		cand := &snap{jid, bookEntry{Phone: digits, Name: r.Name, PushName: r.PushName}, lid}
		if cand.lid == "" {
			cand.lid = cur.lid
		}
		candHas, curHas := r.Name != "", cur.e.Name != ""
		if candHas != curHas {
			if candHas {
				byPhone[digits] = cand
			}
			continue
		}
		if strings.HasSuffix(jid, "@c.us") && !strings.HasSuffix(cur.jid, "@c.us") {
			byPhone[digits] = cand
		}
	}
	out := make([]bookEntry, 0, len(order))
	for _, d := range order {
		s := byPhone[d]
		e := s.e
		e.LID = s.lid
		out = append(out, e)
	}
	return out
}

// buildAddressBookNameMap is _build_address_book_name_map: phone->name and
// lid->name from the real address-book entries only (source == "wa_book").
func buildAddressBookNameMap(book []proto.Contact) map[string]string {
	m := map[string]string{}
	for i := range book {
		c := &book[i]
		if exStr(c, "source") != "wa_book" {
			continue
		}
		phone := exStr(c, "phone")
		if looksLikePhone(c.DisplayName, phone) {
			continue
		}
		if phone != "" {
			m[phone] = c.DisplayName
		}
		if lid := exStr(c, "lid"); lid != "" {
			m[lid] = c.DisplayName
		}
	}
	return m
}

// applyAddressBookNames is _apply_address_book_names: only display_name,
// priority phone > lid. Returns the count of renamed contacts.
func applyAddressBookNames(contacts []*proto.Contact, names map[string]string) int {
	n := 0
	for _, c := range contacts {
		var nn string
		if p := exStr(c, "phone"); p != "" {
			nn = names[p]
		}
		if nn == "" {
			if l := exStr(c, "lid"); l != "" {
				nn = names[l]
			}
		}
		if nn != "" && c.DisplayName != nn {
			c.DisplayName = nn
			n++
		}
	}
	return n
}

// cachedAddressBookName is _cached_address_book_name: a lookup in the
// in-memory snapshot (zero network).
func cachedAddressBookName(book []proto.Contact, phone, lid string) string {
	if len(book) == 0 {
		return ""
	}
	m := buildAddressBookNameMap(book)
	if phone != "" {
		if n := m[phone]; n != "" {
			return n
		}
	}
	if lid != "" {
		if n := m[lid]; n != "" {
			return n
		}
	}
	return ""
}

// rawAddressBook is rest.list_all_contacts: the contact table of the device,
// enriched with the LID <-> phone links. ok=false means "unavailable".
func (b *Backend) rawAddressBook(ctx context.Context) ([]bookRaw, bool) {
	cs := b.deviceContacts()
	if cs == nil {
		return nil, false
	}
	all, err := cs.GetAllContacts(ctx)
	if err != nil {
		b.log.Debug("WhatsApp contact table unavailable", "err", err)
		return nil, false
	}
	out := make([]bookRaw, 0, len(all))
	for j, ci := range all {
		if !ci.Found && contactBestName(ci) == "" {
			continue
		}
		r := bookRaw{ID: WAHAFromJID(j), Name: firstNonEmpty(ci.FullName, ci.FirstName), PushName: firstNonEmpty(ci.PushName, ci.BusinessName)}
		switch j.Server {
		case types.HiddenUserServer:
			if p := b.lidLookup(r.ID); p != "" {
				r.Number = p
			}
		case types.DefaultUserServer, types.LegacyUserServer:
			if l := b.phoneToLID(j.User); l != "" {
				r.LID = l
			}
		}
		out = append(out, r)
	}
	return out, true
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// ListAddressBook implements proto.AddressBook.
func (b *Backend) ListAddressBook(ctx context.Context, force bool) ([]proto.Contact, error) {
	return b.listAddressBook(ctx, force, true), nil
}

// listAddressBook is list_address_book_sync: the complete address book =
// deduplicated book entries U active chats. It never fails: on a remote error
// it serves the stale copy or nothing. "@lid" chats that the cache cannot
// resolve stay standalone (lid_unresolved).
func (b *Backend) listAddressBook(ctx context.Context, force, startResolver bool) []proto.Contact {
	if startResolver {
		b.StartLIDResolver()
	}
	now := nowFunc()
	b.cmu.RLock()
	if !force && b.addressBookS && now.Sub(b.addressBookT) < b.bookTTL {
		out := append([]proto.Contact(nil), b.addressBook...)
		b.cmu.RUnlock()
		return out
	}
	b.cmu.RUnlock()

	stale := func() []proto.Contact {
		b.cmu.RLock()
		defer b.cmu.RUnlock()
		return append([]proto.Contact(nil), b.addressBook...)
	}
	raw, ok := b.rawAddressBook(ctx)
	if !ok {
		// Transport-level failure: keep the cached copy untouched.
		return stale()
	}
	book := dedupBookContacts(raw)
	b.lidCacheLoad()

	// Merge-add the chats (the registry replaces the paginated /chats).
	if err := b.refreshChatRegistry(ctx, false); err != nil {
		b.log.Debug("chat registry refresh failed", "err", err)
	}
	b.cmu.RLock()
	chats := cloneContacts(b.contacts)
	b.cmu.RUnlock()
	known := map[string]bool{}
	for _, c := range chats {
		known[c.ID] = true
	}
	for _, c := range b.buildContactsFromChats(b.chatRows()) {
		if !known[c.ID] {
			chats = append(chats, cloneContact(c))
		}
	}
	// Durable registry of the groups.
	b.cmu.Lock()
	for i := range chats {
		if strings.HasSuffix(chats[i].ID, "@g.us") {
			c := chats[i]
			b.knownGroups[c.ID] = &c
		}
	}
	b.cmu.Unlock()

	byPhone := map[string]*proto.Contact{}
	var phoneOrder []string
	for _, e := range book {
		name := e.Name
		if name == "" {
			name = e.PushName
		}
		if name == "" {
			name = "+" + e.Phone
		}
		extras := map[string]any{
			"phone": e.Phone, "jid": e.Phone + "@c.us", "address_book": true,
			"is_chat_active": false, "source": "wa_book",
		}
		if e.LID != "" {
			extras["lid"] = e.LID
		}
		byPhone[e.Phone] = newContact(e.Phone+"@c.us", name, extras)
		phoneOrder = append(phoneOrder, e.Phone)
	}
	// B3 (stale-usable): a fetch that succeeded but is entirely empty never
	// removes the wa_book entries already present.
	if len(byPhone) == 0 {
		b.cmu.RLock()
		for _, prev := range b.addressBook {
			if exStr(&prev, "source") != "wa_book" || exStr(&prev, "phone") == "" {
				continue
			}
			p := exStr(&prev, "phone")
			if _, dup := byPhone[p]; dup {
				continue
			}
			cp := cloneContact(&prev)
			byPhone[p] = &cp
			phoneOrder = append(phoneOrder, p)
		}
		b.cmu.RUnlock()
	}

	var extra []proto.Contact
	for _, chat := range chats {
		chat := chat
		cid := chat.ID
		if strings.HasSuffix(cid, "@g.us") {
			extra = append(extra, copyContactWith(chat, map[string]any{"address_book": true, "is_chat_active": true, "source": "wa_chats"}))
			continue
		}
		var phone string
		if strings.HasSuffix(cid, "@c.us") {
			phone = jidDigits(cid)
		} else {
			phone = b.lidLookup(cid)
		}
		switch merged := byPhone[phone]; {
		case phone != "" && merged != nil:
			merged.ID = cid
			merged.Extras["is_chat_active"] = true
			setLastTS(merged, lastTS(&chat))
			if strings.HasSuffix(cid, "@lid") {
				merged.Extras["lid"] = cid
			}
			if merged.DisplayName == "+"+phone && chat.DisplayName != "" && chat.DisplayName != cid {
				merged.DisplayName = chat.DisplayName
			}
		case phone != "":
			extra = append(extra, copyContactWith(chat, map[string]any{"address_book": true, "is_chat_active": true, "source": "wa_chats", "phone": phone}))
		default:
			extra = append(extra, copyContactWith(chat, map[string]any{"address_book": true, "is_chat_active": true, "lid_unresolved": true, "source": "wa_chats"}))
		}
	}

	final := make([]proto.Contact, 0, len(phoneOrder)+len(extra))
	for _, p := range phoneOrder {
		final = append(final, *byPhone[p])
	}
	final = append(final, extra...)
	seen := map[string]bool{}
	deduped := final[:0:0]
	for _, c := range final {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		deduped = append(deduped, c)
	}
	b.cmu.Lock()
	b.addressBook, b.addressBookT, b.addressBookS = deduped, now, true
	out := append([]proto.Contact(nil), deduped...)
	b.cmu.Unlock()
	return out
}

// findAddressBookContact is find_address_book_contact: the cached book entry
// (id possibly rewritten to @lid by the merge), else a lookup by the phone of
// a canonical "<digits>@c.us" id. Zero network.
func (b *Backend) findAddressBookContact(contactID string) (proto.Contact, bool) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	if !b.addressBookS {
		return proto.Contact{}, false
	}
	for i := range b.addressBook {
		if b.addressBook[i].ID == contactID {
			return cloneContact(&b.addressBook[i]), true
		}
	}
	digits := jidDigits(contactID)
	if digits == "" || contactID != digits+"@c.us" {
		return proto.Contact{}, false
	}
	var matches []*proto.Contact
	for i := range b.addressBook {
		c := &b.addressBook[i]
		if !exBool(c, "lid_unresolved") && exStr(c, "phone") == digits {
			matches = append(matches, c)
		}
	}
	for _, c := range matches {
		if strings.HasSuffix(c.ID, "@c.us") {
			return cloneContact(c), true
		}
	}
	if len(matches) > 0 {
		return cloneContact(matches[0]), true
	}
	return proto.Contact{}, false
}

var _ = time.Second
