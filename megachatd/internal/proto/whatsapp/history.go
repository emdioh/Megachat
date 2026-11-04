package whatsapp

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	// historyAskCooldown limits the on-demand history requests per chat: the
	// phone answers each one with a (large) history sync.
	historyAskCooldown = 10 * time.Minute
	// historyWait bounds how long FetchHistory waits for the phone's answer.
	historyWait = 8 * time.Second
)

// entryToMessage renders a cache entry as the wire cache message.
func entryToMessage(e *entry) proto.Message {
	str := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	var m proto.Message
	m.ID = str(e.ID)
	text := e.Text
	m.Text = &text
	mine := e.IsMine
	m.IsMine = &mine
	sender := e.Sender
	m.Sender = &sender
	ts := e.Timestamp
	m.Timestamp = &ts
	m.QuoteText = str(e.QuoteText)
	mt := e.msgType()
	m.MsgType = &mt
	m.AttachmentInfo = str(e.AttachmentInfo)
	m.AttachmentID = str(e.AttachmentID)
	m.ContentType = str(e.ContentType)
	read := e.Read
	m.Read = &read
	m.Status = str(e.Status)
	m.QuoteTimestamp = e.QuoteTimestamp
	m.QuoteAuthor = str(e.QuoteAuthor)
	m.ReplyToMessageID = str(e.ReplyToMessageID)
	m.MediaKind = str(e.MediaKind)
	p := proto.ProtocolWhatsApp
	m.Protocol = &p
	return m
}

// FetchHistory implements proto.HistoryFetcher (fetch_history).
//
// WhatsApp has no "get the last N messages" call. Live traffic and the
// history the phone pushes at pairing are already in the cache, so this
// returns the newest limit cached messages of the chat; when the cache holds
// fewer than limit it also asks the phone for older ones (an on-demand
// history sync, throttled per chat) and waits briefly for the answer.
func (b *Backend) FetchHistory(ctx context.Context, contactID string, limit int) ([]proto.Message, error) {
	if !isFetchableJID(contactID) {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if b.connected.Load() && b.cachedCount(contactID) < limit {
		b.requestOlderHistory(ctx, contactID, limit)
	}
	return b.cachedMessages(contactID, limit), nil
}

func (b *Backend) cachedCount(contactID string) int {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return len(b.cache[contactID])
}

// cachedMessages returns the newest limit cached messages, oldest first.
func (b *Backend) cachedMessages(contactID string, limit int) []proto.Message {
	keys := []string{contactID}
	if c, ok := b.FindContact(contactID); ok {
		for _, k := range contactStorageKeys(c) {
			if !containsStr(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	b.ingestMu.Lock()
	var all []*entry
	for _, k := range keys {
		all = append(all, b.cache[k]...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp < all[j].Timestamp })
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	out := make([]proto.Message, len(all))
	for i, e := range all {
		out[i] = entryToMessage(e)
	}
	b.ingestMu.Unlock()
	return out
}

// requestOlderHistory asks the phone for messages older than the oldest cached
// one and waits for the answer (bounded).
func (b *Backend) requestOlderHistory(ctx context.Context, contactID string, limit int) {
	cli := b.currentClient()
	if cli == nil {
		return
	}
	now := nowFunc()
	b.cmu.Lock()
	if t, ok := b.histAsked[contactID]; ok && now.Sub(t) < historyAskCooldown {
		b.cmu.Unlock()
		return
	}
	b.cmu.Unlock()

	b.ingestMu.Lock()
	var oldest *entry
	for _, e := range b.cache[contactID] {
		if e.ID != "" && (oldest == nil || e.Timestamp < oldest.Timestamp) {
			oldest = e
		}
	}
	var anchor *entry
	if oldest != nil {
		c := *oldest
		anchor = &c
	}
	b.ingestMu.Unlock()
	if anchor == nil {
		return // nothing to anchor the request on
	}
	chat, err := JIDFromWAHA(contactID)
	if err != nil {
		return
	}
	p := ParseMsgID(anchor.ID)
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: anchor.IsMine, IsGroup: chat.Server == types.GroupServer},
		ID:            types.MessageID(p.ID), Timestamp: time.UnixMilli(anchor.Timestamp),
	}
	want := limit
	if want < 50 {
		want = 50
	}
	if want > 100 {
		want = 100
	}
	ch := make(chan struct{})
	b.cmu.Lock()
	b.histAsked[contactID] = now
	b.histWait[contactID] = ch
	b.cmu.Unlock()
	defer func() {
		b.cmu.Lock()
		if b.histWait[contactID] == ch {
			delete(b.histWait, contactID)
		}
		b.cmu.Unlock()
	}()
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := cli.SendPeerMessage(sctx, cli.BuildHistorySyncRequest(info, want)); err != nil {
		b.log.Debug("WhatsApp on-demand history request failed", "chat", contactID, "err", err)
		return
	}
	select {
	case <-ch:
	case <-time.After(b.historyTimeout):
	case <-ctx.Done():
	}
}

// signalHistory wakes a FetchHistory waiting for the chat.
func (b *Backend) signalHistory(chat string) {
	b.cmu.Lock()
	ch := b.histWait[chat]
	if ch != nil {
		delete(b.histWait, chat)
	}
	b.cmu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// onHistorySync ingests a history sync pushed by the phone (the port of
// fetch_history's ingestion loop): messages go straight into the cache and
// the store, receipts are re-emitted AFTER the messages, nothing is published
// as a message event.
func (b *Backend) onHistorySync(e *events.HistorySync) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("WhatsApp history sync panicked", "err", r)
		}
	}()
	if e == nil || e.Data == nil {
		return
	}
	cli := b.currentClient()
	if cli == nil {
		return
	}
	ctx := context.Background()
	// Messages from an on-demand sync are older history the user asked to see,
	// not new unread traffic: they are ingested as read.
	onDemand := e.Data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND
	for _, conv := range e.Data.GetConversations() {
		b.ingestConversation(ctx, cli, conv, onDemand)
	}
}

func (b *Backend) ingestConversation(ctx context.Context, cli waClient, conv *waHistorySync.Conversation, onDemand bool) {
	cj, err := types.ParseJID(conv.GetID())
	if err != nil || cj.IsEmpty() {
		return
	}
	if cj.Server == types.BroadcastServer || cj.Server == types.NewsletterServer {
		return
	}
	chat := WAHAFromJID(cj)
	if chat == "" || isBroadcastJID(chat) {
		return
	}
	key := chat
	if c := b.identify(chat); c != nil && c.ID != "" {
		key = c.ID
	}
	// Registry metadata of the chat.
	name := firstNonEmpty(conv.GetName(), conv.GetDisplayName())
	b.noteChat(chat, int64(conv.GetConversationTimestamp())*1000, name)
	b.cmu.Lock()
	if m := b.chats[chat]; m != nil {
		m.Unread = int(conv.GetUnreadCount())
		if conv.Archived != nil {
			m.Archived = conv.GetArchived()
		}
	}
	b.cmu.Unlock()

	msgs := conv.GetMessages()
	if len(msgs) == 0 {
		b.signalHistory(chat)
		return
	}
	var web []*waWeb.WebMessageInfo
	for _, hm := range msgs {
		if w := hm.GetMessage(); w != nil {
			web = append(web, w)
		}
	}
	sort.SliceStable(web, func(i, j int) bool { return web[i].GetMessageTimestamp() < web[j].GetMessageTimestamp() })

	b.ingestMu.Lock()
	pre := append([]*entry(nil), b.cache[key]...)
	b.ingestMu.Unlock()

	readIDs, deliveredIDs := map[string]bool{}, map[string]bool{}
	added := 0
	for _, w := range web {
		pm, err := cli.ParseWebMessage(cj, w)
		if err != nil || pm == nil {
			continue
		}
		for _, ev := range b.messageEvents(pm, true) {
			if ev.Type != proto.EventMessage {
				continue
			}
			d := parseInMsg(ev.Payload)
			ts, _ := toInt64(ev.Payload["timestamp"])
			b.ingestMu.Lock()
			if hit := b.detectEditLocked(key, d.ID, d.Text, d.IsMine, pre); hit != nil {
				b.applyEditLocked(ctx, key, hit.ID, d.Text, &d.IsMine, true)
				b.ingestMu.Unlock()
				continue
			}
			res := b.ingestLocked(ctx, key, d, ts, true, true)
			b.ingestMu.Unlock()
			if res == proto.IngestAdded {
				added++
			}
			if d.IsMine && d.ID != "" {
				switch w.GetStatus() {
				case waWeb.WebMessageInfo_READ, waWeb.WebMessageInfo_PLAYED:
					readIDs[d.ID] = true
				case waWeb.WebMessageInfo_DELIVERY_ACK:
					deliveredIDs[d.ID] = true
				}
			}
		}
	}
	// Receipts AFTER the ingestion (same ordering contract as the live path).
	emitReceipts := func(ids map[string]bool, read bool) {
		list := make([]string, 0, len(ids))
		for id := range ids {
			if !read && readIDs[id] {
				continue
			}
			list = append(list, id)
		}
		sort.Strings(list)
		for _, id := range list {
			b.enqueueEvent(proto.Event{Type: proto.EventReceipt, Protocol: proto.ProtocolWhatsApp, ContactID: key,
				Payload: map[string]any{"message_ids": []any{id}, "is_read": read}})
		}
	}
	emitReceipts(deliveredIDs, false)
	emitReceipts(readIDs, true)

	b.ingestMu.Lock()
	b.sortContactCacheLocked(key)
	b.ingestMu.Unlock()

	if added > 0 {
		b.ensureChatContact(key, name, conv)
	}
	// Align the read flags with the phone: only the newest UnreadCount incoming
	// messages stay unread (an on-demand sync brings old, already read history,
	// so everything it delivered is read). Without this every message ever
	// ingested counted as unread and the badge showed thousands.
	keep := int(conv.GetUnreadCount())
	if onDemand {
		keep = 0
	}
	if cutoff := int64(conv.GetConversationTimestamp()) * 1000; cutoff > 0 {
		b.reconcileChatUnread(ctx, key, keep, cutoff)
	}
	b.signalHistory(chat)
}

// reconcileChatUnread applies the phone's unread count to a chat: incoming
// messages up to cutoffMs are marked read except the newest keep, in the store
// and in the in-memory cache (markReadRemote reads the cache).
func (b *Backend) reconcileChatUnread(ctx context.Context, contactID string, keep int, cutoffMs int64) {
	if b.st == nil {
		return
	}
	if keep < 0 {
		keep = 0
	}
	var extra []string
	if c := b.identify(contactID); c != nil {
		extra = contactStorageKeys(*c)[1:]
	}
	if _, err := b.st.ReconcileChatUnread(ctx, proto.ProtocolWhatsApp, contactID, keep, cutoffMs, extra...); err != nil {
		b.log.Debug("WhatsApp unread reconcile failed", "chat", contactID, "err", err)
		return
	}
	keys := append([]string{contactID}, extra...)
	b.ingestMu.Lock()
	for _, k := range keys {
		entries := b.cache[k]
		var idx []int
		for i := range entries {
			if !entries[i].IsMine && (cutoffMs == 0 || entries[i].Timestamp <= cutoffMs) {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(x, y int) bool { return entries[idx[x]].Timestamp > entries[idx[y]].Timestamp })
		for n, i := range idx {
			if n < keep {
				continue
			}
			entries[i].Read = true
		}
	}
	b.ingestMu.Unlock()
}

// ensureChatContact makes a chat known to the contact list after a history
// sync brought it in (and tells the core with a contact_update).
func (b *Backend) ensureChatContact(id, name string, conv *waHistorySync.Conversation) {
	if _, ok := b.FindContact(id); ok {
		return
	}
	extras := map[string]any{
		"jid": id, "last_message_ts": int64(conv.GetConversationTimestamp()) * 1000,
		"source": "wa_chats", "is_chat_active": true, "archived": conv.GetArchived(),
	}
	if p := b.contactPhone(id); p != "" {
		extras["phone"] = p
	}
	if strings.HasSuffix(id, "@lid") {
		extras["lid"] = id
	}
	if name == "" {
		name = b.nameForChat(context.Background(), id)
	}
	if name == "" {
		if p := exStr(&proto.Contact{Extras: extras}, "phone"); p != "" {
			name = "+" + p
		} else {
			name = id
		}
	}
	c := newContact(id, name, extras)
	if b.RegisterContact(*c) {
		b.enqueueEvent(contactUpdateEvent(c))
	}
}

// ---- resync ------------------------------------------------------------------------------

// selectResyncTargets is _select_resync_targets: chats with unread messages
// first, then the most recent chats of the local DB, deterministic order.
func (b *Backend) selectResyncTargets(maxChats int) []string {
	meta := map[string]struct {
		unread bool
		ts     int64
	}{}
	b.cmu.RLock()
	for id, m := range b.chats {
		if !isFetchableJID(id) || isBroadcastJID(id) {
			continue
		}
		p := meta[id]
		meta[id] = struct {
			unread bool
			ts     int64
		}{p.unread || m.Unread > 0, maxInt64(p.ts, timestampMs(m.LastTS))}
	}
	b.cmu.RUnlock()
	b.ingestMu.Lock()
	for id, rows := range b.cache {
		if !isFetchableJID(id) || isBroadcastJID(id) {
			continue
		}
		var last int64
		for _, r := range rows {
			last = maxInt64(last, r.Timestamp)
		}
		p := meta[id]
		meta[id] = struct {
			unread bool
			ts     int64
		}{p.unread, maxInt64(p.ts, last)}
	}
	b.ingestMu.Unlock()
	ids := make([]string, 0, len(meta))
	for id := range meta {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, c := meta[ids[i]], meta[ids[j]]
		if a.unread != c.unread {
			return a.unread
		}
		if a.ts != c.ts {
			return a.ts > c.ts
		}
		return ids[i] < ids[j]
	})
	if maxChats > 0 && len(ids) > maxChats {
		ids = ids[:maxChats]
	}
	return ids
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ResyncHistory implements proto.HistoryResyncer (resync_history): at startup
// every relevant chat gets its history topped up (waves of resyncWorkers
// chats, staggered). It never fails per chat and returns the number of chats
// queried.
func (b *Backend) ResyncHistory(ctx context.Context, limit int) (int, error) {
	if !b.connected.Load() {
		return 0, nil
	}
	if limit <= 0 {
		limit = b.resyncLimit
	}
	workers := b.resyncWorkers
	if workers <= 0 {
		workers = 2
	}
	targets := b.selectResyncTargets(b.resyncMaxChats)
	for i := 0; i < len(targets); i += workers {
		end := i + workers
		if end > len(targets) {
			end = len(targets)
		}
		var wg sync.WaitGroup
		for _, jid := range targets[i:end] {
			wg.Add(1)
			go func(jid string) {
				defer wg.Done()
				if _, err := b.FetchHistory(ctx, jid, limit); err != nil {
					b.log.Debug("History fetch failed for a single chat", "err", err)
				}
			}(jid)
		}
		wg.Wait()
		if end < len(targets) {
			select {
			case <-time.After(resyncStagger):
			case <-ctx.Done():
				return len(targets), nil
			}
		}
	}
	return len(targets), nil
}
