package whatsapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	gproto "google.golang.org/protobuf/proto"
)

const (
	sendTimeout       = 60 * time.Second
	attachmentTimeout = 180 * time.Second
	maxUploadBytes    = 100 << 20
)

// targetJID converts a contact id into a send target.
func targetJID(contactID string) (types.JID, error) {
	j, err := JIDFromWAHA(contactID)
	if err != nil || strings.HasSuffix(contactID, "@broadcast") {
		return types.EmptyJID, fmt.Errorf("%w: %q", ErrInvalidTarget, contactID)
	}
	return j, nil
}

// resolveSendAlias registers the reverse {phone}@c.us -> @lid alias before a
// send to a @lid chat (the port of _resolve_send_chat_id's
// _register_send_alias). WhatsApp reflects the send echo/ack with the @c.us
// chat, so the alias lets the echo be ingested under the @lid id instead of
// creating a transient @c.us twin. Unresolvable @lid chats are left alone.
func (b *Backend) resolveSendAlias(contactID string) {
	if !strings.HasSuffix(contactID, "@lid") {
		return
	}
	phone := b.lidLookup(contactID)
	if phone == "" {
		return
	}
	b.registerSendAlias(contactID, phone)
}

// ownJIDFor is the own JID to use as quote participant in a chat.
func (b *Backend) ownJIDFor(chat types.JID) types.JID {
	cli := b.currentClient()
	if cli == nil {
		return types.EmptyJID
	}
	d := cli.Device()
	if d == nil {
		return types.EmptyJID
	}
	if (chat.Server == types.HiddenUserServer || chat.Server == types.GroupServer) && !d.LID.IsEmpty() {
		return d.LID.ToNonAD()
	}
	if d.ID != nil {
		return d.ID.ToNonAD()
	}
	return types.EmptyJID
}

// contextFor builds the ContextInfo of a reply (reply_to_message_id and the
// shown quote text), or nil when the send is not a reply.
func (b *Backend) contextFor(chat types.JID, contactID string, q proto.Quote) *waE2E.ContextInfo {
	if q.ReplyToMessageID == "" {
		return nil
	}
	p := ParseMsgID(q.ReplyToMessageID)
	if p.ID == "" {
		return nil
	}
	ci := &waE2E.ContextInfo{StanzaID: gproto.String(p.ID)}
	fromMe := p.FromMe
	// The quoted row (cache, then store) decides what the recipient's quote
	// preview shows: q.Message is client text and may be an attachment name.
	row := b.quotedRow(contactID, q.ReplyToMessageID)
	if row != nil && !p.Serialized {
		fromMe = row.IsMine
	}
	switch {
	case fromMe:
		if own := b.ownJIDFor(chat); !own.IsEmpty() {
			ci.Participant = gproto.String(own.String())
		}
	case chat.Server == types.GroupServer:
		if p.Participant != "" {
			if pj, err := JIDFromWAHA(p.Participant); err == nil {
				ci.Participant = gproto.String(pj.String())
			}
		} else if q.Author != "" {
			if pj, err := JIDFromWAHA(q.Author); err == nil {
				ci.Participant = gproto.String(pj.String())
			}
		}
	default:
		ci.Participant = gproto.String(chat.String())
	}
	if qm := b.quotedMessage(row, q.Message); qm != nil {
		ci.QuotedMessage = qm
	}
	return ci
}

// serializedID is the WAHA id of a message we sent.
func (b *Backend) serializedID(contactID string, id types.MessageID) string {
	part := ""
	if strings.HasSuffix(contactID, "@g.us") {
		if pn, lid := b.ownIDs(); lid != "" {
			part = lid
		} else {
			part = pn
		}
	}
	return BuildMsgID(true, contactID, string(id), part)
}

// SendMessage implements proto.Backend (send_message_sync): sends text and
// returns the WAHA-style id of the new message.
func (b *Backend) SendMessage(ctx context.Context, contactID, text string, quote proto.Quote) (string, error) {
	cli, err := b.requireClient()
	if err != nil {
		return "", err
	}
	to, err := targetJID(contactID)
	if err != nil {
		return "", err
	}
	var msg *waE2E.Message
	if ci := b.contextFor(to, contactID, quote); ci != nil {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: gproto.String(text), ContextInfo: ci}}
	} else {
		msg = &waE2E.Message{Conversation: gproto.String(text)}
	}
	return b.send(ctx, cli, to, contactID, msg, sendTimeout)
}

func (b *Backend) send(ctx context.Context, cli waClient, to types.JID, contactID string, msg *waE2E.Message, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b.resolveSendAlias(contactID)
	id := cli.GenerateMessageID()
	resp, err := cli.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{ID: id})
	if err != nil {
		return "", fmt.Errorf("WhatsApp send failed: %w", err)
	}
	if resp.ID != "" {
		id = resp.ID
	}
	b.noteChat(contactID, nowMs(), "")
	return b.serializedID(contactID, id), nil
}

// ---- attachments ------------------------------------------------------------------------

func mediaTypeFor(kind string) whatsmeow.MediaType {
	switch kind {
	case "image", "gif", "sticker":
		return whatsmeow.MediaImage
	case "video":
		return whatsmeow.MediaVideo
	case "audio", "voice":
		return whatsmeow.MediaAudio
	}
	return whatsmeow.MediaDocument
}

// sendKind is the WAHA routing: image/gif -> sendImage, video -> sendVideo,
// everything else -> sendFile (a document).
func sendKind(att proto.Attachment, mimeType string) string {
	kind := att.MediaKind
	if kind == "" {
		kind = mediaKindFromMime(mimeType, false, false)
	}
	switch kind {
	case "image", "gif":
		return "image"
	case "video":
		return "video"
	}
	return "document"
}

func detectMime(att proto.Attachment, data []byte) string {
	if att.MimeType != "" {
		return att.MimeType
	}
	name := att.Filename
	if name == "" {
		name = att.Path
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return http.DetectContentType(data)
}

// buildMediaMessage uploads the file and builds the message of its kind.
func (b *Backend) buildMediaMessage(ctx context.Context, cli waClient, att proto.Attachment, ci *waE2E.ContextInfo) (*waE2E.Message, error) {
	st, err := os.Stat(att.Path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxUploadBytes {
		return nil, fmt.Errorf("WhatsApp: file too large (%d bytes)", st.Size())
	}
	data, err := os.ReadFile(att.Path)
	if err != nil {
		return nil, err
	}
	mimeType := detectMime(att, data)
	kind := sendKind(att, mimeType)
	up, err := cli.Upload(ctx, data, mediaTypeFor(kind))
	if err != nil {
		return nil, fmt.Errorf("WhatsApp upload failed: %w", err)
	}
	filename := att.Filename
	if filename == "" {
		filename = filepath.Base(att.Path)
	}
	var caption *string
	if att.Caption != "" {
		caption = gproto.String(att.Caption)
	}
	switch kind {
	case "image":
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: gproto.String(mimeType),
			Caption: caption, ContextInfo: ci,
		}}, nil
	case "video":
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: gproto.String(mimeType),
			Caption: caption, ContextInfo: ci,
		}}, nil
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
		FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: gproto.String(mimeType),
		FileName: gproto.String(filename), Title: gproto.String(filename), Caption: caption, ContextInfo: ci,
	}}, nil
}

// SendAttachment implements proto.AttachmentSender (send_attachment_sync).
func (b *Backend) SendAttachment(ctx context.Context, contactID string, att proto.Attachment, quote proto.Quote) (string, error) {
	cli, err := b.requireClient()
	if err != nil {
		return "", err
	}
	to, err := targetJID(contactID)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentTimeout)
	defer cancel()
	msg, err := b.buildMediaMessage(ctx, cli, att, b.contextFor(to, contactID, quote))
	if err != nil {
		return "", err
	}
	return b.send(ctx, cli, to, contactID, msg, attachmentTimeout)
}

// SendAttachments implements proto.BatchAttachmentSender
// (send_attachments_sync): N attachments become N messages (WhatsApp has no
// batch API). The caption and the quote apply to the FIRST message only; the
// first failure stops the batch (already delivered messages stay).
func (b *Backend) SendAttachments(ctx context.Context, contactID string, atts []proto.Attachment, batchID string, quote proto.Quote) ([]string, error) {
	var ids []string
	for i, att := range atts {
		a := att
		q := proto.Quote{}
		if i == 0 {
			q = quote
		} else {
			a.Caption = ""
		}
		id, err := b.SendAttachment(ctx, contactID, a, q)
		if err != nil {
			b.log.Error("WhatsApp multi-attach failed", "index", i, "total", len(atts), "sent", len(ids), "err", err)
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// EnqueueSentMessage implements proto.SentMirror (enqueue_sent_message): the
// facade's successful send is published through the receive queue; an
// attachment is mirrored as "sent-<uuid><ext>" in the media dir.
func (b *Backend) EnqueueSentMessage(ctx context.Context, m proto.SentMessage) error {
	isAtt := m.AttachmentPath != ""
	kind := ""
	if isAtt {
		kind = m.MediaKind
		if kind == "" {
			kind = mediaKindFromMime(m.MimeType, false, false)
		}
		if kind == "" {
			kind = "document"
		}
	}
	msgType := "text"
	if kind != "" {
		msgType = msgTypeForMediaKind(kind)
	}
	text := m.Text
	if msgType == "image" {
		text = ""
	}
	var attachmentID any
	if isAtt {
		dir, err := b.EnsureMediaDir()
		if err != nil {
			return err
		}
		var rnd [16]byte
		_, _ = rand.Read(rnd[:])
		name := "sent-" + hex.EncodeToString(rnd[:]) + strings.ToLower(filepath.Ext(m.AttachmentPath))
		if err := copyFile(m.AttachmentPath, filepath.Join(dir, name)); err != nil {
			return err
		}
		attachmentID = name
	}
	var info any
	if isAtt {
		if msgType == "image" {
			info = strOrNil(firstNonEmpty(m.Text, m.Filename))
		} else {
			info = strOrNil(m.Filename)
		}
	}
	var quoteTS any
	if m.Quote.Timestamp != 0 {
		quoteTS = m.Quote.Timestamp
	}
	var batchIndex any
	if m.BatchIndex != nil {
		batchIndex = int64(*m.BatchIndex)
	}
	b.emit(proto.EventMessage, m.ContactID, map[string]any{
		"id": m.MessageID, "text": text, "is_mine": true, "sender": "You", "timestamp": nowMs(),
		"quote_text": strOrNil(m.Quote.Message), "quote_timestamp": quoteTS, "quote_author": strOrNil(m.Quote.Author),
		"reply_to_message_id": strOrNil(m.Quote.ReplyToMessageID), "msg_type": msgType,
		"attachment_info": info, "attachment_id": attachmentID, "content_type": strOrNil(m.MimeType),
		"media_kind": strOrNil(kind), "batch_id": strOrNil(m.BatchID), "batch_index": batchIndex,
	})
	return nil
}

// EnqueueSentNotification implements proto.SentMirror; WhatsApp mirrors its
// own rows, so there is no lightweight notification.
func (b *Backend) EnqueueSentNotification(ctx context.Context, contactID, messageID string, timestamp int64, batchID string) error {
	return proto.ErrUnsupported
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// ---- edit / reactions / receipts -------------------------------------------------------------

// EditMessage implements proto.Editor (edit_message_sync).
func (b *Backend) EditMessage(ctx context.Context, contactID, messageID, newText string) (bool, error) {
	cli, err := b.requireClient()
	if err != nil {
		return false, err
	}
	to, err := targetJID(contactID)
	if err != nil {
		return false, err
	}
	p := ParseMsgID(messageID)
	if p.ID == "" {
		return false, errBadID
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	edit := cli.BuildEdit(to, types.MessageID(p.ID), &waE2E.Message{Conversation: gproto.String(newText)})
	if _, err := cli.SendMessage(ctx, to, edit); err != nil {
		return false, fmt.Errorf("WhatsApp edit failed: %w", err)
	}
	return true, nil
}

// SendReaction implements proto.Reactor (send_reaction_sync). An empty emoji
// removes the reaction.
func (b *Backend) SendReaction(ctx context.Context, contactID, messageID, emoji, targetAuthor string) (bool, error) {
	cli, err := b.requireClient()
	if err != nil {
		return false, err
	}
	to, err := targetJID(contactID)
	if err != nil {
		return false, err
	}
	p := ParseMsgID(messageID)
	if p.ID == "" {
		return false, errBadID
	}
	fromMe := p.FromMe
	if !p.Serialized {
		if cached, ok := b.cachedEntry(contactID, messageID); ok {
			fromMe = cached.IsMine
		}
	}
	sender := types.EmptyJID
	if !fromMe {
		switch {
		case to.Server == types.GroupServer:
			part := firstNonEmpty(p.Participant, targetAuthor)
			pj, err := JIDFromWAHA(part)
			if err != nil {
				return false, fmt.Errorf("WhatsApp: group reaction needs the sender of the message")
			}
			sender = pj
		default:
			sender = to
		}
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if _, err := cli.SendMessage(ctx, to, cli.BuildReaction(to, sender, types.MessageID(p.ID), emoji)); err != nil {
		return false, fmt.Errorf("WhatsApp reaction failed: %w", err)
	}
	return true, nil
}

// markReadRemote sends the read receipts of the chat's unread incoming
// messages (rest.mark_read: POST /api/sendSeen). Best effort.
func (b *Backend) markReadRemote(ctx context.Context, contactID string) {
	cli := b.currentClient()
	if cli == nil || !b.connected.Load() {
		return
	}
	c := b.identify(contactID)
	chatID := contactID
	if c != nil {
		chatID = c.ID
	}
	to, err := JIDFromWAHA(chatID)
	if err != nil {
		return
	}
	type unread struct {
		id     types.MessageID
		sender types.JID
		ts     time.Time
	}
	b.ingestMu.Lock()
	keys := []string{contactID}
	if chatID != contactID {
		keys = append(keys, chatID)
	}
	var list []unread
	for _, k := range keys {
		for _, m := range b.cache[k] {
			if m.IsMine || m.Read || m.ID == "" {
				continue
			}
			p := ParseMsgID(m.ID)
			sender := to
			if p.Participant != "" {
				if pj, err := JIDFromWAHA(p.Participant); err == nil {
					sender = pj
				}
			}
			list = append(list, unread{types.MessageID(p.ID), sender, time.UnixMilli(m.Timestamp)})
		}
	}
	b.ingestMu.Unlock()
	if len(list) == 0 {
		return
	}
	if len(list) > 100 {
		list = list[len(list)-100:]
	}
	groups := map[types.JID][]types.MessageID{}
	var last time.Time
	for _, u := range list {
		groups[u.sender] = append(groups[u.sender], u.id)
		if u.ts.After(last) {
			last = u.ts
		}
	}
	for sender, ids := range groups {
		ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := cli.MarkRead(ctx2, ids, last, to, sender)
		cancel()
		if err != nil {
			b.log.Debug("WhatsApp mark-read failed", "chat", chatID, "err", err)
			return
		}
	}
	b.ingestMu.Lock()
	for _, k := range keys {
		for _, m := range b.cache[k] {
			if !m.IsMine {
				m.Read = true
			}
		}
	}
	b.ingestMu.Unlock()
}

// SetChatArchived implements proto.Archiver (set_chat_archived_sync): syncs the
// archive flag with the phone through an app-state patch.
func (b *Backend) SetChatArchived(ctx context.Context, contactID string, archived bool) (bool, error) {
	cli, err := b.requireClient()
	if err != nil {
		return false, nil
	}
	chatID := contactID
	if c := b.identify(contactID); c != nil {
		chatID = c.ID
	}
	to, err := targetJID(chatID)
	if err != nil {
		return false, err
	}
	var lastTS time.Time
	var lastKey *waCommon.MessageKey
	b.ingestMu.Lock()
	if msgs := b.cache[chatID]; len(msgs) > 0 {
		m := msgs[len(msgs)-1]
		lastTS = time.UnixMilli(m.Timestamp)
		if m.ID != "" {
			p := ParseMsgID(m.ID)
			lastKey = &waCommon.MessageKey{RemoteJID: gproto.String(to.String()), FromMe: gproto.Bool(m.IsMine), ID: gproto.String(p.ID)}
		}
	}
	b.ingestMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := cli.SendAppState(ctx, appstate.BuildArchive(to, archived, lastTS, lastKey)); err != nil {
		b.log.Debug("WhatsApp archive failed", "chat", chatID, "err", err)
		return false, nil
	}
	b.setChatArchivedLocal(chatID, archived)
	return true, nil
}

// CheckNumberExists implements core.NumberChecker (rest.check_number_exists).
func (b *Backend) CheckNumberExists(ctx context.Context, phone string) (bool, error) {
	cli, err := b.requireClient()
	if err != nil {
		return false, err
	}
	digits := jidDigits(phone)
	if digits == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := cli.IsOnWhatsApp(ctx, []string{"+" + digits})
	if err != nil {
		return false, err
	}
	return len(res) > 0 && res[0].IsIn, nil
}

// SendTyping implements proto.TypingSender: the outgoing typing indicator.
func (b *Backend) SendTyping(ctx context.Context, contactID string, started bool) error {
	cli, err := b.requireClient()
	if err != nil {
		return err
	}
	to, err := targetJID(contactID)
	if err != nil {
		return err
	}
	state := types.ChatPresencePaused
	if started {
		state = types.ChatPresenceComposing
	}
	return cli.SendChatPresence(ctx, to, state, types.ChatPresenceMediaText)
}

// ReconcileUnread implements core.UnreadReconciler (reconcile_unread): marks
// chats read locally when the phone says they are, from the two live sources
// queued in readOnPhone — a markChatAsRead app-state mutation (bounded by the
// phone's last-read timestamp, so newer unread traffic survives) and a read
// receipt for our messages from another device (the whole chat). Throttled to
// one pass per 20 s; never a polling loop. appStateSync calls the forced
// variant so a full-sync snapshot heals the badge deterministically.
func (b *Backend) ReconcileUnread(ctx context.Context) {
	b.reconcileUnread(ctx, false)
}

// reconcileUnread is ReconcileUnread with the throttle optional: force is
// used at the end of the app-state sync, where the markChatAsRead mutations
// have just landed and a contacts.list may already have spent the 20 s window
// (the badge would otherwise stay stale until the next refresh). Each queued
// chat is flushed with its cutoff through reconcileChatUnread.
func (b *Backend) reconcileUnread(ctx context.Context, force bool) {
	if !b.connected.Load() || b.st == nil {
		return
	}
	now := nowFunc()
	b.cmu.Lock()
	if !force && now.Sub(b.lastUnread) < 20*time.Second {
		b.cmu.Unlock()
		return
	}
	b.lastUnread = now
	type pendingRead struct {
		chat   string
		cutoff int64
	}
	var chats []pendingRead
	for c, cutoff := range b.readOnPhone {
		chats = append(chats, pendingRead{c, cutoff})
	}
	b.readOnPhone = map[string]int64{}
	b.cmu.Unlock()
	// The unread counts of the chats that still show a badge: the snapshots
	// (and the history asks below) are driven by what the store holds now.
	unread := b.unreadByChat(ctx)
	// Re-apply the last authoritative count of every chat a history sync told
	// us about. A chat whose read state never changes on the phone (no
	// markChatAsRead entry survives in the app state) has no other heal: the
	// count is what keeps the locally ingested history from inflating the
	// badge forever.
	b.applyUnreadSnapshots(ctx, unread)
	for _, p := range chats {
		// Timestamp-bounded: keep only the newest `keep` (0) incoming messages
		// unread, and never touch a message newer than the phone's boundary
		// (a zero cutoff means the whole chat was read).
		b.reconcileChatUnread(ctx, p.chat, 0, p.cutoff)
	}
	// Chats with local unread but no trusted count: ask the phone for their
	// history (best effort: the ON_DEMAND answer may carry the chat's count)
	// instead of keeping the badge on a guess.
	b.askUntrustedUnread(unread)
}
