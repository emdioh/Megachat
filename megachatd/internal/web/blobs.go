package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// blob is the result of the blob-producing operations (BlobOut, plus the
// filename of gmail.attachment).
type blob struct {
	Path        string  `json:"path"`
	ContentType *string `json:"content_type"`
	Immutable   bool    `json:"immutable"`
	Filename    string  `json:"filename"`
}

func decodeBlob(res any) (blob, error) {
	var b blob
	raw, err := json.Marshal(res)
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	if err != nil || b.Path == "" {
		return b, ops.Errorf(api.CodeInternal, "Internal Server Error")
	}
	return b, nil
}

func (b blob) mediaType() string {
	if b.ContentType == nil || *b.ContentType == "" {
		return ""
	}
	ct := *b.ContentType
	if strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "charset") {
		ct += "; charset=utf-8"
	}
	return ct
}

// serveBlobFile streams the file (FileResponse: ETag, Last-Modified, Range).
func serveBlobFile(w http.ResponseWriter, r *http.Request, b blob) {
	f, err := os.Open(b.Path)
	if err != nil {
		writeDetail(w, 404, "Not Found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeDetail(w, 404, "Not Found")
		return
	}
	cc := "private, max-age=86400"
	if b.Immutable {
		cc = "private, max-age=31536000, immutable"
	}
	ct := b.mediaType()
	if ct == "" {
		ct = contentTypeFor(b.Path)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", cc)
	w.Header().Set("ETag", etagFor(st.ModTime(), st.Size()))
	if rh := r.Header.Get("Range"); rh != "" {
		if status, msg := starletteRangeProblem(rh, st.Size()); status != 0 {
			// Starlette's FileResponse answers these itself (plain text).
			w.Header().Del("ETag")
			w.Header().Del("Cache-Control")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if status == http.StatusRequestedRangeNotSatisfiable {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(st.Size(), 10))
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(msg)))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(msg))
			return
		}
	}
	http.ServeContent(w, r, b.Path, st.ModTime(), f)
}

// starletteRangeProblem mirrors the errors of Starlette's Range handling that
// net/http words differently: it returns (0, "") when the header is usable.
func starletteRangeProblem(h string, size int64) (int, string) {
	unit, spec, ok := strings.Cut(h, "=")
	if !ok {
		return http.StatusBadRequest, "Malformed range header."
	}
	if strings.ToLower(strings.TrimSpace(unit)) != "bytes" {
		return http.StatusBadRequest, "Only support bytes range"
	}
	unsatisfiable := false
	for _, part := range strings.Split(spec, ",") {
		a, z, ok := strings.Cut(strings.TrimSpace(part), "-")
		a, z = strings.TrimSpace(a), strings.TrimSpace(z)
		if !ok || (a == "" && z == "") {
			return http.StatusBadRequest, "Range header: range must be requested"
		}
		start, e1 := strconv.ParseInt(a, 10, 64)
		end, e2 := strconv.ParseInt(z, 10, 64)
		if (a != "" && e1 != nil) || (z != "" && e2 != nil) {
			return http.StatusBadRequest, "Range header: range must be requested"
		}
		if a != "" && z != "" && start > end {
			return http.StatusBadRequest, "Range header: start must be less than end"
		}
		if a != "" && start >= size {
			unsatisfiable = true
		}
	}
	if unsatisfiable {
		return http.StatusRequestedRangeNotSatisfiable, ""
	}
	return 0, ""
}

// writeBody sends a whole in-memory body; Content-Type only when known.
func writeBody(w http.ResponseWriter, ct string, data []byte, hdr ...string) {
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header()["Content-Type"] = nil // suppress sniffing
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		w.Header().Set(hdr[i], hdr[i+1])
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// dynOp lets a params func choose the operation too.
type dynOp struct {
	op     string
	params any
}

// blobRoute calls an op that yields a blob and hands it to send.
func (s *Server) blobRoute(op string, params func(c *call) any, send func(w http.ResponseWriter, r *http.Request, b blob)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &call{r: r}
		p := params(c)
		if !s.finish(w, c) {
			return
		}
		name := op
		if dyn, ok := p.(dynOp); ok {
			name, p = dyn.op, dyn.params
		}
		res, err := s.callOp(r, name, p)
		var b blob
		if err == nil {
			b, err = decodeBlob(res)
		}
		if err != nil {
			writeOpError(w, err)
			return
		}
		send(w, r, b)
	}
}

func readSmall(w http.ResponseWriter, b blob) ([]byte, bool) {
	data, err := os.ReadFile(b.Path)
	if err != nil {
		writeDetail(w, 404, "Not Found")
		return nil, false
	}
	return data, true
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	s.blobRoute(api.OpContactsAvatar, func(c *call) any {
		proto, ok := c.lastQuery("proto")
		if !ok {
			c.addErr("missing", "query", "proto", "Field required")
		}
		return obj{"protocol": proto, "contact_id": c.qStr("contact_id")}
	}, func(w http.ResponseWriter, r *http.Request, b blob) {
		if data, ok := readSmall(w, b); ok {
			writeBody(w, b.mediaType(), data, "Cache-Control", "private, max-age=86400")
		}
	})(w, r)
}

func (s *Server) handleLinkPreviewImage(w http.ResponseWriter, r *http.Request) {
	s.blobRoute(api.OpLinksPreviewImage, func(c *call) any {
		return obj{"url": c.qStr("url")}
	}, func(w http.ResponseWriter, r *http.Request, b blob) {
		if data, ok := readSmall(w, b); ok {
			writeBody(w, b.mediaType(), data,
				"Cache-Control", "private, max-age=86400", "X-Content-Type-Options", "nosniff")
		}
	})(w, r)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	s.blobRoute("", func(c *call) any {
		proto := protoParam(c)
		d := obj{"protocol": proto, "attachment_id": chi.URLParam(r, "*")}
		if wv := c.qIntOpt("w"); wv != nil {
			d["width"] = wv
			return dynOp{api.OpMediaThumbnail, d}
		}
		return dynOp{api.OpMediaAttachment, d}
	}, serveBlobFile)(w, r)
}

func (s *Server) handleQuoteMedia(w http.ResponseWriter, r *http.Request) {
	s.blobRoute(api.OpMediaQuote, func(c *call) any {
		proto := protoParam(c)
		id := c.pathInt("message_row_id", chi.URLParam(r, "message_row_id"))
		return obj{"protocol": proto, "message_row_id": id, "width": c.qIntOpt("w")}
	}, serveBlobFile)(w, r)
}

// consumeBlob reads a temporary blob and deletes it.
func consumeBlob(b blob) ([]byte, error) {
	defer os.Remove(b.Path)
	return os.ReadFile(b.Path)
}

func asciiFilename(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r == '"':
			sb.WriteByte('_')
		case r < utf8.RuneSelf:
			sb.WriteRune(r)
		default:
			sb.WriteByte('?')
		}
	}
	return sb.String()
}

// pyQuote is urllib.parse.quote(s, safe="").
func pyQuote(s string) string {
	const hex = "0123456789ABCDEF"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' || c == '~' {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('%')
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&15])
		}
	}
	return sb.String()
}

func (s *Server) handleGmailAttachment(w http.ResponseWriter, r *http.Request) {
	s.blobRoute(api.OpGmailAttachment, func(c *call) any {
		uid := c.pathInt("uid", chi.URLParam(r, "uid"))
		idx := c.pathInt("index", chi.URLParam(r, "index"))
		return obj{"uid": uid, "index": idx, "folder": c.qStrOpt("folder")}
	}, func(w http.ResponseWriter, r *http.Request, b blob) {
		data, err := consumeBlob(b)
		if err != nil {
			writeDetail(w, 404, "Not Found")
			return
		}
		disp := `attachment; filename="` + asciiFilename(b.Filename) + `"; filename*=UTF-8''` + pyQuote(b.Filename)
		writeBody(w, b.mediaType(), data, "Content-Disposition", disp, "X-Content-Type-Options", "nosniff")
	})(w, r)
}

func (s *Server) handleGmailImage(w http.ResponseWriter, r *http.Request) {
	c := &call{r: r}
	token := c.qStr("token")
	if !s.finish(w, c) {
		return
	}
	s.imageSem <- struct{}{}
	res, err := s.callOp(r, api.OpGmailImage, obj{"token": token})
	<-s.imageSem
	var b blob
	if err == nil {
		b, err = decodeBlob(res)
	}
	if err != nil {
		writeOpError(w, err)
		return
	}
	data, err := consumeBlob(b)
	if err != nil {
		writeDetail(w, 404, "Not Found")
		return
	}
	writeBody(w, b.mediaType(), data,
		"Cache-Control", "private, max-age=3600",
		"Content-Security-Policy", "default-src 'none'",
		"X-Content-Type-Options", "nosniff")
}
