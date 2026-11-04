package gmail

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

func TestParseSummaryAndMIME(t *testing.T) {
	raw := []byte("From: =?UTF-8?B?Sm9zw6k=?= <jose@x.org>\r\nTo: me@example.com\r\nSubject: =?iso-8859-1?Q?caff=E8?=\r\nDate: Tue, 05 Mar 2024 10:00:00 +0100\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n")
	sm := parseSummary(raw)
	if sm.From != "José <jose@x.org>" || sm.Subject != "caffè" || !sm.HasAttachments {
		t.Fatalf("%+v", sm)
	}
	if sm.DateTS == nil || *sm.DateTS != 1709629200 {
		t.Fatalf("date %v", sm.DateTS)
	}
	for _, bad := range []string{"", "garbage", "Tue, 99 Foo 2024"} {
		if parseDateTS(bad) != nil {
			t.Fatalf("date %q parsed", bad)
		}
	}
}

const multipartMsg = `From: a@x.org
To: me@example.com
Subject: multi
Date: Tue, 05 Mar 2024 10:00:00 +0000
Message-ID: <m1@x.org>
Content-Type: multipart/mixed; boundary="OUT"

--OUT
Content-Type: multipart/alternative; boundary="ALT"

--ALT
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

Ciao caff=C3=A8
--ALT
Content-Type: multipart/related; boundary="REL"

--REL
Content-Type: text/html; charset=utf-8

<p>Hi <img src="cid:logo@x"></p>
--REL
Content-Type: image/png
Content-ID: <logo@x>
Content-Transfer-Encoding: base64

iVBORw0KGgo=
--REL--
--ALT--
--OUT
Content-Type: application/pdf; name="=?utf-8?Q?r=C3=A9sum=C3=A9.pdf?="
Content-Disposition: attachment; filename="../evil/report.pdf"
Content-Transfer-Encoding: base64

JVBERi0=
--OUT
Content-Type: text/plain; name="notes.txt"
Content-Disposition: attachment

attached text is not the body
--OUT--
`

func TestParseMultipartAttachments(t *testing.T) {
	s := testSvc(t)
	pm := s.parseFull([]byte(strings.ReplaceAll(multipartMsg, "\n", "\r\n")), "INBOX", 5)
	if pm.Text != "Ciao caffè" {
		t.Fatalf("text %q", pm.Text)
	}
	if len(pm.Attachments) != 3 {
		t.Fatalf("attachments %+v", pm.Attachments)
	}
	a0, a1, a2 := pm.Attachments[0], pm.Attachments[1], pm.Attachments[2]
	if a0.Filename != "attachment-0" || a0.ContentID != "logo@x" || !a0.Inline || string(a0.Data[1:4]) != "PNG" {
		t.Fatalf("inline: %+v", a0)
	}
	if a1.Filename != "report.pdf" || a1.Inline || string(a1.Data) != "%PDF-" || a1.ContentType != "application/pdf" {
		t.Fatalf("pdf: %+v", a1)
	}
	if a2.Filename != "notes.txt" {
		t.Fatalf("txt: %+v", a2)
	}
	if !strings.Contains(pm.HTML, `data-src="gmail-image?token=`) {
		t.Fatalf("cid image not proxied: %s", pm.HTML)
	}
	if pm.MessageID != "<m1@x.org>" {
		t.Fatal(pm.MessageID)
	}
}

func TestHTMLOnlyFallsBackToText(t *testing.T) {
	s := testSvc(t)
	raw := "Subject: h\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<div>Hello<br>World</div>"
	pm := s.parseFull([]byte(raw), "", 0)
	if pm.Text != "Hello\nWorld" {
		t.Fatalf("%q", pm.Text)
	}
}

func TestUnknownCharsetDoesNotCrash(t *testing.T) {
	s := testSvc(t)
	raw := "Subject: =?x-bogus?Q?abc?=\r\nContent-Type: text/plain; charset=x-bogus\r\n\r\ncaff\xe8"
	pm := s.parseFull([]byte(raw), "", 0)
	if !strings.HasPrefix(pm.Text, "caff") || pm.Subject == "" {
		t.Fatalf("%+v", pm)
	}
}

func TestValidators(t *testing.T) {
	s := testSvc(t)
	for _, ok := range []string{"INBOX", "Work/2024", "a b.c-d_e"} {
		if _, err := s.validateFolder(&ok); err != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a*", "a%b", `a"b`, "[Gmail]/Trash", "a\nb"} {
		b := bad
		if _, err := s.validateFolder(&b); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := validateHeaderValue("a\r\nBcc: x", "subject"); err == nil {
		t.Error("CRLF accepted")
	}
	if got, err := parseAddresses("a@x.it, ,B <b@y.it>,", "to"); err != nil || len(got) != 2 || got[1] != "b@y.it" {
		t.Errorf("addresses %v %v", got, err)
	}
	if _, err := parseAddresses("nobody", "to"); err == nil || err.Error() != "Indirizzo to non valido" {
		t.Errorf("err %v", err)
	}
	if got := appendQuote("hi", "a\n\nb"); got != "hi\n\n> a\n>\n> b" {
		t.Errorf("quote %q", got)
	}
	if replySubject("Re: x") != "Re: x" || replySubject("") != "Re:" || forwardSubject("Fw: x") != "Fw: x" || forwardSubject("y") != "Fwd: y" {
		t.Error("subjects")
	}
}

func TestValidateUIDs(t *testing.T) {
	cases := []struct {
		in, msg string
	}{
		{`null`, "Lista UID non valida"}, {`"x"`, "Lista UID non valida"}, {`[]`, "Lista UID vuota"},
		{`[true]`, "UID non valido"}, {`[1.5]`, "UID non valido"}, {`["1"]`, "UID non valido"}, {`[0]`, "uid non valido"},
	}
	for _, c := range cases {
		_, err := validateUIDs(json.RawMessage(c.in))
		if err == nil || err.Error() != c.msg {
			t.Errorf("%s: %v", c.in, err)
		}
	}
	big := "[" + strings.Repeat("1,", 200) + "1]"
	if _, err := validateUIDs(json.RawMessage(big)); err == nil || err.Error() != "Troppi UID (max 200)" {
		t.Error(err)
	}
	got, err := validateUIDs(json.RawMessage(`[3,1,3,2]`))
	if err != nil || len(got) != 3 || got[0] != 3 || got[2] != 2 {
		t.Errorf("%v %v", got, err)
	}
}

func setup(t *testing.T) (*Service, *fakeIMAP, *fakeSMTP) {
	imap := newFakeIMAP(t, "me@example.com", "secret")
	sm := newFakeSMTP(t)
	s, _ := newTestService(t, imap, sm)
	return s, imap, sm
}

func TestListSortCacheAndFlags(t *testing.T) {
	s, imap, _ := setup(t)
	imap.add("INBOX", nil, rfc("old", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	imap.add("INBOX", []string{`\Seen`}, rfc("new", "b@x.org", "Mon, 01 Mar 2024 10:00:00 +0000", "b"))
	imap.add("INBOX", nil, "Subject: nodate\r\n\r\nx")
	ctx := context.Background()
	out, err := s.FetchMessageList(ctx, nil, 0, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.UIDValidity != 7 || out.Folder != "INBOX" || out.Window != 200 || out.Total != 3 || out.HasMore {
		t.Fatalf("%+v", out)
	}
	order := []int{out.Messages[0].UID, out.Messages[1].UID, out.Messages[2].UID}
	if order[0] != 2 || order[1] != 1 || order[2] != 3 {
		t.Fatalf("order %v (undated must be last)", order)
	}
	if !out.Messages[0].Seen || out.Messages[0].Flags[0] != `\Seen` || out.Messages[1].Seen || out.Messages[0].Snippet != "" {
		t.Fatalf("flags %+v", out.Messages)
	}
	// cached: new mail invisible until refresh
	imap.add("INBOX", nil, rfc("newer", "c@x.org", "Mon, 02 Mar 2024 10:00:00 +0000", "b"))
	out, _ = s.FetchMessageList(ctx, nil, 0, 2, false)
	if out.Total != 3 || !out.HasMore || len(out.Messages) != 2 {
		t.Fatalf("cache/pagination %+v", out)
	}
	out, _ = s.FetchMessageList(ctx, nil, 0, 50, true)
	if out.Total != 4 || out.Messages[0].UID != 4 {
		t.Fatalf("refresh %+v", out)
	}
	// mark read patches the cache in place
	if err := s.MarkRead(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	out, _ = s.FetchMessageList(ctx, nil, 0, 50, false)
	for _, m := range out.Messages {
		if m.UID == 1 && !m.Seen {
			t.Fatal("cache not patched")
		}
	}
	if _, err := s.FetchMessageList(ctx, nil, 0, 0, false); err == nil || err.Error() != "Limit non valido" {
		t.Fatal(err)
	}
	if _, err := s.FetchMessageList(ctx, nil, -1, 5, false); err == nil || err.Error() != "Offset non valido" {
		t.Fatal(err)
	}
}

func TestFetchMessageAndAttachment(t *testing.T) {
	s, imap, _ := setup(t)
	uid := imap.add("INBOX", nil, multipartMsg)
	ctx := context.Background()
	d, err := s.FetchMessage(ctx, uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.UID != uid || d.Subject != "multi" || d.Text != "Ciao caffè" || len(d.Attachments) != 3 {
		t.Fatalf("%+v", d)
	}
	if d.Attachments[0].Inline == nil || !*d.Attachments[0].Inline || d.Attachments[0].ContentID == nil || *d.Attachments[0].ContentID != "logo@x" {
		t.Fatalf("%+v", d.Attachments[0])
	}
	if d.Attachments[1].Inline == nil || *d.Attachments[1].Inline || d.Attachments[1].ContentID != nil {
		t.Fatalf("%+v", d.Attachments[1])
	}
	dl, err := s.FetchAttachment(ctx, uid, 1, nil)
	if err != nil || dl.Filename != "report.pdf" || string(dl.Data) != "%PDF-" {
		t.Fatalf("%+v %v", dl, err)
	}
	if _, err := s.FetchAttachment(ctx, uid, 9, nil); err == nil {
		t.Fatal("missing index")
	} else if _, ok := err.(*notFoundError); !ok {
		t.Fatalf("%T", err)
	}
	if _, err := s.FetchMessage(ctx, 999, nil); err == nil {
		t.Fatal("missing uid")
	} else if _, ok := err.(*notFoundError); !ok {
		t.Fatalf("%T", err)
	}
	// inline image prefetched into the cache
	if len(s.cachedInlineImage("INBOX", int64(uid), 0)) == 0 {
		t.Fatal("inline image not cached")
	}
}

func TestDeleteAndBulkDelete(t *testing.T) {
	s, imap, _ := setup(t)
	for i := 0; i < 3; i++ {
		imap.add("INBOX", nil, rfc("m", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	}
	ctx := context.Background()
	res, err := s.DeleteMessages(ctx, []int64{1, 3, 42}, nil)
	if err != nil || res.Trashed != 2 || len(res.Missing) != 1 || res.Missing[0] != 42 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := imap.uids("INBOX"); len(got) != 1 || got[0] != 2 {
		t.Fatalf("inbox %v", got)
	}
	if len(imap.uids("[Gmail]/Trash")) != 2 {
		t.Fatal("trash")
	}
	if err := s.DeleteMessage(ctx, 77, nil); err == nil {
		t.Fatal("deleting unknown uid must fail")
	} else if _, ok := err.(*notFoundError); !ok {
		t.Fatalf("%T", err)
	}
}

func TestUnreadStatusAndWatcher(t *testing.T) {
	s, imap, _ := setup(t)
	imap.add("INBOX", nil, rfc("a", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	imap.add("INBOX", []string{`\Seen`}, rfc("b", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	st, err := s.FetchUnreadStatus(context.Background(), nil)
	if err != nil || st.Unread != 1 || st.UIDNext == nil || *st.UIDNext != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	b := bus.New()
	w := NewWatcher(s, b, 0)
	sub := b.Subscribe(bus.Types(bus.GmailUnread))
	defer sub.Close()
	w.CheckOnce(context.Background())
	w.CheckOnce(context.Background()) // unchanged: no second event
	imap.add("INBOX", nil, rfc("c", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	w.CheckOnce(context.Background())
	e1 := <-sub.C()
	e2 := <-sub.C()
	if e1.Payload["unread"] != 1 || e1.Payload["new_mail"] != false || e2.Payload["unread"] != 2 || e2.Payload["new_mail"] != true {
		t.Fatalf("%v %v", e1.Payload, e2.Payload)
	}
	select {
	case e := <-sub.C():
		t.Fatalf("unexpected event %v", e)
	default:
	}
}

func TestAddressBook(t *testing.T) {
	s, imap, _ := setup(t)
	imap.add("[Gmail]/Sent Mail", nil, "From: me@example.com\nTo: Bob <bob@x.org>, carol@x.org\nCc: dave@x.org\nDate: Mon, 01 Jan 2024 10:00:00 +0000\n\nx")
	imap.add("INBOX", nil, "From: \"Bob B.\" <bob@x.org>\nTo: me@example.com, noreply-bot@x.org\nDate: Mon, 02 Jan 2024 10:00:00 +0000\n\nx")
	imap.add("INBOX", nil, "From: no-reply@x.org\nTo: me@example.com\nDate: Mon, 02 Jan 2024 10:00:00 +0000\n\nx")
	got, err := s.FetchAddressBook(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Email != "bob@x.org" || got[0].Score != 4 || got[0].Name != "Bob B." {
		t.Fatalf("%+v", got)
	}
	if got[1].Email != "carol@x.org" || got[1].Score != 3 || got[2].Email != "dave@x.org" || got[2].Score != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestSendEmailReplyAndAttachments(t *testing.T) {
	s, imap, sm := setup(t)
	uid := imap.add("INBOX", nil, "From: Ann <ann@x.org>\nTo: me@example.com\nSubject: Question\nMessage-ID: <q1@x.org>\nReferences: <r0@x.org>\nDate: Mon, 01 Jan 2024 10:00:00 +0000\nContent-Type: text/plain\n\nline1\n\nline2")
	ctx := context.Background()
	id, err := s.SendEmail(ctx, SendParams{Text: "thanks è", ReplyToUID: &uid, Bcc: "hid@x.org",
		Attachments: []SendFile{{Filename: "../a b.txt", Data: []byte("hello")}}})
	if err != nil {
		t.Fatal(err)
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.from != "me@example.com" || strings.Join(sm.to, ",") != "ann@x.org,hid@x.org" || !strings.HasPrefix(sm.auth, "AUTH PLAIN") {
		t.Fatalf("envelope %q %v %q", sm.from, sm.to, sm.auth)
	}
	for _, want := range []string{"Subject: Re: Question", "In-Reply-To: <q1@x.org>", "References: <r0@x.org> <q1@x.org>", "Message-ID: " + id, "To: ann@x.org", `filename="a b.txt"`, "> line1", ">\r\n", "> line2", "multipart/mixed"} {
		if !strings.Contains(sm.data, want) {
			t.Errorf("missing %q in:\n%s", want, sm.data)
		}
	}
	if strings.Contains(sm.data, "Bcc") || strings.Contains(sm.data, "hid@x.org") {
		t.Error("Bcc leaked into headers")
	}
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("message id %q", id)
	}
}

func TestSendErrors(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	one := 1
	cases := []struct {
		p   SendParams
		msg string
	}{
		{SendParams{To: "a@x.it", ReplyToUID: &one, ForwardUID: &one}, "Risposta e inoltro non possono essere combinati"},
		{SendParams{To: "bad"}, "Indirizzo to non valido"},
		{SendParams{To: "a@x.it", Subject: "x\ny"}, "Intestazione subject non valida"},
		{SendParams{}, "Destinatario mancante"},
		{SendParams{To: "a@x.it", Text: strings.Repeat("x", maxTextLength+1)}, "Testo troppo lungo"},
	}
	for _, c := range cases {
		_, err := s.SendEmail(ctx, c.p)
		if err == nil || err.Error() != c.msg {
			t.Errorf("%+v: %v", c.p, err)
		}
	}
	big := make([]SendFile, 11)
	if _, err := s.SendEmail(ctx, SendParams{To: "a@x.it", Attachments: big}); err == nil || err.Error() != "Troppi allegati" {
		t.Error(err)
	}
}

func TestForwardCarriesAttachments(t *testing.T) {
	s, imap, sm := setup(t)
	uid := imap.add("INBOX", nil, multipartMsg)
	if _, err := s.SendEmail(context.Background(), SendParams{To: "z@x.org", ForwardUID: &uid}); err != nil {
		t.Fatal(err)
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, want := range []string{"Subject: Fwd: multi", "Messaggio inoltrato", "report.pdf", "notes.txt"} {
		if !strings.Contains(sm.data, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// ---- ops ----

func call(t *testing.T, reg *ops.Registry, name, params string) (any, error) {
	t.Helper()
	return reg.Call(context.Background(), name, json.RawMessage(params))
}

func jsonOf(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func opsSetup(t *testing.T) (*ops.Registry, *fakeIMAP, *fakeSMTP, *testCfg) {
	imap := newFakeIMAP(t, "me@example.com", "secret")
	sm := newFakeSMTP(t)
	s, cfg := newTestService(t, imap, sm)
	reg := ops.NewRegistry()
	RegisterOps(reg, Deps{Config: cfg, Service: s, TLS: s.tls})
	return reg, imap, sm, cfg
}

func TestOpsRegistered(t *testing.T) {
	reg, _, _, _ := opsSetup(t)
	got := strings.Join(reg.Names(), ",")
	for _, n := range []string{api.OpGmailStatus, api.OpGmailUnread, api.OpGmailMessagesList, api.OpGmailMessagesGet,
		api.OpGmailMessagesDelete, api.OpGmailMessagesBulkDelete, api.OpGmailMessagesMarkRead, api.OpGmailMessagesMarkUnread,
		api.OpGmailMessagesArchive, api.OpGmailAddressBook, api.OpGmailMessagesSend, api.OpGmailMessagesSendCheck,
		api.OpGmailAttachment, api.OpGmailImage} {
		if !strings.Contains(got, n) {
			t.Errorf("%s not registered", n)
		}
	}
	if len(reg.Names()) != 14 {
		t.Errorf("%d ops", len(reg.Names()))
	}
}

func TestOpsStatusPayload(t *testing.T) {
	reg, _, _, _ := opsSetup(t)
	v, err := call(t, reg, api.OpGmailStatus, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"configured":true,"address":"me@example.com","folder":"INBOX","connected":null,"limits":{"max_text_length":65536,"max_attachments":10,"max_attachment_bytes":26214400,"max_total_attachment_bytes":26214400,"max_download_bytes":52428800,"max_messages_limit":200}}`
	if jsonOf(t, v) != want {
		t.Fatalf("%s", jsonOf(t, v))
	}
}

func TestOpsDeleteQuirks(t *testing.T) {
	reg, imap, _, _ := opsSetup(t)
	for i := 0; i < 3; i++ {
		imap.add("INBOX", nil, rfc("m", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "b"))
	}
	v, err := call(t, reg, api.OpGmailMessagesDelete, `{"uid":1}`)
	if err != nil || jsonOf(t, v) != `{"ok":true,"trashed":true}` {
		t.Fatalf("%v %v", v, err)
	}
	v, err = call(t, reg, api.OpGmailMessagesBulkDelete, `{"uids":[2,3,9]}`)
	if err != nil || jsonOf(t, v) != `{"ok":true,"trashed":2,"missing":[9]}` {
		t.Fatalf("%v %v", v, err)
	}
	v, _ = call(t, reg, api.OpGmailMessagesBulkDelete, `{"uids":[8]}`)
	if jsonOf(t, v) != `{"ok":true,"trashed":0,"missing":[8]}` {
		t.Fatalf("%s", jsonOf(t, v))
	}
	_, err = call(t, reg, api.OpGmailMessagesDelete, `{"uid":55}`)
	if e := ops.AsError(err); e.Code != api.CodeNotFound || e.Message != "Risorsa non trovata" {
		t.Fatalf("%v", err)
	}
	_, err = call(t, reg, api.OpGmailMessagesBulkDelete, `{"uids":[1],"folder":5}`)
	if e := ops.AsError(err); e.Code != api.CodeInvalidRequest || e.Message != "Cartella non valida" {
		t.Fatalf("%v", err)
	}
	_, err = call(t, reg, api.OpGmailMessagesBulkDelete, `{"uids":[]}`)
	if e := ops.AsError(err); e.Code != api.CodeInvalidRequest || e.Message != "Lista UID vuota" {
		t.Fatalf("%v", err)
	}
}

func TestOpsErrorMapping(t *testing.T) {
	reg, _, _, cfg := opsSetup(t)
	cfg.pass = ""
	_, err := call(t, reg, api.OpGmailMessagesList, `{}`)
	if e := ops.AsError(err); e.Code != api.CodeBackendDown || e.Message != "Gmail non configurado" && e.Message != "Gmail non configurato" {
		t.Fatalf("%v", err)
	}
	v, _ := call(t, reg, api.OpGmailUnread, `{}`)
	if jsonOf(t, v) != `{"configured":false,"unread":0}` {
		t.Fatal(jsonOf(t, v))
	}
	cfg.pass = "wrong"
	_, err = call(t, reg, api.OpGmailMessagesList, `{}`)
	if e := ops.AsError(err); e.Code != api.CodeBackendError || e.Message != "Errore di comunicazione con Gmail" {
		t.Fatalf("%v", err)
	}
	cfg.pass = "secret"
	_, err = call(t, reg, api.OpGmailMessagesList, `{"folder":"a*"}`)
	if e := ops.AsError(err); e.Code != api.CodeInvalidRequest || e.Message != "Cartella non valida" {
		t.Fatalf("%v", err)
	}
	_, err = call(t, reg, api.OpGmailMessagesSend, `{"to":"a@x.it","text":"`+strings.Repeat("x", maxTextLength+1)+`"}`)
	if e := ops.AsError(err); e.Code != api.CodeTooLarge || e.Message != "Testo troppo lungo" {
		t.Fatalf("%v", err)
	}
}

func TestOpsListGetFlagsAttachmentImageSend(t *testing.T) {
	reg, imap, sm, _ := opsSetup(t)
	uid := imap.add("INBOX", nil, multipartMsg)
	v, err := call(t, reg, api.OpGmailMessagesList, `{}`)
	if err != nil || !strings.Contains(jsonOf(t, v), `"uidvalidity":7`) || !strings.Contains(jsonOf(t, v), `"has_attachments":true`) {
		t.Fatalf("%v %v", jsonOf(t, v), err)
	}
	v, err = call(t, reg, api.OpGmailUnread, `{}`)
	if err != nil || jsonOf(t, v) != `{"configured":true,"unread":1}` {
		t.Fatalf("%v %v", v, err)
	}
	for _, name := range []string{api.OpGmailMessagesMarkRead, api.OpGmailMessagesMarkUnread, api.OpGmailMessagesArchive} {
		v, err = call(t, reg, name, `{"uid":`+itoa(uid)+`}`)
		if err != nil || jsonOf(t, v) != `{"ok":true}` {
			t.Fatalf("%s: %v %v", name, v, err)
		}
	}
	// message detail with a proxied inline image, then fetch that image
	v, err = call(t, reg, api.OpGmailMessagesGet, `{"uid":`+itoa(uid)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	detail := v.(api.GmailMessageDetail)
	m := regexp.MustCompile(`data-src="gmail-image\?token=([^&"]+)`).FindStringSubmatch(detail.HTML)
	if m == nil {
		t.Fatalf("no proxied image: %s", detail.HTML)
	}
	// 8-byte stub is not a decodable PNG but starts with the PNG magic
	_, err = call(t, reg, api.OpGmailImage, `{"token":"`+m[1]+`"}`)
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	_, err = call(t, reg, api.OpGmailImage, `{"token":"bad.token"}`)
	if e := ops.AsError(err); e.Code != api.CodeNotFound || e.Message != "Immagine non trovata" {
		t.Fatalf("%v", err)
	}
	// remote image without a fetcher is unavailable
	svc := regSvc(reg)
	_ = svc
	// attachment blob
	v, err = call(t, reg, api.OpGmailAttachment, `{"uid":`+itoa(uid)+`,"index":1}`)
	if err != nil {
		t.Fatal(err)
	}
	att := v.(api.GmailAttachmentOut)
	data, rerr := os.ReadFile(att.Path)
	if rerr != nil || string(data) != "%PDF-" || att.Filename != "report.pdf" || *att.ContentType != "application/pdf" || att.Version == nil {
		t.Fatalf("%+v %v", att, rerr)
	}
	// send via file slot
	f := t.TempDir() + "/up.bin"
	_ = os.WriteFile(f, []byte("xyz"), 0o600)
	v, err = call(t, reg, api.OpGmailMessagesSend, `{"to":"a@x.it","subject":"s","text":"t","attachments":[{"path":"`+f+`","filename":"up.bin"}]}`)
	if err != nil || !strings.Contains(jsonOf(t, v), `"ok":true,"message_id":"\u003c`) {
		t.Fatalf("%v %v", v, err)
	}
	sm.mu.Lock()
	if !strings.Contains(sm.data, `filename="up.bin"`) {
		t.Error("attachment missing from SMTP data")
	}
	sm.mu.Unlock()
	// send.check
	_, err = call(t, reg, api.OpGmailMessagesSendCheck, `{"attachment_count":11}`)
	if e := ops.AsError(err); e.Message != "Troppi allegati" {
		t.Fatalf("%v", err)
	}
	_, err = call(t, reg, api.OpGmailMessagesSendCheck, `{"attachment_count":1,"slots_ok":false}`)
	if e := ops.AsError(err); e.Message != "Richiesta non valida" {
		t.Fatalf("%v", err)
	}
	v, err = call(t, reg, api.OpGmailMessagesSendCheck, `{}`)
	if err != nil || jsonOf(t, v) != `{"ok":true}` {
		t.Fatalf("%v %v", v, err)
	}
	// address book payload shape
	v, err = call(t, reg, api.OpGmailAddressBook, `{}`)
	if err != nil || !strings.HasPrefix(jsonOf(t, v), `{"contacts":[`) {
		t.Fatalf("%v %v", v, err)
	}
}

func regSvc(*ops.Registry) *Service { return nil }

type fakeFetcher struct{ calls int }

func (f *fakeFetcher) FetchExternalImage(_ context.Context, u string, _ int64, _ int64) ([]byte, string) {
	f.calls++
	if strings.HasSuffix(u, "/ok.png") {
		return []byte("\x89PNG\r\n\x1a\nxxxx"), "image/png"
	}
	return nil, ""
}

func TestRemoteImageProxy(t *testing.T) {
	imap := newFakeIMAP(t, "me@example.com", "secret")
	s, cfg := newTestService(t, imap, nil)
	ff := &fakeFetcher{}
	reg := ops.NewRegistry()
	RegisterOps(reg, Deps{Config: cfg, Service: s, Images: ff})
	s.images = ff
	tok := func(u string) string {
		m := signedImageRE.FindStringSubmatch(s.signImageURL(u))
		return m[1] + "." + m[2]
	}
	v, err := call(t, reg, api.OpGmailImage, `{"token":"`+tok("https://e.example/ok.png")+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	blob := v.(api.BlobOut)
	if *blob.ContentType != "image/png" || blob.Size == nil || *blob.Size != 12 {
		t.Fatalf("%+v", blob)
	}
	_, _ = call(t, reg, api.OpGmailImage, `{"token":"`+tok("https://e.example/ok.png")+`"}`)
	if ff.calls != 1 {
		t.Fatalf("fetcher called %d times; cache expected", ff.calls)
	}
	_, err = call(t, reg, api.OpGmailImage, `{"token":"`+tok("https://e.example/bad.png")+`"}`)
	if e := ops.AsError(err); e.Code != api.CodeBackendError || e.Message != "Immagine non disponibile" {
		t.Fatalf("%v", err)
	}
	urls := s.remoteImageURLs(`<img data-src="` + strings.ReplaceAll(s.signImageURL("https://e.example/ok.png"), "&", "&amp;") + `">`)
	if len(urls) != 1 || urls[0] != "https://e.example/ok.png" {
		t.Fatalf("%v", urls)
	}
}
