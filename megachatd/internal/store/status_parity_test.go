package store

import "testing"

func statusFixture(t *testing.T, s *Store) {
	for i, st := range []string{"pending", "failed", "sent", "delivered", "read", "weird"} {
		insertRow(t, s, row{"text": "t", "is_mine": 1, "timestamp": 100, "status": st, "msg_id": string(rune('a' + i))})
		insertRow(t, s, row{"text": "u", "is_mine": 1, "timestamp": 200 + i, "status": st, "contact_number": "d"})
	}
	insertRow(t, s, row{"text": "t", "is_mine": 0, "timestamp": 100, "status": "sent"})
	insertRow(t, s, row{"text": "t", "protocol": "telegram", "msg_id": "z", "timestamp": 100, "status": "sent"})
	insertRow(t, s, row{"text": "idl", "is_mine": 1, "timestamp": 1000, "status": "pending", "contact_number": "e"})
	insertRow(t, s, row{"text": "idl", "is_mine": 1, "timestamp": 1500, "status": "pending", "contact_number": "e", "msg_id": ""})
	insertRow(t, s, row{"text": "idl", "is_mine": 1, "timestamp": 700_000, "status": "pending", "contact_number": "e"})
}

func TestStatusUpdatesMatchPython(t *testing.T) {
	statuses := []string{"pending", "failed", "sent", "delivered", "read", "bogus"}
	for _, target := range statuses {
		t.Run("by-timestamp/"+target, func(t *testing.T) {
			parity(t, statusFixture, "update_message_status", []any{100, target, "signal", "c"},
				func(s *Store) any { return must(s.UpdateMessageStatus(ctx, 100, target, "signal", "c", nil, nil)) })
		})
		t.Run("by-timestamp-expected/"+target, func(t *testing.T) {
			parity(t, statusFixture, "update_message_status",
				map[string]any{"args": []any{100, target, "signal", "c"}, "kwargs": map[string]any{"text": "t", "expected_statuses": []string{"pending", "sent"}}},
				func(s *Store) any {
					return must(s.UpdateMessageStatus(ctx, 100, target, "signal", "c", strp("t"), []string{"pending", "sent"}))
				})
		})
		t.Run("by-id/"+target, func(t *testing.T) {
			parity(t, statusFixture, "update_message_status_by_id", []any{"c", target, "signal"},
				func(s *Store) any { return must(s.UpdateMessageStatusByID(ctx, "c", target, "signal", nil)) })
		})
		t.Run("by-text/"+target, func(t *testing.T) {
			parity(t, statusFixture, "update_message_status_by_text", []any{"u", target, "signal", "d"},
				func(s *Store) any { return must(s.UpdateMessageStatusByText(ctx, "u", target, "signal", "d", nil)) })
		})
	}
}

func TestMessageIDUpdateMatchesPython(t *testing.T) {
	for _, ts := range []int64{1000, 1400, 650_000, 700_000, 5_000_000} {
		parity(t, statusFixture, "update_message_id", []any{"e", "idl", true, ts, "real"},
			func(s *Store) any { return must(s.UpdateMessageID(ctx, "signal", "e", "idl", true, ts, "real")) })
	}
}

func TestMarkAsReadMatchesPython(t *testing.T) {
	parity(t, statusFixture, "mark_as_read", []any{"d", "signal", []string{"e", ""}},
		func(s *Store) any { check(s.MarkAsRead(ctx, "signal", "d", "e", "")); return nil })
}

func TestDedupExactMatchesPython(t *testing.T) {
	parity(t, func(t *testing.T, s *Store) {
		for i := 0; i < 3; i++ {
			insertRow(t, s, row{"text": "x", "timestamp": 5})
		}
		insertRow(t, s, row{"text": "x", "timestamp": 5, "is_mine": 1})
		insertRow(t, s, row{"text": nil, "timestamp": 6})
		insertRow(t, s, row{"text": nil, "timestamp": 6})
	}, "dedup_messages", nil, func(s *Store) any { return must(s.DedupMessages(ctx)) })
}

func TestAttachmentUpdatesMatchPython(t *testing.T) {
	setup := func(t *testing.T, s *Store) {
		insertRow(t, s, row{"msg_id": "m", "attachment_id": "sent-1.png", "timestamp": 100})
		insertRow(t, s, row{"msg_id": "m", "attachment_id": "sent-2.png", "timestamp": 100})
		insertRow(t, s, row{"msg_id": "m", "attachment_id": nil, "timestamp": 200, "msg_type": "text"})
	}
	parity(t, setup, "update_message_attachment_id", []any{"signal", "c", "m", 100, "real", "sent-2.png"},
		func(s *Store) any {
			return must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "m", 100, "real", strp("sent-2.png")))
		})
	parity(t, setup, "update_message_attachment_id", []any{"signal", "c", "m", 190, "real"},
		func(s *Store) any {
			return must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "m", 190, "real", nil))
		})
	parity(t, setup, "update_message_attachment_info", []any{"signal", "c", "m", 190, "info"},
		func(s *Store) any { return must(s.UpdateMessageAttachmentInfo(ctx, "signal", "c", "m", 190, "info")) })
	parity(t, setup, "update_message_media_identity", []any{"signal", "c", "m", 190, "doc.pdf", "attachment", "document"},
		func(s *Store) any {
			return must(s.UpdateMessageMediaIdentity(ctx, "signal", "c", "m", 190, "doc.pdf", "attachment", strp("document")))
		})
	parity(t, setup, "fill_message_quote_fields", map[string]any{"args": []any{"signal", "c", "m", 100},
		"kwargs": map[string]any{"quote_text": "q", "quote_timestamp": 3, "reply_to_message_id": "r"}},
		func(s *Store) any {
			return must(s.FillMessageQuoteFields(ctx, "signal", "c", "m", 100, QuoteFill{QuoteText: strp("q"), QuoteTimestamp: i64p(3), ReplyToMessageID: strp("r")}))
		})
}

func TestUpdateTextMatchesPython(t *testing.T) {
	setup := func(t *testing.T, s *Store) {
		insertRow(t, s, row{"msg_id": "m", "text": "old", "timestamp": 100, "is_mine": 1})
		insertRow(t, s, row{"text": "old", "timestamp": 100, "contact_number": "z"})
	}
	parity(t, setup, "update_message_text",
		map[string]any{"args": []any{"c", "new", "signal"}, "kwargs": map[string]any{"msg_id": "m", "old_text": "old", "is_mine": true}},
		func(s *Store) any {
			return must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "signal", ContactNumber: "c", NewText: "new", MsgID: strp("m"), OldText: strp("old"), IsMine: Ptr(true)}))
		})
	parity(t, setup, "update_message_text",
		map[string]any{"args": []any{"z", "new", "signal"}, "kwargs": map[string]any{"timestamp": 100, "mark_edited": false}},
		func(s *Store) any {
			return must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "signal", ContactNumber: "z", NewText: "new", Timestamp: i64p(100), ClearEdited: true}))
		})
}
