package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

// effectiveHost is the browser-facing host; X-Forwarded-Host is trusted only
// for https origins behind an https proxy.
func effectiveHost(r *http.Request) string {
	fwd := r.Header.Get("X-Forwarded-Host")
	origin := r.Header.Get("Origin")
	proto := r.Header.Get("X-Forwarded-Proto")
	scheme, _, _ := strings.Cut(origin, ":")
	if fwd != "" && strings.EqualFold(scheme, "https") {
		first, _, _ := strings.Cut(proto, ",")
		if proto == "" || strings.EqualFold(strings.TrimSpace(first), "https") {
			h, _, _ := strings.Cut(fwd, ",")
			return strings.ToLower(strings.TrimSpace(h))
		}
	}
	return strings.ToLower(r.Host)
}

// originNetloc is urlsplit(origin).netloc.lower().
func originNetloc(origin string) string {
	_, rest, found := strings.Cut(origin, "//")
	if !found {
		return ""
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return strings.ToLower(rest)
}

func rawStr(v string) json.RawMessage { return mustRaw(v) }

// slotsFor builds the attachment handles after the storing rules of
// api._store_attachments. Spooled files stay in f.temps for cleanup.
func (s *Server) storeSendSlots(f *parsedForm) []api.AttachmentIn {
	out := make([]api.AttachmentIn, 0, len(f.slots))
	store := len(f.slots) <= maxSendFiles
	rejected := false
	var total int64
	too := "too_large"
	for _, sl := range f.slots {
		switch {
		case !sl.isFile || !store:
			out = append(out, api.AttachmentIn{})
		case rejected:
			out = append(out, api.AttachmentIn{Rejected: &too})
		default:
			if err := s.guardFile(sl, maxTotalBytes-total, true); err != nil {
				rejected = true
				out = append(out, api.AttachmentIn{Rejected: &too})
				continue
			}
			total += sl.size
			out = append(out, attachmentFor(sl, sl.filename))
		}
	}
	return out
}

func attachmentFor(sl formSlot, filename string) api.AttachmentIn {
	a := api.AttachmentIn{Path: sl.path}
	if filename != "" {
		a.Filename = &filename
	}
	if sl.contentType != "" {
		ct := sl.contentType
		a.ContentType = &ct
	}
	return a
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		oh := originNetloc(origin)
		if oh == "" || oh != effectiveHost(r) {
			writeJSON(w, 403, api.Forbidden403)
			return
		}
	}
	multipart := strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data")
	var payload map[string]json.RawMessage
	var form *parsedForm
	if multipart {
		if cl := r.Header.Get("Content-Length"); cl != "" {
			n, err := strconv.ParseInt(strings.TrimSpace(cl), 10, 64)
			if err != nil {
				writeDetail(w, 400, "Invalid request")
				return
			}
			if n > maxTotalBytes+1<<20 {
				writeDetail(w, 413, "Upload too large")
				return
			}
		}
		var err error
		form, err = s.parseForm(w, r, "file", maxSendFiles+10, maxTotalBytes+1<<20)
		if err != nil {
			if err == errTooManyFiles {
				writeDetail(w, 400, "Too many attachments")
			} else {
				writeDetail(w, 400, "Invalid request")
			}
			return
		}
		defer form.cleanup()
		payload = make(map[string]json.RawMessage, len(form.fields))
		for k, v := range form.fields {
			payload[k] = rawStr(v)
		}
	} else {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		var m map[string]json.RawMessage
		if err != nil || json.Unmarshal(raw, &m) != nil || m == nil {
			writeDetail(w, 400, "Invalid request")
			return
		}
		payload = m
	}
	// Local-frontend knobs are never settable from the web.
	delete(payload, "optimistic")
	delete(payload, "retry")

	if multipart {
		if raw, ok := payload["quote_timestamp"]; ok {
			var str string
			_ = json.Unmarshal(raw, &str)
			if str == "" {
				payload["quote_timestamp"] = json.RawMessage("null")
			} else if n, ok := parsePyInt(str); ok {
				payload["quote_timestamp"] = mustRaw(n)
			}
		}
	}
	with := func(extra map[string]any) map[string]any {
		m := make(map[string]any, len(payload)+len(extra))
		for k, v := range payload {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	if form != nil && len(form.slots) > 0 {
		slotsOK := true
		for _, sl := range form.slots {
			slotsOK = slotsOK && sl.isFile
		}
		if _, err := s.callOp(r, api.OpMessagesSendCheck, with(map[string]any{
			"attachments": []any{}, "attachment_count": len(form.slots), "slots_ok": slotsOK,
		})); err != nil {
			writeOpError(w, err)
			return
		}
	}
	attachments := []api.AttachmentIn{}
	if form != nil {
		attachments = s.storeSendSlots(form)
	}
	res, err := s.callOp(r, api.OpMessagesSend, with(map[string]any{"attachments": attachments}))
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---- Gmail -----------------------------------------------------------------

func (s *Server) gmailRoutes(r chi.Router) {
	r.Get("/api/gmail/status", s.jsonRoute(api.OpGmailStatus, nil))
	r.Get("/api/gmail/unread", s.jsonRoute(api.OpGmailUnread, nil))
	r.Get("/api/gmail/address-book", s.jsonRoute(api.OpGmailAddressBook, nil))
	r.Get("/api/gmail/messages", s.jsonRoute(api.OpGmailMessagesList, func(c *call) any {
		return obj{
			"folder": c.qStrOpt("folder"), "offset": c.qInt("offset", 0),
			"limit": c.qInt("limit", 50), "refresh": c.qBool("refresh", false),
		}
	}))
	r.Post("/api/gmail/messages/bulk-delete", s.handleBulkDelete)
	uidOp := func(op string) http.HandlerFunc {
		return s.jsonRoute(op, func(c *call) any {
			uid := c.pathInt("uid", chi.URLParam(c.r, "uid"))
			return obj{"uid": uid, "folder": c.qStrOpt("folder")}
		})
	}
	r.Delete("/api/gmail/messages/{uid}", uidOp(api.OpGmailMessagesDelete))
	r.Get("/api/gmail/messages/{uid}", uidOp(api.OpGmailMessagesGet))
	r.Get("/api/gmail/messages/{uid}/attachments/{index}", s.handleGmailAttachment)
	r.Post("/api/gmail/messages/{uid}/read", uidOp(api.OpGmailMessagesMarkRead))
	r.Post("/api/gmail/messages/{uid}/unread", uidOp(api.OpGmailMessagesMarkUnread))
	r.Post("/api/gmail/messages/{uid}/archive", uidOp(api.OpGmailMessagesArchive))
	r.Post("/api/gmail/send", s.handleGmailSend)
	r.Get(api.RouteGmailImage, s.handleGmailImage)
}

func (s *Server) handleBulkDelete(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	var m map[string]json.RawMessage
	if err != nil || json.Unmarshal(bytes.TrimSpace(raw), &m) != nil || m == nil {
		writeDetail(w, 400, "Richiesta non valida")
		return
	}
	res, err := s.callOp(r, api.OpGmailMessagesBulkDelete, obj{"uids": get(m, "uids"), "folder": get(m, "folder")})
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleGmailSend(w http.ResponseWriter, r *http.Request) {
	var form *parsedForm
	if ct := strings.ToLower(r.Header.Get("Content-Type")); !strings.HasPrefix(ct, "multipart/form-data") {
		// Starlette's request.form() answers an empty form for any other
		// content type (and parses urlencoded bodies).
		form = &parsedForm{fields: map[string]string{}}
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") && r.ParseForm() == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					form.fields[k] = v[len(v)-1]
				}
			}
		}
	} else {
		var err error
		form, err = s.parseForm(w, r, "file[]", gmailMaxFiles+1, 11*gmailMaxFile)
		if err != nil {
			writeDetail(w, 400, "Richiesta non valida")
			return
		}
	}
	defer form.cleanup()
	field := func(n string) string { return form.fields[n] }
	optUID := func(name string) (any, bool) {
		raw := field(name)
		if raw == "" {
			return nil, true
		}
		n, ok := parsePyInt(raw)
		if !ok {
			writeDetail(w, 400, name+" non valido")
			return nil, false
		}
		return n, true
	}
	orNil := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	reply, ok := optUID("reply_to_uid")
	if !ok {
		return
	}
	fwd, ok := optUID("forward_uid")
	if !ok {
		return
	}
	payload := obj{
		"to": field("to"), "cc": field("cc"), "bcc": field("bcc"),
		"subject": field("subject"), "text": field("text"),
		"reply_to_uid": reply, "reply_to_folder": orNil(field("reply_to_folder")),
		"forward_uid": fwd, "forward_folder": orNil(field("forward_folder")),
	}
	with := func(extra obj) obj {
		m := obj{}
		for k, v := range payload {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	slots := []api.AttachmentIn{}
	if len(form.slots) > 0 {
		slotsOK := true
		for _, sl := range form.slots {
			slotsOK = slotsOK && sl.isFile
		}
		if _, err := s.callOp(r, api.OpGmailMessagesSendCheck, with(obj{
			"attachments": []any{}, "attachment_count": len(form.slots), "slots_ok": slotsOK,
		})); err != nil {
			writeOpError(w, err)
			return
		}
		var total int64
		too := "too_large"
		for _, sl := range form.slots {
			if !sl.isFile {
				slots = append(slots, api.AttachmentIn{})
				continue
			}
			if err := s.guardFile(sl, gmailMaxFile, false); err != nil {
				slots = append(slots, api.AttachmentIn{Rejected: &too})
				break
			}
			name := sl.filename
			if name == "" {
				name = "attachment"
			}
			slots = append(slots, attachmentFor(sl, name))
			total += sl.size
			if total > gmailMaxTotal {
				break
			}
		}
	}
	res, err := s.callOp(r, api.OpGmailMessagesSend, with(obj{"attachments": slots}))
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, 200, res)
}
