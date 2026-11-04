package core

import (
	"context"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func storeQuery(protocol string, keys []string, limit int) store.MessageQuery {
	return store.MessageQuery{Protocol: protocol, Keys: keys, SQLLimit: limit}
}

func optStr(m map[string]any, k string) *string {
	switch v := m[k].(type) {
	case string:
		return &v
	case nil:
		return nil
	}
	return nil
}

func optInt(m map[string]any, k string) *int64 {
	if n, ok := asInt64(m[k]); ok {
		return &n
	}
	return nil
}

// idString is str(value) for ids that may arrive as numbers.
func idString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case nil:
		return "", false
	}
	if n, ok := asInt64(v); ok {
		return itoa(n), true
	}
	return "", false
}

// ingest stores a backend message. A backend implementing MessageIngester
// owns the decision; otherwise the store decides: an identical row is
// unchanged, an outgoing echo carrying a real id upgrades its id-less
// optimistic twin ("changed"), anything else is added.
func (c *Core) ingest(ctx context.Context, protocol, contactID string, msg map[string]any, ts int64) (IngestResult, error) {
	if b, ok := c.mgr.Get(protocol); ok {
		if ing, ok := b.(MessageIngester); ok {
			return ing.IngestMessage(ctx, contactID, msg, ts)
		}
	}
	text, _ := msg["text"].(string)
	msgType, _ := msg["msg_type"].(string)
	if msgType == "image" {
		text = ""
	}
	if msgType == "" {
		msgType = api.MsgTypeText
	}
	isMine := asBool(msg["is_mine"])
	sender, _ := msg["sender"].(string)
	id, hasID := idString(msg["id"])
	attID := optStr(msg, "attachment_id")

	if isMine && hasID {
		// Attach the real id to the optimistic twin, keeping its timestamp.
		upgraded, err := c.attachMessageID(ctx, protocol, contactID, text, ts, id)
		if err != nil {
			return IngestUnchanged, err
		}
		if upgraded {
			return IngestChanged, nil
		}
	}
	nm := store.NewMessage{
		Protocol: protocol, ContactNumber: contactID, Text: text, IsMine: isMine,
		Sender: sender, Timestamp: ts, MsgType: msgType,
		QuoteText: optStr(msg, "quote_text"), AttachmentInfo: optStr(msg, "attachment_info"),
		AttachmentID: attID, ContentType: optStr(msg, "content_type"), MediaKind: optStr(msg, "media_kind"),
		QuoteTimestamp: optInt(msg, "quote_timestamp"), QuoteAuthor: optStr(msg, "quote_author"),
		ReplyToMessageID:  optStr(msg, "reply_to_message_id"),
		QuoteAttachmentID: optStr(msg, "quote_attachment_id"), QuoteAttachmentPath: optStr(msg, "quote_attachment_path"),
		QuoteContentType: optStr(msg, "quote_content_type"), BatchID: optStr(msg, "batch_id"),
		BatchIndex: optInt(msg, "batch_index"),
	}
	if hasID {
		nm.MsgID = &id
	}
	if st, ok := msg["status"].(string); ok {
		nm.Status = st
	}
	_, inserted, err := c.st.AddMessage(ctx, nm)
	if err != nil {
		return IngestUnchanged, err
	}
	if inserted {
		return IngestAdded, nil
	}
	return IngestUnchanged, nil
}

var statusRanks = map[string]int{"pending": 0, "failed": 0, "sent": 1, "delivered": 2, "read": 3}

// cacheEntry renders a stored row in the TUI "cache entry" shape.
func cacheEntry(m store.Message) map[string]any {
	d := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	out := map[string]any{
		"id": d(m.MsgID), "text": store.Deref(m.Text), "is_mine": m.IsMine,
		"sender": d(m.Sender), "timestamp": m.Timestamp, "quote_text": d(m.QuoteText),
		"msg_type": d(m.MsgType), "attachment_info": d(m.AttachmentInfo),
		"attachment_id": d(m.AttachmentID), "content_type": d(m.ContentType),
		"read": m.Read, "status": d(m.Status),
	}
	if m.QuoteTimestamp != nil {
		out["quote_timestamp"] = *m.QuoteTimestamp
	}
	if m.QuoteAuthor != nil {
		out["quote_author"] = *m.QuoteAuthor
	}
	if m.ReplyToMessageID != nil {
		out["reply_to_message_id"] = *m.ReplyToMessageID
	}
	if m.Edited {
		out["edited"] = true
	}
	return out
}

// processReceipt is the store-based fallback of backend.process_receipt.
// Signal receipts carry {isDelivery,isRead,timestamps} and match outgoing rows
// within a 1 s tolerance; the other protocols carry message_ids/is_read.
func (c *Core) processReceipt(ctx context.Context, protocol, contactID string, payload map[string]any) ([]map[string]any, error) {
	if b, ok := c.mgr.Get(protocol); ok {
		if rp, ok := b.(ReceiptProcessor); ok {
			return rp.ProcessReceipt(ctx, contactID, payload)
		}
	}
	var updated []map[string]any
	rows, err := c.st.ListMessages(ctx, storeQuery(protocol, []string{contactID}, 0))
	if err != nil {
		return nil, err
	}
	if protocol == api.ProtocolSignal {
		receipt, _ := payload["receipt"].(map[string]any)
		tsList, _ := receipt["timestamps"].([]any)
		var stamps []int64
		for _, t := range tsList {
			if n, ok := asInt64(t); ok {
				stamps = append(stamps, n)
			}
		}
		if len(stamps) == 0 || contactID == "" {
			return nil, nil
		}
		var newStatus string
		switch {
		case asBool(receipt["isRead"]):
			newStatus = api.StatusRead
		case asBool(receipt["isDelivery"]):
			newStatus = api.StatusDelivered
		default:
			return nil, nil
		}
		for _, m := range rows {
			if !m.IsMine {
				continue
			}
			match := false
			for _, t := range stamps {
				if d := m.Timestamp - t; d >= -1000 && d <= 1000 {
					match = true
					break
				}
			}
			old := "sent"
			if m.Status != nil {
				old = *m.Status
			}
			if !match || statusRanks[newStatus] <= statusRanks[old] {
				continue
			}
			if _, err := c.st.UpdateMessageStatus(ctx, m.Timestamp, newStatus, protocol, contactID, nil, nil); err != nil {
				c.log.Debug("receipt status update failed", "err", err)
			}
			m.Status = &newStatus
			updated = append(updated, cacheEntry(m))
		}
		return updated, nil
	}
	idsRaw, _ := payload["message_ids"].([]any)
	if sl, ok := payload["message_ids"].([]string); ok {
		for _, s := range sl {
			idsRaw = append(idsRaw, s)
		}
	}
	ids := map[string]bool{}
	for _, v := range idsRaw {
		if s, ok := idString(v); ok {
			ids[s] = true
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	target := api.StatusDelivered
	if asBool(payload["is_read"]) {
		target = api.StatusRead
	}
	for _, m := range rows {
		if !m.IsMine || m.MsgID == nil || !ids[*m.MsgID] {
			continue
		}
		old := "sent"
		if m.Status != nil {
			old = *m.Status
		}
		if statusRanks[target] <= statusRanks[old] {
			continue
		}
		if _, err := c.st.UpdateMessageStatusByID(ctx, *m.MsgID, target, protocol, &contactID); err != nil {
			c.log.Debug("receipt status update failed", "err", err)
		}
		updated = append(updated, map[string]any{
			"id": *m.MsgID, "timestamp": m.Timestamp, "status": target,
			"text": store.Deref(m.Text), "is_mine": true,
		})
	}
	return updated, nil
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

// attachMessageID gives the closest id-less outgoing row of text (within the
// echo window around ts) its real id while KEEPING the row's own (optimistic)
// timestamp, which is the entry's identity for receipts.
func (c *Core) attachMessageID(ctx context.Context, protocol, contactID, text string, ts int64, id string) (bool, error) {
	since := ts - store.EchoMatchWindowMs
	rows, err := c.st.ListMessages(ctx, store.MessageQuery{Protocol: protocol, Keys: []string{contactID}, Since: &since})
	if err != nil {
		return false, err
	}
	var best *store.Message
	var bestDelta int64
	for i := range rows {
		m := &rows[i]
		if !m.IsMine || store.Deref(m.Text) != text || (m.MsgID != nil && *m.MsgID != "") {
			continue
		}
		d := m.Timestamp - ts
		if d < 0 {
			d = -d
		}
		if d > store.EchoMatchWindowMs {
			continue
		}
		if best == nil || d < bestDelta {
			best, bestDelta = m, d
		}
	}
	if best == nil {
		return false, nil
	}
	return c.st.UpdateMessageID(ctx, protocol, contactID, text, true, best.Timestamp, id)
}
