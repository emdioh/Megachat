package web

import (
	"encoding/json"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/bus"
)

func legacy(t *testing.T, typ bus.EventType, p map[string]any) string {
	t.Helper()
	f, ok := toLegacy(bus.Event{Type: typ, Payload: p})
	if !ok {
		return "<none>"
	}
	b, _ := json.Marshal(f)
	return string(b)
}

func TestToLegacy(t *testing.T) {
	cases := []struct {
		name string
		typ  bus.EventType
		p    map[string]any
		want string
	}{
		{"backend_status skipped", bus.BackendStatus, map[string]any{"protocol": "signal"}, "<none>"},
		{"gap skipped", bus.Gap, map[string]any{"reason": "buffer"}, "<none>"},
		{"sent_mirror skipped", bus.SentMirror, map[string]any{}, "<none>"},
		{"web_push verbatim", bus.WebPush, map[string]any{"event": map[string]any{"type": "x", "payload": map[string]any{"a": 1}}},
			`{"type":"x","payload":{"a":1}}`},
		{"web_push non-object", bus.WebPush, map[string]any{"event": "nope"}, "<none>"},
		{"message not added", bus.Message, map[string]any{"protocol": "signal", "contact_id": "c", "added": false, "changed": false}, "<none>"},
		{"message changed", bus.Message, map[string]any{"protocol": "signal", "contact_id": "c", "timestamp": 5, "changed": true,
			"message": map[string]any{"id": "m", "is_mine": 1}},
			`{"type":"message","payload":{"id":"m","protocol":"signal","contact_id":"c","timestamp":5,"is_mine":true}}`},
		{"message null id", bus.Message, map[string]any{"protocol": "signal", "contact_id": "c", "added": true, "message": map[string]any{"id": nil}},
			`{"type":"message","payload":{"id":null,"protocol":"signal","contact_id":"c","timestamp":null,"is_mine":false}}`},
		{"message_edit", bus.MessageEdit, map[string]any{"protocol": "signal", "contact_id": "c", "text": "new",
			"info": map[string]any{"message_id": 12, "timestamp": 99, "old_text": nil, "is_mine": true}},
			`{"type":"message_edit","payload":{"protocol":"signal","contact_id":"c","message_id":"12","timestamp":99,"old_text":"","text":"new","is_mine":true}}`},
		{"reaction_update", bus.ReactionUpdate, map[string]any{"protocol": "signal", "contact_id": "c",
			"info": map[string]any{"message_id": "m", "timestamp": 3, "reactions": []any{map[string]any{"emoji": "x"}}}},
			`{"type":"reaction_update","payload":{"protocol":"signal","contact_id":"c","message_id":"m","timestamp":3,"reactions":[{"emoji":"x"}]}}`},
		{"receipt", bus.Receipt, map[string]any{"protocol": "signal", "contact_id": "c", "updated": []any{
			map[string]any{"id": 5, "timestamp": 10, "status": "read", "text": "t"},
			map[string]any{"id": nil, "status": nil}}},
			`{"type":"receipt","payload":{"protocol":"signal","contact_id":"c","updates":[{"id":"5","timestamp":10,"status":"read","text":"t"},{"id":null,"timestamp":0,"status":"sent","text":""}]}}`},
		{"typing", bus.Typing, map[string]any{"protocol": "signal", "contact_id": "c", "action": "STOPPED"},
			`{"type":"typing","payload":{"protocol":"signal","contact_id":"c","action":"STOPPED"}}`},
		{"contact_update", bus.ContactUpdate, map[string]any{"protocol": "signal", "contact_id": "c", "display_name": "N", "phone": nil, "merged_into": "x"},
			`{"type":"contact_update","payload":{"protocol":"signal","contact_id":"c","display_name":"N","phone":null}}`},
		{"contact_update merge", bus.ContactUpdate, map[string]any{"protocol": "whatsapp", "contact_id": "c", "merged_into": "lid", "display_name": "N"},
			`{"type":"contact_update","payload":{"protocol":"whatsapp","contact_id":"c","merged_into":"lid","display_name":"N","phone":null}}`},
		{"lid default", bus.LidWarmupDone, map[string]any{"contact_id": ""}, `{"type":"lid_warmup_done","payload":{"protocol":"whatsapp"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := legacy(t, c.typ, c.p)
			if got == "<none>" || c.want == "<none>" {
				if got != c.want {
					t.Fatalf("got %s want %s", got, c.want)
				}
				return
			}
			jsonEq(t, json.RawMessage(got), c.want)
		})
	}
}
