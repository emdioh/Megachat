package whatsapp

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Typing actions (api.TypingStarted / api.TypingStopped).
const (
	typingStarted = "STARTED"
	typingStopped = "STOPPED"
)

// handleEvent is the whatsmeow event handler: it replaces the WAHA webhook
// (handle_webhook). It never blocks on network work.
func (b *Backend) handleEvent(evt any) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("WhatsApp event handler panicked", "event", fmt.Sprintf("%T", evt), "err", r)
		}
	}()
	switch e := evt.(type) {
	case *events.Message:
		b.onMessage(e)
	case *events.Receipt:
		b.onReceipt(e)
	case *events.ChatPresence:
		b.onChatPresence(e)
	case *events.HistorySync:
		go b.onHistorySync(e)
	case *events.IdentityChange:
		b.onIdentityChange(e)
	case *events.Archive:
		b.setChatArchivedLocal(WAHAFromJID(e.JID), e.Action != nil && e.Action.GetArchived())
	case *events.MarkChatAsRead:
		// The full-sync snapshot is the only heal for an existing DB: mark the
		// chat read only up to the boundary the phone reported, so newer
		// genuinely-unread traffic survives. An explicitly-unread action
		// carries no trustworthy boundary, so it changes nothing.
		if e.Action == nil || !e.Action.GetRead() {
			break
		}
		var cutoff int64
		if r := e.Action.GetMessageRange(); r != nil {
			cutoff = r.GetLastMessageTimestamp()
		}
		chat := WAHAFromJID(e.JID)
		b.cmu.Lock()
		// 0 means "no bound": it dominates any positive cutoff.
		if cur, ok := b.readOnPhone[chat]; !ok || (cur != 0 && cutoff > cur) {
			b.readOnPhone[chat] = cutoff
		}
		b.cmu.Unlock()
	case *events.PushName:
		b.onPushName(e)
	case *events.Connected:
		b.onConnected()
	case *events.Disconnected:
		b.connected.Store(false)
		b.setDisconnected(true)
		b.guard.noteDisconnected()
	case *events.StreamReplaced:
		b.connected.Store(false)
		b.log.Warn("WhatsApp stream replaced by another client")
	case *events.LoggedOut:
		b.onLoggedOut(e)
	case *events.PairSuccess:
		b.loggedOut.Store(false)
		b.log.Info("WhatsApp paired", "jid", e.ID.String())
	}
}

func (b *Backend) onConnected() {
	b.connected.Store(true)
	b.setDisconnected(false)
	b.stopped.Store(false)
	b.loggedOut.Store(false)
	b.guard.noteConnected()
}

func (b *Backend) onLoggedOut(e *events.LoggedOut) {
	b.connected.Store(false)
	b.loggedOut.Store(true)
	b.log.Warn("WhatsApp logged out: the device was unlinked, a new QR scan is required", "reason", e.Reason.String())
	b.guard.noteLoggedOut()
}

func (b *Backend) onPushName(e *events.PushName) {
	if e == nil || e.NewPushName == "" {
		return
	}
	id := WAHAFromJID(e.JID)
	b.noteChat(id, 0, "")
	b.cmu.Lock()
	if m := b.chats[id]; m != nil && (m.Name == "" || m.Name == id) {
		m.Name = e.NewPushName
	}
	var upd *proto.Contact
	if c := b.byJID[id]; c != nil && isPlaceholderDisplayName(c.DisplayName, c.ID, exStr(c, "phone")) {
		c.DisplayName = e.NewPushName
		upd = c
	}
	var ev proto.Event
	if upd != nil {
		ev = contactUpdateEvent(upd)
	}
	b.cmu.Unlock()
	if upd != nil {
		b.enqueueEvent(ev)
	}
}

// ---- messages -------------------------------------------------------------------------

// waMessageID is the WAHA serialized id of a whatsmeow message: "true_..." /
// "false_..." plus the participant in groups.
func waMessageID(info types.MessageInfo) string {
	chat := WAHAFromJID(info.Chat)
	participant := ""
	if info.IsGroup {
		participant = WAHAFromJID(info.Sender)
	}
	return BuildMsgID(info.IsFromMe, chat, info.ID, participant)
}

func (b *Backend) onMessage(e *events.Message) {
	evs := b.messageEvents(e, false)
	for _, ev := range evs {
		if ev.Type == proto.EventMessage {
			key := seenKey{ev.ContactID, getStr(ev.Payload, "id"), norm(getStr(ev.Payload, "text")), getStr(ev.Payload, "attachment_id")}
			if key.id != "" {
				b.ingestMu.Lock()
				_, dup := b.seen[key]
				b.seen[key] = struct{}{}
				b.ingestMu.Unlock()
				if dup {
					continue
				}
			}
		}
		b.enqueueEvent(ev)
	}
}

// resolveSenderName is _resolve_sender_name: a known contact's display name
// for a JID-like sender (exact id, then the number before "@"), then the
// address book (a group participant who never had a direct chat is not in the
// contact list), else sender.
func (b *Backend) resolveSenderName(sender string) string {
	if sender == "" || !strings.Contains(sender, "@") {
		return sender
	}
	b.cmu.RLock()
	if c := b.byJID[sender]; c != nil {
		name := c.DisplayName
		b.cmu.RUnlock()
		return name
	}
	number, _, _ := strings.Cut(sender, "@")
	if number != "" {
		for id, c := range b.byJID {
			if head, _, _ := strings.Cut(id, "@"); head == number {
				name := c.DisplayName
				b.cmu.RUnlock()
				return name
			}
		}
	}
	book := b.addressBook
	b.cmu.RUnlock()
	phone, lid := number, ""
	if strings.HasSuffix(sender, "@lid") {
		lid = sender
		phone = b.lidLookup(sender)
	}
	if n := cachedAddressBookName(book, phone, lid); n != "" {
		return n
	}
	return sender
}

var mentionRe = regexp.MustCompile(`@(\d{5,20})`)

// resolveTextMentions is _resolve_text_mentions: replaces a literal "@<digits>"
// mention with the contact name, or "@+<phone>" through the LID cache. An
// unresolvable run is left untouched.
func (b *Backend) resolveTextMentions(text string) string {
	if text == "" || !strings.Contains(text, "@") {
		return text
	}
	idx := mentionRe.FindAllStringSubmatchIndex(text, -1)
	if len(idx) == 0 {
		return text
	}
	var sb strings.Builder
	last := 0
	for _, m := range idx {
		if m[1] < len(text) && text[m[1]] >= '0' && text[m[1]] <= '9' {
			continue // (?!\d): a longer digit run is not a mention
		}
		digits := text[m[2]:m[3]]
		jid := digits + "@lid"
		repl := text[m[0]:m[1]]
		if r := b.resolveSenderName(jid); r != jid {
			repl = "@" + r
		} else if phone := b.lidLookup(jid); phone != "" {
			repl = "@+" + phone
		}
		sb.WriteString(text[last:m[0]])
		sb.WriteString(repl)
		last = m[1]
	}
	sb.WriteString(text[last:])
	return sb.String()
}

// quoteText is _wa_quote_text for a quoted waE2E.Message; ok=false is None.
// Media quotes use the filename/caption or the typed placeholder.
func quoteText(q *waE2E.Message) (string, bool) {
	if q == nil {
		return "", false
	}
	c := extractContent(q)
	var msgType, detail string
	switch {
	case c.Media != nil:
		switch c.Media.Kind {
		case "image", "gif":
			msgType = "image"
		case "video":
			msgType = "video"
		case "audio", "voice":
			msgType = "audio"
		case "sticker":
			msgType = "sticker"
		default:
			msgType = "attachment"
		}
		detail = strings.TrimSpace(firstNonEmpty(c.Media.FileName, c.Media.Caption))
		return mediaQuotePlaceholder(msgType, detail), true
	case c.Location != nil:
		return "📍 Posizione", true
	case c.Contact != "":
		return "👤 " + c.Contact, true
	}
	if t := strings.TrimSpace(c.Text); t != "" {
		// A reply to a picture from an older client carried the attachment
		// name as plain text ("true_<chat>_<ID>_<part>.jpeg"): show the
		// generic attachment placeholder instead of a serialized id.
		if wahaIDLikeRe.MatchString(t) {
			return mediaQuotePlaceholder("attachment", ""), true
		}
		return t, true
	}
	return "", false
}

// ownUser tells whether a JID string refers to the linked account.
func (b *Backend) ownUser(jid string) bool {
	if jid == "" {
		return false
	}
	pn, lid := b.ownIDs()
	u := strings.SplitN(normalizeWAHAID(jid), "@", 2)[0]
	for _, own := range []string{pn, lid} {
		if own != "" && strings.SplitN(own, "@", 2)[0] == u {
			return true
		}
	}
	return false
}

// quoteFields extracts the quote of a message (replyTo): text, timestamp,
// author and the id of the replied-to message in WAHA form.
func (b *Backend) quoteFields(chat string, isGroup, isMine bool, ci *waE2E.ContextInfo) (text any, ts any, author any, replyTo any) {
	if ci == nil || (ci.GetStanzaID() == "" && ci.GetQuotedMessage() == nil) {
		return nil, nil, nil, nil
	}
	if qt, ok := quoteText(ci.GetQuotedMessage()); ok {
		text = b.resolveTextMentions(qt)
	}
	participant := ci.GetParticipant()
	pWAHA := ""
	if participant != "" {
		pWAHA = normalizeWAHAID(participant)
	}
	if pWAHA != "" {
		author = pWAHA
	}
	if id := ci.GetStanzaID(); id != "" {
		if cached, _, ok := b.findCachedTwin(chat, id); ok {
			replyTo = cached.ID
			if cached.Timestamp != 0 {
				ts = cached.Timestamp
			}
		} else {
			quotedMine := !isMine
			if participant != "" {
				quotedMine = b.ownUser(participant)
			}
			part := ""
			if isGroup {
				part = pWAHA
				if quotedMine {
					if pn, lid := b.ownIDs(); lid != "" {
						part = lid
					} else {
						part = pn
					}
				}
			}
			replyTo = BuildMsgID(quotedMine, chat, id, part)
		}
	}
	return text, ts, author, replyTo
}

// messageEvents converts a whatsmeow message into backend events (the port of
// _event_from_message + the edit/reaction detection of handle_webhook).
// history marks messages that come from a history sync.
func (b *Backend) messageEvents(e *events.Message, history bool) []proto.Event {
	if e == nil || e.Message == nil {
		return nil
	}
	info := e.Info
	if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer || info.Chat.IsEmpty() {
		return nil
	}
	chat := WAHAFromJID(info.Chat)
	if chat == "" || isBroadcastJID(chat) {
		return nil
	}
	m := e.Message
	// HD dual upload: the hidden HD child duplicates the visible SD parent
	// (see isHDChild); dropping it here also skips its media prefetch.
	if isHDChild(m) {
		return nil
	}
	isMine := info.IsFromMe
	tsMs := info.Timestamp.UnixMilli()
	if info.Timestamp.IsZero() {
		tsMs = nowMs()
	}
	isGroup := strings.HasSuffix(chat, "@g.us")

	if rm := m.GetReactionMessage(); rm != nil {
		if ev, ok := b.reactionEvent(e, rm, chat, isMine, tsMs); ok {
			return []proto.Event{ev}
		}
		return nil
	}
	if pm := m.GetProtocolMessage(); pm != nil {
		if pm.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT {
			if ev, ok := b.editEvent(e, pm, chat, isMine, tsMs); ok {
				return []proto.Event{ev}
			}
		}
		return nil
	}

	c := extractContent(m)
	text := c.Text
	locationShown := false
	if c.Location != nil {
		b.saveLocationThumbnail(c.Location)
		text = locationText(c.Location)
		locationShown = true
	} else if c.Contact != "" && c.Media == nil && text == "" {
		text = "👤 " + c.Contact
	}
	if c.Location == nil {
		text = b.resolveTextMentions(text)
	}
	caption := ""
	if !locationShown {
		caption = strings.TrimSpace(text)
	}

	// Sender: push name, else the sender jid / chat / "You".
	sender := ""
	switch {
	case isMine:
		sender = "You"
	case isGroup:
		sender = firstNonEmpty(info.PushName, WAHAFromJID(info.Sender), chat)
	default:
		sender = firstNonEmpty(info.PushName, chat)
	}
	sender = b.resolveSenderName(sender)

	qText, qTS, qAuthor, replyTo := b.quoteFields(chat, isGroup, isMine, c.Ctx)
	msgID := waMessageID(info)
	if info.ID == "" {
		msgID = strconv.FormatInt(tsMs, 10)
	}

	payload := map[string]any{
		"id": msgID, "text": text, "is_mine": isMine, "sender": sender, "is_group": isGroup,
		"timestamp": tsMs, "quote_text": qText, "quote_timestamp": qTS, "quote_author": qAuthor,
		"reply_to_message_id": replyTo, "msg_type": "text", "attachment_info": nil, "attachment_id": nil,
		"media_kind": nil, "content_type": nil, "ack": nil, "contact": nil,
	}

	if c.Media != nil {
		mp := c.Media
		attName := b.attachmentName(msgID, mp)
		itemCaption := caption
		if locationShown {
			itemCaption = ""
		}
		fallback := "Media"
		attInfo, _ := mediaNameAndCaption(mp.Kind, itemCaption, mp.FileName, mp.Mime, fallback)
		attType := msgTypeForMediaKind(mp.Kind)
		msgText := itemCaption
		if attType == "image" {
			msgText = ""
		}
		payload["text"] = msgText
		payload["msg_type"] = attType
		payload["attachment_info"] = strOrNil(attInfo)
		payload["attachment_id"] = attName
		payload["media_kind"] = mp.Kind
		payload["content_type"] = strOrNil(mp.Mime)
		b.rememberMedia(chat, msgID, info, e, mp, attName)
		if !history {
			b.scheduleMediaPrefetch(chat, msgID, attName)
		}
	} else if strings.TrimSpace(text) == "" {
		// An empty bubble (service/ghost message): never created.
		return nil
	}

	if !history {
		b.scheduleContactLIDResolve(chat)
		pn := ""
		if !isMine && !isGroup {
			pn = info.PushName
		}
		b.noteChat(chat, tsMs, pn)
		b.presenceSubscribeLazy(chat)
	}

	return []proto.Event{{Type: proto.EventMessage, Protocol: proto.ProtocolWhatsApp, ContactID: chat, Payload: payload}}
}

// attachmentName is the attachment_id of a media message: the file name under
// which it lives in the media dir ("<serialized msg id>.<ext>", WAHA's own
// naming, so old and new files share one directory).
func (b *Backend) attachmentName(msgID string, mp *mediaPart) string {
	return sanitizeFileName(msgID) + mimeExtension(mp.Mime, mp.FileName)
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9@._-]`)

func sanitizeFileName(s string) string { return unsafeNameRe.ReplaceAllString(s, "_") }

// editPayload builds the message_edit event payload (see handle_webhook).
func (b *Backend) editPayload(chat, editID, text string, origTS, editTS int64, isMine bool, sender string) proto.Event {
	var et any
	if editTS != 0 {
		et = editTS
	}
	return proto.Event{Type: proto.EventMessageEdit, Protocol: proto.ProtocolWhatsApp, ContactID: chat, Payload: map[string]any{
		"edit_message_id": editID, "text": text, "timestamp": origTS, "edit_timestamp": et,
		"is_mine": isMine, "sender": sender, "contact": nil, "msg_type": "text",
	}}
}

// editEvent maps an incoming "message edit" protocol message to message_edit.
func (b *Backend) editEvent(e *events.Message, pm *waE2E.ProtocolMessage, chat string, isMine bool, tsMs int64) (proto.Event, bool) {
	newMsg := pm.GetEditedMessage()
	if newMsg == nil {
		return proto.Event{}, false
	}
	c := extractContent(newMsg)
	text := b.resolveTextMentions(c.Text)
	if strings.TrimSpace(text) == "" {
		return proto.Event{}, false
	}
	key := pm.GetKey()
	hex := key.GetID()
	if hex == "" {
		return proto.Event{}, false
	}
	targetMine := isMine
	if key.FromMe != nil {
		targetMine = key.GetFromMe()
	}
	editID := ""
	var orig int64
	if cached, found, ok := b.findCachedTwin(chat, hex); ok {
		editID, orig, targetMine = cached.ID, cached.Timestamp, cached.IsMine
		chat = found // the core applies the edit under the key the message lives in
	} else {
		part := ""
		if e.Info.IsGroup {
			part = WAHAFromJID(e.Info.Sender)
			if key.GetParticipant() != "" {
				part = normalizeWAHAID(key.GetParticipant())
			}
		}
		editID = BuildMsgID(targetMine, chat, hex, part)
		orig = tsMs
	}
	sender := "You"
	if !isMine {
		sender = b.resolveSenderName(firstNonEmpty(e.Info.PushName, chat))
	}
	ev := b.editPayload(chat, editID, text, orig, 0, targetMine, sender)
	if ms := pm.GetTimestampMS(); ms != 0 {
		ev.Payload["edit_timestamp"] = ms
	} else {
		ev.Payload["edit_timestamp"] = tsMs
	}
	return ev, true
}

// reactionEvent is _event_from_reaction: a reaction delta.
func (b *Backend) reactionEvent(e *events.Message, rm *waE2E.ReactionMessage, chat string, isMine bool, tsMs int64) (proto.Event, bool) {
	key := rm.GetKey()
	hex := key.GetID()
	if hex == "" {
		return proto.Event{}, false
	}
	var author, authorKey string
	if isMine {
		authorKey, author = "me", "You"
	} else {
		authorKey = WAHAFromJID(e.Info.Sender)
		if authorKey == "" {
			authorKey = chat
		}
		author = b.resolveSenderName(authorKey)
	}
	targetMine := key.GetFromMe()
	var targetID string
	if cached, found, ok := b.findCachedTwin(chat, hex); ok {
		targetID = cached.ID
		chat = found // the reaction is stored under the key the message lives in
	} else {
		part := ""
		if e.Info.IsGroup {
			part = normalizeWAHAID(key.GetParticipant())
			if targetMine {
				if pn, lid := b.ownIDs(); lid != "" {
					part = lid
				} else {
					part = pn
				}
			}
		}
		targetID = BuildMsgID(targetMine, chat, hex, part)
	}
	emoji := rm.GetText()
	ts := tsMs
	if ms := rm.GetSenderTimestampMS(); ms != 0 {
		ts = ms
	}
	return proto.Event{Type: proto.EventReactionUpdate, Protocol: proto.ProtocolWhatsApp, ContactID: chat, Payload: map[string]any{
		"target_message_id": targetID, "target_timestamp": nil, "mode": "delta", "emoji": emoji,
		"is_remove": emoji == "", "author": author, "author_key": authorKey, "is_mine": isMine,
		"timestamp": ts, "contact": nil,
	}}, true
}

// saveLocationThumbnail stores the map preview WhatsApp ships with a shared
// location (first one wins), keyed by coordinates like the Python helper.
func (b *Backend) saveLocationThumbnail(l *locationPart) {
	if len(l.Thumbnail) == 0 || len(l.Thumbnail) > 256*1024 || b.thumbsDir == "" {
		return
	}
	if len(l.Thumbnail) < 3 || l.Thumbnail[0] != 0xff || l.Thumbnail[1] != 0xd8 || l.Thumbnail[2] != 0xff {
		return
	}
	path := b.thumbsDir + "/" + fmt.Sprintf("%.6f_%.6f.jpg", l.Lat, l.Lng)
	if isFile(path) {
		return
	}
	if err := os.MkdirAll(b.thumbsDir, 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, l.Thumbnail, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// ---- receipts, presence, identity ----------------------------------------------------

// onReceipt maps delivery/read receipts of OUR messages to receipt events (the
// message.ack path of Python: only fromMe messages with ack >= DEVICE).
func (b *Backend) onReceipt(e *events.Receipt) {
	chat := WAHAFromJID(e.Chat)
	if chat == "" || isBroadcastJID(chat) {
		return
	}
	if e.IsFromMe {
		// One of our other devices read the chat: the phone has read it.
		if e.Type == types.ReceiptTypeRead || e.Type == types.ReceiptTypeReadSelf {
			b.cmu.Lock()
			b.readOnPhone[chat] = 0
			b.cmu.Unlock()
		}
		return
	}
	var isRead bool
	switch e.Type {
	case types.ReceiptTypeDelivered:
		isRead = false
	case types.ReceiptTypeRead, types.ReceiptTypePlayed, types.ReceiptTypeReadSelf:
		isRead = true
	default:
		return // sender / retry / server-error: not a delivery state
	}
	ids := make([]any, 0, len(e.MessageIDs))
	for _, id := range e.MessageIDs {
		if c := CanonicalMsgID(string(id)); c != "" {
			ids = append(ids, c)
		}
	}
	if len(ids) == 0 {
		return
	}
	b.enqueueEvent(proto.Event{Type: proto.EventReceipt, Protocol: proto.ProtocolWhatsApp, ContactID: chat,
		Payload: map[string]any{"message_ids": ids, "is_read": isRead}})
}

// onChatPresence is _event_from_typing: composing/recording -> STARTED,
// paused -> STOPPED.
func (b *Backend) onChatPresence(e *events.ChatPresence) {
	chat := WAHAFromJID(e.Chat)
	if chat == "" {
		return
	}
	var action string
	switch e.State {
	case types.ChatPresenceComposing:
		action = typingStarted
	case types.ChatPresencePaused:
		action = typingStopped
	default:
		return
	}
	b.enqueueEvent(proto.Event{Type: proto.EventTyping, Protocol: proto.ProtocolWhatsApp, ContactID: chat, Payload: map[string]any{"action": action}})
}

// onIdentityChange is _wa_security_notice_text: the "security code changed"
// notice (WEBJS e2e_notification subtype identity) as a system message.
func (b *Backend) onIdentityChange(e *events.IdentityChange) {
	if e == nil || e.Implicit {
		return
	}
	chat := WAHAFromJID(e.JID)
	if chat == "" || isBroadcastJID(chat) {
		return
	}
	text := b.securityNoticeText(chat)
	ts := e.Timestamp.UnixMilli()
	if e.Timestamp.IsZero() {
		ts = nowMs()
	}
	b.enqueueEvent(proto.Event{Type: proto.EventMessage, Protocol: proto.ProtocolWhatsApp, ContactID: chat, Payload: map[string]any{
		"id": strconv.FormatInt(ts, 10), "text": text, "is_mine": false, "sender": "", "is_group": false,
		"timestamp": ts, "quote_text": nil, "quote_timestamp": nil, "quote_author": nil, "reply_to_message_id": nil,
		"msg_type": "system", "attachment_info": nil, "attachment_id": nil, "media_kind": nil,
		"content_type": nil, "ack": nil, "contact": nil,
	}})
}

// securityNoticeText renders the notice for a contact id.
func (b *Backend) securityNoticeText(jid string) string {
	name := ""
	if r := b.resolveSenderName(jid); r != jid {
		name = r
	} else if phone := b.lidLookup(jid); phone != "" && strings.HasSuffix(jid, "@lid") {
		name = "+" + phone
	} else if strings.HasSuffix(jid, "@c.us") {
		name = "+" + strings.SplitN(jid, "@", 2)[0]
	}
	if name != "" {
		return "Il tuo codice di sicurezza con " + name + " è cambiato."
	}
	return "Il codice di sicurezza di un contatto è cambiato."
}

// ---- presence subscription -----------------------------------------------------------

// presenceEnabled mirrors _PRESENCE_SUBSCRIBE_ENABLED (WAHA_PRESENCE_SUBSCRIBE):
// the typing indicator is off by default because announcing "available" mutes
// the phone's push notifications.
func presenceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WAHA_PRESENCE_SUBSCRIBE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// presenceSubscribeLazy subscribes a chat's presence on first activity.
func (b *Backend) presenceSubscribeLazy(chat string) {
	if !presenceEnabled() || chat == "" || strings.HasSuffix(chat, "@g.us") {
		return
	}
	b.cmu.Lock()
	if b.presenceSubscribed == nil {
		b.presenceSubscribed = map[string]bool{}
	}
	if b.presenceSubscribed[chat] {
		b.cmu.Unlock()
		return
	}
	b.presenceSubscribed[chat] = true
	b.cmu.Unlock()
	go b.presenceSubscribe(chat)
}

func (b *Backend) presenceSubscribe(chat string) {
	cli := b.currentClient()
	if cli == nil {
		return
	}
	j, err := JIDFromWAHA(chat)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cli.SubscribePresence(ctx, j); err != nil {
		b.log.Debug("presence subscribe failed", "chat", chat, "err", err)
	}
}

// StartPresenceSubscribe implements proto.PresenceSubscriber
// (start_presence_subscribe): a sweep over the known chats, a no-op unless the
// typing indicator is enabled.
func (b *Backend) StartPresenceSubscribe(ctx context.Context) error {
	if !presenceEnabled() {
		return nil
	}
	cli := b.currentClient()
	if cli == nil {
		return ErrNotConnected
	}
	_ = cli.SendPresence(ctx, types.PresenceAvailable)
	b.cmu.RLock()
	var ids []string
	for _, c := range b.contacts {
		if c.ID != "" && !strings.HasSuffix(c.ID, "@g.us") {
			ids = append(ids, c.ID)
		}
	}
	b.cmu.RUnlock()
	go func() {
		for _, id := range ids {
			b.presenceSubscribeLazy(id)
			time.Sleep(300 * time.Millisecond)
		}
	}()
	return nil
}
