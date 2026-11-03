package api

import "encoding/json"

// Bus event types (server/events_api.py EVENT_MODELS plus the synthetic gap).
// The same envelope {seq, type, payload, ts} is delivered as an "event"
// notification on the Unix socket and as a frame on /ws/v2.
const (
	EventBackendConnecting = "backend_connecting"
	EventBackendStatus     = "backend_status"
	EventBackendReady      = "backend_ready"
	EventBackendDone       = "backend_done"
	EventMessage           = "message"
	EventMessageEdit       = "message_edit"
	EventReactionUpdate    = "reaction_update"
	EventReceipt           = "receipt"
	EventTyping            = "typing"
	EventContactUpdate     = "contact_update"
	EventSentMirror        = "sent_mirror"
	EventLidWarmupDone     = "lid_warmup_done"
	EventWebPush           = "web_push"
	EventGap               = "gap"
)

// EventTypes lists every event type a subscriber can receive.
var EventTypes = []string{
	EventBackendConnecting, EventBackendStatus, EventBackendReady, EventBackendDone,
	EventMessage, EventMessageEdit, EventReactionUpdate, EventReceipt, EventTyping,
	EventContactUpdate, EventSentMirror, EventLidWarmupDone, EventWebPush, EventGap,
}

// Event is the bus envelope. Seq starts at 1 and is monotonic per bus; Ts is
// Unix seconds (float). Payload is one of the structs below, marshalled.
type Event struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Ts      float64         `json:"ts"`
}

// Typing actions.
const (
	TypingStarted = "STARTED"
	TypingStopped = "STOPPED"
)

// Gap reasons. "buffer": the requested since fell out of the 2000-event ring
// (seq of the gap event = bus last seq). "overflow": the subscriber's queue
// (1000, never smaller than the ring) overflowed and was cleared (seq = the
// event that overflowed it). "restart" is synthesised by the Python client only.
const (
	GapBuffer   = "buffer"
	GapOverflow = "overflow"
	GapRestart  = "restart"
)

// GapPayload is the payload of a gap event. Since is present only for
// reason "buffer".
type GapPayload struct {
	Reason string `json:"reason"`
	Since  *int64 `json:"since,omitempty"`
}

// All payload models below are produced with exclude_unset, so a member the
// producer does not set is absent. Producers in this codebase set every member
// listed without omitempty; members with omitempty are conditional.

// BackendConnectingPayload is backend_connecting.
type BackendConnectingPayload struct {
	Protocol string `json:"protocol"`
}

// BackendStatusPayload is backend_status (persistent: keep the text until
// replaced).
type BackendStatusPayload struct {
	Protocol   string `json:"protocol"`
	Text       string `json:"text"`
	Persistent bool   `json:"persistent"`
}

// BackendReadyPayload is backend_ready (contacts = number of known contacts).
type BackendReadyPayload struct {
	Protocol string `json:"protocol"`
	Contacts int    `json:"contacts"`
}

// BackendDonePayload is backend_done; ok is always false (failure, skip or
// timeout; a ready backend implies done on the consumer side).
type BackendDonePayload struct {
	Protocol string `json:"protocol"`
	OK       bool   `json:"ok"`
}

// MessageEventPayload is message. Always emitted for every ingested message,
// even when nothing new was stored: added=true for a new message,
// changed=true when an existing identity was upgraded (optimistic -> echo).
type MessageEventPayload struct {
	Protocol       string       `json:"protocol"`
	ContactID      string       `json:"contact_id"`
	Contact        ContactWire  `json:"contact"`
	WasPlaceholder bool         `json:"was_placeholder"`
	Message        CacheMessage `json:"message"`
	Timestamp      int64        `json:"timestamp"`
	Added          bool         `json:"added"`
	Changed        bool         `json:"changed"`
}

// EditInfo is the info of message_edit (backend.apply_edit result): timestamp
// is the ORIGINAL message timestamp and never changes.
type EditInfo struct {
	MessageID string `json:"message_id"`
	Timestamp int64  `json:"timestamp"`
	OldText   string `json:"old_text"`
	Text      string `json:"text"`
	IsMine    bool   `json:"is_mine"`
}

// MessageEditPayload is message_edit.
type MessageEditPayload struct {
	Protocol  string      `json:"protocol"`
	ContactID string      `json:"contact_id"`
	Contact   ContactWire `json:"contact"`
	Info      EditInfo    `json:"info"`
	Text      string      `json:"text"`
}

// ReactionInfo is the info of reaction_update (backend.apply_reaction result).
type ReactionInfo struct {
	MessageID string     `json:"message_id"`
	Timestamp int64      `json:"timestamp"`
	Reactions []Reaction `json:"reactions"`
}

// MarshalJSON emits reactions as [] instead of null.
func (r ReactionInfo) MarshalJSON() ([]byte, error) {
	type alias ReactionInfo
	r.Reactions = EmptyIfNil(r.Reactions)
	return json.Marshal(alias(r))
}

// ReactionUpdatePayload is reaction_update.
type ReactionUpdatePayload struct {
	Protocol  string       `json:"protocol"`
	ContactID string       `json:"contact_id"`
	Info      ReactionInfo `json:"info"`
}

// ReceiptPayload is receipt. Updated holds the cache rows whose status moved
// (id, timestamp, status, text, ... of each).
type ReceiptPayload struct {
	Protocol  string         `json:"protocol"`
	ContactID string         `json:"contact_id"`
	Updated   []CacheMessage `json:"updated"`
}

// MarshalJSON emits updated as [] instead of null.
func (r ReceiptPayload) MarshalJSON() ([]byte, error) {
	type alias ReceiptPayload
	r.Updated = EmptyIfNil(r.Updated)
	return json.Marshal(alias(r))
}

// TypingPayload is typing (action STARTED or STOPPED).
type TypingPayload struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	Action    string `json:"action"`
}

// ContactUpdatePayload is contact_update. display_name and phone are always
// present (null when unknown); contact is the resolved contact (the survivor
// when merged_into is set); merged_into (WhatsApp only) says contact_id was
// folded into another contact.
type ContactUpdatePayload struct {
	Protocol    string       `json:"protocol"`
	ContactID   string       `json:"contact_id"`
	Contact     *ContactWire `json:"contact"`
	DisplayName *string      `json:"display_name"`
	Phone       *string      `json:"phone"`
	MergedInto  *string      `json:"merged_into,omitempty"`
}

// SentMirrorPayload is sent_mirror (batch send notification; the mirror rows
// are already stored). id is str(message_id).
type SentMirrorPayload struct {
	Protocol  string      `json:"protocol"`
	ContactID string      `json:"contact_id"`
	Contact   ContactWire `json:"contact"`
	ID        *string     `json:"id"`
	Timestamp int64       `json:"timestamp"`
	BatchID   *string     `json:"batch_id"`
}

// LidWarmupDonePayload is lid_warmup_done (contact_id is "").
type LidWarmupDonePayload struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
}

// WebPushPayload is web_push: a legacy /ws frame carried on the bus so it also
// reaches /ws/v2 and the socket. See WebFrame.
type WebPushPayload struct {
	Event WebFrame `json:"event"`
}
