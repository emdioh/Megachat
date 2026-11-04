package web

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// normalizePayload round-trips a bus payload through JSON so that typed
// producers (structs, ints, pointers) look like decoded JSON (numbers are
// json.Number to keep integers exact).
func normalizePayload(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil
	}
	return out
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// pyStr mimics Python str() for decoded JSON values.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case json.Number:
		return t.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// pyInt mimics Python int() for decoded JSON values.
func pyInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
		if f, err := t.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return n, true
		}
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func orEmpty(v any) string {
	if !truthy(v) {
		return ""
	}
	return pyStr(v)
}

// toLegacy maps a bus event to the legacy /ws frame (web/legacy_events.py
// to_legacy). ok is false when the event has no frame.
func toLegacy(ev bus.Event) (frame map[string]any, ok bool) {
	p := normalizePayload(ev.Payload)
	if p == nil {
		return nil, false
	}
	mk := func(t string, payload map[string]any) (map[string]any, bool) {
		return map[string]any{"type": t, "payload": payload}, true
	}
	switch ev.Type {
	case bus.WebPush:
		if f, isMap := p["event"].(map[string]any); isMap {
			return f, true
		}
		return nil, false

	case bus.Message:
		if !truthy(p["added"]) && !truthy(p["changed"]) {
			return nil, false
		}
		msg, _ := p["message"].(map[string]any)
		if msg == nil {
			msg = map[string]any{}
		}
		return mk("message", map[string]any{
			"id":         msg["id"],
			"protocol":   p["protocol"],
			"contact_id": p["contact_id"],
			"timestamp":  p["timestamp"],
			"is_mine":    truthy(msg["is_mine"]),
		})

	case bus.MessageEdit:
		info, _ := p["info"].(map[string]any)
		if info == nil {
			return nil, false
		}
		ts, _ := pyInt(info["timestamp"])
		return mk("message_edit", map[string]any{
			"protocol":   p["protocol"],
			"contact_id": p["contact_id"],
			"message_id": pyStr(info["message_id"]),
			"timestamp":  ts,
			"old_text":   orEmpty(info["old_text"]),
			"text":       p["text"],
			"is_mine":    truthy(info["is_mine"]),
		})

	case bus.ReactionUpdate:
		info, _ := p["info"].(map[string]any)
		if info == nil {
			return nil, false
		}
		ts, _ := pyInt(info["timestamp"])
		reactions := info["reactions"]
		if reactions == nil {
			reactions = []any{}
		}
		return mk("reaction_update", map[string]any{
			"protocol":   p["protocol"],
			"contact_id": p["contact_id"],
			"message_id": pyStr(info["message_id"]),
			"timestamp":  ts,
			"reactions":  reactions,
		})

	case bus.Receipt:
		updates := []any{}
		list, _ := p["updated"].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			if m == nil {
				m = map[string]any{}
			}
			var id any
			if truthy(m["id"]) {
				id = pyStr(m["id"])
			}
			var ts int64
			if truthy(m["timestamp"]) {
				ts, _ = pyInt(m["timestamp"])
			}
			status := "sent"
			if truthy(m["status"]) {
				status = pyStr(m["status"])
			}
			updates = append(updates, map[string]any{
				"id": id, "timestamp": ts, "status": status, "text": orEmpty(m["text"]),
			})
		}
		return mk("receipt", map[string]any{
			"protocol": p["protocol"], "contact_id": p["contact_id"], "updates": updates,
		})

	case bus.Typing:
		return mk("typing", map[string]any{
			"protocol": p["protocol"], "contact_id": p["contact_id"], "action": p["action"],
		})

	case bus.ContactUpdate:
		out := map[string]any{
			"protocol":     p["protocol"],
			"contact_id":   p["contact_id"],
			"display_name": p["display_name"],
			"phone":        p["phone"],
		}
		if truthy(p["merged_into"]) && p["protocol"] == "whatsapp" {
			out["merged_into"] = pyStr(p["merged_into"])
		}
		return mk("contact_update", out)

	case bus.LidWarmupDone:
		proto, has := p["protocol"]
		if !has {
			proto = "whatsapp"
		}
		return mk("lid_warmup_done", map[string]any{"protocol": proto})
	}
	return nil, false
}

