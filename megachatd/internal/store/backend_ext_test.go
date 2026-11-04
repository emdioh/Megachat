package store

import (
	"context"
	"testing"
)

func TestSetBatchSlotAndDeleteMirrorRows(t *testing.T) {
	s := newStore(t)
	a := insertRow(t, s, row{"msg_id": "m", "attachment_id": "a1"})
	b := insertRow(t, s, row{"msg_id": "m", "attachment_id": "a2"})
	c := insertRow(t, s, row{"msg_id": "other", "attachment_id": "a1"})
	if err := s.SetBatchSlot(ctx, b, strp("B"), 1); err != nil {
		t.Fatal(err)
	}
	r := rowByID(t, s, b)
	eq(t, *r.str("batch_id"), "B")
	eq(t, r.int("batch_index"), 1)
	if rowByID(t, s, a).str("batch_id") != nil {
		t.Fatal("other row stamped")
	}
	if err := s.DeleteMirrorRows(ctx, "signal", "c", "m", nil); err != nil {
		t.Fatal(err)
	}
	eq(t, count(t, s, "1=1"), 3)
	if err := s.DeleteMirrorRows(ctx, "signal", "c", "m", []string{"a1", "zz"}); err != nil {
		t.Fatal(err)
	}
	eq(t, count(t, s, "id = ?", a), 0)
	eq(t, count(t, s, "id = ?", b), 1)
	eq(t, count(t, s, "id = ?", c), 1)
}

func TestMergeBatchSlot(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "telegram", "msg_id": "m"})
	done := insertRow(t, s, row{"protocol": "telegram", "msg_id": "m2", "batch_id": "old", "batch_index": 0})
	ok, err := s.MergeBatchSlot(ctx, "telegram", "c", "m", "B", i64p(2))
	if err != nil || !ok {
		t.Fatalf("merge: %v %v", ok, err)
	}
	eq(t, count(t, s, "batch_id = 'B' AND batch_index = 2"), 1)
	// already batched / unknown id / other protocol: no row touched
	for _, mid := range []string{"m", "m2", "nope"} {
		ok, err = s.MergeBatchSlot(ctx, "telegram", "c", mid, "X", nil)
		if err != nil || ok {
			t.Fatalf("%s: %v %v", mid, ok, err)
		}
	}
	ok, _ = s.MergeBatchSlot(ctx, "whatsapp", "c", "m", "X", nil)
	if ok {
		t.Fatal("protocol must scope")
	}
	eq(t, *rowByID(t, s, done).str("batch_id"), "old")
	// nil index stores NULL
	insertRow(t, s, row{"protocol": "telegram", "msg_id": "n"})
	if ok, _ := s.MergeBatchSlot(ctx, "telegram", "c", "n", "Y", nil); !ok {
		t.Fatal("expected touch")
	}
	eq(t, count(t, s, "batch_id = 'Y' AND batch_index IS NULL"), 1)
}

func TestReuseFailedRow(t *testing.T) {
	s := newStore(t)
	base := row{"protocol": "whatsapp", "is_mine": 1, "text": "hello", "status": "failed", "timestamp": 1000}
	with := func(k string, v any) row {
		r := row{}
		for kk, vv := range base {
			r[kk] = vv
		}
		r[k] = v
		return r
	}
	id := insertRow(t, s, base) // msg_id NULL
	got, err := s.ReuseFailedRow(ctx, "whatsapp", "c", "hello", 5000, "W1", "sent")
	if err != nil || got == nil || got.Timestamp != 5000 {
		t.Fatalf("reuse: %+v %v", got, err)
	}
	r := rowByID(t, s, id)
	eq(t, *r.str("msg_id"), "W1")
	eq(t, *r.str("status"), "sent")
	eq(t, r.int("timestamp"), 5000)

	// legacy '' id keeps its timestamp
	id2 := insertRow(t, s, with("msg_id", "").merge(row{"text": "legacy"}))
	got, _ = s.ReuseFailedRow(ctx, "whatsapp", "c", "legacy", 2000, "W2", "sent")
	if got == nil || got.Timestamp != 1000 {
		t.Fatalf("legacy: %+v", got)
	}
	eq(t, r2(t, s, id2), int64(1000))

	// ambiguous (two candidates), outside window and non-failed: untouched
	insertRow(t, s, with("text", "dup"))
	insertRow(t, s, with("text", "dup"))
	if got, _ := s.ReuseFailedRow(ctx, "whatsapp", "c", "dup", 1000, "W3", "sent"); got != nil {
		t.Fatal("ambiguous must not reuse")
	}
	insertRow(t, s, with("text", "far"))
	if got, _ := s.ReuseFailedRow(ctx, "whatsapp", "c", "far", 1000+EchoMatchWindowMs+1, "W4", "sent"); got != nil {
		t.Fatal("outside window")
	}
	insertRow(t, s, with("text", "ok").merge(row{"status": "sent"}))
	if got, _ := s.ReuseFailedRow(ctx, "whatsapp", "c", "ok", 1000, "W5", "sent"); got != nil {
		t.Fatal("non-failed")
	}
	eq(t, count(t, s, "msg_id = 'W3' OR msg_id = 'W4' OR msg_id = 'W5'"), 0)
}

func (r row) merge(o row) row {
	for k, v := range o {
		r[k] = v
	}
	return r
}

func r2(t *testing.T, s *Store, id int64) int64 { return rowByID(t, s, id).int("timestamp") }

// compile-time checks of the backend extension interfaces.
var (
	_ interface {
		SetBatchSlot(context.Context, int64, *string, int64) error
		DeleteMirrorRows(context.Context, string, string, string, []string) error
	} = (*Store)(nil)
	_ interface {
		MergeBatchSlot(context.Context, string, string, string, string, *int64) (bool, error)
	} = (*Store)(nil)
)
