package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func seedOwn(t *testing.T, e *env, protocol, contactID, text string, ts int64, status string) string {
	t.Helper()
	id, err := e.fake(protocol).SendMessage(bg, contactID, text, proto.Quote{})
	if err != nil {
		t.Fatal(err)
	}
	if protocol == "signal" {
		id = itoa(ts)
	}
	_, _, err = e.st.AddMessage(bg, store.NewMessage{Protocol: protocol, ContactNumber: contactID, Text: text, IsMine: true,
		Sender: "You", Timestamp: ts, Status: status, MsgID: &id})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEditSuccess(t *testing.T) {
	e := newEnv(t, "whatsapp")
	e.fake("whatsapp").SetContacts(contact("whatsapp", "c1", "C", nil))
	id := seedOwn(t, e, "whatsapp", "c1", "before", 5000, "sent")
	body := `{"protocol":"whatsapp","contact_id":"c1","message_id":"` + id + `","new_text":"after"}`
	wantJSON(t, e.mustCall("messages.edit", body), `{"ok":true}`)
	row := rowByText(t, e, "whatsapp", "c1", "after")
	if !row.Edited {
		t.Fatal("row must be flagged edited")
	}
	pushes := e.webPushes()
	if len(pushes) != 1 {
		t.Fatalf("pushes %v", pushes)
	}
	wantJSON(t, pushes[0], `{"event":{"type":"message_edit","payload":{"protocol":"whatsapp","contact_id":"c1",
	  "message_id":"`+id+`","timestamp":5000,"old_text":"before","text":"after","is_mine":true}}}`)
	if n := len(e.fake("whatsapp").CallsTo("ApplyEdit")); n != 1 {
		t.Fatalf("backend cache sync calls: %d", n)
	}
}

func TestEditErrors(t *testing.T) {
	e := newEnv(t, "whatsapp", "signal")
	e.fake("whatsapp").SetContacts(contact("whatsapp", "c1", "C", nil))
	id := seedOwn(t, e, "whatsapp", "c1", "txt", 1000, "sent")
	pending := seedOwn(t, e, "whatsapp", "c1", "pend", 2000, "pending")
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "whatsapp", ContactNumber: "c1", Text: "theirs", Sender: "C",
		Timestamp: 3000, MsgID: store.Ptr("in1")})
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "whatsapp", ContactNumber: "c1", Text: "ghost", IsMine: true,
		Timestamp: 4000, Status: "sent"})

	inv := []string{
		`{"protocol":"x","contact_id":"c1","message_id":"1","new_text":"t"}`,
		`{"protocol":"whatsapp","contact_id":" ","message_id":"1","new_text":"t"}`,
		`{"protocol":"whatsapp","contact_id":"c1","message_id":" ","new_text":"t"}`,
		`{"protocol":"whatsapp","contact_id":"c1","message_id":"1","new_text":"  "}`,
		`{"protocol":"whatsapp","contact_id":"c1","message_id":"1","new_text":7}`,
		`{"protocol":"whatsapp","contact_id":"c1","message_id":"1"}`,
	}
	for _, b := range inv {
		if oe := e.callErr("messages.edit", b); oe.Code != "invalid_request" || oe.Message != "Invalid request" {
			t.Errorf("%s: %+v", b, oe)
		}
	}
	if oe := e.callErr("messages.edit", `{"protocol":"whatsapp","contact_id":"c1","message_id":"zzz","new_text":"t"}`); oe.Code != "not_found" || oe.Message != "Not Found" {
		t.Fatalf("%+v", oe)
	}
	for _, mid := range []string{"in1", pending} {
		if oe := e.callErr("messages.edit", `{"protocol":"whatsapp","contact_id":"c1","message_id":"`+mid+`","new_text":"t"}`); oe.Code != "invalid_request" || oe.Message != "Message not editable" {
			t.Errorf("%s: %+v", mid, oe)
		}
	}
	// The backend refuses (it never saw this message): backend_error + details.
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "whatsapp", ContactNumber: "c1", Text: "unknown", IsMine: true,
		Timestamp: 6000, Status: "sent", MsgID: store.Ptr("unknown-id")})
	oe := e.callErr("messages.edit", `{"protocol":"whatsapp","contact_id":"c1","message_id":"unknown-id","new_text":"t"}`)
	if oe.Code != "backend_error" || oe.Message != "Message edit failed" {
		t.Fatalf("%+v", oe)
	}
	wantJSON(t, mustMarshal(t, oe.Details), `{"error":"edit rejected by server"}`)
	// And the row was not touched.
	if rowByText(t, e, "whatsapp", "c1", "unknown").Edited {
		t.Fatal("row edited despite the refusal")
	}
	_ = id
	if len(e.webPushes()) != 0 {
		t.Fatal("failed edits publish nothing")
	}
}

func TestEditSignalIdlessRowUsesTimestamp(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "c1", "C", nil))
	id, _ := e.fake("signal").SendMessage(bg, "c1", "old", proto.Quote{})
	// The fake edits by its own id, which for Signal is the timestamp.
	ts := int64(0)
	for _, ch := range id {
		ts = ts*10 + int64(ch-'0')
	}
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "c1", Text: "old", IsMine: true,
		Sender: "You", Timestamp: ts, Status: "sent"})
	wantJSON(t, e.mustCall("messages.edit", `{"protocol":"signal","contact_id":"c1","message_id":"`+id+`","new_text":"new"}`), `{"ok":true}`)
	if r := rowByText(t, e, "signal", "c1", "new"); !r.Edited || r.Timestamp != ts {
		t.Fatalf("%+v", r)
	}
}

// aggregator is a reactor that returns a proper aggregate.
type aggregator struct {
	*fake.Backend
	user    string
	seen    map[string]any
	reply   map[string]any
	sendErr bool
}

func (a *aggregator) UserNumber() string { return a.user }
func (a *aggregator) ApplyReaction(contactID string, payload map[string]any) (map[string]any, error) {
	a.seen = payload
	return a.reply, nil
}

func TestReactionsSet(t *testing.T) {
	e := newEnv(t, "signal")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "+39", "C", nil))
	ag := &aggregator{Backend: f, user: "+1000", reply: map[string]any{
		"message_id": "777", "timestamp": int64(1234),
		"reactions": []any{map[string]any{"emoji": "👍", "count": 1, "is_mine": true, "authors": []any{"You"}}},
	}}
	_ = e.mgr.Register(ag)
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+39", Text: "hello", Sender: "C", Timestamp: 1234})
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+39", Text: "mine", IsMine: true, Timestamp: 2222})

	// Incoming message: the target author is the contact; the backend id is the row timestamp.
	out := e.mustCall("reactions.set", `{"protocol":"signal","contact_id":" +39 ","message_id":" 1234 ","emoji":"👍"}`)
	wantJSON(t, out, `{"ok":true,"reactions":[{"emoji":"👍","count":1,"is_mine":true,"authors":["You"]}]}`)
	call := f.CallsTo("SendReaction")[0].Args
	if call["id"] != "1234" || call["author"] != "+39" || call["emoji"] != "👍" || call["contact"] != "+39" {
		t.Fatalf("SendReaction args: %v", call)
	}
	p := ag.seen
	if p["mode"] != "delta" || p["target_message_id"] != "1234" || p["target_timestamp"] != int64(1234) || p["is_mine"] != true ||
		p["author_key"] != "me" || p["author"] != "You" || p["is_remove"] != false || p["emoji"] != "👍" {
		t.Fatalf("payload: %v", p)
	}
	if ms, _ := p["timestamp"].(int64); time.Since(time.UnixMilli(ms)) > 5*time.Second {
		t.Fatalf("timestamp %v", p["timestamp"])
	}
	pushes := e.webPushes()
	if len(pushes) != 1 {
		t.Fatalf("pushes %v", pushes)
	}
	wantJSON(t, pushes[0], `{"event":{"type":"reaction_update","payload":{"protocol":"signal","contact_id":"+39",
	  "message_id":"777","timestamp":1234,"reactions":[{"emoji":"👍","count":1,"is_mine":true,"authors":["You"]}]}}}`)

	// Own message: the target author is our account number.
	e.mustCall("reactions.set", `{"protocol":"signal","contact_id":"+39","message_id":"2222","emoji":"❤️"}`)
	calls := f.CallsTo("SendReaction")
	if calls[1].Args["author"] != "+1000" {
		t.Fatalf("own message author: %v", calls[1].Args)
	}

	// No aggregate: falls back to the row identity and [] reactions.
	ag.reply = nil
	out = e.mustCall("reactions.set", `{"protocol":"signal","contact_id":"+39","message_id":"1234","emoji":"👍"}`)
	wantJSON(t, out, `{"ok":true,"reactions":[]}`)
}

func TestReactionsSetErrors(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "+39", "C", nil))
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+39", Text: "hello", Sender: "C", Timestamp: 1234, MsgID: store.Ptr("m1")})
	inv := []string{
		`{"protocol":"x","contact_id":"+39","message_id":"m1","emoji":"👍"}`,
		`{"protocol":"signal","contact_id":" ","message_id":"m1","emoji":"👍"}`,
		`{"protocol":"signal","contact_id":"+39","message_id":" ","emoji":"👍"}`,
		`{"protocol":"signal","contact_id":"+39","message_id":"m1","emoji":""}`,
		`{"protocol":"signal","contact_id":"+39","message_id":"m1","emoji":"abc"}`,
		`{"protocol":"signal","contact_id":"+39","message_id":"m1","emoji":"👍👍👍👍👍👍👍👍👍"}`,
		`{"protocol":"signal","contact_id":"+39","message_id":"m1","emoji":5}`,
	}
	for _, b := range inv {
		if oe := e.callErr("reactions.set", b); oe.Code != "invalid_request" || oe.Message != "Invalid request" {
			t.Errorf("%s: %+v", b, oe)
		}
	}
	if oe := e.callErr("reactions.set", `{"protocol":"telegram","contact_id":"+39","message_id":"m1","emoji":"👍"}`); oe.Code != "not_found" || oe.Message != "Backend not found" {
		t.Fatalf("%+v", oe)
	}
	if oe := e.callErr("reactions.set", `{"protocol":"signal","contact_id":"+39","message_id":"nope","emoji":"👍"}`); oe.Code != "not_found" || oe.Message != "Message not found" {
		t.Fatalf("%+v", oe)
	}
	e.fake("signal").FailMethod("SendReaction", context.Canceled)
	if oe := e.callErr("reactions.set", `{"protocol":"signal","contact_id":"+39","message_id":"m1","emoji":"👍"}`); oe.Code != "backend_error" || oe.Message != "Reaction send failed" {
		t.Fatalf("%+v", oe)
	}
}

func TestReactionsSetTelegramSnapshot(t *testing.T) {
	e := newEnv(t, "telegram")
	f := e.fake("telegram")
	f.SetContacts(contact("telegram", "9", "C", nil))
	ag := &aggregator{Backend: f, reply: nil}
	_ = e.mgr.Register(ag)
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "telegram", ContactNumber: "9", Text: "hello", Sender: "C", Timestamp: 500, MsgID: store.Ptr("t1")})
	tid := int64(500)
	tmid := "t1"
	// Two other people liked it; we previously reacted with a different emoji.
	for _, d := range []store.ReactionDelta{
		{Protocol: "telegram", Contact: "9", TargetMsgID: &tmid, TargetTimestamp: &tid, Emoji: "👍", AuthorKey: "a", Author: "Ann", Timestamp: 10},
		{Protocol: "telegram", Contact: "9", TargetMsgID: &tmid, TargetTimestamp: &tid, Emoji: "👍", AuthorKey: "b", Author: "Bob", Timestamp: 11},
		{Protocol: "telegram", Contact: "9", TargetMsgID: &tmid, TargetTimestamp: &tid, Emoji: "😂", AuthorKey: "me", Author: "You", IsMine: true, Timestamp: 12},
	} {
		if _, err := e.st.ApplyReactionDelta(bg, d); err != nil {
			t.Fatal(err)
		}
	}
	e.mustCall("reactions.set", `{"protocol":"telegram","contact_id":"9","message_id":"t1","emoji":"👍"}`)
	p := ag.seen
	if p["mode"] != "snapshot" {
		t.Fatalf("mode: %v", p["mode"])
	}
	b, _ := json.Marshal(p["snapshot"])
	// Our old 😂 is dropped (count 1 - mine = 0); 👍 gains us.
	wantJSON(t, string(b), `[{"emoji":"👍","count":3,"is_mine":true,"authors":["Ann","Bob","You"]}]`)
	if _, err := os.Stat(filepath.Join(t.TempDir())); err != nil {
		t.Fatal(err)
	}
}
