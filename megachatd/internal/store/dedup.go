package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

// DedupMessages removes exact duplicates ((protocol, contact, timestamp, text,
// is_mine) tuples), keeping the lowest rowid.  Returns rows removed.
func (s *Store) DedupMessages(ctx context.Context) (int, error) {
	var removed int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
                DELETE FROM messages WHERE rowid NOT IN (
                    SELECT MIN(rowid) FROM messages
                    GROUP BY protocol, contact_number, timestamp, text, is_mine
                )
            `)
		if err != nil {
			return err
		}
		removed = rowsAffected(res)
		return nil
	})
	return int(removed), err
}

var idMergeFields = []string{
	"content_type",
	"media_kind",
	"quote_text",
	"quote_timestamp",
	"quote_author",
	"reply_to_message_id",
	"quote_attachment_id",
	"quote_attachment_path",
	"quote_content_type",
}

// DedupMessagesByID merges rows with the same (protocol, contact, msg_id,
// text, attachment_id) identity.  The row with the highest status rank (then
// lowest rowid) survives and absorbs the missing quote/media fields of the
// others; “edited“ becomes the maximum.  Partitions whose timestamps span
// more than EchoMatchWindowMs are logged and left untouched.  Idempotent.
func (s *Store) DedupMessagesByID(ctx context.Context) (int, error) {
	removed := 0
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		divergent, err := queryRowMaps(ctx, tx,
			"SELECT protocol, contact_number, msg_id, text, attachment_id, "+
				"COUNT(*) AS cnt, "+
				"MIN(timestamp) AS min_ts, MAX(timestamp) AS max_ts "+
				"FROM messages WHERE msg_id IS NOT NULL AND msg_id != '' "+
				"GROUP BY protocol, contact_number, msg_id, text, attachment_id "+
				"HAVING MAX(timestamp) - MIN(timestamp) > ?", EchoMatchWindowMs)
		if err != nil {
			return err
		}
		for _, d := range divergent {
			s.log.Warn("dedup skipped partition with divergent timestamps",
				"protocol", d["protocol"], "contact_number", d["contact_number"],
				"msg_id", d["msg_id"], "text", d["text"], "attachment_id", d["attachment_id"],
				"rows", d["cnt"], "min_ts", d["min_ts"], "max_ts", d["max_ts"])
		}

		partitions, err := queryRowMaps(ctx, tx,
			"SELECT protocol, contact_number, msg_id, text, attachment_id "+
				"FROM messages WHERE msg_id IS NOT NULL AND msg_id != '' "+
				"GROUP BY protocol, contact_number, msg_id, text, attachment_id "+
				"HAVING COUNT(*) > 1 "+
				"AND MAX(timestamp) - MIN(timestamp) <= ?", EchoMatchWindowMs)
		if err != nil {
			return err
		}
		for _, p := range partitions {
			rows, err := queryRowMaps(ctx, tx,
				"SELECT id, status, edited, content_type, media_kind, "+
					"quote_text, quote_timestamp, quote_author, "+
					"reply_to_message_id, quote_attachment_id, "+
					"quote_attachment_path, quote_content_type "+
					"FROM messages WHERE protocol = ? AND contact_number = ? "+
					"AND msg_id = ? AND text IS ? AND attachment_id IS ? "+
					"ORDER BY "+statusRankCase+" DESC, rowid ASC",
				p["protocol"], p["contact_number"], p["msg_id"], p["text"], p["attachment_id"])
			if err != nil {
				return err
			}
			if len(rows) < 2 {
				continue
			}
			survivor := rows[0]
			merged := make(map[string]any, len(idMergeFields))
			for _, f := range idMergeFields {
				merged[f] = survivor[f]
			}
			var maxEdited int64
			for i, r := range rows {
				if v := r.int("edited"); i == 0 || v > maxEdited {
					maxEdited = v
				}
			}
			for _, other := range rows[1:] {
				for _, f := range idMergeFields {
					if isMissing(merged[f]) && !isMissing(other[f]) {
						merged[f] = other[f]
					}
				}
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE messages SET content_type = ?, media_kind = ?, "+
					"quote_text = ?, quote_timestamp = ?, quote_author = ?, "+
					"reply_to_message_id = ?, quote_attachment_id = ?, "+
					"quote_attachment_path = ?, quote_content_type = ?, "+
					"edited = ? WHERE id = ?",
				merged["content_type"], merged["media_kind"], merged["quote_text"],
				merged["quote_timestamp"], merged["quote_author"], merged["reply_to_message_id"],
				merged["quote_attachment_id"], merged["quote_attachment_path"],
				merged["quote_content_type"], maxEdited, survivor["id"]); err != nil {
				return err
			}
			ids := make([]any, 0, len(rows)-1)
			for _, r := range rows[1:] {
				ids = append(ids, r["id"])
			}
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM messages WHERE id IN ("+placeholders(len(ids))+")", ids...); err != nil {
				return err
			}
			removed += len(rows) - 1
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// DedupOutgoingAttachmentMirrors merges the WhatsApp outgoing pairs made of
// a WAHA-URL row and its client-side “sent-*“ mirror (same msg_id) into the
// durable “sent-*“ row.  Scope: protocol='whatsapp' AND is_mine=1.
func (s *Store) DedupOutgoingAttachmentMirrors(ctx context.Context) (int, error) {
	removed := 0
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		groups, err := queryRowMaps(ctx, tx,
			"SELECT contact_number, msg_id FROM messages "+
				"WHERE protocol = 'whatsapp' AND is_mine = 1 "+
				"AND msg_id IS NOT NULL AND msg_id != '' "+
				"AND attachment_id IS NOT NULL AND attachment_id != '' "+
				"GROUP BY contact_number, msg_id "+
				"HAVING COUNT(*) = 2 "+
				"AND COUNT(DISTINCT CASE "+
				"WHEN attachment_id LIKE 'sent-%' "+
				"OR attachment_id LIKE '%/sent-%' THEN 'm' ELSE 'r' END) = 2 "+
				"AND COUNT(DISTINCT attachment_id) = 2 "+
				"AND MAX(timestamp) - MIN(timestamp) <= ?", EchoMatchWindowMs)
		if err != nil {
			return err
		}
		for _, g := range groups {
			rows, err := queryRowMaps(ctx, tx,
				"SELECT id, attachment_id, text, attachment_info, status, "+
					"msg_type, content_type, media_kind, quote_text, "+
					"quote_timestamp, quote_author, reply_to_message_id, "+
					"quote_attachment_id, quote_attachment_path, "+
					"quote_content_type, batch_id, batch_index, edited "+
					"FROM messages WHERE protocol = 'whatsapp' "+
					"AND is_mine = 1 AND contact_number = ? AND msg_id = ? "+
					"AND attachment_id IS NOT NULL AND attachment_id != '' "+
					"ORDER BY "+statusRankCase+" DESC, rowid ASC",
				g["contact_number"], g["msg_id"])
			if err != nil {
				return err
			}
			var mirrors, others []rowMap
			for _, r := range rows {
				if isSentMirrorAttachmentID(r.str("attachment_id")) {
					mirrors = append(mirrors, r)
				} else {
					others = append(others, r)
				}
			}
			if len(mirrors) != 1 || len(others) != 1 ||
				mirrors[0]["attachment_id"] == others[0]["attachment_id"] {
				ids := make([]any, len(rows))
				for i, r := range rows {
					ids[i] = r["attachment_id"]
				}
				s.log.Warn("outgoing attachment mirror dedup skipped ambiguous group",
					"contact_number", g["contact_number"], "msg_id", g["msg_id"],
					"rows", len(rows), "attachment_ids", ids)
				continue
			}
			survivor, other := mirrors[0], others[0]

			// Never downgrade: the higher status rank wins (NULL -> 0).
			finalStatus := other["status"]
			if statusRank(survivor.str("status")) >= statusRank(other.str("status")) {
				finalStatus = survivor["status"]
			}

			survivorText := truthyString(survivor["text"])
			otherText := truthyString(other["text"])
			var finalText any
			switch {
			case (isWhatsAppSyntheticMediaText(survivorText, nil, false) || strings.TrimSpace(survivorText) == "") &&
				(strings.TrimSpace(otherText) != "" && !isWhatsAppSyntheticMediaText(otherText, nil, false)):
				finalText = otherText
			case isWhatsAppSyntheticMediaText(survivorText, nil, false):
				finalText = ""
			default:
				finalText = survivor["text"]
			}

			merged := make(map[string]any, len(idMergeFields))
			for _, f := range idMergeFields {
				merged[f] = survivor[f]
				if isMissing(merged[f]) && !isMissing(other[f]) {
					merged[f] = other[f]
				}
			}

			survivorInfo, otherInfo := survivor.str("attachment_info"), other.str("attachment_info")
			mergedKind := firstTruthy(survivor.str("media_kind"), other.str("media_kind"))
			mergedType := firstTruthy(survivor.str("msg_type"), other.str("msg_type"))
			isImage := mergedKind == "image" || mergedKind == "gif" || mergedType == "image"
			var takeOtherInfo bool
			if isImage {
				takeOtherInfo = isMissing(survivor["attachment_info"]) ||
					(isCaptionLike(otherInfo) && !isCaptionLike(survivorInfo))
			} else {
				takeOtherInfo = isMissing(survivor["attachment_info"]) ||
					(isCaptionLike(survivorInfo) && !isCaptionLike(otherInfo))
			}
			mergedInfo := survivor["attachment_info"]
			if takeOtherInfo {
				mergedInfo = other["attachment_info"]
			}

			mergedEdited := survivor.int("edited")
			if v := other.int("edited"); v > mergedEdited {
				mergedEdited = v
			}

			if _, err := tx.ExecContext(ctx,
				"UPDATE messages SET text = ?, attachment_info = ?, "+
					"status = ?, content_type = ?, media_kind = ?, "+
					"quote_text = ?, quote_timestamp = ?, quote_author = ?, "+
					"reply_to_message_id = ?, quote_attachment_id = ?, "+
					"quote_attachment_path = ?, quote_content_type = ?, "+
					"edited = ? WHERE id = ?",
				finalText, mergedInfo, finalStatus,
				merged["content_type"], merged["media_kind"], merged["quote_text"],
				merged["quote_timestamp"], merged["quote_author"], merged["reply_to_message_id"],
				merged["quote_attachment_id"], merged["quote_attachment_path"],
				merged["quote_content_type"], mergedEdited, survivor["id"]); err != nil {
				return err
			}
			s.log.Info("outgoing attachment mirror dedup",
				"contact", g["contact_number"], "msg_id", g["msg_id"],
				"survivor_id", survivor["id"], "removed_id", other["id"],
				"mirror", survivor["attachment_id"], "other", other["attachment_id"])
			if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE id = ?", other["id"]); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// firstTruthy is Python's “a or b“ on optional strings, returning "" when
// both are falsy.
func firstTruthy(a, b *string) string {
	if a != nil && *a != "" {
		return *a
	}
	return Deref(b)
}

// DedupIncomingAttachmentMirrors merges the twin WhatsApp incoming rows
// (is_mine=0) that share (contact, msg_id, attachment_id): one carrying the
// synthetic “Media: <id>“ text and one empty / repeating the filename.
// Survivor and field merge follow pickSurvivorAndMerge; real captions are
// never dropped.
func (s *Store) DedupIncomingAttachmentMirrors(ctx context.Context) (int, error) {
	removed := 0
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		groups, err := queryRowMaps(ctx, tx,
			"SELECT contact_number, msg_id, attachment_id FROM messages "+
				"WHERE protocol = 'whatsapp' AND is_mine = 0 "+
				"AND msg_id IS NOT NULL AND msg_id != '' "+
				"AND attachment_id IS NOT NULL AND attachment_id != '' "+
				"GROUP BY contact_number, msg_id, attachment_id "+
				"HAVING COUNT(*) = 2 "+
				"AND COALESCE("+
				"MAX(CASE WHEN timestamp > 0 THEN timestamp END) "+
				"- MIN(CASE WHEN timestamp > 0 THEN timestamp END), 0"+
				") <= ?", EchoMatchWindowMs)
		if err != nil {
			return err
		}
		for _, g := range groups {
			rows, err := queryRowMaps(ctx, tx,
				"SELECT id, text, sender, status, edited, timestamp, "+
					"attachment_info, content_type, "+
					"media_kind, quote_text, quote_timestamp, quote_author, "+
					"reply_to_message_id, quote_attachment_id, "+
					"quote_attachment_path, quote_content_type "+
					"FROM messages WHERE protocol = 'whatsapp' AND is_mine = 0 "+
					"AND contact_number = ? AND msg_id = ? AND attachment_id = ? "+
					"ORDER BY rowid ASC",
				g["contact_number"], g["msg_id"], g["attachment_id"])
			if err != nil {
				return err
			}
			if len(rows) != 2 {
				s.log.Warn("incoming attachment mirror dedup skipped ambiguous group",
					"contact_number", g["contact_number"], "msg_id", g["msg_id"],
					"attachment_id", g["attachment_id"], "rows", len(rows))
				continue
			}
			si, ov := pickSurvivorAndMerge(rows)
			survivor, other := rows[si], rows[1-si]
			attachmentID := g.str("attachment_id")

			var mediaFilename *string
			if v := ov.get("attachment_info"); v != nil {
				sv := truthyString(v)
				mediaFilename = &sv
			}
			survivorText := truthyString(survivor["text"])
			otherText := truthyString(other["text"])
			survivorSynth := isWhatsAppSyntheticMediaText(survivorText, attachmentID, true)
			otherSynth := isWhatsAppSyntheticMediaText(otherText, attachmentID, true)
			survivorBlank := strings.TrimSpace(survivorText) == ""
			otherBlank := strings.TrimSpace(otherText) == ""
			survivorDup := isDuplicateMediaIdentity(&survivorText, mediaFilename)
			otherDup := isDuplicateMediaIdentity(&otherText, mediaFilename)
			if !((survivorSynth && (otherBlank || otherDup)) ||
				(otherSynth && (survivorBlank || survivorDup))) {
				s.log.Debug("incoming attachment mirror dedup skipped",
					"contact_number", g["contact_number"], "msg_id", g["msg_id"])
				continue
			}
			if survivorSynth {
				ov.set("text", "")
			}
			sets := make([]string, len(ov))
			args := make([]any, 0, len(ov)+1)
			for i, o := range ov {
				sets[i] = o.col + " = ?"
				args = append(args, o.val)
			}
			args = append(args, survivor["id"])
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE messages SET %s WHERE id = ?", strings.Join(sets, ", ")),
				args...); err != nil {
				return err
			}
			s.log.Info("incoming attachment mirror dedup",
				"contact", g["contact_number"], "msg_id", g["msg_id"],
				"survivor_id", survivor["id"], "removed_id", other["id"],
				"attachment_id", g["attachment_id"])
			if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE id = ?", other["id"]); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// GhostRow is one row of a suspicious outgoing-text pair.
type GhostRow struct {
	ID        int64
	MsgID     string
	Timestamp int64
	Status    *string
}

// GhostGroup is a pair of WhatsApp outgoing text rows with identical text and
// distinct msg_ids within the echo window.  Detection only: nothing is ever
// deleted by this package.
type GhostGroup struct {
	Contact string
	Text    string
	Rows    []GhostRow
}

// DetectGhostOutgoingText reports (and logs) suspicious outgoing text pairs.
func (s *Store) DetectGhostOutgoingText(ctx context.Context) ([]GhostGroup, error) {
	const common = "FROM messages " +
		"WHERE protocol = 'whatsapp' " +
		"AND is_mine = 1 AND msg_type = 'text' " +
		"AND TRIM(COALESCE(text, '')) != '' " +
		"AND COALESCE(attachment_id, '') = '' " +
		"AND msg_id IS NOT NULL AND msg_id != '' "
	ambiguous, err := queryRowMaps(ctx, s.db,
		"SELECT contact_number, text, COUNT(*) AS cnt "+common+
			"GROUP BY contact_number, text HAVING COUNT(*) >= 3")
	if err != nil {
		return nil, err
	}
	for _, a := range ambiguous {
		s.log.Warn("ambiguous outgoing text ghost detection skipped",
			"contact", a["contact_number"], "text", a["text"], "rows", a["cnt"])
	}
	groups, err := queryRowMaps(ctx, s.db,
		"SELECT contact_number, text "+common+
			"GROUP BY contact_number, text "+
			"HAVING COUNT(*) = 2 AND COUNT(DISTINCT msg_id) = 2 "+
			"AND MAX(timestamp) - MIN(timestamp) <= ?", EchoMatchWindowMs)
	if err != nil {
		return nil, err
	}
	var detected []GhostGroup
	for _, g := range groups {
		rows, err := queryRowMaps(ctx, s.db,
			"SELECT id, msg_id, timestamp, status FROM messages "+
				"WHERE protocol = 'whatsapp' AND contact_number = ? AND text = ? "+
				"AND is_mine = 1 AND msg_type = 'text' "+
				"AND COALESCE(attachment_id, '') = '' "+
				"AND msg_id IS NOT NULL AND msg_id != '' "+
				"ORDER BY id", g["contact_number"], g["text"])
		if err != nil {
			return nil, err
		}
		if len(rows) != 2 {
			continue
		}
		grp := GhostGroup{Contact: truthyString(g["contact_number"]), Text: truthyString(g["text"])}
		for _, r := range rows {
			grp.Rows = append(grp.Rows, GhostRow{
				ID: r.int("id"), MsgID: truthyString(r["msg_id"]),
				Timestamp: r.int("timestamp"), Status: r.str("status"),
			})
		}
		detected = append(detected, grp)
		s.log.Warn("suspicious outgoing text ghost; run purge_ghost_outgoing.py --apply to remove",
			"contact", grp.Contact, "text", grp.Text, "rows", grp.Rows)
	}
	return detected, nil
}

var (
	startupMu   sync.Mutex
	startupDone = map[string]bool{}
)

// ResetStartupMaintenance forgets the per-path "maintenance done" markers
// (test helper, like Python's reset_startup_maintenance).
func ResetStartupMaintenance() {
	startupMu.Lock()
	defer startupMu.Unlock()
	startupDone = map[string]bool{}
}

// RunStartupMaintenance runs, once per database path and process, the
// idempotent dedup chain: DedupMessagesByID -> DedupOutgoingAttachmentMirrors
// -> DedupIncomingAttachmentMirrors -> DetectGhostOutgoingText.  Concurrent
// callers block until the first run completes.  force re-runs unconditionally.
// If the work fails the marker is dropped so a later call retries.
func (s *Store) RunStartupMaintenance(ctx context.Context, force bool) error {
	startupMu.Lock()
	defer startupMu.Unlock()
	if force {
		delete(startupDone, s.path)
	}
	if !force && startupDone[s.path] {
		return nil
	}
	startupDone[s.path] = true
	run := func() error {
		if _, err := s.DedupMessagesByID(ctx); err != nil {
			return err
		}
		if _, err := s.DedupOutgoingAttachmentMirrors(ctx); err != nil {
			return err
		}
		if _, err := s.DedupIncomingAttachmentMirrors(ctx); err != nil {
			return err
		}
		_, err := s.DetectGhostOutgoingText(ctx)
		return err
	}
	if err := run(); err != nil {
		delete(startupDone, s.path)
		return err
	}
	return nil
}
