package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// scripted overrides SendMessage with a list of errors consumed in order
// (nil = succeed through the fake).
type scripted struct {
	*fake.Backend
	script []error
	calls  counter
}

func (s *scripted) SendMessage(ctx context.Context, contactID, text string, q proto.Quote) (string, error) {
	i := s.calls.inc() - 1
	if i < len(s.script) && s.script[i] != nil {
		return "", s.script[i]
	}
	return s.Backend.SendMessage(ctx, contactID, text, q)
}

func sendEnv(t *testing.T, protocol string) *env {
	e := newEnv(t, protocol)
	e.fake(protocol).SetContacts(contact(protocol, "c1", "Chat One", nil))
	return e
}

func TestSendTextSuccessDuplicateConflict(t *testing.T) {
	e := sendEnv(t, "signal")
	body := `{"protocol":"signal","contact_id":"c1","text":"hello","client_msg_id":" abc "}`
	wantSent(t, 1, e.mustCall("messages.send", body))
	pushes := e.webPushes()
	if len(pushes) != 1 {
		t.Fatalf("web pushes: %v", pushes)
	}
	wantJSON(t, pushes[0], `{"event":{"type":"message","payload":{"protocol":"signal","contact_id":"c1","is_mine":true}}}`)
	if n := len(e.fake("signal").CallsTo("SendMessage")); n != 1 {
		t.Fatalf("backend sends: %d", n)
	}
	// The registry key is the stripped client_msg_id.
	if _, ok := e.core.SendRegistry().Snapshot(SendKey{"signal", "c1", "abc"}); !ok {
		t.Fatal("registry entry missing")
	}
	// Replay with the same fingerprint: duplicate, nothing is sent again.
	wantJSON(t, e.mustCall("messages.send", body), `{"ok":true,"duplicate":true}`)
	if n := len(e.fake("signal").CallsTo("SendMessage")); n != 1 {
		t.Fatalf("duplicate must not resend, sends=%d", n)
	}
	if len(e.webPushes()) != 0 {
		t.Fatal("duplicate publishes nothing")
	}
	// Same id, different text: conflict.
	oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"other","client_msg_id":"abc"}`)
	if oe.Code != "unprocessable" || oe.Message != "Client message id conflict" {
		t.Fatalf("%+v", oe)
	}
}

func TestSendInflightConflict(t *testing.T) {
	e := sendEnv(t, "signal")
	// Pre-claim the key as an in-flight send of the same payload.
	fp := SendFingerprint{Protocol: "signal", ContactID: "c1", Text: "x"}.Build()
	e.core.SendRegistry().ClaimOrLookup(SendKey{"signal", "c1", "k"}, fp)
	oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"x","client_msg_id":"k"}`)
	if oe.Code != "conflict" || oe.Message != "Send in progress" {
		t.Fatalf("%+v", oe)
	}
}

func TestSendRetryThenSuccess(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "c1", "C", nil))
	sc := &scripted{Backend: f, script: []error{syscall.ECONNREFUSED, syscall.ECONNREFUSED}}
	_ = e.mgr.Register(sc)
	wantSent(t, 1, e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"hi","client_msg_id":"r1"}`))
	if sc.calls.n != 3 {
		t.Fatalf("attempts: %d", sc.calls.n)
	}
	pushes := e.webPushes()
	if len(pushes) != 3 {
		t.Fatalf("pushes: %v", pushes)
	}
	for i, attempt := range []string{"2", "3"} {
		var p struct {
			Event struct {
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			} `json:"event"`
		}
		_ = json.Unmarshal([]byte(pushes[i]), &p)
		pl := p.Event.Payload
		if p.Event.Type != "send_retry" || pl["client_msg_id"] != "r1" || pl["protocol"] != "signal" ||
			pl["contact_id"] != "c1" || json.Number(attempt).String() == "" ||
			int(pl["attempt"].(float64)) != i+2 || pl["max_attempts"].(float64) != 3 ||
			!strings.Contains(pl["error"].(string), "refused") {
			t.Fatalf("send_retry frame %d: %s", i, pushes[i])
		}
	}
	wantJSON(t, pushes[2], `{"event":{"type":"message","payload":{"protocol":"signal","contact_id":"c1","is_mine":true}}}`)
}

func TestSendFailureReleasesClaim(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "c1", "C", nil))
	sc := &scripted{Backend: f, script: []error{syscall.ECONNREFUSED, syscall.ECONNREFUSED, syscall.ECONNREFUSED}}
	_ = e.mgr.Register(sc)
	body := `{"protocol":"signal","contact_id":"c1","text":"hi","client_msg_id":"r2"}`
	oe := e.callErr("messages.send", body)
	if oe.Code != "backend_error" || oe.Message != "Message send failed" || sc.calls.n != 3 {
		t.Fatalf("%+v attempts=%d", oe, sc.calls.n)
	}
	if _, ok := e.core.SendRegistry().Snapshot(SendKey{"signal", "c1", "r2"}); ok {
		t.Fatal("failed send must release the claim")
	}
	// The same client_msg_id can be retried and now succeeds.
	wantSent(t, 1, e.mustCall("messages.send", body))

	// retry=false: a single attempt even for a retryable error.
	sc.calls.n = 0
	sc.script = []error{syscall.ECONNREFUSED}
	if oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"again","retry":false}`); oe.Code != "backend_error" || sc.calls.n != 1 {
		t.Fatalf("%+v attempts=%d", oe, sc.calls.n)
	}
	// Terminal error: no retry.
	sc.calls.n = 0
	sc.script = []error{errors.New("bad number")}
	if oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"again2"}`); oe.Code != "backend_error" || sc.calls.n != 1 {
		t.Fatalf("%+v attempts=%d", oe, sc.calls.n)
	}
}

func TestSendValidation(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp", "telegram")
	for _, p := range []string{"signal", "whatsapp", "telegram"} {
		e.fake(p).SetContacts(contact(p, "c1", "C", nil))
	}
	long := strings.Repeat("a", 65537)
	invalid := []string{
		`{}`,
		`{"protocol":"email","contact_id":"c1","text":"x"}`,
		`{"protocol":"signal","contact_id":"  ","text":"x"}`,
		`{"protocol":"signal","contact_id":"c1"}`,
		`{"protocol":"signal","contact_id":"c1","text":"   "}`,
		`{"protocol":"signal","contact_id":"c1","text":"` + long + `"}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_timestamp":true}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_timestamp":1000.0}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_timestamp":"12"}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_author":5}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_message":["a"]}`,
		`{"protocol":"telegram","contact_id":"c1","text":"x","quote_message":"q"}`,
		`{"protocol":"telegram","contact_id":"c1","text":"x","quote_message":"q","reply_to_message_id":"0"}`,
		`{"protocol":"telegram","contact_id":"c1","text":"x","quote_message":"q","reply_to_message_id":"abc"}`,
		`{"protocol":"whatsapp","contact_id":"c1","text":"x","quote_message":"q"}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_content_type":"image/png"}`,
		`{"protocol":"signal","contact_id":"c1","text":"x","quote_attachment_id":"nope"}`,
	}
	for _, body := range invalid {
		before := len(e.fake("signal").Calls())
		oe := e.callErr("messages.send", body)
		if oe.Code != "invalid_request" || oe.Message != "Invalid request" {
			t.Errorf("%.80s: %+v", body, oe)
		}
		_ = before
	}
	for _, p := range []string{"signal", "whatsapp", "telegram"} {
		if n := len(e.fake(p).CallsTo("SendMessage")); n != 0 {
			t.Fatalf("%s: nothing may be sent on validation errors, got %d", p, n)
		}
	}
	// Strict types: a number where a string is required.
	if oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":5,"text":"x"}`); oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}
	// Unknown backend / contact -> Not Found.
	e2 := newEnv(t, "signal")
	if oe := e2.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"x"}`); oe.Code != "not_found" || oe.Message != "Not Found" {
		t.Fatalf("%+v", oe)
	}
	if oe := e2.callErr("messages.send", `{"protocol":"signal","contact_id":"nobody","text":"x"}`); oe.Code != "not_found" || oe.Message != "Not Found" {
		t.Fatalf("%+v", oe)
	}
}

func TestSendQuotesPerProtocol(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp", "telegram")
	for _, p := range []string{"signal", "whatsapp", "telegram"} {
		e.fake(p).SetContacts(contact(p, "c1", "C", nil))
	}
	quoteOf := func(p string) proto.Quote {
		calls := e.fake(p).CallsTo("SendMessage")
		return calls[len(calls)-1].Args["quote"].(proto.Quote)
	}
	e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"a","quote_timestamp":1700,"quote_author":" +39 ","quote_message":" orig ","reply_to_message_id":"zz"}`)
	if q := quoteOf("signal"); q.Timestamp != 1700 || q.Author != "+39" || q.Message != "orig" || q.ReplyToMessageID != "" {
		t.Fatalf("signal ignores reply_to_message_id and strips: %+v", q)
	}
	e.mustCall("messages.send", `{"protocol":"whatsapp","contact_id":"c1","text":"a","quote_timestamp":1700,"quote_message":"orig","reply_to_message_id":" wa-1 "}`)
	if q := quoteOf("whatsapp"); q.ReplyToMessageID != "wa-1" || q.Message != "orig" {
		t.Fatalf("whatsapp: %+v", q)
	}
	e.mustCall("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"a","quote_message":"orig","reply_to_message_id":"77"}`)
	if q := quoteOf("telegram"); q.ReplyToMessageID != "77" {
		t.Fatalf("telegram: %+v", q)
	}
	// A blank quote_message becomes none; quote_content_type/_attachment_id
	// are ignored outside Signal.
	e.mustCall("messages.send", `{"protocol":"whatsapp","contact_id":"c1","text":"b","quote_message":"   ","quote_content_type":"image/png"}`)
	if q := quoteOf("whatsapp"); !q.IsZero() {
		t.Fatalf("whatsapp should have no quote: %+v", q)
	}
}

func TestSendSignalMediaQuote(t *testing.T) {
	e := sendEnv(t, "signal")
	f := e.fake("signal")
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "c1", Text: "", Sender: "C",
		Timestamp: 1000, MsgType: "image", AttachmentID: store.Ptr("att1"), ContentType: store.Ptr("image/png"), MsgID: store.Ptr("m1")})
	f.AddAttachment("att1", "/data/att/att1.png", nil)
	e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"nice","quote_timestamp":1000,"quote_author":"c1","quote_message":"","quote_content_type":"image/png","quote_attachment_id":"att1"}`)
	calls := f.CallsTo("SendMessage")
	q := calls[len(calls)-1].Args["quote"].(proto.Quote)
	if len(q.Attachments) != 1 || q.Attachments[0] != "image/png:att1.png:/data/att/att1.png" || q.Message != "" {
		t.Fatalf("quote attachments: %+v", q)
	}
	// Unknown attachment id of this chat -> invalid; content type without id -> invalid.
	if oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"x","quote_attachment_id":"other"}`); oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}
}

func TestSendCheck(t *testing.T) {
	e := sendEnv(t, "signal")
	wantJSON(t, e.mustCall("messages.send.check", `{"protocol":"signal","contact_id":"c1","text":"","attachment_count":2,"slots_ok":true}`), `{"ok":true}`)
	cases := map[string]struct{ code, msg string }{
		`{"protocol":"signal","contact_id":"c1","text":""}`:                                                    {"invalid_request", "Invalid request"},
		`{"protocol":"signal","contact_id":"c1","text":"x","attachment_count":11}`:                             {"invalid_request", "Too many attachments"},
		`{"protocol":"signal","contact_id":"c1","text":"x","attachment_count":1,"slots_ok":false}`:             {"invalid_request", "Invalid request"},
		`{"protocol":"signal","contact_id":"c1","text":"x","attachment_count":1,"optimistic":{"timestamp":5}}`: {"invalid_request", "Invalid request"},
		`{"protocol":"signal","contact_id":"zz","text":"x"}`:                                                   {"not_found", "Not Found"},
	}
	for body, want := range cases {
		oe := e.callErr("messages.send.check", body)
		if oe.Code != want.code || oe.Message != want.msg {
			t.Errorf("%s: %+v", body, oe)
		}
	}
	if n := len(e.fake("signal").CallsTo("SendMessage")); n != 0 {
		t.Fatal("check sends nothing")
	}
}

func writePNG(t *testing.T, dir, name string, size int) string {
	t.Helper()
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, size)...)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSendAttachments(t *testing.T) {
	e := sendEnv(t, "telegram")
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png", 100)
	b := writePNG(t, dir, "b.png", 50)
	body := `{"protocol":"telegram","contact_id":"c1","text":"caption","batch_id":" batch-1 ","attachments":[
	  {"path":"` + a + `","filename":"a.png"},{"path":"` + b + `","filename":"b.png","content_type":"image/png"}]}`
	wantSent(t, 2, e.mustCall("messages.send", body))
	calls := e.fake("telegram").CallsTo("SendAttachments")
	if len(calls) != 1 {
		t.Fatalf("calls: %+v", e.fake("telegram").Calls())
	}
	atts := calls[0].Args["attachments"].([]proto.Attachment)
	if len(atts) != 2 || atts[0].Caption != "caption" || atts[1].Caption != "" ||
		atts[0].MimeType != "image/png" || atts[0].MediaKind != "image" || atts[0].Filename != "a.png" || atts[1].Path != b {
		t.Fatalf("attachments: %+v", atts)
	}
	if calls[0].Args["batch"] != " batch-1 " {
		t.Fatalf("batch id is passed as given when non-blank: %v", calls[0].Args["batch"])
	}
	// Attachment sends are not deduplicated: no registry entries.
	if e.core.SendRegistry().Len() != 0 {
		t.Fatal("attachment sends bypass the registry")
	}
	pushes := e.webPushes()
	if len(pushes) != 1 {
		t.Fatalf("pushes %v", pushes)
	}

	// First failure wins, in order.
	txt := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(txt, []byte("plain text"), 0o600)
	oe := e.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"","attachments":[{"path":"`+txt+`","filename":"x.txt"},{"path":"","rejected":"too_large"}]}`)
	if oe.Code != "invalid_request" || oe.Message != "Unsupported media type" {
		t.Fatalf("%+v", oe)
	}
	oe = e.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"","attachments":[{"path":"","rejected":"too_large"}]}`)
	if oe.Code != "too_large" || oe.Message != "Upload too large" {
		t.Fatalf("%+v", oe)
	}
	oe = e.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"","attachments":[{"path":"/nonexistent/f.png","filename":"f.png"}]}`)
	if oe.Code != "invalid_request" || oe.Message != "Invalid request" {
		t.Fatalf("%+v", oe)
	}
	// Extension must match the sniffed type.
	wrong := writePNG(t, dir, "p.jpg", 10)
	oe = e.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"","attachments":[{"path":"`+wrong+`","filename":"p.jpg"}]}`)
	if oe.Message != "Unsupported media type" {
		t.Fatalf("%+v", oe)
	}
	// A slot with neither path nor rejection is invalid.
	oe = e.callErr("messages.send", `{"protocol":"telegram","contact_id":"c1","text":"","attachments":[{"path":""}]}`)
	if oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}
	// Backend failure -> Message send failed.
	e.fake("telegram").FailMethod("SendAttachments", errors.New("boom"))
	oe = e.callErr("messages.send", body)
	if oe.Code != "backend_error" || oe.Message != "Message send failed" {
		t.Fatalf("%+v", oe)
	}
}

func TestSendAttachmentsNotSupported(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	p := writePNG(t, dir, "a.png", 10)
	// A backend without any attachment capability.
	e.mgr.Register(&bare{fake.New("signal")})
	e.fake("signal").SetContacts(contact("signal", "c1", "C", nil))
	_ = e.mgr.Register(&bare{e.fake("signal")})
	oe := e.callErr("messages.send", `{"protocol":"signal","contact_id":"c1","text":"","attachments":[{"path":"`+p+`","filename":"a.png"}]}`)
	if oe.Code != "not_supported" || oe.Message != "Attachment send not supported" {
		t.Fatalf("%+v", oe)
	}
}

// bare hides every optional capability of the fake.
type bare struct{ b *fake.Backend }

func (x *bare) Protocol() string                   { return x.b.Protocol() }
func (x *bare) Connect(c context.Context) error    { return x.b.Connect(c) }
func (x *bare) Disconnect(c context.Context) error { return x.b.Disconnect(c) }
func (x *bare) IsConnected() bool                  { return x.b.IsConnected() }
func (x *bare) ListContacts(c context.Context) ([]proto.Contact, error) {
	return x.b.ListContacts(c)
}
func (x *bare) SendMessage(c context.Context, id, t string, q proto.Quote) (string, error) {
	return x.b.SendMessage(c, id, t, q)
}
func (x *bare) MarkRead(c context.Context, id string) error { return x.b.MarkRead(c, id) }
func (x *bare) Receive(c context.Context) (<-chan proto.Event, error) {
	return x.b.Receive(c)
}

// ---- optimistic flavour ------------------------------------------------------

func rowByText(t *testing.T, e *env, protocol, contact, text string) store.Message {
	t.Helper()
	rows, err := e.st.ListMessages(bg, store.MessageQuery{Protocol: protocol, Keys: []string{contact}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if store.Deref(r.Text) == text {
			return r
		}
	}
	t.Fatalf("no row with text %q in %+v", text, rows)
	return store.Message{}
}

func TestSendOptimisticSuccess(t *testing.T) {
	e := sendEnv(t, "signal")
	ts := time.Now().UnixMilli()
	body := `{"protocol":"signal","contact_id":"c1","text":"opt","retry":false,"optimistic":{"timestamp":` + itoa(ts) + `}}`
	out := e.mustCall("messages.send", body)
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	mid, _ := res["message_id"].(string)
	if mid == "" {
		t.Fatalf("message_id should be attached: %s", out)
	}
	wantJSON(t, out, `{"ok":true,"status":"sent","status_applied":true,"message_id":"`+mid+`"}`)
	row := rowByText(t, e, "signal", "c1", "opt")
	if store.Deref(row.Status) != "sent" || store.Deref(row.MsgID) != mid || row.Timestamp != ts || !row.IsMine || store.Deref(row.Sender) != "You" {
		t.Fatalf("row: %+v", row)
	}
	pushes := e.webPushes()
	if len(pushes) != 1 {
		t.Fatalf("pushes: %v", pushes)
	}
	// The seeded row is announced once, with id null and the optimistic ts.
	wantJSON(t, pushes[0], `{"event":{"type":"message","payload":{"id":null,"protocol":"signal","contact_id":"c1","timestamp":`+itoa(ts)+`,"is_mine":true}}}`)
	// Sent straight to the backend: no sent-mirror event was queued.
	if n := len(e.fake("signal").CallsTo("EnqueueSentMessage")); n != 0 {
		t.Fatalf("optimistic sends are not mirrored: %d", n)
	}
	// Recency of the contact was bumped to the optimistic timestamp.
	if ct, _ := e.core.FindContact(bg, "signal", "c1"); ct.LastMessageTS() != ts {
		t.Fatalf("recency: %d want %d", ct.LastMessageTS(), ts)
	}
}

func TestSendOptimisticFailureAndResend(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "c1", "C", nil))
	sc := &scripted{Backend: f, script: []error{errors.New("nope")}}
	_ = e.mgr.Register(sc)
	ts := int64(1700000000000)
	body := `{"protocol":"signal","contact_id":"c1","text":"will fail","retry":false,"optimistic":{"timestamp":1700000000000}}`
	oe := e.callErr("messages.send", body)
	if oe.Code != "backend_error" || oe.Message != "Message send failed" {
		t.Fatalf("%+v", oe)
	}
	wantJSON(t, mustMarshal(t, oe.Details), `{"status_applied":true,"error":"nope"}`)
	if st := store.Deref(rowByText(t, e, "signal", "c1", "will fail").Status); st != "failed" {
		t.Fatalf("status after failure: %q", st)
	}
	// Re-arm and resend: failed -> pending -> sent, nothing seeded twice.
	e.events()
	resend := `{"protocol":"signal","contact_id":"c1","text":"will fail","retry":false,"optimistic":{"timestamp":1700000000000,"resend":true}}`
	out := e.mustCall("messages.send", resend)
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if res["status"] != "sent" || res["status_applied"] != true || res["message_id"] == nil {
		t.Fatalf("resend result: %s", out)
	}
	rows, _ := e.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{"c1"}})
	if len(rows) != 1 || store.Deref(rows[0].Status) != "sent" || rows[0].Timestamp != ts {
		t.Fatalf("rows: %+v", rows)
	}
	if len(e.webPushes()) != 0 {
		t.Fatal("a re-armed row is not announced again")
	}
	// Resending a row that is not failed is refused.
	oe = e.callErr("messages.send", resend)
	if oe.Code != "conflict" || oe.Message != "Message is not in a failed state" {
		t.Fatalf("%+v", oe)
	}
	wantJSON(t, mustMarshal(t, oe.Details), `{"status_applied":false,"error":null}`)
}

func TestSendOptimisticSameRowTwiceSeedsOnce(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "c1", "C", nil))
	sc := &scripted{Backend: f, script: []error{errors.New("x")}}
	_ = e.mgr.Register(sc)
	body := `{"protocol":"signal","contact_id":"c1","text":"same","retry":false,"client_msg_id":"o1","optimistic":{"timestamp":42}}`
	e.callErr("messages.send", body) // first attempt fails, leaving an id-less failed row
	n1 := len(e.webPushes())
	// A new (non-resend) send of the same text within the window adds nothing.
	e.mustCall("messages.send", strings.Replace(strings.Replace(body, `"o1"`, `"o2"`, 1), `"timestamp":42`, `"timestamp":3000`, 1))
	n2 := len(e.webPushes())
	rows, _ := e.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{"c1"}})
	if n1 != 1 || n2 != 0 || len(rows) != 1 {
		t.Fatalf("announced %d then %d times, rows %+v", n1, n2, rows)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wantSent asserts a plain messages.send success: {"ok":true,"message_ids":[n
// non-empty strings]} (the fake backends mint their ids).
func wantSent(t *testing.T, n int, got string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("not JSON: %q", got)
	}
	ids, _ := m["message_ids"].([]any)
	if len(m) != 2 || m["ok"] != true || len(ids) != n {
		t.Fatalf("send response: %s (want ok + %d message_ids)", got, n)
	}
	for _, id := range ids {
		if s, ok := id.(string); !ok || s == "" {
			t.Fatalf("message_ids must be non-empty strings: %s", got)
		}
	}
}

// idBackend returns a fixed id (or "" for none) from SendMessage.
type idBackend struct {
	*fake.Backend
	id string
}

func (s *idBackend) SendMessage(ctx context.Context, contactID, text string, q proto.Quote) (string, error) {
	return s.id, nil
}

func TestSendMessageIDsExactAndEmpty(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "c1", "C", nil))
	ib := &idBackend{Backend: f, id: "1700000000123"}
	_ = e.mgr.Register(ib)
	wantJSON(t, e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"a","client_msg_id":"i1"}`),
		`{"ok":true,"message_ids":["1700000000123"]}`)
	// The duplicate replay carries no ids.
	wantJSON(t, e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"a","client_msg_id":"i1"}`),
		`{"ok":true,"duplicate":true}`)
	// A backend that returns no id: an empty list, never null.
	ib.id = ""
	wantJSON(t, e.mustCall("messages.send", `{"protocol":"signal","contact_id":"c1","text":"b","client_msg_id":"i2"}`),
		`{"ok":true,"message_ids":[]}`)
}
