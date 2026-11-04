package messages

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Port of server/messages.py: the presentation row builder behind
// messages.page.

const (
	maxMessagesLimit = 2000 // _MAX_MESSAGES_LIMIT
	// maxRefreshLimit is MessagesWindowRequest.refresh_limit's upper bound.
	maxRefreshLimit = 200
)

// pendingMediaLabels is _PENDING_MEDIA_LABELS.
var pendingMediaLabels = map[string]string{
	"image": "Immagine", "gif": "GIF", "video": "Video", "audio": "Audio",
	"voice": "Messaggio vocale", "sticker": "Sticker", "document": "Documento",
}

// pendingMediaTypes is _PENDING_MEDIA_TYPES.
var pendingMediaTypes = map[string]string{
	"image": "image/*", "video": "video/*", "audio": "audio/*", "voice": "audio/*",
}

func pendingLabel(kind string) string {
	if l, ok := pendingMediaLabels[kind]; ok {
		return l
	}
	return "Allegato"
}

// imageMimeBySuffix is mimetypes.guess_type for the _IMAGE_EXTENSIONS.
var imageMimeBySuffix = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".heic": "image/heic", ".heif": "image/heif",
	".tif": "image/tiff", ".tiff": "image/tiff",
}

// inferAttachmentType is _infer_attachment_type(attachment_id, None).
func inferAttachmentType(attachmentID string) *string {
	p, _, _ := strings.Cut(attachmentID, "?")
	suffix := strings.ToLower(pySuffix(p))
	mime, ok := imageMimeBySuffix[suffix]
	if !ok {
		return nil
	}
	return &mime
}

// rowMediaKind is _row_media_kind.
func rowMediaKind(r *msg) string {
	if kind := strings.ToLower(pyStrip(strOf(r.MediaKind))); kind != "" {
		return kind
	}
	switch mt := strings.ToLower(pyStrip(strOf(r.MsgType))); mt {
	case "image", "video", "audio", "voice", "document", "sticker", "gif":
		return mt
	}
	return ""
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// pendingAttachment is _pending_attachment (WhatsApp media without a file).
func pendingAttachment(r *msg) *api.MessageAttachment {
	token, ok := lazyMediaToken(r.ContactNumber, strOf(r.MsgID))
	if !ok {
		return nil
	}
	kind := rowMediaKind(r)
	info := pyStrip(strOf(r.AttachmentInfo))
	name, ok := attachmentDisplayName(&info, nil)
	if ok && name == info && isCaptionLike(info) {
		ok = false
	}
	if !ok || name == "" {
		name = pendingLabel(kind)
	}
	var typ *string
	if ct := strOf(r.ContentType); ct != "" {
		typ = &ct
	} else if t, found := pendingMediaTypes[kind]; found {
		typ = &t
	}
	return &api.MessageAttachment{
		AttachmentID: token, Name: name, Type: typ, MediaKind: strPtrOrNil(kind), Pending: true,
	}
}

// messageEditID is _message_edit_id.
func messageEditID(r *msg) *string {
	if !r.IsMine || strOf(r.MsgType) != "text" {
		return nil
	}
	if st := strOf(r.Status); st == "pending" || st == "failed" {
		return nil
	}
	if r.Protocol == api.ProtocolSignal {
		var s string
		if !missingStr(r.MsgID) {
			s = *r.MsgID
		} else {
			s = strconv.FormatInt(r.Timestamp, 10)
		}
		return &s
	}
	if !missingStr(r.MsgID) {
		return r.MsgID
	}
	return nil
}

// reactionAggregate accumulates one emoji of one message.
type reactionAggregate struct {
	emoji          string
	count          int64
	isMine         bool
	authors        map[string]bool
	firstTimestamp int64
}

// aggregateReactions is _aggregate_reactions: persisted reactions per message
// row id.
func aggregateReactions(rows []msg, reactions []store.Reaction) map[int64][]api.Reaction {
	result := map[int64][]api.Reaction{}
	for i := range rows {
		row := &rows[i]
		ids := map[string]bool{strconv.FormatInt(row.Timestamp, 10): true}
		if row.MsgID != nil {
			ids[*row.MsgID] = true
		}
		var order []*reactionAggregate
		byEmoji := map[string]*reactionAggregate{}
		for j := range reactions {
			r := &reactions[j]
			matchesID := r.TargetMsgID != nil && ids[*r.TargetMsgID]
			matchesTS := r.TargetTimestamp != nil && *r.TargetTimestamp == row.Timestamp
			if !matchesID && !matchesTS {
				continue
			}
			agg := byEmoji[r.Emoji]
			if agg == nil {
				agg = &reactionAggregate{emoji: r.Emoji, authors: map[string]bool{}, firstTimestamp: r.Timestamp}
				byEmoji[r.Emoji] = agg
				order = append(order, agg)
			}
			agg.count += r.Count
			agg.isMine = agg.isMine || r.IsMine
			if a := pyStrip(r.Author); a != "" {
				agg.authors[a] = true
			}
			if r.Timestamp < agg.firstTimestamp {
				agg.firstTimestamp = r.Timestamp
			}
		}
		if len(order) == 0 {
			continue
		}
		sort.SliceStable(order, func(a, b int) bool {
			if order[a].count != order[b].count {
				return order[a].count > order[b].count
			}
			return order[a].firstTimestamp < order[b].firstTimestamp
		})
		out := make([]api.Reaction, len(order))
		for k, agg := range order {
			authors := make([]string, 0, len(agg.authors))
			for a := range agg.authors {
				authors = append(authors, a)
			}
			sort.Strings(authors)
			out[k] = api.Reaction{Emoji: agg.emoji, Count: int(agg.count), IsMine: agg.isMine, Authors: authors}
		}
		result[row.RowID] = out
	}
	return result
}

// rowBuilder turns stored rows into presentation rows.
type rowBuilder struct {
	st        quoteStore
	cacheDir  string
	lazyMedia bool // protocol is WhatsApp and wa_lazy_media is on
	protocol  string
}

// rowText applies the caption normalisation of one row and returns the text,
// whether the attachment-info caption is suppressed, and a possible media
// placeholder; attachment is the (possibly pending) attachment.
func (b *rowBuilder) build(ctx context.Context, rows []msg) []api.MessageRow {
	out := make([]api.MessageRow, 0, len(rows))
	for i := range rows {
		out = append(out, b.buildOne(ctx, &rows[i]))
	}
	return out
}

func (b *rowBuilder) buildOne(ctx context.Context, row *msg) api.MessageRow {
	var attachment *api.MessageAttachment
	mediaPlaceholder := ""
	text := strOf(row.Text)
	suppressInfoCaption := false
	protocol := b.protocol

	if b.lazyMedia && missingStr(row.AttachmentID) && strOf(row.MsgType) != api.MsgTypeSystem &&
		isMediaRow(row.MsgType, row.MediaKind) {
		attachment = pendingAttachment(row)
		if attachment == nil {
			mediaPlaceholder = pendingLabel(rowMediaKind(row))
		}
	}
	if !missingStr(row.AttachmentID) || attachment != nil {
		if attachment == nil {
			aid := *row.AttachmentID
			name, ok := attachmentDisplayName(row.AttachmentInfo, row.AttachmentID)
			if !ok {
				name = ""
			}
			var typ *string
			switch {
			case !missingStr(row.ContentType):
				typ = row.ContentType
			case strOf(row.MsgType) == "image":
				typ = api.Ptr("image/*")
			default:
				typ = inferAttachmentType(aid)
			}
			attachment = &api.MessageAttachment{
				AttachmentID: aid, Name: name, Type: typ, MediaKind: row.MediaKind,
			}
		}
		info := pyStrip(strOf(row.AttachmentInfo))
		attType := strings.ToLower(strOf(attachment.Type))
		mediaKind := strings.ToLower(pyStrip(strOf(row.MediaKind)))
		msgType := strings.ToLower(pyStrip(strOf(row.MsgType)))
		switch {
		case text != "" && (strOf(row.MsgType) == "image" || strings.HasPrefix(attType, "image/")):
			stripped := pyStrip(text)
			prefixed := false
			if info != "" {
				for _, label := range []string{"immagine", "image", "media", "video", "audio", "file"} {
					if strings.EqualFold(stripped, label+": "+info) {
						prefixed = true
						break
					}
				}
			}
			if stripped == info || isMediaQuotePlaceholder(text) || prefixed ||
				(protocol == api.ProtocolWhatsApp && isWhatsAppSyntheticMediaText(text, row.AttachmentID)) {
				text = ""
			}
		case text != "" && (isOneOf(mediaKind, "video", "audio", "document", "sticker", "voice") ||
			isOneOf(msgType, "attachment", "video", "audio", "document", "sticker", "voice")):
			stripped := pyStrip(text)
			prefixed := info != "" && strings.HasPrefix(stripped, info+":")
			if isMediaQuotePlaceholder(text) || prefixed ||
				(protocol == api.ProtocolWhatsApp && isWhatsAppSyntheticMediaText(text, row.AttachmentID)) {
				text = ""
				suppressInfoCaption = prefixed
			}
		case protocol == api.ProtocolWhatsApp && text != "":
			if isMediaQuotePlaceholder(text) || isWhatsAppSyntheticMediaText(text, row.AttachmentID) {
				text = ""
			}
		}
		if text == "" && !missingStr(row.AttachmentInfo) && !suppressInfoCaption {
			fileName := pyName(strOf(row.AttachmentID))
			if isCaptionLike(info) && info != fileName {
				text = info
			}
		}
	}

	direction := "in"
	if row.IsMine {
		direction = "out"
	}
	id := strOf(row.MsgID)
	if id == "" {
		id = strconv.FormatInt(row.RowID, 10)
	}
	var status *string
	if direction == "out" {
		s := strOf(row.Status)
		if s == "" {
			s = api.StatusSent
		}
		status = &s
	}
	var batchIndex *int
	if row.BatchIndex != nil {
		v := int(*row.BatchIndex)
		batchIndex = &v
	}
	m := api.MessageRow{
		ID:                    id,
		Text:                  text,
		Direction:             direction,
		Sender:                strOf(row.Sender),
		Timestamp:             row.Timestamp,
		Attachment:            attachment,
		QuoteText:             row.QuoteText,
		QuoteTimestamp:        row.QuoteTimestamp,
		QuoteAuthor:           strOf(row.QuoteAuthor),
		QuoteAttachmentID:     row.QuoteAttachmentID,
		QuoteContentType:      row.QuoteContentType,
		QuoteThumbURL:         quoteThumbURL(ctx, b.st, b.cacheDir, row),
		QuoteMediaPlaceholder: isMediaQuotePlaceholderComposite(strOf(row.QuoteText)),
		Status:                status,
		Read:                  row.Read,
		Edited:                row.Edited,
		EditID:                messageEditID(row),
		BatchID:               row.BatchID,
		BatchIndex:            batchIndex,
		MediaPlaceholder:      mediaPlaceholder,
		System:                strOf(row.MsgType) == api.MsgTypeSystem,
	}
	return m
}

func isOneOf(s string, set ...string) bool { return containsStr(set, s) }
