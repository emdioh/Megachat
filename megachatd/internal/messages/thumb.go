package messages

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/store"
)

// quoteStore is the slice of internal/store the quote-thumbnail cascade uses.
type quoteStore interface {
	QuotedMediaAttachmentID(ctx context.Context, proto, contact string, quoteTS int64, kind store.MediaFilter) (string, error)
	QuotedMediaByFilename(ctx context.Context, proto, contact, filename string, quoterTS int64, kind store.MediaFilter) (string, error)
	QuotedMediaByMessageID(ctx context.Context, proto, contact, replyToMessageID string, kind store.MediaFilter) (string, error)
}

// videoExtensions is server.media._VIDEO_EXTENSIONS, sorted.
var videoExtensions = []string{".3gp", ".avi", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".webm"}

var (
	quoteImageExtensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	// imageNameExtensions is the set of the regexp `\.(?:jpe?g|png|gif|webp)\b`.
	imageNameExtensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
)

func isVideoExt(ext string) bool {
	for _, e := range videoExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}

func filterFor(kind string) store.MediaFilter {
	switch kind {
	case "image":
		return store.MediaImage
	case "video":
		return store.MediaVideo
	}
	return store.MediaAny
}

var (
	quoteNameImagePattern = `jpe?g|png|gif|webp`
	quoteNameVideoPattern = func() string {
		parts := make([]string, len(videoExtensions))
		for i, e := range videoExtensions {
			parts[i] = strings.TrimPrefix(e, ".")
		}
		return strings.Join(parts, "|")
	}()
	// The trailing group emulates the regexp's `\b` with Python's Unicode \w.
	quoteNameRe = map[string]*regexp.Regexp{
		"image": quoteNameRegexp(quoteNameImagePattern),
		"video": quoteNameRegexp(quoteNameVideoPattern),
	}
)

func quoteNameRegexp(exts string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)([A-Za-z0-9][\p{L}\p{N}_.\- ]*\.(?:` + exts + `))(?:$|[^\p{L}\p{N}_])`)
}

// quotedFilename extracts the media file name a textual quote cites.
func quotedFilename(quoteText, kind string) string {
	re := quoteNameRe[kind]
	if re == nil {
		return ""
	}
	m := re.FindStringSubmatch(quoteText)
	if m == nil {
		return ""
	}
	return pyStrip(m[1])
}

// quoteThumbURL is _quote_thumb_url: the thumbnail URL of a quote, computed by
// the server, or nil.  cacheDir is CACHE_DIR (quote thumbnails live under
// <cacheDir>/quote-thumbs).
func quoteThumbURL(ctx context.Context, st quoteStore, cacheDir string, row *msg) *string {
	proto := row.Protocol
	thumbsRoot := resolvePath(filepath.Join(cacheDir, "quote-thumbs"))

	if path := strOf(row.QuoteAttachmentPath); path != "" {
		resolved := resolvePath(path)
		if pyParent(resolved) == thumbsRoot {
			u := fmt.Sprintf("api/quote-media/%s/%d?w=96", proto, row.RowID)
			return &u
		}
		ct := strings.ToLower(strOf(row.QuoteContentType))
		isImage := strings.HasPrefix(ct, "image/") || hasAnySuffix(strings.ToLower(path), quoteImageExtensions)
		isVideo := strings.HasPrefix(ct, "video/") || isVideoExt(strings.ToLower(pySuffix(resolved)))
		if (isImage || isVideo) && isRegularFile(resolved) {
			u := mediaURL(proto, path)
			return &u
		}
	}

	qid := strOf(row.QuoteAttachmentID)
	qct := strings.ToLower(strOf(row.QuoteContentType))
	// str(None) is "None" in Python; it never ends in a known extension.
	isImage := strings.HasPrefix(qct, "image/") || (qct == "" && hasAnySuffix(strings.ToLower(qid), quoteImageExtensions))
	isVideo := strings.HasPrefix(qct, "video/") || (qct == "" && isVideoExt(strings.ToLower(pySuffix(qid))))
	if qid != "" && (isImage || isVideo) {
		u := mediaURL(proto, qid)
		return &u
	}

	// 4. Fallbacks for replies without persisted quote metadata.
	quoteText := strings.ToLower(strOf(row.QuoteText))
	hasVideoName := false
	for _, e := range videoExtensions {
		if containsWordSuffix(quoteText, e) {
			hasVideoName = true
			break
		}
	}
	hasImageName := false
	for _, e := range imageNameExtensions {
		if containsWordSuffix(quoteText, e) {
			hasImageName = true
			break
		}
	}
	kind := ""
	switch {
	case strings.HasPrefix(qct, "video/") || hasVideoName || strings.Contains(quoteText, "\U0001f3ac"):
		kind = "video"
	case strings.HasPrefix(qct, "image/") || hasImageName || strings.Contains(quoteText, "\U0001f5bc️"):
		kind = "image"
	}
	var quoteTS int64
	if row.QuoteTimestamp != nil {
		quoteTS = *row.QuoteTimestamp
	}
	contact := row.ContactNumber
	quoted, _ := st.QuotedMediaAttachmentID(ctx, proto, contact, quoteTS, filterFor(kind))
	if quoted == "" && kind != "" {
		// 5. By file name cited by the quote text.
		if name := quotedFilename(strOf(row.QuoteText), kind); name != "" && row.Timestamp != 0 {
			quoted, _ = st.QuotedMediaByFilename(ctx, proto, contact, name, row.Timestamp, filterFor(kind))
		}
	}
	if quoted == "" {
		// 6. By quoted message id.
		quoted, _ = st.QuotedMediaByMessageID(ctx, proto, contact, strOf(row.ReplyToMessageID), filterFor(kind))
	}
	if quoted != "" {
		u := mediaURL(proto, quoted)
		return &u
	}
	return nil
}
