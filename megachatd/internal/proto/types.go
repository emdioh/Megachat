// Package proto defines the protocol-backend contract (port of
// protocols/base.py) and the Manager that routes operations to backends
// (port of protocols/manager.py).
//
// RECONCILE: the domain types in this file mirror models.py field-for-field
// (ChatContact, ChatMessage, ChatEvent). They were defined here because
// internal/api did not exist when this package was written; once it does,
// replace them with aliases (type Contact = api.Contact ...) or conversions.
package proto

import "github.com/emdioh/megachat/megachatd/internal/api"

// Protocol identifiers (models.PROTOCOL_*); api is the source of truth.
const (
	ProtocolSignal   = api.ProtocolSignal
	ProtocolWhatsApp = api.ProtocolWhatsApp
	ProtocolTelegram = api.ProtocolTelegram
)

// Message delivery statuses (ChatMessage.status); see api.Status*.
const (
	StatusPending   = api.StatusPending
	StatusFailed    = api.StatusFailed
	StatusSent      = api.StatusSent
	StatusDelivered = api.StatusDelivered
	StatusRead      = api.StatusRead
)

// Event types a backend emits (ChatEvent.type). They equal the api bus event
// types except EventSentMirror: the internal backend event is spelled
// "sent-mirror" (core.py maps it to the wire "sent_mirror", api.EventSentMirror).
const (
	EventMessage        = api.EventMessage
	EventMessageEdit    = api.EventMessageEdit
	EventReactionUpdate = api.EventReactionUpdate
	EventTyping         = api.EventTyping
	EventReceipt        = api.EventReceipt
	EventContactUpdate  = api.EventContactUpdate
	EventSentMirror     = "sent-mirror"
	EventLidWarmupDone  = api.EventLidWarmupDone
)

// ContactCacheKey is models.contact_cache_key: "<protocol>:<id>".
func ContactCacheKey(protocol, id string) string { return api.CacheKey(protocol, id) }

// Contact mirrors models.ChatContact. It is the in-process domain contact;
// its wire snapshot (cache_key and last_message_ts derived) is api.ContactWire,
// see ToWire.
type Contact struct {
	ID          string
	DisplayName string
	Protocol    string
	// Extras holds protocol-specific metadata ("phone", "last_message_ts",
	// "address_book", ...).
	Extras map[string]any
}

// CacheKey is unique across protocols.
func (c Contact) CacheKey() string { return ContactCacheKey(c.Protocol, c.ID) }

// ToWire converts to the wire snapshot api.ContactWire.
func (c Contact) ToWire() api.ContactWire {
	return api.ContactWire{
		ID: c.ID, DisplayName: c.DisplayName, Protocol: c.Protocol,
		CacheKey: c.CacheKey(), LastMessageTs: c.LastMessageTS(), Extras: c.Extras,
	}
}

// Phone is the normalised phone number from Extras["phone"], or "".
func (c Contact) Phone() string {
	if s, ok := c.Extras["phone"].(string); ok {
		return s
	}
	return ""
}

// LastMessageTS is Extras["last_message_ts"] (ms), or 0.
func (c Contact) LastMessageTS() int64 {
	switch v := c.Extras["last_message_ts"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// WithExtra returns a copy of c with Extras[key]=value (Extras is cloned).
func (c Contact) WithExtra(key string, value any) Contact {
	m := make(map[string]any, len(c.Extras)+1)
	for k, v := range c.Extras {
		m[k] = v
	}
	m[key] = value
	c.Extras = m
	return c
}

// Message is the wire cache-entry message (models.ChatMessage as it crosses
// the wire). Optional Python None fields are nil pointers; the owning contact
// is given by the call (FetchHistory's contactID), not stored on the message.
type Message = api.CacheMessage

// Event mirrors models.ChatEvent: the internal envelope a backend emits
// (no wire counterpart; the api bus Event is the post-processing envelope).
// Payload shapes per Type are documented on ChatEvent in models.py.
type Event struct {
	Type      string
	Protocol  string
	ContactID string
	Payload   map[string]any
}

// AddressBookRefreshResult mirrors base.AddressBookRefreshResult. Errors is
// non-empty (and nothing was committed) when the remote fetch failed.
type AddressBookRefreshResult struct {
	Protocol        string
	NewContacts     []Contact
	RenamedContacts []Contact
	Errors          string
}

// Quote carries reply/quote metadata of an outgoing message.
type Quote struct {
	Timestamp int64 // ms; 0 = none
	Author    string
	Message   string
	// ReplyToMessageID is the id of the replied-to message (WhatsApp/Telegram).
	ReplyToMessageID string
	// Attachments are Signal quote-attachment descriptors (first one is used).
	Attachments []string
}

// IsZero reports whether no quote data is set.
func (q Quote) IsZero() bool {
	return q.Timestamp == 0 && q.Author == "" && q.Message == "" &&
		q.ReplyToMessageID == "" && len(q.Attachments) == 0
}

// Attachment describes one file to send.
type Attachment struct {
	Path      string
	Caption   string
	MimeType  string
	MediaKind string // "" = unspecified
	Filename  string // "" = use basename of Path
}

// SentMessage describes an outgoing message to mirror into the receive
// queue (base.enqueue_sent_message).
type SentMessage struct {
	ContactID      string
	MessageID      string
	Text           string
	Quote          Quote
	AttachmentPath string
	MimeType       string
	MediaKind      string
	Filename       string
	BatchID        string
	BatchIndex     *int
	// Signal-only quote media fallback display.
	QuoteContentType string
	QuoteFilename    string
}

// EditResult is the dict returned by apply_edit.
type EditResult struct {
	MessageID string
	Timestamp int64
	OldText   string
	Text      string
	IsMine    bool
}

// ApplyEditOptions are the keyword options of apply_edit.
type ApplyEditOptions struct {
	IsMine        *bool
	EditTimestamp int64
	MarkEdited    bool
}
