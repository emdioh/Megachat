package store

import (
	"context"
	"database/sql"
)

// Reaction is one row of the reactions table.  Per-author reactions have the
// author's key in AuthorKey; protocol snapshot rows use "__agg__:<emoji>".
type Reaction struct {
	ID              int64
	Protocol        string
	ContactNumber   string
	TargetMsgID     *string
	TargetTimestamp *int64
	Emoji           string
	AuthorKey       string
	Author          string
	IsMine          bool
	Count           int64
	Timestamp       int64
}

// ReactionDelta is an author-scoped add/change/remove.
type ReactionDelta struct {
	Protocol        string
	Contact         string
	TargetMsgID     *string
	TargetTimestamp *int64
	Emoji           string
	AuthorKey       string
	Author          string
	IsMine          bool
	IsRemove        bool
	Timestamp       int64
}

// ReactionEntry is one aggregate row of a ReplaceReactionsSnapshot call.
type ReactionEntry struct {
	Emoji     string
	AuthorKey string
	Author    string
	IsMine    bool
	Count     int64
}

// ReactionTarget is the cached identity of a reaction target message.
type ReactionTarget struct {
	ID        int64
	MsgID     *string
	Timestamp int64
}

// ApplyReactionDelta replaces the author's reaction on the target (delete +
// insert) or, with IsRemove, just deletes it.  It reports whether anything
// changed (an add/change always does; a remove only if a row existed).
func (s *Store) ApplyReactionDelta(ctx context.Context, d ReactionDelta) (bool, error) {
	var changed bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM reactions WHERE protocol = ? AND contact_number = ? "+
				"AND IFNULL(target_msg_id, '') = IFNULL(?, '') "+
				"AND IFNULL(target_timestamp, 0) = IFNULL(?, 0) "+
				"AND author_key = ?",
			d.Protocol, d.Contact, d.TargetMsgID, d.TargetTimestamp, d.AuthorKey)
		if err != nil {
			return err
		}
		changed = rowsAffected(res) > 0
		if !d.IsRemove {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO reactions "+
					"(protocol, contact_number, target_msg_id, target_timestamp, "+
					"emoji, author_key, author, is_mine, count, timestamp) "+
					"VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)",
				d.Protocol, d.Contact, d.TargetMsgID, d.TargetTimestamp,
				d.Emoji, d.AuthorKey, d.Author, b2i(d.IsMine), d.Timestamp); err != nil {
				return err
			}
			changed = true
		}
		return nil
	})
	return changed, err
}

// ReplaceReactionsSnapshot replaces the aggregate ("__agg__*") rows of a
// target with a complete protocol snapshot; per-author rows are preserved.
func (s *Store) ReplaceReactionsSnapshot(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64, entries []ReactionEntry, ts int64) (bool, error) {
	var changed bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM reactions WHERE protocol = ? AND contact_number = ? "+
				"AND IFNULL(target_msg_id, '') = IFNULL(?, '') "+
				"AND IFNULL(target_timestamp, 0) = IFNULL(?, 0) "+
				"AND author_key LIKE '__agg__%'",
			protocol, contact, targetMsgID, targetTS)
		if err != nil {
			return err
		}
		changed = rowsAffected(res) > 0
		for _, e := range entries {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO reactions "+
					"(protocol, contact_number, target_msg_id, target_timestamp, "+
					"emoji, author_key, author, is_mine, count, timestamp) "+
					"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				protocol, contact, targetMsgID, targetTS,
				e.Emoji, e.AuthorKey, e.Author, b2i(e.IsMine), e.Count, ts); err != nil {
				return err
			}
			changed = true
		}
		return nil
	})
	return changed, err
}

// ReactionsForContact returns every persisted reaction of a chat ordered by id.
func (s *Store) ReactionsForContact(ctx context.Context, protocol, contact string) ([]Reaction, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, protocol, contact_number, target_msg_id, target_timestamp, emoji, "+
			"author_key, author, is_mine, count, timestamp FROM reactions "+
			"WHERE protocol = ? AND contact_number = ? ORDER BY id", protocol, contact)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reaction
	for rows.Next() {
		var (
			r      Reaction
			tmid   sql.NullString
			tts    sql.NullInt64
			isMine sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.Protocol, &r.ContactNumber, &tmid, &tts, &r.Emoji,
			&r.AuthorKey, &r.Author, &isMine, &r.Count, &r.Timestamp); err != nil {
			return nil, err
		}
		r.TargetMsgID, r.TargetTimestamp, r.IsMine = nullStr(tmid), nullInt(tts), isMine.Int64 != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveReactionTargetRow maps a reaction target to its cached message.
// Signal matches by msg_id, falling back to timestamp for id-less rows;
// other protocols match by msg_id only.  nil when not cached.
func (s *Store) ResolveReactionTargetRow(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64) (*ReactionTarget, error) {
	var row *sql.Row
	if protocol == "signal" {
		row = s.db.QueryRowContext(ctx,
			"SELECT id, msg_id, timestamp FROM messages "+
				"WHERE protocol = ? AND contact_number = ? "+
				"AND (msg_id = ? OR (msg_id IS NULL AND ? IS NOT NULL "+
				"AND timestamp = ?)) "+
				"ORDER BY CASE WHEN msg_id = ? THEN 0 ELSE 1 END LIMIT 1",
			protocol, contact, targetMsgID, targetTS, targetTS, targetMsgID)
	} else {
		row = s.db.QueryRowContext(ctx,
			"SELECT id, msg_id, timestamp FROM messages "+
				"WHERE protocol = ? AND contact_number = ? AND msg_id = ? "+
				"ORDER BY rowid LIMIT 1", protocol, contact, targetMsgID)
	}
	var (
		t     ReactionTarget
		msgID sql.NullString
	)
	if err := row.Scan(&t.ID, &msgID, &t.Timestamp); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	t.MsgID = nullStr(msgID)
	return &t, nil
}

// PruneOrphanReactions deletes reactions whose target message is no longer
// cached and returns how many were removed.
func (s *Store) PruneOrphanReactions(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
                DELETE FROM reactions
                WHERE NOT EXISTS (
                    SELECT 1 FROM messages m
                    WHERE m.protocol = reactions.protocol
                      AND m.contact_number = reactions.contact_number
                      AND (
                          (m.msg_id IS NOT NULL AND m.msg_id != ''
                           AND m.msg_id = reactions.target_msg_id)
                          OR (m.msg_id IS NULL
                              AND m.timestamp = reactions.target_timestamp)
                      )
                )
                `)
	if err != nil {
		return 0, err
	}
	return int(rowsAffected(res)), nil
}
