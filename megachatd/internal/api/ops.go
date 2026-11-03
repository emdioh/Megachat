package api

import "reflect"

// OpSpec documents one registered operation. ParamsType / ResultType are the
// Go types of the request params and the "result" member. Clients lists who
// calls it today ("tui": the Textual client over the socket, "web": the SPA
// through REST, "internal": adapter-only pre-check, never called by a user).
// Every operation is callable over the socket by its name; the web adapter only
// exposes those with a route (see Routes).
type OpSpec struct {
	Name       string
	ParamsType reflect.Type
	ResultType reflect.Type
	Clients    string
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// Ops is the complete operation table (68 entries, sorted by name).
// Notes on result types that are unions:
//   - messages.send answers MessagesSendOut, or MessagesSendDuplicateOut (text
//     idempotent replay), or MessagesSendOptimisticOut (optimistic flavour).
//   - ListResults are slices: marshal them through EmptyIfNil.
var Ops = []OpSpec{
	{OpAutotranscribeChat, typeOf[AutoTranscribeChatRequest](), typeOf[AutoTranscribeChatOut](), "web"},
	{OpAutotranscribeConfig, typeOf[EmptyRequest](), typeOf[AutoTranscribeConfigOut](), "web"},
	{OpAutotranscribeSetChat, typeOf[AutoTranscribeSetChatRequest](), typeOf[AutoTranscribeChatOut](), "web"},
	{OpAutotranscribeSetGlobal, typeOf[AutoTranscribeSetGlobalRequest](), typeOf[map[string]bool](), "web"},
	{OpChatsArchive, typeOf[ChatsArchiveRequest](), typeOf[ChatsArchiveOut](), "web"},
	{OpChatsMarkRead, typeOf[ChatsMarkReadRequest](), typeOf[StatusOkOut](), "tui web"},
	{OpConfigSetOpenaiKey, typeOf[OpenAIKeyRequest](), typeOf[StatusOut](), "web"},
	{OpContactsAddressBook, typeOf[ContactsAddressBookRequest](), typeOf[ContactsAddressBookOut](), "tui"},
	{OpContactsAvatar, typeOf[ContactsAvatarRequest](), typeOf[BlobOut](), "web"},
	{OpContactsBook, typeOf[ContactsBookRequest](), typeOf[[]ContactOut](), "web"},
	{OpContactsCheckNumber, typeOf[ContactsCheckNumberRequest](), typeOf[ContactsCheckNumberOut](), "tui"},
	{OpContactsForProtocol, typeOf[ContactsForProtocolRequest](), typeOf[[]ContactWire](), "tui"},
	{OpContactsList, typeOf[ContactsListRequest](), typeOf[[]ContactOut](), "web"},
	{OpContactsRegister, typeOf[ContactsRegisterRequest](), typeOf[ContactWire](), "tui"},
	{OpDownloadServeAttachment, typeOf[DownloadServeAttachmentRequest](), typeOf[DownloadOut](), "tui"},
	{OpDownloadServeText, typeOf[DownloadServeTextRequest](), typeOf[DownloadOut](), "tui"},
	{OpEmojiList, typeOf[EmptyRequest](), typeOf[[]EmojiCategoryOut](), "web"},
	{OpGmailAddressBook, typeOf[EmptyRequest](), typeOf[GmailAddressBookOut](), "web"},
	{OpGmailAttachment, typeOf[GmailAttachmentRequest](), typeOf[GmailAttachmentOut](), "web"},
	{OpGmailImage, typeOf[GmailImageRequest](), typeOf[BlobOut](), "web"},
	{OpGmailMessagesArchive, typeOf[GmailMessageRef](), typeOf[OkOut](), "web"},
	{OpGmailMessagesBulkDelete, typeOf[GmailMessagesBulkDeleteRequest](), typeOf[GmailBulkDeleteOut](), "web"},
	{OpGmailMessagesDelete, typeOf[GmailMessageRef](), typeOf[GmailDeleteOut](), "web"},
	{OpGmailMessagesGet, typeOf[GmailMessageRef](), typeOf[GmailMessageDetail](), "web"},
	{OpGmailMessagesList, typeOf[GmailMessagesListRequest](), typeOf[GmailListOut](), "web"},
	{OpGmailMessagesMarkRead, typeOf[GmailMessageRef](), typeOf[OkOut](), "web"},
	{OpGmailMessagesMarkUnread, typeOf[GmailMessageRef](), typeOf[OkOut](), "web"},
	{OpGmailMessagesSend, typeOf[GmailMessagesSendRequest](), typeOf[GmailSendOut](), "web"},
	{OpGmailMessagesSendCheck, typeOf[GmailMessagesSendCheckRequest](), typeOf[OkOut](), "internal"},
	{OpGmailStatus, typeOf[EmptyRequest](), typeOf[GmailStatusOut](), "web"},
	{OpGmailUnread, typeOf[EmptyRequest](), typeOf[GmailUnreadOut](), "web"},
	{OpLinkCancel, typeOf[LinkProtocolRequest](), typeOf[OkOut](), "tui web"},
	{OpLinkFinalize, typeOf[LinkFinalizeRequest](), typeOf[LinkFinalizeOut](), "tui web"},
	{OpLinkPoll, typeOf[LinkProtocolRequest](), typeOf[LinkPollOut](), "tui web"},
	{OpLinkReconnect, typeOf[LinkReconnectRequest](), typeOf[OkOut](), "tui web"},
	{OpLinkRequired, typeOf[EmptyRequest](), typeOf[LinkRequiredOut](), "web"},
	{OpLinkStart, typeOf[LinkStartRequest](), typeOf[LinkStartOut](), "tui web"},
	{OpLinkStatus, typeOf[EmptyRequest](), typeOf[LinkStatusOut](), "tui web"},
	{OpLinkSubmit2fa, typeOf[Link2faRequest](), typeOf[Link2faOut](), "web"},
	{OpLinksPreview, typeOf[LinksPreviewRequest](), typeOf[LinkPreviewOut](), "web"},
	{OpLinksPreviewImage, typeOf[LinksPreviewRequest](), typeOf[BlobOut](), "web"},
	{OpMediaAttachment, typeOf[MediaAttachmentRequest](), typeOf[BlobOut](), "tui web"},
	{OpMediaQuote, typeOf[MediaQuoteRequest](), typeOf[BlobOut](), "web"},
	{OpMediaQuotedAttachment, typeOf[MediaQuotedAttachmentRequest](), typeOf[MediaQuotedAttachmentOut](), "tui"},
	{OpMediaThumbnail, typeOf[MediaThumbnailRequest](), typeOf[BlobOut](), "web"},
	{OpMessagesEdit, typeOf[MessagesEditRequest](), typeOf[OkOut](), "tui web"},
	{OpMessagesPage, typeOf[MessagesPageRequest](), typeOf[[]MessageRow](), "web"},
	{OpMessagesSend, typeOf[MessagesSendRequest](), typeOf[MessagesSendOut](), "tui web"},
	{OpMessagesSendCheck, typeOf[MessagesSendCheckRequest](), typeOf[OkOut](), "internal"},
	{OpMessagesSnapshot, typeOf[MessagesSnapshotRequest](), typeOf[MessagesSnapshotOut](), "tui"},
	{OpMessagesWindow, typeOf[MessagesWindowRequest](), typeOf[MessagesWindowOut](), "tui"},
	{OpReactionsAvailable, typeOf[ReactionsAvailableRequest](), typeOf[ReactionsAvailableOut](), "web"},
	{OpReactionsSet, typeOf[ReactionsSetRequest](), typeOf[ReactionsSetOut](), "web"},
	{OpServerClientLog, typeOf[ClientLogRequest](), typeOf[StatusOut](), "web"},
	{OpServerClients, typeOf[EmptyRequest](), typeOf[ClientsOut](), "tui"},
	{OpServerConfig, typeOf[EmptyRequest](), typeOf[ServerConfigOut](), "tui"},
	{OpServerLogs, typeOf[ServerLogsRequest](), typeOf[ServerLogsOut](), "web"},
	{OpServerLogsClear, typeOf[EmptyRequest](), typeOf[StatusOut](), "web"},
	{OpServerShutdown, typeOf[EmptyRequest](), typeOf[StatusOut](), "tui"},
	{OpServerStatus, typeOf[EmptyRequest](), typeOf[ServerStatus](), "tui web"},
	{OpTranscribeCheck, typeOf[EmptyRequest](), typeOf[TranscribeCheckOut](), "web"},
	{OpTranscribeConfig, typeOf[EmptyRequest](), typeOf[TranscribeConfigOut](), "web"},
	{OpTranscribeDiagnostics, typeOf[EmptyRequest](), typeOf[TranscribeDiagnosticsOut](), "web"},
	{OpTranscribeSetApiKey, typeOf[TranscribeApiKeyRequest](), typeOf[StatusOut](), "web"},
	{OpTranscribeSetSettings, typeOf[TranscribeSettingsRequest](), typeOf[TranscribeSettingsOut](), "web"},
	{OpTranscribeSettings, typeOf[EmptyRequest](), typeOf[TranscribeSettingsOut](), "web"},
	{OpTranscribeStatus, typeOf[TranscribeStatusRequest](), typeOf[TranscribeStatusOut](), "web"},
	{OpTranscribeSubmit, typeOf[TranscribeSubmitRequest](), typeOf[TranscribeSubmitOut](), "web"},
}

// OpByName returns the spec of an operation.
func OpByName(name string) (OpSpec, bool) {
	for _, op := range Ops {
		if op.Name == name {
			return op, true
		}
	}
	return OpSpec{}, false
}
