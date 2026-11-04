package core

import (
	"context"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// chats.mark_read: backend mark-read (failures swallowed) plus read=1 in SQLite.
func (c *Core) opChatsMarkRead(ctx context.Context, req api.ChatsMarkReadRequest) (api.StatusOkOut, error) {
	protocol, contactID := strings.TrimSpace(req.Protocol), strings.TrimSpace(req.ContactID)
	if !api.IsProtocol(protocol) || contactID == "" {
		return api.StatusOkOut{}, invalidRequest()
	}
	ctx = context.WithoutCancel(ctx)
	if err := c.mgr.MarkRead(ctx, protocol, contactID); err != nil {
		c.log.Warn("web mark-read failed", "protocol", protocol, "contact_id", contactID, "err", err)
	}
	var keys []string
	if ct, ok := c.FindContact(ctx, protocol, contactID); ok {
		keys = ContactStorageKeys(ct)[1:]
	}
	if err := c.st.MarkAsRead(ctx, protocol, contactID, keys...); err != nil {
		c.log.Warn("mark-read persist failed", "protocol", protocol, "contact_id", contactID, "err", err)
	}
	return api.StatusOkOut{Status: "ok"}, nil
}

// chats.archive: WhatsApp only.
func (c *Core) opChatsArchive(ctx context.Context, req api.ChatsArchiveRequest) (api.ChatsArchiveOut, error) {
	protocol, contactID := strings.TrimSpace(req.Protocol), strings.TrimSpace(req.ContactID)
	if protocol != api.ProtocolWhatsApp || contactID == "" {
		return api.ChatsArchiveOut{}, invalidRequest()
	}
	ok, err := c.mgr.SetChatArchived(ctx, protocol, contactID, req.Archived)
	if err != nil && (isMissing(err)) {
		return api.ChatsArchiveOut{}, &ops.Error{Code: api.CodeBackendDown, Message: "Backend unavailable"}
	}
	if err != nil || !ok {
		return api.ChatsArchiveOut{}, &ops.Error{Code: api.CodeBackendError, Message: "Archive failed"}
	}
	return api.ChatsArchiveOut{Archived: req.Archived}, nil
}
