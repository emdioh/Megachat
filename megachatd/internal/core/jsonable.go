// Package core is the Go port of server/core.py (ChatCore) and the operation
// handlers of server/ops/{meta,contacts,send,edit,reactions}.py.
//
// The core owns the bus, the store, the proto.Manager and the config. It turns
// backend events (proto.Event) into store writes and bus events with the exact
// wire payloads of CONTRACTS.md section 5, and registers its operations on an
// *ops.Registry with RegisterOps.
package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// jsonable coerces v to plain JSON data (server/events_api.py jsonable):
// contacts become ContactWire maps, sets sorted lists, []byte base64 text,
// anything else is round-tripped through encoding/json. Never panics.
func jsonable(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return t
	case proto.Contact:
		return wireMap(t.ToWire())
	case *proto.Contact:
		if t == nil {
			return nil
		}
		return wireMap(t.ToWire())
	case api.ContactWire:
		return wireMap(t)
	case *api.ContactWire:
		if t == nil {
			return nil
		}
		return wireMap(*t)
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = jsonable(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = jsonable(x)
		}
		return out
	case []string:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = x
		}
		return out
	case json.RawMessage:
		return decodeNumber(t)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			keys := rv.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			out := make(map[string]any, len(keys))
			for _, k := range keys {
				out[k.String()] = jsonable(rv.MapIndex(k).Interface())
			}
			return out
		}
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return []any{}
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = jsonable(rv.Index(i).Interface())
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return jsonable(rv.Elem().Interface())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return decodeNumber(b)
}

func decodeNumber(b []byte) any {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil
	}
	return out
}

// wireMap converts a ContactWire into a plain map (extras never null).
func wireMap(c api.ContactWire) map[string]any {
	return map[string]any{
		"id":              c.ID,
		"display_name":    c.DisplayName,
		"protocol":        c.Protocol,
		"cache_key":       c.CacheKey,
		"last_message_ts": c.LastMessageTs,
		"extras":          jsonable(api.EmptyMapIfNil(c.Extras)),
	}
}

// toMap marshals a typed payload into the map[string]any the bus carries,
// preserving numbers exactly (json.Number).
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	m, _ := decodeNumber(b).(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// ---- tolerant accessors for event payloads ----------------------------------

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// asInt64 accepts the integer spellings a payload may carry.
func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case uint64:
		return int64(t), true
	case float64:
		if t == float64(int64(t)) {
			return int64(t), true
		}
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	}
	return 0, false
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return v != nil
}

func strPtr(s string) *string { return &s }
