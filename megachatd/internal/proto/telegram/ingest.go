package telegram

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// entry is one message of the in-memory cache (the Python cache dict).
// Empty strings stand for None.
type entry struct {
	ID, Text            string
	IsMine              bool
	Sender              string
	Timestamp           int64
	QuoteText           string
	MsgType             string
	AttachmentInfo      string
	AttachmentID        string
	ContentType         string
	Read                bool
	Status              string
	QuoteTimestamp      *int64
	QuoteAuthor         string
	ReplyToMessageID    string
	QuoteAttachmentID   string
	QuoteAttachmentPath string
	QuoteContentType    string
	Edited              bool
	BatchID             string
	BatchIndex          *int64
}

func entryFromStore(m store.Message) *entry {
	return &entry{
		ID: store.Deref(m.MsgID), Text: store.Deref(m.Text), IsMine: m.IsMine,
		Sender: store.Deref(m.Sender), Timestamp: m.Timestamp, QuoteText: store.Deref(m.QuoteText),
		MsgType: store.Deref(m.MsgType), AttachmentInfo: store.Deref(m.AttachmentInfo),
		AttachmentID: store.Deref(m.AttachmentID), ContentType: store.Deref(m.ContentType),
		Read: m.Read, Status: store.Deref(m.Status), QuoteTimestamp: m.QuoteTimestamp,
		QuoteAuthor: store.Deref(m.QuoteAuthor), ReplyToMessageID: store.Deref(m.ReplyToMessageID),
		QuoteAttachmentID: store.Deref(m.QuoteAttachmentID), QuoteAttachmentPath: store.Deref(m.QuoteAttachmentPath),
		QuoteContentType: store.Deref(m.QuoteContentType), Edited: m.Edited,
		BatchID: store.Deref(m.BatchID), BatchIndex: m.BatchIndex,
	}
}

// inMsg is the parsed ingest payload (the Python data dict).
type inMsg struct {
	ID, Text                                     string
	IsMine                                       bool
	Sender, QuoteText, MsgType, AttachmentInfo   string
	AttachmentID, ContentType, MediaKind, Status string
	QuoteTimestamp                               *int64
	QuoteAuthor, ReplyTo                         string
	QuoteAttachmentID, QuoteAttachmentPath       string
	QuoteContentType                             string
	BatchID                                      string
	BatchIndex                                   *int64
}

func parseInMsg(m map[string]any) inMsg {
	d := inMsg{
		ID: getStr(m, "id"), Text: getStr(m, "text"), IsMine: truthy(m["is_mine"]),
		Sender: getStr(m, "sender"), QuoteText: getStr(m, "quote_text"), MsgType: getStr(m, "msg_type"),
		AttachmentInfo: getStr(m, "attachment_info"), AttachmentID: getStr(m, "attachment_id"),
		ContentType: getStr(m, "content_type"), MediaKind: getStr(m, "media_kind"), Status: getStr(m, "status"),
		QuoteAuthor: getStr(m, "quote_author"), ReplyTo: getStr(m, "reply_to_message_id"),
		QuoteAttachmentID: getStr(m, "quote_attachment_id"), QuoteAttachmentPath: getStr(m, "quote_attachment_path"),
		QuoteContentType: getStr(m, "quote_content_type"), BatchID: getStr(m, "batch_id"),
	}
	if d.MsgType == "" {
		d.MsgType = "text"
	}
	if n, ok := toInt64(m["quote_timestamp"]); ok && m["quote_timestamp"] != nil {
		d.QuoteTimestamp = &n
	}
	if n, ok := toInt64(m["batch_index"]); ok && m["batch_index"] != nil {
		d.BatchIndex = &n
	}
	return d
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// shouldUpgradeOutgoingAttachment is base.should_upgrade_outgoing_attachment.
func shouldUpgradeOutgoingAttachment(isMine bool, existing, incoming string) bool {
	return isMine && (existing == "" || !isFile(existing)) && incoming != "" && isFile(incoming)
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// loadProtocolCache is _load_protocol_cache.
func (b *Backend) loadProtocolCache(ctx context.Context) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	b.cache = map[string][]*entry{}
	b.seen = map[string]struct{}{}
	if b.st == nil {
		return
	}
	rows, err := b.st.LoadCache(ctx, proto.ProtocolTelegram)
	if err != nil {
		b.log.Error("Telegram: failed to load protocol cache", "err", err)
		return
	}
	for contact, msgs := range rows {
		for _, m := range msgs {
			e := entryFromStore(m)
			b.cache[contact] = append(b.cache[contact], e)
			if e.ID != "" {
				b.seen[e.ID] = struct{}{}
			}
		}
	}
}

// persistMessage is _persist_message.
func (b *Backend) persistMessage(ctx context.Context, contactID string, d inMsg, ts int64) {
	if b.st == nil {
		return
	}
	nm := store.NewMessage{
		Protocol: proto.ProtocolTelegram, ContactNumber: contactID, Text: d.Text, IsMine: d.IsMine,
		Sender: d.Sender, Timestamp: ts, MsgType: d.MsgType, Status: d.Status,
		QuoteText: ptrStr(d.QuoteText), AttachmentInfo: ptrStr(d.AttachmentInfo), AttachmentID: ptrStr(d.AttachmentID),
		ContentType: ptrStr(d.ContentType), MediaKind: ptrStr(d.MediaKind), MsgID: ptrStr(d.ID),
		QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: ptrStr(d.QuoteAuthor), ReplyToMessageID: ptrStr(d.ReplyTo),
		QuoteAttachmentID: ptrStr(d.QuoteAttachmentID), QuoteAttachmentPath: ptrStr(d.QuoteAttachmentPath),
		QuoteContentType: ptrStr(d.QuoteContentType), BatchID: ptrStr(d.BatchID), BatchIndex: d.BatchIndex,
	}
	if _, _, err := b.st.AddMessage(ctx, nm); err != nil {
		b.log.Error("Telegram: _add_message_to_cache failed", "err", err)
	}
}

// mergeBatchSlot is _merge_batch_slot.
func (b *Backend) mergeBatchSlot(ctx context.Context, contactID string, e *entry, d inMsg) bool {
	if d.BatchID == "" || e.BatchID != "" {
		return false
	}
	msgID := e.ID
	if msgID == "" {
		msgID = d.ID
	}
	if msgID == "" {
		b.log.Warn("Telegram: batch slot merge skipped, no msg_id", "contact", contactID, "batch_id", d.BatchID)
		return false
	}
	m, ok := b.st.(BatchSlotMerger)
	if !ok || b.st == nil {
		return false
	}
	touched, err := m.MergeBatchSlot(ctx, proto.ProtocolTelegram, contactID, msgID, d.BatchID, d.BatchIndex)
	if err != nil {
		b.log.Error("Telegram: batch slot merge failed", "err", err)
		return false
	}
	if !touched {
		b.log.Warn("Telegram: batch slot merge touched no row", "contact", contactID, "msg_id", msgID, "batch_id", d.BatchID)
		return false
	}
	e.BatchID, e.BatchIndex = d.BatchID, d.BatchIndex
	return true
}

// IngestMessage implements core.MessageIngester (ingest_message).
func (b *Backend) IngestMessage(ctx context.Context, contactID string, msg map[string]any, ts int64) (proto.IngestResult, error) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.ingestLocked(ctx, contactID, parseInMsg(msg), ts, true), nil
}

func (b *Backend) healCaption(ctx context.Context, contactID string, e *entry, d inMsg, ts int64) bool {
	if d.MsgType == "image" && isCaptionLike(d.AttachmentInfo) && !isCaptionLike(e.AttachmentInfo) {
		e.AttachmentInfo = d.AttachmentInfo
		if b.st != nil {
			ets := e.Timestamp
			if ets == 0 {
				ets = ts
			}
			if _, err := b.st.UpdateMessageAttachmentInfo(ctx, proto.ProtocolTelegram, contactID, d.ID, ets, d.AttachmentInfo); err != nil {
				b.log.Error("Telegram: attachment info update failed", "err", err)
			}
		}
		return true
	}
	return false
}

// ingestLocked is ingest_message with ingestMu held.
func (b *Backend) ingestLocked(ctx context.Context, contactID string, d inMsg, ts int64, persist bool) proto.IngestResult {
	if d.MsgType == "image" {
		d.Text = ""
	}
	mid, text := d.ID, d.Text

	if mid != "" {
		if _, dup := b.seen[mid]; dup {
			var e *entry
			for _, m := range b.cache[contactID] {
				if m.ID == mid {
					e = m
					break
				}
			}
			if e != nil && msgTypeOrText(e) == "text" && e.Text != text && text != "" {
				b.applyEditLocked(ctx, contactID, mid, text, nil, true)
			}
			attachmentChanged := false
			if e != nil && d.IsMine && e.IsMine && strings.HasPrefix(e.AttachmentID, tgrefPrefix) &&
				d.AttachmentID != "" && shouldUpgradeOutgoingAttachment(true, e.AttachmentID, d.AttachmentID) {
				b.log.Info("telegram ingest: echo is_mine upgrade", "id", mid, "att_incoming", d.AttachmentID, "att_existing", e.AttachmentID)
				e.AttachmentID = d.AttachmentID
				if b.st != nil {
					if _, err := b.st.UpdateMessageAttachmentID(ctx, proto.ProtocolTelegram, contactID, mid, e.Timestamp, d.AttachmentID, nil); err != nil {
						b.log.Error("Telegram: attachment id update failed", "err", err)
					}
				}
				attachmentChanged = true
			}
			captionChanged := e != nil && b.healCaption(ctx, contactID, e, d, ts)
			batchChanged := e != nil && b.mergeBatchSlot(ctx, contactID, e, d)
			if attachmentChanged || captionChanged || batchChanged {
				return proto.IngestChanged
			}
			return proto.IngestUnchanged
		}
		for _, m := range b.cache[contactID] {
			if m.ID == "" && m.Text == text && absInt64(m.Timestamp-ts) <= incomingDedupWindowMs {
				// Echo of an optimistic message: attach the real id, keep the
				// original optimistic timestamp and status intact.
				m.ID = mid
				b.seen[mid] = struct{}{}
				if b.st != nil {
					if _, err := b.st.UpdateMessageID(ctx, proto.ProtocolTelegram, contactID, text, m.IsMine, m.Timestamp, mid); err != nil {
						b.log.Error("Telegram: _update_message_id failed", "err", err)
					}
				}
				batchChanged := b.mergeBatchSlot(ctx, contactID, m, d)
				if b.healCaption(ctx, contactID, m, d, ts) {
					return proto.IngestChanged
				}
				if batchChanged {
					return proto.IngestChanged
				}
				return proto.IngestUnchanged
			}
		}
		b.seen[mid] = struct{}{}
	} else {
		for _, m := range b.cache[contactID] {
			if m.Text == text && absInt64(m.Timestamp-ts) <= incomingDedupWindowMs {
				return proto.IngestUnchanged
			}
		}
	}

	if persist {
		b.persistMessage(ctx, contactID, d, ts)
	}
	status := d.Status
	if status == "" {
		status = "read"
		if d.IsMine {
			status = "sent"
		}
	}
	b.cache[contactID] = append(b.cache[contactID], &entry{
		ID: mid, Text: text, IsMine: d.IsMine, Sender: d.Sender, Timestamp: ts, QuoteText: d.QuoteText,
		MsgType: d.MsgType, AttachmentInfo: d.AttachmentInfo, AttachmentID: d.AttachmentID,
		ContentType: d.ContentType, Read: d.IsMine, Status: status, QuoteTimestamp: d.QuoteTimestamp,
		QuoteAuthor: d.QuoteAuthor, ReplyToMessageID: d.ReplyTo, QuoteAttachmentID: d.QuoteAttachmentID,
		QuoteAttachmentPath: d.QuoteAttachmentPath, QuoteContentType: d.QuoteContentType,
		BatchID: d.BatchID, BatchIndex: d.BatchIndex,
	})
	if n := len(b.cache[contactID]); n > maxCachePerContact {
		b.cache[contactID] = append([]*entry(nil), b.cache[contactID][n-maxCachePerContact:]...)
	}
	return proto.IngestAdded
}

func msgTypeOrText(e *entry) string {
	if e.MsgType == "" {
		return "text"
	}
	return e.MsgType
}

// ---- edits -----------------------------------------------------------------------

// ApplyEdit implements proto.Editor.
func (b *Backend) ApplyEdit(contactID, messageID, newText string, opts proto.ApplyEditOptions) (*proto.EditResult, error) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.applyEditLocked(context.Background(), contactID, messageID, newText, opts.IsMine, opts.MarkEdited), nil
}

func (b *Backend) applyEditLocked(ctx context.Context, contactID, messageID, newText string, isMine *bool, markEdited bool) *proto.EditResult {
	for _, m := range b.cache[contactID] {
		if m.ID != messageID {
			continue
		}
		if isMine != nil && m.IsMine != *isMine {
			continue
		}
		if msgTypeOrText(m) != "text" {
			return nil
		}
		old := m.Text
		if old == newText {
			return nil // echo of our own edit: no-op
		}
		m.Text = newText
		m.Edited = markEdited
		if b.st != nil {
			mid := messageID
			if _, err := b.st.UpdateMessageText(ctx, store.TextUpdate{
				Protocol: proto.ProtocolTelegram, ContactNumber: contactID, NewText: newText,
				MsgID: &mid, ClearEdited: !markEdited,
			}); err != nil {
				b.log.Error("Telegram: update message text failed", "err", err)
			}
		}
		return &proto.EditResult{MessageID: messageID, Timestamp: m.Timestamp, OldText: old, Text: newText, IsMine: m.IsMine}
	}
	return nil
}

// ---- reactions ----------------------------------------------------------------------

// snapshotItem is one aggregate reaction of a snapshot payload.
type snapshotItem struct {
	Emoji   string
	Count   int64
	IsMine  bool
	Authors []string
}

func parseSnapshot(v any) []snapshotItem {
	var out []snapshotItem
	add := func(m map[string]any) {
		it := snapshotItem{Emoji: getStr(m, "emoji"), IsMine: truthy(m["is_mine"])}
		it.Count, _ = toInt64(m["count"])
		switch a := m["authors"].(type) {
		case []string:
			it.Authors = a
		case []any:
			for _, x := range a {
				if s, ok := x.(string); ok {
					it.Authors = append(it.Authors, s)
				}
			}
		}
		out = append(out, it)
	}
	switch t := v.(type) {
	case []map[string]any:
		for _, m := range t {
			add(m)
		}
	case []any:
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				add(m)
			}
		}
	}
	return out
}

// ApplyReaction implements proto.Reactor: it persists a complete reaction
// snapshot and returns the aggregate to push (nil = unknown target / no-op).
func (b *Backend) ApplyReaction(contactID string, payload map[string]any) (map[string]any, error) {
	if getStr(payload, "mode") != "snapshot" || b.st == nil {
		return nil, nil
	}
	ctx := context.Background()
	target := getStr(payload, "target_message_id")
	if target == "" {
		return nil, nil
	}
	var (
		targetTS       *int64
		targetResolved bool
	)
	b.ingestMu.Lock()
	for _, m := range b.cache[contactID] {
		if m.ID == target {
			ts := m.Timestamp
			targetTS, targetResolved = &ts, true
			break
		}
	}
	b.ingestMu.Unlock()
	if targetTS == nil {
		row, err := b.st.ResolveReactionTargetRow(ctx, proto.ProtocolTelegram, contactID, &target, nil)
		if err != nil {
			return nil, err
		}
		if row != nil {
			ts := row.Timestamp
			targetTS, targetResolved = &ts, true
		} else if n, ok := toInt64(payload["target_timestamp"]); ok && n != 0 {
			targetTS = &n
		}
	}
	var entries []store.ReactionEntry
	for _, it := range parseSnapshot(payload["snapshot"]) {
		entries = append(entries, store.ReactionEntry{
			Emoji: it.Emoji, AuthorKey: "__agg__:" + it.Emoji, Author: strings.Join(it.Authors, ", "),
			IsMine: it.IsMine, Count: it.Count,
		})
	}
	ts, ok := toInt64(payload["timestamp"])
	if !ok || ts == 0 {
		ts = nowMs()
	}
	changed, err := b.st.ReplaceReactionsSnapshot(ctx, proto.ProtocolTelegram, contactID, &target, targetTS, entries, ts)
	if err != nil {
		return nil, err
	}
	if !changed || !targetResolved {
		return nil, nil
	}
	rows, err := b.st.ReactionsForContact(ctx, proto.ProtocolTelegram, contactID)
	if err != nil {
		return nil, err
	}
	type agg struct {
		emoji   string
		count   int64
		isMine  bool
		authors []string
		ts      int64
	}
	var order []*agg
	byEmoji := map[string]*agg{}
	for _, r := range rows {
		if r.TargetMsgID == nil || *r.TargetMsgID != target {
			continue
		}
		a := byEmoji[r.Emoji]
		if a == nil {
			a = &agg{emoji: r.Emoji, ts: r.Timestamp}
			byEmoji[r.Emoji] = a
			order = append(order, a)
		}
		a.count += r.Count
		a.isMine = a.isMine || r.IsMine
		if r.Timestamp < a.ts {
			a.ts = r.Timestamp
		}
		if r.Author != "" {
			dup := false
			for _, x := range a.authors {
				if x == r.Author {
					dup = true
				}
			}
			if !dup {
				a.authors = append(a.authors, r.Author)
			}
		}
	}
	// sorted(key=(-count, timestamp)) is stable.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && (order[j].count > order[j-1].count ||
			(order[j].count == order[j-1].count && order[j].ts < order[j-1].ts)); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	reactions := make([]any, 0, len(order))
	for _, a := range order {
		authors := make([]any, len(a.authors))
		for i, s := range a.authors {
			authors[i] = s
		}
		reactions = append(reactions, map[string]any{
			"emoji": a.emoji, "count": a.count, "is_mine": a.isMine, "authors": authors,
		})
	}
	var tsOut any
	if targetTS != nil {
		tsOut = *targetTS
	}
	return map[string]any{"message_id": target, "timestamp": tsOut, "reactions": reactions}, nil
}

// ---- receipts -----------------------------------------------------------------------

// ProcessReceipt implements core.ReceiptProcessor (process_receipt): the only
// mutator for Telegram receipts. payload carries message_ids and is_read.
func (b *Backend) ProcessReceipt(ctx context.Context, contactID string, payload map[string]any) ([]map[string]any, error) {
	var ids []string
	switch t := payload["message_ids"].(type) {
	case []string:
		ids = t
	case []any:
		for _, x := range t {
			if s := getStr(map[string]any{"v": x}, "v"); s != "" {
				ids = append(ids, s)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	target := "delivered"
	if truthy(payload["is_read"]) {
		target = "read"
	}
	idSet := map[string]bool{}
	for _, i := range ids {
		idSet[i] = true
	}
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	var contacts []string
	if contactID != "" {
		contacts = []string{contactID}
	} else {
		for c := range b.cache {
			contacts = append(contacts, c)
		}
	}
	var updated []map[string]any
	for _, c := range contacts {
		msgs, ok := b.cache[c]
		if !ok {
			continue
		}
		for _, m := range msgs {
			if !m.IsMine || !idSet[m.ID] {
				continue
			}
			if statusRanks[target] > statusRank(m.Status, "sent") {
				m.Status = target
				updated = append(updated, map[string]any{
					"id": m.ID, "timestamp": m.Timestamp, "status": target, "text": m.Text, "is_mine": true,
				})
				if b.st != nil {
					cc := c
					if _, err := b.st.UpdateMessageStatusByID(ctx, m.ID, target, proto.ProtocolTelegram, &cc); err != nil {
						b.log.Error("Telegram: _update_message_status_by_id failed", "err", err)
					}
				}
			}
		}
	}
	return updated, nil
}

// MarkRead implements proto.Backend: persists read=1 (the remote read state is
// not touched, as in Python's mark_read_sync).
func (b *Backend) MarkRead(ctx context.Context, contactID string) error {
	if b.st == nil {
		return nil
	}
	if err := b.st.MarkAsRead(ctx, proto.ProtocolTelegram, contactID); err != nil {
		b.log.Debug("Telegram mark-read persistence failed", "err", err)
	}
	return nil
}

// cachedEntry returns a copy of the cached message with the id (zero value, false).
func (b *Backend) cachedEntry(contactID, msgID string) (entry, bool) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	for _, m := range b.cache[contactID] {
		if m.ID == msgID {
			return *m, true
		}
	}
	return entry{}, false
}

var _ = strconv.Itoa
