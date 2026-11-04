package messages

import (
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Ports of the pure helpers of models.py that server/messages.py uses.

// mediaQuotePlaceholders is models.MEDIA_QUOTE_PLACEHOLDERS.values().
var mediaQuotePlaceholders = []string{
	"\U0001f5bc️ Immagine",
	"\U0001f3a8 Sticker",
	"\U0001f4ce File",
	"\U0001f3b5 Audio",
	"\U0001f3ac Video",
}

// isMediaQuotePlaceholder is models.is_media_quote_placeholder.
func isMediaQuotePlaceholder(text string) bool {
	for _, p := range mediaQuotePlaceholders {
		if text == p {
			return true
		}
	}
	return false
}

// isMediaQuotePlaceholderComposite is models.is_media_quote_placeholder_composite.
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

var mediaLabels = map[string]bool{
	"photo": true, "image": true, "immagine": true, "video": true, "audio": true,
	"document": true, "documento": true, "file": true, "allegato": true, "sticker": true,
	"\U0001f5bc️ image": true, "\U0001f5bc️ immagine": true, "\U0001f5bc️ photo": true,
	"\U0001f3ac video": true, "\U0001f3b5 audio": true, "\U0001f3a8 sticker": true, "\U0001f4ce file": true,
}

var captionFileExtensions = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".heif", ".bmp", ".tif", ".tiff",
	".avif", ".svg", ".mp4", ".mov", ".mkv", ".webm", ".mp3", ".oga", ".ogg", ".opus",
	".aac", ".m4a", ".wav", ".flac", ".pdf", ".doc", ".docx",
}

// mimeOnlyRe is _MIME_ONLY_RE / the inline mime regexp of is_caption_like.
var mimeOnlyRe = regexp.MustCompile(`(?i)^[a-z0-9.+-]+/[a-z0-9.+-]+(?:[` + pySpaceClass + `]*;.*)?$`)

// matchesCaptionFilename is _CAPTION_FILENAME_RE.fullmatch: no whitespace or
// slash before the last dot, an extension of 1..5 alphanumerics.
func matchesCaptionFilename(s string) bool {
	dot := strings.LastIndexByte(s, '.')
	if dot <= 0 {
		return false
	}
	prefix, ext := s[:dot], s[dot+1:]
	if strings.ContainsAny(prefix, "/\\") || hasPySpace(prefix) {
		return false
	}
	if len(ext) < 1 || len(ext) > 5 {
		return false
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// isCaptionLike is models.is_caption_like.
func isCaptionLike(value string) bool {
	stripped := pyStrip(value)
	if stripped == "" || isMediaQuotePlaceholderComposite(stripped) {
		return false
	}
	lowered := strings.ToLower(stripped)
	if mediaLabels[lowered] || matchesCaptionFilename(stripped) {
		return false
	}
	for _, ext := range captionFileExtensions {
		if strings.HasSuffix(lowered, ext) {
			return false
		}
	}
	if mimeOnlyRe.MatchString(lowered) {
		return false
	}
	if strings.HasPrefix(lowered, "media:") {
		rest := pyStrip(stripped[len("media:"):])
		if rest != "" && !hasPySpace(rest) {
			return false
		}
	}
	return true
}

var whatsappMediaIdentityRe = regexp.MustCompile(
	`(?i)^(?:https?://[^` + pySpaceClass + `]+|(?:false|true)_[^` + pySpaceClass + `@]*@[^` + pySpaceClass + `@]+|\p{Nd}+:\p{Nd}+)$`)

// isWhatsAppSyntheticMediaText is models.is_whatsapp_synthetic_media_text with
// require_attachment_match=False (the web layer's mode).
func isWhatsAppSyntheticMediaText(text string, attachmentID *string) bool {
	stripped := pyStrip(text)
	if !strings.HasPrefix(strings.ToLower(stripped), "media:") {
		return false
	}
	identity := pyStrip(stripped[len("media:"):])
	if identity == "" {
		return false
	}
	if attachmentID != nil && *attachmentID != "" {
		if identity == *attachmentID || identity == pyName(*attachmentID) {
			return true
		}
	}
	return whatsappMediaIdentityRe.MatchString(identity)
}

// isDuplicateMediaIdentity is models.is_duplicate_media_identity(text, info).
func isDuplicateMediaIdentity(text, info string) bool {
	stripped := pyStrip(text)
	if stripped == "" || isCaptionLike(stripped) {
		return false
	}
	infoStripped := pyStrip(info)
	if infoStripped == "" {
		return false
	}
	return stripped == infoStripped
}

// attachmentBasename is models._attachment_basename.
func attachmentBasename(id string) string {
	v, _, _ := strings.Cut(id, "?")
	v, _, _ = strings.Cut(v, "#")
	return pyName(v)
}

// attachmentDisplayName is models.attachment_display_name; ok=false is None.
func attachmentDisplayName(info, attachmentID *string) (name string, ok bool) {
	in := ""
	if info != nil {
		in = pyStrip(*info)
	}
	if in != "" && !strings.HasPrefix(in, "http://") && !strings.HasPrefix(in, "https://") &&
		!mimeOnlyRe.MatchString(in) && !isCaptionLike(in) {
		return in, true
	}
	if attachmentID != nil && *attachmentID != "" {
		if b := attachmentBasename(*attachmentID); b != "" {
			return b, true
		}
	}
	if in != "" {
		return in, true
	}
	return "", false
}

// mediaMsgTypes is models.MEDIA_MSG_TYPES.
var mediaMsgTypes = map[string]bool{
	"image": true, "video": true, "audio": true, "voice": true,
	"document": true, "sticker": true, "gif": true, "attachment": true,
}

// isMediaRow is models.is_media_row.
func isMediaRow(msgType, mediaKind *string) bool {
	if mediaKind != nil && *mediaKind != "" {
		return true
	}
	return mediaMsgTypes[strings.ToLower(pyStrip(store.Deref(msgType)))]
}

var lazyIDRe = regexp.MustCompile(`^[A-Za-z0-9@._:+-]{1,256}$`)

func b64url(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// lazyMediaToken is models.lazy_media_token; ok=false is None.
func lazyMediaToken(contactID, msgID string) (string, bool) {
	for _, v := range []string{contactID, msgID} {
		if !lazyIDRe.MatchString(v) || strings.Contains(v, "..") {
			return "", false
		}
	}
	return "lazy-wa-" + b64url(contactID) + "~" + b64url(msgID), true
}

// extraString is str(extras.get(key) or "").
func extraString(extras map[string]any, key string) string { return pyStr(extras[key]) }

// contactStorageKeys is models.contact_storage_keys: every messages.contact_number
// of a contact, canonical first.
func contactStorageKeys(c proto.Contact) []string {
	if c.Protocol != api.ProtocolWhatsApp {
		return []string{c.ID}
	}
	cid := c.ID
	if cid == "" || !strings.Contains(cid, "@") || strings.HasSuffix(cid, "@g.us") {
		return []string{cid}
	}
	keys := []string{cid}
	lid := extraString(c.Extras, "lid")
	phone := extraString(c.Extras, "phone")
	if strings.HasSuffix(cid, "@lid") {
		if phone != "" {
			keys = append(keys, phone+"@c.us")
		}
	} else {
		if lid != "" && strings.HasSuffix(lid, "@lid") {
			keys = append(keys, lid)
		}
		if phone != "" && !strings.HasSuffix(cid, "@c.us") && !containsStr(keys, phone+"@c.us") {
			keys = append(keys, phone+"@c.us")
		}
	}
	var out []string
	for _, k := range keys {
		if !containsStr(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// identity is the normalised message identity of _cross_key_message_equivalent.
type identity struct {
	id           string
	isMine       bool
	text         string
	timestamp    int64
	msgType      string
	attachmentID string
}

// crossKeyEquivalent is models._cross_key_message_equivalent.
func crossKeyEquivalent(a, b identity) bool {
	if a.id != "" && b.id != "" && a.id == b.id {
		return true
	}
	return a.isMine == b.isMine && a.text == b.text && a.timestamp == b.timestamp &&
		a.msgType == b.msgType && a.attachmentID == b.attachmentID
}

// dedupCrossKey is models.dedup_cross_key over indices: it returns the indices
// of the records to keep, canonical-key rows first (kept whole), then the alias
// rows that have no equivalent among the kept ones.
func dedupCrossKey(n int, canonical string, keyOf func(i int) string, norm func(i int) identity) []int {
	if n == 0 {
		return nil
	}
	distinct := map[string]bool{}
	for i := 0; i < n; i++ {
		distinct[keyOf(i)] = true
	}
	if len(distinct) < 2 {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all
	}
	var kept []int
	var keptNorm []identity
	for i := 0; i < n; i++ {
		if keyOf(i) == canonical {
			kept = append(kept, i)
			keptNorm = append(keptNorm, norm(i))
		}
	}
	for i := 0; i < n; i++ {
		if keyOf(i) == canonical {
			continue
		}
		nm := norm(i)
		dup := false
		for _, o := range keptNorm {
			if crossKeyEquivalent(nm, o) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		kept = append(kept, i)
		keptNorm = append(keptNorm, nm)
	}
	return kept
}
