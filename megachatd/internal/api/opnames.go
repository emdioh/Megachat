package api

// Operation names (server/api.py registry; 68 operations, sorted).
const (
	OpAutotranscribeChat      = "autotranscribe.chat"
	OpAutotranscribeConfig    = "autotranscribe.config"
	OpAutotranscribeSetChat   = "autotranscribe.set_chat"
	OpAutotranscribeSetGlobal = "autotranscribe.set_global"
	OpChatsArchive            = "chats.archive"
	OpChatsMarkRead           = "chats.mark_read"
	OpConfigSetOpenaiKey      = "config.set_openai_key"
	OpContactsAddressBook     = "contacts.address_book"
	OpContactsAvatar          = "contacts.avatar"
	OpContactsBook            = "contacts.book"
	OpContactsCheckNumber     = "contacts.check_number"
	OpContactsForProtocol     = "contacts.for_protocol"
	OpContactsList            = "contacts.list"
	OpContactsRegister        = "contacts.register"
	OpDownloadServeAttachment = "download.serve_attachment"
	OpDownloadServeText       = "download.serve_text"
	OpEmojiList               = "emoji.list"
	OpGmailAddressBook        = "gmail.address_book"
	OpGmailAttachment         = "gmail.attachment"
	OpGmailImage              = "gmail.image"
	OpGmailMessagesArchive    = "gmail.messages.archive"
	OpGmailMessagesBulkDelete = "gmail.messages.bulk_delete"
	OpGmailMessagesDelete     = "gmail.messages.delete"
	OpGmailMessagesGet        = "gmail.messages.get"
	OpGmailMessagesList       = "gmail.messages.list"
	OpGmailMessagesMarkRead   = "gmail.messages.mark_read"
	OpGmailMessagesMarkUnread = "gmail.messages.mark_unread"
	OpGmailMessagesSend       = "gmail.messages.send"
	OpGmailMessagesSendCheck  = "gmail.messages.send.check"
	OpGmailStatus             = "gmail.status"
	OpGmailUnread             = "gmail.unread"
	OpLinkCancel              = "link.cancel"
	OpLinkFinalize            = "link.finalize"
	OpLinkPoll                = "link.poll"
	OpLinkReconnect           = "link.reconnect"
	OpLinkRequired            = "link.required"
	OpLinkStart               = "link.start"
	OpLinkStatus              = "link.status"
	OpLinkSubmit2fa           = "link.submit_2fa"
	OpLinksPreview            = "links.preview"
	OpLinksPreviewImage       = "links.preview_image"
	OpMediaAttachment         = "media.attachment"
	OpMediaQuote              = "media.quote"
	OpMediaQuotedAttachment   = "media.quoted_attachment"
	OpMediaThumbnail          = "media.thumbnail"
	OpMessagesEdit            = "messages.edit"
	OpMessagesPage            = "messages.page"
	OpMessagesSend            = "messages.send"
	OpMessagesSendCheck       = "messages.send.check"
	OpMessagesSnapshot        = "messages.snapshot"
	OpMessagesWindow          = "messages.window"
	OpReactionsAvailable      = "reactions.available"
	OpReactionsSet            = "reactions.set"
	OpServerClientLog         = "server.client_log"
	OpServerClients           = "server.clients"
	OpServerConfig            = "server.config"
	OpServerLogs              = "server.logs"
	OpServerLogsClear         = "server.logs_clear"
	OpServerShutdown          = "server.shutdown"
	OpServerStatus            = "server.status"
	OpTranscribeCheck         = "transcribe.check"
	OpTranscribeConfig        = "transcribe.config"
	OpTranscribeDiagnostics   = "transcribe.diagnostics"
	OpTranscribeSetApiKey     = "transcribe.set_api_key"
	OpTranscribeSetSettings   = "transcribe.set_settings"
	OpTranscribeSettings      = "transcribe.settings"
	OpTranscribeStatus        = "transcribe.status"
	OpTranscribeSubmit        = "transcribe.submit"
)

// AllOperations lists every registered operation name, sorted (the
// "operations" member of rpc.hello).
var AllOperations = []string{
	OpAutotranscribeChat,
	OpAutotranscribeConfig,
	OpAutotranscribeSetChat,
	OpAutotranscribeSetGlobal,
	OpChatsArchive,
	OpChatsMarkRead,
	OpConfigSetOpenaiKey,
	OpContactsAddressBook,
	OpContactsAvatar,
	OpContactsBook,
	OpContactsCheckNumber,
	OpContactsForProtocol,
	OpContactsList,
	OpContactsRegister,
	OpDownloadServeAttachment,
	OpDownloadServeText,
	OpEmojiList,
	OpGmailAddressBook,
	OpGmailAttachment,
	OpGmailImage,
	OpGmailMessagesArchive,
	OpGmailMessagesBulkDelete,
	OpGmailMessagesDelete,
	OpGmailMessagesGet,
	OpGmailMessagesList,
	OpGmailMessagesMarkRead,
	OpGmailMessagesMarkUnread,
	OpGmailMessagesSend,
	OpGmailMessagesSendCheck,
	OpGmailStatus,
	OpGmailUnread,
	OpLinkCancel,
	OpLinkFinalize,
	OpLinkPoll,
	OpLinkReconnect,
	OpLinkRequired,
	OpLinkStart,
	OpLinkStatus,
	OpLinkSubmit2fa,
	OpLinksPreview,
	OpLinksPreviewImage,
	OpMediaAttachment,
	OpMediaQuote,
	OpMediaQuotedAttachment,
	OpMediaThumbnail,
	OpMessagesEdit,
	OpMessagesPage,
	OpMessagesSend,
	OpMessagesSendCheck,
	OpMessagesSnapshot,
	OpMessagesWindow,
	OpReactionsAvailable,
	OpReactionsSet,
	OpServerClientLog,
	OpServerClients,
	OpServerConfig,
	OpServerLogs,
	OpServerLogsClear,
	OpServerShutdown,
	OpServerStatus,
	OpTranscribeCheck,
	OpTranscribeConfig,
	OpTranscribeDiagnostics,
	OpTranscribeSetApiKey,
	OpTranscribeSetSettings,
	OpTranscribeSettings,
	OpTranscribeStatus,
	OpTranscribeSubmit,
}
