package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func isMissing(err error) bool {
	return errors.Is(err, proto.ErrNoBackend) || errors.Is(err, proto.ErrUnsupported)
}

// required wraps h so that a missing or null member is invalid_request, like
// a required pydantic field (encoding/json would silently zero it).
func required(h ops.Handler, keys ...string) ops.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(params, &m); err != nil {
			return nil, invalidRequest()
		}
		for _, k := range keys {
			if raw, ok := m[k]; !ok || api.RawIsNull(raw) {
				return nil, invalidRequest()
			}
		}
		return h(ctx, params)
	}
}

// RegisterOps registers the operations the core owns on reg:
//
//	server.status server.config server.clients server.shutdown server.client_log
//	server.logs server.logs_clear emoji.list reactions.available
//	contacts.list contacts.book contacts.for_protocol contacts.register
//	contacts.address_book contacts.check_number
//	chats.mark_read chats.archive
//	messages.send messages.send.check messages.edit reactions.set
//
// It panics on a duplicate name (the Registry's rule).
func (c *Core) RegisterOps(reg *ops.Registry) {
	add := func(name, doc string, h ops.Handler) { reg.Register(ops.Op{Name: name, Doc: doc, Handler: h}) }

	add(api.OpServerStatus, "Real per-protocol connection state.", ops.Typed(c.opServerStatus))
	add(api.OpServerConfig, "Client-relevant settings.", ops.Typed(c.opServerConfig))
	add(api.OpServerClients, "How many clients are connected.", ops.Typed(c.opServerClients(reg)))
	add(api.OpServerShutdown, "Stop the server cleanly, as SIGINT does.", ops.Typed(c.opServerShutdown(reg)))
	add(api.OpServerClientLog, "Write a line from a frontend to the log.", ops.Typed(c.opClientLog))
	add(api.OpServerLogs, "Recent server log lines (redacted).", ops.Typed(c.opServerLogs))
	add(api.OpServerLogsClear, "Clear the in-memory log buffer.", ops.Typed(c.opServerLogsClear))
	add(api.OpEmojiList, "Emoji picker categories with their shortcode aliases.", ops.Typed(c.opEmojiList))
	add(api.OpReactionsAvailable, "Reaction emojis the protocol allows.", required(ops.Typed(c.opReactionsAvailable), "protocol"))

	add(api.OpContactsList, "Active chats with unread counts.", ops.Typed(c.opContactsList))
	add(api.OpContactsBook, "Search the aggregated address book.", required(ops.Typed(c.opContactsBook), "q"))
	add(api.OpContactsForProtocol, "Snapshot of one backend's contacts.", required(ops.Typed(c.opContactsForProtocol), "protocol"))
	add(api.OpContactsRegister, "Open-or-create a contact (ghost).", required(ops.Typed(c.opContactsRegister), "id", "display_name", "protocol"))
	add(api.OpContactsAddressBook, "Whole address book with failures.", ops.Typed(c.opContactsAddressBook))
	add(api.OpContactsCheckNumber, "Does a phone number exist on WhatsApp?", required(ops.Typed(c.opContactsCheckNumber), "phone"))

	add(api.OpChatsMarkRead, "Mark a chat read.", required(ops.Typed(c.opChatsMarkRead), "protocol", "contact_id"))
	add(api.OpChatsArchive, "Archive/unarchive a WhatsApp chat.", required(ops.Typed(c.opChatsArchive), "protocol", "contact_id", "archived"))

	add(api.OpMessagesSend, "Send a text message or attachments.", ops.Typed(c.opSend))
	add(api.OpMessagesSendCheck, "Validate a send before its attachments are stored.", ops.Typed(c.opSendCheck))
	add(api.OpMessagesEdit, "Edit one of our own sent messages.", required(ops.Typed(c.opEdit), "protocol", "contact_id", "message_id", "new_text"))
	add(api.OpReactionsSet, "React to a message.", required(ops.Typed(c.opReactionsSet), "protocol", "contact_id", "message_id", "emoji"))
}
