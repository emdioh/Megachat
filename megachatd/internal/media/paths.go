package media

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var imageExtensions = setOf(".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".heif", ".tif", ".tiff")
var videoExtensions = setOf(".mp4", ".m4v", ".mov", ".webm", ".mkv", ".3gp", ".avi", ".mpg", ".mpeg")

func setOf(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

var audioMimeByExt = map[string]string{
	".m4a": "audio/mp4", ".aac": "audio/aac", ".oga": "audio/ogg", ".ogg": "audio/ogg",
	".opus": "audio/ogg", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".flac": "audio/flac",
}

// Python's mimetypes table for the extensions that matter here (Go's built-in
// table is much smaller and system dependent).
var guessTable = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".tif": "image/tiff", ".tiff": "image/tiff",
	".heic": "image/heic", ".heif": "image/heif", ".svg": "image/svg+xml",
	".mp4": "video/mp4", ".m4v": "video/x-m4v", ".mov": "video/quicktime", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".3gp": "video/3gpp", ".avi": "video/x-msvideo",
	".mpg": "video/mpeg", ".mpeg": "video/mpeg",
	".pdf": "application/pdf", ".zip": "application/zip", ".txt": "text/plain",
	".json": "application/json", ".html": "text/html", ".htm": "text/html", ".csv": "text/csv",
	".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// guessType is mimetypes.guess_type(name)[0] ("" when unknown).
func guessType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := guessTable[ext]; ok {
		return t
	}
	if ext == "" {
		return ""
	}
	t := mime.TypeByExtension(ext)
	if t == "" {
		return ""
	}
	if mt, _, err := mime.ParseMediaType(t); err == nil {
		return mt
	}
	return t
}

// mediaContentType is _media_content_type: deterministic audio types, else a
// guess. ".webm" is ambiguous; preferAudio serves it as audio/webm.
func mediaContentType(path string, preferAudio bool) *string {
	ext := strings.ToLower(filepath.Ext(path))
	if t, ok := audioMimeByExt[ext]; ok {
		return &t
	}
	if preferAudio && ext == ".webm" {
		return api.Ptr("audio/webm")
	}
	if t := guessType(filepath.Base(path)); t != "" {
		return &t
	}
	return nil
}

// resolvePath is Path.resolve() (non-strict): absolute, symlinks resolved as
// far as the path exists, "." and ".." removed.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest := ""
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// isRelativeTo is Path.is_relative_to for resolved paths.
func isRelativeTo(path, root string) bool {
	if path == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(path, root)
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// AllowedMediaRoot is the (resolved) directory attachments of proto must live
// in (_allowed_media_root).
func AllowedMediaRoot(b Backends, cfg Config, proto string) (string, error) {
	switch proto {
	case api.ProtocolSignal:
		return resolvePath(cfg.SignalCLIAttachmentsDir()), nil
	case api.ProtocolWhatsApp:
		if b != nil {
			if be, ok := b.Get(proto); ok && be != nil {
				if mp, ok := be.(MediaDirProvider); ok {
					d, err := mp.EnsureMediaDir()
					if err != nil {
						return "", err
					}
					return resolvePath(d), nil
				}
			}
		}
		return resolvePath(cfg.MediaDir()), nil
	case api.ProtocolTelegram:
		return resolvePath(cfg.TelegramMediaDir()), nil
	}
	return "", fmt.Errorf("Unsupported protocol: %s", proto)
}

// AttachmentVersion is the cache-busting "mtime_ns-size" of an attachment
// already on disk ("" if unknown). Local fast path only (never a lazy
// download); every candidate is resolved and confined to root first.
func AttachmentVersion(root, proto, attachmentID string) string {
	if attachmentID == "" {
		return ""
	}
	bare, _, _ := strings.Cut(attachmentID, "?")
	name := pyName(bare)
	if name == "" {
		name = bare
	}
	var candidates []string
	switch proto {
	case api.ProtocolSignal:
		candidates = []string{filepath.Join(root, name), filepath.Join(root, bare)}
	case api.ProtocolWhatsApp:
		candidates = []string{filepath.Join(root, name)}
	case api.ProtocolTelegram:
		candidates = []string{bare, filepath.Join(root, name)}
	default:
		return ""
	}
	rootResolved := resolvePath(root)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		r := resolvePath(c)
		if !isRelativeTo(r, rootResolved) {
			continue
		}
		st, err := os.Stat(r)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
	}
	return ""
}

// pyName is PurePath(p).name.
func pyName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "." {
		return ""
	}
	return p
}

// pySuffix is PurePath(name).suffix.
func pySuffix(name string) string {
	name = pyName(name)
	i := strings.LastIndex(name, ".")
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// AttachMediaVersions sets Version on every message attachment that exists on
// disk (best effort, root resolved once; never fails).
func AttachMediaVersions(b Backends, cfg Config, proto string, atts []*api.MessageAttachment) {
	root, err := AllowedMediaRoot(b, cfg, proto)
	if err != nil {
		return
	}
	memo := map[string]string{}
	for _, a := range atts {
		if a == nil || a.AttachmentID == "" {
			continue
		}
		v, ok := memo[a.AttachmentID]
		if !ok {
			v = AttachmentVersion(root, proto, a.AttachmentID)
			memo[a.AttachmentID] = v
		}
		if v != "" {
			a.Version = v
		}
	}
}

func (d *Deps) contentType(ctx context.Context, proto, id string) string {
	ct, err := d.Store.AttachmentContentType(ctx, proto, id)
	if err != nil || ct == nil {
		return ""
	}
	return *ct
}

func (d *Deps) mediaKind(ctx context.Context, proto, id string) string {
	k, err := d.Store.AttachmentMediaKind(ctx, proto, id)
	if err != nil || k == nil {
		return ""
	}
	return *k
}

// isThumbnailCandidate: an image that is not a GIF/HEIC/HEIF.
func (d *Deps) isThumbnailCandidate(ctx context.Context, path, proto, id string) bool {
	ct := strings.ToLower(d.contentType(ctx, proto, id))
	suffix := strings.ToLower(filepath.Ext(path))
	isImage := strings.HasPrefix(ct, "image/") || imageExtensions[suffix]
	return isImage && suffix != ".gif" && suffix != ".heic" && suffix != ".heif" &&
		ct != "image/gif" && ct != "image/heic" && ct != "image/heif"
}

// isVideoCandidate: audio never goes through the video pipeline.
func (d *Deps) isVideoCandidate(ctx context.Context, path, proto, id string) bool {
	ct := strings.ToLower(d.contentType(ctx, proto, id))
	if strings.HasPrefix(ct, "audio/") {
		return false
	}
	if ct == "" && d.mediaKind(ctx, proto, id) == "audio" {
		return false
	}
	return strings.HasPrefix(ct, "video/") || videoExtensions[strings.ToLower(filepath.Ext(path))]
}

var quoteFilenameRE = regexp.MustCompile(`(?i)([A-Za-z0-9][\w.\- ]*\.(?:jpe?g|png|gif|webp))\b`)

func quotedByFilename(ctx context.Context, st Store, protocol, contactKey, quoteText, contentType string) string {
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return ""
	}
	m := quoteFilenameRE.FindStringSubmatch(quoteText)
	if m == nil {
		return ""
	}
	id, err := st.QuotedImageByFilename(ctx, protocol, contactKey, strings.TrimSpace(m[1]))
	if err != nil {
		return ""
	}
	return id
}

// QuotedAttachmentID is quoted_attachment_id: the image a reply quotes in the
// same chat. Exact timestamp first (+-2 s only if no row has it), then (no
// timestamp) quoted message id, then file name.
func QuotedAttachmentID(ctx context.Context, st Store, protocol, contactKey string, quoteTS int64, replyToMessageID, quoteText, contentType string) string {
	if quoteTS == 0 {
		if replyToMessageID != "" {
			if id, err := st.QuotedMediaByMessageID(ctx, protocol, contactKey, replyToMessageID, store.MediaImage); err == nil && id != "" {
				return id
			}
		}
		return quotedByFilename(ctx, st, protocol, contactKey, quoteText, contentType)
	}
	id, err := st.QuotedMediaAttachmentID(ctx, protocol, contactKey, quoteTS, store.MediaImage)
	if err != nil {
		return ""
	}
	if id != "" {
		return id
	}
	// The quoted message exists but is not an image: never fall back.
	if exists, err := st.MessageExistsAtTimestamp(ctx, protocol, contactKey, quoteTS); err != nil || exists {
		return ""
	}
	return quotedByFilename(ctx, st, protocol, contactKey, quoteText, contentType)
}
