package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}

// validatedReplyTo is _validated_reply_to_message_id (0 = no reply).
func validatedReplyTo(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, ErrInvalidReplyID
	}
	return n, nil
}

func replyHeader(id int) tg.InputReplyToClass {
	if id <= 0 {
		return nil
	}
	return &tg.InputReplyToMessage{ReplyToMsgID: id}
}

// sendTarget validates the ids and resolves the peer (the _send prologue).
func (b *Backend) sendTarget(contactID string, quote proto.Quote) (*tg.Client, tg.InputPeerClass, int, error) {
	api, err := b.getAPI()
	if err != nil {
		return nil, nil, 0, err
	}
	eid, err := parseContactID(contactID)
	if err != nil {
		return nil, nil, 0, err
	}
	reply, err := validatedReplyTo(quote.ReplyToMessageID)
	if err != nil {
		return nil, nil, 0, err
	}
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		return nil, nil, 0, err
	}
	return api, peer, reply, nil
}

// sentIDs extracts the new message ids of a send result, in the order of the
// random ids (UpdateMessageID), falling back to the new-message updates.
func sentIDs(u tg.UpdatesClass, randoms []int64) []int {
	byRandom := map[int64]int{}
	var fallback []int
	var visit func(tg.UpdateClass)
	visit = func(up tg.UpdateClass) {
		switch t := up.(type) {
		case *tg.UpdateMessageID:
			byRandom[t.RandomID] = t.ID
		case *tg.UpdateNewMessage:
			if m, ok := t.Message.(*tg.Message); ok {
				fallback = append(fallback, m.ID)
			}
		case *tg.UpdateNewChannelMessage:
			if m, ok := t.Message.(*tg.Message); ok {
				fallback = append(fallback, m.ID)
			}
		}
	}
	switch t := u.(type) {
	case *tg.UpdateShortSentMessage:
		return []int{t.ID}
	case *tg.UpdateShortMessage:
		return []int{t.ID}
	case *tg.Updates:
		for _, up := range t.Updates {
			visit(up)
		}
	case *tg.UpdatesCombined:
		for _, up := range t.Updates {
			visit(up)
		}
	}
	var out []int
	for _, r := range randoms {
		if id, ok := byRandom[r]; ok {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func idStrings(ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.Itoa(id)
	}
	return out
}

// SendMessage implements proto.Backend (send_message_sync, 30 s budget).
func (b *Backend) SendMessage(ctx context.Context, contactID, text string, quote proto.Quote) (string, error) {
	api, peer, reply, err := b.sendTarget(contactID, quote)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, ents := parseMarkdown(text)
	rid := randomID()
	req := &tg.MessagesSendMessageRequest{Peer: peer, Message: msg, RandomID: rid, ReplyTo: replyHeader(reply)}
	if len(ents) > 0 {
		req.Entities = ents
	}
	res, err := api.MessagesSendMessage(ctx, req)
	if err != nil {
		return "", err
	}
	ids := sentIDs(res, []int64{rid})
	if len(ids) == 0 {
		return "", fmt.Errorf("telegram: send returned no message id")
	}
	return strconv.Itoa(ids[0]), nil
}

// ---- attachments --------------------------------------------------------------------

func normMime(m string) string {
	return strings.TrimSpace(strings.SplitN(strings.ToLower(m), ";", 2)[0])
}

func isPhotoFile(name, mime string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

func attachmentKind(att proto.Attachment) string {
	if att.MediaKind != "" {
		return att.MediaKind
	}
	if k := mediaKindFromMime(normMime(att.MimeType)); k != "" {
		return k
	}
	return "document"
}

// uploadMedia uploads att and builds the InputMedia (Telethon send_file with
// force_document=forceDoc; voice notes only for audio/ogg).
func (b *Backend) uploadMedia(ctx context.Context, api *tg.Client, att proto.Attachment, forceDoc bool) (tg.InputMediaClass, error) {
	f, err := os.Open(att.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	name := att.Filename
	if name == "" {
		name = filepath.Base(att.Path)
	}
	file, err := uploader.NewUploader(api).Upload(ctx, uploader.NewUpload(name, f, st.Size()))
	if err != nil {
		return nil, err
	}
	mime := normMime(att.MimeType)
	kind := attachmentKind(att)
	if !forceDoc && kind != "document" && isPhotoFile(name, mime) && (kind == "image") {
		return &tg.InputMediaUploadedPhoto{File: file}, nil
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	attrs := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}}
	switch {
	case kind == "voice" && mime == "audio/ogg":
		attrs = append(attrs, &tg.DocumentAttributeAudio{Voice: true})
	case strings.HasPrefix(mime, "audio/"):
		attrs = append(attrs, &tg.DocumentAttributeAudio{})
	}
	return &tg.InputMediaUploadedDocument{File: file, MimeType: mime, Attributes: attrs, ForceFile: forceDoc}, nil
}

// SendAttachment implements proto.AttachmentSender (send_attachment_sync).
func (b *Backend) SendAttachment(ctx context.Context, contactID string, att proto.Attachment, quote proto.Quote) (string, error) {
	api, peer, reply, err := b.sendTarget(contactID, quote)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentSendTimeout)
	defer cancel()
	media, err := b.uploadMedia(ctx, api, att, attachmentKind(att) == "document")
	if err != nil {
		return "", err
	}
	rid := randomID()
	caption, ents := parseMarkdown(att.Caption)
	req := &tg.MessagesSendMediaRequest{Peer: peer, Media: media, Message: caption, RandomID: rid, ReplyTo: replyHeader(reply)}
	if len(ents) > 0 {
		req.Entities = ents
	}
	res, err := api.MessagesSendMedia(ctx, req)
	if err != nil {
		return "", err
	}
	ids := sentIDs(res, []int64{rid})
	if len(ids) == 0 {
		return "", fmt.Errorf("telegram: send returned no message id")
	}
	return strconv.Itoa(ids[0]), nil
}

// registeredMedia converts an uploaded media to the reusable Input form
// (messages.uploadMedia + get_input_media), as albums require.
func registeredMedia(m tg.MessageMediaClass) (tg.InputMediaClass, error) {
	switch t := m.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := t.Photo.(*tg.Photo); ok {
			return &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference}}, nil
		}
	case *tg.MessageMediaDocument:
		if d, ok := t.Document.(*tg.Document); ok {
			return &tg.InputMediaDocument{ID: &tg.InputDocument{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}}, nil
		}
	}
	return nil, fmt.Errorf("telegram: cannot reuse uploaded media %T", m)
}

// SendAttachments implements proto.BatchAttachmentSender: N files as one
// album (chunks of 10); the caption applies to the first media only. Any
// failure aborts the whole batch.
func (b *Backend) SendAttachments(ctx context.Context, contactID string, atts []proto.Attachment, batchID string, quote proto.Quote) ([]string, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	if len(atts) == 1 {
		id, err := b.SendAttachment(ctx, contactID, atts[0], quote)
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}
	api, peer, reply, err := b.sendTarget(contactID, quote)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentSendTimeout*time.Duration(len(atts)))
	defer cancel()
	var singles []tg.InputSingleMedia
	var randoms []int64
	for i, att := range atts {
		media, err := b.uploadMedia(ctx, api, att, false)
		if err != nil {
			return nil, err
		}
		up, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: peer, Media: media})
		if err != nil {
			return nil, err
		}
		in, err := registeredMedia(up)
		if err != nil {
			return nil, err
		}
		rid := randomID()
		s := tg.InputSingleMedia{Media: in, RandomID: rid}
		if i == 0 && atts[0].Caption != "" {
			caption, ents := parseMarkdown(atts[0].Caption)
			s.Message = caption
			if len(ents) > 0 {
				s.Entities = ents
			}
		}
		singles = append(singles, s)
		randoms = append(randoms, rid)
	}
	var ids []int
	for start := 0; start < len(singles); start += 10 {
		end := start + 10
		if end > len(singles) {
			end = len(singles)
		}
		req := &tg.MessagesSendMultiMediaRequest{Peer: peer, MultiMedia: singles[start:end]}
		if start == 0 {
			req.ReplyTo = replyHeader(reply)
		}
		res, err := api.MessagesSendMultiMedia(ctx, req)
		if err != nil {
			return nil, err
		}
		ids = append(ids, sentIDs(res, randoms[start:end])...)
	}
	return idStrings(ids), nil
}

// ---- sent mirror -------------------------------------------------------------------

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(src); err == nil {
		_ = os.Chtimes(dst, st.ModTime(), st.ModTime())
	}
	return nil
}

// EnqueueSentMessage implements proto.SentMirror (enqueue_sent_message): it
// publishes a successful send through the receive queue. An attachment is
// copied to "<chat>-<msg>-sent<ext>" in the media dir.
func (b *Backend) EnqueueSentMessage(ctx context.Context, m proto.SentMessage) error {
	isAtt := m.AttachmentPath != ""
	kind := ""
	if isAtt {
		kind = m.MediaKind
		if kind == "" {
			kind = mediaKindFromMime(m.MimeType)
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
		cid, err1 := strconv.ParseInt(m.ContactID, 10, 64)
		mid, err2 := strconv.ParseInt(m.MessageID, 10, 64)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("telegram: invalid ids for sent mirror")
		}
		persisted := filepath.Join(dir, fmt.Sprintf("%d-%d-sent%s", cid, mid, strings.ToLower(pySuffix(m.AttachmentPath))))
		if err := copyFile(m.AttachmentPath, persisted); err != nil {
			return err
		}
		attachmentID = persisted
	}
	var info any
	if isAtt {
		if msgType == "image" {
			info = strOrNil(firstNonEmpty(m.Text, m.Filename))
		} else {
			info = strOrNil(firstNonEmpty(m.Filename, m.Text))
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

// EnqueueSentNotification is Signal-only; Telegram mirrors via EnqueueSentMessage.
func (b *Backend) EnqueueSentNotification(ctx context.Context, contactID, messageID string, timestamp int64, batchID string) error {
	return proto.ErrUnsupported
}

// ---- edit / reactions / archive ---------------------------------------------------------

// EditMessage implements proto.Editor (edit_message_sync).
func (b *Backend) EditMessage(ctx context.Context, contactID, messageID, newText string) (bool, error) {
	api, err := b.getAPI()
	if err != nil {
		return false, err
	}
	eid, err1 := toInt(contactID)
	mid, err2 := toInt(messageID)
	if !err1 || !err2 {
		return false, ErrInvalidMessage
	}
	if mid <= 0 {
		return false, fmt.Errorf("Invalid Telegram message id")
	}
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text, ents := parseMarkdown(newText)
	req := &tg.MessagesEditMessageRequest{Peer: peer, ID: int(mid), Message: text}
	req.SetMessage(text)
	if len(ents) > 0 {
		req.SetEntities(ents)
	}
	if _, err := api.MessagesEditMessage(ctx, req); err != nil {
		return false, err
	}
	return true, nil
}

// SendReaction implements proto.Reactor (send_reaction_sync): false on any failure.
func (b *Backend) SendReaction(ctx context.Context, contactID, messageID, emoji, targetAuthor string) (bool, error) {
	api, err := b.getAPI()
	if err != nil {
		return false, nil
	}
	eid, ok1 := toInt(contactID)
	mid, ok2 := toInt(messageID)
	if !ok1 || !ok2 || mid <= 0 {
		return false, nil
	}
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		b.log.Debug("Telegram reaction send failed", "err", err)
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req := &tg.MessagesSendReactionRequest{Peer: peer, MsgID: int(mid)}
	req.SetReaction([]tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}})
	if _, err := api.MessagesSendReaction(ctx, req); err != nil {
		b.log.Debug("Telegram reaction send failed", "err", err)
		return false, nil
	}
	return true, nil
}

// AvailableReactions implements proto.ReactionLister (10 minute cache; nil on error).
func (b *Backend) AvailableReactions(ctx context.Context) ([]string, error) {
	b.reactMu.Lock()
	defer b.reactMu.Unlock()
	now := b.clock()
	if b.reactions != nil && now.Sub(b.reactionsTS) < availableReactionsTTL {
		return append([]string(nil), b.reactions...), nil
	}
	api, err := b.getAPI()
	if err != nil {
		return []string{}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := api.MessagesGetAvailableReactions(cctx, 0)
	if err != nil {
		b.log.Debug("Telegram available reactions fetch failed", "err", err)
		return []string{}, nil
	}
	emojis := []string{}
	if r, ok := res.(*tg.MessagesAvailableReactions); ok {
		for _, a := range r.Reactions {
			if a.Reaction != "" {
				emojis = append(emojis, a.Reaction)
			}
		}
	}
	b.reactions, b.reactionsTS = append([]string(nil), emojis...), now
	return emojis, nil
}

// SetChatArchived implements proto.Archiver (folders.editPeerFolders; not in
// the Python backend).
func (b *Backend) SetChatArchived(ctx context.Context, contactID string, archived bool) (bool, error) {
	api, err := b.getAPI()
	if err != nil {
		return false, err
	}
	eid, err := parseContactID(contactID)
	if err != nil {
		return false, err
	}
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		return false, err
	}
	folder := 0
	if archived {
		folder = 1
	}
	if _, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: peer, FolderID: folder}}); err != nil {
		return false, err
	}
	return true, nil
}
