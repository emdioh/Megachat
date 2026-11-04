package signal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---- loose JSON helpers (Python dict.get semantics) ---------------------------

func getMap(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

func getStr(m map[string]any, k string) string {
	switch t := m[k].(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func getList(m map[string]any, k string) []any {
	l, _ := m[k].([]any)
	return l
}

// truthy is Python truthiness for decoded JSON / payload values.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	case []string:
		return len(t) > 0
	}
	return true
}

// firstTruthy is Python's `a or b or ...`.
func firstTruthy(vs ...any) any {
	for _, v := range vs {
		if truthy(v) {
			return v
		}
	}
	if len(vs) > 0 {
		return vs[len(vs)-1]
	}
	return nil
}

// toInt64 is int(v) for ints, floats, numeric strings and json.Number.
func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case float64:
		return int64(t), true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
		if f, err := t.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// anyString is str(v) for ids that may be strings or numbers; "" for nil.
func anyString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	}
	return ""
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func int64OrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ---- models.py helpers ---------------------------------------------------------

// Media quote placeholders (models.MEDIA_QUOTE_PLACEHOLDERS).
var mediaQuotePlaceholders = map[string]string{
	"image":      "🖼️ Immagine",
	"sticker":    "🎨 Sticker",
	"attachment": "📎 File",
	"audio":      "🎵 Audio",
	"video":      "🎬 Video",
}

var kindToPlaceholder = map[string]string{
	"image": "image", "gif": "image", "video": "video", "voice": "audio",
	"audio": "audio", "document": "attachment", "sticker": "sticker",
}

// mediaKindFromMime is models.media_kind_from_mime; "" = None.
func mediaKindFromMime(mime string, isVoice bool) string {
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

// mediaQuoteDisplay is models.media_quote_display ("" = None).
func mediaQuoteDisplay(contentType, filename string) string {
	kind := mediaKindFromMime(contentType, false)
	key, ok := kindToPlaceholder[kind]
	if kind == "" || !ok {
		return ""
	}
	ph := mediaQuotePlaceholders[key]
	if filename != "" {
		return filename + " — " + ph
	}
	return ph
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

// sniffImageContentType is media_utils.sniff_image_content_type.
func sniffImageContentType(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case strings.HasPrefix(string(data), "\x89PNG\r\n\x1a\n"):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return "image/jpeg"
}

// maxAvatarBytes is media_utils.MAX_AVATAR_BYTES.
const maxAvatarBytes = 5 * 1024 * 1024

// pySuffix is pathlib.PurePath(name).suffix.
func pySuffix(name string) string {
	name = filepath.Base(name)
	i := strings.LastIndex(name, ".")
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

func truncRunes(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ = unicode.IsSpace
