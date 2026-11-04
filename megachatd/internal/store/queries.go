package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// Read-side queries that the Python server (server/messages.py, media.py,
// blobs.py, autotranscribe.py, outgoing.py, ops/contacts.py,
// protocols/media_prune.py) ran directly against the same database.  Only the
// SQL lives here; the HTTP-level shaping stays with the callers.

// MessagesLimitSlack is the extra number of rows server/messages.py fetches
// beyond the requested page so the caller's own row dedup still leaves a full
// page.
const MessagesLimitSlack = 50

// MessageQuery selects the rows of one chat (possibly several alias keys).
type MessageQuery struct {
	Protocol string
	// Keys are the contact_number values of the chat (WhatsApp @c.us / @lid
	// aliases); at least one.
	Keys []string
	// Since, when set, keeps only rows with timestamp >= *Since.
	Since *int64
	// SQLLimit > 0 returns the newest SQLLimit rows (callers pass
	// limit+MessagesLimitSlack); 0 returns everything.
	SQLLimit int
}

// ListMessages returns the chat rows in chronological order (timestamp, id).
func (s *Store) ListMessages(ctx context.Context, q MessageQuery) ([]Message, error) {
	if len(q.Keys) == 0 {
		return nil, nil
	}
	query := "SELECT " + messageColumns + " FROM messages WHERE protocol = ? " +
		"AND contact_number IN (" + placeholders(len(q.Keys)) + ") "
	args := append([]any{q.Protocol}, strArgs(q.Keys)...)
	if q.Since != nil {
		query += "AND timestamp >= ? "
		args = append(args, *q.Since)
	}
	if q.SQLLimit > 0 {
		query += "ORDER BY timestamp DESC, id DESC LIMIT ?"
		args = append(args, q.SQLLimit)
	} else {
		query += "ORDER BY timestamp, id"
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	msgs, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	if q.SQLLimit > 0 {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

// MediaFilter restricts quoted-media lookups.
type MediaFilter string

const (
	MediaAny   MediaFilter = ""
	MediaImage MediaFilter = "image"
	MediaVideo MediaFilter = "video"
)

var videoExtensions = []string{".3gp", ".avi", ".m4v", ".mkv", ".mov", ".mp4", ".mpeg", ".mpg", ".webm"}

func init() { sort.Strings(videoExtensions) }

func quotedMediaClause(kind MediaFilter) string {
	image := "content_type LIKE 'image/%' OR lower(attachment_id) LIKE '%.jpg' " +
		"OR lower(attachment_id) LIKE '%.jpeg' OR lower(attachment_id) LIKE '%.png' " +
		"OR lower(attachment_id) LIKE '%.gif' OR lower(attachment_id) LIKE '%.webp'"
	parts := make([]string, len(videoExtensions))
	for i, e := range videoExtensions {
		parts[i] = "lower(attachment_id) LIKE '%" + e + "'"
	}
	video := "content_type LIKE 'video/%' OR " + strings.Join(parts, " OR ")
	switch kind {
	case MediaImage:
		return "AND (" + image + ")"
	case MediaVideo:
		return "AND (" + video + ")"
	}
	return "AND (" + image + " OR " + video + ")"
}

func scanOptionalString(row *sql.Row) (string, error) {
	var v sql.NullString
	err := row.Scan(&v)
	if isNoRows(err) {
		return "", nil
	}
	return v.String, err
}

// QuotedMediaAttachmentID resolves the attachment id of the media quoted by
// a reply, in the same chat: first the exact timestamp; only when no message
// has that timestamp at all, the nearest media within +-2000 ms.  "" when
// there is none (quoteTS 0 included).
func (s *Store) QuotedMediaAttachmentID(ctx context.Context, proto, contact string, quoteTS int64, kind MediaFilter) (string, error) {
	if quoteTS == 0 {
		return "", nil
	}
	clause := quotedMediaClause(kind)
	id, err := scanOptionalString(s.db.QueryRowContext(ctx,
		"SELECT attachment_id FROM messages "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND timestamp = ? AND attachment_id IS NOT NULL "+clause+" LIMIT 1",
		proto, contact, quoteTS))
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	var one int
	err = s.db.QueryRowContext(ctx,
		"SELECT 1 FROM messages WHERE protocol = ? AND contact_number = ? "+
			"AND timestamp = ? LIMIT 1", proto, contact, quoteTS).Scan(&one)
	if err == nil {
		// The quoted message exists but is not compatible media: no
		// thumbnail, never a nearby message.
		return "", nil
	}
	if !isNoRows(err) {
		return "", err
	}
	return scanOptionalString(s.db.QueryRowContext(ctx,
		"SELECT attachment_id FROM messages "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND ABS(timestamp - ?) <= 2000 AND attachment_id IS NOT NULL "+clause+
			" ORDER BY ABS(timestamp - ?) LIMIT 1",
		proto, contact, quoteTS, quoteTS))
}

// QuotedMediaByFilename finds the attachment whose attachment_info or
// attachment_id contains filename, among the 5 newest media strictly older
// than quoterTS, returning the one closest to quoterTS.  "" when none.
func (s *Store) QuotedMediaByFilename(ctx context.Context, proto, contact, filename string, quoterTS int64, kind MediaFilter) (string, error) {
	if filename == "" || quoterTS == 0 {
		return "", nil
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT attachment_id, timestamp FROM messages "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND attachment_id IS NOT NULL AND timestamp < ? "+
			"AND (attachment_info LIKE ? OR attachment_id LIKE ?) "+
			quotedMediaClause(kind)+" "+
			"ORDER BY timestamp DESC LIMIT 5",
		proto, contact, quoterTS, "%"+filename+"%", "%"+filename+"%")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	best, bestDiff, found := "", int64(0), false
	for rows.Next() {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return "", err
		}
		d := ts - quoterTS
		if d < 0 {
			d = -d
		}
		if !found || d < bestDiff {
			best, bestDiff, found = id, d, true
		}
	}
	return best, rows.Err()
}

// QuotedMediaByMessageID finds the quoted media by msg_id (exact, or any id
// ending in "_<id>" such as WhatsApp serialized ids).
func (s *Store) QuotedMediaByMessageID(ctx context.Context, proto, contact, replyToMessageID string, kind MediaFilter) (string, error) {
	if replyToMessageID == "" {
		return "", nil
	}
	return scanOptionalString(s.db.QueryRowContext(ctx,
		"SELECT attachment_id FROM messages "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND attachment_id IS NOT NULL "+
			"AND (msg_id = ? OR msg_id LIKE '%_' || ?) "+
			quotedMediaClause(kind)+" LIMIT 1",
		proto, contact, replyToMessageID, replyToMessageID))
}

// QuotedSender returns the sender of the message with exactly this timestamp
// in the chat ("" when unknown).
func (s *Store) QuotedSender(ctx context.Context, proto, contact string, quoteTS int64) (string, error) {
	if quoteTS == 0 {
		return "", nil
	}
	return scanOptionalString(s.db.QueryRowContext(ctx,
		"SELECT sender FROM messages "+
			"WHERE protocol = ? AND contact_number = ? AND timestamp = ? LIMIT 1",
		proto, contact, quoteTS))
}

// EditTarget is the row looked up before editing a message.
type EditTarget struct {
	RowID     int64
	MsgID     *string
	Text      *string
	IsMine    bool
	Timestamp int64
	Protocol  string
	MsgType   *string
	Status    *string
}

// MessageRowForEdit finds the message to edit by msg_id (Signal id-less rows
// are matched by timestamp == messageID).  nil when not found.
func (s *Store) MessageRowForEdit(ctx context.Context, protocol, contact, messageID string, extraKeys ...string) (*EditTarget, error) {
	keys := []string{contact}
	for _, k := range extraKeys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	args := append([]any{protocol}, strArgs(keys)...)
	args = append(args, messageID, protocol, messageID, messageID)
	var (
		t                       EditTarget
		msgID, text, mt, status sql.NullString
		isMine                  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT id, msg_id, text, is_mine, timestamp, protocol, msg_type, "+
			"status FROM messages "+
			"WHERE protocol = ? AND contact_number IN ("+placeholders(len(keys))+") "+
			"AND (msg_id = ? OR (? = 'signal' AND msg_id IS NULL "+
			"AND timestamp = CAST(? AS INTEGER))) "+
			"ORDER BY CASE WHEN msg_id = ? THEN 0 ELSE 1 END LIMIT 1", args...).
		Scan(&t.RowID, &msgID, &text, &isMine, &t.Timestamp, &t.Protocol, &mt, &status)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.MsgID, t.Text, t.MsgType, t.Status, t.IsMine = nullStr(msgID), nullStr(text), nullStr(mt), nullStr(status), isMine.Int64 != 0
	return &t, nil
}

// ReactionTargetMessage is the message a web reaction refers to.
type ReactionTargetMessage struct {
	RowID     int64
	MsgID     *string
	IsMine    bool
	Timestamp int64
	Protocol  string
}

// MessageRowForReaction finds the message a UI reaction targets: by msg_id,
// and for Signal also by timestamp or row id equal to messageID.
func (s *Store) MessageRowForReaction(ctx context.Context, protocol, contact, messageID string) (*ReactionTargetMessage, error) {
	var (
		t      ReactionTargetMessage
		msgID  sql.NullString
		isMine sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT id, msg_id, is_mine, timestamp, protocol FROM messages "+
			"WHERE protocol = ? AND contact_number = ? AND "+
			"(msg_id = ? "+
			"OR (? = 'signal' AND timestamp = CAST(? AS INTEGER)) "+
			"OR (? = 'signal' AND id = CAST(? AS INTEGER))) "+
			"ORDER BY CASE WHEN msg_id = ? THEN 0 ELSE 1 END LIMIT 1",
		protocol, contact, messageID, protocol, messageID, protocol, messageID, messageID).
		Scan(&t.RowID, &msgID, &isMine, &t.Timestamp, &t.Protocol)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.MsgID, t.IsMine = nullStr(msgID), isMine.Int64 != 0
	return &t, nil
}

// PersistMessageEdit applies a web edit (text + edited=1) to every row
// matching msg_id (or, for Signal id-less rows, timestamp == messageID) and
// returns the number of rows changed.
func (s *Store) PersistMessageEdit(ctx context.Context, protocol, contact, messageID, newText string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET text = ?, edited = 1 "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND (msg_id = ? OR (? = 'signal' AND msg_id IS NULL "+
			"AND timestamp = CAST(? AS INTEGER)))",
		newText, protocol, contact, messageID, protocol, messageID)
	if err != nil {
		return 0, err
	}
	return rowsAffected(res), nil
}

// AttachmentContentType returns the content type of the newest row carrying
// the attachment ("image/*" for legacy image rows without one); nil if unknown.
func (s *Store) AttachmentContentType(ctx context.Context, proto, attachmentID string) (*string, error) {
	var ct, mt sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT content_type, msg_type FROM messages "+
			"WHERE protocol = ? AND attachment_id = ? "+
			"ORDER BY id DESC LIMIT 1", proto, attachmentID).Scan(&ct, &mt)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ct.Valid && ct.String != "" {
		return &ct.String, nil
	}
	if mt.Valid && mt.String == "image" {
		return Ptr("image/*"), nil
	}
	return nil, nil
}

// AttachmentMediaKind returns media_kind of the newest row carrying the
// attachment; nil if unknown.
func (s *Store) AttachmentMediaKind(ctx context.Context, proto, attachmentID string) (*string, error) {
	var mk sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT media_kind FROM messages WHERE protocol = ? AND attachment_id = ? "+
			"ORDER BY id DESC LIMIT 1", proto, attachmentID).Scan(&mk)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nullStr(mk), nil
}

// QuoteAttachmentPath returns quote_attachment_path of message row rowID of
// the protocol; nil when the row or the value is missing.
func (s *Store) QuoteAttachmentPath(ctx context.Context, proto string, rowID int64) (*string, error) {
	var p sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT quote_attachment_path FROM messages WHERE id = ? AND protocol = ?",
		rowID, proto).Scan(&p)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nullStr(p), nil
}

// QuotedRow is the stored row of the message a reply refers to.
type QuotedRow struct {
	MsgID        *string
	Timestamp    int64
	MediaKind    *string
	AttachmentID *string
	IsMine       bool
}

// FindQuotedMessage looks up the replied-to message among the chat keys:
// first by reply id (exact or "..._<id>" suffix), then by quote timestamp;
// rows with a voice/audio kind are preferred.  nil when neither matches.
func (s *Store) FindQuotedMessage(ctx context.Context, protocol string, keys []string, replyToMessageID string, quoteTS int64) (*QuotedRow, error) {
	base := "SELECT msg_id, timestamp, media_kind, attachment_id, is_mine " +
		"FROM messages WHERE protocol = ? AND contact_number IN (" + placeholders(len(keys)) + ") "
	type attempt struct {
		sql  string
		args []any
	}
	var attempts []attempt
	head := append([]any{protocol}, strArgs(keys)...)
	if rid := strings.TrimSpace(replyToMessageID); rid != "" {
		attempts = append(attempts, attempt{
			base + "AND (msg_id = ? OR msg_id LIKE '%_' || ?) " +
				"ORDER BY (media_kind IN ('voice', 'audio')) DESC LIMIT 1",
			append(append([]any{}, head...), rid, rid)})
	}
	if quoteTS != 0 {
		attempts = append(attempts, attempt{
			base + "AND timestamp = ? ORDER BY (media_kind IN ('voice', 'audio')) DESC LIMIT 1",
			append(append([]any{}, head...), quoteTS)})
	}
	for _, a := range attempts {
		var (
			r                  QuotedRow
			msgID, kind, attID sql.NullString
			isMine             sql.NullInt64
		)
		err := s.db.QueryRowContext(ctx, a.sql, a.args...).Scan(&msgID, &r.Timestamp, &kind, &attID, &isMine)
		if isNoRows(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		r.MsgID, r.MediaKind, r.AttachmentID, r.IsMine = nullStr(msgID), nullStr(kind), nullStr(attID), isMine.Int64 != 0
		return &r, nil
	}
	return nil, nil
}

// OutgoingRowCounts counts outgoing rows by text and by timestamp for a chat
// (diagnostics of a failed status transition).
func (s *Store) OutgoingRowCounts(ctx context.Context, protocol, contact, text string, timestamp int64) (byText, byTimestamp int, err error) {
	if err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE protocol=? "+
			"AND contact_number=? AND text=? AND is_mine=1",
		protocol, contact, text).Scan(&byText); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE protocol=? "+
			"AND contact_number=? AND timestamp=? AND is_mine=1",
		protocol, contact, timestamp).Scan(&byTimestamp)
	return
}

// MediaReferences collects, per requested protocol, every attachment_id,
// quote_attachment_id and quote_attachment_path referenced by a message
// (the input of the media-file prune).
func (s *Store) MediaReferences(ctx context.Context, protocols []string) (map[string]map[string]struct{}, error) {
	seen := map[string]bool{}
	var req []string
	for _, p := range protocols {
		if !seen[p] {
			seen[p] = true
			req = append(req, p)
		}
	}
	result := make(map[string]map[string]struct{}, len(req))
	if len(req) == 0 {
		return result, nil
	}
	for _, p := range req {
		result[p] = map[string]struct{}{}
	}
	rows, err := s.db.QueryContext(ctx, `
            SELECT protocol, attachment_id, quote_attachment_id, quote_attachment_path
            FROM messages
            WHERE protocol IN (`+placeholders(len(req))+`)
              AND (attachment_id IS NOT NULL OR quote_attachment_id IS NOT NULL
                   OR quote_attachment_path IS NOT NULL)
        `, strArgs(req)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var a, b, c sql.NullString
		if err := rows.Scan(&p, &a, &b, &c); err != nil {
			return nil, err
		}
		refs := result[p]
		if refs == nil {
			refs = map[string]struct{}{}
			result[p] = refs
		}
		for _, v := range []sql.NullString{a, b, c} {
			if v.Valid {
				refs[v.String] = struct{}{}
			}
		}
	}
	return result, rows.Err()
}

// DistinctContacts lists the distinct contact_number values stored for
// protocol (any row, incoming or outgoing).
func (s *Store) DistinctContacts(ctx context.Context, protocol string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT DISTINCT contact_number FROM messages WHERE protocol=?", protocol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MessagesByWAID returns the rows of the chat (any of keys) whose msg_id is
// waID or a WAHA-serialized id containing it ("true_<chat>_<ID>[_<part>]").
// Candidates are a superset: callers confirm with their own id comparison.
func (s *Store) MessagesByWAID(ctx context.Context, protocol string, keys []string, waID string) ([]Message, error) {
	if len(keys) == 0 || waID == "" {
		return nil, nil
	}
	args := append([]any{protocol}, strArgs(keys)...)
	args = append(args, waID, "%\\_"+escapeLike(waID)+"%")
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+messageColumns+" FROM messages WHERE protocol = ? "+
			"AND contact_number IN ("+placeholders(len(keys))+") "+
			"AND (msg_id = ? OR msg_id LIKE ? ESCAPE '\\') ORDER BY id DESC LIMIT 20", args...)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
