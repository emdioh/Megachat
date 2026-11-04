package media

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func (d *Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func notFound() error { return ops.Errorf(api.CodeNotFound, "Not Found") }

// BlobFor describes the existing local file path. Version is
// "<mtime_ns>-<size>", the cache-busting token messages.page puts on
// attachments.
func BlobFor(path string, contentType *string, immutable bool) api.BlobOut {
	out := api.BlobOut{Path: path, ContentType: contentType, Immutable: immutable}
	st, err := os.Stat(path)
	if err != nil {
		return out
	}
	size := st.Size()
	out.Size = &size
	out.Version = api.Ptr(fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), size))
	return out
}

// confinedAttachment is the backend's local file for the attachment, only if
// it is a regular file inside root; "" otherwise.
func (d *Deps) confinedAttachment(ctx context.Context, protocol, attachmentID, root string) string {
	var resolved string
	if d.Backends != nil {
		if be, ok := d.Backends.Get(protocol); ok {
			if r, ok := be.(proto.AttachmentResolver); ok {
				if p, ok := r.AttachmentPath(ctx, attachmentID); ok {
					resolved = p
				}
			}
		}
	}
	if resolved == "" {
		d.log().Warn("media 404", "proto", protocol, "attachment_id", attachmentID)
		return ""
	}
	p := resolvePath(resolved)
	if !isRegularFile(p) || !isRelativeTo(p, root) {
		d.log().Warn("media 404", "proto", protocol, "attachment_id", attachmentID, "resolved", p)
		return ""
	}
	return p
}

func (d *Deps) originalBlob(ctx context.Context, path, protocol, attachmentID string) api.BlobOut {
	preferAudio := strings.ToLower(filepath.Ext(path)) == ".webm" && d.mediaKind(ctx, protocol, attachmentID) == "audio"
	return BlobFor(path, mediaContentType(path, preferAudio), false)
}

// AttachmentBlob is the attachment file (not_found when missing or outside
// the media root).
func (d *Deps) AttachmentBlob(ctx context.Context, protocol, attachmentID string) (api.BlobOut, error) {
	root, err := AllowedMediaRoot(d.Backends, d.Config, protocol)
	if err != nil {
		return api.BlobOut{}, err
	}
	path := d.confinedAttachment(ctx, protocol, attachmentID, root)
	if path == "" {
		return api.BlobOut{}, notFound()
	}
	d.log().Info("media ok", "proto", protocol, "attachment_id", attachmentID, "path", path)
	return d.originalBlob(ctx, path, protocol, attachmentID), nil
}

// ThumbnailBlob is a JPEG thumbnail (immutable) of an image or video; anything
// that cannot be thumbnailed, or a width that is not a thumbnail width, is the
// original attachment. A video whose thumbnail cannot be produced is
// unprocessable.
func (d *Deps) ThumbnailBlob(ctx context.Context, protocol, attachmentID string, width int) (api.BlobOut, error) {
	if !ThumbWidths[width] {
		return d.AttachmentBlob(ctx, protocol, attachmentID)
	}
	root, err := AllowedMediaRoot(d.Backends, d.Config, protocol)
	if err != nil {
		return api.BlobOut{}, err
	}
	videoRequest := d.isVideoCandidate(ctx, attachmentID, protocol, attachmentID)
	chunkAvailable := false
	if videoRequest && (protocol == api.ProtocolWhatsApp || protocol == api.ProtocolTelegram) {
		var thumb string
		thumb, chunkAvailable = d.chunkVideoThumbnail(ctx, protocol, attachmentID, width)
		if thumb != "" {
			return BlobFor(thumb, api.Ptr("image/jpeg"), true), nil
		}
	}
	path := d.confinedAttachment(ctx, protocol, attachmentID, root)
	if path == "" {
		if chunkAvailable {
			return api.BlobOut{}, ops.Errorf(api.CodeUnprocessable, "Video thumbnail unavailable")
		}
		return api.BlobOut{}, notFound()
	}
	d.log().Info("media ok", "proto", protocol, "attachment_id", attachmentID, "path", path, "w", width)
	if thumb, ok := d.thumbnailPath(ctx, path, protocol, attachmentID, width); ok {
		return BlobFor(thumb, api.Ptr("image/jpeg"), true), nil
	}
	if videoRequest || d.isVideoCandidate(ctx, path, protocol, attachmentID) {
		thumb, ok := d.videoThumbnail(ctx, path, protocol, attachmentID, width, "")
		if !ok {
			return api.BlobOut{}, ops.Errorf(api.CodeUnprocessable, "Video thumbnail unavailable")
		}
		return BlobFor(thumb, api.Ptr("image/jpeg"), true), nil
	}
	return d.originalBlob(ctx, path, protocol, attachmentID), nil
}

// QuoteBlob is the quote thumbnail saved for message row rowID, confined to
// <CACHE_DIR>/quote-thumbs; a thumbnail of it when width is a thumbnail width.
func (d *Deps) QuoteBlob(ctx context.Context, protocol string, rowID int64, width *int) (api.BlobOut, error) {
	stored, err := d.Store.QuoteAttachmentPath(ctx, protocol, rowID)
	if err != nil {
		d.log().Error("quote media lookup database error", "proto", protocol, "row_id", rowID, "err", err)
		return api.BlobOut{}, ops.Errorf(api.CodeInternal, "Database error")
	}
	root := resolvePath(d.Config.QuoteThumbsDir())
	path := ""
	if stored != nil && *stored != "" {
		path = resolvePath(*stored)
	}
	if path == "" || !isRegularFile(path) || filepath.Dir(path) != root {
		d.log().Warn("quote media 404", "proto", protocol, "row_id", rowID, "resolved", path)
		return api.BlobOut{}, notFound()
	}
	served, immutable := path, false
	if width != nil && ThumbWidths[*width] {
		if thumb, ok := d.thumbnailPath(ctx, path, protocol, fmt.Sprint(rowID), *width); ok {
			served, immutable = thumb, true
		}
	}
	var ct *string
	if t := guessType(filepath.Base(served)); t != "" {
		ct = &t
	}
	return BlobFor(served, ct, immutable), nil
}
