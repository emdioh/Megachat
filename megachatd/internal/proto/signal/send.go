package signal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func storeQuery(contactID string) store.MessageQuery {
	return store.MessageQuery{Protocol: proto.ProtocolSignal, Keys: []string{contactID}}
}

const maxSentAttachmentPaths = 1024

func (b *Backend) nowMs() int64 { return b.clock().UnixMilli() }

func i64p(v int64) *int64 { return &v }

// quoteParams maps a proto.Quote to the optional send arguments.
func quoteParams(q proto.Quote) (qts *int64, qa, qm *string, qatt []string) {
	if q.Timestamp != 0 {
		qts = i64p(q.Timestamp)
	}
	// A quote always carries author and message (possibly ""), like the
	// Python caller; without a quote timestamp only non-empty fields go out.
	if q.Author != "" || q.Timestamp != 0 {
		a := q.Author
		qa = &a
	}
	if q.Message != "" || q.Timestamp != 0 {
		m := q.Message
		qm = &m
	}
	if len(q.Attachments) > 0 {
		qatt = q.Attachments
	}
	return
}

// sendCore performs the send through the daemon (or the subprocess fallback)
// and returns the real server timestamp when known, else the optimistic one.
func (b *Backend) sendCore(ctx context.Context, contactID, text string, q proto.Quote, attachments []string) (string, error) {
	ts := b.nowMs()
	qts, qa, qm, qatt := quoteParams(q)
	p := SendParams{Message: text, Recipient: contactID, QuoteTimestamp: qts, QuoteAuthor: qa,
		QuoteMessage: qm, QuoteAttachments: qatt, Attachments: attachments}
	if b.useDaemon.Load() {
		p.Timestamp = &ts
		r := b.rpc.Send(ctx, p)
		if !r.OK() {
			return "", fmt.Errorf("%s", r.Err)
		}
		if res, ok := r.Result.(map[string]any); ok {
			if real := res["timestamp"]; real != nil {
				if n, ok := toInt64(real); ok {
					return strconv.FormatInt(n, 10), nil
				}
			}
		}
		return strconv.FormatInt(ts, 10), nil
	}
	out, err := b.sendSubprocess(ctx, p)
	if err != nil {
		return "", err
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
		return strconv.FormatInt(n, 10), nil
	}
	return strconv.FormatInt(ts, 10), nil
}

// SendMessage sends text and returns the server timestamp (the message id).
func (b *Backend) SendMessage(ctx context.Context, contactID, text string, quote proto.Quote) (string, error) {
	return b.sendCore(ctx, contactID, text, quote, nil)
}

// EditMessage edits an own message; messageID is the original timestamp (ms).
func (b *Backend) EditMessage(ctx context.Context, contactID, messageID, newText string) (bool, error) {
	target, err := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if err != nil {
		return false, nil
	}
	p := SendParams{Message: newText, Recipient: contactID, EditTimestamp: &target}
	if b.useDaemon.Load() {
		r := b.rpc.Send(ctx, p)
		if !r.OK() {
			return false, fmt.Errorf("%s", r.Err)
		}
		return true, nil
	}
	if _, err := b.sendSubprocess(ctx, p); err != nil {
		return false, err
	}
	return true, nil
}

// SendReaction sends a reaction; failures are reported as false, not errors.
func (b *Backend) SendReaction(ctx context.Context, contactID, messageID, emoji, targetAuthor string) (bool, error) {
	targetTS, err := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if err != nil {
		return false, nil
	}
	author := targetAuthor
	if author == "" {
		b.ingestMu.Lock()
		var target *entry
		for _, m := range b.cache[contactID] {
			key := m.ID
			if key == "" {
				key = strconv.FormatInt(m.Timestamp, 10)
			}
			if key == messageID || m.Timestamp == targetTS {
				target = m
				break
			}
		}
		b.ingestMu.Unlock()
		author = contactID
		if target != nil && target.IsMine {
			author = b.UserNumber()
		}
	}
	if !b.useDaemon.Load() {
		return false, nil
	}
	return b.rpc.SendReaction(ctx, contactID, emoji, author, targetTS), nil
}

// ---- attachments -------------------------------------------------------------------------

// copySentAttachment copies src into the signal-cli attachments directory
// (so signal-cli can read it and the mirror row can resolve it) under a
// collision-free name. Call with sentMu held.
func (b *Backend) copySentAttachment(src, filename string) (string, error) {
	dir := b.cfg.SignalCLIAttachmentsDir()
	if filename == "" {
		filename = "sent-" + randHex(16) + strings.ToLower(pySuffix(src))
	}
	dest := filepath.Join(dir, filename)
	if _, err := os.Lstat(dest); err == nil {
		suffix := pySuffix(filename)
		stem := strings.TrimSuffix(filename, suffix)
		for i := 1; ; i++ {
			marker := fmt.Sprintf(" (%d)", i)
			maxStem := 255 - utf8.RuneCountInString(suffix) - utf8.RuneCountInString(marker)
			cand := strings.TrimRight(truncRunes(stem, maxStem), " .") + marker + suffix
			dest = filepath.Join(dir, cand)
			if _, err := os.Lstat(dest); err != nil {
				break
			}
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return "", err
	}
	_ = os.Chmod(dest, 0o644)
	_ = os.Chtimes(dest, st.ModTime(), st.ModTime())
	return dest, nil
}

// registerSentLocked records src -> persistent copy; sentMu must be held.
func (b *Backend) registerSentLocked(src, persistent string) {
	key := resolveLoose(src)
	if _, ok := b.sentPaths[key]; !ok {
		b.sentOrder = append(b.sentOrder, key)
	}
	b.sentPaths[key] = persistent
	for len(b.sentPaths) > maxSentAttachmentPaths && len(b.sentOrder) > 0 {
		oldest := b.sentOrder[0]
		b.sentOrder = b.sentOrder[1:]
		delete(b.sentPaths, oldest)
	}
}

func (b *Backend) unregisterSentCopies(copies []string) {
	b.sentMu.Lock()
	defer b.sentMu.Unlock()
	set := map[string]bool{}
	for _, c := range copies {
		set[c] = true
	}
	var keep []string
	for _, k := range b.sentOrder {
		if set[b.sentPaths[k]] {
			delete(b.sentPaths, k)
			continue
		}
		keep = append(keep, k)
	}
	b.sentOrder = keep
}

// SendAttachment sends one file (send_attachment_sync).
func (b *Backend) SendAttachment(ctx context.Context, contactID string, att proto.Attachment, quote proto.Quote) (string, error) {
	if err := os.MkdirAll(b.cfg.SignalCLIAttachmentsDir(), 0o755); err != nil {
		return "", err
	}
	safe := media.SanitizeFilename(att.Filename)
	b.sentMu.Lock()
	persistent, err := b.copySentAttachment(att.Path, safe)
	b.sentMu.Unlock()
	if err != nil {
		return "", err
	}
	id, err := b.sendCore(ctx, contactID, att.Caption, quote, []string{persistent})
	if err != nil {
		os.Remove(persistent)
		return "", err
	}
	b.sentMu.Lock()
	b.registerSentLocked(att.Path, persistent)
	b.sentMu.Unlock()
	return id, nil
}

// SendAttachments sends N attachments as ONE Signal message (signal-cli batch
// send) and materialises the N mirror rows inside an atomic barrier under the
// ingest lock; see signal.py send_attachments_sync for the race analysis.
func (b *Backend) SendAttachments(ctx context.Context, contactID string, atts []proto.Attachment, batchID string, quote proto.Quote) ([]string, error) {
	if len(atts) == 0 {
		return []string{}, nil
	}
	if err := os.MkdirAll(b.cfg.SignalCLIAttachmentsDir(), 0o755); err != nil {
		return nil, err
	}
	caption := atts[0].Caption
	var persistent []string
	cleanup := func() {
		for _, p := range persistent {
			os.Remove(p)
		}
	}
	b.sentMu.Lock()
	for _, a := range atts {
		p, err := b.copySentAttachment(a.Path, media.SanitizeFilename(a.Filename))
		if err != nil {
			b.sentMu.Unlock()
			cleanup()
			return nil, err
		}
		persistent = append(persistent, p)
	}
	b.sentMu.Unlock()
	messageID, err := b.sendCore(ctx, contactID, caption, quote, persistent)
	if err != nil {
		cleanup()
		return nil, err
	}
	ts, perr := strconv.ParseInt(messageID, 10, 64)
	if perr != nil || !isDigits(messageID) {
		ts = b.nowMs()
	}
	qct, qfn := proto.ParseQuoteAttachmentDescriptor(firstString(quote.Attachments))

	var inserted []string
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	fail := func(cause error) ([]string, error) {
		b.log.Error("barrier ingest failed", "at", len(inserted), "of", len(atts), "err", cause)
		kept := b.cache[contactID][:0]
		for _, m := range b.cache[contactID] {
			drop := false
			if m.ID == messageID {
				for _, id := range inserted {
					if m.AttachmentID == id {
						drop = true
					}
				}
			}
			if !drop {
				kept = append(kept, m)
			}
		}
		b.cache[contactID] = kept
		if len(inserted) > 0 {
			if w, ok := b.st.(BatchSlotWriter); ok {
				if err := w.DeleteMirrorRows(context.Background(), proto.ProtocolSignal, contactID, messageID, inserted); err != nil {
					b.log.Error("rollback failed (partial mirror rows left)", "err", err)
				}
			} else if b.st != nil {
				b.log.Error("rollback unavailable: store lacks BatchSlotWriter (partial mirror rows left)")
			}
		}
		b.unregisterSentCopies(persistent)
		cleanup()
		return nil, cause
	}

	b.sentMu.Lock()
	for i, a := range atts {
		b.registerSentLocked(a.Path, persistent[i])
	}
	b.sentMu.Unlock()

	for i, a := range atts {
		kind := a.MediaKind
		if kind == "" {
			kind = mediaKindFromMime(a.MimeType, false)
		}
		if kind == "" {
			kind = "document"
		}
		d := inMsg{
			ID: messageID, IsMine: true, Sender: "You", MsgType: msgTypeForMediaKind(kind),
			AttachmentID: filepath.Base(persistent[i]), ContentType: a.MimeType, MediaKind: kind,
			BatchID: batchID, BatchIndex: i64p(int64(i)), QuoteTimestamp: nonZero(quote.Timestamp),
			QuoteAuthor: quote.Author, ReplyTo: quote.ReplyToMessageID,
		}
		if i == 0 {
			d.Text = caption
		}
		if strings.TrimSpace(quote.Message) != "" {
			d.QuoteText = quote.Message
		} else {
			d.QuoteText = mediaQuoteDisplay(qct, qfn)
		}
		if i == 0 && kind == "image" {
			d.AttachmentInfo = firstNonEmpty(caption, a.Filename)
		} else {
			d.AttachmentInfo = firstNonEmpty(a.Filename, caption)
		}
		inserted = append(inserted, filepath.Base(persistent[i]))
		if _, err := b.ingestLocked(ctx, contactID, d, ts, true); err != nil {
			return fail(err)
		}
	}
	b.repairBatchSlots(ctx, contactID, messageID, batchID, len(atts))
	return []string{messageID}, nil
}

func nonZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func firstString(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

type slotRow struct {
	key   any // db row id (int64) or *entry
	index *int64
}

// missingBatchSlotAssignments gives each slotless row, in order, the missing
// slot of range(fileCount); indexes already present are never re-assigned.
func missingBatchSlotAssignments(rows []slotRow, fileCount int) []struct {
	key   any
	index int64
} {
	taken := map[int64]bool{}
	for _, r := range rows {
		if r.index != nil {
			taken[*r.index] = true
		}
	}
	var missing []int64
	for i := 0; i < fileCount; i++ {
		if !taken[int64(i)] {
			missing = append(missing, int64(i))
		}
	}
	var out []struct {
		key   any
		index int64
	}
	for _, r := range rows {
		if r.index != nil {
			continue
		}
		if len(out) >= len(missing) {
			break
		}
		out = append(out, struct {
			key   any
			index int64
		}{r.key, missing[len(out)]})
	}
	return out
}

// repairBatchSlots stamps the batch slot on rows that lost it to an echo race.
// Best effort: failures are logged. ingestMu must be held.
func (b *Backend) repairBatchSlots(ctx context.Context, contactID, messageID, batchID string, fileCount int) {
	if b.st != nil {
		// DB rows share msg_id; order (timestamp, id) is what ListMessages gives.
		rows, err := b.st.ListMessages(ctx, storeQuery(contactID))
		if err != nil {
			b.log.Error("batch slot repair failed", "contact", contactID, "err", err)
		} else {
			var sel []slotRow
			for _, r := range rows {
				if r.MsgID != nil && *r.MsgID == messageID {
					sel = append(sel, slotRow{r.RowID, r.BatchIndex})
				}
			}
			assigns := missingBatchSlotAssignments(sel, fileCount)
			if w, ok := b.st.(BatchSlotWriter); ok {
				for _, a := range assigns {
					if err := w.SetBatchSlot(ctx, a.key.(int64), ptrStr(batchID), a.index); err != nil {
						b.log.Error("batch slot repair failed", "err", err)
					}
				}
			} else if len(assigns) > 0 {
				b.log.Warn("batch slot repair unavailable: store lacks BatchSlotWriter", "rows", len(assigns))
			}
			if len(sel) > fileCount {
				b.log.Warn("batch slot repair found extra rows", "rows", len(sel), "files", fileCount)
			}
		}
	}
	var cached []slotRow
	for _, m := range b.cache[contactID] {
		if m.ID == messageID && m.IsMine {
			cached = append(cached, slotRow{m, m.BatchIndex})
		}
	}
	for _, a := range missingBatchSlotAssignments(cached, fileCount) {
		m := a.key.(*entry)
		m.BatchID = batchID
		idx := a.index
		m.BatchIndex = &idx
	}
}

// ---- sent mirror ---------------------------------------------------------------------------

// EnqueueSentMessage publishes a successful facade send through the receive
// queue as a "message" event (enqueue_sent_message). A failing attachment
// copy is returned so the manager can retry without the attachment.
func (b *Backend) EnqueueSentMessage(ctx context.Context, m proto.SentMessage) error {
	ts, err := strconv.ParseInt(m.MessageID, 10, 64)
	if err != nil {
		ts = b.nowMs()
	}
	var attachmentID, attachmentInfo string
	msgType := "text"
	mediaKind, mime := m.MediaKind, m.MimeType
	if m.AttachmentPath != "" {
		if mediaKind == "" {
			mediaKind = mediaKindFromMime(mime, false)
		}
		if mediaKind == "" {
			mediaKind = "document"
		}
		msgType = msgTypeForMediaKind(mediaKind)
		safe := media.SanitizeFilename(m.Filename)
		if msgType == "image" {
			attachmentInfo = firstNonEmpty(m.Text, safe)
		} else {
			attachmentInfo = firstNonEmpty(safe, m.Text)
		}
		b.sentMu.Lock()
		key := resolveLoose(m.AttachmentPath)
		persistent, ok := b.sentPaths[key]
		if !ok {
			if err := os.MkdirAll(b.cfg.SignalCLIAttachmentsDir(), 0o755); err != nil {
				b.sentMu.Unlock()
				return err
			}
			p, err := b.copySentAttachment(m.AttachmentPath, safe)
			if err != nil {
				b.sentMu.Unlock()
				return err
			}
			persistent = p
			b.registerSentLocked(m.AttachmentPath, p)
		}
		b.sentMu.Unlock()
		attachmentID = filepath.Base(persistent)
	}
	eventText := ""
	if msgType != "image" {
		eventText = firstNonEmpty(m.Text, attachmentInfo)
	}
	quoteText := m.Quote.Message
	if strings.TrimSpace(quoteText) == "" {
		quoteText = mediaQuoteDisplay(m.QuoteContentType, m.QuoteFilename)
	}
	var batchID, batchIndex any
	if m.BatchID != "" {
		batchID = m.BatchID
	}
	if m.BatchIndex != nil {
		batchIndex = int64(*m.BatchIndex)
	}
	var mk any
	if mediaKind != "" {
		mk = mediaKind
	}
	b.queue.put(proto.Event{
		Type: proto.EventMessage, Protocol: proto.ProtocolSignal, ContactID: m.ContactID,
		Payload: map[string]any{
			"id": m.MessageID, "text": eventText, "is_mine": true, "sender": "You", "timestamp": ts,
			"quote_text":          strOrNil(quoteText),
			"quote_timestamp":     int64OrNil(nonZero(m.Quote.Timestamp)),
			"quote_author":        strOrNil(m.Quote.Author),
			"reply_to_message_id": strOrNil(m.Quote.ReplyToMessageID),
			"msg_type":            msgType,
			"attachment_info":     strOrNil(attachmentInfo),
			"attachment_id":       strOrNil(attachmentID),
			"content_type":        strOrNil(mime),
			"media_kind":          mk,
			"batch_id":            batchID,
			"batch_index":         batchIndex,
		},
	})
	return nil
}

// EnqueueSentNotification publishes the lightweight "sent-mirror" event after
// a batch send already mirrored its own rows.
func (b *Backend) EnqueueSentNotification(ctx context.Context, contactID, messageID string, timestamp int64, batchID string) error {
	var contact any
	b.cmu.RLock()
	if c, ok := b.byKey[proto.ContactCacheKey(proto.ProtocolSignal, contactID)]; ok {
		contact = cloneContact(*c)
	}
	b.cmu.RUnlock()
	var bid any
	if batchID != "" {
		bid = batchID
	}
	b.queue.put(proto.Event{
		Type: proto.EventSentMirror, Protocol: proto.ProtocolSignal, ContactID: contactID,
		Payload: map[string]any{"id": messageID, "timestamp": timestamp, "batch_id": bid, "contact": contact},
	})
	return nil
}

// ---- profile photo ---------------------------------------------------------------------------

var avatarIDRE = regexp.MustCompile(`^[0-9A-Za-z.+\-]+$`)

// resolveAvatarPath is rpc.resolve_avatar_path.
func (b *Backend) resolveAvatarPath(identifier, prefix string) (string, bool) {
	if identifier == "" || !avatarIDRE.MatchString(identifier) {
		return "", false
	}
	dir := b.cfg.SignalCLIAvatarsDir()
	cand := filepath.Join(dir, prefix+"-"+identifier)
	if !isRelativeTo(resolveLoose(dir), resolveLoose(cand)) {
		b.log.Warn("rejected avatar path outside the avatars directory")
		return "", false
	}
	if isFile(cand) {
		return cand, true
	}
	return "", false
}

// ProfilePhoto implements media.ProfilePhotoProvider: the contact's local
// avatar from signal-cli's avatars directory (profile photo preferred over the
// contact thumbnail). Empty data = no picture.
func (b *Backend) ProfilePhoto(ctx context.Context, contactID string) ([]byte, string, error) {
	if contactID == "" {
		return nil, "", nil
	}
	ids := []string{contactID}
	if strings.HasPrefix(contactID, "+") {
		if digits := contactID[1:]; digits != "" {
			ids = append(ids, digits)
		}
	} else if c, ok := b.FindContact(contactID); ok {
		if phone := strings.TrimSpace(anyString(c.Extras["phone"])); phone != "" {
			ids = append(ids, "+"+strings.TrimLeft(phone, "+"))
		}
	}
	for _, id := range ids {
		for _, prefix := range []string{"profile", "contact"} {
			p, ok := b.resolveAvatarPath(id, prefix)
			if !ok {
				continue
			}
			st, err := os.Stat(p)
			if err != nil || st.Size() > maxAvatarBytes {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			return data, sniffImageContentType(data), nil
		}
	}
	return nil, "", nil
}
