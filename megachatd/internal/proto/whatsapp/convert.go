package whatsapp

import (
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// ---- models.py helpers --------------------------------------------------------------

var mediaQuotePlaceholders = map[string]string{
	"image":      "🖼️ Immagine",
	"sticker":    "🎨 Sticker",
	"attachment": "📎 File",
	"audio":      "🎵 Audio",
	"video":      "🎬 Video",
}

// mediaQuotePlaceholder is models.media_quote_placeholder.
func mediaQuotePlaceholder(msgType, detail string) string {
	if detail != "" {
		return detail
	}
	if p, ok := mediaQuotePlaceholders[msgType]; ok {
		return p
	}
	return mediaQuotePlaceholders["attachment"]
}

// mediaKindFromMime is models.media_kind_from_mime; "" = None.
func mediaKindFromMime(mimeType string, isGIF, isVoice bool) string {
	n := strings.TrimSpace(strings.SplitN(strings.ToLower(mimeType), ";", 2)[0])
	switch {
	case n == "":
		return ""
	case n == "image/gif" || isGIF:
		return "gif"
	case strings.HasPrefix(n, "image/"):
		return "image"
	case strings.HasPrefix(n, "video/"):
		return "video"
	case strings.HasPrefix(n, "audio/"):
		if isVoice {
			return "voice"
		}
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

// mediaNameAndCaption is _wa_media_name_and_caption: (attachment_info, caption).
//
//   - image/gif: the caption lives in attachment_info, text stays empty;
//   - sticker/voice: attachment_info is the real filename only, caption in text;
//   - others: filename (mime as fallback) in attachment_info, caption in text.
func mediaNameAndCaption(kind, itemCaption, filename, mimeType, fallback string) (info, caption string) {
	first := func(vs ...string) string {
		for _, v := range vs {
			if v != "" {
				return v
			}
		}
		return ""
	}
	switch kind {
	case "image", "gif":
		return first(itemCaption, filename, mimeType, fallback), ""
	case "sticker", "voice":
		return filename, itemCaption
	}
	return first(filename, mimeType, fallback), itemCaption
}

// ---- message content -----------------------------------------------------------------

// mediaPart is a media message of a WhatsApp message.
type mediaPart struct {
	Kind     string // image gif video audio voice document sticker
	Mime     string
	FileName string
	Caption  string
	Size     uint64
	Msg      whatsmeow.DownloadableMessage
}

// locationPart is a (live) location message.
type locationPart struct {
	Lat, Lng             float64
	Name, Address, Descr string
	Live                 bool
	Thumbnail            []byte
	Caption              string
}

// content is the interesting part of a waE2E.Message.
type content struct {
	Text      string // conversation / extended text / caption
	Media     *mediaPart
	Location  *locationPart
	Contact   string // shared contact card display name(s)
	Ctx       *waE2E.ContextInfo
	Mentioned []string
}

// extractContent flattens a waE2E.Message.
func extractContent(m *waE2E.Message) content {
	var c content
	if m == nil {
		return c
	}
	switch {
	case m.GetConversation() != "":
		c.Text = m.GetConversation()
	case m.GetExtendedTextMessage() != nil:
		et := m.GetExtendedTextMessage()
		c.Text, c.Ctx = et.GetText(), et.GetContextInfo()
	}
	if im := m.GetImageMessage(); im != nil {
		c.Media = &mediaPart{Kind: "image", Mime: im.GetMimetype(), Caption: im.GetCaption(), Size: im.GetFileLength(), Msg: im}
		if c.Media.Mime == "image/gif" {
			c.Media.Kind = "gif"
		}
		c.Ctx = im.GetContextInfo()
	} else if vm := m.GetVideoMessage(); vm != nil {
		kind := "video"
		if vm.GetGifPlayback() {
			kind = "gif"
		}
		c.Media = &mediaPart{Kind: kind, Mime: vm.GetMimetype(), Caption: vm.GetCaption(), Size: vm.GetFileLength(), Msg: vm}
		c.Ctx = vm.GetContextInfo()
	} else if am := m.GetAudioMessage(); am != nil {
		kind := "audio"
		if am.GetPTT() {
			kind = "voice"
		}
		c.Media = &mediaPart{Kind: kind, Mime: am.GetMimetype(), Size: am.GetFileLength(), Msg: am}
		c.Ctx = am.GetContextInfo()
	} else if dm := m.GetDocumentMessage(); dm != nil {
		name := dm.GetFileName()
		c.Media = &mediaPart{Kind: "document", Mime: dm.GetMimetype(), FileName: name, Caption: dm.GetCaption(), Size: dm.GetFileLength(), Msg: dm}
		c.Ctx = dm.GetContextInfo()
	} else if sm := m.GetStickerMessage(); sm != nil {
		c.Media = &mediaPart{Kind: "sticker", Mime: sm.GetMimetype(), Size: sm.GetFileLength(), Msg: sm}
		c.Ctx = sm.GetContextInfo()
	}
	if c.Media != nil {
		c.Text = c.Media.Caption
	}
	if lm := m.GetLocationMessage(); lm != nil {
		c.Location = &locationPart{Lat: lm.GetDegreesLatitude(), Lng: lm.GetDegreesLongitude(),
			Name: lm.GetName(), Address: lm.GetAddress(), Descr: lm.GetComment(), Thumbnail: lm.GetJPEGThumbnail()}
		c.Ctx = lm.GetContextInfo()
	} else if ll := m.GetLiveLocationMessage(); ll != nil {
		c.Location = &locationPart{Lat: ll.GetDegreesLatitude(), Lng: ll.GetDegreesLongitude(),
			Descr: ll.GetCaption(), Live: true, Thumbnail: ll.GetJPEGThumbnail()}
		c.Ctx = ll.GetContextInfo()
	}
	if cm := m.GetContactMessage(); cm != nil {
		c.Contact = cm.GetDisplayName()
		c.Ctx = cm.GetContextInfo()
	} else if cam := m.GetContactsArrayMessage(); cam != nil {
		var names []string
		for _, x := range cam.GetContacts() {
			if n := x.GetDisplayName(); n != "" {
				names = append(names, n)
			}
		}
		c.Contact = strings.Join(names, ", ")
		if c.Contact == "" {
			c.Contact = cam.GetDisplayName()
		}
		c.Ctx = cam.GetContextInfo()
	}
	if c.Ctx != nil {
		c.Mentioned = c.Ctx.GetMentionedJID()
	}
	return c
}

// mapsURL is location_thumbs.maps_url.
func mapsURL(lat, lng float64) string {
	return fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", lat, lng)
}

// locationText is _wa_location_text: readable text with a Google Maps link.
func locationText(l *locationPart) string {
	label := "📍 Posizione"
	if l.Live {
		label = "📍 Posizione in tempo reale"
	}
	var details []string
	for _, v := range []string{l.Name, l.Address, l.Descr} {
		if v = strings.TrimSpace(v); v != "" {
			details = append(details, v)
		}
	}
	// Descriptions often repeat name/address joined by a newline.
	var seen []string
	for _, d := range details {
		dup := false
		for _, s := range seen {
			if strings.Contains(s, d) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		kept := seen[:0]
		for _, s := range seen {
			if !strings.Contains(d, s) {
				kept = append(kept, s)
			}
		}
		seen = append(kept, d)
	}
	first := label
	if len(seen) > 0 {
		first += ": " + seen[0]
	}
	lines := []string{first}
	if len(seen) > 1 {
		lines = append(lines, seen[1:]...)
	}
	lines = append(lines, mapsURL(l.Lat, l.Lng))
	return strings.Join(lines, "\n")
}

// mimeExtension maps a mime type to the file extension WAHA used for media
// files (".jpeg", ".oga", ...).
func mimeExtension(mimeType, fileName string) string {
	if ext := strings.ToLower(filepath.Ext(fileName)); ext != "" && len(ext) <= 8 && safeExtRe.MatchString(ext) {
		return ext
	}
	base := strings.TrimSpace(strings.SplitN(strings.ToLower(mimeType), ";", 2)[0])
	if ext, ok := extOverrides[base]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(base); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

var safeExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,7}$`)

var extOverrides = map[string]string{
	"image/jpeg":      ".jpeg",
	"image/png":       ".png",
	"image/webp":      ".webp",
	"image/gif":       ".gif",
	"video/mp4":       ".mp4",
	"audio/ogg":       ".oga",
	"audio/mpeg":      ".mp3",
	"audio/mp4":       ".m4a",
	"audio/aac":       ".aac",
	"application/pdf": ".pdf",
}
