package whatsapp

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	gproto "google.golang.org/protobuf/proto"
)

// quoteThumbSide is the longest side of the JPEG thumbnail sent with a quoted
// image (what WhatsApp clients attach to the quote preview).
const quoteThumbSide = 72

// WAIDFinder is an OPTIONAL store extension: the rows of a chat whose msg_id
// is a WhatsApp id (or a WAHA-serialized id containing it). *store.Store
// implements it. Without it a quote missing from the cache has no row.
type WAIDFinder interface {
	MessagesByWAID(ctx context.Context, protocol string, keys []string, waID string) ([]store.Message, error)
}

// wahaIDLikeRe matches a serialized WAHA message id with a file extension
// ("true_<chat>_<HEX>_<part>@lid.jpeg"): the attachment name the web client
// sends as quote text for a media reply, never something to show.
var wahaIDLikeRe = regexp.MustCompile(`(?i)^(true|false)_\S+\.[a-z0-9]{1,5}$`)

// isAttachmentRef reports whether text is a reference to the attachment (its
// id, file name or a serialized WAHA id) rather than something a person wrote.
func isAttachmentRef(text string, row *entry) bool {
	if isIDRef(text, row) {
		return true
	}
	t := strings.TrimSpace(text)
	if row != nil {
		if row.isMediaRow() && row.AttachmentInfo != "" && t == row.AttachmentInfo && !isCaptionLike(row.AttachmentInfo) {
			return true
		}
	}
	return false
}

// isIDRef is true for blank text, a serialized WAHA id or the row's attachment
// id: never a legitimate quote text nor a file name.
func isIDRef(text string, row *entry) bool {
	t := strings.TrimSpace(text)
	if t == "" || wahaIDLikeRe.MatchString(t) || ParseMsgID(t).Serialized {
		return true
	}
	if row != nil {
		for _, ref := range []string{row.AttachmentID, filepath.Base(row.AttachmentID)} {
			if ref != "" && ref != "." && t == ref {
				return true
			}
		}
	}
	return false
}

// isMediaRow is true for a row that carries (or carried) an attachment.
func (e *entry) isMediaRow() bool {
	if e.MediaKind != "" || e.AttachmentID != "" {
		return true
	}
	switch e.MsgType {
	case "image", "sticker", "attachment", "video", "audio", "voice", "document", "gif":
		return true
	}
	return false
}

// quotedKind is the media kind of a media row.
func (e *entry) quotedKind() string {
	if e.MediaKind != "" {
		return e.MediaKind
	}
	switch e.MsgType {
	case "image", "sticker", "video", "audio", "voice", "document", "gif":
		return e.MsgType
	}
	if k := mediaKindFromMime(e.ContentType, false, false); k != "" {
		return k
	}
	return "document"
}

// quotedRow resolves the quoted message: the cache first, then the store.
func (b *Backend) quotedRow(contactID, replyTo string) *entry {
	if e, ok := b.cachedEntry(contactID, replyTo); ok {
		return &e
	}
	f, ok := b.st.(WAIDFinder)
	if !ok || b.st == nil {
		return nil
	}
	waID := ParseMsgID(replyTo).ID
	if waID == "" {
		return nil
	}
	keys := []string{contactID}
	if c, ok := b.FindContact(contactID); ok {
		for _, k := range contactStorageKeys(c) {
			if !containsStr(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	rows, err := f.MessagesByWAID(context.Background(), proto.ProtocolWhatsApp, keys, waID)
	if err != nil {
		b.log.Debug("WhatsApp quote lookup failed", "err", err)
		return nil
	}
	for _, m := range rows {
		if id := store.Deref(m.MsgID); id != "" && (id == replyTo || sameMsgID(id, replyTo)) {
			return entryFromStore(m)
		}
	}
	return nil
}

// quotedMessage builds the QuotedMessage of a reply, or nil when nothing
// trustworthy can be shown (the quote then carries only StanzaID and
// Participant and WhatsApp resolves the preview itself).
//
// The row (cache, then store) is authoritative. Client-supplied quote text is
// used only when the row is unknown or a text row without text, and never when
// it is an attachment reference.
func (b *Backend) quotedMessage(row *entry, clientText string) *waE2E.Message {
	if row != nil && row.isMediaRow() {
		return b.quotedMediaMessage(row)
	}
	text := ""
	if row != nil {
		text = row.Text
	}
	if text == "" && !isAttachmentRef(clientText, row) {
		if row != nil || isCaptionLike(clientText) {
			text = clientText
		}
	}
	if text == "" {
		return nil
	}
	return &waE2E.Message{Conversation: gproto.String(text)}
}

func normalizedMime(ct, fallback string) string {
	ct = strings.TrimSpace(ct)
	if ct == "" || strings.HasSuffix(ct, "/*") || mimeOnlyRE.FindString(strings.ToLower(ct)) == "" {
		return fallback
	}
	return ct
}

func (b *Backend) quotedMediaMessage(row *entry) *waE2E.Message {
	kind := row.quotedKind()
	caption := ""
	switch kind {
	case "image", "gif":
		// The caption of an image lives in attachment_info (a file name
		// otherwise) and text stays empty.
		for _, c := range []string{row.AttachmentInfo, row.Text} {
			if isCaptionLike(c) && !isAttachmentRef(c, row) {
				caption = c
				break
			}
		}
	default:
		if isCaptionLike(row.Text) && !isAttachmentRef(row.Text, row) {
			caption = row.Text
		}
	}
	var capPtr *string
	if caption != "" {
		capPtr = gproto.String(caption)
	}
	ct := strings.ToLower(row.ContentType)
	switch kind {
	case "gif":
		if strings.HasPrefix(ct, "video/") {
			return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				Mimetype: gproto.String(normalizedMime(row.ContentType, "video/mp4")), Caption: capPtr, GifPlayback: gproto.Bool(true)}}
		}
		fallthrough
	case "image":
		def := "image/jpeg"
		if kind == "gif" {
			def = "image/gif"
		}
		im := &waE2E.ImageMessage{Mimetype: gproto.String(normalizedMime(row.ContentType, def)), Caption: capPtr}
		im.JPEGThumbnail = b.quoteThumbnail(row)
		return &waE2E.Message{ImageMessage: im}
	case "video":
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Mimetype: gproto.String(normalizedMime(row.ContentType, "video/mp4")), Caption: capPtr}}
	case "voice", "audio":
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: gproto.String(normalizedMime(row.ContentType, "audio/ogg; codecs=opus")), PTT: gproto.Bool(kind == "voice")}}
	case "sticker":
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			Mimetype: gproto.String(normalizedMime(row.ContentType, "image/webp"))}}
	}
	doc := &waE2E.DocumentMessage{
		Mimetype: gproto.String(normalizedMime(row.ContentType, "application/octet-stream")), Caption: capPtr}
	if name := row.AttachmentInfo; name != "" && !isIDRef(name, row) && mimeOnlyRE.FindString(strings.ToLower(name)) == "" {
		doc.FileName, doc.Title = gproto.String(name), gproto.String(name)
	}
	return &waE2E.Message{DocumentMessage: doc}
}

// quoteThumbnail is the ~72 px JPEG of the quoted image from the local media
// file; nil when the file is not there or not decodable.
func (b *Backend) quoteThumbnail(row *entry) []byte {
	if row.AttachmentID == "" || b.mediaDir == "" {
		return nil
	}
	name := filepath.Base(row.AttachmentID)
	if name == "." || name == string(filepath.Separator) || strings.Contains(row.AttachmentID, "://") {
		return nil
	}
	data, ok := media.JPEGThumbnail(filepath.Join(b.mediaDir, name), quoteThumbSide)
	if !ok {
		return nil
	}
	return data
}
