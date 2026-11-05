package messages

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// groupFallbackRows are group rows whose sender or quote author nobody knows,
// exercising the page row builder directly (the byte-parity fixture in
// fixture_test.go pins the same divergence through the allowlist in
// parity_test.go).
func groupFallbackRows(*config.Config) []row {
	return []row{
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "lid stranger", "sender": "20508603076702@lid", "timestamp": at(1), "msg_id": "L1"},
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "known lid", "sender": "111@lid", "timestamp": at(2), "msg_id": "L2"},
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "phone jid", "sender": "222@c.us", "timestamp": at(3), "msg_id": "L3"},
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "stored placeholder", "sender": "Contatto WhatsApp (ID 20508603076702)", "timestamp": at(4), "msg_id": "L4"},
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "lid quote", "sender": "Bea", "timestamp": at(5), "msg_id": "L5",
			"quote_text": "q", "quote_timestamp": at(1), "quote_author": "999@lid"},
		{"protocol": "whatsapp", "contact_number": waGrp, "is_mine": 0, "text": "phone quote", "sender": "Bea", "timestamp": at(6), "msg_id": "L6",
			"quote_text": "q", "quote_timestamp": at(1), "quote_author": "777@c.us"},
	}
}

func newGroupFallbackEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, envOpts{rows: groupFallbackRows})
	e.contacts = map[string][]proto.Contact{"whatsapp": {
		{ID: waGrp, DisplayName: "The group", Protocol: "whatsapp", Extras: map[string]any{"is_group": true}},
	}}
	e.addressBook = map[string][]proto.Contact{"whatsapp": {
		{ID: "111@lid", DisplayName: "Ann", Protocol: "whatsapp", Extras: map[string]any{"phone": "39111"}},
		{ID: "777@c.us", DisplayName: "Carl", Protocol: "whatsapp", Extras: map[string]any{"phone": "4455"}},
	}}
	return e
}

// TestPageGroupSenderFallback: a group row whose sender is an unknown "@lid"
// shows the annotated placeholder on messages.page, never the raw JID and never
// bare LID digits that read like a phone number (mc-zao.5.7). It stays a
// placeholder, so a later name can replace it.
func TestPageGroupSenderFallback(t *testing.T) {
	e := newGroupFallbackEnv(t)
	byText := map[string]map[string]any{}
	for _, m := range e.page(map[string]any{"protocol": "whatsapp", "contact_id": waGrp}) {
		byText[m["text"].(string)] = m
	}
	want := map[string]string{
		"lid stranger": "Contatto WhatsApp (ID 20508603076702)",
		"known lid":    "Ann", // the address book still wins over the placeholder
		"phone jid":    "222", // "@c.us" keeps Python's bare number
		"lid quote":    "Bea", // the sender itself is a real name
		"phone quote":  "Bea",
	}
	for text, sender := range want {
		m, ok := byText[text]
		if !ok {
			t.Fatalf("row %q missing from page: %v", text, byText)
		}
		if m["sender"] != sender {
			t.Errorf("%q sender = %v, want %q", text, m["sender"], sender)
		}
	}
	if got := byText["stored placeholder"]["sender"]; got != "Contatto WhatsApp (ID 20508603076702)" {
		t.Errorf("stored placeholder sender = %v", got)
	}
	// Quote authors go through the same resolver.
	if got := byText["lid quote"]["quote_author"]; got != "Contatto WhatsApp (ID 999)" {
		t.Errorf("lid quote author = %v", got)
	}
	if got := byText["phone quote"]["quote_author"]; got != "Carl" {
		t.Errorf("phone quote author = %v", got)
	}
	// Nothing on the wire may leak a LID (placeholder, or a bare LID number).
	for _, m := range byText {
		for _, field := range []string{"sender", "quote_author"} {
			s, _ := m[field].(string)
			if strings.Contains(s, "@lid") || s == "20508603076702" || s == "999" {
				t.Errorf("%v.%s leaks the lid: %q", m["text"], field, s)
			}
		}
	}
}

// TestWindowSnapshotGroupLIDsPlaceholder is mc-zao.5.8. A group row ingested
// before mc-zao.5.7 keeps the raw "<n>@lid" in sender/quote_author, and
// messages.window / messages.snapshot return cache rows verbatim, so the
// placeholder is substituted at display time only: the stored row is left
// untouched (messages.page still resolves "111@lid" to Ann), a "@c.us" sender
// keeps Python's shape, and a stored placeholder passes through unchanged.
func TestWindowSnapshotGroupLIDsPlaceholder(t *testing.T) {
	e := newGroupFallbackEnv(t)
	byID := func(rows []map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, r := range rows {
			out[r["id"].(string)] = r
		}
		return out
	}
	wantSenders := map[string]string{
		"L1": "Contatto WhatsApp (ID 20508603076702)", // legacy raw @lid
		"L2": "Contatto WhatsApp (ID 111)",            // legacy raw @lid, known name
		"L3": "222@c.us",                              // a JID is not a placeholder
		"L4": "Contatto WhatsApp (ID 20508603076702)", // already stored placeholder
		"L5": "Bea",
	}
	wantQuote := map[string]string{
		"L5": "Contatto WhatsApp (ID 999)", // legacy raw @lid quote author
		"L6": "777@c.us",
	}
	check := func(where string, rows map[string]map[string]any) {
		t.Helper()
		if len(rows) != 6 { // L1..L6
			t.Fatalf("%s rows = %d, want 6", where, len(rows))
		}
		for id, want := range wantSenders {
			if got := rows[id]["sender"]; got != want {
				t.Errorf("%s %s sender = %v, want %q", where, id, got, want)
			}
		}
		for id, want := range wantQuote {
			if got := rows[id]["quote_author"]; got != want {
				t.Errorf("%s %s quote_author = %v, want %q", where, id, got, want)
			}
		}
	}

	var win struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(e.mustCall(api.OpMessagesWindow, map[string]any{
		"protocol": "whatsapp", "contact_id": waGrp,
	}), &win); err != nil {
		t.Fatal(err)
	}
	check("window", byID(win.Messages))

	var snap map[string][]map[string]any
	if err := json.Unmarshal(e.mustCall(api.OpMessagesSnapshot, map[string]any{"protocol": "whatsapp"}), &snap); err != nil {
		t.Fatal(err)
	}
	check("snapshot", byID(snap[waGrp]))

	// Display-time only: nothing rewrote the shared database, so messages.page
	// still resolves the known LID through the address book.
	stored, err := e.st.ListMessages(context.Background(), store.MessageQuery{Protocol: "whatsapp", Keys: []string{waGrp}})
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]string{}
	for _, m := range stored {
		raw[store.Deref(m.MsgID)] = store.Deref(m.Sender)
	}
	if raw["L1"] != "20508603076702@lid" || raw["L2"] != "111@lid" {
		t.Fatalf("stored senders were rewritten: %v", raw)
	}
	if got := byID(e.page(map[string]any{"protocol": "whatsapp", "contact_id": waGrp}))["L2"]["sender"]; got != "Ann" {
		t.Errorf("page L2 sender = %v, want Ann", got)
	}
}
