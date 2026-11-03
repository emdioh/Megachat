package store

import (
	"context"
	"database/sql"
)

// Transcription is one row of the transcriptions table (voice-note
// transcripts keyed by (protocol, attachment_id)).
type Transcription struct {
	Protocol     string
	AttachmentID string
	Status       string
	Text         *string
	Error        *string
	Model        *string
	UpdatedAt    float64 // unix seconds
}

// GetTranscription returns the stored record, or nil when absent.
func (s *Store) GetTranscription(ctx context.Context, protocol, attachmentID string) (*Transcription, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	t := Transcription{Protocol: protocol, AttachmentID: attachmentID}
	var text, errText, model sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT status, text, error, model, updated_at "+
			"FROM transcriptions WHERE protocol = ? AND attachment_id = ?",
		protocol, attachmentID).Scan(&t.Status, &text, &errText, &model, &t.UpdatedAt)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Text, t.Error, t.Model = nullStr(text), nullStr(errText), nullStr(model)
	return &t, nil
}

// SetTranscription upserts a record (all columns are replaced) and stamps
// updated_at with the current time.  An empty Status is stored as given; the
// Python callers always pass one.
func (s *Store) SetTranscription(ctx context.Context, t Transcription) error {
	if err := s.settle(ctx); err != nil {
		return err
	}
	now := float64(s.clock().UnixNano()) / 1e9
	_, err := s.db.ExecContext(ctx, `
                INSERT INTO transcriptions (
                    protocol, attachment_id, status, text, error, model, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(protocol, attachment_id) DO UPDATE SET
                    status = excluded.status,
                    text = excluded.text,
                    error = excluded.error,
                    model = excluded.model,
                    updated_at = excluded.updated_at
                `, t.Protocol, t.AttachmentID, t.Status, t.Text, t.Error, t.Model, now)
	return err
}

// RecentTranscriptions returns the most recently updated records.
func (s *Store) RecentTranscriptions(ctx context.Context, limit int) ([]Transcription, error) {
	if err := s.settle(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT protocol, attachment_id, status, text, error, model, "+
			"updated_at FROM transcriptions ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transcription
	for rows.Next() {
		var t Transcription
		var text, errText, model sql.NullString
		if err := rows.Scan(&t.Protocol, &t.AttachmentID, &t.Status, &text, &errText, &model, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Text, t.Error, t.Model = nullStr(text), nullStr(errText), nullStr(model)
		out = append(out, t)
	}
	return out, rows.Err()
}
