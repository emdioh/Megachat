package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func process(t *testing.T, e *env, ev proto.Event) []evt {
	t.Helper()
	e.core.PumpEvent(bg, ev)
	return e.events()
}

func msgPayload(id, text string, mine bool, ts int64) map[string]any {
	return map[string]any{
		"id": id, "text": text, "is_mine": mine, "sender": "Alice", "timestamp": ts,
		"quote_text": nil, "msg_type": "text", "attachment_info": nil, "attachment_id": nil,
		"content_type": nil, "read": mine, "status": "read",
	}
}

func TestProcessMessageWireJSON(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", map[string]any{"phone": "391", "last_message_ts": int64(5)}))
	evs := process(t, e, proto.Event{Type: proto.EventMessage, Protocol: "signal", ContactID: "+391",
		Payload: msgPayload("m1", "hi", false, 1700000000000)})
	if len(evs) != 1 || evs[0].Type != "message" {
		t.Fatalf("events: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{
	  "protocol":"signal","contact_id":"+391",
	  "contact":{"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391",
	             "last_message_ts":1700000000000,"extras":{"phone":"391","last_message_ts":1700000000000}},
	  "was_placeholder":false,
	  "message":{"id":"m1","text":"hi","is_mine":false,"sender":"Alice","timestamp":1700000000000,
	             "quote_text":null,"msg_type":"text","attachment_info":null,"attachment_id":null,
	             "content_type":null,"read":false,"status":"read"},
	  "timestamp":1700000000000,"added":true,"changed":false}`)

	// Stored, unread.
	rows, _ := e.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{"+391"}})
	if len(rows) != 1 || rows[0].Read || store.Deref(rows[0].MsgID) != "m1" {
		t.Fatalf("stored rows: %+v", rows)
	}
	// The same message again: the event is still emitted, nothing new.
	evs = process(t, e, proto.Event{Type: proto.EventMessage, Protocol: "signal", ContactID: "+391",
		Payload: msgPayload("m1", "hi", false, 1700000000000)})
	if len(evs) != 1 {
		t.Fatalf("duplicate must still emit: %+v", evs)
	}
	var p map[string]any
	_ = json.Unmarshal([]byte(evs[0].Payload), &p)
	if p["added"] != false || p["changed"] != false {
		t.Fatalf("duplicate flags: %v %v", p["added"], p["changed"])
	}
}

func TestProcessMessagePlaceholderAndRegistration(t *testing.T) {
	e := newEnv(t, "signal")
	evs := process(t, e, proto.Event{Type: proto.EventMessage, Protocol: "signal", ContactID: "+777",
		Payload: msgPayload("m9", "yo", false, 10)})
	var p map[string]any
	_ = json.Unmarshal([]byte(evs[0].Payload), &p)
	if p["was_placeholder"] != true {
		t.Fatalf("placeholder flag: %v", p["was_placeholder"])
	}
	c := p["contact"].(map[string]any)
	if c["display_name"] != "+777" || c["cache_key"] != "signal:+777" {
		t.Fatalf("placeholder contact: %v", c)
	}
	// Unknown contacts are registered so later lookups succeed.
	if _, ok := e.core.FindContact(bg, "signal", "+777"); !ok {
		t.Fatal("contact should have been registered")
	}
	// Unknown protocol: dropped.
	if evs := process(t, e, proto.Event{Type: proto.EventMessage, Protocol: "telegram", ContactID: "1", Payload: msgPayload("a", "b", false, 1)}); len(evs) != 0 {
		t.Fatalf("unknown backend: %+v", evs)
	}
}

func TestProcessOptimisticEchoIsChanged(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	_, _, err := e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+391", Text: "hey",
		IsMine: true, Sender: "You", Timestamp: 1000, Status: "sent"})
	if err != nil {
		t.Fatal(err)
	}
	evs := process(t, e, proto.Event{Type: proto.EventMessage, Protocol: "signal", ContactID: "+391",
		Payload: msgPayload("srv1", "hey", true, 1500)})
	var p map[string]any
	_ = json.Unmarshal([]byte(evs[0].Payload), &p)
	if p["added"] != false || p["changed"] != true {
		t.Fatalf("echo flags: added=%v changed=%v", p["added"], p["changed"])
	}
	rows, _ := e.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{"+391"}})
	if len(rows) != 1 || store.Deref(rows[0].MsgID) != "srv1" || rows[0].Timestamp != 1000 {
		t.Fatalf("optimistic row must take the id and keep its timestamp: %+v", rows)
	}
}

func TestProcessSentMirror(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	evs := process(t, e, proto.Event{Type: proto.EventSentMirror, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"id": "1700000000123", "timestamp": int64(1700000000123), "batch_id": ""}})
	if len(evs) != 1 || evs[0].Type != "sent_mirror" {
		t.Fatalf("events: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"signal","contact_id":"+391",
	  "contact":{"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391",
	             "last_message_ts":1700000000123,"extras":{"last_message_ts":1700000000123}},
	  "id":"1700000000123","timestamp":1700000000123,"batch_id":null}`)
	evs = process(t, e, proto.Event{Type: proto.EventSentMirror, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"id": "5", "timestamp": int64(5), "batch_id": "b-1"}})
	var p map[string]any
	_ = json.Unmarshal([]byte(evs[0].Payload), &p)
	if p["batch_id"] != "b-1" {
		t.Fatalf("batch_id: %v", p["batch_id"])
	}
}

func TestProcessTypingPassthroughAndWarmup(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	evs := process(t, e, proto.Event{Type: proto.EventTyping, Protocol: "signal", ContactID: "+1", Payload: map[string]any{"action": "STARTED"}})
	if len(evs) != 1 || evs[0].Type != "typing" {
		t.Fatalf("typing: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"signal","contact_id":"+1","action":"STARTED"}`)
	if evs := process(t, e, proto.Event{Type: proto.EventTyping, Protocol: "signal", ContactID: "+1", Payload: map[string]any{"action": "OTHER"}}); len(evs) != 0 {
		t.Fatalf("other typing actions are dropped: %+v", evs)
	}

	evs = process(t, e, proto.Event{Type: proto.EventContactUpdate, Protocol: "whatsapp", ContactID: "123@lid",
		Payload: map[string]any{
			"contact":      contact("whatsapp", "39@c.us", "Mario", map[string]any{"phone": "39"}),
			"display_name": "Mario", "phone": "39", "merged_into": "39@c.us"}})
	if len(evs) != 1 || evs[0].Type != "contact_update" {
		t.Fatalf("contact_update: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"whatsapp","contact_id":"123@lid",
	  "contact":{"id":"39@c.us","display_name":"Mario","protocol":"whatsapp","cache_key":"whatsapp:39@c.us","last_message_ts":0,"extras":{"phone":"39"}},
	  "display_name":"Mario","phone":"39","merged_into":"39@c.us"}`)

	evs = process(t, e, proto.Event{Type: proto.EventLidWarmupDone, Protocol: "whatsapp", ContactID: "", Payload: map[string]any{}})
	if len(evs) != 1 || evs[0].Type != "lid_warmup_done" {
		t.Fatalf("lid_warmup_done: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"whatsapp","contact_id":""}`)
}

func TestProcessEditReactionReceipt(t *testing.T) {
	e := newEnv(t, "signal", "telegram")
	f := e.fake("signal")
	f.SetContacts(contact("signal", "+391", "Alice", nil))
	// The fake edits messages it sent itself.
	id, err := f.SendMessage(bg, "+391", "old", proto.Quote{})
	if err != nil {
		t.Fatal(err)
	}
	evs := process(t, e, proto.Event{Type: proto.EventMessageEdit, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"text": "new", "edit_message_id": id, "is_mine": true}})
	if len(evs) != 1 || evs[0].Type != "message_edit" {
		t.Fatalf("edit: %+v", evs)
	}
	var p map[string]any
	_ = json.Unmarshal([]byte(evs[0].Payload), &p)
	info := p["info"].(map[string]any)
	if info["old_text"] != "old" || info["text"] != "new" || info["message_id"] != id || info["is_mine"] != true || p["text"] != "new" {
		t.Fatalf("edit payload: %s", evs[0].Payload)
	}
	// Idempotent / invalid edits publish nothing.
	if evs := process(t, e, proto.Event{Type: proto.EventMessageEdit, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"text": "new", "edit_message_id": id}}); len(evs) != 0 {
		t.Fatalf("idempotent edit: %+v", evs)
	}
	if evs := process(t, e, proto.Event{Type: proto.EventMessageEdit, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"text": "", "edit_message_id": id}}); len(evs) != 0 {
		t.Fatalf("empty edit: %+v", evs)
	}

	evs = process(t, e, proto.Event{Type: proto.EventReactionUpdate, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"target_message_id": "m1", "emoji": "👍"}})
	if len(evs) != 1 || evs[0].Type != "reaction_update" {
		t.Fatalf("reaction: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"signal","contact_id":"+391","info":{"message_id":"m1","reactions":["👍"]}}`)
	if evs := process(t, e, proto.Event{Type: proto.EventReactionUpdate, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"emoji": "👍"}}); len(evs) != 0 {
		t.Fatalf("reaction without target: %+v", evs)
	}
}

func TestProcessReceipts(t *testing.T) {
	e := newEnv(t, "signal", "telegram")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	for _, m := range []store.NewMessage{
		{Protocol: "signal", ContactNumber: "+391", Text: "a", IsMine: true, Sender: "You", Timestamp: 1000, Status: "sent"},
		{Protocol: "signal", ContactNumber: "+391", Text: "b", IsMine: true, Sender: "You", Timestamp: 9000, Status: "sent"},
		{Protocol: "telegram", ContactNumber: "77", Text: "t", IsMine: true, Sender: "You", Timestamp: 5, Status: "sent", MsgID: store.Ptr("55")},
	} {
		if _, _, err := e.st.AddMessage(bg, m); err != nil {
			t.Fatal(err)
		}
	}
	// Signal: delivery receipt within the 1 s tolerance of ts 1000.
	evs := process(t, e, proto.Event{Type: proto.EventReceipt, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"receipt": map[string]any{"isDelivery": true, "timestamps": []any{int64(1500)}}}})
	if len(evs) != 1 || evs[0].Type != "receipt" {
		t.Fatalf("receipt: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"signal","contact_id":"+391","updated":[
	  {"id":null,"text":"a","is_mine":true,"sender":"You","timestamp":1000,"quote_text":null,"msg_type":"text",
	   "attachment_info":null,"attachment_id":null,"content_type":null,"read":true,"status":"delivered"}]}`)
	// Never a downgrade: the same receipt again changes nothing.
	if evs := process(t, e, proto.Event{Type: proto.EventReceipt, Protocol: "signal", ContactID: "+391",
		Payload: map[string]any{"receipt": map[string]any{"isDelivery": true, "timestamps": []any{int64(1000)}}}}); len(evs) != 0 {
		t.Fatalf("repeated receipt: %+v", evs)
	}
	// Telegram: by message id, read.
	evs = process(t, e, proto.Event{Type: proto.EventReceipt, Protocol: "telegram", ContactID: "77",
		Payload: map[string]any{"message_ids": []any{"55"}, "is_read": true}})
	if len(evs) != 1 {
		t.Fatalf("telegram receipt: %+v", evs)
	}
	wantJSON(t, evs[0].Payload, `{"protocol":"telegram","contact_id":"77","updated":[
	  {"id":"55","timestamp":5,"status":"read","text":"t","is_mine":true}]}`)
}

// waitFor drains the subscription until pred matches or the deadline passes.
func waitFor(t *testing.T, sub *bus.Subscription, pred func(bus.Event) bool) bus.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			if pred(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for a bus event")
		}
	}
}

func TestStartConnectsPumpsAndStops(t *testing.T) {
	e := newEnv(t, "signal", "telegram")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	e.fake("telegram").FailConnect(context.DeadlineExceeded)
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	var ready, done bus.Event
	waitFor(t, e.sub, func(ev bus.Event) bool {
		switch ev.Type {
		case bus.BackendReady:
			ready = ev
		case bus.BackendDone:
			done = ev
		}
		return ready.Seq != 0 && done.Seq != 0
	})
	b, _ := json.Marshal(ready.Payload)
	wantJSON(t, string(b), `{"protocol":"signal","contacts":1}`)
	b, _ = json.Marshal(done.Payload)
	wantJSON(t, string(b), `{"protocol":"telegram","ok":false}`)

	if !e.fake("signal").Emit(proto.Event{Type: proto.EventMessage, ContactID: "+391", Payload: msgPayload("m1", "ciao", false, 100)}) {
		t.Fatal("emit")
	}
	msg := waitFor(t, e.sub, func(ev bus.Event) bool { return ev.Type == bus.Message })
	if msg.Payload["protocol"] != "signal" || msg.Payload["added"] != true {
		t.Fatalf("message event: %v", msg.Payload)
	}
	sctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	if err := e.core.Stop(sctx); err != nil {
		t.Logf("stop: %v", err)
	}
	if e.fake("signal").IsConnected() {
		t.Fatal("backend should be disconnected after Stop")
	}
	_ = api.EventMessage
}

func TestStartupStatusEventsOrder(t *testing.T) {
	e := newEnv(t, "signal")
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	defer e.core.Stop(bg)
	var types []string
	waitFor(t, e.sub, func(ev bus.Event) bool {
		types = append(types, string(ev.Type))
		return ev.Type == bus.BackendStatus && ev.Payload["text"] == "✅ Signal: daemon attivo"
	})
	want := []string{"backend_connecting", "backend_status", "backend_ready", "backend_status", "backend_status"}
	if len(types) != len(want) {
		t.Fatalf("event order %v want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event order %v want %v", types, want)
		}
	}
}
