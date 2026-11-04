package whatsapp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

var baseTime = time.Unix(1_700_000_000, 0)

func str(s string) *string { return gproto.String(s) }

func dmEvent(user, server, id string, fromMe bool, msg *waE2E.Message) *events.Message {
	chat := types.NewJID(user, server)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: fromMe},
			ID:            types.MessageID(id), Timestamp: baseTime, PushName: "",
		},
		Message: msg,
	}
}

func groupEvent(group, participant, id string, fromMe bool, push string, msg *waE2E.Message) *events.Message {
	chat := types.NewJID(group, types.GroupServer)
	sender, _ := types.ParseJID(participant)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: fromMe, IsGroup: true},
			ID:            types.MessageID(id), Timestamp: baseTime, PushName: push,
		},
		Message: msg,
	}
}

func text(t string) *waE2E.Message { return &waE2E.Message{Conversation: str(t)} }

func one(t *testing.T, evs []proto.Event) proto.Event {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(evs), evs)
	}
	return evs[0]
}

func TestIncomingDMText(t *testing.T) {
	e := newEnv(t)
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0ABC", false, text("Ciao")), false))
	if ev.Type != proto.EventMessage || ev.Protocol != "whatsapp" || ev.ContactID != "391234567890@c.us" {
		t.Fatalf("event header: %+v", ev)
	}
	want := map[string]any{
		"id": "false_391234567890@c.us_3EB0ABC", "text": "Ciao", "is_mine": false, "sender": "391234567890@c.us",
		"is_group": false, "timestamp": int64(1_700_000_000_000), "quote_text": nil, "quote_timestamp": nil,
		"quote_author": nil, "reply_to_message_id": nil, "msg_type": "text", "attachment_info": nil,
		"attachment_id": nil, "media_kind": nil, "content_type": nil, "ack": nil, "contact": nil,
	}
	if len(ev.Payload) != len(want) {
		t.Errorf("payload has %d keys, want %d: %v", len(ev.Payload), len(want), ev.Payload)
	}
	for k, w := range want {
		if got, ok := ev.Payload[k]; !ok || got != w {
			t.Errorf("payload[%q] = %#v, want %#v", k, got, w)
		}
	}
}

func TestPushNameBecomesSender(t *testing.T) {
	e := newEnv(t)
	ev := dmEvent("391234567890", types.DefaultUserServer, "A1", false, text("hi"))
	ev.Info.PushName = "Mario"
	got := one(t, e.b.messageEvents(ev, false))
	if got.Payload["sender"] != "Mario" {
		t.Errorf("sender = %v", got.Payload["sender"])
	}
}

func TestOutgoingFromAnotherDevice(t *testing.T) {
	e := newEnv(t)
	// fromMe: the chat is the recipient, never our own jid.
	ev := dmEvent("391234567890", types.DefaultUserServer, "3EB0DEF", true, text("ciao"))
	ev.Info.Sender = types.NewJID("390000000000", types.DefaultUserServer)
	ev.Info.PushName = "Io"
	got := one(t, e.b.messageEvents(ev, false))
	if got.ContactID != "391234567890@c.us" || got.Payload["is_mine"] != true || got.Payload["sender"] != "You" {
		t.Fatalf("outgoing event: %+v", got)
	}
	if got.Payload["id"] != "true_391234567890@c.us_3EB0DEF" {
		t.Errorf("id = %v", got.Payload["id"])
	}
}

func TestLIDChatKeepsLIDContactID(t *testing.T) {
	e := newEnv(t)
	got := one(t, e.b.messageEvents(dmEvent("189025889575055", types.HiddenUserServer, "3A268CF00E4ECCEA4474", false, text("x")), false))
	if got.ContactID != "189025889575055@lid" || got.Payload["id"] != "false_189025889575055@lid_3A268CF00E4ECCEA4474" {
		t.Fatalf("lid event: %+v", got)
	}
}

func TestGroupMessage(t *testing.T) {
	e := newEnv(t)
	ev := groupEvent("120363000000000000", "20508603076702@lid", "3EB0ABC", false, "Luigi", text("yo"))
	got := one(t, e.b.messageEvents(ev, false))
	if got.ContactID != "120363000000000000@g.us" || got.Payload["is_group"] != true || got.Payload["sender"] != "Luigi" {
		t.Fatalf("group event: %+v", got)
	}
	if got.Payload["id"] != "false_120363000000000000@g.us_3EB0ABC_20508603076702@lid" {
		t.Errorf("id = %v", got.Payload["id"])
	}
	// No push name: the sender is the participant jid, resolved to a contact name when known.
	ev = groupEvent("120363000000000000", "20508603076702@lid", "3EB0ABD", false, "", text("yo"))
	got = one(t, e.b.messageEvents(ev, false))
	if got.Payload["sender"] != "20508603076702@lid" {
		t.Errorf("sender fallback = %v", got.Payload["sender"])
	}
	e.b.RegisterContact(*newContact("20508603076702@lid", "Luigi Verdi", nil))
	got = one(t, e.b.messageEvents(ev, false))
	if got.Payload["sender"] != "Luigi Verdi" {
		t.Errorf("resolved sender = %v", got.Payload["sender"])
	}
}

func TestOwnGroupMessageID(t *testing.T) {
	e := newEnv(t)
	ev := groupEvent("123456789", "555555@lid", "3EB0D9A373425F46E2A5F6", true, "", text("mine"))
	got := one(t, e.b.messageEvents(ev, false))
	if got.Payload["id"] != "true_123456789@g.us_3EB0D9A373425F46E2A5F6_555555@lid" || got.Payload["sender"] != "You" {
		t.Fatalf("own group message: %+v", got.Payload)
	}
}

func TestImageWithCaption(t *testing.T) {
	e := newEnv(t)
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: str("image/jpeg"), Caption: str("Guarda"), DirectPath: str("/img1")}}
	got := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0ABC", false, msg), false))
	p := got.Payload
	if p["msg_type"] != "image" || p["text"] != "" || p["attachment_info"] != "Guarda" || p["media_kind"] != "image" ||
		p["content_type"] != "image/jpeg" || p["attachment_id"] != "false_391234567890@c.us_3EB0ABC.jpeg" {
		t.Fatalf("image payload: %+v", p)
	}
	if mp, ok := e.b.lookupMedia("false_391234567890@c.us_3EB0ABC"); !ok || mp.Kind != "image" {
		t.Error("media info should be remembered for the lazy download")
	}
}

func TestImageWithoutCaptionFallsBackToMime(t *testing.T) {
	e := newEnv(t)
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: str("image/png")}}
	got := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "A1B2", false, msg), false))
	if got.Payload["attachment_info"] != "image/png" || got.Payload["text"] != "" {
		t.Fatalf("payload: %+v", got.Payload)
	}
}

func TestDocumentKeepsFilenameAndCaptionInText(t *testing.T) {
	e := newEnv(t)
	msg := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		Mimetype: str("application/pdf"), FileName: str("fattura.pdf"), Caption: str("ecco"), DirectPath: str("/d")}}
	got := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "D1", false, msg), false))
	p := got.Payload
	if p["msg_type"] != "attachment" || p["attachment_info"] != "fattura.pdf" || p["text"] != "ecco" ||
		p["media_kind"] != "document" || p["attachment_id"] != "false_391234567890@c.us_D1.pdf" {
		t.Fatalf("document payload: %+v", p)
	}
}

func TestVoiceNoteVideoGifSticker(t *testing.T) {
	e := newEnv(t)
	voice := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: str("audio/ogg; codecs=opus"), PTT: gproto.Bool(true)}}
	p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "V1", false, voice), false)).Payload
	if p["media_kind"] != "voice" || p["msg_type"] != "attachment" || p["attachment_info"] != nil || p["attachment_id"] != "false_39@c.us_V1.oga" {
		t.Errorf("voice payload: %+v", p)
	}
	audio := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: str("audio/mpeg")}}
	if p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "V2", false, audio), false)).Payload; p["media_kind"] != "audio" {
		t.Errorf("audio kind: %v", p["media_kind"])
	}
	gif := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: str("video/mp4"), GifPlayback: gproto.Bool(true)}}
	if p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "G1", false, gif), false)).Payload; p["media_kind"] != "gif" || p["msg_type"] != "image" {
		t.Errorf("gif payload: %+v", p)
	}
	video := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: str("video/mp4"), Caption: str("clip")}}
	if p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "G2", false, video), false)).Payload; p["media_kind"] != "video" || p["text"] != "clip" || p["msg_type"] != "attachment" {
		t.Errorf("video payload: %+v", p)
	}
	sticker := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: str("image/webp")}}
	if p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "S1", false, sticker), false)).Payload; p["media_kind"] != "sticker" || p["msg_type"] != "sticker" {
		t.Errorf("sticker payload: %+v", p)
	}
}

func TestReplyBuildsWAHAReplyID(t *testing.T) {
	e := newEnv(t)
	quoted := &waE2E.Message{Conversation: str("originale")}
	ci := &waE2E.ContextInfo{StanzaID: str("3EB0A01"), Participant: str("391234567890@s.whatsapp.net"), QuotedMessage: quoted}
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: str("risposta"), ContextInfo: ci}}
	p := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "R1", false, msg), false)).Payload
	if p["text"] != "risposta" || p["quote_text"] != "originale" || p["quote_author"] != "391234567890@c.us" ||
		p["reply_to_message_id"] != "false_391234567890@c.us_3EB0A01" || p["quote_timestamp"] != nil {
		t.Fatalf("reply payload: %+v", p)
	}
	// Replying to one of OUR messages: the quoted id is a "true_" id.
	ci2 := &waE2E.ContextInfo{StanzaID: str("3EB0A02"), Participant: str("390000000000@s.whatsapp.net"), QuotedMessage: quoted}
	msg2 := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: str("ok"), ContextInfo: ci2}}
	p = one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "R2", false, msg2), false)).Payload
	if p["reply_to_message_id"] != "true_391234567890@c.us_3EB0A02" {
		t.Errorf("reply to own message: %v", p["reply_to_message_id"])
	}
}

func TestReplyUsesCachedOriginal(t *testing.T) {
	e := newEnv(t)
	// The original is cached under its stored id (participant form differs).
	e.b.ingestMu.Lock()
	e.b.cache["120363000000000000@g.us"] = []*entry{{ID: "false_120363000000000000@g.us_3EB0A01_20508603076702@lid", Text: "orig", Timestamp: 1234}}
	e.b.ingestMu.Unlock()
	ci := &waE2E.ContextInfo{StanzaID: str("3EB0A01"), Participant: str("20508603076702@lid"), QuotedMessage: text("orig")}
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: str("re"), ContextInfo: ci}}
	p := one(t, e.b.messageEvents(groupEvent("120363000000000000", "99@lid", "R9", false, "X", msg), false)).Payload
	if p["reply_to_message_id"] != "false_120363000000000000@g.us_3EB0A01_20508603076702@lid" || p["quote_timestamp"] != int64(1234) {
		t.Fatalf("cached reply: %+v", p)
	}
}

func TestMediaQuotePlaceholders(t *testing.T) {
	cases := []struct {
		quoted *waE2E.Message
		want   string
	}{
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, "🖼️ Immagine"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: str("bella foto")}}, "bella foto"},
		{&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: str("a.pdf")}}, "a.pdf"},
		{&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{}}, "📎 File"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "🎵 Audio"},
		{&waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}, "🎬 Video"},
		{&waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, "🎨 Sticker"},
		{&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: str("  testo  ")}}, "testo"},
	}
	for _, c := range cases {
		got, ok := quoteText(c.quoted)
		if !ok || got != c.want {
			t.Errorf("quoteText = %q (%v), want %q", got, ok, c.want)
		}
	}
	if _, ok := quoteText(&waE2E.Message{}); ok {
		t.Error("an empty quote has no text")
	}
}

func TestMentionsResolveToNames(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(*newContact("20508603076702@lid", "Anna", nil))
	got := e.b.resolveTextMentions("ciao @20508603076702 come va")
	if got != "ciao @Anna come va" {
		t.Errorf("mention by name = %q", got)
	}
	e.lids.pn["77777777"] = "393331112222"
	if got := e.b.resolveTextMentions("hey @77777777!"); got != "hey @+393331112222!" {
		t.Errorf("mention by lid cache = %q", got)
	}
	// Unknown ids and over-long digit runs are untouched.
	if got := e.b.resolveTextMentions("x @99999999 y"); got != "x @99999999 y" {
		t.Errorf("unknown mention = %q", got)
	}
	if got := e.b.resolveTextMentions("x @123456789012345678901 y"); got != "x @123456789012345678901 y" {
		t.Errorf("long run = %q", got)
	}
	if got := e.b.resolveTextMentions("mail a@b.c"); got != "mail a@b.c" {
		t.Errorf("email = %q", got)
	}
}

func TestLocationMessage(t *testing.T) {
	e := newEnv(t)
	thumb := append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, 50)...)
	msg := &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude: gproto.Float64(45.4642), DegreesLongitude: gproto.Float64(9.19),
		Name: str("Torre"), Address: str("Piazza del Duomo"), JPEGThumbnail: thumb}}
	p := one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "L1", false, msg), false)).Payload
	want := "📍 Posizione: Torre\nPiazza del Duomo\nhttps://maps.google.com/?q=45.464200,9.190000"
	if p["text"] != want || p["msg_type"] != "text" {
		t.Fatalf("location payload: %q", p["text"])
	}
	tp := filepath.Join(e.b.thumbsDir, "45.464200_9.190000.jpg")
	if data, err := os.ReadFile(tp); err != nil || len(data) != len(thumb) {
		t.Errorf("thumbnail not stored: %v", err)
	}
	live := &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{
		DegreesLatitude: gproto.Float64(1), DegreesLongitude: gproto.Float64(2)}}
	p = one(t, e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "L2", false, live), false)).Payload
	if p["text"] != "📍 Posizione in tempo reale\nhttps://maps.google.com/?q=1.000000,2.000000" {
		t.Errorf("live location: %q", p["text"])
	}
}

func TestDroppedMessages(t *testing.T) {
	e := newEnv(t)
	if evs := e.b.messageEvents(dmEvent("status", types.BroadcastServer, "X", false, text("story")), false); len(evs) != 0 {
		t.Error("status@broadcast must be ignored")
	}
	if evs := e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "X", false, text("   ")), false); len(evs) != 0 {
		t.Error("an empty bubble must never be created")
	}
	revoke := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum()}}
	if evs := e.b.messageEvents(dmEvent("39", types.DefaultUserServer, "X", false, revoke), false); len(evs) != 0 {
		t.Error("protocol messages produce no bubble")
	}
	if evs := e.b.messageEvents(&events.Message{}, false); len(evs) != 0 {
		t.Error("nil message")
	}
}

func TestEditEvent(t *testing.T) {
	e := newEnv(t)
	e.b.ingestMu.Lock()
	e.b.cache["391234567890@c.us"] = []*entry{{ID: "false_391234567890@c.us_3EB0A01", Text: "vecchio", Timestamp: 5000}}
	e.b.ingestMu.Unlock()
	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key:           &waCommon.MessageKey{ID: str("3EB0A01"), FromMe: gproto.Bool(false)},
		EditedMessage: text("nuovo"), TimestampMS: gproto.Int64(1_700_000_100_000)}}
	ev := dmEvent("391234567890", types.DefaultUserServer, "EDITID", false, edit)
	ev.IsEdit = true
	got := one(t, e.b.messageEvents(ev, false))
	if got.Type != proto.EventMessageEdit || got.ContactID != "391234567890@c.us" {
		t.Fatalf("edit event: %+v", got)
	}
	p := got.Payload
	if p["edit_message_id"] != "false_391234567890@c.us_3EB0A01" || p["text"] != "nuovo" || p["timestamp"] != int64(5000) ||
		p["edit_timestamp"] != int64(1_700_000_100_000) || p["is_mine"] != false || p["msg_type"] != "text" {
		t.Fatalf("edit payload: %+v", p)
	}
	// An edit of a message we do not have still maps to a WAHA id.
	edit.ProtocolMessage.Key.ID = str("UNKNOWN1")
	got = one(t, e.b.messageEvents(ev, false))
	if got.Payload["edit_message_id"] != "false_391234567890@c.us_UNKNOWN1" {
		t.Errorf("uncached edit id = %v", got.Payload["edit_message_id"])
	}
}

func TestReactionEvent(t *testing.T) {
	e := newEnv(t)
	react := func(emoji string, fromMe bool) *waE2E.Message {
		return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
			Key:  &waCommon.MessageKey{ID: str("3EB0A03"), FromMe: gproto.Bool(fromMe)},
			Text: str(emoji), SenderTimestampMS: gproto.Int64(1_700_000_050_000)}}
	}
	got := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "RX1", false, react("👍", true)), false))
	p := got.Payload
	if got.Type != proto.EventReactionUpdate || p["mode"] != "delta" || p["emoji"] != "👍" || p["is_remove"] != false ||
		p["target_message_id"] != "true_391234567890@c.us_3EB0A03" || p["author_key"] != "391234567890@c.us" ||
		p["is_mine"] != false || p["timestamp"] != int64(1_700_000_050_000) || p["target_timestamp"] != nil {
		t.Fatalf("reaction payload: %+v", p)
	}
	// Removal, from ourselves: author is always "me".
	ev := dmEvent("391234567890", types.DefaultUserServer, "RX2", true, react("", false))
	got = one(t, e.b.messageEvents(ev, false))
	p = got.Payload
	if p["is_remove"] != true || p["author_key"] != "me" || p["author"] != "You" || p["is_mine"] != true ||
		p["target_message_id"] != "false_391234567890@c.us_3EB0A03" {
		t.Fatalf("own removal: %+v", p)
	}
	// In a group the target carries the participant.
	gr := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key:  &waCommon.MessageKey{ID: str("3EB0G"), FromMe: gproto.Bool(false), Participant: str("20508603076702@lid")},
		Text: str("❤️")}}
	got = one(t, e.b.messageEvents(groupEvent("123", "99@lid", "RX3", false, "P", gr), false))
	if got.Payload["target_message_id"] != "false_123@g.us_3EB0G_20508603076702@lid" || got.Payload["author_key"] != "99@lid" {
		t.Errorf("group reaction: %+v", got.Payload)
	}
}

func TestReceiptMapping(t *testing.T) {
	e := newEnv(t)
	chat := types.NewJID("391234567890", types.DefaultUserServer)
	mk := func(typ types.ReceiptType, fromMe bool, ids ...string) *events.Receipt {
		r := &events.Receipt{Type: typ, Timestamp: baseTime}
		r.Chat, r.IsFromMe = chat, fromMe
		for _, id := range ids {
			r.MessageIDs = append(r.MessageIDs, types.MessageID(id))
		}
		return r
	}
	e.b.onReceipt(mk(types.ReceiptTypeDelivered, false, "3eb0abc"))
	e.b.onReceipt(mk(types.ReceiptTypeRead, false, "3EB0ABC", "3EB0DEF"))
	e.b.onReceipt(mk(types.ReceiptTypePlayed, false, "3EB0A04"))
	e.b.onReceipt(mk(types.ReceiptTypeRetry, false, "X"))
	e.b.onReceipt(mk(types.ReceiptTypeSender, false, "X"))
	evs := drain(e.b)
	if len(evs) != 3 {
		t.Fatalf("want 3 receipt events, got %d: %+v", len(evs), evs)
	}
	check := func(ev proto.Event, ids []any, read bool) {
		t.Helper()
		if ev.Type != proto.EventReceipt || ev.ContactID != "391234567890@c.us" || ev.Payload["is_read"] != read {
			t.Fatalf("receipt header: %+v", ev)
		}
		got := ev.Payload["message_ids"].([]any)
		if len(got) != len(ids) {
			t.Fatalf("ids = %v, want %v", got, ids)
		}
		for i := range ids {
			if got[i] != ids[i] {
				t.Errorf("ids = %v, want %v", got, ids)
			}
		}
	}
	check(evs[0], []any{"3EB0ABC"}, false)
	check(evs[1], []any{"3EB0ABC", "3EB0DEF"}, true)
	check(evs[2], []any{"3EB0A04"}, true)
	// Our other device reading a chat is not a delivery receipt.
	e.b.onReceipt(mk(types.ReceiptTypeReadSelf, true, "IN1"))
	if evs := drain(e.b); len(evs) != 0 {
		t.Errorf("read-self must not produce a receipt event: %+v", evs)
	}
	if !e.b.readOnPhone["391234567890@c.us"] {
		t.Error("read-self should flag the chat as read on the phone")
	}
}

func TestChatPresenceMapping(t *testing.T) {
	e := newEnv(t)
	cp := func(s types.ChatPresence) *events.ChatPresence {
		c := &events.ChatPresence{State: s}
		c.Chat = types.NewJID("391234567890", types.DefaultUserServer)
		return c
	}
	e.b.onChatPresence(cp(types.ChatPresenceComposing))
	e.b.onChatPresence(cp(types.ChatPresencePaused))
	e.b.onChatPresence(cp("something-else"))
	evs := drain(e.b)
	if len(evs) != 2 || evs[0].Type != proto.EventTyping || evs[0].Payload["action"] != "STARTED" || evs[1].Payload["action"] != "STOPPED" ||
		evs[0].ContactID != "391234567890@c.us" {
		t.Fatalf("typing events: %+v", evs)
	}
}

func TestIdentityChangeNotice(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", nil))
	e.b.onIdentityChange(&events.IdentityChange{JID: types.NewJID("391234567890", types.DefaultUserServer), Timestamp: baseTime})
	ev := one(t, drain(e.b))
	if ev.Payload["msg_type"] != "system" || ev.Payload["text"] != "Il tuo codice di sicurezza con Mario è cambiato." ||
		ev.Payload["is_mine"] != false || ev.Payload["sender"] != "" {
		t.Fatalf("notice: %+v", ev.Payload)
	}
	if got := e.b.securityNoticeText("999@c.us"); got != "Il tuo codice di sicurezza con +999 è cambiato." {
		t.Errorf("phone fallback = %q", got)
	}
	if got := e.b.securityNoticeText("1234@lid"); got != "Il codice di sicurezza di un contatto è cambiato." {
		t.Errorf("unknown lid = %q", got)
	}
	e.b.onIdentityChange(&events.IdentityChange{JID: types.NewJID("391234567890", types.DefaultUserServer), Implicit: true})
	if len(drain(e.b)) != 0 {
		t.Error("implicit identity changes are silent")
	}
}

func TestOnMessageDedup(t *testing.T) {
	e := newEnv(t)
	ev := dmEvent("391234567890", types.DefaultUserServer, "DUP1", false, text("ciao"))
	e.b.handleEvent(ev)
	e.b.handleEvent(ev) // a redelivery
	if evs := drain(e.b); len(evs) != 1 {
		t.Fatalf("redelivered event must be queued once, got %d", len(evs))
	}
}

func TestArchiveEventUpdatesContact(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{}))
	drain(e.b)
	e.b.handleEvent(&events.Archive{JID: types.NewJID("391234567890", types.DefaultUserServer),
		Action: &waSyncAction.ArchiveChatAction{Archived: gproto.Bool(true)}})
	ev := one(t, drain(e.b))
	if ev.Type != proto.EventContactUpdate || ev.Payload["display_name"] != "Mario" {
		t.Fatalf("archive event: %+v", ev)
	}
	c, _ := e.b.FindContact("391234567890@c.us")
	if c.Extras["archived"] != true {
		t.Error("contact should be flagged archived")
	}
	// Idempotent: no second event.
	e.b.handleEvent(&events.Archive{JID: types.NewJID("391234567890", types.DefaultUserServer),
		Action: &waSyncAction.ArchiveChatAction{Archived: gproto.Bool(true)}})
	if len(drain(e.b)) != 0 {
		t.Error("archiving twice must not republish")
	}
}

// A message of the @lid chat that Python cached under the @c.us twin: edits and
// reactions must be addressed to the key where the message lives, because the
// core applies them under the event's contact id.
func TestEditAndReactionUseTheTwinStorageKey(t *testing.T) {
	e := newEnv(t)
	e.lids.pn["189025889575055"] = "391234567890"
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{"phone": "391234567890", "lid": "189025889575055@lid"}))
	drain(e.b)
	e.b.ingestMu.Lock()
	e.b.cache["391234567890@c.us"] = []*entry{{ID: "false_391234567890@c.us_3EB0A01", Text: "vecchio", Timestamp: 5000}}
	e.b.ingestMu.Unlock()

	edit := dmEvent("189025889575055", types.HiddenUserServer, "ED", false, editMsg("3EB0A01", "nuovo"))
	edit.IsEdit = true
	got := one(t, e.b.messageEvents(edit, false))
	if got.Type != proto.EventMessageEdit || got.ContactID != "391234567890@c.us" || got.Payload["edit_message_id"] != "false_391234567890@c.us_3EB0A01" {
		t.Fatalf("edit routed to the twin: %+v", got)
	}
	res, err := e.b.ApplyEdit(got.ContactID, got.Payload["edit_message_id"].(string), "nuovo", proto.ApplyEditOptions{MarkEdited: true})
	if err != nil || res == nil || res.OldText != "vecchio" {
		t.Fatalf("apply edit: %+v %v", res, err)
	}

	rx := dmEvent("189025889575055", types.HiddenUserServer, "RX", false, reactionMsg("3EB0A01", false, "👍"))
	got = one(t, e.b.messageEvents(rx, false))
	if got.ContactID != "391234567890@c.us" || got.Payload["target_message_id"] != "false_391234567890@c.us_3EB0A01" {
		t.Fatalf("reaction routed to the twin: %+v", got)
	}
}
