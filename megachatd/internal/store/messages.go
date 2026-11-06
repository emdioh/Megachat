package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Message is one row of the messages table.  NULL-able columns are pointers
// so NULL and "" stay distinguishable (the dedup rules depend on it).
type Message struct {
	RowID               int64 // messages.id (autoincrement)
	Protocol            string
	ContactNumber       string
	Text                *string
	IsMine              bool
	Sender              *string
	Timestamp           int64
	QuoteText           *string
	MsgType             *string
	AttachmentInfo      *string
	AttachmentID        *string
	ContentType         *string
	MediaKind           *string
	QuoteAttachmentID   *string
	QuoteAttachmentPath *string
	QuoteContentType    *string
	Read                bool
	Status              *string
	MsgID               *string // stable per-message id ("id" in the Python cache dicts)
	QuoteTimestamp      *int64
	QuoteAuthor         *string
	ReplyToMessageID    *string
	Edited              bool
	BatchID             *string
	BatchIndex          *int64
}

const messageColumns = "id, protocol, contact_number, text, is_mine, sender, timestamp, " +
	"quote_text, msg_type, attachment_info, attachment_id, content_type, media_kind, " +
	"quote_attachment_id, quote_attachment_path, quote_content_type, read, status, " +
	"msg_id, quote_timestamp, quote_author, reply_to_message_id, edited, batch_id, batch_index"

type scanner interface{ Scan(dest ...any) error }

func nullStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func scanMessage(sc scanner) (Message, error) {
	var (
		m                                                           Message
		text, sender, quoteText, msgType, attInfo, attID, ctype     sql.NullString
		mediaKind, qAttID, qAttPath, qCType, status, msgID, qAuthor sql.NullString
		replyTo, batchID                                            sql.NullString
		isMine, read, edited                                        sql.NullInt64
		qTS, batchIndex                                             sql.NullInt64
	)
	err := sc.Scan(&m.RowID, &m.Protocol, &m.ContactNumber, &text, &isMine, &sender, &m.Timestamp,
		&quoteText, &msgType, &attInfo, &attID, &ctype, &mediaKind,
		&qAttID, &qAttPath, &qCType, &read, &status,
		&msgID, &qTS, &qAuthor, &replyTo, &edited, &batchID, &batchIndex)
	if err != nil {
		return m, err
	}
	m.Text, m.Sender, m.QuoteText, m.MsgType = nullStr(text), nullStr(sender), nullStr(quoteText), nullStr(msgType)
	m.AttachmentInfo, m.AttachmentID, m.ContentType, m.MediaKind = nullStr(attInfo), nullStr(attID), nullStr(ctype), nullStr(mediaKind)
	m.QuoteAttachmentID, m.QuoteAttachmentPath, m.QuoteContentType = nullStr(qAttID), nullStr(qAttPath), nullStr(qCType)
	m.Status, m.MsgID, m.QuoteAuthor = nullStr(status), nullStr(msgID), nullStr(qAuthor)
	m.ReplyToMessageID, m.BatchID = nullStr(replyTo), nullStr(batchID)
	m.QuoteTimestamp, m.BatchIndex = nullInt(qTS), nullInt(batchIndex)
	m.IsMine, m.Read, m.Edited = isMine.Int64 != 0, read.Int64 != 0, edited.Int64 != 0
	return m, nil
}

func collectMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LoadCache loads messages grouped by contact_number, ordered by
// (timestamp, id) -- the id tie-break keeps the N mirror rows of a
// multi-attachment message in insertion order.  protocol "" loads every
// protocol.  Like Python's _load_cache it first runs the idempotent
// DedupMessagesByID and DetectGhostOutgoingText.  Image rows have their text
// blanked, as in the Python cache dicts.
func (s *Store) LoadCache(ctx context.Context, protocol string) (map[string][]Message, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	if err := s.RepairMediaKinds(ctx); err != nil {
		return nil, err
	}
	if _, err := s.DedupMessagesByID(ctx); err != nil {
		return nil, err
	}
	if _, err := s.DetectGhostOutgoingText(ctx); err != nil {
		return nil, err
	}
	var (
		rows *sql.Rows
		err  error
	)
	if protocol == "" {
		rows, err = s.db.QueryContext(ctx, "SELECT "+messageColumns+" FROM messages ORDER BY timestamp, id")
	} else {
		rows, err = s.db.QueryContext(ctx, "SELECT "+messageColumns+
			" FROM messages WHERE protocol = ? ORDER BY timestamp, id", protocol)
	}
	if err != nil {
		return nil, err
	}
	msgs, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	cache := make(map[string][]Message)
	for _, m := range msgs {
		if m.MsgType != nil && *m.MsgType == "image" {
			empty := ""
			m.Text = &empty
		}
		cache[m.ContactNumber] = append(cache[m.ContactNumber], m)
	}
	return cache, nil
}

// NewMessage is the input of AddMessage (Python _add_message_to_cache).
type NewMessage struct {
	Protocol      string // default "signal"
	ContactNumber string
	Text          string
	IsMine        bool
	Sender        string
	Timestamp     int64
	MsgType       string // default "text"
	Status        string // default "sent" (outgoing) / "read" (incoming)

	QuoteText           *string
	AttachmentInfo      *string
	AttachmentID        *string
	ContentType         *string
	MediaKind           *string
	MsgID               *string
	QuoteTimestamp      *int64
	QuoteAuthor         *string
	ReplyToMessageID    *string
	QuoteAttachmentID   *string
	QuoteAttachmentPath *string
	QuoteContentType    *string
	BatchID             *string
	BatchIndex          *int64
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AddMessage inserts a message unless a row with the same
// (protocol, contact, text, is_mine, timestamp, msg_id, attachment_id)
// already exists (NULLs compare equal).  It returns the row id and whether a
// new row was inserted.  The batch slot is deliberately not part of the
// dedup key.  The row is stored read=is_mine.
func (s *Store) AddMessage(ctx context.Context, m NewMessage) (id int64, inserted bool, err error) {
	if err = s.settle(ctx); err != nil {
		return 0, false, err
	}
	if m.Protocol == "" {
		m.Protocol = "signal"
	}
	if m.MsgType == "" {
		m.MsgType = "text"
	}
	status := m.Status
	if status == "" {
		if m.IsMine {
			status = "sent"
		} else {
			status = "read"
		}
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		e := tx.QueryRowContext(ctx,
			"SELECT id FROM messages "+
				"WHERE protocol = ? AND contact_number = ? AND text = ? "+
				"AND is_mine = ? AND timestamp = ? "+
				"AND ((msg_id IS NULL AND ? IS NULL) OR msg_id = ?) "+
				"AND ((attachment_id IS NULL AND ? IS NULL) OR attachment_id = ?) "+
				"LIMIT 1",
			m.Protocol, m.ContactNumber, m.Text, b2i(m.IsMine), m.Timestamp,
			m.MsgID, m.MsgID, m.AttachmentID, m.AttachmentID).Scan(&id)
		if e == nil {
			return nil
		}
		if !isNoRows(e) {
			return e
		}
		res, e := tx.ExecContext(ctx,
			`INSERT INTO messages
                   (protocol, contact_number, text, is_mine, sender, timestamp,
                     quote_text, msg_type, attachment_info, attachment_id, content_type,
                     media_kind,
                     quote_attachment_id, quote_attachment_path, quote_content_type,
                       read, status, msg_id, quote_timestamp, quote_author, reply_to_message_id,
                       batch_id, batch_index)
                     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Protocol, m.ContactNumber, m.Text, b2i(m.IsMine), m.Sender, m.Timestamp,
			m.QuoteText, m.MsgType, m.AttachmentInfo, m.AttachmentID, m.ContentType,
			m.MediaKind,
			m.QuoteAttachmentID, m.QuoteAttachmentPath, m.QuoteContentType,
			b2i(m.IsMine), status, m.MsgID, m.QuoteTimestamp, m.QuoteAuthor, m.ReplyToMessageID,
			m.BatchID, m.BatchIndex)
		if e != nil {
			return e
		}
		id, e = res.LastInsertId()
		inserted = true
		return e
	})
	if inserted {
		s.markDirty()
	}
	return id, inserted, err
}

// UpdateMessageID attaches the real msg_id (and the server timestamp) to the
// single closest id-less optimistic row ((protocol, contact, text, is_mine),
// msg_id NULL or ”), restricted to EchoMatchWindowMs around timestamp; ties
// break on rowid.  Reports whether a row was updated.
func (s *Store) UpdateMessageID(ctx context.Context, protocol, contact, text string, isMine bool, timestamp int64, msgID string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	if protocol == "" {
		protocol = "signal"
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET msg_id = ?, timestamp = ? "+
			"WHERE id = ("+
			"SELECT id FROM messages WHERE protocol = ? AND contact_number = ? "+
			"AND text = ? AND is_mine = ? AND (msg_id IS NULL OR msg_id = '') "+
			"AND ABS(timestamp - ?) <= ? "+
			"ORDER BY ABS(timestamp - ?) ASC, rowid ASC LIMIT 1)",
		msgID, timestamp, protocol, contact, text, b2i(isMine),
		timestamp, EchoMatchWindowMs, timestamp)
	if err != nil {
		return false, err
	}
	return rowsAffected(res) > 0, nil
}

// closestRowID picks the row of (protocol, contact, msg_id) nearest to
// timestamp (ties -> lowest rowid).  Returns 0 when none.
func closestRowID(ctx context.Context, ex execer, protocol, contact, msgID string, timestamp int64, extraWhere string, extraArgs ...any) (int64, error) {
	q := "SELECT id FROM messages WHERE protocol = ? AND contact_number = ? AND msg_id = ?"
	args := []any{protocol, contact, msgID}
	if extraWhere != "" {
		q += " AND " + extraWhere
		args = append(args, extraArgs...)
	}
	q += " ORDER BY ABS(timestamp - ?) ASC, rowid ASC LIMIT 1"
	args = append(args, timestamp)
	var id int64
	err := ex.QueryRowContext(ctx, q, args...).Scan(&id)
	if isNoRows(err) {
		return 0, nil
	}
	return id, err
}

// UpdateMessageAttachmentID sets attachment_id on the row of
// (protocol, contact, msgID) closest to timestamp.  When expected is non-nil
// only the row currently carrying that attachment id is eligible (needed to
// upgrade one slot of a multi-attachment batch).  Empty msgID -> false.
func (s *Store) UpdateMessageAttachmentID(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID string, expected *string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	if msgID == "" {
		return false, nil
	}
	var ok bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var (
			id  int64
			err error
		)
		if expected != nil {
			id, err = closestRowID(ctx, tx, protocol, contact, msgID, timestamp, "attachment_id = ?", *expected)
		} else {
			id, err = closestRowID(ctx, tx, protocol, contact, msgID, timestamp, "")
		}
		if err != nil || id == 0 {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE messages SET attachment_id = ? WHERE id = ?", attachmentID, id)
		if err != nil {
			return err
		}
		ok = rowsAffected(res) > 0
		return nil
	})
	if ok {
		s.markDirty()
	}
	return ok, err
}

// UpdateMessageAttachmentInfo sets attachment_info on the closest row of
// (protocol, contact, msgID).
func (s *Store) UpdateMessageAttachmentInfo(ctx context.Context, protocol, contact, msgID string, timestamp int64, info string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	if msgID == "" {
		return false, nil
	}
	var ok bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		id, err := closestRowID(ctx, tx, protocol, contact, msgID, timestamp, "")
		if err != nil || id == 0 {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE messages SET attachment_info = ? WHERE id = ?", info, id)
		if err != nil {
			return err
		}
		ok = rowsAffected(res) > 0
		return nil
	})
	if ok {
		s.markDirty()
	}
	return ok, err
}

// UpdateMessageMediaIdentity repairs "media race" rows: it sets
// attachment_id, msg_type and media_kind on the closest row of
// (protocol, contact, msgID) provided the row has no attachment_id yet.
func (s *Store) UpdateMessageMediaIdentity(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID, msgType string, mediaKind *string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	if msgID == "" {
		return false, nil
	}
	var ok bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		id, err := closestRowID(ctx, tx, protocol, contact, msgID, timestamp, "")
		if err != nil || id == 0 {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE messages SET attachment_id = ?, msg_type = ?, media_kind = ? "+
				"WHERE id = ? AND (attachment_id IS NULL OR attachment_id = '')",
			attachmentID, msgType, mediaKind, id)
		if err != nil {
			return err
		}
		ok = rowsAffected(res) > 0
		return nil
	})
	if ok {
		s.markDirty()
	}
	return ok, err
}

// MediaRow is the media identity of a message row.
type MediaRow struct {
	AttachmentID *string
	Timestamp    int64
	MsgType      *string
	MediaKind    *string
	ContentType  *string
}

// MessageMediaRow returns the media identity of (protocol, contact, msgID),
// preferring rows that already have an attachment id; nil when missing.
func (s *Store) MessageMediaRow(ctx context.Context, protocol, contact, msgID string) (*MediaRow, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	if msgID == "" {
		return nil, nil
	}
	var (
		r                       MediaRow
		att, mtype, kind, ctype sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT attachment_id, timestamp, msg_type, media_kind, content_type "+
			"FROM messages WHERE protocol = ? AND contact_number = ? "+
			"AND msg_id = ? ORDER BY (attachment_id IS NULL OR attachment_id = ''), "+
			"rowid ASC LIMIT 1", protocol, contact, msgID).Scan(&att, &r.Timestamp, &mtype, &kind, &ctype)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.AttachmentID, r.MsgType, r.MediaKind, r.ContentType = nullStr(att), nullStr(mtype), nullStr(kind), nullStr(ctype)
	return &r, nil
}

// QuoteFill carries the quote fields FillMessageQuoteFields may complete.
type QuoteFill struct {
	QuoteText           *string
	QuoteTimestamp      *int64
	QuoteAuthor         *string
	ReplyToMessageID    *string
	QuoteAttachmentID   *string
	QuoteAttachmentPath *string
	QuoteContentType    *string
}

// FillMessageQuoteFields completes the quote columns of the closest row of
// (protocol, contact, msgID): a column is only written when it is currently
// NULL/” (quote_timestamp: NULL).  Reports whether a row matched.
func (s *Store) FillMessageQuoteFields(ctx context.Context, protocol, contact, msgID string, timestamp int64, q QuoteFill) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	if msgID == "" {
		return false, nil
	}
	var ok bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		id, err := closestRowID(ctx, tx, protocol, contact, msgID, timestamp, "")
		if err != nil || id == 0 {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE messages SET "+
				"quote_text = CASE WHEN quote_text IS NULL OR quote_text = '' "+
				"THEN ? ELSE quote_text END, "+
				"quote_timestamp = CASE WHEN quote_timestamp IS NULL "+
				"THEN ? ELSE quote_timestamp END, "+
				"quote_author = CASE WHEN quote_author IS NULL OR quote_author = '' "+
				"THEN ? ELSE quote_author END, "+
				"reply_to_message_id = CASE "+
				"WHEN reply_to_message_id IS NULL OR reply_to_message_id = '' "+
				"THEN ? ELSE reply_to_message_id END, "+
				"quote_attachment_id = CASE "+
				"WHEN quote_attachment_id IS NULL OR quote_attachment_id = '' "+
				"THEN ? ELSE quote_attachment_id END, "+
				"quote_attachment_path = CASE "+
				"WHEN quote_attachment_path IS NULL OR quote_attachment_path = '' "+
				"THEN ? ELSE quote_attachment_path END, "+
				"quote_content_type = CASE "+
				"WHEN quote_content_type IS NULL OR quote_content_type = '' "+
				"THEN ? ELSE quote_content_type END WHERE id = ?",
			q.QuoteText, q.QuoteTimestamp, q.QuoteAuthor, q.ReplyToMessageID,
			q.QuoteAttachmentID, q.QuoteAttachmentPath, q.QuoteContentType, id)
		if err != nil {
			return err
		}
		ok = rowsAffected(res) > 0
		return nil
	})
	return ok, err
}

// MarkAsRead marks every message of the contact (plus alias keys, for the
// WhatsApp @c.us/@lid union) of the protocol as read.
func (s *Store) MarkAsRead(ctx context.Context, protocol, contact string, extraKeys ...string) error {
	if err := s.settle(ctx); err != nil {
		return err
	}
	if protocol == "" {
		protocol = "signal"
	}
	keys := []string{contact}
	for _, k := range extraKeys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	args := append([]any{protocol}, strArgs(keys)...)
	_, err := s.db.ExecContext(ctx,
		"UPDATE messages SET read = 1 "+
			"WHERE protocol = ? AND contact_number IN ("+placeholders(len(keys))+")", args...)
	return err
}

// ReconcileChatUnread aligns a chat's read flags with the phone's unread
// count: incoming messages up to cutoffMs (0 = no bound; pass the timestamp of
// the sync that reported the count) are marked read except the newest keep,
// the ones still unread on the phone. Alias keys cover the WhatsApp @c.us/@lid
// twin storage. Returns the number of rows updated.
func (s *Store) ReconcileChatUnread(ctx context.Context, protocol, contact string, keep int, cutoffMs int64, extraKeys ...string) (int64, error) {
	if err := s.settle(ctx); err != nil {
		return 0, err
	}
	if protocol == "" {
		protocol = "signal"
	}
	if keep < 0 {
		keep = 0
	}
	keys := []string{contact}
	for _, k := range extraKeys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	in := "(" + placeholders(len(keys)) + ")"
	args := append([]any{protocol}, strArgs(keys)...)
	args = append(args, cutoffMs, cutoffMs)
	args = append(args, protocol)
	args = append(args, strArgs(keys)...)
	args = append(args, cutoffMs, cutoffMs, keep)
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET read = 1 "+
			"WHERE protocol = ? AND contact_number IN "+in+" AND is_mine = 0 AND read = 0 "+
			"AND (? = 0 OR timestamp <= ?) "+
			"AND id NOT IN ("+
			"SELECT id FROM messages "+
			"WHERE protocol = ? AND contact_number IN "+in+" AND is_mine = 0 "+
			"AND (? = 0 OR timestamp <= ?) "+
			"ORDER BY timestamp DESC, id DESC LIMIT ?)", args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func statusGuard(newStatus string) string {
	return " AND " + statusRankCase + " <= CASE ? WHEN 'pending' THEN 0 WHEN 'failed' THEN 0 " +
		"WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END"
}

// UpdateMessageStatus sets the status of the rows matching
// (protocol, contact, timestamp[, text][, status IN expected]).  A status
// never moves to a lower rank (pending/failed < sent < delivered < read).
func (s *Store) UpdateMessageStatus(ctx context.Context, timestamp int64, status, protocol, contact string, text *string, expected []string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	where := "protocol = ? AND contact_number = ? AND timestamp = ?"
	params := []any{protocol, contact, timestamp}
	if text != nil {
		where += " AND text = ?"
		params = append(params, *text)
	}
	if len(expected) > 0 {
		where += " AND status IN (" + placeholders(len(expected)) + ")"
		params = append(params, strArgs(expected)...)
	}
	args := append([]any{status}, params...)
	args = append(args, status)
	res, err := s.db.ExecContext(ctx, "UPDATE messages SET status = ? WHERE "+where+statusGuard(status), args...)
	if err != nil {
		return false, err
	}
	return rowsAffected(res) > 0, nil
}

// UpdateMessageStatusByID is UpdateMessageStatus keyed by msg_id; contact is
// optional.
func (s *Store) UpdateMessageStatusByID(ctx context.Context, msgID, status, protocol string, contact *string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	where := "protocol = ? AND msg_id = ?"
	params := []any{protocol, msgID}
	if contact != nil {
		where += " AND contact_number = ?"
		params = append(params, *contact)
	}
	args := append([]any{status}, params...)
	args = append(args, status)
	res, err := s.db.ExecContext(ctx, "UPDATE messages SET status = ? WHERE "+where+statusGuard(status), args...)
	if err != nil {
		return false, err
	}
	return rowsAffected(res) > 0, nil
}

// UpdateMessageStatusByText updates the most recent outgoing row (highest
// timestamp) of (protocol, contact, text), with the same never-downgrade
// guard.  Fallback for when the echo replaced the optimistic timestamp.
func (s *Store) UpdateMessageStatusByText(ctx context.Context, text, status, protocol, contact string, expected []string) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	where := "protocol = ? AND contact_number = ? AND text = ? AND is_mine = 1"
	params := []any{protocol, contact, text}
	if len(expected) > 0 {
		where += " AND status IN (" + placeholders(len(expected)) + ")"
		params = append(params, strArgs(expected)...)
	}
	args := append([]any{status}, params...)
	args = append(args, status)
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET status = ? WHERE id = ("+
			"SELECT id FROM messages WHERE "+where+
			" ORDER BY timestamp DESC LIMIT 1)"+statusGuard(status), args...)
	if err != nil {
		return false, err
	}
	return rowsAffected(res) > 0, nil
}

// TextUpdate describes an in-place text edit (UpdateMessageText).  Rows are
// matched by (Protocol, ContactNumber, MsgID) when MsgID is set, otherwise by
// (Protocol, ContactNumber, Timestamp); with neither the call is a no-op.
type TextUpdate struct {
	Protocol      string
	ContactNumber string
	NewText       string
	MsgID         *string
	Timestamp     *int64
	OldText       *string // optional guard: current text must equal it
	IsMine        *bool   // optional guard
	// ClearEdited stores edited=0 instead of 1 (rollback path).
	ClearEdited bool
}

// UpdateMessageText rewrites the text of existing rows and flags them edited.
func (s *Store) UpdateMessageText(ctx context.Context, u TextUpdate) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	var (
		where  string
		params []any
	)
	switch {
	case u.MsgID != nil:
		where = "protocol = ? AND contact_number = ? AND msg_id = ?"
		params = []any{u.Protocol, u.ContactNumber, *u.MsgID}
	case u.Timestamp != nil:
		where = "protocol = ? AND contact_number = ? AND timestamp = ?"
		params = []any{u.Protocol, u.ContactNumber, *u.Timestamp}
	default:
		return false, nil
	}
	if u.OldText != nil {
		where += " AND text = ?"
		params = append(params, *u.OldText)
	}
	if u.IsMine != nil {
		where += " AND is_mine = ?"
		params = append(params, b2i(*u.IsMine))
	}
	edited := 1
	if u.ClearEdited {
		edited = 0
	}
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE messages SET text = ?, edited = ? WHERE %s", where),
		append([]any{u.NewText, edited}, params...)...)
	if err != nil {
		return false, err
	}
	return rowsAffected(res) > 0, nil
}

// ContactKey identifies a chat.
type ContactKey struct{ Protocol, Contact string }

// CountUnread counts unread incoming messages per contact_number (all
// protocols merged, like Python's _count_unread).
func (s *Store) CountUnread(ctx context.Context) (map[string]int, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT contact_number, COUNT(*) as cnt FROM messages "+
			"WHERE is_mine = 0 AND read = 0 GROUP BY contact_number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// UnreadCounts counts unread incoming messages per (protocol, contact).
func (s *Store) UnreadCounts(ctx context.Context) (map[ContactKey]int, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT protocol, contact_number, COUNT(*) FROM messages "+
			"WHERE is_mine = 0 AND read = 0 "+
			"GROUP BY protocol, contact_number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[ContactKey]int{}
	for rows.Next() {
		var k ContactKey
		var n int
		if err := rows.Scan(&k.Protocol, &k.Contact, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// PruneCache keeps at most limit messages per (protocol, contact), newest
// first (ties: highest rowid).  Rows with status pending/failed and id-less
// rows younger than the echo window are protected.  limit <= 0 disables the
// prune; 0 < limit < MinPruneLimit is raised to MinPruneLimit.  nowMs == 0
// means "now".  Orphaned reactions are pruned afterwards and the file is
// VACUUMed when rows were deleted (best effort).  Returns rows deleted.
//
// The media-file prune that Python chains after this (protocols.media_prune)
// is filesystem work and is not part of the store.
func (s *Store) PruneCache(ctx context.Context, limit int, nowMs int64) (int, error) {
	if err := s.settle(ctx); err != nil {
		return 0, err
	}
	if limit > 0 && limit < MinPruneLimit {
		s.log.Warn("messages.retention_per_contact below minimum; clamped",
			"limit", limit, "min", MinPruneLimit)
		limit = MinPruneLimit
	}
	if limit <= 0 {
		return 0, nil
	}
	if nowMs == 0 {
		nowMs = s.clock().UnixMilli()
	}
	res, err := s.db.ExecContext(ctx, `
                DELETE FROM messages WHERE id IN (
                    SELECT id FROM (
                        SELECT id, ROW_NUMBER() OVER (
                            PARTITION BY protocol, contact_number
                            ORDER BY timestamp DESC, rowid DESC
                        ) AS rn FROM messages
                        WHERE COALESCE(status, '') NOT IN ('pending', 'failed')
                          AND ((msg_id IS NOT NULL AND msg_id != '')
                               OR timestamp < ?)
                    ) WHERE rn > ?
                )
                `, nowMs-EchoMatchWindowMs, limit)
	if err != nil {
		return 0, err
	}
	deleted := int(rowsAffected(res))
	if _, err := s.PruneOrphanReactions(ctx); err != nil {
		s.log.Debug("reaction orphan prune failed", "err", err)
	}
	if deleted > 0 {
		if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
			s.log.Debug("vacuum failed (ignored)", "err", err)
		}
		s.log.Info("cache pruned", "removed", deleted, "limit", limit)
	} else {
		s.log.Info("cache prune: no rows removed", "limit", limit)
	}
	return deleted, nil
}
