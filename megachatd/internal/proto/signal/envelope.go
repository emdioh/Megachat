package signal

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// ---- quote helpers (signal.py _signal_quote_*) -----------------------------------

func quoteFirstAttachment(quote map[string]any) map[string]any {
	if quote == nil {
		return nil
	}
	atts := getList(quote, "attachments")
	if len(atts) == 0 {
		return nil
	}
	first, _ := atts[0].(map[string]any)
	if first == nil {
		first = map[string]any{}
	}
	return first
}

// quoteText resolves the quote_text: a real caption wins, otherwise a typed
// media placeholder. "" = None.
func quoteText(quote map[string]any) string {
	if quote == nil {
		return ""
	}
	if t := strings.TrimSpace(getStr(quote, "text")); t != "" {
		return t
	}
	first := quoteFirstAttachment(quote)
	if first == nil {
		return ""
	}
	ct := strings.TrimSpace(getStr(first, "contentType"))
	fn := strings.TrimSpace(getStr(first, "filename"))
	return mediaQuoteDisplay(ct, fn)
}

func quoteContentType(quote map[string]any) string {
	first := quoteFirstAttachment(quote)
	if first == nil {
		return ""
	}
	return strings.TrimSpace(getStr(first, "contentType"))
}

// quoteTimestamp is the timestamp of the quoted message (targetSentTimestamp
// or id); nil when absent or not numeric.
func quoteTimestamp(quote map[string]any) *int64 {
	if quote == nil {
		return nil
	}
	raw := firstTruthy(quote["targetSentTimestamp"], quote["id"])
	if raw == nil {
		return nil
	}
	n, ok := toInt64(raw)
	if !ok {
		return nil
	}
	return &n
}

func quoteAttachmentID(quote map[string]any) string {
	first := quoteFirstAttachment(quote)
	if first == nil {
		return ""
	}
	v := firstTruthy(first["id"], first["attachmentId"])
	return strings.TrimSpace(anyString(v))
}

func coerceThumbnailBytes(v any) []byte {
	if m, ok := v.(map[string]any); ok {
		v = firstTruthy(m["thumbnail"], m["thumbnailData"], m["data"])
	}
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		b, err := base64.StdEncoding.DecodeString(t)
		if err != nil {
			return nil
		}
		return b
	case []byte:
		if len(t) == 0 {
			return nil
		}
		return t
	}
	return nil
}

// validImage decodes raw like Pillow's open()+load(): a real decode (x/image
// handles WebP, BMP and TIFF too), so truncated or corrupt data is rejected.
// Animated WebP, which x/image cannot decode but Pillow can, is accepted when
// its container is intact.
func validImage(raw []byte) (format string, ok bool) {
	_, f, err := image.Decode(bytes.NewReader(raw))
	if err == nil {
		return f, true
	}
	if animatedWebP(raw) {
		return "webp", true
	}
	return "", false
}

// animatedWebP reports a RIFF/WEBP container whose VP8X header has the
// animation flag set and whose declared RIFF size matches the data.
func animatedWebP(raw []byte) bool {
	if len(raw) < 30 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" || string(raw[12:16]) != "VP8X" {
		return false
	}
	if int(binary.LittleEndian.Uint32(raw[4:8]))+8 > len(raw) {
		return false
	}
	return raw[20]&0x02 != 0
}

// extractQuoteThumbnail persists the quoted attachment thumbnail under
// <quoteThumbsDir>/<sha1[:16]>.<ext> and returns its path, "" on any failure.
func extractQuoteThumbnail(quote map[string]any, dir string) string {
	first := quoteFirstAttachment(quote)
	if first == nil {
		return ""
	}
	raw := coerceThumbnailBytes(first["thumbnail"])
	if raw == nil {
		raw = coerceThumbnailBytes(first["thumbnailData"])
	}
	if raw == nil {
		return ""
	}
	format, ok := validImage(raw)
	if !ok {
		return ""
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "webp": ".webp", "gif": ".gif"}[format]
	if ext == "" {
		ext = ".png"
	}
	sum := sha1.Sum(raw)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, hex.EncodeToString(sum[:])[:16]+ext)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return ""
	}
	return path
}

// ---- typing / receipt --------------------------------------------------------------

func envelopeSource(env map[string]any) string {
	return anyString(firstTruthy(env["sourceNumber"], env["source"]))
}

// processTyping is rpc._process_typing.
func processTyping(env map[string]any) (source, action string, ok bool) {
	typing := getMap(env, "typingMessage")
	if len(typing) == 0 {
		return "", "", false
	}
	action = getStr(typing, "action")
	if action != "STARTED" && action != "STOPPED" {
		return "", "", false
	}
	source = envelopeSource(env)
	if source == "" {
		return "", "", false
	}
	return source, action, true
}

// ---- contact identification ----------------------------------------------------------

func (b *Backend) identifyContactForEnvelope(env map[string]any) (proto.Contact, bool) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	sent := getMap(getMap(env, "syncMessage"), "sentMessage")
	if len(sent) > 0 {
		dest := getStr(sent, "destination")
		destNumber := getStr(sent, "destinationNumber")
		destUUID := getStr(sent, "destinationUuid")
		for _, c := range b.contacts {
			if dest == c.ID || destNumber == c.ID {
				return cloneContact(*c), true
			}
			if aci, _ := c.Extras["aci"].(string); destUUID != "" && aci != "" && destUUID == aci {
				return cloneContact(*c), true
			}
		}
		return proto.Contact{}, false
	}
	source, sourceNumber, sourceUUID := getStr(env, "source"), getStr(env, "sourceNumber"), getStr(env, "sourceUuid")
	for _, c := range b.contacts {
		if source == c.ID || sourceNumber == c.ID {
			return cloneContact(*c), true
		}
		if aci, _ := c.Extras["aci"].(string); sourceUUID != "" && aci != "" && sourceUUID == aci {
			return cloneContact(*c), true
		}
	}
	return proto.Contact{}, false
}

// ---- message extraction -----------------------------------------------------------------

type classified struct {
	msgType, info, attID, contentType, mediaKind string
}

func classifyAttachments(atts []any) []classified {
	var out []classified
	for _, a := range atts {
		att, _ := a.(map[string]any)
		if att == nil {
			att = map[string]any{}
		}
		contentType := getStr(att, "contentType")
		kind := mediaKindFromMime(contentType, truthy(att["voiceNote"]))
		if kind == "" {
			kind = "document"
		}
		fname := getStr(att, "filename")
		caption := getStr(att, "caption")
		attID := anyString(firstTruthy(att["id"], att["attachmentId"]))
		var info string
		switch kind {
		case "image", "gif":
			info = caption
			if info == "" {
				if fname != "" {
					info = "Image: " + fname
				} else {
					info = "🖼️ Image"
				}
			}
		case "video":
			info = caption
			if info == "" {
				if fname != "" {
					info = "Video: " + fname
				} else {
					info = mediaQuotePlaceholders["video"]
				}
			}
		case "voice", "audio":
			info = caption
			if info == "" {
				if fname != "" {
					info = "Audio: " + fname
				} else {
					info = mediaQuotePlaceholders["audio"]
				}
			}
		default:
			for _, c := range []string{caption, fname, contentType, mediaQuotePlaceholders["attachment"]} {
				if c != "" {
					info = c
					break
				}
			}
		}
		out = append(out, classified{msgType: msgTypeForMediaKind(kind), info: info, attID: attID, contentType: contentType, mediaKind: kind})
	}
	return out
}

// quoteCtx groups the fields derived from a quote.
type quoteCtx struct {
	text      string
	timestamp *int64
	attID     string
	path      string
	ctype     string
}

func (b *Backend) newQuoteCtx(quote map[string]any) quoteCtx {
	return quoteCtx{
		text:      quoteText(quote),
		timestamp: quoteTimestamp(quote),
		attID:     quoteAttachmentID(quote),
		path:      extractQuoteThumbnail(quote, b.cfg.QuoteThumbsDir()),
		ctype:     quoteContentType(quote),
	}
}

func (q quoteCtx) apply(m map[string]any) {
	m["quote_text"] = strOrNil(q.text)
	m["quote_timestamp"] = int64OrNil(q.timestamp)
	m["quote_attachment_id"] = strOrNil(q.attID)
	m["quote_attachment_path"] = strOrNil(q.path)
	m["quote_content_type"] = strOrNil(q.ctype)
}

func buildMsgDicts(sender, text string, isMine bool, q quoteCtx, atts []any) []map[string]any {
	cl := classifyAttachments(atts)
	if len(cl) == 0 {
		m := map[string]any{
			"sender": sender, "text": text, "is_mine": isMine, "msg_type": "text",
			"attachment_info": nil, "attachment_id": nil, "content_type": nil, "media_kind": nil,
		}
		q.apply(m)
		return []map[string]any{m}
	}
	msgs := make([]map[string]any, 0, len(cl))
	for i, c := range cl {
		info := c.info
		if i == 0 && text != "" && c.msgType == "image" {
			info = text
		}
		var msgText string
		if i == 0 && text != "" {
			msgText = text
		} else {
			label := info
			if label == "" {
				label = "Media"
			}
			if c.attID != "" {
				msgText = label + ": " + c.attID
			} else {
				msgText = label
			}
		}
		m := map[string]any{
			"sender": sender, "text": msgText, "is_mine": isMine, "msg_type": c.msgType,
			"attachment_info": strOrNil(info), "attachment_id": strOrNil(c.attID),
			"content_type": strOrNil(c.contentType), "media_kind": c.mediaKind,
		}
		q.apply(m)
		msgs = append(msgs, m)
	}
	return msgs
}

func extractSticker(st map[string]any) (info string, ok bool) {
	if len(st) == 0 {
		return "", false
	}
	pack := getStr(st, "packId")
	id := getStr(st, "stickerId")
	if pack != "" {
		return "Sticker #" + id + " (pack:" + truncRunes(pack, 8) + "…)", true
	}
	return "Sticker #" + id, true
}

func (b *Backend) buildFromData(sender string, isMine bool, data map[string]any) []map[string]any {
	text := getStr(data, "message")
	q := b.newQuoteCtx(getMap(data, "quote"))
	if info, ok := extractSticker(getMap(data, "sticker")); ok {
		if text == "" {
			text = info
			if text == "" {
				text = "🎨 Sticker"
			}
		}
		m := map[string]any{
			"sender": sender, "text": text, "is_mine": isMine, "msg_type": "sticker",
			"attachment_info": info, "content_type": nil, "media_kind": "sticker",
		}
		q.apply(m)
		return []map[string]any{m}
	}
	return buildMsgDicts(sender, text, isMine, q, getList(data, "attachments"))
}

// extractMessageData normalises a dataMessage / sync sentMessage into one dict
// per attachment (the body goes on the first one only).
func (b *Backend) extractMessageData(env map[string]any) []map[string]any {
	sourceName := getStr(env, "sourceName")
	sourceNumber := envelopeSource(env)
	if dm := getMap(env, "dataMessage"); len(dm) > 0 {
		sender := sourceName
		if sender == "" {
			sender = sourceNumber
		}
		return b.buildFromData(sender, false, dm)
	}
	if sent := getMap(getMap(env, "syncMessage"), "sentMessage"); len(sent) > 0 {
		return b.buildFromData("You", true, sent)
	}
	return nil
}

func messageTimestamp(env map[string]any) int64 {
	ts := env["timestamp"]
	if !truthy(ts) {
		ts = getMap(env, "dataMessage")["timestamp"]
	}
	if !truthy(ts) {
		ts = getMap(getMap(env, "syncMessage"), "sentMessage")["timestamp"]
	}
	n, _ := toInt64(ts)
	return n
}

// ---- edits / reactions ----------------------------------------------------------------------

func (b *Backend) editEnvelopeToEvent(env map[string]any) (proto.Event, bool) {
	isMine := false
	edit := getMap(env, "editMessage")
	if len(edit) == 0 {
		sent := getMap(getMap(env, "syncMessage"), "sentMessage")
		edit = getMap(sent, "editMessage")
		isMine = len(edit) > 0
	}
	if len(edit) == 0 {
		return proto.Event{}, false
	}
	target := edit["targetSentTimestamp"]
	data := getMap(edit, "dataMessage")
	newText := getStr(data, "message")
	if !truthy(target) || newText == "" {
		return proto.Event{}, false
	}
	if truthy(data["attachments"]) {
		return proto.Event{}, false
	}
	targetN, ok := toInt64(target)
	if !ok {
		return proto.Event{}, false
	}
	contact, ok := b.identifyContactForEnvelope(env)
	if !ok {
		return proto.Event{}, false
	}
	sender := "You"
	if !isMine {
		sender = anyString(firstTruthy(env["sourceName"], env["sourceNumber"], env["source"]))
	}
	editTS, _ := toInt64(firstTruthy(data["timestamp"], env["timestamp"], 0))
	var editTSv any
	if editTS != 0 {
		editTSv = editTS
	}
	return proto.Event{
		Type: proto.EventMessageEdit, Protocol: proto.ProtocolSignal, ContactID: contact.ID,
		Payload: map[string]any{
			"edit_message_id": anyString(target),
			"text":            newText,
			"timestamp":       targetN,
			"edit_timestamp":  editTSv,
			"is_mine":         isMine,
			"sender":          sender,
			"contact":         contact,
			"msg_type":        "text",
		},
	}, true
}

func hasEditContent(env map[string]any) bool {
	if _, ok := env["editMessage"]; ok {
		return true
	}
	sent := getMap(getMap(env, "syncMessage"), "sentMessage")
	_, ok := sent["editMessage"]
	return ok
}

func hasReactionContent(env map[string]any) bool {
	if _, ok := getMap(env, "dataMessage")["reaction"]; ok {
		return true
	}
	_, ok := getMap(getMap(env, "syncMessage"), "sentMessage")["reaction"]
	return ok
}

func (b *Backend) reactionEnvelopeToEvent(env map[string]any) (proto.Event, bool) {
	isMine := false
	data := getMap(env, "dataMessage")
	reaction, isMap := data["reaction"].(map[string]any)
	if !isMap {
		data = getMap(getMap(env, "syncMessage"), "sentMessage")
		reaction, isMap = data["reaction"].(map[string]any)
		isMine = isMap
	}
	if !isMap {
		return proto.Event{}, false
	}
	target := reaction["targetSentTimestamp"]
	emoji, emojiIsStr := reaction["emoji"].(string)
	isRemove := truthy(reaction["isRemove"])
	if target == nil || !emojiIsStr || (emoji == "" && !isRemove) {
		return proto.Event{}, false
	}
	targetTS, ok := toInt64(target)
	if !ok {
		return proto.Event{}, false
	}
	eventTS, ok := toInt64(firstTruthy(env["timestamp"], data["timestamp"], 0))
	if !ok {
		return proto.Event{}, false
	}
	contact, ok := b.identifyContactForEnvelope(env)
	if !ok {
		return proto.Event{}, false
	}
	sourceKey := anyString(firstTruthy(env["sourceNumber"], env["source"]))
	sender := "You"
	authorKey := "me"
	if !isMine {
		sender = anyString(firstTruthy(env["sourceName"], sourceKey))
		authorKey = sourceKey
	}
	return proto.Event{
		Type: proto.EventReactionUpdate, Protocol: proto.ProtocolSignal, ContactID: contact.ID,
		Payload: map[string]any{
			"target_message_id": anyString(target),
			"target_timestamp":  targetTS,
			"mode":              "delta",
			"emoji":             emoji,
			"is_remove":         isRemove,
			"author":            sender,
			"author_key":        authorKey,
			"is_mine":           isMine,
			"timestamp":         eventTS,
			"contact":           contact,
		},
	}, true
}

// EnvelopeToEvents classifies a Signal envelope into zero or more normalised
// events (envelope_to_event): an edit, a reaction delta, a typing indicator,
// a receipt, or one message event per attachment.
func (b *Backend) EnvelopeToEvents(env map[string]any) []proto.Event {
	if ev, ok := b.editEnvelopeToEvent(env); ok {
		return []proto.Event{ev}
	}
	if hasEditContent(env) {
		return nil
	}
	if ev, ok := b.reactionEnvelopeToEvent(env); ok {
		return []proto.Event{ev}
	}
	if hasReactionContent(env) {
		return nil
	}
	if source, action, ok := processTyping(env); ok {
		return []proto.Event{{
			Type: proto.EventTyping, Protocol: proto.ProtocolSignal, ContactID: source,
			Payload: map[string]any{"action": action},
		}}
	}
	if _, ok := env["receiptMessage"]; ok {
		receipt := getMap(env, "receiptMessage")
		if receipt == nil {
			receipt = map[string]any{}
		}
		return []proto.Event{{
			Type: proto.EventReceipt, Protocol: proto.ProtocolSignal, ContactID: envelopeSource(env),
			Payload: map[string]any{"receipt": receipt},
		}}
	}
	contact, ok := b.identifyContactForEnvelope(env)
	if !ok {
		return nil
	}
	list := b.extractMessageData(env)
	if len(list) == 0 {
		return nil
	}
	ts := messageTimestamp(env)
	events := make([]proto.Event, 0, len(list))
	for _, data := range list {
		data["timestamp"] = ts
		data["contact"] = contact
		if data["is_mine"] == true {
			data["id"] = anyString(ts)
		}
		events = append(events, proto.Event{
			Type: proto.EventMessage, Protocol: proto.ProtocolSignal, ContactID: contact.ID, Payload: data,
		})
	}
	return events
}
