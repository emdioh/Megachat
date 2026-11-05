package messages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func cacheRows(*config.Config) []row {
	return []row{
		{"contact_number": sig, "text": "one", "is_mine": 0, "sender": sig, "timestamp": at(10), "msg_id": "a1"},
		{"contact_number": "+39222", "text": "two", "is_mine": 1, "timestamp": at(5), "msg_id": "b1", "status": "sent", "edited": 1},
		{"contact_number": sig, "text": "caption", "is_mine": 0, "timestamp": at(20), "msg_id": "a2", "msg_type": "image", "attachment_id": "i.jpg", "content_type": "image/jpeg", "media_kind": "image"},
		{"protocol": "whatsapp", "contact_number": waGhost, "text": "hello", "timestamp": at(1), "msg_id": "W1"},
		{"protocol": "whatsapp", "contact_number": waLid, "text": "hello", "timestamp": at(1), "msg_id": "W1"},
		{"protocol": "whatsapp", "contact_number": waGhost, "text": "ghost only", "timestamp": at(2), "msg_id": "W2"},
		{"protocol": "whatsapp", "contact_number": waLid, "text": "lid only", "timestamp": at(3), "msg_id": "W3", "quote_text": "q", "quote_timestamp": at(1), "quote_author": "x", "reply_to_message_id": "W1"},
		{"protocol": "whatsapp", "contact_number": "orphan@lid", "text": "orphan", "timestamp": at(4), "msg_id": "W4"},
		// Legacy group rows stored before mc-zao.5.7 still carry the raw
		// "<n>@lid" sender/quote_author. window/snapshot return cache rows
		// verbatim (Python parity) and show the annotated placeholder at
		// display time (mc-zao.5.8, see lidPlaceholderFields in
		// parity_test.go).
		{"protocol": "whatsapp", "contact_number": waGrp, "text": "legacy lid", "is_mine": 0, "sender": "111@lid", "timestamp": at(7), "msg_id": "G1",
			"quote_text": "q", "quote_timestamp": at(1), "quote_author": "112@lid"},
		{"protocol": "whatsapp", "contact_number": waGrp, "text": "named sender", "is_mine": 0, "sender": "Ann", "timestamp": at(8), "msg_id": "G2"},
		{"protocol": "whatsapp", "contact_number": waGrp, "text": "phone sender", "is_mine": 0, "sender": "222@c.us", "timestamp": at(9), "msg_id": "G3"},
		{"protocol": "telegram", "contact_number": "t1", "text": "tg", "timestamp": at(6), "msg_id": "7", "batch_id": "b", "batch_index": 2},
	}
}

func newCacheEnv(t *testing.T) *env {
	e := newEnv(t, envOpts{rows: cacheRows})
	e.contacts = map[string][]proto.Contact{"whatsapp": {
		{ID: waLid, DisplayName: "Mario", Protocol: "whatsapp", Extras: map[string]any{"phone": "391", "lid": waLid}},
	}}
	return e
}

var cacheCalls = []map[string]any{
	{"name": "w_sig", "op": "window", "protocol": "signal", "contact_id": sig},
	{"name": "w_none", "op": "window", "protocol": "signal", "contact_id": "+nobody"},
	{"name": "w_ghost", "op": "window", "protocol": "whatsapp", "contact_id": waGhost},
	{"name": "w_lid", "op": "window", "protocol": "whatsapp", "contact_id": waLid},
	{"name": "w_grp", "op": "window", "protocol": "whatsapp", "contact_id": waGrp},
	{"name": "w_tg", "op": "window", "protocol": "telegram", "contact_id": "t1"},
	{"name": "s_sig", "op": "snapshot", "protocol": "signal"},
	{"name": "s_wa", "op": "snapshot", "protocol": "whatsapp"},
	{"name": "s_tg", "op": "snapshot", "protocol": "telegram"},
}

// TestParityWindowSnapshot compares messages.window / messages.snapshot with
// the Python cache loader on the same database.
func TestParityWindowSnapshot(t *testing.T) {
	compareCalls(t, newCacheEnv(t), cacheCalls)
}

func TestWindowRefresh(t *testing.T) {
	e := newCacheEnv(t)
	var out struct {
		Messages  []json.RawMessage `json:"messages"`
		Refreshed bool              `json:"refreshed"`
	}
	if err := json.Unmarshal(e.mustCall(api.OpMessagesWindow, map[string]any{"protocol": "signal", "contact_id": sig, "refresh": true, "refresh_limit": 10}), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Refreshed || len(out.Messages) != 2 {
		t.Fatalf("got %+v", out)
	}
	if n := len(e.backends["signal"].CallsTo("FetchHistory")); n != 1 {
		t.Fatalf("FetchHistory calls = %d", n)
	}
}

func TestSnapshotOrderByFirstAppearance(t *testing.T) {
	e := newCacheEnv(t)
	got := string(e.mustCall(api.OpMessagesSnapshot, map[string]any{"protocol": "signal"}))
	if !strings.HasPrefix(got, `{"+39222":[`) {
		t.Fatalf("snapshot order: %s", got)
	}
}

func TestPageValidation(t *testing.T) {
	e := newCacheEnv(t)
	for name, p := range map[string]map[string]any{
		"limit 0":      {"protocol": "signal", "contact_id": sig, "limit": 0},
		"limit big":    {"protocol": "signal", "contact_id": sig, "limit": 2001},
		"bad protocol": {"protocol": "xmpp", "contact_id": sig},
		"no contact":   {"protocol": "signal"},
		"null contact": {"protocol": "signal", "contact_id": nil},
		"bad refresh":  {"protocol": "signal", "contact_id": sig, "refresh": "maybe"},
	} {
		_, err := e.call(api.OpMessagesPage, p)
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := e.call(api.OpMessagesPage, map[string]any{"protocol": "signal", "contact_id": sig, "limit": "2", "refresh": "true"}); err != nil {
		t.Fatalf("lax coercion: %v", err)
	}
	if _, err := e.call(api.OpMessagesWindow, map[string]any{"protocol": "signal", "contact_id": sig, "refresh_limit": 201}); err == nil {
		t.Fatal("refresh_limit 201 accepted")
	}
	if got := e.page(map[string]any{"protocol": "signal", "contact_id": sig, "limit": 1}); len(got) != 1 || got[0]["id"] != "a2" {
		t.Fatalf("limit keeps newest: %v", got)
	}
	if got := string(e.mustCall(api.OpMessagesPage, map[string]any{"protocol": "signal", "contact_id": "none"})); got != "[]" {
		t.Fatalf("empty page = %s", got)
	}
}
