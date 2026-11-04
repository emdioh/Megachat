package whatsapp

import (
	"context"
	"sort"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// entry is one message of the in-memory cache (the Python cache dict). Empty
// strings stand for None.
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
	MediaKind           string
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
	FromHistory         bool
}

func entryFromStore(m store.Message) *entry {
	return &entry{
		ID: store.Deref(m.MsgID), Text: store.Deref(m.Text), IsMine: m.IsMine,
		Sender: store.Deref(m.Sender), Timestamp: m.Timestamp, QuoteText: store.Deref(m.QuoteText),
		MsgType: store.Deref(m.MsgType), AttachmentInfo: store.Deref(m.AttachmentInfo),
		AttachmentID: store.Deref(m.AttachmentID), ContentType: store.Deref(m.ContentType),
		MediaKind: store.Deref(m.MediaKind),
		Read:      m.Read, Status: store.Deref(m.Status), QuoteTimestamp: m.QuoteTimestamp,
		QuoteAuthor: store.Deref(m.QuoteAuthor), ReplyToMessageID: store.Deref(m.ReplyToMessageID),
		QuoteAttachmentID: store.Deref(m.QuoteAttachmentID), QuoteAttachmentPath: store.Deref(m.QuoteAttachmentPath),
		QuoteContentType: store.Deref(m.QuoteContentType), Edited: m.Edited,
		BatchID: store.Deref(m.BatchID), BatchIndex: m.BatchIndex,
	}
}

func (e *entry) msgType() string {
	if e.MsgType == "" {
		return "text"
	}
	return e.MsgType
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
	hasStatus                                    bool
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

// shouldUpgradeOutgoingAttachment is base.should_upgrade_outgoing_attachment.
func shouldUpgradeOutgoingAttachment(isMine bool, existing, incoming string) bool {
	return isMine && (existing == "" || !isFile(existing)) && incoming != "" && isFile(incoming)
}

var statusRanks = map[string]int{"pending": 0, "failed": 0, "sent": 1, "delivered": 2, "read": 3}

func statusRank(s, def string) int {
	if s == "" {
		s = def
	}
	return statusRanks[s]
}

// loadProtocolCache is _load_protocol_cache: seeds the in-memory cache from
// SQLite so the dedup works across sessions.
func (b *Backend) loadProtocolCache(ctx context.Context) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	b.cache = map[string][]*entry{}
	b.seen = map[seenKey]struct{}{}
	if b.st == nil {
		return
	}
	rows, err := b.st.LoadCache(ctx, proto.ProtocolWhatsApp)
	if err != nil {
		b.log.Error("WhatsApp: failed to load protocol cache", "err", err)
		return
	}
	for contact, msgs := range rows {
		for _, m := range msgs {
			b.cache[contact] = append(b.cache[contact], entryFromStore(m))
		}
	}
}

// ---- cache helpers --------------------------------------------------------------

func msgSortKey(e *entry) (int64, string) {
	id := e.ID
	if id == "" {
		id = e.Text
	}
	return e.Timestamp, id
}

// sortContactCacheLocked is _sort_contact_cache.
func (b *Backend) sortContactCacheLocked(contact string) {
	msgs := b.cache[contact]
	sort.SliceStable(msgs, func(i, j int) bool {
		ti, ii := msgSortKey(msgs[i])
		tj, ij := msgSortKey(msgs[j])
		if ti != tj {
			return ti < tj
		}
		return ii < ij
	})
}

func (b *Backend) addCachedLocked(contact string, e *entry) {
	b.cache[contact] = append(b.cache[contact], e)
	b.sortContactCacheLocked(contact)
}

// messageAlreadyCachedLocked is _message_already_cached.
func (b *Backend) messageAlreadyCachedLocked(contact string, ts int64, isMine bool, text, msgID, attachmentID, msgType string, reconcile bool) *entry {
	entries := b.cache[contact]
	var bestIdless *entry
	var bestDelta int64 = -1
	for _, m := range entries {
		if m.IsMine != isMine {
			continue
		}
		cachedAtt := m.AttachmentID
		cachedMirror := isMine && isSentMirror(cachedAtt)
		incomingMirror := isMine && isSentMirror(attachmentID)
		sameAtt := attachmentID == "" || cachedAtt == "" || cachedAtt == attachmentID || cachedMirror != incomingMirror
		if isMine && sameMsgID(m.ID, msgID) && sameAtt {
			return m
		}
		if !isMine && msgID != "" && m.ID != "" && sameMsgID(m.ID, msgID) && sameAtt {
			return m
		}
		if m.Text != text {
			continue
		}
		switch {
		case !isMine:
			if attachmentID != "" && cachedAtt == attachmentID {
				return m
			}
			if attachmentID != "" && cachedAtt != attachmentID {
				continue
			}
			// Two messages with different ids are different messages: the
			// text+time heuristic is only for id-less entries (and for the
			// live path, where the same traffic can arrive twice).
			if strings.TrimSpace(text) != "" && absInt64(m.Timestamp-ts) <= 5000 &&
				(!reconcile || msgID == "" || m.ID == "") {
				return m
			}
		case msgID != "":
			if sameMsgID(m.ID, msgID) {
				return m
			}
			if m.ID == "" && sameAtt {
				delta := absInt64(m.Timestamp - ts)
				if delta <= echoMatchWindowMs && (bestDelta < 0 || delta < bestDelta) {
					bestIdless, bestDelta = m, delta
				}
			}
		default:
			if absInt64(m.Timestamp-ts) <= sendDedupWindowMs {
				return m
			}
		}
	}
	if bestIdless != nil {
		return bestIdless
	}
	if reconcile && isMine && msgID != "" && strings.TrimSpace(text) != "" && attachmentID == "" && msgType == "text" {
		var cands []*entry
		for _, m := range entries {
			if m.IsMine && m.Text == text && m.msgType() == "text" && m.AttachmentID == "" {
				cands = append(cands, m)
			}
		}
		if len(cands) == 1 {
			return cands[0]
		}
	}
	return nil
}

// isSentMirror is models.is_sent_mirror_attachment_id: a client-side "sent-*"
// attachment copy.
func isSentMirror(id string) bool {
	if id == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(pyBase(id)), "sent-")
}

// persistMessage is _persist_message.
func (b *Backend) persistMessage(ctx context.Context, contactID string, d inMsg, ts int64) {
	if b.st == nil {
		return
	}
	nm := store.NewMessage{
		Protocol: proto.ProtocolWhatsApp, ContactNumber: contactID, Text: d.Text, IsMine: d.IsMine,
		Sender: d.Sender, Timestamp: ts, MsgType: d.MsgType, Status: d.Status,
		QuoteText: ptrStr(d.QuoteText), AttachmentInfo: ptrStr(d.AttachmentInfo), AttachmentID: ptrStr(d.AttachmentID),
		ContentType: ptrStr(d.ContentType), MediaKind: ptrStr(d.MediaKind), MsgID: ptrStr(d.ID),
		QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: ptrStr(d.QuoteAuthor), ReplyToMessageID: ptrStr(d.ReplyTo),
		QuoteAttachmentID: ptrStr(d.QuoteAttachmentID), QuoteAttachmentPath: ptrStr(d.QuoteAttachmentPath),
		QuoteContentType: ptrStr(d.QuoteContentType), BatchID: ptrStr(d.BatchID), BatchIndex: d.BatchIndex,
	}
	if _, _, err := b.st.AddMessage(ctx, nm); err != nil {
		b.log.Error("WhatsApp: _add_message_to_cache failed", "err", err)
	}
}

// mergeBatchSlotLocked is _merge_batch_slot.
func (b *Backend) mergeBatchSlotLocked(ctx context.Context, contactID string, e *entry, d inMsg) bool {
	if d.BatchID == "" || e.BatchID != "" {
		return false
	}
	msgID := e.ID
	if msgID == "" {
		msgID = d.ID
	}
	if msgID == "" {
		b.log.Warn("WhatsApp: batch slot merge skipped, no msg_id", "contact", contactID, "batch_id", d.BatchID)
		return false
	}
	m, ok := b.st.(BatchSlotMerger)
	if !ok || b.st == nil {
		return false
	}
	touched, err := m.MergeBatchSlot(ctx, proto.ProtocolWhatsApp, contactID, msgID, d.BatchID, d.BatchIndex)
	if err != nil {
		b.log.Error("WhatsApp: batch slot merge failed", "err", err)
		return false
	}
	if !touched {
		b.log.Warn("WhatsApp: batch slot merge touched no row", "contact", contactID, "msg_id", msgID, "batch_id", d.BatchID)
		return false
	}
	e.BatchID, e.BatchIndex = d.BatchID, d.BatchIndex
	return true
}

// upgradeOutgoingAttachmentLocked is _upgrade_outgoing_attachment.
func (b *Backend) upgradeOutgoingAttachmentLocked(ctx context.Context, contactID string, m *entry, d inMsg, ts int64) bool {
	incoming := d.AttachmentID
	if incoming == "" {
		return false
	}
	mid := m.ID
	if mid == "" {
		mid = d.ID
	}
	etS := m.Timestamp
	if etS == 0 {
		etS = ts
	}
	if m.AttachmentID == "" {
		// Media race: the row was created without its attachment.
		m.AttachmentID = incoming
		if d.MsgType != "" {
			m.MsgType = d.MsgType
		}
		m.MediaKind = d.MediaKind
		if b.st != nil {
			var kind *string
			if m.MediaKind != "" {
				k := m.MediaKind
				kind = &k
			}
			if _, err := b.st.UpdateMessageMediaIdentity(ctx, proto.ProtocolWhatsApp, contactID, mid, etS, incoming, m.msgType(), kind); err != nil {
				b.log.Error("WhatsApp: media identity update failed", "err", err)
			}
		}
		return true
	}
	dir := b.mediaDir
	curPath := dir + "/" + pyBase(m.AttachmentID)
	incPath := dir + "/" + pyBase(incoming)
	if !strings.HasPrefix(pyBase(incoming), "sent-") || !shouldUpgradeOutgoingAttachment(d.IsMine, curPath, incPath) {
		return false
	}
	m.AttachmentID = incoming
	if b.st != nil {
		if _, err := b.st.UpdateMessageAttachmentID(ctx, proto.ProtocolWhatsApp, contactID, mid, etS, incoming, nil); err != nil {
			b.log.Error("WhatsApp: attachment id update failed", "err", err)
		}
	}
	return true
}

// IngestMessage implements core.MessageIngester (ingest_message).
func (b *Backend) IngestMessage(ctx context.Context, contactID string, msg map[string]any, ts int64) (proto.IngestResult, error) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.ingestLocked(ctx, contactID, parseInMsg(msg), ts, true, false), nil
}

// ingestLocked is ingest_message with ingestMu held: it stores a message into
// SQLite and the in-memory cache, deduplicating against what is cached.
func (b *Backend) ingestLocked(ctx context.Context, contactID string, d inMsg, ts int64, persist, reconcile bool) proto.IngestResult {
	if d.MsgType == "image" {
		d.Text = ""
	}
	text, isMine, msgID := d.Text, d.IsMine, d.ID

	existing := b.messageAlreadyCachedLocked(contactID, ts, isMine, text, msgID, d.AttachmentID, d.MsgType, reconcile)
	if existing != nil {
		if reconcile && msgID != "" && existing.ID != "" && existing.ID != msgID {
			b.log.Info("history reconcile kept original id", "kept", existing.ID, "remote", msgID, "contact", contactID)
			return proto.IngestUnchanged
		}
		// Upgrade an entry that lacks an id (legacy / optimistic send).
		if msgID != "" && existing.ID == "" {
			existing.ID = msgID
			if isMine {
				existing.Timestamp = ts
			}
			b.sortContactCacheLocked(contactID)
			if b.st != nil {
				uts := existing.Timestamp
				if isMine {
					uts = ts
				}
				if _, err := b.st.UpdateMessageID(ctx, proto.ProtocolWhatsApp, contactID, text, isMine, uts, msgID); err != nil {
					b.log.Error("WhatsApp: _update_message_id failed", "err", err)
				}
			}
		}
		changed := false
		q := store.QuoteFill{}
		if existing.QuoteText == "" && d.QuoteText != "" {
			existing.QuoteText, q.QuoteText, changed = d.QuoteText, ptrStr(d.QuoteText), true
		}
		if existing.QuoteTimestamp == nil && d.QuoteTimestamp != nil {
			existing.QuoteTimestamp, q.QuoteTimestamp, changed = d.QuoteTimestamp, d.QuoteTimestamp, true
		}
		if existing.QuoteAuthor == "" && d.QuoteAuthor != "" {
			existing.QuoteAuthor, q.QuoteAuthor, changed = d.QuoteAuthor, ptrStr(d.QuoteAuthor), true
		}
		if existing.ReplyToMessageID == "" && d.ReplyTo != "" {
			existing.ReplyToMessageID, q.ReplyToMessageID, changed = d.ReplyTo, ptrStr(d.ReplyTo), true
		}
		if changed && b.st != nil {
			id := existing.ID
			if id == "" {
				id = msgID
			}
			ets := existing.Timestamp
			if ets == 0 {
				ets = ts
			}
			if _, err := b.st.FillMessageQuoteFields(ctx, proto.ProtocolWhatsApp, contactID, id, ets, q); err != nil {
				b.log.Error("WhatsApp: quote fill failed", "err", err)
			}
		}
		attachmentChanged := b.upgradeOutgoingAttachmentLocked(ctx, contactID, existing, d, ts)

		incomingInfo := d.AttachmentInfo
		incomingIsMedia := d.AttachmentID != "" || d.MsgType == "image" || d.MsgType == "attachment" || d.MsgType == "sticker"
		infoID := existing.ID
		if infoID == "" {
			infoID = msgID
		}
		infoTS := existing.Timestamp
		if infoTS == 0 {
			infoTS = ts
		}
		captionChanged := d.MsgType == "image" && isCaptionLike(incomingInfo) && !isCaptionLike(existing.AttachmentInfo)
		if captionChanged {
			existing.AttachmentInfo = incomingInfo
			b.updateInfo(ctx, contactID, infoID, infoTS, incomingInfo)
		}
		nameChanged := incomingIsMedia && d.MsgType != "image" && incomingInfo != "" &&
			isCaptionLike(existing.AttachmentInfo) && !isCaptionLike(incomingInfo)
		if nameChanged {
			existing.AttachmentInfo = incomingInfo
			b.updateInfo(ctx, contactID, infoID, infoTS, incomingInfo)
		}
		batchChanged := b.mergeBatchSlotLocked(ctx, contactID, existing, d)
		if attachmentChanged || captionChanged || nameChanged || batchChanged {
			return proto.IngestChanged
		}
		return proto.IngestUnchanged
	}

	if reconcile && isMine && msgID != "" && strings.TrimSpace(text) != "" && d.AttachmentID == "" && d.MsgType == "text" {
		n := 0
		for _, m := range b.cache[contactID] {
			if m.IsMine && m.Text == text && m.msgType() == "text" && m.AttachmentID == "" {
				n++
			}
		}
		if n >= 2 {
			b.log.Warn("history reconcile skipped ambiguous outgoing text", "contact", contactID, "remote_id", msgID, "candidates", n)
			return proto.IngestUnchanged
		}
	}

	// bug #44: reuse the unique failed id-less DB row for the echo of a retried send.
	if isMine && msgID != "" && b.reuseFailedDBRowLocked(ctx, contactID, d, text, msgID, ts) {
		return proto.IngestUnchanged
	}

	if persist {
		b.persistMessage(ctx, contactID, d, ts)
	}
	status := d.Status
	if status == "" {
		status = "read"
		if isMine {
			status = "sent"
		}
	}
	b.addCachedLocked(contactID, &entry{
		ID: msgID, Text: text, IsMine: isMine, Sender: d.Sender, Timestamp: ts, QuoteText: d.QuoteText,
		MsgType: d.MsgType, AttachmentInfo: d.AttachmentInfo, AttachmentID: d.AttachmentID,
		ContentType: d.ContentType, MediaKind: d.MediaKind, Read: isMine, Status: status,
		QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: d.QuoteAuthor, ReplyToMessageID: d.ReplyTo,
		QuoteAttachmentID: d.QuoteAttachmentID, QuoteAttachmentPath: d.QuoteAttachmentPath,
		QuoteContentType: d.QuoteContentType, BatchID: d.BatchID, BatchIndex: d.BatchIndex,
		FromHistory: reconcile,
	})
	return proto.IngestAdded
}

// reuseFailedDBRowLocked is _reuse_failed_db_row: re-key the single failed
// id-less row and mirror it into the cache.
func (b *Backend) reuseFailedDBRowLocked(ctx context.Context, contactID string, d inMsg, text, msgID string, ts int64) bool {
	r, ok := b.st.(FailedRowReuser)
	if !ok || b.st == nil {
		return false
	}
	status := d.Status
	if status == "" {
		status = "sent"
	}
	row, err := r.ReuseFailedRow(ctx, proto.ProtocolWhatsApp, contactID, text, ts, msgID, status)
	if err != nil {
		b.log.Error("WhatsApp: _reuse_failed_db_row failed", "err", err)
		return false
	}
	if row == nil {
		return false
	}
	msgType := d.MsgType
	if msgType == "" {
		msgType = "text"
	}
	b.addCachedLocked(contactID, &entry{
		ID: msgID, Text: text, IsMine: true, Sender: d.Sender, Timestamp: row.Timestamp, QuoteText: d.QuoteText,
		MsgType: msgType, AttachmentInfo: d.AttachmentInfo, AttachmentID: d.AttachmentID,
		Read: true, Status: row.Status, QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: d.QuoteAuthor,
		ReplyToMessageID: d.ReplyTo,
	})
	return true
}

func (b *Backend) updateInfo(ctx context.Context, contact, id string, ts int64, info string) {
	if b.st == nil {
		return
	}
	if _, err := b.st.UpdateMessageAttachmentInfo(ctx, proto.ProtocolWhatsApp, contact, id, ts, info); err != nil {
		b.log.Error("WhatsApp: attachment info update failed", "err", err)
	}
}

// ---- edits ----------------------------------------------------------------------

// detectEditLocked is _detect_edit for history ingestion: the cached entry the
// history copy of a message updates, or nil. Matching is by message id ONLY.
// The old text+time heuristic folded distinct history messages that happened
// to be seconds apart into one another (their texts differ, which looked like
// an edit), losing every message but the first and the last of a burst.
func (b *Backend) detectEditLocked(contactID, msgID, text string, isMine bool, override []*entry) *entry {
	if text == "" || msgID == "" {
		return nil
	}
	nt := norm(text)
	entries := override
	if entries == nil {
		entries = b.cache[contactID]
	}
	for _, m := range entries {
		if m.IsMine != isMine || m.msgType() != "text" {
			continue
		}
		if m.ID == msgID {
			if norm(m.Text) != nt {
				return m
			}
			return nil
		}
	}
	return nil
}

// ApplyEdit implements proto.Editor.
func (b *Backend) ApplyEdit(contactID, messageID, newText string, opts proto.ApplyEditOptions) (*proto.EditResult, error) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.applyEditLocked(context.Background(), contactID, messageID, newText, opts.IsMine, opts.MarkEdited), nil
}

func (b *Backend) applyEditLocked(ctx context.Context, contactID, messageID, newText string, isMine *bool, markEdited bool) *proto.EditResult {
	var target *entry
	for _, m := range b.cache[contactID] {
		if m.ID == messageID {
			if isMine != nil && m.IsMine != *isMine {
				continue
			}
			target = m
			break
		}
	}
	if target == nil || target.msgType() != "text" {
		return nil
	}
	old := target.Text
	if norm(old) == norm(newText) {
		return nil // echo of our own edit
	}
	target.Text = newText
	target.Edited = markEdited
	if b.st != nil {
		u := store.TextUpdate{Protocol: proto.ProtocolWhatsApp, ContactNumber: contactID, NewText: newText, ClearEdited: !markEdited}
		if target.ID != "" {
			id := target.ID
			u.MsgID = &id
		} else {
			ts := target.Timestamp
			u.Timestamp, u.OldText = &ts, &old
		}
		if _, err := b.st.UpdateMessageText(ctx, u); err != nil {
			b.log.Error("WhatsApp: update message text failed", "err", err)
		}
	}
	id := target.ID
	if id == "" {
		id = messageID
	}
	return &proto.EditResult{MessageID: id, Timestamp: target.Timestamp, OldText: old, Text: newText, IsMine: target.IsMine}
}

// ---- receipts ---------------------------------------------------------------------

// ProcessReceipt implements core.ReceiptProcessor (process_receipt). It
// matches on the canonical id (WAHA ids vs the bare hex of an ack), falls back
// to the single id-less optimistic "sent" entry, and persists the status.
func (b *Backend) ProcessReceipt(ctx context.Context, contactID string, payload map[string]any) ([]map[string]any, error) {
	var raw []string
	switch t := payload["message_ids"].(type) {
	case []string:
		raw = t
	case []any:
		for _, x := range t {
			if s := getStr(map[string]any{"v": x}, "v"); s != "" {
				raw = append(raw, s)
			}
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	target := "delivered"
	if truthy(payload["is_read"]) {
		target = "read"
	}
	canon := map[string]bool{}
	for _, id := range raw {
		if c := CanonicalMsgID(id); c != "" {
			canon[c] = true
		}
	}

	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	var scan []string
	if contactID != "" {
		scan = []string{contactID}
	} else {
		for c := range b.cache {
			scan = append(scan, c)
		}
		sort.Strings(scan)
	}
	type persist struct {
		id      string
		ts      int64
		text    string
		contact string
	}
	var updated []map[string]any
	var todo []persist
	matched := false
	apply := func(keys []string) {
		for _, c := range keys {
			for _, m := range b.cache[c] {
				if !m.IsMine || m.ID == "" || !canon[CanonicalMsgID(m.ID)] {
					continue
				}
				matched = true
				if statusRanks[target] > statusRank(m.Status, "sent") {
					m.Status = target
					updated = append(updated, map[string]any{
						"id": m.ID, "timestamp": m.Timestamp, "status": target, "text": m.Text, "is_mine": true,
					})
					todo = append(todo, persist{m.ID, m.Timestamp, m.Text, c})
				}
			}
		}
	}
	apply(scan)
	if !matched && contactID != "" {
		// The receipt names the chat the message was sent to, which can be the
		// @lid/@c.us twin of the cache key; ids are unique, so look everywhere.
		var all []string
		for c := range b.cache {
			if c != contactID {
				all = append(all, c)
			}
		}
		sort.Strings(all)
		apply(all)
	}
	if !matched {
		type cand struct {
			c string
			m *entry
		}
		var cands []cand
		for _, c := range scan {
			for _, m := range b.cache[c] {
				if m.IsMine && m.ID == "" && (m.Status == "" || m.Status == "sent") {
					cands = append(cands, cand{c, m})
				}
			}
		}
		if len(cands) == 1 {
			m := cands[0].m
			m.Status = target
			updated = append(updated, map[string]any{
				"id": "", "timestamp": m.Timestamp, "status": target, "text": m.Text, "is_mine": true,
			})
			todo = append(todo, persist{"", m.Timestamp, m.Text, cands[0].c})
		}
	}
	if !matched && len(updated) == 0 {
		b.log.Debug("WhatsApp receipt matched no is_mine entry", "contact_id", contactID, "ids", raw)
	}
	if b.st != nil {
		for _, p := range todo {
			if p.id != "" {
				c := p.contact
				if _, err := b.st.UpdateMessageStatusByID(ctx, p.id, target, proto.ProtocolWhatsApp, &c); err != nil {
					b.log.Error("WhatsApp: _update_message_status_by_id failed", "err", err)
				}
			} else {
				txt := p.text
				if _, err := b.st.UpdateMessageStatus(ctx, p.ts, target, proto.ProtocolWhatsApp, p.contact, &txt, nil); err != nil {
					b.log.Error("WhatsApp: _update_message_status failed", "err", err)
				}
			}
		}
	}
	return updated, nil
}

// MarkRead implements proto.Backend (mark_read_sync): marks the chat read on
// the phone/WhatsApp and in the local store, across the @c.us/@lid union.
func (b *Backend) MarkRead(ctx context.Context, contactID string) error {
	b.markReadRemote(ctx, contactID)
	if b.st == nil {
		return nil
	}
	var extra []string
	if c := b.identify(contactID); c != nil {
		keys := contactStorageKeys(*c)
		if len(keys) > 1 {
			extra = keys[1:]
		}
	}
	if err := b.st.MarkAsRead(ctx, proto.ProtocolWhatsApp, contactID, extra...); err != nil {
		b.log.Debug("WhatsApp mark-read persistence failed", "err", err)
	}
	return nil
}

// cachedEntry returns a copy of the cached message with the id.
func (b *Backend) cachedEntry(contactID, msgID string) (entry, bool) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	for _, m := range b.cache[contactID] {
		if m.ID == msgID || (m.ID != "" && sameMsgID(m.ID, msgID)) {
			return *m, true
		}
	}
	return entry{}, false
}

// chatKeys are the storage keys under which a chat's messages may live: the id
// itself, then its known @c.us / @lid twins.
func (b *Backend) chatKeys(chat string) []string {
	keys := []string{chat}
	if c := b.identify(chat); c != nil {
		for _, k := range append([]string{c.ID}, contactStorageKeys(*c)...) {
			if !containsStr(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// findCachedTwin looks the message with the canonical id up in the chat and
// its twins; key is the storage key it was found under.
func (b *Backend) findCachedTwin(chat, id string) (e entry, key string, ok bool) {
	c := CanonicalMsgID(id)
	if c == "" {
		return entry{}, "", false
	}
	keys := b.chatKeys(chat)
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	for _, k := range keys {
		for _, m := range b.cache[k] {
			if m.ID != "" && CanonicalMsgID(m.ID) == c {
				return *m, k, true
			}
		}
	}
	return entry{}, "", false
}

// findCachedByCanonical returns the cached entry of the chat whose id has the
// same canonical token.
func (b *Backend) findCachedByCanonical(contactID, id string) (entry, bool) {
	c := CanonicalMsgID(id)
	if c == "" {
		return entry{}, false
	}
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	for _, m := range b.cache[contactID] {
		if m.ID != "" && CanonicalMsgID(m.ID) == c {
			return *m, true
		}
	}
	return entry{}, false
}
