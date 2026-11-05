package messages

import (
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// groupFallbackRows are group rows whose sender nobody knows. They cannot go in
// pageFixture: for an unresolved "@lid" the row builder deliberately differs
// from Python (which shows the bare digits) and the byte-for-byte parity test
// there must stay green, so the divergence is pinned here instead.
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
