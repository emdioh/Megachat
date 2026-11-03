package api

import "encoding/json"

// ContactWire is a contact snapshot (server/events_api.py ContactWire and
// contact_to_wire): identity plus the JSON-only part of ChatContact.extras.
// All six members are always present. cache_key is "<protocol>:<id>".
// last_message_ts mirrors extras.last_message_ts (0 when unknown).
type ContactWire struct {
	ID            string         `json:"id"`
	DisplayName   string         `json:"display_name"`
	Protocol      string         `json:"protocol"`
	CacheKey      string         `json:"cache_key"`
	LastMessageTs int64          `json:"last_message_ts"`
	Extras        map[string]any `json:"extras"`
}

// MarshalJSON emits extras as {} instead of null.
func (c ContactWire) MarshalJSON() ([]byte, error) {
	type alias ContactWire
	c.Extras = EmptyMapIfNil(c.Extras)
	return json.Marshal(alias(c))
}

// CacheKey returns "<protocol>:<id>" (models.contact_cache_key).
func CacheKey(protocol, id string) string { return protocol + ":" + id }

// ContactOut is a contact in the web shape (contacts.list / contacts.book):
// the ChatContact plus unread count; no cache_key.
type ContactOut struct {
	ID            string         `json:"id"`
	DisplayName   string         `json:"display_name"`
	Protocol      string         `json:"protocol"`
	Extras        map[string]any `json:"extras"`
	LastMessageTs int64          `json:"last_message_ts"`
	Unread        int            `json:"unread"`
}

// MarshalJSON emits extras as {} instead of null.
func (c ContactOut) MarshalJSON() ([]byte, error) {
	type alias ContactOut
	c.Extras = EmptyMapIfNil(c.Extras)
	return json.Marshal(alias(c))
}

// BlobOut is a handle to a local file (server/models_api.py BlobOut). path is
// absolute on the server machine; version is "<mtime_ns>-<size>"; immutable is
// true for content-addressed files (generated thumbnails).
type BlobOut struct {
	Path        string  `json:"path"`
	ContentType *string `json:"content_type"`
	Size        *int64  `json:"size"`
	Version     *string `json:"version"`
	Immutable   bool    `json:"immutable"`
}

// AttachmentIn is a file slot of messages.send / gmail.messages.send.
// path is a client-named absolute local path (socket: staged/copied by the
// adapter before the operation sees it) or a server-owned temp file (HTTP).
// rejected="too_large" marks a slot the adapter could not store. The socket
// adapter re-emits slots with model_dump(), i.e. all four keys.
type AttachmentIn struct {
	Path        string  `json:"path"`
	Filename    *string `json:"filename"`
	ContentType *string `json:"content_type"`
	Rejected    *string `json:"rejected"`
}

// OptimisticSend marks the optimistic flavour of messages.send (text only).
// Only the socket adapter forwards it; the HTTP route strips it.
type OptimisticSend struct {
	Timestamp           int64   `json:"timestamp"`
	Resend              bool    `json:"resend"`
	QuoteText           *string `json:"quote_text,omitempty"`
	AttachmentID        *string `json:"attachment_id,omitempty"`
	ContentType         *string `json:"content_type,omitempty"`
	QuoteAttachmentID   *string `json:"quote_attachment_id,omitempty"`
	QuoteAttachmentPath *string `json:"quote_attachment_path,omitempty"`
	QuoteContentType    *string `json:"quote_content_type,omitempty"`
}

// Reaction is one aggregated reaction on a message (also an element of
// reaction_update info.reactions and of reactions.set result).
type Reaction struct {
	Emoji   string   `json:"emoji"`
	Count   int      `json:"count"`
	IsMine  bool     `json:"is_mine"`
	Authors []string `json:"authors"`
}

// MarshalJSON emits authors as [] instead of null.
func (r Reaction) MarshalJSON() ([]byte, error) {
	type alias Reaction
	r.Authors = EmptyIfNil(r.Authors)
	return json.Marshal(alias(r))
}

// MessageAttachment is the "attachment" object of a messages.page row.
// version and pending are only present when set.
type MessageAttachment struct {
	AttachmentID string  `json:"attachment_id"`
	Name         string  `json:"name"`
	Type         *string `json:"type"`
	MediaKind    *string `json:"media_kind"`
	Version      string  `json:"version,omitempty"`
	Pending      bool    `json:"pending,omitempty"`
}

// MessageRow is a presentation row of messages.page / GET /api/messages
// (server/messages.py _messages + messages_page), oldest first. Members
// media_placeholder, system, reactions and is_group are only present when set
// (hence omitempty); all others are always present. quote_author is always a
// string after messages_page ("" in 1:1 chats). status is null for incoming
// rows. direction is "in" or "out".
type MessageRow struct {
	ID                    string             `json:"id"`
	Text                  string             `json:"text"`
	Direction             string             `json:"direction"`
	Sender                string             `json:"sender"`
	Timestamp             int64              `json:"timestamp"`
	Attachment            *MessageAttachment `json:"attachment"`
	QuoteText             *string            `json:"quote_text"`
	QuoteTimestamp        *int64             `json:"quote_timestamp"`
	QuoteAuthor           string             `json:"quote_author"`
	QuoteAttachmentID     *string            `json:"quote_attachment_id"`
	QuoteContentType      *string            `json:"quote_content_type"`
	QuoteThumbURL         *string            `json:"quote_thumb_url"`
	QuoteMediaPlaceholder bool               `json:"quote_media_placeholder"`
	Status                *string            `json:"status"`
	Read                  bool               `json:"read"`
	Edited                bool               `json:"edited"`
	EditID                *string            `json:"edit_id"`
	BatchID               *string            `json:"batch_id"`
	BatchIndex            *int               `json:"batch_index"`
	MediaPlaceholder      string             `json:"media_placeholder,omitempty"`
	System                bool               `json:"system,omitempty"`
	Reactions             []Reaction         `json:"reactions,omitempty"`
	IsGroup               bool               `json:"is_group,omitempty"`
}

// Message delivery statuses (rank guard: pending=failed=0 < sent < delivered < read).
const (
	StatusPending   = "pending"
	StatusFailed    = "failed"
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusRead      = "read"
)

// Message types of the cache entry ("msg_type").
const (
	MsgTypeText       = "text"
	MsgTypeImage      = "image"
	MsgTypeSticker    = "sticker"
	MsgTypeAttachment = "attachment"
	MsgTypeSystem     = "system"
)

// CacheMessage is the canonical "cache entry" message dict (CONTRACTS §2):
// the shape of the raw message payload in the "message" event, of the rows of
// messages.window / messages.snapshot, and of the items of receipt.updated.
// Every member is optional (backends differ); keys that are not modelled here
// are kept in Extra and re-emitted unchanged. The nested "contact" (when a
// backend put a ChatContact there) travels as ContactWire.
type CacheMessage struct {
	ID                  *string      `json:"id,omitempty"`
	Text                *string      `json:"text,omitempty"`
	IsMine              *bool        `json:"is_mine,omitempty"`
	Sender              *string      `json:"sender,omitempty"`
	Timestamp           *int64       `json:"timestamp,omitempty"`
	QuoteText           *string      `json:"quote_text,omitempty"`
	MsgType             *string      `json:"msg_type,omitempty"`
	AttachmentInfo      *string      `json:"attachment_info,omitempty"`
	AttachmentID        *string      `json:"attachment_id,omitempty"`
	ContentType         *string      `json:"content_type,omitempty"`
	Read                *bool        `json:"read,omitempty"`
	Status              *string      `json:"status,omitempty"`
	QuoteTimestamp      *int64       `json:"quote_timestamp,omitempty"`
	QuoteAuthor         *string      `json:"quote_author,omitempty"`
	ReplyToMessageID    *string      `json:"reply_to_message_id,omitempty"`
	Edited              *bool        `json:"edited,omitempty"`
	Protocol            *string      `json:"protocol,omitempty"`
	IsGroup             *bool        `json:"is_group,omitempty"`
	MediaKind           *string      `json:"media_kind,omitempty"`
	BatchID             *string      `json:"batch_id,omitempty"`
	BatchIndex          *int         `json:"batch_index,omitempty"`
	QuoteAttachmentID   *string      `json:"quote_attachment_id,omitempty"`
	QuoteAttachmentPath *string      `json:"quote_attachment_path,omitempty"`
	QuoteContentType    *string      `json:"quote_content_type,omitempty"`
	Contact             *ContactWire `json:"contact,omitempty"`

	// Extra holds members not listed above (round-tripped verbatim).
	Extra map[string]json.RawMessage `json:"-"`
}

// MarshalJSON merges Extra into the known members.
func (m CacheMessage) MarshalJSON() ([]byte, error) {
	type alias CacheMessage
	return marshalWithExtra(alias(m), m.Extra)
}

// UnmarshalJSON captures unknown members into Extra.
func (m *CacheMessage) UnmarshalJSON(data []byte) error {
	type alias CacheMessage
	var a alias
	extra, err := unmarshalWithExtra(data, &a)
	if err != nil {
		return err
	}
	*m = CacheMessage(a)
	m.Extra = extra
	return nil
}

// EmojiCategoryOut is one emoji picker category (emoji.list / GET /api/emoji).
type EmojiCategoryOut struct {
	Category string            `json:"category"`
	Icon     string            `json:"icon"`
	Emojis   []string          `json:"emojis"`
	Aliases  map[string]string `json:"aliases"`
}

// MarshalJSON emits emojis as [] and aliases as {} instead of null.
func (e EmojiCategoryOut) MarshalJSON() ([]byte, error) {
	type alias EmojiCategoryOut
	e.Emojis = EmptyIfNil(e.Emojis)
	e.Aliases = EmptyMapIfNil(e.Aliases)
	return json.Marshal(alias(e))
}
