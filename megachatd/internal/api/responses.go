package api

import "encoding/json"

// Result models of the operations (server/models_api.py plus the typed form of
// every dict[str, Any] result). Operation results are serialised by pydantic
// without exclude_none: Optional members are pointers WITHOUT omitempty and
// travel as null. Members that are only present sometimes carry omitempty.

// StatusOut is {"status":"ok"}: server.client_log, server.logs_clear,
// server.shutdown, transcribe.set_api_key, config.set_openai_key.
type StatusOut struct {
	Status string `json:"status"`
}

// StatusOkOut is chats.mark_read's {"status":"ok"} (same shape as StatusOut).
type StatusOkOut struct {
	Status string `json:"status"`
}

// OkOut is {"ok":true}: messages.edit, link.cancel, link.reconnect,
// messages.send.check, gmail.messages.mark_read / mark_unread / archive,
// gmail.messages.send.check.
type OkOut struct {
	OK bool `json:"ok"`
}

// ServerStatus is server.status: protocol -> backend connected.
type ServerStatus map[string]bool

// ServerConfigOut is server.config.
type ServerConfigOut struct {
	ThumbnailMaxLines      int    `json:"thumbnail_max_lines"`
	ThumbnailMaxCols       int    `json:"thumbnail_max_cols"`
	PickerMaxResults       int    `json:"picker_max_results"`
	PickerPreferredBackend string `json:"picker_preferred_backend"`
	WaLazyMedia            bool   `json:"wa_lazy_media"`
}

// Defaults of ServerConfigOut when nothing is configured.
const (
	DefaultThumbnailMaxLines = 12
	DefaultThumbnailMaxCols  = 60
	DefaultPickerMaxResults  = 50
)

// LogRecord is one element of server.logs records (logbuffer ring, 2000 max).
// time is Unix seconds (float); level is a logging level name (DEBUG, INFO,
// WARNING, ERROR, CRITICAL); message is secret-redacted and cut to 4000 chars.
type LogRecord struct {
	Seq     int64   `json:"seq"`
	Time    float64 `json:"time"`
	Level   string  `json:"level"`
	Logger  string  `json:"logger"`
	Message string  `json:"message"`
}

// ServerLogsOut is server.logs. last_seq is the last record's seq, else the
// request's max(after, 0). since is the buffer install time (null if none).
type ServerLogsOut struct {
	Records []LogRecord `json:"records"`
	LastSeq int64       `json:"last_seq"`
	Since   *float64    `json:"since"`
}

// MarshalJSON emits records as [] instead of null.
func (o ServerLogsOut) MarshalJSON() ([]byte, error) {
	type alias ServerLogsOut
	o.Records = EmptyIfNil(o.Records)
	return json.Marshal(alias(o))
}

// ClientsOut is server.clients.
type ClientsOut struct {
	Connections  int  `json:"connections"`
	SelfIncluded bool `json:"self_included"`
	Web          int  `json:"web"`
}

// ReactionsAvailableOut is reactions.available (Telegram only; others []).
type ReactionsAvailableOut struct {
	Emojis []string `json:"emojis"`
}

// MarshalJSON emits emojis as [] instead of null.
func (o ReactionsAvailableOut) MarshalJSON() ([]byte, error) {
	type alias ReactionsAvailableOut
	o.Emojis = EmptyIfNil(o.Emojis)
	return json.Marshal(alias(o))
}

// ContactsAddressBookOut is contacts.address_book: the address book plus the
// per-protocol failure messages.
type ContactsAddressBookOut struct {
	Contacts []ContactWire     `json:"contacts"`
	Errors   map[string]string `json:"errors"`
}

// MarshalJSON emits contacts as [] and errors as {} instead of null.
func (o ContactsAddressBookOut) MarshalJSON() ([]byte, error) {
	type alias ContactsAddressBookOut
	o.Contacts = EmptyIfNil(o.Contacts)
	o.Errors = EmptyMapIfNil(o.Errors)
	return json.Marshal(alias(o))
}

// ContactsCheckNumberOut is contacts.check_number (null = unknown).
type ContactsCheckNumberOut struct {
	Exists *bool `json:"exists"`
}

// ChatsArchiveOut is chats.archive.
type ChatsArchiveOut struct {
	Archived bool `json:"archived"`
}

// MessagesWindowOut is messages.window.
type MessagesWindowOut struct {
	Messages  []CacheMessage `json:"messages"`
	Refreshed bool           `json:"refreshed"`
}

// MarshalJSON emits messages as [] instead of null.
func (o MessagesWindowOut) MarshalJSON() ([]byte, error) {
	type alias MessagesWindowOut
	o.Messages = EmptyIfNil(o.Messages)
	return json.Marshal(alias(o))
}

// MessagesSnapshotOut is messages.snapshot: contact id -> cache rows.
type MessagesSnapshotOut map[string][]CacheMessage

// MessagesSendOut is the plain messages.send result: {"ok":true,"message_ids":[...]}.
// message_ids holds the real backend ids of the sent messages (text: the one
// id the backend returned, or none; attachments: the ids of the attachment
// send), so the web client can pair its optimistic bubble with the real row.
type MessagesSendOut struct {
	OK         bool     `json:"ok"`
	MessageIDs []string `json:"message_ids"`
}

// MarshalJSON emits message_ids as [] instead of null.
func (o MessagesSendOut) MarshalJSON() ([]byte, error) {
	type alias MessagesSendOut
	o.MessageIDs = EmptyIfNil(o.MessageIDs)
	return json.Marshal(alias(o))
}

// MessagesSendDuplicateOut is the result of a text send that repeats an
// already sent (protocol, contact_id, client_msg_id) with the same
// fingerprint: {"ok":true,"duplicate":true}.
type MessagesSendDuplicateOut struct {
	OK        bool `json:"ok"`
	Duplicate bool `json:"duplicate"`
}

// MessagesSendOptimisticOut is the result of the optimistic flavour. status is
// always "sent"; status_applied says whether the pending row moved to sent;
// message_id is the server id attached to the row (null if none).
type MessagesSendOptimisticOut struct {
	OK            bool    `json:"ok"`
	Status        string  `json:"status"`
	StatusApplied bool    `json:"status_applied"`
	MessageID     *string `json:"message_id"`
}

// ReactionsSetOut is reactions.set.
type ReactionsSetOut struct {
	OK        bool       `json:"ok"`
	Reactions []Reaction `json:"reactions"`
}

// MarshalJSON emits reactions as [] instead of null.
func (o ReactionsSetOut) MarshalJSON() ([]byte, error) {
	type alias ReactionsSetOut
	o.Reactions = EmptyIfNil(o.Reactions)
	return json.Marshal(alias(o))
}

// MediaQuotedAttachmentOut is media.quoted_attachment.
type MediaQuotedAttachmentOut struct {
	AttachmentID *string `json:"attachment_id"`
}

// DownloadOut is download.serve_attachment / serve_text: a URL on the
// temporary download server (port 10042).
type DownloadOut struct {
	URL string `json:"url"`
}

// LinkPreviewOut is links.preview (also GET /api/link-preview).
type LinkPreviewOut struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SiteName    string `json:"site_name"`
	HasImage    bool   `json:"has_image"`
}

// ---- device link -----------------------------------------------------------

// LinkProtocolStatus is one protocol in link.status. linked is null when it
// cannot be known without asking the remote service (WhatsApp).
type LinkProtocolStatus struct {
	Available bool   `json:"available"`
	Account   string `json:"account"`
	Linked    *bool  `json:"linked"`
}

// LinkStatusOut is link.status.
type LinkStatusOut struct {
	Protocols map[string]LinkProtocolStatus `json:"protocols"`
}

// LinkRequiredOut is link.required: protocol -> needs a (re)pairing QR.
type LinkRequiredOut struct {
	Protocols map[string]bool `json:"protocols"`
}

// QR kinds of LinkQrOut.
const (
	QRKindURL   = "url"
	QRKindPNG   = "png"
	QRKindText  = "text"
	QRKindInfo  = "info"
	QRKindError = "error"
)

// LinkQrOut is what to show for a pairing. kind url: a link to render as a QR
// (sgnl://link..., tg://login); png: base64 PNG in data; text: pre-rendered or
// raw text; info/error: a message instead of a QR. The HTTP adapter converts
// url/text into png before answering; the socket answers them raw.
type LinkQrOut struct {
	Kind string `json:"kind"`
	Data string `json:"data"`
}

// LinkStartOut is link.start.
type LinkStartOut struct {
	QR LinkQrOut `json:"qr"`
}

// Poll states of LinkPollOut.
const (
	LinkStateWaiting  = "waiting"
	LinkStateFinalize = "finalize"
	LinkStateDone     = "done"
)

// LinkPollOut is link.poll. needs_2fa is true when Telegram asks for its
// password; qr is a refreshed code (null otherwise).
type LinkPollOut struct {
	State    string     `json:"state"`
	Needs2FA bool       `json:"needs_2fa"`
	QR       *LinkQrOut `json:"qr"`
}

// LinkFinalizeOut is link.finalize.
type LinkFinalizeOut struct {
	Account string `json:"account"`
}

// Link2faOut is link.submit_2fa (ok=false: wrong password).
type Link2faOut struct {
	OK bool `json:"ok"`
}

// ---- transcription -----------------------------------------------------------

// TranscribeConfigOut is transcribe.config.
type TranscribeConfigOut struct {
	Enabled bool `json:"enabled"`
}

// TranscribeStatusOut is transcribe.status when a record is stored: status is
// "ok" | "pending" | "error"; text, error, model and updated_at are the SQLite
// columns and travel as null when unset. When nothing is stored the result is
// TranscribeStatusUnknownOut instead.
type TranscribeStatusOut struct {
	Status    string   `json:"status"`
	Text      *string  `json:"text"`
	Error     *string  `json:"error"`
	Model     *string  `json:"model"`
	UpdatedAt *float64 `json:"updated_at"`
}

// TranscribeStatusUnknownOut is transcribe.status for an unknown attachment:
// {"status":"unknown"}.
type TranscribeStatusUnknownOut struct {
	Status string `json:"status"`
}

// TranscribeSubmitOut is transcribe.submit: {"status":"pending"} or
// {"status":"done","text":"..."} (note: "done", not the stored "ok").
type TranscribeSubmitOut struct {
	Status string  `json:"status"`
	Text   *string `json:"text,omitempty"`
}

// AutoTranscribeConfigOut is autotranscribe.config. Globals is protocol ->
// enabled for whatsapp, signal and telegram.
type AutoTranscribeConfigOut struct {
	Configured bool            `json:"configured"`
	Globals    map[string]bool `json:"globals"`
}

// AutoTranscribeChatOut is autotranscribe.chat / autotranscribe.set_chat.
// override is null when the chat has none.
type AutoTranscribeChatOut struct {
	Global    bool  `json:"global"`
	Override  *bool `json:"override"`
	Effective bool  `json:"effective"`
}

// TranscribeProviderSettings is one entry of TranscribeSettingsOut.Providers.
// http_referer and app_title exist for "openrouter" only.
type TranscribeProviderSettings struct {
	APIKeySet   bool    `json:"api_key_set"`
	Model       string  `json:"model"`
	BaseURL     string  `json:"base_url"`
	HTTPReferer *string `json:"http_referer,omitempty"`
	AppTitle    *string `json:"app_title,omitempty"`
}

// TranscribeSettingsOut is transcribe.settings / transcribe.set_settings. It
// never contains an API key. Provider names: openai, openrouter, gemini.
// api_key_set / model / base_url repeat the selected provider's values;
// env_overrides lists settings forced by environment variables.
type TranscribeSettingsOut struct {
	Configured   bool                                  `json:"configured"`
	Provider     string                                `json:"provider"`
	Providers    map[string]TranscribeProviderSettings `json:"providers"`
	APIKeySet    bool                                  `json:"api_key_set"`
	Model        string                                `json:"model"`
	Language     string                                `json:"language"`
	BaseURL      string                                `json:"base_url"`
	Timeout      int                                   `json:"timeout"`
	EnvOverrides []string                              `json:"env_overrides"`
}

// MarshalJSON emits env_overrides as [] and providers as {} instead of null.
func (o TranscribeSettingsOut) MarshalJSON() ([]byte, error) {
	type alias TranscribeSettingsOut
	o.EnvOverrides = EmptyIfNil(o.EnvOverrides)
	o.Providers = EmptyMapIfNil(o.Providers)
	return json.Marshal(alias(o))
}

// TranscribeRecentRow is an element of TranscribeDiagnosticsOut.Recent (the
// ten most recently updated records).
type TranscribeRecentRow struct {
	Protocol     string   `json:"protocol"`
	AttachmentID string   `json:"attachment_id"`
	Status       string   `json:"status"`
	Text         *string  `json:"text"`
	Error        *string  `json:"error"`
	Model        *string  `json:"model"`
	UpdatedAt    *float64 `json:"updated_at"`
}

// TranscribeDBErrorRow is the single element of Recent when the store cannot be
// read: {"status":"db_error","error":"..."}.
type TranscribeDBErrorRow struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

// TranscribeDiagnosticsOut is transcribe.diagnostics (never the API key).
type TranscribeDiagnosticsOut struct {
	Provider       string          `json:"provider"`
	APIKeySet      bool            `json:"api_key_set"`
	Model          string          `json:"model"`
	BaseURL        string          `json:"base_url"`
	ServiceActive  bool            `json:"service_active"`
	ServiceClient  *string         `json:"service_client"`
	ServiceModel   *string         `json:"service_model"`
	ServiceBase    *string         `json:"service_base_url"`
	WorkerAlive    bool            `json:"worker_alive"`
	QueueSize      *int            `json:"queue_size"`
	AutoTranscribe map[string]bool `json:"auto_transcribe"`
	AutoListener   bool            `json:"auto_listener"`
	FFmpeg         bool            `json:"ffmpeg"`
	EnvOverrides   []string        `json:"env_overrides"`
	Recent         any             `json:"recent"`
}

// MarshalJSON emits env_overrides / recent as [] and auto_transcribe as {}.
func (o TranscribeDiagnosticsOut) MarshalJSON() ([]byte, error) {
	type alias TranscribeDiagnosticsOut
	o.EnvOverrides = EmptyIfNil(o.EnvOverrides)
	if o.Recent == nil {
		o.Recent = []TranscribeRecentRow{}
	}
	o.AutoTranscribe = EmptyMapIfNil(o.AutoTranscribe)
	return json.Marshal(alias(o))
}

// TranscribeCheckOut is transcribe.check. url / http_status are absent when
// the key is missing; http_status is absent on a network error.
type TranscribeCheckOut struct {
	OK         bool   `json:"ok"`
	Provider   string `json:"provider"`
	URL        string `json:"url,omitempty"`
	HTTPStatus *int   `json:"http_status,omitempty"`
	Detail     string `json:"detail"`
}

// ---- Gmail ---------------------------------------------------------------------

// GmailLimits is GmailStatusOut.Limits.
type GmailLimits struct {
	MaxTextLength           int `json:"max_text_length"`
	MaxAttachments          int `json:"max_attachments"`
	MaxAttachmentBytes      int `json:"max_attachment_bytes"`
	MaxTotalAttachmentBytes int `json:"max_total_attachment_bytes"`
	MaxDownloadBytes        int `json:"max_download_bytes"`
	MaxMessagesLimit        int `json:"max_messages_limit"`
}

// GmailStatusOut is gmail.status. connected is always null (no network I/O).
// address is "" when not configured.
type GmailStatusOut struct {
	Configured bool        `json:"configured"`
	Address    string      `json:"address"`
	Folder     string      `json:"folder"`
	Connected  *bool       `json:"connected"`
	Limits     GmailLimits `json:"limits"`
}

// GmailUnreadOut is gmail.unread.
type GmailUnreadOut struct {
	Configured bool `json:"configured"`
	Unread     int  `json:"unread"`
}

// GmailSummary is one message of gmail.messages.list. snippet is always "".
type GmailSummary struct {
	UID            int      `json:"uid"`
	From           string   `json:"from"`
	To             string   `json:"to"`
	Subject        string   `json:"subject"`
	DateTs         *int64   `json:"date_ts"`
	Snippet        string   `json:"snippet"`
	HasAttachments bool     `json:"has_attachments"`
	Seen           bool     `json:"seen"`
	Flags          []string `json:"flags"`
}

// MarshalJSON emits flags as [] instead of null.
func (s GmailSummary) MarshalJSON() ([]byte, error) {
	type alias GmailSummary
	s.Flags = EmptyIfNil(s.Flags)
	return json.Marshal(alias(s))
}

// GmailListOut is gmail.messages.list (a date-descending page of the newest
// 200 messages; total counts the window only).
type GmailListOut struct {
	UIDValidity int            `json:"uidvalidity"`
	Folder      string         `json:"folder"`
	Offset      int            `json:"offset"`
	Limit       int            `json:"limit"`
	Window      int            `json:"window"`
	Total       int            `json:"total"`
	HasMore     bool           `json:"has_more"`
	Messages    []GmailSummary `json:"messages"`
}

// MarshalJSON emits messages as [] instead of null.
func (o GmailListOut) MarshalJSON() ([]byte, error) {
	type alias GmailListOut
	o.Messages = EmptyIfNil(o.Messages)
	return json.Marshal(alias(o))
}

// GmailAttachmentInfo describes an attachment of gmail.messages.get. inline is
// present only for inline images referenced by the HTML.
type GmailAttachmentInfo struct {
	Index       int     `json:"index"`
	Filename    string  `json:"filename"`
	ContentType string  `json:"content_type"`
	Size        int     `json:"size"`
	ContentID   *string `json:"content_id"`
	Inline      *bool   `json:"inline,omitempty"`
}

// GmailMessageDetail is gmail.messages.get. html is sanitised and its remote
// images are rewritten to /gmail-image?token=... proxy URLs.
type GmailMessageDetail struct {
	UID         int                   `json:"uid"`
	From        string                `json:"from"`
	To          string                `json:"to"`
	Cc          string                `json:"cc"`
	Subject     string                `json:"subject"`
	DateTs      *int64                `json:"date_ts"`
	MessageID   string                `json:"message_id"`
	InReplyTo   string                `json:"in_reply_to"`
	References  string                `json:"references"`
	Text        string                `json:"text"`
	HTML        string                `json:"html"`
	Attachments []GmailAttachmentInfo `json:"attachments"`
}

// MarshalJSON emits attachments as [] instead of null.
func (d GmailMessageDetail) MarshalJSON() ([]byte, error) {
	type alias GmailMessageDetail
	d.Attachments = EmptyIfNil(d.Attachments)
	return json.Marshal(alias(d))
}

// GmailDeleteOut is gmail.messages.delete: {"ok":true,"trashed":true}.
type GmailDeleteOut struct {
	OK      bool `json:"ok"`
	Trashed bool `json:"trashed"`
}

// GmailBulkDeleteOut is gmail.messages.bulk_delete: how many moved to Trash and
// which requested UIDs no longer exist.
type GmailBulkDeleteOut struct {
	OK      bool  `json:"ok"`
	Trashed int   `json:"trashed"`
	Missing []int `json:"missing"`
}

// MarshalJSON emits missing as [] instead of null.
func (o GmailBulkDeleteOut) MarshalJSON() ([]byte, error) {
	type alias GmailBulkDeleteOut
	o.Missing = EmptyIfNil(o.Missing)
	return json.Marshal(alias(o))
}

// GmailAddressEntry is one compose-autocomplete address.
type GmailAddressEntry struct {
	Email  string  `json:"email"`
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	LastTs float64 `json:"last_ts"`
}

// GmailAddressBookOut is gmail.address_book (best first, cached 15 minutes).
type GmailAddressBookOut struct {
	Contacts []GmailAddressEntry `json:"contacts"`
}

// MarshalJSON emits contacts as [] instead of null.
func (o GmailAddressBookOut) MarshalJSON() ([]byte, error) {
	type alias GmailAddressBookOut
	o.Contacts = EmptyIfNil(o.Contacts)
	return json.Marshal(alias(o))
}

// GmailSendOut is gmail.messages.send: the Message-ID of the sent mail.
type GmailSendOut struct {
	OK        bool   `json:"ok"`
	MessageID string `json:"message_id"`
}

// GmailAttachmentOut is gmail.attachment: a temporary BlobOut (delete the file
// once delivered) plus the safe filename.
type GmailAttachmentOut struct {
	BlobOut
	Filename string `json:"filename"`
}
