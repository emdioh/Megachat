package store

import (
	"fmt"
	"testing"
)

type dedupKind int

const (
	byID dedupKind = iota
	outgoing
	incoming
)

func (k dedupKind) py() string {
	return [...]string{"dedup_messages_by_id", "dedup_outgoing_attachment_mirrors", "dedup_incoming_attachment_mirrors"}[k]
}

func (k dedupKind) run(s *Store) (int, error) {
	switch k {
	case byID:
		return s.DedupMessagesByID(ctx)
	case outgoing:
		return s.DedupOutgoingAttachmentMirrors(ctx)
	}
	return s.DedupIncomingAttachmentMirrors(ctx)
}

type dedupCase struct {
	name string
	kind dedupKind
	rows []row
	want int // rows removed
	// check inspects the surviving rows (in rowid order).
	check func(t *testing.T, rows []rowMap)
}

func wa(r row) row {
	out := row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "M1"}
	for k, v := range r {
		out[k] = v
	}
	return out
}

func waOut(r row) row { r["is_mine"] = 1; return wa(r) }

const wahaURL = "https://waha.local/files/doc.pdf"

func dedupCases() []dedupCase {
	only := func(t *testing.T, rows []rowMap) rowMap {
		t.Helper()
		if len(rows) != 1 {
			t.Fatalf("want 1 surviving row, got %d: %v", len(rows), rows)
		}
		return rows[0]
	}
	return []dedupCase{
		// ---- DedupMessagesByID --------------------------------------------
		{name: "byid/sent-placeholder-and-read-empty-merge", kind: byID, want: 1, rows: []row{
			{"msg_id": "m", "text": "t", "status": "sent", "quote_text": "Q", "quote_timestamp": 5, "content_type": "image/png", "timestamp": 10},
			{"msg_id": "m", "text": "t", "status": "read", "quote_text": "", "edited": 1, "timestamp": 12},
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("status"), "read")
			eq(t, *r.str("quote_text"), "Q")
			eq(t, r.int("quote_timestamp"), 5)
			eq(t, *r.str("content_type"), "image/png")
			eq(t, r.int("edited"), 1)
			eq(t, r.int("timestamp"), 12)
		}},
		{name: "byid/never-overwrites-survivor-values", kind: byID, want: 1, rows: []row{
			{"msg_id": "m", "text": "t", "status": "read", "quote_text": "mine", "media_kind": "image"},
			{"msg_id": "m", "text": "t", "status": "sent", "quote_text": "theirs", "media_kind": "gif", "quote_author": "A"},
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("quote_text"), "mine")
			eq(t, *r.str("media_kind"), "image")
			eq(t, *r.str("quote_author"), "A")
		}},
		{name: "byid/same-status-keeps-lowest-rowid", kind: byID, want: 1, rows: []row{
			{"msg_id": "m", "text": "t", "status": "sent", "sender": "first"},
			{"msg_id": "m", "text": "t", "status": "sent", "sender": "second", "quote_author": "Z"},
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("sender"), "first")
			eq(t, *r.str("quote_author"), "Z")
		}},
		{name: "byid/divergent-timestamps-not-merged", kind: byID, want: 0, rows: []row{
			{"msg_id": "m", "text": "t", "timestamp": 1000},
			{"msg_id": "m", "text": "t", "timestamp": 1000 + 600_001},
		}},
		{name: "byid/window-boundary-merges", kind: byID, want: 1, rows: []row{
			{"msg_id": "m", "text": "t", "timestamp": 1000},
			{"msg_id": "m", "text": "t", "timestamp": 1000 + 600_000},
		}},
		{name: "byid/multi-attachment-batch-not-collapsed", kind: byID, want: 0, rows: []row{
			{"msg_id": "m", "text": "", "attachment_id": "a1", "batch_id": "b", "batch_index": 0},
			{"msg_id": "m", "text": "", "attachment_id": "a2", "batch_id": "b", "batch_index": 1},
		}},
		{name: "byid/idless-and-empty-id-rows-ignored", kind: byID, want: 0, rows: []row{
			{"text": "t"}, {"text": "t"}, {"text": "t", "msg_id": ""}, {"text": "t", "msg_id": ""},
		}},
		{name: "byid/null-vs-empty-attachment-are-distinct", kind: byID, want: 0, rows: []row{
			{"msg_id": "m", "text": "t"}, {"msg_id": "m", "text": "t", "attachment_id": ""},
		}},
		{name: "byid/null-text-partition", kind: byID, want: 1, rows: []row{
			{"msg_id": "m", "text": nil, "status": "sent"}, {"msg_id": "m", "text": nil, "status": "read"},
		}},
		{name: "byid/three-way-edited-max", kind: byID, want: 2, rows: []row{
			{"msg_id": "m", "text": "t", "status": "pending", "edited": 0},
			{"msg_id": "m", "text": "t", "status": "delivered", "edited": 1},
			{"msg_id": "m", "text": "t", "status": "sent", "edited": 0, "quote_text": "q"},
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("status"), "delivered")
			eq(t, r.int("edited"), 1)
			eq(t, *r.str("quote_text"), "q")
		}},

		// ---- DedupOutgoingAttachmentMirrors --------------------------------
		{name: "out/wahaURL-and-sent-mirror-fuse", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "Media: " + wahaURL, "timestamp": 100, "status": "sent", "msg_type": "attachment"}),
			waOut(row{"attachment_id": "sent-abc.pdf", "text": "", "timestamp": 200, "status": "sent", "msg_type": "attachment", "attachment_info": "doc.pdf"}),
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("attachment_id"), "sent-abc.pdf")
			eq(t, *r.str("text"), "")
			eq(t, r.int("timestamp"), 200) // survivor identity kept
		}},
		{name: "out/keeps-highest-status", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "Media: " + wahaURL, "status": "read"}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "", "status": "sent", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("status"), "read") }},
		{name: "out/null-status-does-not-downgrade", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "Media: " + wahaURL, "status": "delivered"}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "", "status": nil, "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("status"), "delivered") }},
		{name: "out/multiword-caption-preserved", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "Media: " + wahaURL}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "Media: bella foto", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("text"), "Media: bella foto") }},
		{name: "out/synthetic-wahaURL-text-cleared", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": ""}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "Media: http://x/y", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("text"), "") }},
		{name: "out/real-caption-transferred-from-other", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "ecco il file"}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("text"), "ecco il file") }},
		{name: "out/survivor-real-caption-wins", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "other caption"}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "my caption", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("text"), "my caption") }},
		{name: "out/filename-wins-over-caption-info-nonimage", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "", "attachment_info": "doc.pdf", "msg_type": "attachment"}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "", "attachment_info": "una didascalia lunga", "msg_type": "attachment", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("attachment_info"), "doc.pdf") }},
		{name: "out/image-keeps-caption-info", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": "http://x/i.jpg", "text": "", "attachment_info": "una didascalia", "media_kind": "image", "msg_type": "image"}),
			waOut(row{"attachment_id": "sent-i.jpg", "text": "", "attachment_info": "i.jpg", "media_kind": "image", "msg_type": "image", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("attachment_info"), "una didascalia") }},
		{name: "out/preserves-batch-quote-edited", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": "", "quote_text": "Q", "quote_author": "A", "edited": 1, "batch_id": "bx", "batch_index": 7}),
			waOut(row{"attachment_id": "sent-a.pdf", "text": "", "timestamp": 2, "batch_id": "b1", "batch_index": 0}),
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("quote_text"), "Q")
			eq(t, r.int("edited"), 1)
			eq(t, *r.str("batch_id"), "b1") // slot stays the survivor's
			eq(t, r.int("batch_index"), 0)
		}},
		{name: "out/path-form-mirror", kind: outgoing, want: 1, rows: []row{
			waOut(row{"attachment_id": wahaURL, "text": ""}),
			waOut(row{"attachment_id": "/cache/whatsapp-media/SENT-9.pdf", "text": "", "timestamp": 2}),
		}},
		{name: "out/ambiguous-three-rows-skipped", kind: outgoing, want: 0, rows: []row{
			waOut(row{"attachment_id": wahaURL}), waOut(row{"attachment_id": "sent-a.pdf"}), waOut(row{"attachment_id": "sent-b.pdf"}),
		}},
		{name: "out/two-wahaURL-rows-skipped", kind: outgoing, want: 0, rows: []row{
			waOut(row{"attachment_id": wahaURL}), waOut(row{"attachment_id": wahaURL + "?2"}),
		}},
		{name: "out/two-distinct-sent-mirrors-not-merged", kind: outgoing, want: 0, rows: []row{
			waOut(row{"attachment_id": "sent-a.pdf"}), waOut(row{"attachment_id": "sent-b.pdf"}),
		}},
		{name: "out/outside-window-not-fused", kind: outgoing, want: 0, rows: []row{
			waOut(row{"attachment_id": wahaURL, "timestamp": 1}), waOut(row{"attachment_id": "sent-a.pdf", "timestamp": 1 + 600_001}),
		}},
		{name: "out/signal-multi-attachment-untouched", kind: outgoing, want: 0, rows: []row{
			{"is_mine": 1, "msg_id": "S", "attachment_id": "a1"}, {"is_mine": 1, "msg_id": "S", "attachment_id": "sent-a2"},
		}},
		{name: "out/incoming-untouched", kind: outgoing, want: 0, rows: []row{
			wa(row{"attachment_id": wahaURL}), wa(row{"attachment_id": "sent-a.pdf"}),
		}},

		// ---- DedupIncomingAttachmentMirrors --------------------------------
		{name: "in/synthetic-and-blank-fuse", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "voice.ogg", "text": "Media: voice.ogg", "sender": "1234@lid", "timestamp": 10}),
			wa(row{"attachment_id": "voice.ogg", "text": "", "sender": "Alice", "timestamp": 11, "status": "read"}),
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("sender"), "Alice")
			eq(t, *r.str("text"), "")
		}},
		{name: "in/synthetic-survivor-text-cleared", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "v.ogg", "text": "Media: v.ogg", "status": "read", "timestamp": 10}),
			wa(row{"attachment_id": "v.ogg", "text": "", "status": "sent", "timestamp": 11}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("text"), "") }},
		{name: "in/status-no-downgrade-and-read-flag-of-survivor", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "v.ogg", "text": "Media: v.ogg", "status": "sent", "read": 1}),
			wa(row{"attachment_id": "v.ogg", "text": "", "status": "delivered", "read": 0, "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("status"), "delivered")
			eq(t, r.int("read"), 0) // read is immutable on the survivor
		}},
		{name: "in/real-caption-pair-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "ciao a tutti"}), wa(row{"attachment_id": "p.jpg", "text": "altro testo"}),
		}},
		{name: "in/two-synthetic-rows-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg"}), wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg"}),
		}},
		{name: "in/outside-window-not-fused", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg", "timestamp": 1}),
			wa(row{"attachment_id": "p.jpg", "text": "", "timestamp": 1 + 600_001}),
		}},
		{name: "in/three-rows-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg"}), wa(row{"attachment_id": "p.jpg", "text": ""}), wa(row{"attachment_id": "p.jpg", "text": ""}),
		}},
		{name: "in/distinct-attachment-ids-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "a.jpg", "text": "Media: a.jpg"}), wa(row{"attachment_id": "b.jpg", "text": ""}),
		}},
		{name: "in/merges-quote-fields-and-edited", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg", "quote_text": "Q", "reply_to_message_id": "R", "edited": 1, "status": "sent"}),
			wa(row{"attachment_id": "p.jpg", "text": "", "quote_author": "Bob", "status": "read", "timestamp": 3}),
		}, check: func(t *testing.T, rows []rowMap) {
			r := only(t, rows)
			eq(t, *r.str("quote_text"), "Q")
			eq(t, *r.str("quote_author"), "Bob")
			eq(t, *r.str("reply_to_message_id"), "R")
			eq(t, r.int("edited"), 1)
		}},
		{name: "in/ts-zero-twin-fused-and-takes-real-ts", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg", "timestamp": 0, "status": "read"}),
			wa(row{"attachment_id": "p.jpg", "text": "", "timestamp": 5_000_000, "status": "sent"}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, only(t, rows).int("timestamp"), 5_000_000) }},
		{name: "in/both-ts-zero-fused", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "p.jpg", "text": "Media: p.jpg", "timestamp": 0}),
			wa(row{"attachment_id": "p.jpg", "text": "", "timestamp": 0}),
		}},
		{name: "in/synthetic-and-filename-twin-fused", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "doc.pdf", "text": "Media: doc.pdf", "attachment_info": "doc.pdf"}),
			wa(row{"attachment_id": "doc.pdf", "text": "doc.pdf", "timestamp": 2}),
		}},
		{name: "in/synthetic-and-wahaURL-caption-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "doc.pdf", "text": "Media: doc.pdf"}),
			wa(row{"attachment_id": "doc.pdf", "text": "https://example.com/page", "timestamp": 2}),
		}},
		{name: "in/media-wahaURL-caption-without-attachment-match-skipped", kind: incoming, want: 0, rows: []row{
			wa(row{"attachment_id": "doc.pdf", "text": "Media: https://x.test/other"}),
			wa(row{"attachment_id": "doc.pdf", "text": "", "timestamp": 2}),
		}},
		{name: "in/attachment-info-merged", kind: incoming, want: 1, rows: []row{
			wa(row{"attachment_id": "doc.pdf", "text": "Media: doc.pdf", "attachment_info": nil, "status": "read"}),
			wa(row{"attachment_id": "doc.pdf", "text": "", "attachment_info": "Contratto.pdf", "timestamp": 2}),
		}, check: func(t *testing.T, rows []rowMap) { eq(t, *only(t, rows).str("attachment_info"), "Contratto.pdf") }},
		{name: "in/outgoing-and-other-protocols-untouched", kind: incoming, want: 0, rows: []row{
			waOut(row{"attachment_id": "p.jpg", "text": "Media: p.jpg"}), waOut(row{"attachment_id": "p.jpg", "text": ""}),
			{"protocol": "telegram", "msg_id": "T", "attachment_id": "p.jpg", "text": "Media: p.jpg"},
			{"protocol": "telegram", "msg_id": "T", "attachment_id": "p.jpg", "text": ""},
		}},
	}
}

func TestDedupCases(t *testing.T) {
	for _, c := range dedupCases() {
		t.Run(c.name, func(t *testing.T) {
			var removed int
			s := parity(t,
				func(t *testing.T, s *Store) {
					for _, r := range c.rows {
						insertRow(t, s, r)
					}
				},
				c.kind.py(), nil,
				func(s *Store) any { removed = must(c.kind.run(s)); return removed })
			eq(t, removed, c.want)
			if c.check != nil {
				c.check(t, allRows(t, s))
			}
			// idempotent: a second run changes nothing.
			before := allRows(t, s)
			eq(t, must(c.kind.run(s)), 0)
			if fmt.Sprint(before) != fmt.Sprint(allRows(t, s)) {
				t.Fatal("second run modified rows")
			}
		})
	}
}

func TestRunStartupMaintenanceOnceAndForce(t *testing.T) {
	ResetStartupMaintenance()
	s := newStore(t)
	dup := func() {
		insertRow(t, s, row{"msg_id": "m", "text": "t", "status": "sent"})
		insertRow(t, s, row{"msg_id": "m", "text": "t", "status": "read"})
	}
	dup()
	check(s.RunStartupMaintenance(ctx, false))
	eq(t, count(t, s, "1=1"), 1)
	dup()
	insertRow(t, s, row{"msg_id": "m2", "text": "t", "status": "sent"})
	check(s.RunStartupMaintenance(ctx, false)) // already done for this path: no-op
	eq(t, count(t, s, "msg_id = 'm'"), 3)
	check(s.RunStartupMaintenance(ctx, true))
	eq(t, count(t, s, "msg_id = 'm'"), 1)
	ResetStartupMaintenance()
}

func TestRunStartupMaintenanceRunsAllPasses(t *testing.T) {
	ResetStartupMaintenance()
	s := newStore(t)
	insertRow(t, s, wa(row{"is_mine": 1, "msg_id": "o", "attachment_id": wahaURL, "text": ""}))
	insertRow(t, s, wa(row{"is_mine": 1, "msg_id": "o", "attachment_id": "sent-x.pdf", "text": "", "timestamp": 2}))
	insertRow(t, s, wa(row{"msg_id": "i", "attachment_id": "v.ogg", "text": "Media: v.ogg"}))
	insertRow(t, s, wa(row{"msg_id": "i", "attachment_id": "v.ogg", "text": "", "timestamp": 2}))
	check(s.RunStartupMaintenance(ctx, false))
	eq(t, count(t, s, "1=1"), 2)
	ResetStartupMaintenance()
}

func TestRunStartupMaintenanceConcurrentCallersRunOnce(t *testing.T) {
	ResetStartupMaintenance()
	defer ResetStartupMaintenance()
	s := newStore(t)
	insertRow(t, s, row{"msg_id": "m", "text": "t", "status": "sent"})
	insertRow(t, s, row{"msg_id": "m", "text": "t", "status": "read"})
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() { errs <- s.RunStartupMaintenance(ctx, false) }()
	}
	for i := 0; i < 6; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	eq(t, count(t, s, "1=1"), 1)
}

func TestRunStartupMaintenanceFailureDropsMarker(t *testing.T) {
	ResetStartupMaintenance()
	defer ResetStartupMaintenance()
	s := newStore(t)
	mustExec(t, s, "DROP TABLE messages")
	if err := s.RunStartupMaintenance(ctx, false); err == nil {
		t.Fatal("expected an error")
	}
	// the marker was removed, so a retry runs again (and fails again) instead
	// of silently reporting success.
	if err := s.RunStartupMaintenance(ctx, false); err == nil {
		t.Fatal("expected the retry to run and fail")
	}
}

func TestDetectGhostOutgoingText(t *testing.T) {
	s := newStore(t)
	g := func(msgID string, ts int64, extra row) {
		r := wa(row{"is_mine": 1, "msg_type": "text", "text": "ciao", "msg_id": msgID, "timestamp": ts, "status": "sent"})
		for k, v := range extra {
			r[k] = v
		}
		insertRow(t, s, r)
	}
	g("A", 1000, nil)
	g("B", 2000, nil)
	// 3 identical rows: ambiguous, only logged
	for i := 0; i < 3; i++ {
		insertRow(t, s, wa(row{"is_mine": 1, "msg_type": "text", "text": "tre", "msg_id": fmt.Sprint("t", i)}))
	}
	// same msg_id twice: not a ghost pair
	insertRow(t, s, wa(row{"is_mine": 1, "msg_type": "text", "text": "dup", "msg_id": "D"}))
	insertRow(t, s, wa(row{"is_mine": 1, "msg_type": "text", "text": "dup", "msg_id": "D", "timestamp": 2}))
	// telegram pair: ignored
	insertRow(t, s, row{"protocol": "telegram", "is_mine": 1, "msg_type": "text", "text": "ciao", "msg_id": "X"})
	insertRow(t, s, row{"protocol": "telegram", "is_mine": 1, "msg_type": "text", "text": "ciao", "msg_id": "Y"})

	got := must(s.DetectGhostOutgoingText(ctx))
	if len(got) != 1 {
		t.Fatalf("ghost groups = %+v", got)
	}
	eq(t, got[0].Contact, "w")
	eq(t, got[0].Text, "ciao")
	eq(t, len(got[0].Rows), 2)
	eq(t, got[0].Rows[0].MsgID, "A")
	eq(t, got[0].Rows[1].MsgID, "B")
	eq(t, *got[0].Rows[0].Status, "sent")
	eq(t, count(t, s, "1=1"), 2+3+2+2) // detection never deletes

	// outside the window the pair is no longer suspicious
	mustExec(t, s, "UPDATE messages SET timestamp = timestamp + 700000 WHERE msg_id = 'B'")
	eq(t, len(must(s.DetectGhostOutgoingText(ctx))), 0)
}
