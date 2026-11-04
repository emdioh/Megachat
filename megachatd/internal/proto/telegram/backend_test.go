package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"github.com/gotd/td/tg"
)

var bg = context.Background()

func textMsg(id int, peer tg.PeerClass, text string, out bool) *tg.Message {
	return &tg.Message{ID: id, PeerID: peer, Message: text, Out: out, Date: 1700000000 + id}
}

func TestMessageToEventTextAndReply(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.b.ingestLocked(bg, "7", inMsg{ID: "4", Text: "orig", MsgType: "text"}, 1, false)
	msg := textMsg(5, &tg.PeerUser{UserID: 7}, "hi", false)
	msg.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: 4}
	msg.ReplyTo.(*tg.MessageReplyHeader).SetReplyToMsgID(4)
	ents := entityMaps{users: map[int64]*tg.User{7: {ID: 7, FirstName: "Bob", LastName: "B"}}}
	ev := e.b.messageToEvent(msg, ents, "")
	if ev == nil || ev.ContactID != "7" || ev.Type != proto.EventMessage {
		t.Fatalf("%+v", ev)
	}
	p := ev.Payload
	if p["id"] != "5" || p["text"] != "hi" || p["is_mine"] != false || p["sender"] != "Bob B" ||
		p["timestamp"] != int64(1700000005)*1000 || p["quote_text"] != "orig" || p["reply_to_message_id"] != "4" ||
		p["msg_type"] != "text" || p["protocol"] != "telegram" {
		t.Fatalf("%v", p)
	}
	if c, ok := p["contact"].(proto.Contact); !ok || c.DisplayName != "Bob" {
		t.Fatalf("contact %v", p["contact"])
	}
}

func TestMessageToEventMedia(t *testing.T) {
	e := newEnv(t)
	photo := textMsg(9, &tg.PeerUser{UserID: 7}, "", false)
	photo.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}
	p := e.b.messageToEvent(photo, entityMaps{}, "").Payload
	if p["msg_type"] != "image" || p["text"] != "" || p["attachment_info"] != "Photo" || p["attachment_id"] != "tgref:7:9" ||
		p["media_kind"] != "image" || p["content_type"] != "image/jpeg" {
		t.Fatalf("%v", p)
	}
	// A caption becomes the attachment info; a downloaded path wins over tgref.
	photo.Message = "sunset"
	p = e.b.messageToEvent(photo, entityMaps{}, "/m/p.jpg").Payload
	if p["attachment_info"] != "sunset" || p["attachment_id"] != "/m/p.jpg" || p["text"] != "" {
		t.Fatalf("%v", p)
	}
	doc := textMsg(10, &tg.PeerChat{ChatID: 3}, "", true)
	doc.Media = &tg.MessageMediaDocument{Document: &tg.Document{MimeType: "application/pdf",
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "r.pdf"}}}}
	ev := e.b.messageToEvent(doc, entityMaps{}, "")
	p = ev.Payload
	if ev.ContactID != "-3" || p["msg_type"] != "attachment" || p["attachment_info"] != "📎 r.pdf" ||
		p["media_kind"] != "document" || p["content_type"] != "application/pdf" || p["is_mine"] != true {
		t.Fatalf("%v", p)
	}
	if e.b.messageToEvent(textMsg(11, &tg.PeerUser{UserID: 7}, "", false), entityMaps{}, "") != nil {
		t.Fatal("empty message must be dropped")
	}
	// Reply-only (no text, no media) is kept.
	r := textMsg(12, &tg.PeerUser{UserID: 7}, "", false)
	r.ReplyTo = &tg.MessageReplyHeader{}
	if e.b.messageToEvent(r, entityMaps{}, "") == nil {
		t.Fatal("reply-only message dropped")
	}
}

func TestMessageToEventSenderConventions(t *testing.T) {
	e := newEnv(t)
	// Outgoing private message: the sender is the account itself.
	if got := e.b.messageToEvent(textMsg(1, &tg.PeerUser{UserID: 7}, "x", true), entityMaps{}, "").Payload["sender"]; got != "Me" {
		t.Fatalf("%v", got)
	}
	// Channel post: the channel's raw id.
	ents := entityMaps{channels: map[int64]*tg.Channel{3: {ID: 3}}}
	if got := e.b.messageToEvent(textMsg(1, &tg.PeerChannel{ChannelID: 3}, "x", false), ents, "").Payload["sender"]; got != "3" {
		t.Fatalf("%v", got)
	}
	// Unresolved sender stays empty.
	if got := e.b.messageToEvent(textMsg(1, &tg.PeerUser{UserID: 7}, "x", false), entityMaps{}, "").Payload["sender"]; got != "" {
		t.Fatalf("%v", got)
	}
}

func TestIngestDedupAndEchoUpgrade(t *testing.T) {
	e := newEnv(t)
	msg := map[string]any{"id": "10", "text": "hello", "is_mine": false, "sender": "Bob", "msg_type": "text"}
	r, err := e.b.IngestMessage(bg, "7", msg, 1000)
	if err != nil || r != proto.IngestAdded {
		t.Fatalf("%v %v", r, err)
	}
	if r, _ := e.b.IngestMessage(bg, "7", msg, 1000); r != proto.IngestUnchanged {
		t.Fatalf("duplicate: %v", r)
	}
	rows, _ := e.st.LoadCache(bg, "telegram")
	if len(rows["7"]) != 1 || store.Deref(rows["7"][0].MsgID) != "10" || rows["7"][0].Protocol != "telegram" {
		t.Fatalf("%+v", rows)
	}
	// An id-less optimistic row gets the server id from its echo.
	if r, _ := e.b.IngestMessage(bg, "7", map[string]any{"text": "mine", "is_mine": true, "msg_type": "text"}, 5000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	echo := map[string]any{"id": "11", "text": "mine", "is_mine": true, "msg_type": "text"}
	if r, _ := e.b.IngestMessage(bg, "7", echo, 5800); r != proto.IngestUnchanged {
		t.Fatalf("echo: %v", r)
	}
	rows, _ = e.st.LoadCache(bg, "telegram")
	found := false
	for _, m := range rows["7"] {
		if m.IsMine && store.Deref(m.MsgID) == "11" && m.Timestamp == 5000 {
			found = true
		}
	}
	if !found || len(rows["7"]) != 2 {
		t.Fatalf("echo must attach the id and keep the optimistic ts: %+v", rows["7"])
	}
	// Same text within the dedup window and no id: fallback dedup.
	if r, _ := e.b.IngestMessage(bg, "7", map[string]any{"text": "hello", "msg_type": "text"}, 1500); r != proto.IngestUnchanged {
		t.Fatalf("fallback: %v", r)
	}
}

func TestIngestImageClearsTextAndHealsCaption(t *testing.T) {
	e := newEnv(t)
	img := map[string]any{"id": "20", "text": "ignored", "msg_type": "image", "attachment_info": "Photo", "attachment_id": "tgref:7:20"}
	e.b.IngestMessage(bg, "7", img, 1)
	img["attachment_info"] = "a nice caption"
	r, _ := e.b.IngestMessage(bg, "7", img, 1)
	if r != proto.IngestChanged {
		t.Fatalf("caption heal: %v", r)
	}
	if got, _ := e.b.cachedEntry("7", "20"); got.Text != "" || got.AttachmentInfo != "a nice caption" {
		t.Fatalf("%+v", got)
	}
}

func TestIngestEchoUpgradesOutgoingTgrefToLocalFile(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(bg, "7", map[string]any{"id": "30", "is_mine": true, "msg_type": "image", "attachment_id": "tgref:7:30"}, 1)
	local := filepath.Join(e.dir, "sent.png")
	os.WriteFile(local, []byte("x"), 0o644)
	r, _ := e.b.IngestMessage(bg, "7", map[string]any{"id": "30", "is_mine": true, "msg_type": "image", "attachment_id": local}, 1)
	if r != proto.IngestChanged {
		t.Fatalf("%v", r)
	}
	rows, _ := e.st.LoadCache(bg, "telegram")
	if store.Deref(rows["7"][0].AttachmentID) != local {
		t.Fatalf("%+v", rows["7"][0])
	}
	// An incoming message never upgrades.
	if shouldUpgradeOutgoingAttachment(false, "tgref:1:1", local) {
		t.Fatal("incoming upgraded")
	}
}

func TestIngestBoundsCache(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= maxCachePerContact+10; i++ {
		e.b.IngestMessage(bg, "7", map[string]any{"id": strconv.Itoa(i), "text": "m" + strconv.Itoa(i), "msg_type": "text"}, int64(i)*10000)
	}
	e.b.ingestMu.Lock()
	n := len(e.b.cache["7"])
	first := e.b.cache["7"][0].ID
	e.b.ingestMu.Unlock()
	if n != maxCachePerContact || first != "11" {
		t.Fatalf("n=%d first=%s", n, first)
	}
}

func TestEditFlow(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(bg, "7", map[string]any{"id": "40", "text": "old", "is_mine": true, "msg_type": "text"}, 100)
	mine := true
	res, _ := e.b.ApplyEdit("7", "40", "new", proto.ApplyEditOptions{IsMine: &mine, MarkEdited: true})
	if res == nil || res.OldText != "old" || res.Text != "new" || res.Timestamp != 100 || !res.IsMine {
		t.Fatalf("%+v", res)
	}
	rows, _ := e.st.LoadCache(bg, "telegram")
	if store.Deref(rows["7"][0].Text) != "new" || !rows["7"][0].Edited {
		t.Fatalf("%+v", rows["7"][0])
	}
	if r, _ := e.b.ApplyEdit("7", "40", "new", proto.ApplyEditOptions{MarkEdited: true}); r != nil {
		t.Fatal("echo of own edit must be a no-op")
	}
	other := false
	if r, _ := e.b.ApplyEdit("7", "40", "x", proto.ApplyEditOptions{IsMine: &other, MarkEdited: true}); r != nil {
		t.Fatal("is_mine mismatch applied")
	}
	if r, _ := e.b.ApplyEdit("7", "999", "x", proto.ApplyEditOptions{}); r != nil {
		t.Fatal("unknown id")
	}
	// Re-ingesting a known text message with different text applies the edit.
	e.b.IngestMessage(bg, "7", map[string]any{"id": "40", "text": "newer", "is_mine": true, "msg_type": "text"}, 100)
	if got, _ := e.b.cachedEntry("7", "40"); got.Text != "newer" {
		t.Fatalf("%+v", got)
	}
	// Media messages are never edited.
	e.b.IngestMessage(bg, "7", map[string]any{"id": "41", "text": "c", "msg_type": "attachment"}, 200)
	if r, _ := e.b.ApplyEdit("7", "41", "zzz", proto.ApplyEditOptions{}); r != nil {
		t.Fatal("media edited")
	}
}

func TestHandleMessageEdited(t *testing.T) {
	e := newEnv(t)
	m := textMsg(50, &tg.PeerUser{UserID: 7}, "edited x", true)
	m.Entities = []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 7, Length: 1}}
	m.SetEditDate(1700000100)
	e.b.handleMessageEdited(m)
	evs := e.b.PollOnce()
	if len(evs) != 1 || evs[0].Type != proto.EventMessageEdit {
		t.Fatalf("%+v", evs)
	}
	p := evs[0].Payload
	if p["edit_message_id"] != "50" || p["text"] != "edited **x**" || p["timestamp"] != int64(1700000050)*1000 ||
		p["edit_timestamp"] != int64(1700000100)*1000 || p["is_mine"] != true || p["msg_type"] != "text" {
		t.Fatalf("%v", p)
	}
	media := textMsg(51, &tg.PeerUser{UserID: 7}, "cap", true)
	media.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{}}
	e.b.handleMessageEdited(media)
	e.b.handleMessageEdited(textMsg(52, &tg.PeerUser{UserID: 7}, "   ", true))
	if len(e.b.PollOnce()) != 0 {
		t.Fatal("media / blank edits must be skipped")
	}
	// No edit_date -> nil edit_timestamp.
	e.b.handleMessageEdited(textMsg(53, &tg.PeerUser{UserID: 7}, "t", false))
	if p := e.b.PollOnce()[0].Payload; p["edit_timestamp"] != nil || p["is_mine"] != false {
		t.Fatalf("%v", p)
	}
}

func TestReadReceiptsAndProcessReceipt(t *testing.T) {
	e := newEnv(t)
	for i, id := range []string{"1", "2", "3"} {
		e.b.IngestMessage(bg, "7", map[string]any{"id": id, "text": "t" + id, "is_mine": true, "msg_type": "text"}, int64(i+1)*10000)
	}
	e.b.IngestMessage(bg, "7", map[string]any{"id": "4", "text": "incoming", "is_mine": false, "msg_type": "text"}, 50000)
	e.b.IngestMessage(bg, "8", map[string]any{"id": "2", "text": "other chat", "is_mine": true, "msg_type": "text"}, 60000)

	e.b.handleReadReceipt(&tg.UpdateReadHistoryOutbox{Peer: &tg.PeerUser{UserID: 7}, MaxID: 2})
	evs := e.b.PollOnce()
	if len(evs) != 1 || evs[0].Type != proto.EventReceipt || evs[0].ContactID != "7" {
		t.Fatalf("%+v", evs)
	}
	ids := evs[0].Payload["message_ids"].([]string)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" || evs[0].Payload["is_read"] != true {
		t.Fatalf("%v", evs[0].Payload)
	}
	upd, err := e.b.ProcessReceipt(bg, "7", evs[0].Payload)
	if err != nil || len(upd) != 2 || upd[0]["status"] != "read" || upd[0]["is_mine"] != true || upd[0]["text"] != "t1" {
		t.Fatalf("%v %v", upd, err)
	}
	// Idempotent, scoped per contact (chat 8 also has id 2) and persisted.
	if again, _ := e.b.ProcessReceipt(bg, "7", evs[0].Payload); len(again) != 0 {
		t.Fatalf("not idempotent: %v", again)
	}
	if got, _ := e.b.cachedEntry("8", "2"); got.Status == "read" {
		t.Fatal("leaked to another chat")
	}
	rows, _ := e.st.LoadCache(bg, "telegram")
	st := map[string]string{}
	for _, m := range rows["7"] {
		st[store.Deref(m.MsgID)] = store.Deref(m.Status)
	}
	if st["1"] != "read" || st["2"] != "read" || st["3"] != "sent" {
		t.Fatalf("%v", st)
	}
	// "delivered" never downgrades "read".
	if d, _ := e.b.ProcessReceipt(bg, "7", map[string]any{"message_ids": []any{"1"}, "is_read": false}); len(d) != 0 {
		t.Fatalf("downgrade: %v", d)
	}
	// Peer id conventions of the receipt producer.
	for peer, want := range map[tg.PeerClass]string{&tg.PeerChat{ChatID: 5}: "-5", &tg.PeerChannel{ChannelID: 5}: "-1000000000005"} {
		e.b.IngestMessage(bg, want, map[string]any{"id": strconv.Itoa(900 + len(want)), "text": "z", "is_mine": true, "msg_type": "text"}, 1)
		e.b.handleReadReceipt(&tg.UpdateReadHistoryOutbox{Peer: peer, MaxID: 99999})
		if evs := e.b.PollOnce(); len(evs) != 1 || evs[0].ContactID != want {
			t.Fatalf("%v: %+v", peer, evs)
		}
	}
	// A receipt for an unknown chat emits nothing.
	e.b.handleReadReceipt(&tg.UpdateReadHistoryOutbox{Peer: &tg.PeerUser{UserID: 99}, MaxID: 9})
	if len(e.b.PollOnce()) != 0 {
		t.Fatal("empty receipt emitted")
	}
}

func TestTypingEvents(t *testing.T) {
	e := newEnv(t)
	e.b.handleTyping("7", &tg.SendMessageTypingAction{})
	e.b.handleTyping("7", &tg.SendMessageRecordAudioAction{})
	e.b.handleTyping("7", &tg.SendMessageCancelAction{})
	evs := e.b.PollOnce()
	if len(evs) != 3 || evs[0].Payload["action"] != "STARTED" || evs[1].Payload["action"] != "STARTED" || evs[2].Payload["action"] != "STOPPED" {
		t.Fatalf("%+v", evs)
	}
	if evs[0].Type != proto.EventTyping || evs[0].ContactID != "7" {
		t.Fatal("typing event")
	}
}

func reactionsFor(emoji string, count int) tg.MessageReactions {
	rc := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: emoji}, Count: count}
	rc.SetChosenOrder(0)
	return tg.MessageReactions{
		CanSeeList: true,
		Results:    []tg.ReactionCount{rc, {Reaction: &tg.ReactionCustomEmoji{DocumentID: 1}, Count: 2}},
		RecentReactions: []tg.MessagePeerReaction{
			{Reaction: &tg.ReactionEmoji{Emoticon: emoji}, PeerID: &tg.PeerUser{UserID: 7}},
			{Reaction: &tg.ReactionEmoji{Emoticon: emoji}, PeerID: &tg.PeerUser{UserID: 8}},
		},
	}
}

func TestReactionSnapshotAndApply(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.b.IngestMessage(bg, "7", map[string]any{"id": "60", "text": "react to me", "is_mine": true, "msg_type": "text"}, 777)
	e.b.handleReactionsUpdate(&tg.UpdateMessageReactions{Peer: &tg.PeerUser{UserID: 7}, MsgID: 60, Reactions: reactionsFor("👍", 2)})
	evs := e.b.PollOnce()
	if len(evs) != 1 || evs[0].Type != proto.EventReactionUpdate || evs[0].ContactID != "7" {
		t.Fatalf("%+v", evs)
	}
	snap := evs[0].Payload["snapshot"].([]map[string]any)
	if len(snap) != 1 || snap[0]["emoji"] != "👍" || snap[0]["count"] != int64(2) || snap[0]["is_mine"] != true {
		t.Fatalf("custom emoji must be skipped: %v", snap)
	}
	authors := snap[0]["authors"].([]string)
	if len(authors) != 2 || authors[0] != "Bob" || authors[1] != "8" {
		t.Fatalf("authors %v", authors)
	}
	out, err := e.b.ApplyReaction("7", evs[0].Payload)
	if err != nil || out == nil || out["message_id"] != "60" || out["timestamp"] != int64(777) {
		t.Fatalf("%v %v", out, err)
	}
	rs := out["reactions"].([]any)
	r0 := rs[0].(map[string]any)
	if len(rs) != 1 || r0["emoji"] != "👍" || r0["count"] != int64(2) || r0["is_mine"] != true {
		t.Fatalf("%v", rs)
	}
	// The aggregate row is stored like Python: author_key "__agg__:<emoji>".
	rows, _ := e.st.ReactionsForContact(bg, "telegram", "7")
	if len(rows) != 1 || rows[0].AuthorKey != "__agg__:👍" || rows[0].Author != "Bob, 8" {
		t.Fatalf("%+v", rows)
	}
	// An empty snapshot clears the aggregate.
	clearEv := map[string]any{"mode": "snapshot", "snapshot": []map[string]any{}, "target_message_id": "60", "timestamp": int64(5)}
	out, _ = e.b.ApplyReaction("7", clearEv)
	if out == nil || len(out["reactions"].([]any)) != 0 {
		t.Fatalf("%v", out)
	}
	// Unknown target: persisted but nothing to push.
	unk := map[string]any{"mode": "snapshot", "snapshot": []map[string]any{{"emoji": "❤️", "count": 1, "is_mine": false, "authors": []string{"x"}}}, "target_message_id": "999", "timestamp": int64(5)}
	if out, _ := e.b.ApplyReaction("7", unk); out != nil {
		t.Fatalf("unknown target pushed: %v", out)
	}
	if out, _ := e.b.ApplyReaction("7", map[string]any{"mode": "delta"}); out != nil {
		t.Fatal("non-snapshot applied")
	}
	// Reactions carried by a fetched message are queued before the message.
	m := textMsg(61, &tg.PeerUser{UserID: 7}, "has reactions", false)
	m.SetReactions(reactionsFor("🔥", 1))
	ev := e.b.messageToEvent(m, entityMaps{}, "")
	got := e.b.PollOnce()
	if ev == nil || len(got) != 1 || got[0].Type != proto.EventReactionUpdate {
		t.Fatalf("%+v %+v", ev, got)
	}
	if e.b.messageToEvent(textMsg(62, &tg.PeerUser{UserID: 7}, "none", false), entityMaps{}, ""); len(e.b.PollOnce()) != 0 {
		t.Fatal("message without reactions queued an event")
	}
}

func TestLoadContactsAndReconcileReadState(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(bg, "7", map[string]any{"id": "5", "text": "a", "is_mine": true, "msg_type": "text"}, 1)
	e.b.IngestMessage(bg, "7", map[string]any{"id": "9", "text": "b", "is_mine": true, "msg_type": "text"}, 2)
	e.f.reply(&tg.MessagesGetDialogsRequest{}, &tg.MessagesDialogs{
		Dialogs: []tg.DialogClass{
			&tg.Dialog{Peer: &tg.PeerUser{UserID: 7}, TopMessage: 9, ReadOutboxMaxID: 5},
			&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 3}, TopMessage: 1},
		},
		Messages: []tg.MessageClass{
			&tg.Message{ID: 9, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000},
			&tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 3}, Date: 1600000000},
		},
		Users: []tg.UserClass{&tg.User{ID: 7, FirstName: "Bob", AccessHash: 11}},
		Chats: []tg.ChatClass{&tg.Channel{ID: 3, Title: "News", AccessHash: 22, Photo: &tg.ChatPhotoEmpty{}}},
	})
	api, _ := e.b.getAPI()
	e.b.loadContacts(bg, api)
	cs, _ := e.b.ListContacts(bg)
	if len(cs) != 2 || cs[0].ID != "7" || cs[0].DisplayName != "Bob" || cs[1].ID != "-1000000000003" || cs[1].Extras["is_channel"] != true {
		t.Fatalf("%+v", cs)
	}
	if cs[0].LastMessageTS() != 1700000000000 || cs[0].Extras["read_outbox_max_id"] != int64(5) {
		t.Fatalf("%+v", cs[0].Extras)
	}
	// Access hashes learnt from the dialogs make the peers sendable.
	if p, err := e.b.resolveInputPeer(-1000000000003); err != nil || p.(*tg.InputPeerChannel).AccessHash != 22 {
		t.Fatalf("%v %v", p, err)
	}
	// Reconcile: id 5 <= max 5 becomes read, 9 stays sent; one receipt event.
	if got, _ := e.b.cachedEntry("7", "5"); got.Status != "read" {
		t.Fatalf("%+v", got)
	}
	if got, _ := e.b.cachedEntry("7", "9"); got.Status == "read" {
		t.Fatalf("%+v", got)
	}
	evs := e.b.PollOnce()
	if len(evs) != 1 || evs[0].Type != proto.EventReceipt {
		t.Fatalf("%+v", evs)
	}
	// Merge refresh keeps ghosts and reports new contacts.
	e.b.RegisterContact(proto.Contact{ID: "555", DisplayName: "Ghost", Protocol: "telegram", Extras: map[string]any{"ghost": true}})
	newC, err := e.b.loadContactsMerge(bg, api)
	if err != nil || len(newC) != 0 {
		t.Fatalf("%v %v", newC, err)
	}
	if _, ok := e.b.FindContact("555"); !ok {
		t.Fatal("ghost dropped by merge")
	}
}

func TestRegisterFindPlaceholder(t *testing.T) {
	e := newEnv(t)
	c := proto.Contact{ID: "77", DisplayName: "Zed", Protocol: "telegram"}
	if !e.b.RegisterContact(c) || e.b.RegisterContact(c) {
		t.Fatal("register must be idempotent by cache key")
	}
	if got, ok := e.b.FindContact("77"); !ok || got.DisplayName != "Zed" {
		t.Fatal("find")
	}
	if e.b.PlaceholderName("77") != "Zed" || e.b.PlaceholderName("123") != "" {
		t.Fatal("placeholder")
	}
	if e.b.identifyContact("77") == nil || e.b.identifyContact("x") != nil {
		t.Fatal("identify")
	}
}

func TestAddressBookMerge(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.b.contacts[0].Extras["last_message_ts"] = int64(123)
	e.f.reply(&tg.ContactsGetContactsRequest{}, &tg.ContactsContacts{Users: []tg.UserClass{
		&tg.User{ID: 7, FirstName: "Bob", Phone: "39 1", AccessHash: 5},
		&tg.User{ID: 8, FirstName: "Anna", AccessHash: 6},
		&tg.User{ID: 9, FirstName: "Robot", Bot: true},
		&tg.User{ID: 10, FirstName: "Deleted Account", Deleted: true},
	}})
	book, err := e.b.ListAddressBook(bg, false)
	if err != nil || len(book) != 2 {
		t.Fatalf("%v %v", book, err)
	}
	if book[0].ID != "7" || book[0].Extras["is_chat_active"] != true || book[0].LastMessageTS() != 123 || book[0].Extras["phone"] != "391" {
		t.Fatalf("%+v", book[0])
	}
	if book[1].ID != "8" || book[1].Extras["is_chat_active"] != nil || book[1].Extras["source"] != "tg_book" {
		t.Fatalf("%+v", book[1])
	}
	// TTL cache: no second request.
	e.b.ListAddressBook(bg, false)
	if e.f.count(&tg.ContactsGetContactsRequest{}) != 1 {
		t.Fatal("address book not cached")
	}
	e.b.ListAddressBook(bg, true)
	if e.f.count(&tg.ContactsGetContactsRequest{}) != 2 {
		t.Fatal("force must refetch")
	}
	if c, ok := e.b.FindContact("8"); !ok || c.DisplayName != "Anna" {
		t.Fatal("find in address book")
	}
	// Book users become sendable through their access_hash.
	if p, err := e.b.resolveInputPeer(8); err != nil || p.(*tg.InputPeerUser).AccessHash != 6 {
		t.Fatalf("%v %v", p, err)
	}
}

func TestResolveInputPeer(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.resolveInputPeer(4242); err == nil {
		t.Fatal("expected access_hash error")
	}
	if p, _ := e.b.resolveInputPeer(-5); p.(*tg.InputPeerChat).ChatID != 5 {
		t.Fatal("chat")
	}
	if p, _ := e.b.resolveInputPeer(1000); p == nil {
		t.Fatal("self")
	} else if _, ok := p.(*tg.InputPeerSelf); !ok {
		t.Fatal("self peer")
	}
	e.b.peers.set(4242, 9)
	if p, _ := e.b.resolveInputPeer(4242); p.(*tg.InputPeerUser).AccessHash != 9 {
		t.Fatal("user")
	}
	if _, err := e.b.resolveInputPeer(-1000000000009); err == nil {
		t.Fatal("channel without hash")
	}
}

func TestPeerStorePersists(t *testing.T) {
	p := newPeerStore(filepath.Join(t.TempDir(), "peers.json"))
	p.set(5, 50)
	p.set(-1000000000003, 30)
	p.flush()
	q := newPeerStore(p.path)
	if h, ok := q.get(5); !ok || h != 50 {
		t.Fatal("user hash lost")
	}
	if h, ok := q.get(-1000000000003); !ok || h != 30 {
		t.Fatal("channel hash lost")
	}
}
