package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

func TestAuth(t *testing.T) {
	e := newEnv(t)
	for _, h := range []string{"", "Bearer", "Bearer ", "Bearer  tok-123", "Basic tok-123", "Bearer nope", "tok-123"} {
		resp, body := e.do(t, "GET", "/api/status", "", "Authorization", h)
		if resp.StatusCode != 401 || body != `{"detail":"Unauthorized"}` || resp.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("header %q: %d %s", h, resp.StatusCode, body)
		}
	}
	// unknown /api path: auth first, then 404
	if resp, _ := e.do(t, "GET", "/api/nope", "", "Authorization", ""); resp.StatusCode != 401 {
		t.Error(resp.StatusCode)
	}
	if resp, body := e.do(t, "GET", "/api/nope", ""); resp.StatusCode != 404 || body != `{"detail":"Not Found"}` {
		t.Error(resp.StatusCode, body)
	}
	if resp, _ := e.do(t, "POST", "/api/status", ""); resp.StatusCode != 405 {
		t.Error(resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/api/status", "", "Authorization", "bEaReR "+testToken); resp.StatusCode != 200 {
		t.Error("scheme must be case-insensitive")
	}
	// health: no auth
	resp, body := e.do(t, "GET", "/health", "", "Authorization", "")
	var h api.HealthResponse
	_ = json.Unmarshal([]byte(body), &h)
	if resp.StatusCode != 200 || h.Status != "ok" || !h.AuthRequired {
		t.Error(body)
	}
}

func TestNoAuth(t *testing.T) {
	e := newEnv(t, WithNoAuth())
	if resp, _ := e.do(t, "GET", "/api/status", "", "Authorization", ""); resp.StatusCode != 200 {
		t.Error(resp.StatusCode)
	}
	_, body := e.do(t, "GET", "/health", "")
	if !strings.Contains(body, `"auth_required":false`) {
		t.Error(body)
	}
}

func TestStatic(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/index.html"} {
		resp, body := e.do(t, "GET", p, "", "Authorization", "")
		if resp.StatusCode != 200 || body != "<html>spa</html>" || resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q %v", p, resp.StatusCode, body, resp.Header)
		}
	}
	resp, body := e.do(t, "GET", "/app.js", "", "Authorization", "")
	if resp.StatusCode != 200 || body != "console.log(1)" || resp.Header.Get("Cache-Control") != "" ||
		resp.Header.Get("ETag") == "" || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Errorf("app.js: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "GET", "/missing.css", "", "Authorization", ""); resp.StatusCode != 404 {
		t.Error(resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/../etc/passwd", "", "Authorization", ""); resp.StatusCode == 200 {
		t.Error("traversal")
	}
}

func TestSendJSONStripsKnobsAndChecksOrigin(t *testing.T) {
	e := newEnv(t)
	body := `{"protocol":"signal","contact_id":"+1","text":"hi","client_msg_id":"c","optimistic":{"timestamp":1},"retry":false}`
	resp, out := e.do(t, "POST", "/api/send", body)
	if resp.StatusCode != 200 || out != `{"ok":true}` {
		t.Fatal(resp.StatusCode, out)
	}
	c := e.d.last()
	if c.Name != "messages.send" {
		t.Fatal(c.Name)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(c.Params, &m)
	if _, ok := m["optimistic"]; ok {
		t.Error("optimistic not stripped")
	}
	if _, ok := m["retry"]; ok {
		t.Error("retry not stripped")
	}
	if string(m["attachments"]) != "[]" || string(m["text"]) != `"hi"` {
		t.Errorf("params %s", c.Params)
	}

	host := strings.TrimPrefix(e.ts.URL, "http://")
	cases := []struct {
		hdr  []string
		want int
	}{
		{nil, 200},
		{[]string{"Origin", "http://" + host}, 200},
		{[]string{"Origin", "http://" + strings.ToUpper(host)}, 200},
		{[]string{"Origin", "http://evil.example"}, 403},
		{[]string{"Origin", "null"}, 403},
		// X-Forwarded-Host is trusted only for an https origin
		{[]string{"Origin", "http://fwd.example", "X-Forwarded-Host", "fwd.example"}, 403},
		{[]string{"Origin", "https://fwd.example", "X-Forwarded-Host", "Fwd.Example, other"}, 200},
		{[]string{"Origin", "https://fwd.example", "X-Forwarded-Host", "fwd.example", "X-Forwarded-Proto", "http"}, 403},
		{[]string{"Origin", "https://fwd.example", "X-Forwarded-Host", "fwd.example", "X-Forwarded-Proto", "https,http"}, 200},
	}
	for _, c := range cases {
		resp, out := e.do(t, "POST", "/api/send", body, c.hdr...)
		if resp.StatusCode != c.want {
			t.Errorf("%v: %d %s", c.hdr, resp.StatusCode, out)
		}
		if c.want == 403 && out != `{"detail":"Forbidden"}` {
			t.Error(out)
		}
	}
	for _, bad := range []string{`[1]`, `nope`, `null`, ``} {
		resp, out := e.do(t, "POST", "/api/send", bad)
		if resp.StatusCode != 400 || out != `{"detail":"Invalid request"}` {
			t.Errorf("%q: %d %s", bad, resp.StatusCode, out)
		}
	}
}

type mpFile struct{ field, name, ctype, data string }

func buildMultipart(fields map[string]string, files []mpFile) (string, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, f.field, f.name))
		if f.ctype != "" {
			h.Set("Content-Type", f.ctype)
		}
		pw, _ := w.CreatePart(h)
		_, _ = pw.Write([]byte(f.data))
	}
	_ = w.Close()
	return w.FormDataContentType(), buf.String()
}

func TestSendMultipart(t *testing.T) {
	e := newEnv(t)
	dir := e.srv.uploadDir
	var seen []string
	var existed bool
	e.d.results["messages.send"] = map[string]any{"ok": true}
	ct, body := buildMultipart(map[string]string{
		"protocol": "signal", "contact_id": "+1", "text": "", "quote_timestamp": "123",
		"optimistic": "x", "retry": "false",
	}, []mpFile{{"file", "a.png", "image/png", "PNGDATA"}, {"file", "b.bin", "", "BB"}})
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	if got := e.d.names(); len(got) != 2 || got[0] != "messages.send.check" || got[1] != "messages.send" {
		t.Fatalf("ops %v", got)
	}
	var chk map[string]json.RawMessage
	_ = json.Unmarshal(e.d.calls[0].Params, &chk)
	if string(chk["attachment_count"]) != "2" || string(chk["slots_ok"]) != "true" || string(chk["attachments"]) != "[]" {
		t.Errorf("check %s", e.d.calls[0].Params)
	}
	var send struct {
		QuoteTimestamp json.RawMessage `json:"quote_timestamp"`
		Optimistic     json.RawMessage `json:"optimistic"`
		Attachments    []api.AttachmentIn
	}
	_ = json.Unmarshal(e.d.calls[1].Params, &send)
	if string(send.QuoteTimestamp) != "123" || send.Optimistic != nil || len(send.Attachments) != 2 {
		t.Fatalf("send %s", e.d.calls[1].Params)
	}
	a := send.Attachments[0]
	if a.Filename == nil || *a.Filename != "a.png" || a.ContentType == nil || *a.ContentType != "image/png" ||
		filepath.Dir(a.Path) != dir || !strings.HasSuffix(a.Path, ".png") || !strings.HasPrefix(filepath.Base(a.Path), "upload-") {
		t.Errorf("slot0 %+v", a)
	}
	if send.Attachments[1].ContentType != nil || strings.HasSuffix(send.Attachments[1].Path, ".bin") {
		t.Errorf("slot1 %+v", send.Attachments[1])
	}
	_, _, _ = seen, existed, os.Getpid
	// temp files removed afterwards
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("leftover temp files: %v", ents)
	}
}

func TestSendMultipartEdgeCases(t *testing.T) {
	e := newEnv(t)
	post := func(fields map[string]string, files []mpFile) (*http.Response, string) {
		ct, body := buildMultipart(fields, files)
		return e.do(t, "POST", "/api/send", body, "Content-Type", ct)
	}
	// quote_timestamp: "" -> null, junk passes through
	post(map[string]string{"protocol": "signal", "quote_timestamp": ""}, nil)
	jsonEq(t, e.d.last().Params, `{"protocol":"signal","quote_timestamp":null,"attachments":[]}`)
	post(map[string]string{"quote_timestamp": "abc"}, nil)
	jsonEq(t, e.d.last().Params, `{"quote_timestamp":"abc","attachments":[]}`)

	// more than 20 file parts: Too many attachments
	var files []mpFile
	for i := 0; i < 21; i++ {
		files = append(files, mpFile{"file", "a.png", "", "x"})
	}
	n := len(e.d.calls)
	resp, out := post(nil, files)
	if resp.StatusCode != 400 || out != `{"detail":"Too many attachments"}` || len(e.d.calls) != n {
		t.Errorf("%d %s", resp.StatusCode, out)
	}
	// 11..20 files: stored none, op decides (empty slots)
	files = files[:12]
	post(nil, files)
	var sent struct{ Attachments []api.AttachmentIn }
	_ = json.Unmarshal(e.d.last().Params, &sent)
	if len(sent.Attachments) != 12 || sent.Attachments[0].Path != "" {
		t.Errorf("expected 12 empty slots: %s", e.d.last().Params)
	}
	// non-file "file" field: slots_ok false, empty slot
	post(map[string]string{"file": "oops"}, nil)
	var chk map[string]json.RawMessage
	_ = json.Unmarshal(e.d.calls[len(e.d.calls)-2].Params, &chk)
	if e.d.calls[len(e.d.calls)-2].Name != "messages.send.check" || string(chk["slots_ok"]) != "false" {
		t.Errorf("%s", e.d.calls[len(e.d.calls)-2].Params)
	}
	// too many fields
	fields := map[string]string{}
	for i := 0; i < 21; i++ {
		fields[fmt.Sprintf("f%d", i)] = "v"
	}
	if resp, out := post(fields, nil); resp.StatusCode != 400 || out != `{"detail":"Invalid request"}` {
		t.Errorf("%d %s", resp.StatusCode, out)
	}
	// declared Content-Length too large
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/send", strings.NewReader("x"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.ContentLength = 300 << 20
	req.Body = http.NoBody
	// (a real body that large is not sent; the check precedes parsing)
	r2, err := http.DefaultClient.Do(req)
	if err == nil {
		r2.Body.Close()
	}
	// check failure short-circuits before send
	e.d.errs["messages.send.check"] = &ops.Error{Code: api.CodeInvalidRequest, Message: "bad"}
	n = len(e.d.calls)
	resp, _ = post(nil, []mpFile{{"file", "a.png", "", "x"}})
	if resp.StatusCode != 400 || len(e.d.calls) != n+1 {
		t.Errorf("check err: %d, calls %d", resp.StatusCode, len(e.d.calls)-n)
	}
}

func TestSendRejectsBeyondBudget(t *testing.T) {
	e := newEnv(t, WithUploadGuard(capGuard{}))
	ct, body := buildMultipart(nil, []mpFile{{"file", "a.png", "", "ok"}, {"file", "b.png", "", "TOOBIG"}, {"file", "c.png", "", "ok"}})
	e.do(t, "POST", "/api/send", body, "Content-Type", ct)
	var sent struct{ Attachments []api.AttachmentIn }
	_ = json.Unmarshal(e.d.last().Params, &sent)
	if len(sent.Attachments) != 3 || sent.Attachments[0].Path == "" ||
		sent.Attachments[1].Rejected == nil || sent.Attachments[2].Rejected == nil ||
		sent.Attachments[1].Path != "" {
		t.Errorf("%s", e.d.last().Params)
	}
}

type capGuard struct{}

func (capGuard) NewGuard(string, string, int64, bool) func([]byte) error {
	return func(c []byte) error {
		if bytes.Contains(c, []byte("TOOBIG")) {
			return fmt.Errorf("too large")
		}
		return nil
	}
}

func TestGmailSend(t *testing.T) {
	e := newEnv(t)
	ct, body := buildMultipart(map[string]string{
		"to": "a@b.c", "subject": "s", "text": "t", "reply_to_uid": "7", "forward_folder": "",
	}, []mpFile{{"file[]", "r.pdf", "application/pdf", "PDF"}, {"file[]", "", "", "Z"}})
	resp, out := e.do(t, "POST", "/api/gmail/send", body, "Content-Type", ct)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, out)
	}
	if got := e.d.names(); len(got) != 2 || got[0] != "gmail.messages.send.check" || got[1] != "gmail.messages.send" {
		t.Fatal(got)
	}
	var s struct {
		To, CC, Subject string
		ReplyToUID      *int    `json:"reply_to_uid"`
		ForwardUID      *int    `json:"forward_uid"`
		ForwardFolder   *string `json:"forward_folder"`
		Attachments     []api.AttachmentIn
	}
	_ = json.Unmarshal(e.d.calls[1].Params, &s)
	if s.To != "a@b.c" || s.ReplyToUID == nil || *s.ReplyToUID != 7 || s.ForwardUID != nil || s.ForwardFolder != nil ||
		len(s.Attachments) != 2 || *s.Attachments[1].Filename != "attachment" {
		t.Errorf("%s", e.d.calls[1].Params)
	}
	// invalid uid
	ct, body = buildMultipart(map[string]string{"forward_uid": "x"}, nil)
	resp, out = e.do(t, "POST", "/api/gmail/send", body, "Content-Type", ct)
	if resp.StatusCode != 400 || out != `{"detail":"forward_uid non valido"}` {
		t.Error(resp.StatusCode, out)
	}
	// not multipart
	resp, out = e.do(t, "POST", "/api/gmail/send", `{}`)
	if resp.StatusCode != 400 || out != `{"detail":"Richiesta non valida"}` {
		t.Error(resp.StatusCode, out)
	}
}

func TestGmailBulkDeleteMalformed(t *testing.T) {
	e := newEnv(t)
	for _, b := range []string{`nope`, `[1]`, `null`, ``} {
		resp, out := e.do(t, "POST", "/api/gmail/messages/bulk-delete", b)
		if resp.StatusCode != 400 || out != `{"detail":"Richiesta non valida"}` {
			t.Errorf("%q: %d %s", b, resp.StatusCode, out)
		}
	}
}

func writeTemp(t *testing.T, name, data string) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMediaStreamsWithRangeAndCache(t *testing.T) {
	e := newEnv(t)
	p := writeTemp(t, "v.mp4", "0123456789")
	e.d.results["media.attachment"] = map[string]any{"path": p, "content_type": "video/mp4", "immutable": true}
	e.d.results["media.thumbnail"] = map[string]any{"path": p, "content_type": "image/jpeg", "immutable": false}
	resp, body := e.do(t, "GET", "/api/media/signal/ab/cd?v=1", "")
	if resp.StatusCode != 200 || body != "0123456789" || resp.Header.Get("Content-Type") != "video/mp4" ||
		resp.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" ||
		resp.Header.Get("Accept-Ranges") != "bytes" || resp.Header.Get("ETag") == "" || resp.Header.Get("Last-Modified") == "" {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	jsonEq(t, e.d.last().Params, `{"protocol":"signal","attachment_id":"ab/cd"}`)
	resp, body = e.do(t, "GET", "/api/media/signal/x", "", "Range", "bytes=2-4")
	if resp.StatusCode != 206 || body != "234" {
		t.Error(resp.StatusCode, body)
	}
	resp, _ = e.do(t, "GET", "/api/media/whatsapp/x?w=240", "")
	if e.d.last().Name != "media.thumbnail" || resp.Header.Get("Cache-Control") != "private, max-age=86400" {
		t.Error(e.d.last().Name, resp.Header)
	}
	jsonEq(t, e.d.last().Params, `{"protocol":"whatsapp","attachment_id":"x","width":240}`)

	e.d.results["media.quote"] = map[string]any{"path": p, "content_type": "image/png", "immutable": false}
	e.do(t, "GET", "/api/quote-media/signal/42?w=96", "")
	jsonEq(t, e.d.last().Params, `{"protocol":"signal","message_row_id":42,"width":96}`)
	e.do(t, "GET", "/api/quote-media/signal/42", "")
	jsonEq(t, e.d.last().Params, `{"protocol":"signal","message_row_id":42,"width":null}`)
}

func TestSmallBlobRoutes(t *testing.T) {
	e := newEnv(t)
	p := writeTemp(t, "a.png", "IMG")
	e.d.results["contacts.avatar"] = map[string]any{"path": p, "content_type": "image/png"}
	e.d.results["links.preview_image"] = map[string]any{"path": p, "content_type": "image/webp"}
	resp, body := e.do(t, "GET", "/api/contact-avatar?proto=signal&contact_id=%2B1", "")
	if body != "IMG" || resp.Header.Get("Cache-Control") != "private, max-age=86400" || resp.Header.Get("ETag") != "" {
		t.Error(body, resp.Header)
	}
	jsonEq(t, e.d.last().Params, `{"protocol":"signal","contact_id":"+1"}`)
	resp, body = e.do(t, "GET", "/api/link-preview/image?url=u", "")
	if body != "IMG" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Type") != "image/webp" {
		t.Error(body, resp.Header)
	}
}

func TestGmailAttachmentAndImage(t *testing.T) {
	e := newEnv(t)
	p := writeTemp(t, "blob", "DATA")
	e.d.results["gmail.attachment"] = map[string]any{"path": p, "content_type": "application/pdf", "filename": `rè "x".pdf`}
	resp, body := e.do(t, "GET", "/api/gmail/messages/5/attachments/1?folder=F", "")
	want := `attachment; filename="r? _x_.pdf"; filename*=UTF-8''r%C3%A8%20%22x%22.pdf`
	if body != "DATA" || resp.Header.Get("Content-Disposition") != want ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Length") != "4" {
		t.Fatal(body, resp.Header)
	}
	jsonEq(t, e.d.last().Params, `{"uid":5,"index":1,"folder":"F"}`)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("temp blob not deleted")
	}

	p = writeTemp(t, "img", "PX")
	e.d.results["gmail.image"] = map[string]any{"path": p, "content_type": "image/gif"}
	resp, body = e.do(t, "GET", "/gmail-image?token=abc", "", "Authorization", "")
	if resp.StatusCode != 200 || body != "PX" || resp.Header.Get("Content-Security-Policy") != "default-src 'none'" ||
		resp.Header.Get("Cache-Control") != "private, max-age=3600" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	jsonEq(t, e.d.last().Params, `{"token":"abc"}`)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("temp image not deleted")
	}
}
