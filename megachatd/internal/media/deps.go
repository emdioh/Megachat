// Package media is the Go port of the media side of the Python server:
// attachment/thumbnail/quote/avatar blobs (server/blobs.py, media.py,
// thumbnails.py, video_thumbs.py, avatar_cache.py), the upload policy
// (server/uploads.py), the SSRF-guarded link previews (server/link_preview.py)
// and the operations that expose them (server/ops/media.py, links.py,
// download.py).
//
// server/attach.py is the TUI client's "find or spawn a server" logic; it has
// no server-side counterpart and is not ported.
package media

import (
	"context"
	"log/slog"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Store is the slice of internal/store this package queries.
// *store.Store implements it (QuotedImageByFilename is added by this work).
type Store interface {
	AttachmentContentType(ctx context.Context, proto, attachmentID string) (*string, error)
	AttachmentMediaKind(ctx context.Context, proto, attachmentID string) (*string, error)
	QuoteAttachmentPath(ctx context.Context, proto string, rowID int64) (*string, error)
	QuotedMediaAttachmentID(ctx context.Context, proto, contact string, quoteTS int64, kind store.MediaFilter) (string, error)
	QuotedMediaByMessageID(ctx context.Context, proto, contact, replyToMessageID string, kind store.MediaFilter) (string, error)
	MessageExistsAtTimestamp(ctx context.Context, proto, contact string, ts int64) (bool, error)
	QuotedImageByFilename(ctx context.Context, proto, contact, filename string) (string, error)
}

// Backends looks a protocol backend up. *proto.Manager implements it.
type Backends interface {
	Get(protocol string) (proto.Backend, bool)
}

// Config is the path configuration this package needs. *config.Config
// implements it.
type Config interface {
	CacheDir() string
	MediaDir() string
	TelegramMediaDir() string
	QuoteThumbsDir() string
	LocationThumbsDir() string
	LinkPreviewImagesDir() string
	SignalCLIAttachmentsDir() string
	WebThumbDir(protocol string) string
	AvatarDir(name string) string
	AvatarBlobsDir(protocol string) string
}

// Downloader is the temporary download HTTP server (protocols/download.py,
// port 10042). Both methods return a URL, or an "ERROR: ..." string like the
// Python helpers. Optional: without it the download.* ops are not registered.
type Downloader interface {
	ServeFilePath(path string) (url string, err error)
	ServeTextAsFile(text, filename string) (url string, err error)
}

// Deps are the collaborators of RegisterOps.
type Deps struct {
	Store      Store
	Backends   Backends
	Config     Config
	Downloader Downloader     // optional
	Previewer  *LinkPreviewer // optional; a default one is built when nil
	Avatars    *AvatarCache   // optional; a default one is built when nil
	Logger     *slog.Logger   // optional
}

// MediaDirProvider is implemented by a WhatsApp backend that knows its media
// directory (Python _ensure_media_dir).
type MediaDirProvider interface {
	EnsureMediaDir() (string, error)
}

// ProfilePhotoProvider is implemented by Signal and Telegram backends
// (get_profile_photo_bytes). Empty data means "no picture". contentType may
// be "" (then it is sniffed).
type ProfilePhotoProvider interface {
	ProfilePhoto(ctx context.Context, contactID string) (data []byte, contentType string, err error)
}

// ProfilePictureURLProvider is implemented by the WhatsApp backend
// (rest.get_profile_picture_url). noPhoto is true when WAHA answered 200 but
// without a picture (Python last_status == 200), which earns a negative marker.
type ProfilePictureURLProvider interface {
	ProfilePictureURL(ctx context.Context, contactID string) (url string, noPhoto bool, err error)
}
