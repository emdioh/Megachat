package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// laxInt mimics pydantic lax int: a JSON integer, an integral float, or a
// numeric string.
type laxInt int64

func (l *laxInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) >= 2 && s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*l = laxInt(n)
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != float64(int64(f)) {
		return fmt.Errorf("not an integer")
	}
	*l = laxInt(int64(f))
	return nil
}

type attachmentReq struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
}
type thumbnailReq struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
	Width        laxInt `json:"width"`
}
type quoteReq struct {
	Protocol     string  `json:"protocol"`
	MessageRowID laxInt  `json:"message_row_id"`
	Width        *laxInt `json:"width"`
}
type quotedReq struct {
	Protocol         string  `json:"protocol"`
	ContactKey       string  `json:"contact_key"`
	QuoteTimestamp   *laxInt `json:"quote_timestamp"`
	ReplyToMessageID *string `json:"reply_to_message_id"`
	QuoteText        string  `json:"quote_text"`
	ContentType      *string `json:"content_type"`
}

// decode validates params like pydantic: required keys must be present and
// non-null, types must fit. Any failure is invalid_request / "Invalid request".
func decode[T any](params json.RawMessage, required ...string) (T, error) {
	var zero T
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		return zero, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
	}
	for _, k := range required {
		v, ok := m[k]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return zero, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
		}
	}
	return ops.Decode[T](params)
}

func handler[T any](required []string, f func(ctx context.Context, req T) (any, error)) ops.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decode[T](params, required...)
		if err != nil {
			return nil, err
		}
		return f(ctx, req)
	}
}

func known(protocol string) error {
	if !api.IsProtocol(protocol) {
		return notFound()
	}
	return nil
}

// RegisterOps registers media.attachment, media.thumbnail, media.quote,
// media.quoted_attachment, contacts.avatar, links.preview, links.preview_image
// and, when deps.Downloader is set, download.serve_attachment and
// download.serve_text.
func RegisterOps(reg *ops.Registry, deps Deps) {
	d := &deps
	if d.Avatars == nil {
		d.Avatars = NewAvatarCache(d.Config)
	}
	if d.Previewer == nil {
		d.Previewer = NewLinkPreviewer(d.Config)
	}
	reg.Register(ops.Op{Name: api.OpMediaAttachment, Doc: "Local file of an attachment (downloaded lazily when needed).",
		Handler: handler([]string{"protocol", "attachment_id"}, func(ctx context.Context, r attachmentReq) (any, error) {
			if err := known(r.Protocol); err != nil {
				return nil, err
			}
			return d.AttachmentBlob(ctx, r.Protocol, r.AttachmentID)
		})})
	reg.Register(ops.Op{Name: api.OpMediaThumbnail, Doc: "JPEG thumbnail (96, 240 or 480) of an image or video attachment.",
		Handler: handler([]string{"protocol", "attachment_id", "width"}, func(ctx context.Context, r thumbnailReq) (any, error) {
			if err := known(r.Protocol); err != nil {
				return nil, err
			}
			return d.ThumbnailBlob(ctx, r.Protocol, r.AttachmentID, int(r.Width))
		})})
	reg.Register(ops.Op{Name: api.OpMediaQuote, Doc: "Quote thumbnail image saved for a message row.",
		Handler: handler([]string{"protocol", "message_row_id"}, func(ctx context.Context, r quoteReq) (any, error) {
			if err := known(r.Protocol); err != nil {
				return nil, err
			}
			var w *int
			if r.Width != nil {
				v := int(*r.Width)
				w = &v
			}
			return d.QuoteBlob(ctx, r.Protocol, int64(r.MessageRowID), w)
		})})
	reg.Register(ops.Op{Name: api.OpMediaQuotedAttachment, Doc: "Attachment id of the image a reply quotes (same chat), if any.",
		Handler: handler([]string{"protocol", "contact_key"}, func(ctx context.Context, r quotedReq) (any, error) {
			var ts int64
			if r.QuoteTimestamp != nil {
				ts = int64(*r.QuoteTimestamp)
			}
			reply, ct := "", ""
			if r.ReplyToMessageID != nil {
				reply = *r.ReplyToMessageID
			}
			if r.ContentType != nil {
				ct = *r.ContentType
			}
			id := QuotedAttachmentID(ctx, d.Store, r.Protocol, r.ContactKey, ts, reply, r.QuoteText, ct)
			out := api.MediaQuotedAttachmentOut{}
			if id != "" {
				out.AttachmentID = &id
			}
			return out, nil
		})})
	reg.Register(ops.Op{Name: api.OpContactsAvatar, Doc: "Profile picture of a contact (cached on disk).",
		Handler: handler([]string{"protocol", "contact_id"}, func(ctx context.Context, r api.ContactsAvatarRequest) (any, error) {
			if err := known(r.Protocol); err != nil {
				return nil, err
			}
			if strings.TrimSpace(r.ContactID) == "" {
				return nil, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
			}
			return d.AvatarBlob(ctx, r.Protocol, r.ContactID)
		})})
	reg.Register(ops.Op{Name: api.OpLinksPreview, Doc: "Preview card of a URL (SSRF guarded).",
		Handler: handler([]string{"url"}, func(ctx context.Context, r api.LinksPreviewRequest) (any, error) {
			p, err := d.Previewer.GetPreview(ctx, r.URL)
			if err != nil {
				if IsBlockedURL(err) {
					return nil, ops.Errorf(api.CodeInvalidRequest, "URL not allowed")
				}
				return nil, err
			}
			if p == nil {
				return nil, ops.Errorf(api.CodeNotFound, "No preview")
			}
			return api.LinkPreviewOut{URL: p.URL, Title: p.Title, Description: p.Description,
				SiteName: p.SiteName, HasImage: p.ImageURL != "" || p.LocalImage}, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinksPreviewImage, Doc: "The raster image advertised by the page's own preview.",
		Handler: handler([]string{"url"}, func(ctx context.Context, r api.LinksPreviewRequest) (any, error) {
			data, ct, err := d.Previewer.GetPreviewImage(ctx, r.URL)
			if err != nil {
				if IsBlockedURL(err) {
					return nil, ops.Errorf(api.CodeInvalidRequest, "URL not allowed")
				}
				return nil, err
			}
			if data == nil {
				return nil, ops.Errorf(api.CodeNotFound, "No preview image")
			}
			path := d.Previewer.MaterializeImage(data, ct)
			if path == "" {
				return nil, ops.Errorf(api.CodeNotFound, "No preview image")
			}
			return BlobFor(path, &ct, false), nil
		})})
	if d.Downloader != nil {
		registerDownload(reg, d)
	}
}

func registerDownload(reg *ops.Registry, d *Deps) {
	checked := func(url string, err error) (any, error) {
		if err != nil {
			return nil, ops.Errorf(api.CodeBackendError, "ERROR: Cannot serve file: %v", err)
		}
		if strings.HasPrefix(url, "ERROR:") {
			return nil, ops.Errorf(api.CodeBackendError, "%s", url)
		}
		return api.DownloadOut{URL: url}, nil
	}
	reg.Register(ops.Op{Name: api.OpDownloadServeAttachment, Doc: "Serve an attachment file on the temporary download server.",
		Handler: handler([]string{"protocol", "attachment_id"}, func(ctx context.Context, r api.DownloadServeAttachmentRequest) (any, error) {
			id := r.AttachmentID
			if len([]rune(id)) > 80 {
				id = string([]rune(id)[:80])
			}
			missing := ops.Errorf(api.CodeNotFound, "ERROR: Attachment file not found (id=%s)", id)
			if !api.IsProtocol(r.Protocol) {
				return nil, missing
			}
			blob, err := d.AttachmentBlob(ctx, r.Protocol, r.AttachmentID)
			if err != nil {
				if oe := ops.AsError(err); oe.Code == api.CodeNotFound {
					return nil, missing
				}
				return nil, err
			}
			if !isRegularFile(blob.Path) {
				return nil, missing
			}
			return checked(d.Downloader.ServeFilePath(blob.Path))
		})})
	reg.Register(ops.Op{Name: api.OpDownloadServeText, Doc: "Serve text as a file on the temporary download server.",
		Handler: handler([]string{"text"}, func(ctx context.Context, r api.DownloadServeTextRequest) (any, error) {
			n := r.Filename
			if n == "" || n == "." || n == ".." || filepath.Base(n) != n || strings.ContainsRune(n, 0) {
				return nil, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
			}
			return checked(d.Downloader.ServeTextAsFile(r.Text, n))
		})})
}
