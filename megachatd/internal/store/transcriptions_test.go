package store

import (
	"testing"
	"time"
)

func TestTranscriptionGetMissing(t *testing.T) {
	s := newStore(t)
	if must(s.GetTranscription(ctx, "signal", "missing")) != nil {
		t.Fatal("expected nil")
	}
}

func TestTranscriptionSetThenGet(t *testing.T) {
	now := time.Unix(1_700_000_000, 500_000_000)
	s, err := Open(t.TempDir()+"/m.db", quiet, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check(s.SetTranscription(ctx, Transcription{Protocol: "signal", AttachmentID: "audio-1", Status: "ok", Text: strp("ciao"), Model: strp("gpt-transcribe")}))
	r := must(s.GetTranscription(ctx, "signal", "audio-1"))
	if r == nil {
		t.Fatal("nil")
	}
	eq(t, r.Status, "ok")
	eq(t, *r.Text, "ciao")
	eq(t, *r.Model, "gpt-transcribe")
	if r.Error != nil {
		t.Fatal("error must be NULL")
	}
	eq(t, r.UpdatedAt, 1_700_000_000.5)
}

func TestTranscriptionUpsertNoDuplicate(t *testing.T) {
	s := newStore(t)
	check(s.SetTranscription(ctx, Transcription{Protocol: "telegram", AttachmentID: "audio-2", Status: "pending", Text: strp("partial")}))
	check(s.SetTranscription(ctx, Transcription{Protocol: "telegram", AttachmentID: "audio-2", Status: "failed", Error: strp("boom"), Model: strp("whisper-1")}))
	r := must(s.GetTranscription(ctx, "telegram", "audio-2"))
	eq(t, r.Status, "failed")
	eq(t, *r.Error, "boom")
	eq(t, *r.Model, "whisper-1")
	if r.Text != nil {
		t.Fatal("upsert must replace every column (text back to NULL)")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM transcriptions WHERE protocol='telegram' AND attachment_id='audio-2'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	eq(t, n, 1)
	// same attachment id under another protocol is a different key
	check(s.SetTranscription(ctx, Transcription{Protocol: "signal", AttachmentID: "audio-2", Status: "ok"}))
	eq(t, must(s.GetTranscription(ctx, "signal", "audio-2")).Status, "ok")
	eq(t, must(s.GetTranscription(ctx, "telegram", "audio-2")).Status, "failed")
}

func TestTranscriptionTableSchema(t *testing.T) {
	s := newStore(t)
	rows, err := queryRowMaps(ctx, s.db, "PRAGMA table_info(transcriptions)")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[*r.str("name")] = true
	}
	for _, c := range []string{"protocol", "attachment_id", "status", "text", "error", "model", "updated_at"} {
		if !got[c] {
			t.Errorf("missing column %s", c)
		}
	}
	eq(t, len(got), 7)
	eq(t, must(s.SchemaVersion(ctx)), 5)
}

func TestRecentTranscriptionsOrderAndLimit(t *testing.T) {
	tick := int64(0)
	s, err := Open(t.TempDir()+"/m.db", quiet, WithClock(func() time.Time { tick++; return time.Unix(1000+tick, 0) }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"a", "b", "c"} {
		check(s.SetTranscription(ctx, Transcription{Protocol: "signal", AttachmentID: id, Status: "ok"}))
	}
	check(s.SetTranscription(ctx, Transcription{Protocol: "signal", AttachmentID: "a", Status: "ok", Text: strp("again")}))
	got := must(s.RecentTranscriptions(ctx, 2))
	eq(t, len(got), 2)
	eq(t, got[0].AttachmentID, "a")
	eq(t, got[1].AttachmentID, "c")
	eq(t, len(must(s.RecentTranscriptions(ctx, 10))), 3)
}

func TestTranscriptionStoreReadableByPython(t *testing.T) {
	requirePython(t)
	s := newStore(t)
	check(s.SetTranscription(ctx, Transcription{Protocol: "signal", AttachmentID: "x", Status: "ok", Text: strp("hi")}))
	_ = s.Close()
	py, err := pyRun("dump", s.path)
	if err != nil {
		t.Fatal(err)
	}
	rows := py.Tables["transcriptions"]
	eq(t, len(rows), 1)
	eq(t, rows[0]["text"], any("hi"))
}
