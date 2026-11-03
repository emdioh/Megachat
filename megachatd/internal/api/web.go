package api

import "encoding/json"

// ---- authentication ----------------------------------------------------------

// Web authentication constants (web/auth.py, web/ws.py).
const (
	// AuthHeader carries "Bearer <token>" (scheme case-insensitive, token
	// compared in constant time) on every /api/* request.
	AuthHeader = "Authorization"
	// WSBearerSubprotocol must be offered by browsers together with
	// WSTokenSubprotocolPrefix+base64url(token); the server echoes
	// WSBearerSubprotocol back as the accepted subprotocol.
	WSBearerSubprotocol      = "signal-tui-bearer"
	WSTokenSubprotocolPrefix = "signal-tui-token."
	// WSAuthCloseCode is the WebSocket close code of an unauthorised socket.
	WSAuthCloseCode = 1008
	// EnvWebToken overrides web.token of config.json.
	EnvWebToken = "SIGNAL_TUI_WEB_TOKEN"
	// DefaultWebPort / DefaultWebHost are the web.port / web.host defaults.
	DefaultWebPort = 4242
	DefaultWebHost = "127.0.0.1"
)

// Unauthorized401 is the body of a 401 (with header "WWW-Authenticate: Bearer").
var Unauthorized401 = HTTPError{Detail: "Unauthorized"}

// Forbidden403 is the body of a cross-origin POST /api/send.
var Forbidden403 = HTTPError{Detail: "Forbidden"}

// HealthResponse is GET /health (unauthenticated). The SPA reads auth_required
// before deciding to show the token dialog.
type HealthResponse struct {
	Status       string `json:"status"`
	Port         int    `json:"port"`
	AuthRequired bool   `json:"auth_required"`
}

// ---- WebSocket /ws (legacy frames) ----------------------------------------------

// Legacy frame types written on /ws. The SPA ignores frames without payload.
const (
	FrameHeartbeat      = "heartbeat" // {"type":"heartbeat"} (no payload), every 5 s idle
	FrameMessage        = "message"
	FrameMessageEdit    = "message_edit"
	FrameReactionUpdate = "reaction_update"
	FrameReceipt        = "receipt"
	FrameTyping         = "typing"
	FrameContactUpdate  = "contact_update"
	FrameLidWarmupDone  = "lid_warmup_done"
	FrameSendRetry      = "send_retry"
	FrameLinkRequired   = "link_required"
	FrameGmailUnread    = "gmail_unread"
)

// LegacyFrameTypes lists the frame types that carry a payload.
var LegacyFrameTypes = []string{
	FrameMessage, FrameMessageEdit, FrameReactionUpdate, FrameReceipt, FrameTyping,
	FrameContactUpdate, FrameLidWarmupDone, FrameSendRetry, FrameLinkRequired,
	FrameGmailUnread,
}

// WebFrame is one legacy /ws frame {"type","payload"}; the same object is what
// web_push events carry in payload.event. Payload is one of the Legacy*
// structs below.
type WebFrame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// HeartbeatFrame is the idle frame of /ws and /ws/v2: {"type":"heartbeat"}.
type HeartbeatFrame struct {
	Type string `json:"type"`
}

// LegacyMessagePayload is the payload of a "message" frame built from a bus
// message event (to_legacy): id is the message id or null; timestamp is the
// event timestamp.
type LegacyMessagePayload struct {
	ID        *string `json:"id"`
	Protocol  string  `json:"protocol"`
	ContactID string  `json:"contact_id"`
	Timestamp int64   `json:"timestamp"`
	IsMine    bool    `json:"is_mine"`
}

// LegacyMessageSentPayload is the payload of the "message" frame published by
// messages.send after a successful non-optimistic send:
// {"protocol","contact_id","is_mine":true}. (The optimistic flavour publishes
// a LegacyMessagePayload with the row id and the client timestamp, only when
// the pending row was newly added.)
type LegacyMessageSentPayload struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	IsMine    bool   `json:"is_mine"`
}

// LegacyMessageEditPayload is the payload of a "message_edit" frame (from the
// bus and from messages.edit).
type LegacyMessageEditPayload struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	MessageID string `json:"message_id"`
	Timestamp int64  `json:"timestamp"`
	OldText   string `json:"old_text"`
	Text      string `json:"text"`
	IsMine    bool   `json:"is_mine"`
}

// LegacyReactionUpdatePayload is the payload of a "reaction_update" frame (from
// the bus and from reactions.set).
type LegacyReactionUpdatePayload struct {
	Protocol  string     `json:"protocol"`
	ContactID string     `json:"contact_id"`
	MessageID string     `json:"message_id"`
	Timestamp int64      `json:"timestamp"`
	Reactions []Reaction `json:"reactions"`
}

// MarshalJSON emits reactions as [] instead of null.
func (r LegacyReactionUpdatePayload) MarshalJSON() ([]byte, error) {
	type alias LegacyReactionUpdatePayload
	r.Reactions = EmptyIfNil(r.Reactions)
	return json.Marshal(alias(r))
}

// LegacyReceiptUpdate is one element of LegacyReceiptPayload.Updates:
// id = str(msg.id) or null, timestamp = int(msg.timestamp or 0), status
// defaults to "sent", text defaults to "".
type LegacyReceiptUpdate struct {
	ID        *string `json:"id"`
	Timestamp int64   `json:"timestamp"`
	Status    string  `json:"status"`
	Text      string  `json:"text"`
}

// LegacyReceiptPayload is the payload of a "receipt" frame.
type LegacyReceiptPayload struct {
	Protocol  string                `json:"protocol"`
	ContactID string                `json:"contact_id"`
	Updates   []LegacyReceiptUpdate `json:"updates"`
}

// MarshalJSON emits updates as [] instead of null.
func (r LegacyReceiptPayload) MarshalJSON() ([]byte, error) {
	type alias LegacyReceiptPayload
	r.Updates = EmptyIfNil(r.Updates)
	return json.Marshal(alias(r))
}

// LegacyTypingPayload is the payload of a "typing" frame.
type LegacyTypingPayload struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	Action    string `json:"action"`
}

// LegacyContactUpdatePayload is the payload of a "contact_update" frame.
// merged_into is present only for a WhatsApp merge.
type LegacyContactUpdatePayload struct {
	Protocol    string  `json:"protocol"`
	ContactID   string  `json:"contact_id"`
	MergedInto  *string `json:"merged_into,omitempty"`
	DisplayName *string `json:"display_name"`
	Phone       *string `json:"phone"`
}

// LegacyLidWarmupPayload is the payload of a "lid_warmup_done" frame (protocol
// defaults to "whatsapp").
type LegacyLidWarmupPayload struct {
	Protocol string `json:"protocol"`
}

// SendRetryPayload is the payload of a "send_retry" frame, published while
// messages.send backs off before attempt N+1 (never when retry=false).
type SendRetryPayload struct {
	ClientMsgID string `json:"client_msg_id"`
	Protocol    string `json:"protocol"`
	ContactID   string `json:"contact_id"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	Error       string `json:"error"`
}

// LinkRequiredPayload is the payload of a "link_required" frame (WhatsApp
// session needs/no longer needs a QR scan).
type LinkRequiredPayload struct {
	Protocol string `json:"protocol"`
	Required bool   `json:"required"`
}

// GmailUnreadPayload is the payload of a "gmail_unread" frame (published only
// when the unread count or UIDNEXT changed; new_mail when UIDNEXT grew).
type GmailUnreadPayload struct {
	Unread  int  `json:"unread"`
	NewMail bool `json:"new_mail"`
}

// ---- WebSocket /ws/v2 ----------------------------------------------------------------

// V2 client frame / server error types.
const (
	V2Subscribe     = "subscribe"
	V2Error         = "error"
	V2ReasonInvalid = "invalid_subscribe"
)

// V2SubscribeFrame is the only client frame: {"type":"subscribe","since":N}.
// since may be an int >= 0 or a digit string; anything else (bool, negative,
// float, junk) is answered with a V2ErrorFrame and ignored. Non-subscribe
// frames and non-JSON text are ignored silently. Since is raw JSON for that
// reason (absent/null = live only).
type V2SubscribeFrame struct {
	Type  string          `json:"type"`
	Since json.RawMessage `json:"since,omitempty"`
}

// V2ErrorPayload is the payload of a V2ErrorFrame.
type V2ErrorPayload struct {
	Reason string `json:"reason"`
}

// V2ErrorFrame is {"type":"error","payload":{"reason":"invalid_subscribe"}}.
type V2ErrorFrame struct {
	Type    string         `json:"type"`
	Payload V2ErrorPayload `json:"payload"`
}

// The /ws/v2 event frame is Event ({seq,type,payload,ts}); the idle frame is
// HeartbeatFrame.

// ---- REST bodies that are not an operation result ---------------------------------------

// SendJSONBody is the JSON body of POST /api/send without files; it is the
// MessagesSendRequest minus attachments/retry/optimistic (the route removes
// optimistic and retry and overwrites attachments). Multipart bodies carry the
// same members as form fields plus repeated "file" parts.
type SendJSONBody = MessagesSendRequest

// LogBody is the body of POST /api/log ({"message": any}).
type LogBody struct {
	Message json.RawMessage `json:"message,omitempty"`
}

// TranscribeBody is the body of POST /api/transcribe.
type TranscribeBody struct {
	Protocol     json.RawMessage `json:"protocol,omitempty"`
	AttachmentID json.RawMessage `json:"attachment_id,omitempty"`
}

// EnabledBody is the body of PUT /api/autotranscribe/global/{proto} and
// PUT /api/autotranscribe/chat. For the chat route "enabled" is mandatory
// (422 "enabled richiesto" when absent); null clears the override.
type EnabledBody struct {
	Enabled json.RawMessage `json:"enabled,omitempty"`
}

// GmailBulkDeleteBody is the body of POST /api/gmail/messages/bulk-delete.
type GmailBulkDeleteBody = GmailMessagesBulkDeleteRequest

// LinkFinalizeBody is the body of POST /api/link/finalize.
type LinkFinalizeBody struct {
	Phone json.RawMessage `json:"phone,omitempty"`
}

// OpenAIKeyBody is the body of POST /api/config/openai-key.
type OpenAIKeyBody = OpenAIKeyRequest
