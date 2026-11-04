package core

import (
	"context"
	"errors"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func seedUnread(t *testing.T, e *env, protocol, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, _, err := e.st.AddMessage(bg, store.NewMessage{Protocol: protocol, ContactNumber: key,
			Text: "m" + string(rune('a'+i)), Sender: "x", Timestamp: int64(100 + i)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestContactsList(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	e.fake("signal").SetContacts(
		contact("signal", "+391", "Alice", map[string]any{"phone": "391", "last_message_ts": int64(1700)}),
		contact("signal", "+392", "Bob", nil))
	e.fake("whatsapp").SetContacts(contact("whatsapp", "123@lid", "Carla", map[string]any{"phone": "39", "lid": "123@lid"}))
	seedUnread(t, e, "signal", "+391", 2)
	// WhatsApp history is split over the @lid and @c.us keys: unread is the sum.
	seedUnread(t, e, "whatsapp", "123@lid", 1)
	seedUnread(t, e, "whatsapp", "39@c.us", 2)
	// Read and outgoing rows do not count.
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+391", Text: "mine", IsMine: true, Timestamp: 5})

	wantJSON(t, e.mustCall("contacts.list", `{}`), `[
	 {"id":"+391","display_name":"Alice","protocol":"signal","extras":{"phone":"391","last_message_ts":1700},"last_message_ts":1700,"unread":2},
	 {"id":"+392","display_name":"Bob","protocol":"signal","extras":{},"last_message_ts":0,"unread":0},
	 {"id":"123@lid","display_name":"Carla","protocol":"whatsapp","extras":{"phone":"39","lid":"123@lid"},"last_message_ts":0,"unread":3}]`)
	wantJSON(t, e.mustCall("contacts.list", `{"q":"  ALI "}`), `[
	 {"id":"+391","display_name":"Alice","protocol":"signal","extras":{"phone":"391","last_message_ts":1700},"last_message_ts":1700,"unread":2}]`)
	wantJSON(t, e.mustCall("contacts.list", `{"q":"nobody"}`), `[]`)
}

type reconciler struct {
	*fake.Backend
	n int
}

func (r *reconciler) ReconcileUnread(ctx context.Context) { r.n++ }

func TestContactsListReconcilesWhatsApp(t *testing.T) {
	e := newEnv(t, "whatsapp")
	r := &reconciler{Backend: e.fake("whatsapp")}
	_ = e.mgr.Register(r)
	e.mustCall("contacts.list", `{}`)
	if r.n != 1 {
		t.Fatalf("reconcile calls: %d", r.n)
	}
}

func TestContactsBook(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	e.fake("whatsapp").SetAddressBook(
		contact("whatsapp", "39@c.us", "Mario Rossi", map[string]any{"phone": "39"}),
		contact("whatsapp", "40@c.us", "Luigi", map[string]any{"phone": "40"}))
	wantJSON(t, e.mustCall("contacts.book", `{"q":"   "}`), `[]`)
	wantJSON(t, e.mustCall("contacts.book", `{"q":"mario"}`), `[
	 {"id":"39@c.us","display_name":"Mario Rossi","protocol":"whatsapp","extras":{"phone":"39","address_book":true},"last_message_ts":0,"unread":0}]`)
}

func TestContactsForProtocolAndRegister(t *testing.T) {
	e := newEnv(t, "signal")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", map[string]any{"phone": "391"}))
	wantJSON(t, e.mustCall("contacts.for_protocol", `{"protocol":" signal "}`), `[
	 {"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391","last_message_ts":0,"extras":{"phone":"391"}}]`)
	if oe := e.callErr("contacts.for_protocol", `{"protocol":"telegram"}`); oe.Code != "backend_down" || oe.Message != "Backend unavailable" {
		t.Fatalf("missing backend: %+v", oe)
	}

	wantJSON(t, e.mustCall("contacts.register", `{"id":" +399 ","display_name":"New","protocol":"signal","extras":{"x":1}}`),
		`{"id":"+399","display_name":"New","protocol":"signal","cache_key":"signal:+399","last_message_ts":0,"extras":{"x":1,"ghost":true}}`)
	// The registered ghost is now listed with the backend's contacts.
	wantJSON(t, e.mustCall("contacts.for_protocol", `{"protocol":"signal"}`), `[
	 {"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391","last_message_ts":0,"extras":{"phone":"391"}},
	 {"id":"+399","display_name":"New","protocol":"signal","cache_key":"signal:+399","last_message_ts":0,"extras":{"x":1,"ghost":true}}]`)

	for _, bad := range []string{
		`{"id":"  ","display_name":"n","protocol":"signal"}`,
		`{"id":"1","display_name":"n","protocol":"nope"}`,
	} {
		if oe := e.callErr("contacts.register", bad); oe.Code != "invalid_request" || oe.Message != "Invalid request" {
			t.Fatalf("%s: %+v", bad, oe)
		}
	}
	if oe := e.callErr("contacts.register", `{"id":"1","display_name":"n","protocol":"telegram"}`); oe.Code != "backend_down" {
		t.Fatalf("register without backend: %+v", oe)
	}
	if oe := e.callErr("contacts.register", `{"id":"1","protocol":"signal"}`); oe.Code != "invalid_request" {
		t.Fatalf("display_name is required: %+v", oe)
	}
}

func TestContactsAddressBook(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	e.fake("whatsapp").FailMethod("ListAddressBook", errors.New("waha down"))
	wantJSON(t, e.mustCall("contacts.address_book", `{}`), `{
	 "contacts":[{"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391","last_message_ts":0,"extras":{"address_book":true}}],
	 "errors":{"whatsapp":"waha down"}}`)
	wantJSON(t, e.mustCall("contacts.address_book", `{"protocols":["signal"]}`), `{
	 "contacts":[{"id":"+391","display_name":"Alice","protocol":"signal","cache_key":"signal:+391","last_message_ts":0,"extras":{"address_book":true}}],
	 "errors":{}}`)
	wantJSON(t, e.mustCall("contacts.address_book", `{"protocols":[]}`), `{"contacts":[],"errors":{}}`)
}

type numberChecker struct {
	*fake.Backend
	exists bool
	err    error
	seen   string
}

func (n *numberChecker) CheckNumberExists(ctx context.Context, phone string) (bool, error) {
	n.seen = phone
	return n.exists, n.err
}

func TestContactsCheckNumber(t *testing.T) {
	e := newEnv(t, "whatsapp")
	wantJSON(t, e.mustCall("contacts.check_number", `{"phone":"39123"}`), `{"exists":null}`)
	nc := &numberChecker{Backend: e.fake("whatsapp"), exists: true}
	_ = e.mgr.Register(nc)
	wantJSON(t, e.mustCall("contacts.check_number", `{"phone":" 39123 "}`), `{"exists":true}`)
	if nc.seen != "39123" {
		t.Fatalf("phone is stripped, saw %q", nc.seen)
	}
	wantJSON(t, e.mustCall("contacts.check_number", `{"phone":"  "}`), `{"exists":null}`)
	nc.err = errors.New("boom")
	wantJSON(t, e.mustCall("contacts.check_number", `{"phone":"1"}`), `{"exists":null}`)
}

func TestChatsMarkReadAndArchive(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	e.fake("signal").SetContacts(contact("signal", "+391", "Alice", nil))
	seedUnread(t, e, "signal", "+391", 2)
	wantJSON(t, e.mustCall("chats.mark_read", `{"protocol":" signal ","contact_id":" +391 "}`), `{"status":"ok"}`)
	if n, _ := e.st.UnreadCounts(bg); n[store.ContactKey{Protocol: "signal", Contact: "+391"}] != 0 {
		t.Fatalf("unread after mark_read: %v", n)
	}
	if len(e.fake("signal").CallsTo("MarkRead")) != 1 {
		t.Fatal("backend MarkRead not called")
	}
	// Backend failure is swallowed.
	e.fake("signal").FailMethod("MarkRead", errors.New("x"))
	wantJSON(t, e.mustCall("chats.mark_read", `{"protocol":"signal","contact_id":"+391"}`), `{"status":"ok"}`)
	if oe := e.callErr("chats.mark_read", `{"protocol":"email","contact_id":"x"}`); oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}
	if oe := e.callErr("chats.mark_read", `{"protocol":"signal","contact_id":" "}`); oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}

	wantJSON(t, e.mustCall("chats.archive", `{"protocol":"whatsapp","contact_id":"39@c.us","archived":true}`), `{"archived":true}`)
	if !e.fake("whatsapp").Archived("39@c.us") {
		t.Fatal("archive flag")
	}
	if oe := e.callErr("chats.archive", `{"protocol":"signal","contact_id":"x","archived":true}`); oe.Code != "invalid_request" {
		t.Fatalf("%+v", oe)
	}
	if oe := e.callErr("chats.archive", `{"protocol":"whatsapp","contact_id":"x","archived":"yes"}`); oe.Code != "invalid_request" {
		t.Fatalf("archived is a strict bool: %+v", oe)
	}
	e.fake("whatsapp").FailMethod("SetChatArchived", errors.New("x"))
	if oe := e.callErr("chats.archive", `{"protocol":"whatsapp","contact_id":"x","archived":false}`); oe.Code != "backend_error" || oe.Message != "Archive failed" {
		t.Fatalf("%+v", oe)
	}
}

var _ = proto.ProtocolSignal
