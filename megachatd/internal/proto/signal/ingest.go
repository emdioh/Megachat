package signal

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Windows (ms) from signal.py / db.py.
const (
	sendDedupWindowMs     = 5000
	incomingDedupWindowMs = 2000
	echoMatchWindowMs     = store.EchoMatchWindowMs
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
	e := &entry{
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
	return e
}

// toMap renders the entry as the Python cache dict.
func (e *entry) toMap() map[string]any {
	m := map[string]any{
		"id": strOrNil(e.ID), "text": e.Text, "is_mine": e.IsMine, "sender": e.Sender,
		"timestamp": e.Timestamp, "quote_text": strOrNil(e.QuoteText), "msg_type": e.MsgType,
		"attachment_info": strOrNil(e.AttachmentInfo), "attachment_id": strOrNil(e.AttachmentID),
		"content_type": strOrNil(e.ContentType), "read": e.Read, "status": e.Status,
		"quote_timestamp": int64OrNil(e.QuoteTimestamp), "quote_author": strOrNil(e.QuoteAuthor),
		"reply_to_message_id":   strOrNil(e.ReplyToMessageID),
		"quote_attachment_id":   strOrNil(e.QuoteAttachmentID),
		"quote_attachment_path": strOrNil(e.QuoteAttachmentPath),
		"quote_content_type":    strOrNil(e.QuoteContentType),
	}
	if e.Edited {
		m["edited"] = true
	}
	return m
}

// inMsg is the parsed ingest payload (the data dict).
type inMsg struct {
	ID, Text                                        string
	IsMine                                          bool
	Sender, QuoteText, MsgType, AttachmentInfo      string
	AttachmentID, ContentType, MediaKind, Status    string
	QuoteTimestamp                                  *int64
	QuoteAuthor, ReplyTo                            string
	QuoteAttachmentID, QuoteAttachmentPath, QuoteCT string
	BatchID                                         string
	BatchIndex                                      *int64
}

func parseInMsg(m map[string]any) inMsg {
	d := inMsg{
		ID: anyString(m["id"]), Text: getStr(m, "text"), IsMine: truthy(m["is_mine"]),
		Sender: getStr(m, "sender"), QuoteText: getStr(m, "quote_text"), MsgType: getStr(m, "msg_type"),
		AttachmentInfo: getStr(m, "attachment_info"), AttachmentID: getStr(m, "attachment_id"),
		ContentType: getStr(m, "content_type"), MediaKind: getStr(m, "media_kind"), Status: getStr(m, "status"),
		QuoteAuthor: getStr(m, "quote_author"), ReplyTo: getStr(m, "reply_to_message_id"),
		QuoteAttachmentID: getStr(m, "quote_attachment_id"), QuoteAttachmentPath: getStr(m, "quote_attachment_path"),
		QuoteCT: getStr(m, "quote_content_type"), BatchID: getStr(m, "batch_id"),
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

// ---- attachment path helpers -----------------------------------------------------------

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// resolveLoose is Path.resolve(): symlinks resolved when the path exists,
// otherwise the cleaned absolute path.
func resolveLoose(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	// resolve the deepest existing parent
	dir, base := filepath.Split(filepath.Clean(p))
	if dir == "" || dir == p {
		return filepath.Clean(p)
	}
	return filepath.Join(resolveLoose(dir), base)
}

func isRelativeTo(base, p string) bool {
	return p == base || strings.HasPrefix(p, strings.TrimRight(base, string(filepath.Separator))+string(filepath.Separator))
}

// safeJoin resolves name below dir; "" when it escapes dir.
func safeJoin(dir, name string) (string, bool) {
	cand := filepath.Join(dir, name)
	if filepath.IsAbs(name) {
		cand = name
	}
	if !isRelativeTo(resolveLoose(dir), resolveLoose(cand)) {
		return "", false
	}
	return cand, true
}

// attachmentPath resolves a signal-cli attachment id to a readable file.
func (b *Backend) attachmentPath(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	dir := b.cfg.SignalCLIAttachmentsDir()
	if cand := filepath.Join(dir, filepath.Base(id)); isFile(cand) {
		return cand, true
	}
	cand, ok := safeJoin(dir, id)
	if !ok {
		b.log.Warn("rejected attachment path outside the attachments directory")
		return "", false
	}
	if isFile(cand) {
		return cand, true
	}
	return "", false
}

// AttachmentPath implements proto.AttachmentResolver.
func (b *Backend) AttachmentPath(ctx context.Context, attachmentID string) (string, bool) {
	return b.attachmentPath(attachmentID)
}

func (b *Backend) isRegisteredSentAttachment(path string) bool {
	if path == "" {
		return false
	}
	r := resolveLoose(path)
	b.sentMu.Lock()
	defer b.sentMu.Unlock()
	for _, p := range b.sentPaths {
		if resolveLoose(p) == r {
			return true
		}
	}
	return false
}

func (b *Backend) isSentAttachment(id string) bool {
	if id == "" {
		return false
	}
	if strings.HasPrefix(filepath.Base(id), "sent-") {
		return true
	}
	p, _ := b.attachmentPath(id)
	return b.isRegisteredSentAttachment(p)
}

func (b *Backend) outgoingAttachmentsMatch(current, incoming string) bool {
	return current == "" || incoming == "" || current == incoming ||
		b.isSentAttachment(current) || b.isSentAttachment(incoming)
}

func (b *Backend) sameAttachmentSlot(cached, incoming string) bool {
	if cached == incoming || cached == "" || incoming == "" {
		return true
	}
	return b.isSentAttachment(cached) != b.isSentAttachment(incoming)
}

// ---- dedup ---------------------------------------------------------------------------------------

func (b *Backend) messageAlreadyCached(contactID string, ts int64, isMine bool, text, msgID, attachmentID string, batchIndex *int64) *entry {
	var bestIdless *entry
	var bestDelta int64 = -1
	list := b.cache[contactID]
	var positions map[*entry]int64
	if isMine && msgID != "" && batchIndex != nil {
		positions = map[*entry]int64{}
		var pos int64
		for _, m := range list {
			if m.IsMine && m.ID == msgID {
				positions[m] = pos
				pos++
			}
		}
	}
	for _, msg := range list {
		if msg.IsMine != isMine {
			continue
		}
		cachedAtt := msg.AttachmentID
		sameAttachment := b.outgoingAttachmentsMatch(cachedAtt, attachmentID)
		if isMine && msgID != "" && msg.ID != "" && msg.ID == msgID {
			if !b.sameAttachmentSlot(cachedAtt, attachmentID) {
				continue
			}
			if len(positions) > 0 {
				if p, ok := positions[msg]; !ok || p != *batchIndex {
					continue
				}
			}
			return msg
		}
		if msg.Text != text {
			continue
		}
		if !isMine && attachmentID != "" && cachedAtt != attachmentID {
			continue
		}
		switch {
		case !isMine:
			if absInt64(msg.Timestamp-ts) <= incomingDedupWindowMs {
				return msg
			}
		case msgID != "":
			if msg.ID != "" && msg.ID == msgID && b.sameAttachmentSlot(cachedAtt, attachmentID) &&
				(len(positions) == 0 || positions[msg] == *batchIndex) {
				return msg
			}
			if msg.ID == "" && sameAttachment {
				delta := absInt64(msg.Timestamp - ts)
				if delta <= echoMatchWindowMs && (bestDelta < 0 || delta < bestDelta) {
					bestIdless, bestDelta = msg, delta
				}
			}
		default:
			if msg.ID == "" && sameAttachment && absInt64(msg.Timestamp-ts) <= sendDedupWindowMs {
				return msg
			}
		}
	}
	return bestIdless
}

func (b *Backend) upgradeOutgoingAttachment(ctx context.Context, contactID string, m *entry, d inMsg, ts int64) bool {
	current, incoming := m.AttachmentID, d.AttachmentID
	currentPath, _ := b.attachmentPath(current)
	incomingPath, _ := b.attachmentPath(incoming)
	currentLegacySent := current != "" && strings.HasPrefix(filepath.Base(current), "sent-")
	currentIsSent := b.isSentAttachment(current)
	incomingIsSent := b.isSentAttachment(incoming)
	if incoming == "" || incoming == current || !(currentIsSent || incomingIsSent) || !d.IsMine ||
		incomingPath == "" || !isFile(incomingPath) ||
		(currentLegacySent && currentPath != "" && isFile(currentPath)) {
		return false
	}
	b.log.Info("signal ingest: upgrade attachment", "id", firstNonEmpty(m.ID, d.ID), "from", current, "to", incoming)
	m.AttachmentID = incoming
	if b.st != nil {
		var expected *string
		if current != "" {
			expected = &current
		}
		if _, err := b.st.UpdateMessageAttachmentID(ctx, proto.ProtocolSignal, contactID,
			firstNonEmpty(m.ID, d.ID), m.Timestamp, incoming, expected); err != nil {
			b.log.Warn("attachment id update failed", "err", err)
		}
	}
	return true
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func (b *Backend) healImageCaption(ctx context.Context, contactID string, m *entry, d inMsg) bool {
	if d.MsgType != "image" || !isCaptionLike(d.AttachmentInfo) || isCaptionLike(m.AttachmentInfo) {
		return false
	}
	m.AttachmentInfo = d.AttachmentInfo
	if b.st != nil {
		if _, err := b.st.UpdateMessageAttachmentInfo(ctx, proto.ProtocolSignal, contactID,
			firstNonEmpty(m.ID, d.ID), m.Timestamp, d.AttachmentInfo); err != nil {
			b.log.Warn("attachment info update failed", "err", err)
		}
	}
	return true
}

// mergeQuoteFields fills the quote fields missing in m (never overwrites).
func (b *Backend) mergeQuoteFields(ctx context.Context, contactID string, m *entry, d inMsg) bool {
	changed := false
	fill := func(dst *string, v string) {
		if *dst == "" && v != "" {
			*dst = v
			changed = true
		}
	}
	fill(&m.QuoteText, d.QuoteText)
	if m.QuoteTimestamp == nil && d.QuoteTimestamp != nil {
		m.QuoteTimestamp = d.QuoteTimestamp
		changed = true
	}
	fill(&m.QuoteAuthor, d.QuoteAuthor)
	fill(&m.ReplyToMessageID, d.ReplyTo)
	fill(&m.QuoteAttachmentID, d.QuoteAttachmentID)
	fill(&m.QuoteAttachmentPath, d.QuoteAttachmentPath)
	fill(&m.QuoteContentType, d.QuoteCT)
	if changed && b.st != nil {
		_, err := b.st.FillMessageQuoteFields(ctx, proto.ProtocolSignal, contactID, firstNonEmpty(m.ID, d.ID), m.Timestamp,
			store.QuoteFill{
				QuoteText: ptrStr(d.QuoteText), QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: ptrStr(d.QuoteAuthor),
				ReplyToMessageID: ptrStr(d.ReplyTo), QuoteAttachmentID: ptrStr(d.QuoteAttachmentID),
				QuoteAttachmentPath: ptrStr(d.QuoteAttachmentPath), QuoteContentType: ptrStr(d.QuoteCT),
			})
		if err != nil {
			b.log.Warn("quote fill failed", "err", err)
		}
	}
	return changed
}

func (b *Backend) persistMessage(ctx context.Context, contactID string, d inMsg, ts int64) (inserted bool, err error) {
	if b.st == nil {
		return true, nil
	}
	nm := store.NewMessage{
		Protocol: proto.ProtocolSignal, ContactNumber: contactID, Text: d.Text, IsMine: d.IsMine,
		Sender: d.Sender, Timestamp: ts, MsgType: d.MsgType, Status: d.Status,
		QuoteText: ptrStr(d.QuoteText), AttachmentInfo: ptrStr(d.AttachmentInfo), AttachmentID: ptrStr(d.AttachmentID),
		ContentType: ptrStr(d.ContentType), MediaKind: ptrStr(d.MediaKind), MsgID: ptrStr(d.ID),
		QuoteTimestamp: d.QuoteTimestamp, QuoteAuthor: ptrStr(d.QuoteAuthor), ReplyToMessageID: ptrStr(d.ReplyTo),
		QuoteAttachmentID: ptrStr(d.QuoteAttachmentID), QuoteAttachmentPath: ptrStr(d.QuoteAttachmentPath),
		QuoteContentType: ptrStr(d.QuoteCT), BatchID: ptrStr(d.BatchID), BatchIndex: d.BatchIndex,
	}
	_, inserted, err = b.st.AddMessage(ctx, nm)
	return inserted, err
}

// IngestMessage stores an incoming/outgoing message (ingest_message): it is
// idempotent per message identity, upgrades an optimistic outgoing twin with
// the real id, heals attachment ids / image captions / quote fields on
// duplicates. It implements core.MessageIngester (the result values match
// core.IngestResult: 0 unchanged, 1 added, 2 changed).
func (b *Backend) IngestMessage(ctx context.Context, contactID string, msg map[string]any, ts int64) (proto.IngestResult, error) {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.ingestLocked(ctx, contactID, parseInMsg(msg), ts, true)
}

// ingestLocked is ingest_message with ingestMu held.
func (b *Backend) ingestLocked(ctx context.Context, contactID string, d inMsg, ts int64, persist bool) (proto.IngestResult, error) {
	if d.MsgType == "image" {
		d.Text = ""
	}
	text, isMine := d.Text, d.IsMine
	if isMine && d.AttachmentID != "" {
		if p, ok := b.attachmentPath(d.AttachmentID); !ok || !isFile(p) {
			d.AttachmentID = ""
		}
	}

	// Upgrade branch: an outgoing echo carrying the real server id attaches
	// it to the optimistic twin without touching its optimistic timestamp.
	if d.ID != "" && isMine {
		incomingAtt := d.AttachmentID
		incomingIsSent := b.isSentAttachment(incomingAtt)
		var best *entry
		var bestDelta int64 = -1
	scan:
		for _, m := range b.cache[contactID] {
			if !m.IsMine {
				continue
			}
			cachedAtt := m.AttachmentID
			sameAttachment := b.outgoingAttachmentsMatch(cachedAtt, incomingAtt)
			cachedIsSent := b.isSentAttachment(cachedAtt)
			sameSlot := b.sameAttachmentSlot(cachedAtt, incomingAtt)
			validDirection := cachedAtt == "" || (cachedIsSent && !incomingIsSent)
			idMatches := m.ID == strconv.FormatInt(ts, 10) && sameSlot && validDirection
			delta := absInt64(m.Timestamp - ts)
			optimistic := m.ID == "" && m.Text == text && delta <= sendDedupWindowMs && sameAttachment
			if idMatches {
				best = m
				break scan
			}
			if optimistic && (bestDelta < 0 || delta < bestDelta) {
				best, bestDelta = m, delta
			}
		}
		if best != nil {
			m := best
			m.ID = d.ID
			if b.st != nil {
				if _, err := b.st.UpdateMessageID(ctx, proto.ProtocolSignal, contactID, text, true, m.Timestamp, d.ID); err != nil {
					b.log.Error("signal: update message id failed", "err", err)
				}
			}
			alreadyPresent := false
			for _, o := range b.cache[contactID] {
				if o.ID == d.ID && o.AttachmentID == incomingAtt && o != m {
					alreadyPresent = true
					break
				}
			}
			cachedIsSent := b.isSentAttachment(m.AttachmentID)
			validDirection := m.AttachmentID == "" || (cachedIsSent && !incomingIsSent)
			changed := false
			if !alreadyPresent && validDirection {
				changed = b.upgradeOutgoingAttachment(ctx, contactID, m, d, ts)
			}
			changed = b.healImageCaption(ctx, contactID, m, d) || changed
			quoteChanged := b.mergeQuoteFields(ctx, contactID, m, d)
			if changed || quoteChanged {
				return proto.IngestChanged, nil
			}
			return proto.IngestUnchanged, nil
		}
	}

	existing := b.messageAlreadyCached(contactID, ts, isMine, text, d.ID, d.AttachmentID, d.BatchIndex)
	if existing != nil {
		changed := false
		if isMine {
			incomingAtt := d.AttachmentID
			alreadyPresent := false
			for _, o := range b.cache[contactID] {
				if o.ID == d.ID && o.AttachmentID == incomingAtt && o != existing {
					alreadyPresent = true
					break
				}
			}
			cachedIsSent := b.isSentAttachment(existing.AttachmentID)
			incomingIsSent := b.isSentAttachment(incomingAtt)
			validDirection := existing.AttachmentID == "" || (cachedIsSent && !incomingIsSent)
			if !alreadyPresent && validDirection {
				changed = b.upgradeOutgoingAttachment(ctx, contactID, existing, d, ts)
			}
			changed = b.healImageCaption(ctx, contactID, existing, d) || changed
		}
		quoteChanged := b.mergeQuoteFields(ctx, contactID, existing, d)
		if changed || quoteChanged {
			return proto.IngestChanged, nil
		}
		return proto.IngestUnchanged, nil
	}

	added := true
	if persist {
		inserted, err := b.persistMessage(ctx, contactID, d, ts)
		if err != nil {
			return proto.IngestUnchanged, err
		}
		added = inserted
	}
	status := d.Status
	if status == "" {
		if isMine {
			status = "sent"
		} else {
			status = "read"
		}
	}
	b.cache[contactID] = append(b.cache[contactID], &entry{
		ID: d.ID, Text: text, IsMine: isMine, Sender: d.Sender, Timestamp: ts, QuoteText: d.QuoteText,
		MsgType: d.MsgType, AttachmentInfo: d.AttachmentInfo, AttachmentID: d.AttachmentID,
		ContentType: d.ContentType, Read: isMine, Status: status, QuoteTimestamp: d.QuoteTimestamp,
		QuoteAuthor: d.QuoteAuthor, ReplyToMessageID: d.ReplyTo, QuoteAttachmentID: d.QuoteAttachmentID,
		QuoteAttachmentPath: d.QuoteAttachmentPath, QuoteContentType: d.QuoteCT,
	})
	if added {
		return proto.IngestAdded, nil
	}
	return proto.IngestUnchanged, nil
}

// ---- receipts ---------------------------------------------------------------------------------------

var statusRank = map[string]int{"pending": 0, "failed": 0, "sent": 1, "delivered": 2, "read": 3}

// ProcessReceipt applies a receipt event to the cache and the store and
// returns the updated rows (process_receipt). payload is the event payload
// ({"receipt": {isDelivery, isRead, timestamps}}). Implements
// core.ReceiptProcessor.
func (b *Backend) ProcessReceipt(ctx context.Context, contactID string, payload map[string]any) ([]map[string]any, error) {
	receipt := getMap(payload, "receipt")
	stamps := getList(receipt, "timestamps")
	if len(stamps) == 0 || contactID == "" {
		return nil, nil
	}
	var newStatus string
	switch {
	case truthy(receipt["isRead"]):
		newStatus = "read"
	case truthy(receipt["isDelivery"]):
		newStatus = "delivered"
	default:
		return nil, nil
	}
	var ts []int64
	for _, s := range stamps {
		if n, ok := toInt64(s); ok {
			ts = append(ts, n)
		}
	}
	b.ingestMu.Lock()
	var updated []*entry
	for _, m := range b.cache[contactID] {
		if !m.IsMine {
			continue
		}
		match := false
		for _, t := range ts {
			if absInt64(m.Timestamp-t) <= 1000 {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		old := m.Status
		if old == "" {
			old = "sent"
		}
		if statusRank[newStatus] > statusRank[old] {
			m.Status = newStatus
			updated = append(updated, m)
		}
	}
	out := make([]map[string]any, 0, len(updated))
	type upd struct {
		ts     int64
		status string
	}
	var persist []upd
	for _, m := range updated {
		out = append(out, m.toMap())
		persist = append(persist, upd{m.Timestamp, m.Status})
	}
	b.ingestMu.Unlock()
	if b.st != nil {
		for _, u := range persist {
			if _, err := b.st.UpdateMessageStatus(ctx, u.ts, u.status, proto.ProtocolSignal, contactID, nil, nil); err != nil {
				b.log.Debug("receipt status update failed", "err", err)
			}
		}
	}
	return out, nil
}

// ---- edits ----------------------------------------------------------------------------------------------

// ApplyEdit applies a received / echoed edit to the cache and the store
// (apply_edit). nil when nothing changed.
func (b *Backend) ApplyEdit(contactID, messageID, newText string, opts proto.ApplyEditOptions) (*proto.EditResult, error) {
	targetTS, hasTS := toInt64(messageID)
	b.ingestMu.Lock()
	var hit *entry
	var oldText string
	for _, msg := range b.cache[contactID] {
		if msg.ID == "" {
			if !hasTS || msg.Timestamp != targetTS {
				continue
			}
		} else if msg.ID != messageID {
			continue
		}
		if opts.IsMine != nil && msg.IsMine != *opts.IsMine {
			continue
		}
		mt := msg.MsgType
		if mt == "" {
			mt = "text"
		}
		if mt != "text" {
			b.ingestMu.Unlock()
			return nil, nil
		}
		oldText = msg.Text
		if oldText == newText {
			b.ingestMu.Unlock()
			return nil, nil
		}
		msg.Text = newText
		msg.Edited = opts.MarkEdited
		hit = msg
		break
	}
	if hit == nil {
		b.ingestMu.Unlock()
		return nil, nil
	}
	res := &proto.EditResult{MessageID: messageID, Timestamp: hit.Timestamp, OldText: oldText, Text: newText, IsMine: hit.IsMine}
	ts, mine := hit.Timestamp, hit.IsMine
	b.ingestMu.Unlock()
	if b.st != nil {
		_, err := b.st.UpdateMessageText(context.Background(), store.TextUpdate{
			Protocol: proto.ProtocolSignal, ContactNumber: contactID, NewText: newText,
			Timestamp: &ts, OldText: &oldText, IsMine: &mine, ClearEdited: !opts.MarkEdited,
		})
		if err != nil {
			b.log.Warn("edit persist failed", "err", err)
		}
	}
	return res, nil
}

// ---- reactions ---------------------------------------------------------------------------------------------

// ApplyReaction applies a reaction delta and returns the target's aggregate
// {"message_id","timestamp","reactions":[...]}, or nil for unknown targets /
// no-ops (apply_reaction).
func (b *Backend) ApplyReaction(contactID string, payload map[string]any) (map[string]any, error) {
	ctx := context.Background()
	if getStr(payload, "mode") != "delta" {
		return nil, nil
	}
	var targetMsgID *string
	if payload["target_message_id"] != nil {
		s := anyString(payload["target_message_id"])
		targetMsgID = &s
	}
	var targetTS *int64
	if payload["target_timestamp"] != nil {
		if n, ok := toInt64(payload["target_timestamp"]); ok {
			targetTS = &n
		}
	}
	if targetMsgID == nil && targetTS == nil {
		return nil, nil
	}

	type row struct {
		msgID *string
		ts    int64
	}
	var target *row
	b.ingestMu.Lock()
	for _, msg := range b.cache[contactID] {
		if msg.ID != "" {
			if targetMsgID == nil || msg.ID != *targetMsgID {
				continue
			}
		} else if targetTS == nil || msg.Timestamp != *targetTS {
			continue
		}
		target = &row{ptrStr(msg.ID), msg.Timestamp}
		break
	}
	b.ingestMu.Unlock()
	if target == nil && b.st != nil {
		r, err := b.st.ResolveReactionTargetRow(ctx, proto.ProtocolSignal, contactID, targetMsgID, targetTS)
		if err != nil {
			return nil, err
		}
		if r != nil {
			target = &row{r.MsgID, r.Timestamp}
		}
	}
	emoji, ok := payload["emoji"].(string)
	isRemove := truthy(payload["is_remove"])
	if !ok || (emoji == "" && !isRemove) {
		return nil, nil
	}
	eventTS := int64(0)
	if truthy(payload["timestamp"]) {
		n, ok := toInt64(payload["timestamp"])
		if !ok {
			return nil, nil
		}
		eventTS = n
	}
	if b.st == nil {
		return nil, nil
	}
	changed, err := b.st.ApplyReactionDelta(ctx, store.ReactionDelta{
		Protocol: proto.ProtocolSignal, Contact: contactID, TargetMsgID: targetMsgID, TargetTimestamp: targetTS,
		Emoji: emoji, AuthorKey: anyString(payload["author_key"]), Author: anyString(payload["author"]),
		IsMine: truthy(payload["is_mine"]), IsRemove: isRemove, Timestamp: eventTS,
	})
	if err != nil {
		return nil, err
	}
	if !changed || target == nil {
		return nil, nil
	}

	rowTS := target.ts
	rowMsgID := ""
	if target.msgID != nil {
		rowMsgID = *target.msgID
	}
	reactions, err := b.st.ReactionsForContact(ctx, proto.ProtocolSignal, contactID)
	if err != nil {
		return nil, err
	}
	type agg struct {
		emoji   string
		count   int64
		isMine  bool
		authors map[string]bool
		first   int64
	}
	var order []*agg
	byEmoji := map[string]*agg{}
	rowTSStr := strconv.FormatInt(rowTS, 10)
	for _, r := range reactions {
		matchesID := false
		if r.TargetMsgID != nil {
			matchesID = *r.TargetMsgID == rowMsgID || *r.TargetMsgID == rowTSStr
		}
		matchesTS := r.TargetTimestamp != nil && *r.TargetTimestamp == rowTS
		if !matchesID && !matchesTS {
			continue
		}
		a := byEmoji[r.Emoji]
		if a == nil {
			a = &agg{emoji: r.Emoji, authors: map[string]bool{}, first: r.Timestamp}
			byEmoji[r.Emoji] = a
			order = append(order, a)
		}
		a.count += r.Count
		a.isMine = a.isMine || r.IsMine
		if r.Author != "" {
			a.authors[r.Author] = true
		}
		if r.Timestamp < a.first {
			a.first = r.Timestamp
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].count != order[j].count {
			return order[i].count > order[j].count
		}
		return order[i].first < order[j].first
	})
	list := make([]map[string]any, 0, len(order))
	for _, a := range order {
		authors := make([]string, 0, len(a.authors))
		for k := range a.authors {
			authors = append(authors, k)
		}
		sort.Strings(authors)
		list = append(list, map[string]any{"emoji": a.emoji, "count": a.count, "is_mine": a.isMine, "authors": authors})
	}
	messageID := firstNonEmpty(ptrDeref(targetMsgID), rowMsgID, rowTSStr)
	return map[string]any{"message_id": messageID, "timestamp": rowTS, "reactions": list}, nil
}

func ptrDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
