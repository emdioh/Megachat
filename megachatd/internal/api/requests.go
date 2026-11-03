package api

import "encoding/json"

// Request ("params") models of every operation, mirroring
// server/models_api.py. They are only ever decoded by the Go server, so
// optional members carry omitempty and are pointers. Python validates in
// pydantic lax mode ("10" is accepted for an int, "true" for a bool) except
// where a field is documented as strict; a type mismatch is invalid_request
// "Invalid request". Fields documented "Stripped" go through
// str(value or "").strip() (null and non-strings become strings, whitespace
// is trimmed). Defaults that are not the Go zero value are applied by the
// UnmarshalJSON of the type.

// EmptyRequest is the params of operations without parameters ({} / absent).
type EmptyRequest struct{}

// ReactionsAvailableRequest: reactions.available.
type ReactionsAvailableRequest struct {
	Protocol string `json:"protocol"`
}

// ContactsListRequest: contacts.list. Q is a case-insensitive substring over
// name/id/phone of the active chats; blank = all.
type ContactsListRequest struct {
	Q *string `json:"q,omitempty"`
}

// ContactsBookRequest: contacts.book. Blank q answers [] without work.
type ContactsBookRequest struct {
	Q     string `json:"q"`
	Force bool   `json:"force"`
}

// ContactsForProtocolRequest: contacts.for_protocol. Protocol is Stripped.
type ContactsForProtocolRequest struct {
	Protocol string `json:"protocol"`
}

// ContactsRegisterRequest: contacts.register. ID and Protocol are Stripped;
// DisplayName is a plain str; Extras defaults to {}.
type ContactsRegisterRequest struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Protocol    string         `json:"protocol"`
	Extras      map[string]any `json:"extras,omitempty"`
}

// ContactsAddressBookRequest: contacts.address_book. Protocols nil = all.
type ContactsAddressBookRequest struct {
	Protocols []string `json:"protocols,omitempty"`
	Force     bool     `json:"force"`
}

// ContactsCheckNumberRequest: contacts.check_number. Phone is Stripped.
type ContactsCheckNumberRequest struct {
	Phone string `json:"phone"`
}

// ChatsMarkReadRequest: chats.mark_read. Both fields are Stripped.
type ChatsMarkReadRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
}

// ChatsArchiveRequest: chats.archive. Protocol/ContactID Stripped; Archived is
// a strict bool.
type ChatsArchiveRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	Archived  bool   `json:"archived"`
}

// MessagesPageRequest: messages.page. Protocol must be one of the three
// protocols (pydantic Literal -> invalid_request otherwise). Limit, when
// present, must be 1..2000 (else invalid_request "Invalid limit"); Since keeps
// timestamp >= since; Limit keeps the newest rows.
type MessagesPageRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	Refresh   bool   `json:"refresh"`
	Limit     *int   `json:"limit,omitempty"`
	Since     *int64 `json:"since,omitempty"`
}

// MessagesWindowRequest: messages.window. RefreshLimit is 1..200, default 50.
type MessagesWindowRequest struct {
	Protocol     string `json:"protocol"`
	ContactID    string `json:"contact_id"`
	Refresh      bool   `json:"refresh"`
	RefreshLimit int    `json:"refresh_limit"`
}

// UnmarshalJSON applies refresh_limit=50.
func (r *MessagesWindowRequest) UnmarshalJSON(data []byte) error {
	type alias MessagesWindowRequest
	a := alias{RefreshLimit: 50}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = MessagesWindowRequest(a)
	return nil
}

// MessagesSnapshotRequest: messages.snapshot.
type MessagesSnapshotRequest struct {
	Protocol string `json:"protocol"`
}

// MessagesEditRequest: messages.edit. All four fields are strict strings
// (missing / non-string = invalid_request "Invalid request").
type MessagesEditRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	MessageID string `json:"message_id"`
	NewText   string `json:"new_text"`
}

// MessagesSendRequest: messages.send (and the base of messages.send.check).
// Protocol/ContactID/Text are strict str|None. The Any fields keep their raw
// JSON so the operation can reproduce Python's isinstance checks (see RawInt /
// RawString). Retry defaults to true (web policy); false = a single attempt
// (TUI). Optimistic is only accepted from the socket adapter.
type MessagesSendRequest struct {
	Protocol          *string         `json:"protocol,omitempty"`
	ContactID         *string         `json:"contact_id,omitempty"`
	Text              *string         `json:"text,omitempty"`
	ClientMsgID       json.RawMessage `json:"client_msg_id,omitempty"`
	QuoteTimestamp    json.RawMessage `json:"quote_timestamp,omitempty"`
	QuoteAuthor       json.RawMessage `json:"quote_author,omitempty"`
	QuoteMessage      json.RawMessage `json:"quote_message,omitempty"`
	ReplyToMessageID  json.RawMessage `json:"reply_to_message_id,omitempty"`
	QuoteContentType  json.RawMessage `json:"quote_content_type,omitempty"`
	QuoteAttachmentID json.RawMessage `json:"quote_attachment_id,omitempty"`
	BatchID           json.RawMessage `json:"batch_id,omitempty"`
	Attachments       []AttachmentIn  `json:"attachments,omitempty"`
	Retry             bool            `json:"retry"`
	Optimistic        *OptimisticSend `json:"optimistic,omitempty"`
}

// UnmarshalJSON applies retry=true.
func (r *MessagesSendRequest) UnmarshalJSON(data []byte) error {
	type alias MessagesSendRequest
	a := alias{Retry: true}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = MessagesSendRequest(a)
	return nil
}

// MessagesSendCheckRequest: messages.send.check = MessagesSendRequest plus the
// slot counters filled by the adapter before it stores any file.
type MessagesSendCheckRequest struct {
	MessagesSendRequest
	AttachmentCount int  `json:"attachment_count"`
	SlotsOK         bool `json:"slots_ok"`
}

// UnmarshalJSON applies retry=true and slots_ok=true. (The embedded type has
// its own UnmarshalJSON, so the members are decoded in two passes.)
func (r *MessagesSendCheckRequest) UnmarshalJSON(data []byte) error {
	var base MessagesSendRequest
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	counters := struct {
		AttachmentCount int  `json:"attachment_count"`
		SlotsOK         bool `json:"slots_ok"`
	}{SlotsOK: true}
	if err := json.Unmarshal(data, &counters); err != nil {
		return err
	}
	r.MessagesSendRequest = base
	r.AttachmentCount = counters.AttachmentCount
	r.SlotsOK = counters.SlotsOK
	return nil
}

// MediaAttachmentRequest: media.attachment.
type MediaAttachmentRequest struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
}

// MediaThumbnailRequest: media.thumbnail. Width 96, 240 or 480; any other
// width answers the original attachment.
type MediaThumbnailRequest struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
	Width        int    `json:"width"`
}

// MediaQuoteRequest: media.quote. MessageRowID is the SQLite messages.id.
type MediaQuoteRequest struct {
	Protocol     string `json:"protocol"`
	MessageRowID int64  `json:"message_row_id"`
	Width        *int   `json:"width,omitempty"`
}

// MediaQuotedAttachmentRequest: media.quoted_attachment.
type MediaQuotedAttachmentRequest struct {
	Protocol         string  `json:"protocol"`
	ContactKey       string  `json:"contact_key"`
	QuoteTimestamp   *int64  `json:"quote_timestamp,omitempty"`
	ReplyToMessageID *string `json:"reply_to_message_id,omitempty"`
	QuoteText        string  `json:"quote_text"`
	ContentType      *string `json:"content_type,omitempty"`
}

// ContactsAvatarRequest: contacts.avatar.
type ContactsAvatarRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
}

// LinkStartRequest: link.start. Protocol is one of the three protocols;
// Phone and DeviceName are Signal only. DeviceName defaults to
// "Signal-TUI-Client". Phone/DeviceName are strict strings.
type LinkStartRequest struct {
	Protocol   string `json:"protocol"`
	Phone      string `json:"phone"`
	DeviceName string `json:"device_name"`
}

// UnmarshalJSON applies device_name="Signal-TUI-Client".
func (r *LinkStartRequest) UnmarshalJSON(data []byte) error {
	type alias LinkStartRequest
	a := alias{DeviceName: DefaultDeviceName}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = LinkStartRequest(a)
	return nil
}

// LinkProtocolRequest: link.poll and link.cancel.
type LinkProtocolRequest struct {
	Protocol string `json:"protocol"`
}

// LinkFinalizeRequest: link.finalize. Protocol is the literal "signal"
// (default); Phone is a strict string defaulting to "".
type LinkFinalizeRequest struct {
	Protocol string `json:"protocol"`
	Phone    string `json:"phone"`
}

// UnmarshalJSON applies protocol="signal".
func (r *LinkFinalizeRequest) UnmarshalJSON(data []byte) error {
	type alias LinkFinalizeRequest
	a := alias{Protocol: ProtocolSignal}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = LinkFinalizeRequest(a)
	return nil
}

// Link2faRequest: link.submit_2fa. Password is a strict string; never log it.
type Link2faRequest struct {
	Password string `json:"password"`
}

// LinkReconnectRequest: link.reconnect.
type LinkReconnectRequest struct {
	Protocols []string `json:"protocols"`
}

// DownloadServeAttachmentRequest: download.serve_attachment (strict strings).
type DownloadServeAttachmentRequest struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
}

// DownloadServeTextRequest: download.serve_text. Filename defaults to
// "message.txt" and must be a plain file name.
type DownloadServeTextRequest struct {
	Text     string `json:"text"`
	Filename string `json:"filename"`
}

// UnmarshalJSON applies filename="message.txt".
func (r *DownloadServeTextRequest) UnmarshalJSON(data []byte) error {
	type alias DownloadServeTextRequest
	a := alias{Filename: "message.txt"}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = DownloadServeTextRequest(a)
	return nil
}

// ReactionsSetRequest: reactions.set (strict strings).
type ReactionsSetRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// LinksPreviewRequest: links.preview and links.preview_image.
type LinksPreviewRequest struct {
	URL string `json:"url"`
}

// ClientLogRequest: server.client_log. Message is Any (stringified and cut to
// 500 chars); Source defaults to "client" (the web route forces "web-client").
type ClientLogRequest struct {
	Message json.RawMessage `json:"message,omitempty"`
	Source  string          `json:"source"`
}

// UnmarshalJSON applies source="client".
func (r *ClientLogRequest) UnmarshalJSON(data []byte) error {
	type alias ClientLogRequest
	a := alias{Source: "client"}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = ClientLogRequest(a)
	return nil
}

// ServerLogsRequest: server.logs. Limit defaults to 500 (values outside
// 1..2000 are replaced by 500); Level is a logging level name ("" = all).
type ServerLogsRequest struct {
	After int64  `json:"after"`
	Limit int    `json:"limit"`
	Level string `json:"level"`
}

// UnmarshalJSON applies limit=500.
func (r *ServerLogsRequest) UnmarshalJSON(data []byte) error {
	type alias ServerLogsRequest
	a := alias{Limit: 500}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = ServerLogsRequest(a)
	return nil
}

// TranscribeSubmitRequest: transcribe.submit. Both fields are Any and checked
// by the operation (str, known protocol, non-blank after strip).
type TranscribeSubmitRequest struct {
	Protocol     json.RawMessage `json:"protocol,omitempty"`
	AttachmentID json.RawMessage `json:"attachment_id,omitempty"`
}

// TranscribeStatusRequest: transcribe.status.
type TranscribeStatusRequest struct {
	Protocol     string `json:"protocol"`
	AttachmentID string `json:"attachment_id"`
}

// AutoTranscribeSetGlobalRequest: autotranscribe.set_global (enabled strict bool).
type AutoTranscribeSetGlobalRequest struct {
	Protocol string `json:"protocol"`
	Enabled  bool   `json:"enabled"`
}

// AutoTranscribeChatRequest: autotranscribe.chat (contact_id min length 1).
type AutoTranscribeChatRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
}

// AutoTranscribeSetChatRequest: autotranscribe.set_chat. "enabled" is required
// but nullable: null clears the chat override. Enabled == nil <=> null; the
// key itself must be present (HTTP route answers 422 "enabled richiesto" when
// absent), hence no omitempty.
type AutoTranscribeSetChatRequest struct {
	Protocol  string `json:"protocol"`
	ContactID string `json:"contact_id"`
	Enabled   *bool  `json:"enabled"`
}

// TranscribeSettingsRequest: transcribe.set_settings. Partial update: only the
// keys present in the JSON object are applied, so every member is raw JSON and
// a nil RawMessage means "absent" (an explicit null is the 4 bytes "null").
// Providers is {"openai":{"model","base_url"},"openrouter":{"model","base_url",
// "http_referer","app_title"},"gemini":{...}}.
type TranscribeSettingsRequest struct {
	Provider  json.RawMessage `json:"provider,omitempty"`
	Model     json.RawMessage `json:"model,omitempty"`
	Language  json.RawMessage `json:"language,omitempty"`
	BaseURL   json.RawMessage `json:"base_url,omitempty"`
	Timeout   json.RawMessage `json:"timeout,omitempty"`
	Providers json.RawMessage `json:"providers,omitempty"`
}

// TranscribeApiKeyRequest: transcribe.set_api_key (checked by the operation).
type TranscribeApiKeyRequest struct {
	Provider json.RawMessage `json:"provider,omitempty"`
	APIKey   json.RawMessage `json:"api_key,omitempty"`
}

// OpenAIKeyRequest: config.set_openai_key (checked by the operation).
type OpenAIKeyRequest struct {
	APIKey json.RawMessage `json:"api_key,omitempty"`
}

// GmailMessageRef: gmail.messages.get / delete / mark_read / mark_unread /
// archive. Folder nil = the configured folder.
type GmailMessageRef struct {
	UID    int     `json:"uid"`
	Folder *string `json:"folder,omitempty"`
}

// GmailMessagesListRequest: gmail.messages.list. Limit defaults to 50.
type GmailMessagesListRequest struct {
	Folder  *string `json:"folder,omitempty"`
	Offset  int     `json:"offset"`
	Limit   int     `json:"limit"`
	Refresh bool    `json:"refresh"`
}

// UnmarshalJSON applies limit=50.
func (r *GmailMessagesListRequest) UnmarshalJSON(data []byte) error {
	type alias GmailMessagesListRequest
	a := alias{Limit: 50}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = GmailMessagesListRequest(a)
	return nil
}

// GmailMessagesBulkDeleteRequest: gmail.messages.bulk_delete. Uids and Folder
// are Any and checked by the operation (list of 1..200 ints, folder str|null).
type GmailMessagesBulkDeleteRequest struct {
	Uids   json.RawMessage `json:"uids,omitempty"`
	Folder json.RawMessage `json:"folder,omitempty"`
}

// GmailAttachmentRequest: gmail.attachment.
type GmailAttachmentRequest struct {
	UID    int     `json:"uid"`
	Folder *string `json:"folder,omitempty"`
	Index  int     `json:"index"`
}

// GmailMessagesSendRequest: gmail.messages.send. Text members are "FormText":
// absent/null means "".
type GmailMessagesSendRequest struct {
	To            string         `json:"to"`
	Cc            string         `json:"cc"`
	Bcc           string         `json:"bcc"`
	Subject       string         `json:"subject"`
	Text          string         `json:"text"`
	ReplyToUID    *int           `json:"reply_to_uid,omitempty"`
	ReplyToFolder *string        `json:"reply_to_folder,omitempty"`
	ForwardUID    *int           `json:"forward_uid,omitempty"`
	ForwardFolder *string        `json:"forward_folder,omitempty"`
	Attachments   []AttachmentIn `json:"attachments,omitempty"`
}

// GmailMessagesSendCheckRequest: gmail.messages.send.check.
type GmailMessagesSendCheckRequest struct {
	GmailMessagesSendRequest
	AttachmentCount int  `json:"attachment_count"`
	SlotsOK         bool `json:"slots_ok"`
}

// UnmarshalJSON applies slots_ok=true.
func (r *GmailMessagesSendCheckRequest) UnmarshalJSON(data []byte) error {
	type alias GmailMessagesSendCheckRequest
	a := alias{SlotsOK: true}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = GmailMessagesSendCheckRequest(a)
	return nil
}

// GmailImageRequest: gmail.image. Token is the HMAC-signed value issued by
// the server when it rewrote the email HTML.
type GmailImageRequest struct {
	Token string `json:"token"`
}
