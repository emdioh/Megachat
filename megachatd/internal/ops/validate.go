package ops

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

// Pydantic-shaped request validation for the operations whose handlers do not
// decode with pydantic semantics themselves. Python reports EVERY offending
// field, in declaration order, as {"type","loc","msg"}; the Go handlers stop at
// the first problem (or report a Go decoder message). This table lists, per
// operation, the required fields and the types whose rejection is certain in
// pydantic lax mode (str never accepts a number or null; a strict bool never
// accepts anything but a bool; a Literal only its members). Lax-coerced types
// (int, bool) are only checked for presence. A request that passes here is left
// to the handler untouched, so this can only turn a failure into a better one.

type kind int

const (
	kStr      kind = iota // str / StrictStr: required, must be a JSON string
	kAny                  // required, any value (Stripped, int, list, ...)
	kBool                 // StrictBool: required, must be a JSON bool
	kLit3                 // Literal["signal","whatsapp","telegram"], required
	kLit1                 // Literal["signal"], optional
	kOptStr               // str | None, optional
	kBoolNull             // StrictBool | None, required
	kInt                  // int (lax): required; strings must parse, null/containers rejected
	kStripped             // Stripped (str(value or "").strip()): required, any JSON accepted
)

type fieldSpec struct {
	name string
	kind kind
}

func s(n string) fieldSpec  { return fieldSpec{n, kStr} }
func a(n string) fieldSpec  { return fieldSpec{n, kAny} }
func i(n string) fieldSpec  { return fieldSpec{n, kInt} }
func st(n string) fieldSpec { return fieldSpec{n, kStripped} }

var requestSpecs = map[string][]fieldSpec{
	api.OpReactionsAvailable:      {s("protocol")},
	api.OpContactsBook:            {s("q")},
	api.OpContactsList:            {{"q", kOptStr}},
	api.OpContactsForProtocol:     {st("protocol")},
	api.OpContactsRegister:        {st("id"), s("display_name"), st("protocol")},
	api.OpContactsCheckNumber:     {st("phone")},
	api.OpContactsAvatar:          {s("protocol"), s("contact_id")},
	api.OpChatsMarkRead:           {st("protocol"), st("contact_id")},
	api.OpChatsArchive:            {st("protocol"), st("contact_id"), {"archived", kBool}},
	api.OpMessagesEdit:            {s("protocol"), s("contact_id"), s("message_id"), s("new_text")},
	api.OpReactionsSet:            {s("protocol"), s("contact_id"), s("message_id"), s("emoji")},
	api.OpMediaAttachment:         {s("protocol"), s("attachment_id")},
	api.OpMediaThumbnail:          {s("protocol"), s("attachment_id"), i("width")},
	api.OpMediaQuote:              {s("protocol"), i("message_row_id")},
	api.OpMediaQuotedAttachment:   {s("protocol"), s("contact_key")},
	api.OpLinkStart:               {{"protocol", kLit3}},
	api.OpLinkPoll:                {{"protocol", kLit3}},
	api.OpLinkCancel:              {{"protocol", kLit3}},
	api.OpLinkFinalize:            {{"protocol", kLit1}},
	api.OpLinkSubmit2fa:           {s("password")},
	api.OpLinkReconnect:           {a("protocols")},
	api.OpDownloadServeAttachment: {s("protocol"), s("attachment_id")},
	api.OpDownloadServeText:       {s("text")},
	api.OpLinksPreview:            {s("url")},
	api.OpLinksPreviewImage:       {s("url")},
	api.OpTranscribeStatus:        {{"protocol", kLit3}, s("attachment_id")},
	api.OpAutotranscribeSetGlobal: {{"protocol", kLit3}, {"enabled", kBool}},
	api.OpAutotranscribeChat:      {{"protocol", kLit3}, s("contact_id")},
	api.OpAutotranscribeSetChat:   {{"protocol", kLit3}, s("contact_id"), {"enabled", kBoolNull}},
	api.OpGmailMessagesGet:        {i("uid"), {"folder", kOptStr}},
	api.OpGmailMessagesDelete:     {i("uid"), {"folder", kOptStr}},
	api.OpGmailMessagesMarkRead:   {i("uid"), {"folder", kOptStr}},
	api.OpGmailMessagesMarkUnread: {i("uid"), {"folder", kOptStr}},
	api.OpGmailMessagesArchive:    {i("uid"), {"folder", kOptStr}},
	api.OpGmailAttachment:         {i("uid"), {"folder", kOptStr}, i("index")},
	api.OpGmailImage:              {s("token")},
}

func verr(typ, field, msg string) api.ValidationDetail {
	return api.ValidationDetail{Type: typ, Loc: []any{field}, Msg: msg}
}

// validateRequest returns the invalid_request error pydantic would raise, or
// nil when nothing certain is wrong.
func validateRequest(op string, params json.RawMessage) (json.RawMessage, *Error) {
	spec, ok := requestSpecs[op]
	if !ok {
		return params, nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(params, &m) != nil || m == nil {
		return params, nil // not an object: the transport rejects it earlier
	}
	rewritten := false
	var errs []api.ValidationDetail
	for _, f := range spec {
		raw, present := m[f.name]
		var str string
		isStr := present && json.Unmarshal(raw, &str) == nil
		switch f.kind {
		case kStr:
			if !present {
				errs = append(errs, verr("missing", f.name, "Field required"))
			} else if !isStr {
				errs = append(errs, verr("string_type", f.name, "Input should be a valid string"))
			}
		case kAny:
			if !present {
				errs = append(errs, verr("missing", f.name, "Field required"))
			}
		case kInt:
			if !present {
				errs = append(errs, verr("missing", f.name, "Field required"))
				break
			}
			t := strings.TrimSpace(string(raw))
			switch {
			case isStr:
				if _, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err != nil {
					errs = append(errs, verr("int_parsing", f.name, "Input should be a valid integer, unable to parse string as an integer"))
				}
			case t == "null" || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{"):
				errs = append(errs, verr("int_type", f.name, "Input should be a valid integer"))
			case t != "true" && t != "false":
				if _, err := strconv.ParseInt(t, 10, 64); err != nil {
					if fl, e2 := strconv.ParseFloat(t, 64); e2 == nil && fl != math.Trunc(fl) {
						errs = append(errs, verr("int_from_float", f.name, "Input should be a valid integer, got a number with a fractional part"))
					}
				}
			}
		case kStripped:
			if !present {
				errs = append(errs, verr("missing", f.name, "Field required"))
			} else if !isStr {
				m[f.name] = pyStr(raw)
				rewritten = true
			}
		case kOptStr:
			if present && string(raw) != "null" && !isStr {
				errs = append(errs, verr("string_type", f.name, "Input should be a valid string"))
			}
		case kBool, kBoolNull:
			var b bool
			switch {
			case !present:
				errs = append(errs, verr("missing", f.name, "Field required"))
			case f.kind == kBoolNull && string(raw) == "null":
			case json.Unmarshal(raw, &b) != nil:
				errs = append(errs, verr("bool_type", f.name, "Input should be a valid boolean"))
			}
		case kLit3:
			if !present {
				errs = append(errs, verr("missing", f.name, "Field required"))
			} else if !isStr || (str != api.ProtocolSignal && str != api.ProtocolWhatsApp && str != api.ProtocolTelegram) {
				errs = append(errs, verr("literal_error", f.name, "Input should be 'signal', 'whatsapp' or 'telegram'"))
			}
		case kLit1:
			if present && (!isStr || str != api.ProtocolSignal) {
				errs = append(errs, verr("literal_error", f.name, "Input should be 'signal'"))
			}
		}
	}
	if len(errs) > 0 {
		return params, &Error{Code: api.CodeInvalidRequest, Message: "Invalid request", Details: errs}
	}
	if rewritten {
		if b, err := json.Marshal(m); err == nil {
			return b, nil
		}
	}
	return params, nil
}

// pyStr renders a non-string JSON value the way Python's str(value or "")
// does for the scalars a client can send: falsy -> "", True -> "True",
// numbers as their JSON text. Containers are left as their JSON text.
func pyStr(raw json.RawMessage) json.RawMessage {
	t := strings.TrimSpace(string(raw))
	switch t {
	case "null", "false", "0", "0.0", "[]", "{}":
		t = ""
	case "true":
		t = "True"
	}
	b, _ := json.Marshal(t)
	return b
}
