package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// equalJSON reports whether two JSON documents are semantically equal.
func equalJSON(t *testing.T, want, got []byte) bool {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("bad want JSON: %v\n%s", err, want)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("bad got JSON: %v\n%s", err, got)
	}
	return reflect.DeepEqual(w, g)
}

type rtCase struct {
	name string
	json string
	new  func() any
}

func ptrTo[T any]() func() any { return func() any { return new(T) } }

// handWritten samples follow the shape the Python code produces (docstrings
// and tests/test_web_*_contract.py); every Optional result member is spelled
// out as null because pydantic does not drop None.
var handWritten = map[string]string{
	"MessageRow": `{"id":"m1","text":"hello","direction":"in","sender":"Alice","timestamp":1000,
		"attachment":null,"quote_text":null,"quote_timestamp":null,"quote_author":"",
		"quote_attachment_id":null,"quote_content_type":null,"quote_thumb_url":null,
		"quote_media_placeholder":false,"status":null,"read":false,"edited":false,
		"edit_id":null,"batch_id":null,"batch_index":null}`,
	"MessageRowOut": `{"id":"m2","text":"yo","direction":"out","sender":"You","timestamp":2000,
		"attachment":{"attachment_id":"a.jpg","name":"a.jpg","type":"image/jpeg","media_kind":"image","version":"1-2"},
		"quote_text":"hello","quote_timestamp":1000,"quote_author":"Mario",
		"quote_attachment_id":"q.jpg","quote_content_type":"image/jpeg",
		"quote_thumb_url":"api/media/signal/q.jpg?w=96","quote_media_placeholder":true,
		"status":"sent","read":true,"edited":true,"edit_id":"2000","batch_id":"b","batch_index":1,
		"reactions":[{"emoji":"👍","count":2,"is_mine":true,"authors":["Alice","You"]}],
		"is_group":true,"media_placeholder":"Immagine","system":true}`,
	"MessageRowPending": `{"id":"3","text":"","direction":"in","sender":"","timestamp":3,
		"attachment":{"attachment_id":"tok","name":"Video","type":"video/*","media_kind":"video","pending":true},
		"quote_text":null,"quote_timestamp":null,"quote_author":"","quote_attachment_id":null,
		"quote_content_type":null,"quote_thumb_url":null,"quote_media_placeholder":false,
		"status":null,"read":false,"edited":false,"edit_id":null,"batch_id":null,"batch_index":null}`,
	"EmojiCategoryOut":       `{"category":"Smileys","icon":"😀","emojis":["😀","😃"],"aliases":{"😀":"grinning_face","😃":"grinning_face_with_big_eyes"}}`,
	"ReactionsAvailableOut":  `{"emojis":["👍","❤"]}`,
	"ReactionsAvailableNone": `{"emojis":[]}`,
	"ServerLogsOut":          `{"records":[{"seq":1,"time":1700000000.5,"level":"INFO","logger":"signal_tui","message":"up"}],"last_seq":1,"since":1700000000.25}`,
	"ServerLogsOutEmpty":     `{"records":[],"last_seq":0,"since":null}`,
	"MessagesSendOut":        `{"ok":true,"message_ids":["1700000000123"]}`,
	"MessagesSendDup":        `{"ok":true,"duplicate":true}`,
	"MessagesSendOpt":        `{"ok":true,"status":"sent","status_applied":true,"message_id":null}`,
	"ChatsArchiveOut":        `{"archived":true}`,
	"StatusOut":              `{"status":"ok"}`,
	"OkOut":                  `{"ok":true}`,
	"DownloadOut":            `{"url":"http://10.0.0.2:10042/message.txt"}`,
	"MediaQuotedAttachment":  `{"attachment_id":null}`,
	"ContactsCheckNumberOut": `{"exists":null}`,
	"LinkFinalizeOut":        `{"account":"+391234567890"}`,
	"Link2faOut":             `{"ok":false}`,
	"LinkRequiredOut":        `{"protocols":{"signal":false,"whatsapp":true,"telegram":false}}`,
	"TranscribeConfigOut":    `{"enabled":true}`,
	"TranscribeStatusUnk":    `{"status":"unknown"}`,
	"TranscribeStatusOk":     `{"status":"ok","text":"ciao","error":null,"model":"gpt-transcribe","updated_at":1700000000.5}`,
	"TranscribeSubmitDone":   `{"status":"done","text":"ciao"}`,
	"TranscribeSubmitPend":   `{"status":"pending"}`,
	"AutoTranscribeConfig":   `{"configured":true,"globals":{"whatsapp":true,"signal":false,"telegram":false}}`,
	"AutoTranscribeChat":     `{"global":true,"override":null,"effective":true}`,
	"TranscribeSettings": `{"configured":true,"provider":"openai",
		"providers":{"openai":{"api_key_set":true,"model":"gpt-transcribe","base_url":"https://api.openai.com/v1"},
		"openrouter":{"api_key_set":false,"model":"openai/whisper-large-v3","base_url":"https://openrouter.ai/api/v1","http_referer":"","app_title":""},
		"gemini":{"api_key_set":false,"model":"gemini-flash-latest","base_url":"https://generativelanguage.googleapis.com/v1beta"}},
		"api_key_set":true,"model":"gpt-transcribe","language":"","base_url":"https://api.openai.com/v1","timeout":120,"env_overrides":["language"]}`,
	"TranscribeDiagnostics": `{"provider":"openai","api_key_set":true,"model":"m","base_url":"u","service_active":true,
		"service_client":"OpenAIClient","service_model":"m","service_base_url":"u","worker_alive":true,"queue_size":0,
		"auto_transcribe":{"whatsapp":false,"signal":false,"telegram":false},"auto_listener":true,"ffmpeg":true,
		"env_overrides":[],"recent":[{"protocol":"signal","attachment_id":"a","status":"ok","text":"t","error":null,"model":"m","updated_at":1.5}]}`,
	"TranscribeCheck":   `{"ok":false,"provider":"openai","detail":"Chiave API mancante"}`,
	"TranscribeCheckOK": `{"ok":true,"provider":"openai","url":"https://api.openai.com/v1/models","http_status":200,"detail":"{}"}`,
	"GmailStatusOut": `{"configured":true,"address":"a@gmail.com","folder":"INBOX","connected":null,
		"limits":{"max_text_length":65536,"max_attachments":10,"max_attachment_bytes":26214400,
		"max_total_attachment_bytes":26214400,"max_download_bytes":52428800,"max_messages_limit":200}}`,
	"GmailUnreadOut": `{"configured":true,"unread":4}`,
	"GmailListOut": `{"uidvalidity":1,"folder":"INBOX","offset":0,"limit":50,"window":200,"total":1,"has_more":false,
		"messages":[{"uid":9,"from":"B <b@x>","to":"a@gmail.com","subject":"s","date_ts":1700000000,"snippet":"",
		"has_attachments":false,"seen":true,"flags":["\\Seen"]},
		{"uid":8,"from":"","to":"","subject":"","date_ts":null,"snippet":"","has_attachments":true,"seen":false,"flags":[]}]}`,
	"GmailMessageDetail": `{"uid":9,"from":"B","to":"A","cc":"","subject":"s","date_ts":1700000000,"message_id":"<1@x>",
		"in_reply_to":"","references":"","text":"hi","html":"<p>hi</p>",
		"attachments":[{"index":0,"filename":"a.png","content_type":"image/png","size":10,"content_id":"cid1","inline":true},
		{"index":1,"filename":"b.pdf","content_type":"application/pdf","size":20,"content_id":null}]}`,
	"GmailDelete":        `{"ok":true,"trashed":true}`,
	"GmailBulk":          `{"ok":true,"trashed":2,"missing":[5]}`,
	"GmailAddressBook":   `{"contacts":[{"email":"b@x","name":"B","score":3.5,"last_ts":1700000000}]}`,
	"GmailSendOut":       `{"ok":true,"message_id":"<abc@gmail.com>"}`,
	"GmailAttachmentOut": `{"path":"/tmp/x","content_type":"application/pdf","size":3,"version":"1-3","immutable":false,"filename":"b.pdf"}`,
	// Bus payloads built by hand (not covered by the generated samples).
	"event.gap_buffer":   `{"reason":"buffer","since":3}`,
	"event.gap_overflow": `{"reason":"overflow"}`,
	// Legacy /ws frames.
	"frame.message":         `{"type":"message","payload":{"id":null,"protocol":"signal","contact_id":"+1","timestamp":5,"is_mine":false}}`,
	"frame.message_send":    `{"protocol":"signal","contact_id":"+1","is_mine":true}`,
	"frame.message_edit":    `{"protocol":"signal","contact_id":"+1","message_id":"1","timestamp":2,"old_text":"a","text":"b","is_mine":true}`,
	"frame.reaction_update": `{"protocol":"telegram","contact_id":"9","message_id":"1","timestamp":2,"reactions":[{"emoji":"👍","count":1,"is_mine":true,"authors":["You"]}]}`,
	"frame.receipt":         `{"protocol":"signal","contact_id":"+1","updates":[{"id":null,"timestamp":0,"status":"sent","text":""},{"id":"1","timestamp":5,"status":"read","text":"yo"}]}`,
	"frame.typing":          `{"protocol":"signal","contact_id":"+1","action":"STOPPED"}`,
	"frame.contact_update":  `{"protocol":"signal","contact_id":"+1","display_name":"Ada","phone":null}`,
	"frame.contact_merged":  `{"protocol":"whatsapp","contact_id":"1@c.us","merged_into":"1@lid","display_name":"Z","phone":"1"}`,
	"frame.lid_warmup_done": `{"protocol":"whatsapp"}`,
	"frame.send_retry":      `{"client_msg_id":"c","protocol":"whatsapp","contact_id":"1@c.us","attempt":2,"max_attempts":3,"error":"timeout"}`,
	"frame.link_required":   `{"protocol":"whatsapp","required":true}`,
	"frame.gmail_unread":    `{"unread":3,"new_mail":true}`,
	"frame.heartbeat":       `{"type":"heartbeat"}`,
	"v2.error":              `{"type":"error","payload":{"reason":"invalid_subscribe"}}`,
	"health":                `{"status":"ok","port":4242,"auth_required":true,"version":"0.2.1"}`,
	"http.error":            `{"detail":"Invalid request"}`,
	"http.validation":       `{"detail":[{"type":"literal_error","loc":["query","proto"],"msg":"Input should be 'signal', 'whatsapp' or 'telegram'","input":"irc","ctx":{"expected":"'signal', 'whatsapp' or 'telegram'"}}]}`,
	// JSON-RPC envelopes.
	"rpc.result":      `{"jsonrpc":"2.0","id":1,"result":{"pong":true,"ts":1700000000.5}}`,
	"rpc.error":       `{"jsonrpc":"2.0","id":7,"error":{"code":-32001,"message":"Not Found","data":{"code":"not_found","message":"Not Found","details":null}}}`,
	"rpc.error_nil":   `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}`,
	"rpc.invalid":     `{"jsonrpc":"2.0","id":3,"error":{"code":-32602,"message":"Invalid request","data":{"code":"invalid_request","message":"Invalid request","details":[{"type":"missing","loc":["protocol"],"msg":"Field required"}]}}}`,
	"rpc.event":       `{"jsonrpc":"2.0","method":"event","params":{"seq":5,"type":"typing","payload":{"protocol":"signal","contact_id":"+1","action":"STARTED"},"ts":1700000000.1}}`,
	"rpc.heartbeat":   `{"jsonrpc":"2.0","method":"heartbeat","params":{"ts":1700000000.1}}`,
	"rpc.hello":       `{"api_version":1,"server_version":"0.2.1","operations":["chats.archive","server.status"]}`,
	"rpc.subscribe":   `{"subscribed":true,"last_seq":12}`,
	"rpc.unsubscribe": `{"subscribed":false}`,
	"runtime":         `{"pid":123,"socket":"/run/user/1000/megachat/server.sock","version":"0.2.1","api_version":1,"started_at":1700000000.25}`,
	// Requests (socket params / REST bodies).
	"req.messages_send": `{"protocol":"signal","contact_id":"+1","text":"hi","client_msg_id":"c1","quote_timestamp":1000,
		"quote_author":"+2","quote_message":"x","quote_content_type":"image/png","quote_attachment_id":"a.png","retry":false,
		"attachments":[{"path":"/tmp/a.png","filename":"a.png","content_type":"image/png","rejected":null}],
		"optimistic":{"timestamp":1700000000123,"resend":false,"quote_text":"x"}}`,
	"req.messages_send_check": `{"protocol":"whatsapp","contact_id":"1@c.us","text":"hi","attachment_count":2,"slots_ok":false,"retry":true}`,
	"req.messages_page":       `{"protocol":"signal","contact_id":"+1","refresh":true,"limit":50,"since":1000}`,
	"req.contacts_register":   `{"id":"+1","display_name":"Ada","protocol":"signal","extras":{"phone":"1"}}`,
	"req.link_start":          `{"protocol":"signal","phone":"+1","device_name":"Signal-TUI-Client"}`,
	"req.transcribe_settings": `{"provider":"gemini","providers":{"openrouter":{"http_referer":""}},"timeout":60}`,
	"req.autotranscribe_set":  `{"protocol":"signal","contact_id":"+1","enabled":null}`,
	"req.gmail_list":          `{"offset":0,"limit":50,"refresh":false}`,
	"req.gmail_send":          `{"to":"a@x","cc":"","bcc":"","subject":"s","text":"t","reply_to_uid":4,"reply_to_folder":"INBOX"}`,
	"req.media_quote":         `{"protocol":"signal","message_row_id":12,"width":96}`,
	"req.server_logs":         `{"after":0,"limit":500,"level":""}`,
}

func roundTripCases() []rtCase {
	return []rtCase{
		// generated from Python
		{"ContactWire", pythonSamples["ContactWire"], ptrTo[ContactWire]()},
		{"ContactOut", pythonSamples["ContactOut"], ptrTo[ContactOut]()},
		{"BlobOut", pythonSamples["BlobOut"], ptrTo[BlobOut]()},
		{"BlobOutNull", pythonSamples["BlobOutNull"], ptrTo[BlobOut]()},
		{"AttachmentIn", pythonSamples["AttachmentIn"], ptrTo[AttachmentIn]()},
		{"ServerConfigOut", pythonSamples["ServerConfigOut"], ptrTo[ServerConfigOut]()},
		{"ClientsOut", pythonSamples["ClientsOut"], ptrTo[ClientsOut]()},
		{"LinkStatusOut", pythonSamples["LinkStatusOut"], ptrTo[LinkStatusOut]()},
		{"LinkPollOut", pythonSamples["LinkPollOut"], ptrTo[LinkPollOut]()},
		{"LinkPollOutNull", pythonSamples["LinkPollOutNull"], ptrTo[LinkPollOut]()},
		{"LinkStartOut", pythonSamples["LinkStartOut"], ptrTo[LinkStartOut]()},
		{"ReactionsSetOut", pythonSamples["ReactionsSetOut"], ptrTo[ReactionsSetOut]()},
		{"LinkPreviewOut", pythonSamples["LinkPreviewOut"], ptrTo[LinkPreviewOut]()},
		{"ContactsAddressBookOut", pythonSamples["ContactsAddressBookOut"], ptrTo[ContactsAddressBookOut]()},
		{"MessagesWindowOut", pythonSamples["MessagesWindowOut"], ptrTo[MessagesWindowOut]()},
		// events (server.events_api.to_wire output)
		{"event.backend_connecting", pythonSamples["event.backend_connecting"], ptrTo[BackendConnectingPayload]()},
		{"event.backend_status", pythonSamples["event.backend_status"], ptrTo[BackendStatusPayload]()},
		{"event.backend_ready", pythonSamples["event.backend_ready"], ptrTo[BackendReadyPayload]()},
		{"event.backend_done", pythonSamples["event.backend_done"], ptrTo[BackendDonePayload]()},
		{"event.message", pythonSamples["event.message"], ptrTo[MessageEventPayload]()},
		{"event.message_edit", pythonSamples["event.message_edit"], ptrTo[MessageEditPayload]()},
		{"event.reaction_update", pythonSamples["event.reaction_update"], ptrTo[ReactionUpdatePayload]()},
		{"event.receipt", pythonSamples["event.receipt"], ptrTo[ReceiptPayload]()},
		{"event.typing", pythonSamples["event.typing"], ptrTo[TypingPayload]()},
		{"event.contact_update", pythonSamples["event.contact_update"], ptrTo[ContactUpdatePayload]()},
		{"event.contact_update_merged", pythonSamples["event.contact_update_merged"], ptrTo[ContactUpdatePayload]()},
		{"event.sent_mirror", pythonSamples["event.sent_mirror"], ptrTo[SentMirrorPayload]()},
		{"event.lid_warmup_done", pythonSamples["event.lid_warmup_done"], ptrTo[LidWarmupDonePayload]()},
		{"event.web_push", pythonSamples["event.web_push"], ptrTo[WebPushPayload]()},
		{"event.gap_buffer", handWritten["event.gap_buffer"], ptrTo[GapPayload]()},
		{"event.gap_overflow", handWritten["event.gap_overflow"], ptrTo[GapPayload]()},
		// hand written
		{"MessageRow", handWritten["MessageRow"], ptrTo[MessageRow]()},
		{"MessageRowOut", handWritten["MessageRowOut"], ptrTo[MessageRow]()},
		{"MessageRowPending", handWritten["MessageRowPending"], ptrTo[MessageRow]()},
		{"EmojiCategoryOut", handWritten["EmojiCategoryOut"], ptrTo[EmojiCategoryOut]()},
		{"ReactionsAvailableOut", handWritten["ReactionsAvailableOut"], ptrTo[ReactionsAvailableOut]()},
		{"ReactionsAvailableNone", handWritten["ReactionsAvailableNone"], ptrTo[ReactionsAvailableOut]()},
		{"ServerLogsOut", handWritten["ServerLogsOut"], ptrTo[ServerLogsOut]()},
		{"ServerLogsOutEmpty", handWritten["ServerLogsOutEmpty"], ptrTo[ServerLogsOut]()},
		{"MessagesSendOut", handWritten["MessagesSendOut"], ptrTo[MessagesSendOut]()},
		{"MessagesSendDup", handWritten["MessagesSendDup"], ptrTo[MessagesSendDuplicateOut]()},
		{"MessagesSendOpt", handWritten["MessagesSendOpt"], ptrTo[MessagesSendOptimisticOut]()},
		{"ChatsArchiveOut", handWritten["ChatsArchiveOut"], ptrTo[ChatsArchiveOut]()},
		{"StatusOut", handWritten["StatusOut"], ptrTo[StatusOut]()},
		{"OkOut", handWritten["OkOut"], ptrTo[OkOut]()},
		{"DownloadOut", handWritten["DownloadOut"], ptrTo[DownloadOut]()},
		{"MediaQuotedAttachment", handWritten["MediaQuotedAttachment"], ptrTo[MediaQuotedAttachmentOut]()},
		{"ContactsCheckNumberOut", handWritten["ContactsCheckNumberOut"], ptrTo[ContactsCheckNumberOut]()},
		{"LinkFinalizeOut", handWritten["LinkFinalizeOut"], ptrTo[LinkFinalizeOut]()},
		{"Link2faOut", handWritten["Link2faOut"], ptrTo[Link2faOut]()},
		{"LinkRequiredOut", handWritten["LinkRequiredOut"], ptrTo[LinkRequiredOut]()},
		{"TranscribeConfigOut", handWritten["TranscribeConfigOut"], ptrTo[TranscribeConfigOut]()},
		{"TranscribeStatusUnk", handWritten["TranscribeStatusUnk"], ptrTo[TranscribeStatusUnknownOut]()},
		{"TranscribeStatusOk", handWritten["TranscribeStatusOk"], ptrTo[TranscribeStatusOut]()},
		{"TranscribeSubmitDone", handWritten["TranscribeSubmitDone"], ptrTo[TranscribeSubmitOut]()},
		{"TranscribeSubmitPend", handWritten["TranscribeSubmitPend"], ptrTo[TranscribeSubmitOut]()},
		{"AutoTranscribeConfig", handWritten["AutoTranscribeConfig"], ptrTo[AutoTranscribeConfigOut]()},
		{"AutoTranscribeChat", handWritten["AutoTranscribeChat"], ptrTo[AutoTranscribeChatOut]()},
		{"TranscribeSettings", handWritten["TranscribeSettings"], ptrTo[TranscribeSettingsOut]()},
		{"TranscribeDiagnostics", handWritten["TranscribeDiagnostics"], ptrTo[TranscribeDiagnosticsOut]()},
		{"TranscribeCheck", handWritten["TranscribeCheck"], ptrTo[TranscribeCheckOut]()},
		{"TranscribeCheckOK", handWritten["TranscribeCheckOK"], ptrTo[TranscribeCheckOut]()},
		{"GmailStatusOut", handWritten["GmailStatusOut"], ptrTo[GmailStatusOut]()},
		{"GmailUnreadOut", handWritten["GmailUnreadOut"], ptrTo[GmailUnreadOut]()},
		{"GmailListOut", handWritten["GmailListOut"], ptrTo[GmailListOut]()},
		{"GmailMessageDetail", handWritten["GmailMessageDetail"], ptrTo[GmailMessageDetail]()},
		{"GmailDelete", handWritten["GmailDelete"], ptrTo[GmailDeleteOut]()},
		{"GmailBulk", handWritten["GmailBulk"], ptrTo[GmailBulkDeleteOut]()},
		{"GmailAddressBook", handWritten["GmailAddressBook"], ptrTo[GmailAddressBookOut]()},
		{"GmailSendOut", handWritten["GmailSendOut"], ptrTo[GmailSendOut]()},
		{"GmailAttachmentOut", handWritten["GmailAttachmentOut"], ptrTo[GmailAttachmentOut]()},
		{"frame.message", handWritten["frame.message"], ptrTo[WebFrame]()},
		{"frame.message.payload", `{"id":null,"protocol":"signal","contact_id":"+1","timestamp":5,"is_mine":false}`, ptrTo[LegacyMessagePayload]()},
		{"frame.message_send", handWritten["frame.message_send"], ptrTo[LegacyMessageSentPayload]()},
		{"frame.message_edit", handWritten["frame.message_edit"], ptrTo[LegacyMessageEditPayload]()},
		{"frame.reaction_update", handWritten["frame.reaction_update"], ptrTo[LegacyReactionUpdatePayload]()},
		{"frame.receipt", handWritten["frame.receipt"], ptrTo[LegacyReceiptPayload]()},
		{"frame.typing", handWritten["frame.typing"], ptrTo[LegacyTypingPayload]()},
		{"frame.contact_update", handWritten["frame.contact_update"], ptrTo[LegacyContactUpdatePayload]()},
		{"frame.contact_merged", handWritten["frame.contact_merged"], ptrTo[LegacyContactUpdatePayload]()},
		{"frame.lid_warmup_done", handWritten["frame.lid_warmup_done"], ptrTo[LegacyLidWarmupPayload]()},
		{"frame.send_retry", handWritten["frame.send_retry"], ptrTo[SendRetryPayload]()},
		{"frame.link_required", handWritten["frame.link_required"], ptrTo[LinkRequiredPayload]()},
		{"frame.gmail_unread", handWritten["frame.gmail_unread"], ptrTo[GmailUnreadPayload]()},
		{"frame.heartbeat", handWritten["frame.heartbeat"], ptrTo[HeartbeatFrame]()},
		{"v2.error", handWritten["v2.error"], ptrTo[V2ErrorFrame]()},
		{"health", handWritten["health"], ptrTo[HealthResponse]()},
		{"http.error", handWritten["http.error"], ptrTo[HTTPError]()},
		{"http.validation", handWritten["http.validation"], ptrTo[HTTPValidationError]()},
		{"rpc.result", handWritten["rpc.result"], ptrTo[RPCResult]()},
		{"rpc.error", handWritten["rpc.error"], ptrTo[RPCErrorResponse]()},
		{"rpc.error_nil", handWritten["rpc.error_nil"], ptrTo[RPCErrorResponse]()},
		{"rpc.invalid", handWritten["rpc.invalid"], ptrTo[RPCErrorResponse]()},
		{"rpc.event", handWritten["rpc.event"], ptrTo[EventNotification]()},
		{"rpc.heartbeat", handWritten["rpc.heartbeat"], ptrTo[HeartbeatNotification]()},
		{"rpc.hello", handWritten["rpc.hello"], ptrTo[HelloResult]()},
		{"rpc.subscribe", handWritten["rpc.subscribe"], ptrTo[SubscribeResult]()},
		{"rpc.unsubscribe", handWritten["rpc.unsubscribe"], ptrTo[UnsubscribeResult]()},
		{"runtime", handWritten["runtime"], ptrTo[RuntimeFile]()},
		{"req.messages_send", handWritten["req.messages_send"], ptrTo[MessagesSendRequest]()},
		{"req.messages_send_check", handWritten["req.messages_send_check"], ptrTo[MessagesSendCheckRequest]()},
		{"req.messages_page", handWritten["req.messages_page"], ptrTo[MessagesPageRequest]()},
		{"req.contacts_register", handWritten["req.contacts_register"], ptrTo[ContactsRegisterRequest]()},
		{"req.link_start", handWritten["req.link_start"], ptrTo[LinkStartRequest]()},
		{"req.transcribe_settings", handWritten["req.transcribe_settings"], ptrTo[TranscribeSettingsRequest]()},
		{"req.autotranscribe_set", handWritten["req.autotranscribe_set"], ptrTo[AutoTranscribeSetChatRequest]()},
		{"req.gmail_list", handWritten["req.gmail_list"], ptrTo[GmailMessagesListRequest]()},
		{"req.gmail_send", handWritten["req.gmail_send"], ptrTo[GmailMessagesSendRequest]()},
		{"req.media_quote", handWritten["req.media_quote"], ptrTo[MediaQuoteRequest]()},
		{"req.server_logs", handWritten["req.server_logs"], ptrTo[ServerLogsRequest]()},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, tc := range roundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.new()
			if err := json.Unmarshal([]byte(tc.json), v); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !equalJSON(t, []byte(tc.json), out) {
				t.Fatalf("round trip differs\n want %s\n  got %s", compact(tc.json), out)
			}
		})
	}
}

func compact(s string) string {
	var b strings.Builder
	for _, f := range strings.Fields(s) {
		b.WriteString(f)
	}
	return b.String()
}

// Python never answers null for a list/dict that defaults to empty.
func TestNilCollectionsMarshalEmpty(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"ContactWire", ContactWire{ID: "a", DisplayName: "A", Protocol: "signal", CacheKey: "signal:a"},
			`{"id":"a","display_name":"A","protocol":"signal","cache_key":"signal:a","last_message_ts":0,"extras":{}}`},
		{"ContactOut", ContactOut{ID: "a", DisplayName: "A", Protocol: "signal"},
			`{"id":"a","display_name":"A","protocol":"signal","extras":{},"last_message_ts":0,"unread":0}`},
		{"ReactionsAvailableOut", ReactionsAvailableOut{}, `{"emojis":[]}`},
		{"ContactsAddressBookOut", ContactsAddressBookOut{}, `{"contacts":[],"errors":{}}`},
		{"MessagesWindowOut", MessagesWindowOut{}, `{"messages":[],"refreshed":false}`},
		{"ServerLogsOut", ServerLogsOut{}, `{"records":[],"last_seq":0,"since":null}`},
		{"ReactionsSetOut", ReactionsSetOut{OK: true}, `{"ok":true,"reactions":[]}`},
		{"Reaction", Reaction{Emoji: "x"}, `{"emoji":"x","count":0,"is_mine":false,"authors":[]}`},
		{"GmailBulkDeleteOut", GmailBulkDeleteOut{OK: true}, `{"ok":true,"trashed":0,"missing":[]}`},
		{"ReceiptPayload", ReceiptPayload{Protocol: "signal", ContactID: "1"}, `{"protocol":"signal","contact_id":"1","updated":[]}`},
		{"EmptyIfNil", EmptyIfNil[int](nil), `[]`},
	}
	for _, tc := range cases {
		out, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !equalJSON(t, []byte(tc.want), out) {
			t.Errorf("%s: want %s got %s", tc.name, tc.want, out)
		}
	}
}

func TestCacheMessageKeepsUnknownKeysAndNulls(t *testing.T) {
	in := `{"id":null,"text":"x","is_mine":true,"sender":"You","timestamp":1,"quote_text":null,"msg_type":"text",
	"attachment_info":null,"attachment_id":null,"content_type":null,"read":true,"status":"pending","future_key":{"a":[1,2]}}`
	var m CacheMessage
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Extra["future_key"]; !ok || len(m.Extra) != 1 {
		t.Fatalf("extra = %v", m.Extra)
	}
	out, _ := json.Marshal(m)
	if !equalJSON(t, []byte(in), out) {
		t.Fatalf("got %s", out)
	}
}

func TestRequestDefaults(t *testing.T) {
	var send MessagesSendRequest
	mustUnmarshal(t, `{"protocol":"signal"}`, &send)
	if !send.Retry {
		t.Error("retry must default to true")
	}
	var check MessagesSendCheckRequest
	mustUnmarshal(t, `{"protocol":"signal","attachment_count":3}`, &check)
	if !check.Retry || !check.SlotsOK || check.AttachmentCount != 3 {
		t.Errorf("check defaults: %+v", check)
	}
	var win MessagesWindowRequest
	mustUnmarshal(t, `{"protocol":"signal","contact_id":"1"}`, &win)
	if win.RefreshLimit != 50 {
		t.Error("refresh_limit default")
	}
	var link LinkStartRequest
	mustUnmarshal(t, `{"protocol":"signal"}`, &link)
	if link.DeviceName != DefaultDeviceName {
		t.Error("device_name default")
	}
	var fin LinkFinalizeRequest
	mustUnmarshal(t, `{}`, &fin)
	if fin.Protocol != ProtocolSignal {
		t.Error("finalize protocol default")
	}
	var dl DownloadServeTextRequest
	mustUnmarshal(t, `{"text":"t"}`, &dl)
	if dl.Filename != "message.txt" {
		t.Error("filename default")
	}
	var gl GmailMessagesListRequest
	mustUnmarshal(t, `{}`, &gl)
	if gl.Limit != 50 {
		t.Error("gmail limit default")
	}
	var logs ServerLogsRequest
	mustUnmarshal(t, `{}`, &logs)
	if logs.Limit != 500 {
		t.Error("logs limit default")
	}
	var cl ClientLogRequest
	mustUnmarshal(t, `{"message":"x"}`, &cl)
	if cl.Source != "client" {
		t.Error("client_log source default")
	}
	var gs GmailMessagesSendCheckRequest
	mustUnmarshal(t, `{"attachment_count":1}`, &gs)
	if !gs.SlotsOK {
		t.Error("gmail slots_ok default")
	}
}

func mustUnmarshal(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatal(err)
	}
}

func TestRawHelpers(t *testing.T) {
	ints := map[string]bool{`12`: true, `0`: true, `-3`: true, `12.0`: false, `1e3`: false, `true`: false, `"12"`: false, `null`: false}
	for in, want := range ints {
		if _, ok := RawInt(json.RawMessage(in)); ok != want {
			t.Errorf("RawInt(%s) = %v, want %v", in, ok, want)
		}
	}
	if s, ok := RawString(json.RawMessage(`"x"`)); !ok || s != "x" {
		t.Error("RawString")
	}
	if _, ok := RawString(json.RawMessage(`1`)); ok {
		t.Error("RawString of number")
	}
	if !RawIsNull(nil) || !RawIsNull(json.RawMessage(` null `)) || RawIsNull(json.RawMessage(`0`)) {
		t.Error("RawIsNull")
	}
}

func TestOpsTable(t *testing.T) {
	if len(Ops) != 68 {
		t.Fatalf("Ops has %d entries, want 68", len(Ops))
	}
	if len(AllOperations) != len(Ops) {
		t.Fatalf("AllOperations %d vs Ops %d", len(AllOperations), len(Ops))
	}
	if !sort.StringsAreSorted(AllOperations) {
		t.Error("AllOperations must be sorted (rpc.hello advertises it sorted)")
	}
	seen := map[string]bool{}
	for i, op := range Ops {
		if seen[op.Name] {
			t.Errorf("duplicate op %s", op.Name)
		}
		seen[op.Name] = true
		if op.Name != AllOperations[i] {
			t.Errorf("Ops[%d]=%s but AllOperations[%d]=%s", i, op.Name, i, AllOperations[i])
		}
		if op.ParamsType == nil || op.ResultType == nil {
			t.Errorf("%s: missing type", op.Name)
		}
		for _, b := range BuiltinMethods {
			if op.Name == b {
				t.Errorf("%s shadows a builtin method", op.Name)
			}
		}
		// every params type must decode {} (EmptyRequest) or an object
		if op.ParamsType.Kind() != reflect.Struct {
			t.Errorf("%s: params type %s is not a struct", op.Name, op.ParamsType)
		}
	}
	if spec, ok := OpByName(OpMessagesSend); !ok || spec.ParamsType != reflect.TypeFor[MessagesSendRequest]() {
		t.Error("OpByName(messages.send)")
	}
	// operations the Textual client calls (tui/client_timeouts.py OP_TIMEOUTS)
	tui := []string{"server.config", "server.status", "server.clients", "server.shutdown", "link.status",
		"contacts.check_number", "media.quoted_attachment", "contacts.for_protocol", "messages.snapshot",
		"messages.window", "media.attachment", "chats.mark_read", "contacts.register", "download.serve_attachment",
		"download.serve_text", "link.cancel", "link.reconnect", "messages.edit", "messages.send",
		"contacts.address_book", "link.start", "link.poll", "link.finalize", "link.submit_2fa"}
	for _, name := range tui {
		op, ok := OpByName(name)
		if !ok {
			t.Errorf("TUI op %s missing", name)
			continue
		}
		if !strings.Contains(op.Clients, "tui") && name != "link.submit_2fa" {
			t.Errorf("%s not flagged as a TUI op", name)
		}
	}
}

func TestOpParamsDecodeEmptyObject(t *testing.T) {
	for _, op := range Ops {
		v := reflect.New(op.ParamsType).Interface()
		if err := json.Unmarshal([]byte(`{}`), v); err != nil {
			t.Errorf("%s: params do not decode {}: %v", op.Name, err)
		}
	}
}

func TestErrorTables(t *testing.T) {
	if len(ErrorCodes) != 11 {
		t.Fatalf("ErrorCodes = %d", len(ErrorCodes))
	}
	for _, c := range ErrorCodes {
		if _, ok := RPCCodeFor[c]; !ok {
			t.Errorf("no RPC code for %s", c)
		}
	}
	// unauthorized/forbidden never come from operations but must still map
	for _, c := range ErrorCodes {
		if _, ok := HTTPStatusFor[c]; !ok {
			t.Errorf("no HTTP status for %s", c)
		}
	}
	if RPCCodeFor[CodeInvalidRequest] != -32602 || RPCCodeFor[CodeBackendError] != -32009 || RPCCodeFor[CodeInternal] != -32603 {
		t.Error("RPC code table drifted")
	}
	if HTTPStatusFor[CodeBackendDown] != 503 || HTTPStatusFor[CodeBackendError] != 502 || HTTPStatusFor[CodeTooLarge] != 413 {
		t.Error("HTTP status table drifted")
	}
}

func TestRoutesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Routes {
		key := r.Method + " " + r.Path
		if seen[key] {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = true
		if r.Auth != strings.HasPrefix(r.Path, "/api/") {
			t.Errorf("%s: Auth=%v but path prefix says otherwise", key, r.Auth)
		}
		if r.Op != "" {
			if _, ok := OpByName(r.Op); !ok {
				t.Errorf("%s: unknown op %s", key, r.Op)
			}
		}
	}
	if len(Routes) != 55 {
		t.Errorf("Routes has %d entries, want 55", len(Routes))
	}
}

func TestEventTypes(t *testing.T) {
	if len(EventTypes) != 14 {
		t.Fatalf("EventTypes = %d", len(EventTypes))
	}
	for _, name := range EventTypes {
		if name == "" {
			t.Fatal("empty event type")
		}
	}
	if len(LegacyFrameTypes) != 10 {
		t.Errorf("LegacyFrameTypes = %d", len(LegacyFrameTypes))
	}
}

func TestSocketPathAndRuntime(t *testing.T) {
	if got := DefaultSocketPath("/x/y.sock", "/run/user/1", "/home/u"); got != "/x/y.sock" {
		t.Errorf("override: %s", got)
	}
	if got := DefaultSocketPath("", "/run/user/1000", "/home/u"); got != "/run/user/1000/megachat/server.sock" {
		t.Errorf("xdg: %s", got)
	}
	if got := DefaultSocketPath("", "relative", "/home/u"); got != "/home/u/.local/state/megachat/server.sock" {
		t.Errorf("home: %s", got)
	}
	if got := RuntimePathFor("/run/user/1000/megachat/server.sock"); got != "/run/user/1000/megachat/server.json" {
		t.Errorf("runtime: %s", got)
	}
}

func TestRPCRequestIDPresence(t *testing.T) {
	var withID, notif RPCRequest
	mustUnmarshal(t, `{"jsonrpc":"2.0","id":"abc","method":"server.ping"}`, &withID)
	mustUnmarshal(t, `{"jsonrpc":"2.0","method":"server.ping","timeout":1.5}`, &notif)
	if string(withID.ID) != `"abc"` || len(notif.ID) != 0 {
		t.Errorf("id presence: %q / %q", withID.ID, notif.ID)
	}
	if notif.Timeout == nil || *notif.Timeout != 1.5 {
		t.Error("timeout")
	}
	out, _ := json.Marshal(notif)
	if strings.Contains(string(out), `"id"`) {
		t.Errorf("notification must not carry an id: %s", out)
	}
}
