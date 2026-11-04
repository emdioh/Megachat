package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/tg"
)

// ---- entity -> contact -------------------------------------------------------

func userDisplayName(u *tg.User) string {
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	if strings.TrimSpace(name) == "" {
		name = u.Username
		if name == "" {
			name = strconv.FormatInt(u.ID, 10)
		}
	}
	return name
}

// userToContact is _entity_to_contact for a User.
func userToContact(u *tg.User) proto.Contact {
	id := strconv.FormatInt(markedID(PeerUser, u.ID), 10)
	name := strings.TrimSpace(userDisplayName(u))
	if name == "" {
		name = id
	}
	return proto.Contact{ID: id, DisplayName: name, Protocol: proto.ProtocolTelegram, Extras: map[string]any{
		"username": u.Username, "phone": u.Phone, "is_group": false,
	}}
}

func chatToContact(c *tg.Chat) proto.Contact {
	id := strconv.FormatInt(markedID(PeerChat, c.ID), 10)
	name := strings.TrimSpace(c.Title)
	if name == "" {
		name = id
	}
	return proto.Contact{ID: id, DisplayName: name, Protocol: proto.ProtocolTelegram, Extras: map[string]any{"is_group": true}}
}

func channelToContact(c *tg.Channel) proto.Contact {
	id := strconv.FormatInt(markedID(PeerChannel, c.ID), 10)
	name := strings.TrimSpace(c.Title)
	if name == "" {
		name = id
	}
	return proto.Contact{ID: id, DisplayName: name, Protocol: proto.ProtocolTelegram, Extras: map[string]any{"is_channel": true}}
}

// userToAddressBookContact is _user_to_address_book_contact.
func userToAddressBookContact(u *tg.User) proto.Contact {
	var phone strings.Builder
	for _, r := range u.Phone {
		if r >= '0' && r <= '9' {
			phone.WriteRune(r)
		}
	}
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName + " " + u.LastName))
	if name == "" {
		name = u.Username
	}
	if name == "" && phone.Len() > 0 {
		name = "+" + phone.String()
	}
	if name == "" {
		name = strconv.FormatInt(u.ID, 10)
	}
	hash := ""
	if u.AccessHash != 0 {
		hash = strconv.FormatInt(u.AccessHash, 10)
	}
	return proto.Contact{
		ID: strconv.FormatInt(u.ID, 10), DisplayName: strings.TrimSpace(name), Protocol: proto.ProtocolTelegram,
		Extras: map[string]any{
			"phone": phone.String(), "username": u.Username, "access_hash": hash,
			"is_group": false, "address_book": true, "source": "tg_book",
		},
	}
}

// ---- dialogs -------------------------------------------------------------------

type msgKey struct {
	peer int64
	id   int
}

type entityMaps struct {
	users    map[int64]*tg.User
	chats    map[int64]*tg.Chat
	channels map[int64]*tg.Channel
	others   map[int64]string // forbidden/empty chats: marked id -> title
}

func newEntityMaps(users []tg.UserClass, chats []tg.ChatClass) entityMaps {
	m := entityMaps{users: map[int64]*tg.User{}, chats: map[int64]*tg.Chat{}, channels: map[int64]*tg.Channel{}, others: map[int64]string{}}
	for _, u := range users {
		if uu, ok := u.(*tg.User); ok {
			m.users[uu.ID] = uu
		}
	}
	for _, c := range chats {
		switch t := c.(type) {
		case *tg.Chat:
			m.chats[t.ID] = t
		case *tg.Channel:
			m.channels[t.ID] = t
		case *tg.ChatForbidden:
			m.others[markedID(PeerChat, t.ID)] = t.Title
		case *tg.ChannelForbidden:
			m.others[markedID(PeerChannel, t.ID)] = t.Title
		}
	}
	return m
}

func (m entityMaps) contactFor(p tg.PeerClass) (proto.Contact, bool) {
	switch t := p.(type) {
	case *tg.PeerUser:
		if u, ok := m.users[t.UserID]; ok {
			return userToContact(u), true
		}
	case *tg.PeerChat:
		if c, ok := m.chats[t.ChatID]; ok {
			return chatToContact(c), true
		}
		if title, ok := m.others[markedID(PeerChat, t.ChatID)]; ok {
			id := strconv.FormatInt(markedID(PeerChat, t.ChatID), 10)
			return proto.Contact{ID: id, DisplayName: firstNonEmpty(strings.TrimSpace(title), id), Protocol: proto.ProtocolTelegram, Extras: map[string]any{"is_group": true}}, true
		}
	case *tg.PeerChannel:
		if c, ok := m.channels[t.ChannelID]; ok {
			return channelToContact(c), true
		}
		if title, ok := m.others[markedID(PeerChannel, t.ChannelID)]; ok {
			id := strconv.FormatInt(markedID(PeerChannel, t.ChannelID), 10)
			return proto.Contact{ID: id, DisplayName: firstNonEmpty(strings.TrimSpace(title), id), Protocol: proto.ProtocolTelegram, Extras: map[string]any{"is_channel": true}}, true
		}
	}
	return proto.Contact{}, false
}

func (m entityMaps) inputPeer(p tg.PeerClass) tg.InputPeerClass {
	switch t := p.(type) {
	case *tg.PeerUser:
		if u, ok := m.users[t.UserID]; ok {
			return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: t.ChatID}
	case *tg.PeerChannel:
		if c, ok := m.channels[t.ChannelID]; ok {
			return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}
		}
	}
	return &tg.InputPeerEmpty{}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func messageDateID(m tg.MessageClass) (date, id int, peer tg.PeerClass) {
	switch t := m.(type) {
	case *tg.Message:
		return t.Date, t.ID, t.PeerID
	case *tg.MessageService:
		return t.Date, t.ID, t.PeerID
	case *tg.MessageEmpty:
		return 0, t.ID, t.PeerID
	}
	return 0, 0, nil
}

// fetchDialogs is get_dialogs(limit=200): main-folder dialogs, newest first.
// last_message_ts / read_outbox_max_id are filled like _load_contacts.
func (b *Backend) fetchDialogs(ctx context.Context, api *tg.Client) ([]proto.Contact, error) {
	var out []proto.Contact
	var (
		offDate, offID int
		offPeer        tg.InputPeerClass = &tg.InputPeerEmpty{}
	)
	for len(out) < dialogLimit {
		limit := dialogLimit - len(out)
		if limit > 100 {
			limit = 100
		}
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offDate, OffsetID: offID, OffsetPeer: offPeer, Limit: limit,
		})
		if err != nil {
			return nil, err
		}
		var (
			dialogs  []tg.DialogClass
			messages []tg.MessageClass
			chats    []tg.ChatClass
			users    []tg.UserClass
			complete bool
		)
		switch r := res.(type) {
		case *tg.MessagesDialogs:
			dialogs, messages, chats, users, complete = r.Dialogs, r.Messages, r.Chats, r.Users, true
		case *tg.MessagesDialogsSlice:
			dialogs, messages, chats, users = r.Dialogs, r.Messages, r.Chats, r.Users
		default:
			return out, nil
		}
		b.peers.learnLists(users, chats)
		ents := newEntityMaps(users, chats)
		msgs := map[msgKey]tg.MessageClass{}
		for _, m := range messages {
			if _, _, p := messageDateID(m); p != nil {
				if pid, ok := peerMarked(p); ok {
					_, id, _ := messageDateID(m)
					msgs[msgKey{pid, id}] = m
				}
			}
		}
		var lastPeer tg.PeerClass
		var lastDate, lastID int
		count := 0
		for _, dc := range dialogs {
			d, ok := dc.(*tg.Dialog)
			if !ok {
				continue
			}
			count++
			pid, _ := peerMarked(d.Peer)
			lastPeer = d.Peer
			lastID = d.TopMessage
			lastDate = 0
			var msgDate int
			if m, ok := msgs[msgKey{pid, d.TopMessage}]; ok {
				msgDate, _, _ = messageDateID(m)
				lastDate = msgDate
			}
			cc, ok := ents.contactFor(d.Peer)
			if !ok {
				continue
			}
			if msgDate != 0 {
				cc.Extras["last_message_ts"] = int64(msgDate) * 1000
			}
			if d.ReadOutboxMaxID != 0 {
				cc.Extras["read_outbox_max_id"] = int64(d.ReadOutboxMaxID)
			}
			out = append(out, cc)
		}
		if complete || count < limit || lastPeer == nil || len(out) >= dialogLimit {
			break
		}
		offDate, offID, offPeer = lastDate, lastID, ents.inputPeer(lastPeer)
		if _, empty := offPeer.(*tg.InputPeerEmpty); empty {
			break
		}
	}
	return out, nil
}

// lastMessageTS reads Extras["last_message_ts"].
func lastMessageTS(c proto.Contact) int64 { return c.LastMessageTS() }

// loadContacts is _load_contacts: replaces the contact list.
func (b *Backend) loadContacts(ctx context.Context, api *tg.Client) {
	fresh, err := b.fetchDialogs(ctx, api)
	if err != nil {
		b.log.Error("Telegram get_dialogs failed", "err", err)
		b.cmu.Lock()
		b.contacts, b.byID = nil, map[int64]*proto.Contact{}
		b.cmu.Unlock()
		return
	}
	list := make([]*proto.Contact, 0, len(fresh))
	byID := map[int64]*proto.Contact{}
	for i := range fresh {
		c := fresh[i]
		list = append(list, &c)
		if id, ok := toInt(c.ID); ok {
			byID[id] = &c
		}
	}
	b.cmu.Lock()
	b.contacts, b.byID = list, byID
	b.cmu.Unlock()
	b.reconcileReadState(ctx)
}

// loadContactsMerge is _load_contacts_merge: merge in place, return new ones.
func (b *Backend) loadContactsMerge(ctx context.Context, api *tg.Client) ([]proto.Contact, error) {
	fresh, err := b.fetchDialogs(ctx, api)
	if err != nil {
		return nil, err
	}
	freshByID := map[int64]*proto.Contact{}
	for i := range fresh {
		if id, ok := toInt(fresh[i].ID); ok {
			freshByID[id] = &fresh[i]
		}
	}
	b.cmu.Lock()
	var kept []*proto.Contact
	for _, c := range b.contacts {
		id, ok := toInt(c.ID)
		var f *proto.Contact
		if ok {
			f = freshByID[id]
			delete(freshByID, id)
		}
		switch {
		case f != nil:
			if f.DisplayName != "" && f.DisplayName != c.DisplayName && tgIsPlaceholder(c.DisplayName, c.ID) {
				c.DisplayName = f.DisplayName
			}
			if f.LastMessageTS() > c.LastMessageTS() {
				c.Extras = ensureExtras(c.Extras)
				c.Extras["last_message_ts"] = f.LastMessageTS()
			}
			if rm, ok := f.Extras["read_outbox_max_id"]; ok && rm != nil {
				c.Extras = ensureExtras(c.Extras)
				c.Extras["read_outbox_max_id"] = rm
			}
			kept = append(kept, c)
		case truthy(c.Extras["ghost"]):
			kept = append(kept, c)
		}
	}
	keptIDs := map[int64]bool{}
	for _, c := range kept {
		if id, ok := toInt(c.ID); ok {
			keptIDs[id] = true
		}
	}
	var newC []proto.Contact
	for i := range fresh {
		id, ok := toInt(fresh[i].ID)
		if ok && keptIDs[id] {
			continue
		}
		c := fresh[i]
		newC = append(newC, cloneContact(c))
		kept = append(kept, &c)
	}
	byID := map[int64]*proto.Contact{}
	for _, c := range kept {
		if id, ok := toInt(c.ID); ok {
			byID[id] = c
		}
	}
	b.contacts, b.byID = kept, byID
	b.cmu.Unlock()
	b.reconcileReadState(ctx)
	return newC, nil
}

func ensureExtras(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// reconcileReadState is _reconcile_read_state: mark outgoing messages read
// from the dialogs' read_outbox_max_id (never downgrades).
func (b *Backend) reconcileReadState(ctx context.Context) {
	b.cmu.RLock()
	type item struct {
		id  string
		max int64
	}
	var items []item
	for _, c := range b.contacts {
		if mx, ok := toInt64(c.Extras["read_outbox_max_id"]); ok && mx != 0 {
			items = append(items, item{c.ID, mx})
		}
	}
	b.cmu.RUnlock()
	for _, it := range items {
		var updated []string
		b.ingestMu.Lock()
		for _, m := range b.cache[it.id] {
			if !m.IsMine || m.ID == "" {
				continue
			}
			mid, ok := toInt(m.ID)
			if !ok || mid > it.max {
				continue
			}
			if statusRank(m.Status, "sent") < statusRank("read", "") {
				m.Status = "read"
				updated = append(updated, m.ID)
				if b.st != nil {
					cid := it.id
					if _, err := b.st.UpdateMessageStatusByID(ctx, m.ID, "read", proto.ProtocolTelegram, &cid); err != nil {
						b.log.Error("Telegram: update status failed", "err", err)
					}
				}
			}
		}
		b.ingestMu.Unlock()
		if len(updated) > 0 {
			ids := make([]any, len(updated))
			for i, u := range updated {
				ids[i] = u
			}
			b.emit(proto.EventReceipt, it.id, map[string]any{"message_ids": ids, "is_read": true})
		}
	}
}

var statusRanks = map[string]int{"pending": 0, "failed": 0, "sent": 1, "delivered": 2, "read": 3}

// statusRank is rank.get(status or default, 0).
func statusRank(status, def string) int {
	if status == "" {
		status = def
	}
	return statusRanks[status]
}

// ---- proto.Backend: contacts -----------------------------------------------------

// ListContacts implements proto.Backend (a copy of the dialog list).
func (b *Backend) ListContacts(ctx context.Context) ([]proto.Contact, error) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	out := make([]proto.Contact, len(b.contacts))
	for i, c := range b.contacts {
		out[i] = cloneContact(*c)
	}
	return out, nil
}

// identifyContact is _identify_contact (nil = unknown).
func (b *Backend) identifyContact(contactID string) *proto.Contact {
	id, ok := toInt(contactID)
	if !ok {
		return nil
	}
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	if c, ok := b.byID[id]; ok {
		cc := cloneContact(*c)
		return &cc
	}
	return nil
}

// contactValue is the payload["contact"] value: a proto.Contact or nil.
func (b *Backend) contactValue(contactID string) any {
	if c := b.identifyContact(contactID); c != nil {
		return *c
	}
	return nil
}

// RegisterContact implements core.ContactRegistrar (register_contact).
func (b *Backend) RegisterContact(c proto.Contact) bool {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	for _, e := range b.contacts {
		if e.CacheKey() == c.CacheKey() {
			return false
		}
	}
	cc := cloneContact(c)
	b.contacts = append(b.contacts, &cc)
	if id, ok := toInt(cc.ID); ok {
		b.byID[id] = &cc
	}
	return true
}

// FindContact implements proto.ContactFinder: contacts first, then the cached
// address book (zero network).
func (b *Backend) FindContact(contactID string) (proto.Contact, bool) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	for _, c := range b.contacts {
		if c.ID == contactID {
			return cloneContact(*c), true
		}
	}
	if b.addressBookS {
		for _, c := range b.addressBook {
			if c.ID == contactID {
				return cloneContact(c), true
			}
		}
	}
	return proto.Contact{}, false
}

// PlaceholderName implements core.PlaceholderNamer: the cached display name
// of a not yet listed contact ("" = unknown).
func (b *Backend) PlaceholderName(contactID string) string {
	if c, ok := b.FindContact(contactID); ok && !tgIsPlaceholder(c.DisplayName, c.ID) {
		return c.DisplayName
	}
	return ""
}

// ---- address book ----------------------------------------------------------------

// fetchAddressBook is _fetch_address_book: contacts.getContacts minus bots
// and deleted accounts.
func (b *Backend) fetchAddressBook(ctx context.Context, api *tg.Client) ([]proto.Contact, error) {
	res, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, err
	}
	cs, ok := res.(*tg.ContactsContacts)
	if !ok {
		return nil, nil
	}
	b.peers.learnLists(cs.Users, nil)
	var out []proto.Contact
	for _, uc := range cs.Users {
		u, ok := uc.(*tg.User)
		if !ok || u.Bot {
			continue
		}
		name := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if u.Deleted || strings.Contains(strings.ToLower(name), "deleted account") {
			continue
		}
		out = append(out, userToAddressBookContact(u))
	}
	return out, nil
}

// ListAddressBook implements proto.AddressBook: contacts book U dialogs,
// TTL-cached, never failing (stale copy or nil on error).
func (b *Backend) ListAddressBook(ctx context.Context, force bool) ([]proto.Contact, error) {
	now := b.clock()
	b.cmu.RLock()
	if !force && b.addressBookS && now.Sub(b.addressBookT) < b.bookTTL {
		out := cloneContacts(b.addressBook)
		b.cmu.RUnlock()
		return out, nil
	}
	stale := func() []proto.Contact {
		if b.addressBookS {
			return cloneContacts(b.addressBook)
		}
		return nil
	}
	api, err := b.getAPI()
	if err != nil {
		out := stale()
		b.cmu.RUnlock()
		return out, nil
	}
	b.cmu.RUnlock()

	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	book, err := b.fetchAddressBook(cctx, api)
	if err != nil {
		b.log.Warn("Telegram address book build failed", "err", err)
		b.cmu.RLock()
		out := stale()
		b.cmu.RUnlock()
		return out, nil
	}

	b.cmu.Lock()
	defer b.cmu.Unlock()
	dialogs := map[int64]*proto.Contact{}
	var dialogOrder []int64
	for _, c := range b.contacts {
		if id, ok := toInt(c.ID); ok {
			if _, dup := dialogs[id]; !dup {
				dialogOrder = append(dialogOrder, id)
			}
			dialogs[id] = c
		}
	}
	entries := make([]proto.Contact, 0, len(book)+len(dialogs))
	for _, cc := range book {
		id, _ := toInt(cc.ID)
		if d, ok := dialogs[id]; ok {
			delete(dialogs, id)
			cc.Extras["last_message_ts"] = d.LastMessageTS()
			cc.Extras["is_chat_active"] = true
			if rm, ok := d.Extras["read_outbox_max_id"]; ok && rm != nil && rm != int64(0) {
				cc.Extras["read_outbox_max_id"] = rm
			}
		}
		entries = append(entries, cc)
	}
	for _, id := range dialogOrder {
		d, ok := dialogs[id]
		if !ok {
			continue
		}
		c := cloneContact(*d)
		c.Extras["address_book"] = true
		c.Extras["is_chat_active"] = true
		c.Extras["source"] = "tg_dialogs"
		entries = append(entries, c)
	}
	b.addressBook, b.addressBookT, b.addressBookS = entries, now, true
	for i := range book {
		cc := cloneContact(book[i])
		if id, ok := toInt(cc.ID); ok {
			if _, have := b.byID[id]; !have {
				b.byID[id] = &cc
			}
		}
	}
	return cloneContacts(b.addressBook), nil
}

// applyBookToContacts is _apply_book_to_contacts (object identity kept).
func (b *Backend) applyBookToContacts(book []proto.Contact) {
	byID := map[int64]proto.Contact{}
	for _, c := range book {
		if id, ok := toInt(c.ID); ok {
			byID[id] = c
		}
	}
	b.cmu.Lock()
	defer b.cmu.Unlock()
	for _, c := range b.contacts {
		id, ok := toInt(c.ID)
		if !ok {
			continue
		}
		bk, ok := byID[id]
		if !ok {
			continue
		}
		if bk.DisplayName != "" && bk.DisplayName != c.DisplayName && tgIsPlaceholder(c.DisplayName, c.ID) {
			c.DisplayName = bk.DisplayName
		}
		if getStr(c.Extras, "access_hash") == "" && getStr(bk.Extras, "access_hash") != "" {
			c.Extras = ensureExtras(c.Extras)
			c.Extras["access_hash"] = bk.Extras["access_hash"]
		}
	}
	for _, bk := range book {
		id, ok := toInt(bk.ID)
		if !ok {
			continue
		}
		existing, have := b.byID[id]
		if !have {
			cc := cloneContact(bk)
			b.byID[id] = &cc
			continue
		}
		if bk.DisplayName != "" && tgIsPlaceholder(existing.DisplayName, existing.ID) && bk.DisplayName != existing.DisplayName {
			existing.DisplayName = bk.DisplayName
		}
		if getStr(existing.Extras, "access_hash") == "" && getStr(bk.Extras, "access_hash") != "" {
			existing.Extras = ensureExtras(existing.Extras)
			existing.Extras["access_hash"] = bk.Extras["access_hash"]
		}
	}
}

func contactUpdatePayload(c proto.Contact) map[string]any {
	return map[string]any{"display_name": c.DisplayName, "phone": c.Extras["phone"], "contact": c}
}

// RefreshContacts implements proto.ContactRefresher (refresh_contacts_sync).
func (b *Backend) RefreshContacts(ctx context.Context, force bool) proto.AddressBookRefreshResult {
	res := proto.AddressBookRefreshResult{Protocol: proto.ProtocolTelegram}
	api, err := b.getAPI()
	if err != nil {
		res.Errors = "not connected"
		return res
	}
	b.cmu.RLock()
	before := map[string]string{}
	for _, c := range b.contacts {
		before[c.ID] = c.DisplayName
	}
	b.cmu.RUnlock()

	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	newC, err := b.loadContactsMerge(cctx, api)
	cancel()
	if err != nil {
		b.log.Warn("Telegram refresh: get_dialogs failed", "err", err)
		res.Errors = err.Error()
		return res
	}
	book, _ := b.ListAddressBook(ctx, force)
	b.applyBookToContacts(book)

	var renamed []proto.Contact
	b.cmu.RLock()
	for _, c := range b.contacts {
		if old, ok := before[c.ID]; ok && old != c.DisplayName {
			renamed = append(renamed, cloneContact(*c))
		}
	}
	b.cmu.RUnlock()
	for _, c := range append(append([]proto.Contact{}, newC...), renamed...) {
		b.emit(proto.EventContactUpdate, c.ID, contactUpdatePayload(c))
	}
	res.NewContacts, res.RenamedContacts = newC, renamed
	return res
}

// ---- peer resolution ---------------------------------------------------------------

// resolveInputPeer is _resolve_input_entity: peer cache first, then the
// access_hash kept in the contact extras (address book).
func (b *Backend) resolveInputPeer(eid int64) (tg.InputPeerClass, error) {
	kind, raw := splitMarked(eid)
	switch kind {
	case PeerChat:
		return &tg.InputPeerChat{ChatID: raw}, nil
	case PeerChannel:
		if h, ok := b.peers.get(eid); ok {
			return &tg.InputPeerChannel{ChannelID: raw, AccessHash: h}, nil
		}
		return nil, fmt.Errorf("Telegram: access_hash mancante per %d", eid)
	}
	if me := b.selfUser(); me != nil && me.ID == raw {
		return &tg.InputPeerSelf{}, nil
	}
	if h, ok := b.peers.get(eid); ok {
		return &tg.InputPeerUser{UserID: raw, AccessHash: h}, nil
	}
	b.cmu.RLock()
	c := b.byID[eid]
	var rawHash string
	if c != nil {
		rawHash = getStr(c.Extras, "access_hash")
	}
	b.cmu.RUnlock()
	if h, err := strconv.ParseInt(rawHash, 10, 64); err == nil && rawHash != "" {
		return &tg.InputPeerUser{UserID: raw, AccessHash: h}, nil
	}
	return nil, fmt.Errorf("Telegram: access_hash mancante per %d", eid)
}

// parseContactID parses a contact id like int(contact_id).
func parseContactID(contactID string) (int64, error) {
	id, ok := toInt(contactID)
	if !ok {
		return 0, fmt.Errorf("Invalid Telegram contact id: %s", contactID)
	}
	return id, nil
}

var errNoAPI = errors.New("Telegram backend not connected")
