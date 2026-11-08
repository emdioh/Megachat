package store

import (
	"fmt"
	"testing"
)

// pm inserts a compact prune fixture row.
func pm(t *testing.T, s *Store, protocol, contact string, i int, ts int64, status string, msgID *string, read int) {
	t.Helper()
	if msgID == nil && status != "" && false {
		return
	}
	insertRow(t, s, row{"protocol": protocol, "contact_number": contact, "text": fmt.Sprint("msg-", i),
		"timestamp": ts, "status": status, "msg_id": msgID, "read": read})
}

func fill(t *testing.T, s *Store, protocol, contact string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%s-%s-%d", protocol, contact, i)
		pm(t, s, protocol, contact, i, int64(i), "read", &id, 1)
	}
}

func TestPruneProtectedRowsSurviveOutsideCap(t *testing.T) {
	s := newStore(t)
	now := int64(2_000_000)
	for i := 0; i < 310; i++ {
		id := fmt.Sprint("id-", i)
		pm(t, s, "signal", "A", i, 1_000_000+int64(i), "read", &id, 1)
	}
	pm(t, s, "signal", "A", 1000, now-20, "pending", strp("pending-id"), 0)
	pm(t, s, "signal", "A", 1001, now-10, "failed", strp("failed-id"), 0)
	pm(t, s, "signal", "A", 1002, now-1, "read", nil, 0) // recent id-less

	eq(t, must(s.PruneCache(ctx, 300, now)), 10)
	eq(t, count(t, s, "1=1"), 303)
	eq(t, count(t, s, "status IN ('pending','failed') OR msg_id IS NULL"), 3)
}

func TestPruneOldIdlessRowBeyondCapIsPruned(t *testing.T) {
	s := newStore(t)
	now := int64(2_000_000)
	for i := 0; i < 100; i++ {
		id := fmt.Sprint("id-", i)
		pm(t, s, "signal", "A", i, 1_500_000+int64(i), "read", &id, 1)
	}
	pm(t, s, "signal", "A", 500, now-600_001, "read", nil, 1)
	eq(t, must(s.PruneCache(ctx, 100, now)), 1)
	eq(t, count(t, s, "text = 'msg-500'"), 0)
}

func TestPruneCapIsPerProtocolAndContact(t *testing.T) {
	s := newStore(t)
	fill(t, s, "signal", "A", 350)
	fill(t, s, "telegram", "B", 120)
	fill(t, s, "whatsapp", "C", 350)
	eq(t, must(s.PruneCache(ctx, 300, 1_000_000)), 100)
	eq(t, count(t, s, "protocol='signal' AND contact_number='A'"), 300)
	eq(t, count(t, s, "protocol='telegram' AND contact_number='B'"), 120)
	eq(t, count(t, s, "protocol='whatsapp' AND contact_number='C'"), 300)
	// the newest rows are the ones kept
	eq(t, count(t, s, "protocol='signal' AND timestamp < 50"), 0)
}

func TestPruneEqualTimestampsKeepHighestRowids(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 105; i++ {
		id := fmt.Sprint("id-", i)
		pm(t, s, "signal", "A", i, 123_456, "read", &id, 1)
	}
	first := allRows(t, s)
	eq(t, must(s.PruneCache(ctx, 100, 1_000_000)), 5)
	kept := map[int64]bool{}
	for _, r := range allRows(t, s) {
		kept[r.int("id")] = true
	}
	for _, r := range first[5:] {
		if !kept[r.int("id")] {
			t.Fatalf("row %d should have been kept", r.int("id"))
		}
	}
}

func TestPruneDisabledAndClamped(t *testing.T) {
	s := newStore(t)
	fill(t, s, "signal", "A", 130)
	eq(t, must(s.PruneCache(ctx, 0, 1_000_000)), 0)
	eq(t, must(s.PruneCache(ctx, -5, 1_000_000)), 0)
	eq(t, count(t, s, "1=1"), 130)
	// below the floor: clamped to 100
	eq(t, must(s.PruneCache(ctx, 50, 1_000_000)), 30)
	eq(t, count(t, s, "1=1"), MinPruneLimit)
}

func TestPruneDefaultsNowToClock(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 101; i++ {
		pm(t, s, "signal", "A", i, int64(i), "read", nil, 1) // id-less but ancient
	}
	eq(t, must(s.PruneCache(ctx, 100, 0)), 1)
}

func TestPruneDedupRefetchPreservesCountAndReadState(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 150; i++ {
		pm(t, s, "whatsapp", "C", i, int64(i), "read", strp(fmt.Sprint("wa-", i)), 1)
	}
	eq(t, must(s.PruneCache(ctx, 100, 1_000_000)), 50)
	for i := 100; i < 150; i++ {
		_, inserted, err := s.AddMessage(ctx, NewMessage{Protocol: "whatsapp", ContactNumber: "C", Text: fmt.Sprint("msg-", i),
			Sender: "Alice", Timestamp: int64(i), MsgID: strp(fmt.Sprint("wa-", i))})
		if err != nil || inserted {
			t.Fatalf("re-fetch of a kept message must dedup (inserted=%v err=%v)", inserted, err)
		}
	}
	eq(t, count(t, s, "1=1"), 100)
	eq(t, count(t, s, "read = 1"), 100)
}

func TestPruneRemovesReactionsOfPrunedMessages(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 101; i++ {
		addMsg(t, s, "signal", "contact", int64(i), strp(fmt.Sprint(i)))
	}
	must(s.ApplyReactionDelta(ctx, ReactionDelta{Protocol: "signal", Contact: "contact", TargetMsgID: strp("0"), TargetTimestamp: i64p(0),
		Emoji: "👍", AuthorKey: "alice", Author: "Alice", Timestamp: 1}))
	eq(t, must(s.PruneCache(ctx, 100, 1_000_000)), 1)
	eq(t, len(must(s.ReactionsForContact(ctx, "signal", "contact"))), 0)
}
