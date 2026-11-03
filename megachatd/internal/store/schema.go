package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// initSchema is the port of _init_db: create the tables/indexes if missing,
// upgrade an older database in place, and stamp PRAGMA user_version.
func (s *Store) initSchema(ctx context.Context) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{createMessagesSQL, createReactionsSQL, createTranscriptionsSQL} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		existing := map[string]bool{}
		rows, err := tx.QueryContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'reactions'")
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return err
			}
			existing[name] = true
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !existing["idx_reactions_identity"] {
			if _, err := tx.ExecContext(ctx, createReactionsIdentityIndexSQL); err != nil {
				return err
			}
		}
		if !existing["idx_reactions_contact"] {
			if _, err := tx.ExecContext(ctx, createReactionsContactIndexSQL); err != nil {
				return err
			}
		}
		if err := migrateProtocolSchema(ctx, tx); err != nil {
			return err
		}
		v, err := schemaVersion(ctx, tx)
		if err != nil {
			return err
		}
		if v < SchemaVersion {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
				return err
			}
		}
		return nil
	})
}

// SchemaVersion reports PRAGMA user_version of the open database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return schemaVersion(ctx, s.db)
}

func schemaVersion(ctx context.Context, ex execer) (int, error) {
	var v int
	err := ex.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

func messageColumnSet(ctx context.Context, ex execer) (map[string]bool, error) {
	rows, err := ex.QueryContext(ctx, "PRAGMA table_info(messages)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// audioLike / videoLike are the SQL fragments of the media_kind backfill,
// spelled exactly like the Python source.
const (
	audioCorrectionPredicate = "(LOWER(COALESCE(attachment_id, '')) LIKE '%.oga' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.ogg' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.opus' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.mp3' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.aac' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.m4a' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.wav' " +
		"OR LOWER(LTRIM(COALESCE(content_type, ''))) LIKE 'audio/%' " +
		"OR LOWER(LTRIM(COALESCE(attachment_info, ''))) LIKE 'audio/%')"

	videoCorrectionPredicate = "(LOWER(COALESCE(attachment_id, '')) LIKE '%.mp4' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.mov' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.mkv' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.webm' " +
		"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.avi' " +
		"OR LOWER(LTRIM(COALESCE(content_type, ''))) LIKE 'video/%' " +
		"OR LOWER(LTRIM(COALESCE(attachment_info, ''))) LIKE 'video/%' " +
		"OR LTRIM(COALESCE(attachment_info, '')) LIKE 'Video:%' " +
		"OR LTRIM(COALESCE(attachment_info, '')) LIKE '🎬%' " +
		"OR LTRIM(COALESCE(attachment_info, '')) LIKE 'videoMessage%')"
)

// migrateProtocolSchema is the port of _migrate_protocol_schema.  Columns
// that later code relies on are ensured unconditionally; the legacy part
// (protocol/msg_id columns, index rebuild, media_kind backfill) is gated by
// PRAGMA user_version >= LegacyMigrationVersion.
func migrateProtocolSchema(ctx context.Context, ex execer) error {
	exec := func(q string) error {
		_, err := ex.ExecContext(ctx, q)
		return err
	}
	addColumn := func(columns map[string]bool, name, ddl string) error {
		if columns[name] {
			return nil
		}
		return exec("ALTER TABLE messages ADD COLUMN " + ddl)
	}

	columns, err := messageColumnSet(ctx, ex)
	if err != nil {
		return err
	}
	if err := addColumn(columns, "edited", "edited INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumn(columns, "content_type", "content_type TEXT"); err != nil {
		return err
	}
	if err := addColumn(columns, "media_kind", "media_kind TEXT"); err != nil {
		return err
	}

	if columns, err = messageColumnSet(ctx, ex); err != nil {
		return err
	}
	version, err := schemaVersion(ctx, ex)
	if err != nil {
		return err
	}
	if err := correctMediaKinds(ctx, ex, columns, version); err != nil {
		return err
	}

	for _, c := range [][2]string{
		{"quote_attachment_id", "quote_attachment_id TEXT"},
		{"quote_content_type", "quote_content_type TEXT"},
		{"quote_attachment_path", "quote_attachment_path TEXT"},
		{"batch_id", "batch_id TEXT"},
		{"batch_index", "batch_index INTEGER"},
	} {
		if err := addColumn(columns, c[0], c[1]); err != nil {
			return err
		}
	}

	// The user_version is re-read here, as Python does (the updates above do
	// not change it).
	if version, err = schemaVersion(ctx, ex); err != nil {
		return err
	}
	if version >= LegacyMigrationVersion {
		return nil
	}

	for _, c := range [][2]string{
		{"protocol", "protocol TEXT NOT NULL DEFAULT 'signal'"},
		{"msg_id", "msg_id TEXT"},
		{"quote_timestamp", "quote_timestamp INTEGER"},
		{"quote_author", "quote_author TEXT"},
		{"reply_to_message_id", "reply_to_message_id TEXT"},
	} {
		if err := addColumn(columns, c[0], c[1]); err != nil {
			return err
		}
	}

	if err := exec("DROP INDEX IF EXISTS idx_messages_contact"); err != nil {
		return err
	}
	if err := exec("CREATE INDEX IF NOT EXISTS idx_messages_contact " +
		"ON messages(protocol, contact_number, timestamp)"); err != nil {
		return err
	}

	if columns, err = messageColumnSet(ctx, ex); err != nil {
		return err
	}
	has := func(names ...string) bool {
		for _, n := range names {
			if !columns[n] {
				return false
			}
		}
		return true
	}
	steps := []struct {
		ok  bool
		sql string
	}{
		{has("msg_type"), "UPDATE messages SET media_kind='sticker' " +
			"WHERE media_kind IS NULL AND msg_type='sticker'"},
		{has("content_type"), "UPDATE messages SET media_kind='gif' " +
			"WHERE media_kind IS NULL AND content_type='image/gif'"},
		{has("msg_type"), "UPDATE messages SET media_kind='image' " +
			"WHERE media_kind IS NULL AND msg_type='image'"},
		{has("msg_type", "content_type"), "UPDATE messages SET media_kind='video' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND content_type LIKE 'video/%'"},
		{has("msg_type", "content_type"), "UPDATE messages SET media_kind='audio' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND content_type LIKE 'audio/%'"},
		{has("msg_type", "content_type"), "UPDATE messages SET media_kind='image' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND content_type LIKE 'image/%'"},
		{has("msg_type", "attachment_info"), "UPDATE messages SET media_kind='video' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND (attachment_info LIKE '🎬%' " +
			"OR attachment_info LIKE 'Video:%' " +
			"OR attachment_info LIKE 'videoMessage%')"},
		{has("msg_type", "attachment_info"), "UPDATE messages SET media_kind='audio' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND (attachment_info LIKE '🎵%' " +
			"OR attachment_info LIKE '🎤%' OR attachment_info LIKE 'Audio:%' " +
			"OR attachment_info LIKE 'audioMessage%' " +
			"OR LOWER(LTRIM(attachment_info)) LIKE 'audio/%')"},
		{has("msg_type", "attachment_id"), "UPDATE messages SET media_kind='video' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND (attachment_id LIKE '%.mp4' " +
			"OR attachment_id LIKE '%.mov' OR attachment_id LIKE '%.mkv' " +
			"OR attachment_id LIKE '%.webm' OR attachment_id LIKE '%.avi')"},
		{has("msg_type", "attachment_id"), "UPDATE messages SET media_kind='audio' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND (attachment_id LIKE '%.mp3' " +
			"OR attachment_id LIKE '%.oga' OR attachment_id LIKE '%.ogg' " +
			"OR attachment_id LIKE '%.opus' " +
			"OR attachment_id LIKE '%.aac' OR attachment_id LIKE '%.m4a' " +
			"OR attachment_id LIKE '%.wav')"},
		{has("msg_type", "attachment_id"), "UPDATE messages SET media_kind='image' WHERE media_kind IS NULL " +
			"AND msg_type='attachment' AND (attachment_id LIKE '%.jpg' " +
			"OR attachment_id LIKE '%.jpeg' OR attachment_id LIKE '%.png' " +
			"OR attachment_id LIKE '%.webp' OR attachment_id LIKE '%.heic')"},
		{has("msg_type"), "UPDATE messages SET media_kind='document' " +
			"WHERE media_kind IS NULL AND msg_type='attachment'"},
	}
	for _, st := range steps {
		if !st.ok {
			continue
		}
		if err := exec(st.sql); err != nil {
			return err
		}
	}
	return exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion))
}

// settle re-applies the media_kind correction pass when this process has
// written rows that may need it.  Python re-ran _migrate_protocol_schema on
// every _init_db() call, so rows saved with media_kind NULL were classified
// by the next operation; the Go store reproduces that lazily (it only does
// work after a write that can create such rows).
func (s *Store) settle(ctx context.Context) error {
	if !s.dirty.CompareAndSwap(true, false) {
		return nil
	}
	if err := s.RepairMediaKinds(ctx); err != nil {
		s.dirty.Store(true)
		return err
	}
	return nil
}

// RepairMediaKinds runs the idempotent media_kind classification pass of the
// schema migration (NULL / mis-typed "document" rows -> audio/video/image/
// document).  It is a no-op on a table without the needed columns.
func (s *Store) RepairMediaKinds(ctx context.Context) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		columns, err := messageColumnSet(ctx, tx)
		if err != nil {
			return err
		}
		version, err := schemaVersion(ctx, tx)
		if err != nil {
			return err
		}
		return correctMediaKinds(ctx, tx, columns, version)
	})
}

// markDirty records that rows touched by this process may need RepairMediaKinds.
func (s *Store) markDirty() { s.dirty.Store(true) }

// correctMediaKinds is the media_kind correction pass of
// _migrate_protocol_schema.  Python executes it on every _init_db() call, not
// only once per database.
func correctMediaKinds(ctx context.Context, ex execer, columns map[string]bool, version int) error {
	exec := func(q string) error {
		_, err := ex.ExecContext(ctx, q)
		return err
	}
	hasAll := columns["media_kind"] && columns["attachment_id"] && columns["attachment_info"]
	hasCandidate := false
	if hasAll {
		conds := []string{
			"media_kind='document'",
			"(media_kind IS NULL AND (attachment_id IS NOT NULL " +
				"OR attachment_info IS NOT NULL))",
		}
		if columns["msg_type"] {
			conds = append(conds, "(media_kind IS NULL AND msg_type='attachment')")
		}
		var one int
		err := ex.QueryRowContext(ctx,
			"SELECT 1 FROM messages WHERE "+strings.Join(conds, " OR ")+" LIMIT 1").Scan(&one)
		switch {
		case err == nil:
			hasCandidate = true
		case isNoRows(err):
		default:
			return err
		}
	}

	if hasAll && hasCandidate {
		if err := exec("UPDATE messages SET media_kind='audio' WHERE media_kind='document' AND " +
			audioCorrectionPredicate); err != nil {
			return err
		}
		if err := exec("UPDATE messages SET media_kind='audio' WHERE media_kind IS NULL AND " +
			audioCorrectionPredicate); err != nil {
			return err
		}
		if err := exec("UPDATE messages SET media_kind='video' WHERE media_kind IS NULL AND " +
			videoCorrectionPredicate); err != nil {
			return err
		}
		if columns["msg_type"] {
			if err := exec("UPDATE messages SET media_kind='image' WHERE media_kind IS NULL " +
				"AND COALESCE(msg_type, '') != 'image' " +
				"AND (LOWER(COALESCE(attachment_id, '')) LIKE '%.jpg' " +
				"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.jpeg' " +
				"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.png' " +
				"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.webp' " +
				"OR LOWER(COALESCE(attachment_id, '')) LIKE '%.heic' " +
				"OR LOWER(LTRIM(COALESCE(attachment_info, ''))) LIKE 'image/%')"); err != nil {
				return err
			}
			if version >= LegacyMigrationVersion {
				if err := exec("UPDATE messages SET media_kind='document' " +
					"WHERE media_kind IS NULL " +
					"AND COALESCE(msg_type, '') != 'image' " +
					"AND (msg_type='attachment' OR attachment_id IS NOT NULL) " +
					"AND LOWER(COALESCE(content_type, '')) NOT LIKE 'image/%' " +
					"AND LOWER(LTRIM(COALESCE(attachment_info, ''))) " +
					"NOT LIKE 'image/%'"); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
