package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// Protocol identifiers (models.py).
const (
	ProtocolSignal   = "signal"
	ProtocolWhatsApp = "whatsapp"
	ProtocolTelegram = "telegram"
)

// Protocols in display order (server/ops/common.py PROTOCOL_ORDER).
var Protocols = []string{ProtocolSignal, ProtocolWhatsApp, ProtocolTelegram}

// IsProtocol reports whether p is one of the three known protocols.
func IsProtocol(p string) bool {
	return p == ProtocolSignal || p == ProtocolWhatsApp || p == ProtocolTelegram
}

// Ptr returns a pointer to v (for nullable wire members).
func Ptr[T any](v T) *T { return &v }

// Limits shared by several operations.
const (
	MaxTextLength    = 64 * 1024 // messages.send / messages.edit text, characters
	MaxMessagesLimit = 2000      // messages.page limit upper bound
	MaxAttachments   = 10        // media attachments per send
	MaxUploadBytes   = 20 << 20  // images; video 100 MiB, audio/document 50 MiB
	MaxTotalUpload   = 250 << 20 // request-wide cap (web) / staged total (socket)
	GmailMaxAttach   = 10
	GmailMaxFileSize = 25 << 20
	GmailMaxTotal    = 25 << 20
	DownloadPort     = 10042 // temporary download server (download.* ops)
)

// DefaultDeviceName is the Signal linked-device name (models.py).
const DefaultDeviceName = "Signal-TUI-Client"

// ThumbWidths are the accepted media.thumbnail widths.
var ThumbWidths = []int{96, 240, 480}

// RawIsNull reports whether a captured JSON value is absent or the literal null.
func RawIsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// RawString decodes a JSON string; ok is false for anything else.
func RawString(raw json.RawMessage) (s string, ok bool) {
	if err := json.Unmarshal(raw, &s); err != nil || RawIsNull(raw) {
		return "", false
	}
	return s, true
}

// RawInt decodes a JSON integer the way Python's isinstance(v, int) and not
// bool does: "12" yes, "12.0" / "1e3" / true / "12" (string) no.
func RawInt(raw json.RawMessage) (n int64, ok bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || strings.ContainsAny(t, ".eE") || t == "true" || t == "false" || t == "null" {
		return 0, false
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// jsonKeys returns the JSON member names of a struct type.
func jsonKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys[name] = true
	}
	return keys
}

// marshalWithExtra marshals the struct v (already converted to an alias type
// without methods) and merges extra members that are not struct fields.
func marshalWithExtra(v any, extra map[string]json.RawMessage) ([]byte, error) {
	base, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return base, nil
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, raw := range extra {
		if _, taken := m[k]; !taken {
			m[k] = raw
		}
	}
	return json.Marshal(m)
}

// unmarshalWithExtra decodes data into v (alias pointer) and returns the
// members that are not struct fields.
func unmarshalWithExtra(data []byte, v any) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	known := jsonKeys(reflect.TypeOf(v).Elem())
	extra := map[string]json.RawMessage{}
	for k, raw := range all {
		if !known[k] {
			extra[k] = raw
		}
	}
	if len(extra) == 0 {
		return nil, nil
	}
	return extra, nil
}
