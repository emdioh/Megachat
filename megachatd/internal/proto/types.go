// Package proto defines the protocol-backend contract (port of
// protocols/base.py) and the Manager that routes operations to backends
// (port of protocols/manager.py).
//
// RECONCILE: the domain types in this file mirror models.py field-for-field
// (ChatContact, ChatMessage, ChatEvent). They were defined here because
// internal/api did not exist when this package was written; once it does,
// replace them with aliases (type Contact = api.Contact ...) or conversions.
package proto

// Protocol identifiers (models.PROTOCOL_*).
const (
	ProtocolSignal   = "signal"
	ProtocolWhatsApp = "whatsapp"
	ProtocolTelegram = "telegram"
)

// Message delivery statuses (ChatMessage.status).
const (
	StatusPending   = "pending"
	StatusFailed    = "failed"
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusRead      = "read"
)

// Event types a backend emits (ChatEvent.type).
const (
	EventMessage        = "message"
	EventMessageEdit    = "message_edit"
	EventReactionUpdate = "reaction_update"
	EventTyping         = "typing"
	EventReceipt        = "receipt"
	EventContactUpdate  = "contact_update"
	EventSentMirror     = "sent-mirror"
	EventLidWarmupDone  = "lid_warmup_done"
)

// ContactCacheKey is models.contact_cache_key: "<protocol>:<id>".
func ContactCacheKey(protocol, id string) string { return protocol + ":" + id }

// Contact mirrors models.ChatContact.
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

// Message mirrors models.ChatMessage. Timestamp is Unix milliseconds. The
// optional string fields are "" when None in Python.
type Message struct {
	ID                  string
	ContactID           string
	Protocol            string
	Text                string
	IsMine              bool
	Sender              string
	Timestamp           int64
	QuoteText           string
	MsgType             string // "text", "image", "sticker", "attachment" (default "text")
	AttachmentInfo      string
	AttachmentID        string
	ContentType         string
	Status              string // default "sent"
	ReplyToMessageID    string
	QuoteAttachmentID   string
	QuoteAttachmentPath string
	QuoteContentType    string
	MediaKind           string
}

// Event mirrors models.ChatEvent: a normalised event from a backend.
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
