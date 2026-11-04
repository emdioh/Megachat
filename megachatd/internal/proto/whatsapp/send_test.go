package whatsapp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func lastSent(t *testing.T, e *env) sentMsg {
	t.Helper()
	e.fc.mu.Lock()
	defer e.fc.mu.Unlock()
	if len(e.fc.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return e.fc.sent[len(e.fc.sent)-1]
}

func TestSendTextDM(t *testing.T) {
	e := newEnv(t)
	id, err := e.b.SendMessage(ctx, "391234567890@c.us", "Ciao", proto.Quote{})
	if err != nil {
		t.Fatal(err)
	}
	s := lastSent(t, e)
	if s.To.String() != "391234567890@s.whatsapp.net" || s.Msg.GetConversation() != "Ciao" {
		t.Fatalf("sent: %+v", s)
	}
	if want := "true_391234567890@c.us_" + string(s.Extra.ID); id != want {
		t.Errorf("id = %q, want %q", id, want)
	}
	if CanonicalMsgID(id) != string(s.Extra.ID) {
		t.Error("the returned id must canonicalise to the WhatsApp id")
	}
}

func TestSendTextToGroupAppendsOwnLID(t *testing.T) {
	e := newEnv(t)
	id, err := e.b.SendMessage(ctx, "120363000000000000@g.us", "yo", proto.Quote{})
	if err != nil {
		t.Fatal(err)
	}
	s := lastSent(t, e)
	if s.To.Server != types.GroupServer {
		t.Fatalf("to = %v", s.To)
	}
	if want := "true_120363000000000000@g.us_" + string(s.Extra.ID) + "_555555@lid"; id != want {
		t.Errorf("group id = %q, want %q", id, want)
	}
}

func TestSendLIDChatGoesToLID(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.SendMessage(ctx, "189025889575055@lid", "x", proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	if to := lastSent(t, e).To.String(); to != "189025889575055@lid" {
		t.Errorf("to = %q", to)
	}
}

// seedLIDContact registers a single @lid contact as the only contact, exactly
// like the Python _lid_contact test helper (no resolver side effects).
func seedLIDContact(e *env, lid string, phone string) *proto.Contact {
	extras := map[string]any{"jid": lid}
	if phone != "" {
		extras["phone"] = phone
	}
	c := newContact(lid, "Bob", extras)
	e.b.cmu.Lock()
	e.b.contacts = []*proto.Contact{c}
	e.b.byJID = map[string]*proto.Contact{lid: c}
	e.b.cmu.Unlock()
	return c
}

func TestSendLIDRegistersReverseAlias(t *testing.T) {
	e := newEnv(t)
	e.lids.put("139153", "393331234567")
	c := seedLIDContact(e, "139153@lid", "")
	if _, err := e.b.SendMessage(ctx, "139153@lid", "x", proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	e.b.cmu.RLock()
	alias := e.b.byJID["393331234567@c.us"]
	e.b.cmu.RUnlock()
	if alias != c {
		t.Fatalf("reverse alias %q -> %p, want the @lid contact %p", "393331234567@c.us", alias, c)
	}
	if got := exStr(c, "phone"); got != "393331234567" {
		t.Errorf("extras.phone = %q, want %q", got, "393331234567")
	}
	// The echo/ack (which WhatsApp reflects as the @c.us chat) resolves to the
	// @lid contact, so it is ingested under the @lid id.
	if got, ok := e.b.FindContact("393331234567@c.us"); !ok || got.ID != "139153@lid" {
		t.Errorf("FindContact(@c.us) = %+v (ok=%v), want the @lid contact", got, ok)
	}
}

func TestSendLIDUnresolvableLeavesNoAlias(t *testing.T) {
	e := newEnv(t)
	seedLIDContact(e, "139153@lid", "")
	if _, err := e.b.SendMessage(ctx, "139153@lid", "x", proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	e.b.cmu.RLock()
	_, has := e.b.byJID["393331234567@c.us"]
	e.b.cmu.RUnlock()
	if has {
		t.Error("an unresolvable @lid send must not register a reverse alias")
	}
}

func TestSendCUSChatUntouched(t *testing.T) {
	e := newEnv(t)
	c := seedLIDContact(e, "393331234567@c.us", "")
	if _, err := e.b.SendMessage(ctx, "393331234567@c.us", "x", proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	if _, has := c.Extras["phone"]; has {
		t.Error("a @c.us send must not add extras.phone")
	}
	e.b.cmu.RLock()
	alias := e.b.byJID["393331234567@c.us"]
	e.b.cmu.RUnlock()
	if alias != c {
		t.Errorf("the @c.us id must keep mapping to the @c.us contact, got %p", alias)
	}
}

// newBehaviourCore wires a Core to the env's backend exactly like the receive
// pump, so a backend event can be resolved and ingested through the real core.
func newBehaviourCore(t *testing.T, e *env) *core.Core {
	t.Helper()
	mgr := proto.New(proto.WithLogger(quietLogger()))
	if err := mgr.Register(e.b); err != nil {
		t.Fatal(err)
	}
	return core.New(e.cfg, e.st, mgr, bus.New(bus.WithLogger(quietLogger())),
		core.WithLogger(quietLogger()), core.WithoutStartupMaintenance())
}

// sendAndGetIDs sends "ciao" to the @lid chat and returns the returned WAHA id
// and the raw WhatsApp message id.
func sendAndGetIDs(t *testing.T, e *env) (string, string) {
	t.Helper()
	id, err := e.b.SendMessage(ctx, "139153@lid", "ciao", proto.Quote{})
	if err != nil {
		t.Fatal(err)
	}
	wid := string(lastSent(t, e).Extra.ID)
	if want := "true_139153@lid_" + wid; id != want {
		t.Fatalf("sent id = %q, want %q", id, want)
	}
	return id, wid
}

// Regression (mc-zao.5.2 / Python 1682377): after a send to a @lid chat with a
// known phone, WhatsApp reflects the echo with the @c.us chat. The alias the
// send registered makes the core resolve it to the @lid contact, so the echo
// is cached and stored under the @lid id: no transient @c.us twin contact and
// one row for the msg_id. Without the alias the core falls back to a @c.us
// placeholder, which this test rejects.
func TestSendLIDEchoIngestedUnderLID(t *testing.T) {
	e := newEnv(t)
	e.lids.put("139153", "393331234567")
	seedLIDContact(e, "139153@lid", "")
	cr := newBehaviourCore(t, e)
	_, wid := sendAndGetIDs(t, e)

	// The reflected echo: our own message, but with the @c.us chat.
	e.b.onMessage(dmEvent("393331234567", types.DefaultUserServer, wid, true, text("ciao")))
	ev := one(t, drain(e.b))
	if ev.Type != proto.EventMessage || ev.ContactID != "393331234567@c.us" {
		t.Fatalf("echo event: %+v", ev)
	}
	res, err := cr.Process(ctx, ev)
	if err != nil || len(res) != 1 || res[0].Type != bus.Message {
		t.Fatalf("process echo: %+v %v", res, err)
	}
	if got := res[0].Payload["contact_id"]; got != "139153@lid" {
		t.Errorf("echo ingested under %v, want the @lid contact", got)
	}
	entries := cached(e.b, "139153@lid")
	if len(entries) != 1 || entries[0].ID != "true_393331234567@c.us_"+wid || !entries[0].IsMine {
		t.Fatalf("cache under @lid: %+v", entries)
	}
	if twin := cached(e.b, "393331234567@c.us"); len(twin) != 0 {
		t.Errorf("the echo must not be cached under @c.us: %+v", twin)
	}
	rows := dbRows(t, e, "139153@lid")
	if len(rows) != 1 || store0(rows[0].MsgID) != "true_393331234567@c.us_"+wid || !rows[0].IsMine {
		t.Fatalf("stored rows under @lid: %+v", rows)
	}
	if twin := dbRows(t, e, "393331234567@c.us"); len(twin) != 0 {
		t.Errorf("the echo must not be stored under @c.us: %+v", twin)
	}
	// No twin contact: the @c.us id resolves to the one @lid contact.
	list, _ := e.b.ListContacts(ctx)
	if len(list) != 1 || list[0].ID != "139153@lid" {
		t.Fatalf("contacts = %+v, want only the @lid contact", list)
	}
	if got, ok := e.b.FindContact("393331234567@c.us"); !ok || got.ID != "139153@lid" {
		t.Errorf("FindContact(@c.us) = %+v (ok=%v), want the @lid contact", got, ok)
	}
}

// Regression (mc-zao.5.2 / Python 1682377): the receipt is scoped to the @lid
// chat, so it must find the is_mine row the @c.us echo produced under the @lid
// contact and upgrade it to read.
func TestSendLIDEchoReceiptUpdatesIsMineRow(t *testing.T) {
	e := newEnv(t)
	e.lids.put("139153", "393331234567")
	seedLIDContact(e, "139153@lid", "")
	cr := newBehaviourCore(t, e)
	_, wid := sendAndGetIDs(t, e)

	e.b.onMessage(dmEvent("393331234567", types.DefaultUserServer, wid, true, text("ciao")))
	if _, err := cr.Process(ctx, one(t, drain(e.b))); err != nil {
		t.Fatal(err)
	}
	if rows := dbRows(t, e, "139153@lid"); len(rows) != 1 || store0(rows[0].Status) != "sent" {
		t.Fatalf("outgoing row before receipt: %+v", rows)
	}

	// The phone reports the read on the @lid chat.
	rc := &events.Receipt{Type: types.ReceiptTypeRead, Timestamp: baseTime,
		MessageIDs: []types.MessageID{types.MessageID(wid)}}
	rc.Chat = jid("139153", types.HiddenUserServer)
	e.b.onReceipt(rc)
	rev := one(t, drain(e.b))
	if rev.Type != proto.EventReceipt || rev.ContactID != "139153@lid" {
		t.Fatalf("receipt event: %+v", rev)
	}
	res, err := cr.Process(ctx, rev)
	if err != nil || len(res) != 1 || res[0].Type != bus.Receipt {
		t.Fatalf("process receipt: %+v %v", res, err)
	}
	updated, _ := res[0].Payload["updated"].([]any)
	if len(updated) != 1 {
		t.Fatalf("updated = %+v, want the is_mine row", updated)
	}
	rows := dbRows(t, e, "139153@lid")
	if len(rows) != 1 || store0(rows[0].Status) != "read" || !rows[0].IsMine {
		t.Fatalf("row after receipt: %+v", rows)
	}
	if entries := cached(e.b, "139153@lid"); len(entries) != 1 || entries[0].Status != "read" {
		t.Fatalf("cache after receipt: %+v", entries)
	}
	if twin := dbRows(t, e, "393331234567@c.us"); len(twin) != 0 {
		t.Errorf("no @c.us row may exist: %+v", twin)
	}
}

func TestSendReply(t *testing.T) {
	e := newEnv(t)
	// Reply to a received message.
	_, err := e.b.SendMessage(ctx, "391234567890@c.us", "ok", proto.Quote{
		ReplyToMessageID: "false_391234567890@c.us_3EB0A01", Message: "originale", Author: "391234567890@c.us", Timestamp: 5})
	if err != nil {
		t.Fatal(err)
	}
	ext := lastSent(t, e).Msg.GetExtendedTextMessage()
	ci := ext.GetContextInfo()
	if ext.GetText() != "ok" || ci.GetStanzaID() != "3EB0A01" || ci.GetParticipant() != "391234567890@s.whatsapp.net" ||
		ci.GetQuotedMessage().GetConversation() != "originale" {
		t.Fatalf("reply: %+v", ext)
	}
	// Reply to one of our own messages: the participant is our own jid.
	_, _ = e.b.SendMessage(ctx, "391234567890@c.us", "ok", proto.Quote{ReplyToMessageID: "true_391234567890@c.us_3EB0A02"})
	ci = lastSent(t, e).Msg.GetExtendedTextMessage().GetContextInfo()
	if ci.GetParticipant() != "390000000000@s.whatsapp.net" {
		t.Errorf("own participant = %q", ci.GetParticipant())
	}
	// Group reply: the participant comes from the id.
	_, _ = e.b.SendMessage(ctx, "120363000000000000@g.us", "ok", proto.Quote{
		ReplyToMessageID: "false_120363000000000000@g.us_3EB0A03_20508603076702@lid"})
	ci = lastSent(t, e).Msg.GetExtendedTextMessage().GetContextInfo()
	if ci.GetStanzaID() != "3EB0A03" || ci.GetParticipant() != "20508603076702@lid" {
		t.Errorf("group reply ctx: %+v", ci)
	}
	// The quoted text falls back to the cached message.
	seed(e.b, "391234567890@c.us", &entry{ID: "false_391234567890@c.us_3EB0A04", Text: "dalla cache"})
	_, _ = e.b.SendMessage(ctx, "391234567890@c.us", "ok", proto.Quote{ReplyToMessageID: "false_391234567890@c.us_3EB0A04"})
	if q := lastSent(t, e).Msg.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage().GetConversation(); q != "dalla cache" {
		t.Errorf("cached quote text = %q", q)
	}
}

func TestSendErrors(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.SendMessage(ctx, "not-a-jid", "x", proto.Quote{}); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("invalid target: %v", err)
	}
	if _, err := e.b.SendMessage(ctx, "status@broadcast", "x", proto.Quote{}); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("broadcast target: %v", err)
	}
	e.fc.sendErr = errors.New("boom")
	if _, err := e.b.SendMessage(ctx, "39@c.us", "x", proto.Quote{}); err == nil {
		t.Error("a failing send must report the error")
	}
	e.b.connected.Store(false)
	if _, err := e.b.SendMessage(ctx, "39@c.us", "x", proto.Quote{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("not connected: %v", err)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSendAttachmentKinds(t *testing.T) {
	e := newEnv(t)
	img := writeFile(t, e.dir, "p.png", "PNGDATA")
	vid := writeFile(t, e.dir, "v.mp4", "MP4DATA")
	pdf := writeFile(t, e.dir, "doc.pdf", "PDFDATA")
	aud := writeFile(t, e.dir, "a.ogg", "OGGDATA")

	id, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: img, Caption: "foto", MimeType: "image/png"}, proto.Quote{})
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	im := lastSent(t, e).Msg.GetImageMessage()
	if im == nil || im.GetCaption() != "foto" || im.GetMimetype() != "image/png" || im.GetFileLength() != 7 {
		t.Fatalf("image: %+v", im)
	}
	if _, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: vid, MimeType: "video/mp4", MediaKind: "video"}, proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	if vm := lastSent(t, e).Msg.GetVideoMessage(); vm == nil || vm.GetMimetype() != "video/mp4" {
		t.Fatalf("video: %+v", vm)
	}
	if _, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: pdf, Caption: "ecco", Filename: "fattura.pdf"}, proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	dm := lastSent(t, e).Msg.GetDocumentMessage()
	if dm == nil || dm.GetFileName() != "fattura.pdf" || dm.GetMimetype() != "application/pdf" || dm.GetCaption() != "ecco" {
		t.Fatalf("document: %+v", dm)
	}
	// Audio goes out as a file, like WAHA's sendFile.
	if _, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: aud, MediaKind: "audio"}, proto.Quote{}); err != nil {
		t.Fatal(err)
	}
	if lastSent(t, e).Msg.GetDocumentMessage() == nil {
		t.Error("audio is sent as a document")
	}
	// A reply goes into the media message's context.
	if _, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: img}, proto.Quote{ReplyToMessageID: "false_39@c.us_3EB0A09"}); err != nil {
		t.Fatal(err)
	}
	if ci := lastSent(t, e).Msg.GetImageMessage().GetContextInfo(); ci.GetStanzaID() != "3EB0A09" {
		t.Errorf("media reply ctx: %+v", ci)
	}
	if _, err := e.b.SendAttachment(ctx, "39@c.us", proto.Attachment{Path: filepath.Join(e.dir, "missing.png")}, proto.Quote{}); err == nil {
		t.Error("missing file")
	}
}

func TestSendAttachmentsBatch(t *testing.T) {
	e := newEnv(t)
	a := writeFile(t, e.dir, "1.png", "1")
	b := writeFile(t, e.dir, "2.png", "22")
	c := writeFile(t, e.dir, "3.png", "333")
	ids, err := e.b.SendAttachments(ctx, "39@c.us", []proto.Attachment{
		{Path: a, Caption: "primo", MimeType: "image/png"},
		{Path: b, Caption: "secondo", MimeType: "image/png"},
		{Path: c, MimeType: "image/png"},
	}, "batch-1", proto.Quote{ReplyToMessageID: "false_39@c.us_3EB0A0A"})
	if err != nil || len(ids) != 3 {
		t.Fatal(ids, err)
	}
	e.fc.mu.Lock()
	sent := append([]sentMsg(nil), e.fc.sent...)
	e.fc.mu.Unlock()
	if len(sent) != 3 {
		t.Fatalf("N attachments are N messages, got %d", len(sent))
	}
	first, second := sent[0].Msg.GetImageMessage(), sent[1].Msg.GetImageMessage()
	if first.GetCaption() != "primo" || first.GetContextInfo().GetStanzaID() != "3EB0A0A" {
		t.Errorf("caption and quote apply to the first message: %+v", first)
	}
	if second.GetCaption() != "" || second.GetContextInfo() != nil {
		t.Errorf("later messages carry neither caption nor quote: %+v", second)
	}
	// The first failure stops the batch (what was delivered stays).
	e.fc.sendErr = errors.New("boom")
	ids, err = e.b.SendAttachments(ctx, "39@c.us", []proto.Attachment{{Path: a}, {Path: b}}, "b2", proto.Quote{})
	if err == nil || len(ids) != 0 {
		t.Errorf("batch failure: ids=%v err=%v", ids, err)
	}
}

func TestEditMessage(t *testing.T) {
	e := newEnv(t)
	ok, err := e.b.EditMessage(ctx, "391234567890@c.us", "true_391234567890@c.us_3EB0A0B", "corretto")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	s := lastSent(t, e)
	pm := s.Msg.GetEditedMessage().GetMessage().GetProtocolMessage()
	if s.To.String() != "391234567890@s.whatsapp.net" || pm.GetKey().GetID() != "3EB0A0B" || pm.GetEditedMessage().GetConversation() != "corretto" {
		t.Fatalf("edit: %+v", s.Msg)
	}
	// A bare WhatsApp id works too.
	if ok, err = e.b.EditMessage(ctx, "391234567890@c.us", "3EB0A0C", "x"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ = e.b.EditMessage(ctx, "391234567890@c.us", "", "x"); ok {
		t.Error("empty id")
	}
}

func TestSendReaction(t *testing.T) {
	e := newEnv(t)
	ok, err := e.b.SendReaction(ctx, "391234567890@c.us", "false_391234567890@c.us_3EB0A0D", "👍", "391234567890@c.us")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	rm := lastSent(t, e).Msg.GetReactionMessage()
	if rm.GetText() != "👍" || rm.GetKey().GetID() != "3EB0A0D" || rm.GetKey().GetFromMe() {
		t.Fatalf("reaction: %+v", rm)
	}
	// Removing: empty text.
	_, _ = e.b.SendReaction(ctx, "391234567890@c.us", "true_391234567890@c.us_3EB0A0E", "", "")
	rm = lastSent(t, e).Msg.GetReactionMessage()
	if rm.GetText() != "" || !rm.GetKey().GetFromMe() {
		t.Errorf("removal of own-message reaction: %+v", rm)
	}
	// Group: the sender of the reacted message is required.
	_, err = e.b.SendReaction(ctx, "120363000000000000@g.us", "false_120363000000000000@g.us_3EB0A0F_2050@lid", "❤️", "")
	if err != nil {
		t.Fatal(err)
	}
	if k := lastSent(t, e).Msg.GetReactionMessage().GetKey(); k.GetParticipant() != "2050@lid" {
		t.Errorf("group reaction key: %+v", k)
	}
	if ok, _ = e.b.SendReaction(ctx, "120363000000000000@g.us", "3EB0A0F", "❤️", ""); ok {
		t.Error("a group reaction on a bare id of an unknown message cannot name the sender")
	}
}

func TestArchiveCheckNumberAndTyping(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{}))
	seed(e.b, "391234567890@c.us", &entry{ID: "false_391234567890@c.us_AA", Timestamp: 99, Text: "x"})
	drain(e.b)
	ok, err := e.b.SetChatArchived(ctx, "391234567890@c.us", true)
	if err != nil || !ok || len(e.fc.appStates) != 1 {
		t.Fatalf("archive: ok=%v err=%v patches=%d", ok, err, len(e.fc.appStates))
	}
	if evs := drain(e.b); len(evs) != 1 || evs[0].Type != proto.EventContactUpdate {
		t.Errorf("archive should notify: %+v", evs)
	}

	e.fc.onWhats["+393331112222"] = true
	if ok, _ := e.b.CheckNumberExists(ctx, "393331112222"); !ok {
		t.Error("existing number")
	}
	if ok, _ := e.b.CheckNumberExists(ctx, "+39 000"); ok {
		t.Error("unknown number")
	}
	if ok, _ := e.b.CheckNumberExists(ctx, "abc"); ok {
		t.Error("no digits")
	}

	_ = e.b.SendTyping(ctx, "391234567890@c.us", true)
	_ = e.b.SendTyping(ctx, "391234567890@c.us", false)
	if len(e.fc.chatPres) != 2 || e.fc.chatPres[0] != types.ChatPresenceComposing || e.fc.chatPres[1] != types.ChatPresencePaused {
		t.Errorf("typing: %v", e.fc.chatPres)
	}
}

func TestProfilePictureURL(t *testing.T) {
	e := newEnv(t)
	e.fc.picture = &types.ProfilePictureInfo{URL: "https://pps.whatsapp.net/x.jpg"}
	url, noPhoto, err := e.b.ProfilePictureURL(ctx, "391234567890@c.us")
	if err != nil || url != "https://pps.whatsapp.net/x.jpg" || noPhoto {
		t.Fatal(url, noPhoto, err)
	}
	e.fc.picture, e.fc.pictureEr = nil, errNotSet()
	url, noPhoto, err = e.b.ProfilePictureURL(ctx, "391234567890@c.us")
	if err != nil || url != "" || !noPhoto {
		t.Fatalf("no picture must earn a negative marker: %q %v %v", url, noPhoto, err)
	}
	e.fc.pictureEr = errors.New("net down")
	if _, noPhoto, err = e.b.ProfilePictureURL(ctx, "391234567890@c.us"); err == nil || noPhoto {
		t.Error("transport error must not be a negative marker")
	}
}

var _ = events.Message{}
var _ = waE2E.Message{}
