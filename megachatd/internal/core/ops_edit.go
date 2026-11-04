package core

import (
	"context"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// messageEditID is messages._message_edit_id: the id a backend needs to edit
// the row, or "" when the row cannot be edited (not our own text message, or
// still pending / failed).
func messageEditID(row *store.EditTarget) string {
	if !row.IsMine || store.Deref(row.MsgType) != "text" {
		return ""
	}
	if s := store.Deref(row.Status); s == api.StatusPending || s == api.StatusFailed {
		return ""
	}
	id := store.Deref(row.MsgID)
	if row.Protocol == api.ProtocolSignal {
		if id != "" {
			return id
		}
		return itoa(row.Timestamp)
	}
	return id
}

// opEdit is messages.edit: backend edit, SQLite update, backend cache sync,
// then a message_edit web frame.
func (c *Core) opEdit(ctx context.Context, req api.MessagesEditRequest) (api.OkOut, error) {
	ctx = context.WithoutCancel(ctx)
	protocol, contactID := req.Protocol, req.ContactID
	messageID, newText := req.MessageID, req.NewText
	if !api.IsProtocol(protocol) || strings.TrimSpace(contactID) == "" ||
		strings.TrimSpace(messageID) == "" || strings.TrimSpace(newText) == "" ||
		runeLen(newText) > api.MaxTextLength {
		return api.OkOut{}, invalidRequest()
	}

	// Read-union cross-key: the row may live under the alias key.
	var editKeys []string
	if ct, ok := c.FindContact(ctx, protocol, contactID); ok {
		editKeys = ContactStorageKeys(ct)[1:]
	}
	row, err := c.st.MessageRowForEdit(ctx, protocol, contactID, messageID, editKeys...)
	if err != nil {
		return api.OkOut{}, &ops.Error{Code: api.CodeInternal, Message: "Database error"}
	}
	if row == nil {
		return api.OkOut{}, &ops.Error{Code: api.CodeNotFound, Message: "Not Found"}
	}
	if messageEditID(row) == "" {
		return api.OkOut{}, &ops.Error{Code: api.CodeInvalidRequest, Message: "Message not editable"}
	}

	fail := func(err error) error {
		c.log.Error("Web edit failed", "protocol", protocol, "contact", contactID, "err", err)
		return &ops.Error{Code: api.CodeBackendError, Message: "Message edit failed",
			Details: map[string]any{"error": err.Error()}}
	}
	edited, err := c.mgr.EditMessage(ctx, protocol, contactID, messageID, newText)
	if err != nil {
		return api.OkOut{}, fail(err)
	}
	if !edited {
		return api.OkOut{}, fail(errEditRejected)
	}
	updated, err := c.st.PersistMessageEdit(ctx, protocol, contactID, messageID, newText)
	if err != nil {
		return api.OkOut{}, fail(err)
	}
	if updated == 0 {
		c.log.Debug("Web edit persistence found no row", "protocol", protocol, "contact", contactID, "id", messageID)
	}
	if ed, err := proto.As[proto.Editor](c.mgr, protocol); err == nil {
		mine := true
		if _, err := ed.ApplyEdit(contactID, messageID, newText, proto.ApplyEditOptions{IsMine: &mine, MarkEdited: true}); err != nil {
			c.log.Debug("Web edit backend cache sync failed", "protocol", protocol, "contact", contactID, "err", err)
		}
	}
	c.PublishWebPush("message_edit", map[string]any{
		"protocol": protocol, "contact_id": contactID, "message_id": messageID,
		"timestamp": row.Timestamp, "old_text": store.Deref(row.Text), "text": newText, "is_mine": true,
	})
	return api.OkOut{OK: true}, nil
}

type constError string

func (e constError) Error() string { return string(e) }

const errEditRejected = constError("edit rejected by server")
