package telegram

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

// channelOffset is Telethon's marked-id offset for channels (-100 prefix).
const channelOffset = 1000000000000

// PeerKind is the kind of a Telegram peer.
type PeerKind int

const (
	PeerUser PeerKind = iota
	PeerChat
	PeerChannel
)

// markedID is Telethon's utils.get_peer_id: users keep their id, legacy
// groups are -id and channels/supergroups -1000000000000-id. This is the
// string Python stored as contact_number (Message.chat_id).
func markedID(kind PeerKind, raw int64) int64 {
	switch kind {
	case PeerChat:
		return -raw
	case PeerChannel:
		return -channelOffset - raw
	}
	return raw
}

// splitMarked is utils.resolve_id: it maps a marked id back to (kind, raw id).
func splitMarked(id int64) (PeerKind, int64) {
	switch {
	case id < -channelOffset:
		return PeerChannel, -channelOffset - id
	case id < 0:
		return PeerChat, -id
	}
	return PeerUser, id
}

// peerMarked returns the marked id of a tg.PeerClass.
func peerMarked(p tg.PeerClass) (int64, bool) {
	switch t := p.(type) {
	case *tg.PeerUser:
		return markedID(PeerUser, t.UserID), true
	case *tg.PeerChat:
		return markedID(PeerChat, t.ChatID), true
	case *tg.PeerChannel:
		return markedID(PeerChannel, t.ChannelID), true
	}
	return 0, false
}

func peerContactID(p tg.PeerClass) (string, bool) {
	id, ok := peerMarked(p)
	if !ok {
		return "", false
	}
	return strconv.FormatInt(id, 10), true
}

// toInt is _to_int: a base-10 parse that never fails loudly.
func toInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

// ---- models.py helpers ---------------------------------------------------

var mediaQuotePlaceholders = map[string]string{
	"image":      "🖼️ Immagine",
	"sticker":    "🎨 Sticker",
	"attachment": "📎 File",
	"audio":      "🎵 Audio",
	"video":      "🎬 Video",
}

// mediaQuotePlaceholder is models.media_quote_placeholder (without detail).
func mediaQuotePlaceholder(msgType string) string {
	if p, ok := mediaQuotePlaceholders[msgType]; ok {
		return p
	}
	return mediaQuotePlaceholders["attachment"]
}

// mediaKindFromMime is models.media_kind_from_mime; "" = None.
func mediaKindFromMime(mime string) string {
	n := strings.TrimSpace(strings.SplitN(strings.ToLower(mime), ";", 2)[0])
	switch {
	case n == "":
		return ""
	case n == "image/gif":
		return "gif"
	case strings.HasPrefix(n, "image/"):
		return "image"
	case strings.HasPrefix(n, "video/"):
		return "video"
	case strings.HasPrefix(n, "audio/"):
		return "audio"
	}
	return "document"
}

// msgTypeForMediaKind is models.msg_type_for_media_kind.
func msgTypeForMediaKind(kind string) string {
	switch kind {
	case "image", "gif":
		return "image"
	case "sticker":
		return "sticker"
	}
	return "attachment"
}

func isMediaQuotePlaceholderComposite(text string) bool {
	if text == "" {
		return false
	}
	for _, p := range mediaQuotePlaceholders {
		if text == p || strings.HasSuffix(text, " — "+p) {
			return true
		}
	}
	return false
}

var (
	captionFilenameRE = regexp.MustCompile(`(?i)^[^\s/\\]+\.[a-z0-9]{1,5}$`)
	mimeOnlyRE        = regexp.MustCompile(`^[a-z0-9.+-]+/[a-z0-9.+-]+(?:\s*;.*)?$`)
	nonSpaceRE        = regexp.MustCompile(`^\S+$`)
)

var captionFileExtensions = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".heif", ".bmp", ".tif", ".tiff",
	".avif", ".svg", ".mp4", ".mov", ".mkv", ".webm", ".mp3", ".oga", ".ogg", ".opus",
	".aac", ".m4a", ".wav", ".flac", ".pdf", ".doc", ".docx",
}

var mediaLabels = map[string]bool{
	"photo": true, "image": true, "immagine": true, "video": true, "audio": true,
	"document": true, "documento": true, "file": true, "allegato": true, "sticker": true,
	"🖼️ image": true, "🖼️ immagine": true, "🖼️ photo": true, "🎬 video": true,
	"🎵 audio": true, "🎨 sticker": true, "📎 file": true,
}

// isCaptionLike is models.is_caption_like.
func isCaptionLike(value string) bool {
	stripped := strings.TrimSpace(value)
	if stripped == "" || isMediaQuotePlaceholderComposite(stripped) {
		return false
	}
	lowered := strings.ToLower(stripped)
	if mediaLabels[lowered] || captionFilenameRE.MatchString(stripped) {
		return false
	}
	for _, ext := range captionFileExtensions {
		if strings.HasSuffix(lowered, ext) {
			return false
		}
	}
	if mimeOnlyRE.MatchString(lowered) {
		return false
	}
	if strings.HasPrefix(lowered, "media:") && nonSpaceRE.MatchString(strings.TrimSpace(stripped[6:])) {
		return false
	}
	return true
}

// ---- telegram.py helpers ---------------------------------------------------

// tgIsPlaceholder is _tg_is_placeholder on a display name / id pair.
func tgIsPlaceholder(displayName, id string) bool {
	if displayName == "" || displayName == id {
		return true
	}
	for _, r := range displayName {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// tgLabelFor is _tg_label_for.
func tgLabelFor(kind, filename string) string {
	if filename != "" {
		return "📎 " + filename
	}
	switch kind {
	case "image":
		return "Photo"
	case "gif":
		return "📎 Document"
	case "video":
		return "🎬 Video"
	case "voice":
		return "🎤 Voice"
	case "audio":
		return "🎵 Audio"
	case "document":
		return "📎 Document"
	case "sticker":
		return "🎨 Sticker"
	}
	return "📎 Document"
}

var tgQuoteInfoType = map[string]string{
	"Photo":      "image",
	"🎨 Sticker":  "sticker",
	"🎬 Video":    "video",
	"🎤 Voice":    "audio",
	"🎵 Audio":    "audio",
	"📎 Document": "attachment",
}

// tgQuoteTextFromCached is _tg_quote_text_from_cached; ok=false is None.
func tgQuoteTextFromCached(e *entry) (string, bool) {
	msgType := e.MsgType
	if msgType == "" {
		msgType = "text"
	}
	if msgType == "text" {
		return e.Text, e.Text != ""
	}
	caption := strings.TrimSpace(e.Text)
	if caption != "" {
		return caption, true
	}
	info := strings.TrimSpace(e.AttachmentInfo)
	if fine, ok := tgQuoteInfoType[info]; ok {
		return mediaQuotePlaceholder(fine), true
	}
	if info != "" {
		return info + " — " + mediaQuotePlaceholder(msgType), true
	}
	return mediaQuotePlaceholder(msgType), true
}

// mediaInfo is the part of a tg.Message the Telethon helpers look at.
type mediaInfo struct {
	Photo *tg.Photo
	Doc   *tg.Document
}

func (m mediaInfo) any() bool { return m.Photo != nil || m.Doc != nil }

// messageMedia mirrors Message.photo / Message.document: plain media plus the
// photo/document of a web page preview.
func messageMedia(msg *tg.Message) mediaInfo {
	var mi mediaInfo
	switch m := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := m.Photo.(*tg.Photo); ok {
			mi.Photo = p
		}
	case *tg.MessageMediaDocument:
		if d, ok := m.Document.(*tg.Document); ok {
			mi.Doc = d
		}
	case *tg.MessageMediaWebPage:
		if wp, ok := m.Webpage.(*tg.WebPage); ok {
			if p, ok := wp.Photo.(*tg.Photo); ok {
				mi.Photo = p
			}
			if d, ok := wp.Document.(*tg.Document); ok {
				mi.Doc = d
			}
		}
	}
	return mi
}

// docAttrs summarises the DocumentAttribute list.
type docAttrs struct {
	Filename                             string
	Sticker, Animated, Video, Voice, Aud bool
	Performer, Title                     string
}

func readDocAttrs(d *tg.Document) docAttrs {
	var a docAttrs
	for _, attr := range d.Attributes {
		switch t := attr.(type) {
		case *tg.DocumentAttributeFilename:
			if t.FileName != "" {
				a.Filename = t.FileName
			}
		case *tg.DocumentAttributeSticker:
			a.Sticker = true
		case *tg.DocumentAttributeAnimated:
			a.Animated = true
		case *tg.DocumentAttributeVideo:
			a.Video = true
		case *tg.DocumentAttributeAudio:
			if t.Voice {
				a.Voice = true
			} else {
				a.Aud = true
			}
			if a.Performer == "" {
				a.Performer = t.Performer
			}
			if a.Title == "" {
				a.Title = t.Title
			}
		}
	}
	return a
}

// tgMediaKind is _tg_media_kind: (kind, filename, mime); kind "" = no media.
func tgMediaKind(msg *tg.Message) (kind, filename, mime string) {
	mi := messageMedia(msg)
	if mi.Photo != nil {
		return "image", "", "image/jpeg"
	}
	if mi.Doc == nil {
		return "", "", ""
	}
	mime = strings.ToLower(mi.Doc.MimeType)
	a := readDocAttrs(mi.Doc)
	filename = a.Filename
	switch {
	case a.Sticker:
		return "sticker", filename, mime
	case a.Animated:
		return "gif", filename, mime
	case a.Voice:
		return "voice", filename, mime
	case a.Video:
		return "video", filename, mime
	case a.Aud || strings.HasPrefix(mime, "audio/"):
		return "audio", filename, mime
	}
	if k := mediaKindFromMime(mime); k != "" {
		return k, filename, mime
	}
	return "document", filename, mime
}

// ---- Telethon markdown unparse ---------------------------------------------

// ---- file naming -------------------------------------------------------------

var mimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"image/heic": ".heic", "image/bmp": ".bmp", "image/tiff": ".tiff", "image/svg+xml": ".svg",
	"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm", "video/x-matroska": ".mkv",
	"video/mpeg": ".mpeg",
	"audio/mpeg": ".mp3", "audio/ogg": ".oga", "audio/opus": ".opus", "audio/mp4": ".m4a",
	"audio/x-wav": ".wav", "audio/wav": ".wav", "audio/flac": ".flac", "audio/aac": ".aac",
	"application/pdf": ".pdf", "application/zip": ".zip", "application/json": ".json",
	"application/x-tgsticker": ".tgs", "text/plain": ".txt", "application/msword": ".doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
}

// guessExtension approximates mimetypes.guess_extension.
func guessExtension(mime string) string {
	return mimeExt[strings.ToLower(strings.TrimSpace(mime))]
}

// splitExt is os.path.splitext on a base name.
func splitExt(name string) (string, string) {
	ext := filepath.Ext(name)
	if ext == name { // ".hidden"
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}

// pySuffix is pathlib.PurePath(name).suffix.
func pySuffix(name string) string {
	name = filepath.Base(name)
	i := strings.LastIndex(name, ".")
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}
