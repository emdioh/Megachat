package store

import "context"

// QuotedImageByFilename finds the attachment id of an image of the chat whose
// attachment_info or attachment_id contains filename (server/media.py
// _quoted_id_by_filename: no timestamp constraint, first match). "" if none.
func (s *Store) QuotedImageByFilename(ctx context.Context, proto, contact, filename string) (string, error) {
	return scanOptionalString(s.db.QueryRowContext(ctx,
		"SELECT attachment_id FROM messages "+
			"WHERE protocol = ? AND contact_number = ? "+
			"AND attachment_id IS NOT NULL "+
			"AND (attachment_info LIKE ? OR attachment_id LIKE ?) "+
			quotedMediaClause(MediaImage)+" LIMIT 1",
		proto, contact, "%"+filename+"%", "%"+filename+"%"))
}

// MessageExistsAtTimestamp reports whether any message of the chat has exactly
// this timestamp.
func (s *Store) MessageExistsAtTimestamp(ctx context.Context, proto, contact string, ts int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM messages WHERE protocol = ? AND contact_number = ? "+
			"AND timestamp = ? LIMIT 1", proto, contact, ts).Scan(&one)
	if isNoRows(err) {
		return false, nil
	}
	return err == nil, err
}
