package store

import (
	"regexp"
	"strings"
	"unicode"
)

// Ports of the pure helpers of models.py that protocols/db.py depends on
// (status rank, display-sender choice, caption/identity predicates and the
// twin survivor/merge selection).

// statusRank is models._status_rank: NULL/unknown -> 0.
func statusRank(status *string) int {
	if status == nil {
		return 0
	}
	switch strings.ToLower(*status) {
	case "pending", "failed":
		return 0
	case "sent":
		return 1
	case "delivered":
		return 2
	case "read":
		return 3
	}
	return 0
}

// preferDisplaySender is models._prefer_display_sender.
func preferDisplaySender(a, b *string) *string {
	isDisplay := func(v *string) bool { return v != nil && *v != "" && !strings.Contains(*v, "@") }
	if isDisplay(a) {
		return a
	}
	if isDisplay(b) {
		return b
	}
	// Python: ``a or b`` (a falsy, i.e. None or "").
	if a != nil && *a != "" {
		return a
	}
	return b
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

var mimeOnlyRe = regexp.MustCompile(`^[a-z0-9.+-]+/[a-z0-9.+-]+(?:\s*;.*)?$`)

func hasSpace(s string) bool { return strings.IndexFunc(s, unicode.IsSpace) >= 0 }

// matchesCaptionFilename is _CAPTION_FILENAME_RE.fullmatch.
func matchesCaptionFilename(s string) bool {
	dot := strings.LastIndexByte(s, '.')
	if dot <= 0 {
		return false
	}
	prefix, ext := s[:dot], s[dot+1:]
	if strings.ContainsAny(prefix, "/\\") || hasSpace(prefix) {
		return false
	}
	if len(ext) < 1 || len(ext) > 5 {
		return false
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// isCaptionLike is models.is_caption_like.
func isCaptionLike(value *string) bool {
	stripped := strings.TrimSpace(Deref(value))
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
		rest := strings.TrimSpace(stripped[6:])
		if rest != "" && !hasSpace(rest) {
			return false
		}
	}
	return true
}

var whatsappMediaIdentityRe = regexp.MustCompile(
	`(?i)^(?:https?://\S+|(?:false|true)_[^\s@]*@[^\s@]+|\d+:\d+)$`)

// pyPathName mimics pathlib.PurePosixPath(value).name.
func pyPathName(value string) string {
	parts := strings.Split(value, "/")
	name := ""
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		name = p
	}
	return name
}

// isWhatsAppSyntheticMediaText is models.is_whatsapp_synthetic_media_text.
func isWhatsAppSyntheticMediaText(text string, attachmentID *string, requireAttachmentMatch bool) bool {
	stripped := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(stripped), "media:") {
		return false
	}
	identity := strings.TrimSpace(stripped[len("media:"):])
	if identity == "" {
		return false
	}
	if attachmentID != nil && *attachmentID != "" {
		if identity == *attachmentID || identity == pyPathName(*attachmentID) {
			return true
		}
	}
	if requireAttachmentMatch {
		return false
	}
	return whatsappMediaIdentityRe.MatchString(identity)
}

// isDuplicateMediaIdentity is models.is_duplicate_media_identity.
func isDuplicateMediaIdentity(text, info *string) bool {
	stripped := strings.TrimSpace(Deref(text))
	if stripped == "" || isCaptionLike(&stripped) {
		return false
	}
	infoStripped := strings.TrimSpace(Deref(info))
	if infoStripped == "" {
		return false
	}
	return stripped == infoStripped
}

// isSentMirrorAttachmentID is models.is_sent_mirror_attachment_id.
func isSentMirrorAttachmentID(value *string) bool {
	if value == nil || *value == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(pyPathName(*value)), "sent-")
}
