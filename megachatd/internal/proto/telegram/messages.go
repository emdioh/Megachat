package telegram

import (
	"context"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/tg"
)

func entityMapsFrom(e tg.Entities) entityMaps {
	m := entityMaps{users: e.Users, chats: e.Chats, channels: e.Channels, others: map[int64]string{}}
	if m.users == nil {
		m.users = map[int64]*tg.User{}
	}
	if m.chats == nil {
		m.chats = map[int64]*tg.Chat{}
	}
	if m.channels == nil {
		m.channels = map[int64]*tg.Channel{}
	}
	return m
}

// senderName mirrors the sender block of _message_to_chat_event: the sender
// entity's first/last name, or its id when blank; "" when unresolved.
func (b *Backend) senderName(msg *tg.Message, ents entityMaps) string {
	var peer tg.PeerClass = msg.FromID
	if peer == nil {
		switch p := msg.PeerID.(type) {
		case *tg.PeerUser:
			if msg.Out {
				if me := b.selfUser(); me != nil {
					return nameOrID(me)
				}
				return ""
			}
			peer = p
		default:
			peer = msg.PeerID
		}
	}
	switch p := peer.(type) {
	case *tg.PeerUser:
		if u, ok := ents.users[p.UserID]; ok {
			return nameOrID(u)
		}
		if me := b.selfUser(); me != nil && me.ID == p.UserID {
			return nameOrID(me)
		}
	case *tg.PeerChat:
		if _, ok := ents.chats[p.ChatID]; ok {
			return strconv.FormatInt(p.ChatID, 10)
		}
	case *tg.PeerChannel:
		if _, ok := ents.channels[p.ChannelID]; ok {
			return strconv.FormatInt(p.ChannelID, 10)
		}
	}
	return ""
}

func nameOrID(u *tg.User) string {
	s := u.FirstName
	if u.LastName != "" {
		s += " " + u.LastName
	}
	if strings.TrimSpace(s) == "" {
		s = strconv.FormatInt(u.ID, 10)
	}
	return s
}

// mediaRef builds a lazy-download reference.
func mediaRef(chatID string, msgID int) string {
	return tgrefPrefix + chatID + ":" + strconv.Itoa(msgID)
}

// messageToEvent is _message_to_chat_event. A reaction snapshot event, if the
// message carries reactions, is queued BEFORE the returned message event
// (Python enqueues it as a side effect). nil = message dropped.
func (b *Backend) messageToEvent(msg *tg.Message, ents entityMaps, attachmentID string) *proto.Event {
	chatID, ok := peerContactID(msg.PeerID)
	if !ok || chatID == "0" {
		return nil
	}
	text := unparseMarkdown(msg.Message, msg.Entities)
	sender := b.senderName(msg, ents)
	ts := int64(msg.Date) * 1000

	msgType := "text"
	attID := attachmentID
	var attachmentInfo string
	kind, filename, mime := tgMediaKind(msg)
	var contentType string
	if kind != "" {
		msgType = msgTypeForMediaKind(kind)
		attachmentInfo = text
		if attachmentInfo == "" {
			attachmentInfo = tgLabelFor(kind, filename)
		}
		if kind == "image" || kind == "sticker" {
			text = ""
		}
		contentType = mime
	}
	mi := messageMedia(msg)
	if attID == "" && mi.any() && msg.ID != 0 {
		attID = mediaRef(chatID, msg.ID)
	}

	var (
		quoteText, quoteAtt, quoteCT, replyTo string
		quoteTextOK                           bool
	)
	if rh, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
		if id, has := rh.GetReplyToMsgID(); has && id != 0 {
			replyTo = strconv.Itoa(id)
			b.ingestMu.Lock()
			for _, m := range b.cache[chatID] {
				if m.ID == replyTo {
					quoteText, quoteTextOK = tgQuoteTextFromCached(m)
					quoteAtt = m.AttachmentID
					quoteCT = m.ContentType
					break
				}
			}
			b.ingestMu.Unlock()
		}
	}

	if text == "" && !mi.any() && msg.ReplyTo == nil {
		return nil
	}

	var qt any
	if quoteTextOK {
		qt = quoteText
	}
	payload := map[string]any{
		"id":                  strconv.Itoa(msg.ID),
		"text":                text,
		"is_mine":             msg.Out,
		"sender":              sender,
		"timestamp":           ts,
		"quote_text":          qt,
		"quote_attachment_id": strOrNil(quoteAtt),
		"quote_content_type":  strOrNil(quoteCT),
		"reply_to_message_id": strOrNil(replyTo),
		"msg_type":            msgType,
		"attachment_info":     strOrNil(attachmentInfo),
		"attachment_id":       strOrNil(attID),
		"media_kind":          strOrNil(kind),
		"content_type":        strOrNil(contentType),
		"protocol":            proto.ProtocolTelegram,
		"contact":             b.contactValue(chatID),
	}
	if rev := b.reactionsEventFromMsg(msg, chatID); rev != nil {
		b.queue.put(*rev)
	}
	return &proto.Event{Type: proto.EventMessage, Protocol: proto.ProtocolTelegram, ContactID: chatID, Payload: payload}
}

// reactionsEventFromMsg is _reactions_event_from_msg.
func (b *Backend) reactionsEventFromMsg(msg *tg.Message, chatID string) *proto.Event {
	r, ok := msg.GetReactions()
	if !ok || len(r.Results) == 0 {
		return nil
	}
	return &proto.Event{Type: proto.EventReactionUpdate, Protocol: proto.ProtocolTelegram, ContactID: chatID, Payload: map[string]any{
		"mode": "snapshot", "snapshot": b.buildReactionsSnapshot(r), "target_message_id": strconv.Itoa(msg.ID),
		"target_timestamp": nil, "timestamp": nowMs(), "contact": b.contactValue(chatID),
	}}
}

// buildReactionsSnapshot is _build_reactions_snapshot.
func (b *Backend) buildReactionsSnapshot(r tg.MessageReactions) []map[string]any {
	all := r.RecentReactions
	var visible []tg.MessagePeerReaction
	if r.CanSeeList {
		visible = r.RecentReactions
	}
	snapshot := []map[string]any{}
	for _, rc := range r.Results {
		re, ok := rc.Reaction.(*tg.ReactionEmoji)
		if !ok {
			continue // custom emoji and paid reactions are skipped
		}
		emoticon := re.Emoticon
		my := false
		for _, pr := range all {
			if e, ok := pr.Reaction.(*tg.ReactionEmoji); ok && e.Emoticon == emoticon && pr.My {
				my = true
			}
		}
		authors := []string{}
		for _, pr := range visible {
			e, ok := pr.Reaction.(*tg.ReactionEmoji)
			if !ok || e.Emoticon != emoticon {
				continue
			}
			var rawID int64
			switch p := pr.PeerID.(type) {
			case *tg.PeerUser:
				rawID = p.UserID
			case *tg.PeerChat:
				rawID = p.ChatID
			case *tg.PeerChannel:
				rawID = p.ChannelID
			default:
				continue
			}
			authorID, _ := peerContactID(pr.PeerID)
			author := strconv.FormatInt(rawID, 10)
			if c := b.identifyContact(authorID); c != nil {
				author = c.DisplayName
			}
			if author != "" {
				dup := false
				for _, a := range authors {
					if a == author {
						dup = true
					}
				}
				if !dup {
					authors = append(authors, author)
				}
			}
		}
		_, chosen := rc.GetChosenOrder()
		snapshot = append(snapshot, map[string]any{
			"emoji": emoticon, "count": int64(rc.Count), "is_mine": chosen || my, "authors": authors,
		})
	}
	return snapshot
}

// ---- live handlers -----------------------------------------------------------------

func (b *Backend) registerHandlers(d *tg.UpdateDispatcher) {
	onNew := func(ctx context.Context, e tg.Entities, m tg.MessageClass) error {
		b.peers.learnEntities(e)
		if msg, ok := m.(*tg.Message); ok {
			b.handleNewMessage(ctx, msg, entityMapsFrom(e))
		}
		return nil
	}
	d.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		return onNew(ctx, e, u.Message)
	})
	d.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return onNew(ctx, e, u.Message)
	})
	onEdit := func(e tg.Entities, m tg.MessageClass) error {
		b.peers.learnEntities(e)
		if msg, ok := m.(*tg.Message); ok {
			b.handleMessageEdited(msg)
		}
		return nil
	}
	d.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return onEdit(e, u.Message)
	})
	d.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return onEdit(e, u.Message)
	})
	d.OnReadHistoryOutbox(func(ctx context.Context, e tg.Entities, u *tg.UpdateReadHistoryOutbox) error {
		b.handleReadReceipt(u)
		return nil
	})
	d.OnMessageReactions(func(ctx context.Context, e tg.Entities, u *tg.UpdateMessageReactions) error {
		b.peers.learnEntities(e)
		b.handleReactionsUpdate(u)
		return nil
	})
	d.OnUserTyping(func(ctx context.Context, e tg.Entities, u *tg.UpdateUserTyping) error {
		b.handleTyping(strconv.FormatInt(markedID(PeerUser, u.UserID), 10), u.Action)
		return nil
	})
	d.OnChatUserTyping(func(ctx context.Context, e tg.Entities, u *tg.UpdateChatUserTyping) error {
		b.handleTyping(strconv.FormatInt(markedID(PeerChat, u.ChatID), 10), u.Action)
		return nil
	})
	d.OnChannelUserTyping(func(ctx context.Context, e tg.Entities, u *tg.UpdateChannelUserTyping) error {
		b.handleTyping(strconv.FormatInt(markedID(PeerChannel, u.ChannelID), 10), u.Action)
		return nil
	})
}

// handleNewMessage is _handle_new_message: download the media, then queue.
func (b *Backend) handleNewMessage(ctx context.Context, msg *tg.Message, ents entityMaps) {
	var attachmentID string
	if messageMedia(msg).any() {
		if api, err := b.getAPI(); err == nil {
			path, err := b.downloadMedia(ctx, api, msg)
			if err != nil {
				b.log.Error("Telegram: failed to download media", "err", err)
			} else if path != "" {
				attachmentID = path
				b.log.Info("Telegram: downloaded media", "path", path)
			}
		}
	}
	if ev := b.messageToEvent(msg, ents, attachmentID); ev != nil {
		b.queue.put(*ev)
	}
}

// handleMessageEdited is _handle_message_edited (text edits only).
func (b *Backend) handleMessageEdited(msg *tg.Message) {
	chatID, ok := peerContactID(msg.PeerID)
	if !ok || chatID == "0" {
		return
	}
	if messageMedia(msg).any() {
		return // caption/media edits are out of scope
	}
	newText := unparseMarkdown(msg.Message, msg.Entities)
	if strings.TrimSpace(newText) == "" {
		return
	}
	var editTS any
	if ed, ok := msg.GetEditDate(); ok && ed != 0 {
		editTS = int64(ed) * 1000
	}
	b.emit(proto.EventMessageEdit, chatID, map[string]any{
		"edit_message_id": strconv.Itoa(msg.ID),
		"text":            newText,
		"timestamp":       int64(msg.Date) * 1000, // original timestamp
		"edit_timestamp":  editTS,
		"is_mine":         msg.Out,
		"sender":          "",
		"contact":         b.contactValue(chatID),
		"msg_type":        "text",
	})
}

// handleReadReceipt is _handle_read_receipt: a pure translator.
func (b *Backend) handleReadReceipt(u *tg.UpdateReadHistoryOutbox) {
	contactID, ok := peerContactID(u.Peer)
	if !ok {
		return
	}
	var ids []string
	b.ingestMu.Lock()
	for _, m := range b.cache[contactID] {
		if m.ID == "" || !m.IsMine {
			continue
		}
		if mid, ok := toInt(m.ID); ok && mid <= int64(u.MaxID) {
			ids = append(ids, m.ID)
		}
	}
	b.ingestMu.Unlock()
	if len(ids) > 0 {
		b.emit(proto.EventReceipt, contactID, map[string]any{"message_ids": ids, "is_read": true})
	}
}

// handleReactionsUpdate is _handle_reactions_update.
func (b *Backend) handleReactionsUpdate(u *tg.UpdateMessageReactions) {
	contactID, ok := peerContactID(u.Peer)
	if !ok {
		return
	}
	b.emit(proto.EventReactionUpdate, contactID, map[string]any{
		"mode": "snapshot", "snapshot": b.buildReactionsSnapshot(u.Reactions),
		"target_message_id": strconv.Itoa(u.MsgID), "target_timestamp": nil,
		"timestamp": nowMs(), "contact": b.contactValue(contactID),
	})
}

// handleTyping is _handle_typing_update: any action but cancel is STARTED.
func (b *Backend) handleTyping(contactID string, action tg.SendMessageActionClass) {
	act := "STARTED"
	if _, cancel := action.(*tg.SendMessageCancelAction); cancel {
		act = "STOPPED"
	}
	b.emit(proto.EventTyping, contactID, map[string]any{"action": act})
}
