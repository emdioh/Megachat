package store

import (
	"context"
	"database/sql"
)

// Store methods the protocol backends need (mc-zao.3.7). Each ports raw SQL
// the Python backends ran against messages.db directly.

// SetBatchSlot stamps batch_id/batch_index on the row with messages.id rowID
// (protocols/signal.py _repair_batch_slots: UPDATE ... WHERE id = ?).
func (s *Store) SetBatchSlot(ctx context.Context, rowID int64, batchID *string, batchIndex int64) error {
	if err := s.settle(ctx); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET batch_id = ?, batch_index = ? WHERE id = ?",
		batchID, batchIndex, rowID)
	if err != nil {
		return err
	}
	if rowsAffected(res) > 0 {
		s.markDirty()
	}
	return nil
}

// DeleteMirrorRows deletes the rows of (protocol, contact, msgID) whose
// attachment_id is in attachmentIDs (signal.py barrier rollback). An empty
// list is a no-op.
func (s *Store) DeleteMirrorRows(ctx context.Context, protocol, contact, msgID string, attachmentIDs []string) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if err := s.settle(ctx); err != nil {
		return err
	}
	args := append([]any{protocol, contact, msgID}, strArgs(attachmentIDs)...)
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM messages WHERE protocol = ? AND contact_number = ? AND msg_id = ? "+
			"AND attachment_id IN ("+placeholders(len(attachmentIDs))+")", args...)
	if err != nil {
		return err
	}
	if rowsAffected(res) > 0 {
		s.markDirty()
	}
	return nil
}

// MergeBatchSlot stamps the batch slot on the not-yet-batched rows of
// (protocol, contact, msgID) (telegram/whatsapp _merge_batch_slot: UPDATE ...
// AND batch_id IS NULL). A nil batchIndex stores NULL. Reports whether a row
// was touched.
func (s *Store) MergeBatchSlot(ctx context.Context, protocol, contact, msgID, batchID string, batchIndex *int64) (bool, error) {
	if err := s.settle(ctx); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET batch_id = ?, batch_index = ? "+
			"WHERE protocol = ? AND contact_number = ? AND msg_id = ? AND batch_id IS NULL",
		batchID, batchIndex, protocol, contact, msgID)
	if err != nil {
		return false, err
	}
	ok := rowsAffected(res) > 0
	if ok {
		s.markDirty()
	}
	return ok, nil
}

// ReusedRow is the row ReuseFailedRow re-keyed.
type ReusedRow struct {
	Timestamp int64
	Status    string
}

// ReuseFailedRow is whatsapp.py _reuse_failed_db_row: when exactly one failed,
// id-less outgoing row with this text lies within EchoMatchWindowMs of ts, give
// it msgID and status, and (only if its msg_id was NULL, not ”) the echo's
// timestamp. Returns nil when there are 0 or 2+ candidates.
func (s *Store) ReuseFailedRow(ctx context.Context, protocol, contact, text string, ts int64, msgID, status string) (*ReusedRow, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	var out *ReusedRow
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			"SELECT id, msg_id, timestamp FROM messages "+
				"WHERE protocol = ? AND contact_number = ? AND is_mine = 1 "+
				"AND text = ? AND (msg_id IS NULL OR msg_id = '') "+
				"AND status = 'failed' AND ABS(timestamp - ?) <= ? "+
				"ORDER BY ABS(timestamp - ?) ASC, rowid ASC",
			protocol, contact, text, ts, EchoMatchWindowMs, ts)
		if err != nil {
			return err
		}
		type cand struct {
			id, ts int64
			msgID  sql.NullString
		}
		var cands []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.id, &c.msgID, &c.ts); err != nil {
				_ = rows.Close()
				return err
			}
			cands = append(cands, c)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		if len(cands) != 1 {
			return nil
		}
		c := cands[0]
		newTS := c.ts
		if !c.msgID.Valid {
			newTS = ts
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE messages SET msg_id = ?, timestamp = ?, status = ? WHERE id = ?",
			msgID, newTS, status, c.id)
		if err != nil {
			return err
		}
		if rowsAffected(res) > 0 {
			out = &ReusedRow{Timestamp: newTS, Status: status}
		}
		return nil
	})
	if out != nil {
		s.markDirty()
	}
	return out, err
}
