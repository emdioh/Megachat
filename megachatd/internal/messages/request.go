package messages

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// Request decoding with pydantic lax semantics ("true" for a bool, "10" for an
// int, unknown members ignored); every failure is invalid_request "Invalid
// request" with pydantic-shaped details.

type fieldError struct {
	Type string   `json:"type"`
	Loc  []string `json:"loc"`
	Msg  string   `json:"msg"`
}

func invalid(errs ...fieldError) *ops.Error {
	return &ops.Error{Code: api.CodeInvalidRequest, Message: "Invalid request", Details: errs}
}

type fields map[string]json.RawMessage

func parseFields(params json.RawMessage) (fields, *ops.Error) {
	var f fields
	if err := json.Unmarshal(params, &f); err != nil || f == nil {
		return nil, invalid(fieldError{"model_attributes_type", []string{}, "Input should be a valid dictionary or object to extract fields from"})
	}
	return f, nil
}

func (f fields) get(name string) (json.RawMessage, bool) {
	raw, ok := f[name]
	return raw, ok
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

// protocolField is Literal["signal", "whatsapp", "telegram"].
func (f fields) protocolField() (string, *ops.Error) {
	raw, ok := f.get("protocol")
	if !ok {
		return "", invalid(fieldError{"missing", []string{"protocol"}, "Field required"})
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || (s != api.ProtocolSignal && s != api.ProtocolWhatsApp && s != api.ProtocolTelegram) {
		return "", invalid(fieldError{"literal_error", []string{"protocol"}, "Input should be 'signal', 'whatsapp' or 'telegram'"})
	}
	return s, nil
}

func (f fields) stringField(name string) (string, *ops.Error) {
	raw, ok := f.get(name)
	if !ok {
		return "", invalid(fieldError{"missing", []string{name}, "Field required"})
	}
	var s string
	if isNull(raw) || json.Unmarshal(raw, &s) != nil {
		return "", invalid(fieldError{"string_type", []string{name}, "Input should be a valid string"})
	}
	return s, nil
}

func (f fields) boolField(name string) (bool, *ops.Error) {
	raw, ok := f.get(name)
	if !ok {
		return false, nil
	}
	if isNull(raw) {
		return false, invalid(fieldError{"bool_type", []string{name}, "Input should be a valid boolean"})
	}
	bad := invalid(fieldError{"bool_parsing", []string{name}, "Input should be a valid boolean, unable to interpret input"})
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b, nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		switch n.String() {
		case "1":
			return true, nil
		case "0":
			return false, nil
		}
		return false, bad
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "t", "yes", "y", "on", "1":
			return true, nil
		case "false", "f", "no", "n", "off", "0":
			return false, nil
		}
	}
	return false, bad
}

// intField reads an int (lax: 10.0 and "10" are accepted). present=false for
// a missing member; null is returned as present=false too.
func (f fields) intField(name string) (v int64, present bool, err *ops.Error) {
	raw, ok := f.get(name)
	if !ok || isNull(raw) {
		return 0, false, nil
	}
	bad := invalid(fieldError{"int_parsing", []string{name}, "Input should be a valid integer, unable to parse string as an integer"})
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		if i, e := strconv.ParseInt(n.String(), 10, 64); e == nil {
			return i, true, nil
		}
		if fl, e := strconv.ParseFloat(n.String(), 64); e == nil && fl == math.Trunc(fl) && math.Abs(fl) < 1<<63 {
			return int64(fl), true, nil
		}
		return 0, false, invalid(fieldError{"int_from_float", []string{name}, "Input should be a valid integer, got a number with a fractional part"})
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if i, e := strconv.ParseInt(strings.TrimSpace(s), 10, 64); e == nil {
			return i, true, nil
		}
	}
	return 0, false, bad
}

// decodePage decodes messages.page.
func decodePage(params json.RawMessage) (api.MessagesPageRequest, *ops.Error) {
	var req api.MessagesPageRequest
	f, err := parseFields(params)
	if err != nil {
		return req, err
	}
	if req.Protocol, err = f.protocolField(); err != nil {
		return req, err
	}
	if req.ContactID, err = f.stringField("contact_id"); err != nil {
		return req, err
	}
	if req.Refresh, err = f.boolField("refresh"); err != nil {
		return req, err
	}
	if v, ok, e := f.intField("limit"); e != nil {
		return req, e
	} else if ok {
		n := int(v)
		req.Limit = &n
	}
	if v, ok, e := f.intField("since"); e != nil {
		return req, e
	} else if ok {
		req.Since = &v
	}
	return req, nil
}

// decodeWindow decodes messages.window (refresh_limit: 1..200, default 50).
func decodeWindow(params json.RawMessage) (api.MessagesWindowRequest, *ops.Error) {
	req := api.MessagesWindowRequest{RefreshLimit: 50}
	f, err := parseFields(params)
	if err != nil {
		return req, err
	}
	if req.Protocol, err = f.protocolField(); err != nil {
		return req, err
	}
	if req.ContactID, err = f.stringField("contact_id"); err != nil {
		return req, err
	}
	if req.Refresh, err = f.boolField("refresh"); err != nil {
		return req, err
	}
	if raw, ok := f.get("refresh_limit"); ok {
		v, present, e := f.intField("refresh_limit")
		if e != nil {
			return req, e
		}
		if !present && isNull(raw) {
			return req, invalid(fieldError{"int_type", []string{"refresh_limit"}, "Input should be a valid integer"})
		}
		if v < 1 {
			return req, invalid(fieldError{"greater_than_equal", []string{"refresh_limit"}, "Input should be greater than or equal to 1"})
		}
		if v > maxRefreshLimit {
			return req, invalid(fieldError{"less_than_equal", []string{"refresh_limit"}, "Input should be less than or equal to 200"})
		}
		req.RefreshLimit = int(v)
	}
	return req, nil
}

// decodeSnapshot decodes messages.snapshot.
func decodeSnapshot(params json.RawMessage) (api.MessagesSnapshotRequest, *ops.Error) {
	var req api.MessagesSnapshotRequest
	f, err := parseFields(params)
	if err != nil {
		return req, err
	}
	req.Protocol, err = f.protocolField()
	return req, err
}
