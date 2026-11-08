package api

import "net/http"

// REST route paths (web/api.py, web/gmail_api.py, web/server.py). Paths with
// {name} are chi/FastAPI-style templates; {x:path} matches slashes.
const (
	RouteHealth = "/health"

	RouteEmoji              = "/api/emoji"
	RouteReactions          = "/api/reactions"
	RouteSend               = "/api/send"
	RouteStatus             = "/api/status"
	RouteContacts           = "/api/contacts"
	RouteContactsBook       = "/api/contacts/book"
	RouteContactAvatar      = "/api/contact-avatar"
	RouteLinkPreview        = "/api/link-preview"
	RouteLinkPreviewImage   = "/api/link-preview/image"
	RouteLog                = "/api/log"
	RouteLogs               = "/api/logs" // GET and DELETE
	RouteTranscribe         = "/api/transcribe"
	RouteOpenAIKey          = "/api/config/openai-key"
	RouteTranscribeDiag     = "/api/transcribe/diagnostics"
	RouteTranscribeCheck    = "/api/transcribe/check"
	RouteTranscribeConfig   = "/api/transcribe/config"
	RouteTranscribeSetting  = "/api/transcribe/settings" // GET and PUT
	RouteTranscribeAPIKey   = "/api/transcribe/api-key"
	RouteAutotranscribe     = "/api/autotranscribe"
	RouteAutotranscribeGlb  = "/api/autotranscribe/global/{proto}"
	RouteAutotranscribeChat = "/api/autotranscribe/chat" // GET and PUT
	RouteTranscribeStatus   = "/api/transcribe/{proto}/{attachment_id:path}"
	RouteTranscribeStatusQ  = "/api/transcribe/status" // ?protocol=&attachment_id=
	RouteMessages           = "/api/messages"
	RouteMessagesRead       = "/api/messages/read"
	RouteChatsArchive       = "/api/chats/archive"
	RouteMessagesEdit       = "/api/messages/edit"
	RouteMessagesReaction   = "/api/messages/reaction"
	RouteLinkStatus         = "/api/link/status"
	RouteLinkRequired       = "/api/link/required"
	RouteLinkStart          = "/api/link/start"
	RouteLinkPoll           = "/api/link/poll"
	RouteLinkFinalize       = "/api/link/finalize"
	RouteLinkCancel         = "/api/link/cancel"
	RouteLinkSubmit2FA      = "/api/link/submit_2fa"
	RouteLinkReconnect      = "/api/link/reconnect"
	RouteMedia              = "/api/media/{proto}/{attachment_id:path}"
	RouteQuoteMedia         = "/api/quote-media/{proto}/{message_row_id}"

	RouteGmailStatus      = "/api/gmail/status"
	RouteGmailUnread      = "/api/gmail/unread"
	RouteGmailAddressBook = "/api/gmail/address-book"
	RouteGmailMessages    = "/api/gmail/messages"
	RouteGmailBulkDelete  = "/api/gmail/messages/bulk-delete"
	RouteGmailMessage     = "/api/gmail/messages/{uid}" // GET and DELETE
	RouteGmailAttachment  = "/api/gmail/messages/{uid}/attachments/{index}"
	RouteGmailRead        = "/api/gmail/messages/{uid}/read"
	RouteGmailUnreadMsg   = "/api/gmail/messages/{uid}/unread"
	RouteGmailArchive     = "/api/gmail/messages/{uid}/archive"
	RouteGmailSend        = "/api/gmail/send"
	RouteGmailImage       = "/gmail-image" // NOT under /api: no Bearer token

	RouteWS   = "/ws"
	RouteWSV2 = "/ws/v2"
)

// Route describes one HTTP endpoint. Op is the operation it wraps ("" when the
// route is pure transport; GET /api/media switches to media.thumbnail when the
// w query parameter is present). Auth is true when the Bearer token is required
// (every path under /api/). Blob routes answer a file, not JSON.
type Route struct {
	Method string
	Path   string
	Auth   bool
	Op     string
	Blob   bool
}

// Routes is the complete REST table in registration order (WebSocket and the
// static mount are listed in CONTRACTS.md).
var Routes = []Route{
	{http.MethodGet, RouteHealth, false, "", false},

	{http.MethodGet, RouteEmoji, true, OpEmojiList, false},
	{http.MethodGet, RouteReactions, true, OpReactionsAvailable, false},
	{http.MethodPost, RouteSend, true, OpMessagesSend, false},
	{http.MethodGet, RouteStatus, true, OpServerStatus, false},
	{http.MethodGet, RouteContacts, true, OpContactsList, false},
	{http.MethodGet, RouteContactsBook, true, OpContactsBook, false},
	{http.MethodGet, RouteContactAvatar, true, OpContactsAvatar, true},
	{http.MethodGet, RouteLinkPreview, true, OpLinksPreview, false},
	{http.MethodGet, RouteLinkPreviewImage, true, OpLinksPreviewImage, true},
	{http.MethodPost, RouteLog, true, OpServerClientLog, false},
	{http.MethodPost, RouteTranscribe, true, OpTranscribeSubmit, false},
	{http.MethodPost, RouteOpenAIKey, true, OpConfigSetOpenaiKey, false},
	{http.MethodGet, RouteLogs, true, OpServerLogs, false},
	{http.MethodDelete, RouteLogs, true, OpServerLogsClear, false},
	{http.MethodGet, RouteTranscribeDiag, true, OpTranscribeDiagnostics, false},
	{http.MethodPost, RouteTranscribeCheck, true, OpTranscribeCheck, false},
	{http.MethodGet, RouteTranscribeConfig, true, OpTranscribeConfig, false},
	{http.MethodGet, RouteTranscribeSetting, true, OpTranscribeSettings, false},
	{http.MethodPut, RouteTranscribeSetting, true, OpTranscribeSetSettings, false},
	{http.MethodPut, RouteTranscribeAPIKey, true, OpTranscribeSetApiKey, false},
	{http.MethodGet, RouteAutotranscribe, true, OpAutotranscribeConfig, false},
	{http.MethodPut, RouteAutotranscribeGlb, true, OpAutotranscribeSetGlobal, false},
	{http.MethodGet, RouteAutotranscribeChat, true, OpAutotranscribeChat, false},
	{http.MethodPut, RouteAutotranscribeChat, true, OpAutotranscribeSetChat, false},
	{http.MethodGet, RouteTranscribeStatusQ, true, OpTranscribeStatus, false},
	{http.MethodGet, RouteTranscribeStatus, true, OpTranscribeStatus, false},
	{http.MethodGet, RouteMessages, true, OpMessagesPage, false},
	{http.MethodPost, RouteMessagesRead, true, OpChatsMarkRead, false},
	{http.MethodPost, RouteChatsArchive, true, OpChatsArchive, false},
	{http.MethodPost, RouteMessagesEdit, true, OpMessagesEdit, false},
	{http.MethodPost, RouteMessagesReaction, true, OpReactionsSet, false},
	{http.MethodGet, RouteLinkStatus, true, OpLinkStatus, false},
	{http.MethodGet, RouteLinkRequired, true, OpLinkRequired, false},
	{http.MethodPost, RouteLinkStart, true, OpLinkStart, false},
	{http.MethodPost, RouteLinkPoll, true, OpLinkPoll, false},
	{http.MethodPost, RouteLinkFinalize, true, OpLinkFinalize, false},
	{http.MethodPost, RouteLinkCancel, true, OpLinkCancel, false},
	{http.MethodPost, RouteLinkSubmit2FA, true, OpLinkSubmit2fa, false},
	{http.MethodPost, RouteLinkReconnect, true, OpLinkReconnect, false},
	{http.MethodGet, RouteMedia, true, OpMediaAttachment, true}, // with ?w=N: media.thumbnail,
	{http.MethodGet, RouteQuoteMedia, true, OpMediaQuote, true},

	{http.MethodGet, RouteGmailStatus, true, OpGmailStatus, false},
	{http.MethodGet, RouteGmailUnread, true, OpGmailUnread, false},
	{http.MethodGet, RouteGmailAddressBook, true, OpGmailAddressBook, false},
	{http.MethodGet, RouteGmailMessages, true, OpGmailMessagesList, false},
	{http.MethodPost, RouteGmailBulkDelete, true, OpGmailMessagesBulkDelete, false},
	{http.MethodDelete, RouteGmailMessage, true, OpGmailMessagesDelete, false},
	{http.MethodGet, RouteGmailMessage, true, OpGmailMessagesGet, false},
	{http.MethodGet, RouteGmailAttachment, true, OpGmailAttachment, true},
	{http.MethodPost, RouteGmailRead, true, OpGmailMessagesMarkRead, false},
	{http.MethodPost, RouteGmailUnreadMsg, true, OpGmailMessagesMarkUnread, false},
	{http.MethodPost, RouteGmailArchive, true, OpGmailMessagesArchive, false},
	{http.MethodPost, RouteGmailSend, true, OpGmailMessagesSend, false},
	{http.MethodGet, RouteGmailImage, false, OpGmailImage, true},
}
