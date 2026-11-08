package store

import (
	"testing"
)

func addMsg(t *testing.T, s *Store, protocol, contact string, ts int64, msgID *string) int64 {
	t.Helper()
	return add(t, s, NewMessage{Protocol: protocol, ContactNumber: contact, Text: "message", Sender: "Alice", Timestamp: ts, MsgID: msgID})
}

func delta(s *Store, mod func(d *ReactionDelta)) (bool, error) {
	d := ReactionDelta{Protocol: "signal", Contact: "contact", TargetMsgID: strp("1000"), TargetTimestamp: i64p(1000),
		Emoji: "👍", AuthorKey: "alice", Author: "Alice", Timestamp: 2000}
	if mod != nil {
		mod(&d)
	}
	return s.ApplyReactionDelta(ctx, d)
}

func TestReactionSchemaIsIdempotent(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 2; i++ {
		s2, err := Open(s.Path(), quiet)
		if err != nil {
			t.Fatal(err)
		}
		_ = s2.Close()
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='reactions' AND name IN ('idx_reactions_identity','idx_reactions_contact')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	eq(t, n, 2)
}

func TestReactionDeltaAddChangeRemove(t *testing.T) {
	s := newStore(t)
	eq(t, must(delta(s, nil)), true)
	rows := must(s.ReactionsForContact(ctx, "signal", "contact"))
	eq(t, len(rows), 1)
	eq(t, rows[0].Emoji, "👍")
	eq(t, rows[0].Count, int64(1))
	eq(t, rows[0].Author, "Alice")
	eq(t, rows[0].IsMine, false)

	// changing the emoji replaces the author's row (unique identity)
	eq(t, must(delta(s, func(d *ReactionDelta) { d.Emoji = "❤️"; d.Timestamp = 2001 })), true)
	rows = must(s.ReactionsForContact(ctx, "signal", "contact"))
	eq(t, len(rows), 1)
	eq(t, rows[0].Emoji, "❤️")

	// removal ignores the emoji and reports whether something was removed
	eq(t, must(delta(s, func(d *ReactionDelta) { d.Emoji = "ignored"; d.IsRemove = true })), true)
	eq(t, len(must(s.ReactionsForContact(ctx, "signal", "contact"))), 0)
	eq(t, must(delta(s, func(d *ReactionDelta) { d.IsRemove = true })), false)
}

func TestReactionDeltaAuthorsAreIndependent(t *testing.T) {
	s := newStore(t)
	must(delta(s, nil))
	must(delta(s, func(d *ReactionDelta) { d.AuthorKey = "bob"; d.Author = "Bob"; d.IsMine = true }))
	must(delta(s, func(d *ReactionDelta) { d.TargetMsgID = strp("1001"); d.TargetTimestamp = i64p(1001) }))
	rows := must(s.ReactionsForContact(ctx, "signal", "contact"))
	eq(t, len(rows), 3)
	eq(t, rows[1].IsMine, true)
	// ordered by id
	if !(rows[0].ID < rows[1].ID && rows[1].ID < rows[2].ID) {
		t.Fatal("not ordered by id")
	}
}

func TestReactionNullTargetsShareIdentity(t *testing.T) {
	s := newStore(t)
	// NULL target_msg_id / target_timestamp are treated as ''/0 by the identity.
	must(delta(s, func(d *ReactionDelta) { d.Protocol = "telegram"; d.TargetMsgID = strp("42"); d.TargetTimestamp = nil }))
	must(delta(s, func(d *ReactionDelta) {
		d.Protocol = "telegram"
		d.TargetMsgID = strp("42")
		d.TargetTimestamp = nil
		d.Emoji = "🔥"
	}))
	rows := must(s.ReactionsForContact(ctx, "telegram", "contact"))
	eq(t, len(rows), 1)
	eq(t, rows[0].Emoji, "🔥")
	if rows[0].TargetTimestamp != nil {
		t.Fatal("target_timestamp must stay NULL")
	}
	// '' and NULL msg ids are the same target
	must(delta(s, func(d *ReactionDelta) { d.Protocol = "signal"; d.TargetMsgID = nil; d.TargetTimestamp = i64p(5) }))
	must(delta(s, func(d *ReactionDelta) {
		d.Protocol = "signal"
		d.TargetMsgID = strp("")
		d.TargetTimestamp = i64p(5)
		d.Emoji = "x"
	}))
	eq(t, len(must(s.ReactionsForContact(ctx, "signal", "contact"))), 1)
}

func aggEntries() []ReactionEntry {
	return []ReactionEntry{
		{Emoji: "👍", Count: 3, IsMine: true, AuthorKey: "__agg__:👍"},
		{Emoji: "❤️", Count: 2, AuthorKey: "__agg__:❤️"},
	}
}

func TestReactionSnapshotReplacePreservesDeltaRows(t *testing.T) {
	s := newStore(t)
	must(delta(s, func(d *ReactionDelta) { d.Protocol = "telegram"; d.TargetMsgID = strp("42"); d.TargetTimestamp = nil }))
	eq(t, must(s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, aggEntries(), 3000)), true)

	agg := func() map[string]int64 {
		out := map[string]int64{}
		for _, r := range must(s.ReactionsForContact(ctx, "telegram", "contact")) {
			if len(r.AuthorKey) >= 8 && r.AuthorKey[:8] == "__agg__:" {
				out[r.AuthorKey] = r.Count
			}
		}
		return out
	}
	got := agg()
	eq(t, len(got), 2)
	eq(t, got["__agg__:👍"], int64(3))
	eq(t, got["__agg__:❤️"], int64(2))

	eq(t, must(s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, aggEntries()[1:], 3001)), true)
	got = agg()
	eq(t, len(got), 1)
	eq(t, got["__agg__:❤️"], int64(2))
	found := false
	for _, r := range must(s.ReactionsForContact(ctx, "telegram", "contact")) {
		found = found || r.AuthorKey == "alice"
	}
	if !found {
		t.Fatal("per-author delta row was removed by a snapshot")
	}
	// an empty snapshot clears the aggregates and reports it
	eq(t, must(s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, nil, 3002)), true)
	eq(t, len(agg()), 0)
	eq(t, must(s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, nil, 3003)), false)
}

func TestReactionSnapshotFailureRollsBack(t *testing.T) {
	s := newStore(t)
	must(s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, aggEntries(), 1))
	dup := []ReactionEntry{{Emoji: "a", Count: 1, AuthorKey: "__agg__:x"}, {Emoji: "b", Count: 1, AuthorKey: "__agg__:x"}}
	if _, err := s.ReplaceReactionsSnapshot(ctx, "telegram", "contact", strp("42"), nil, dup, 2); err == nil {
		t.Fatal("expected a unique-identity violation")
	}
	eq(t, len(must(s.ReactionsForContact(ctx, "telegram", "contact"))), 2) // old snapshot intact
}

func TestResolveReactionTargetPerProtocol(t *testing.T) {
	s := newStore(t)
	signalID := addMsg(t, s, "signal", "+39", 1000, strp("1000"))
	signalLegacy := addMsg(t, s, "signal", "+39", 2000, nil)
	waID := addMsg(t, s, "whatsapp", "wa", 3000, strp("wa-id"))
	tgID := addMsg(t, s, "telegram", "tg", 4000, strp("44"))

	r := must(s.ResolveReactionTargetRow(ctx, "signal", "+39", strp("1000"), i64p(999)))
	if r == nil {
		t.Fatal("nil")
	}
	eq(t, r.ID, signalID)
	eq(t, *r.MsgID, "1000")
	eq(t, r.Timestamp, int64(1000))

	r = must(s.ResolveReactionTargetRow(ctx, "signal", "+39", nil, i64p(2000)))
	if r == nil || r.ID != signalLegacy || r.MsgID != nil {
		t.Fatalf("legacy id-less signal row: %+v", r)
	}
	r = must(s.ResolveReactionTargetRow(ctx, "whatsapp", "wa", strp("wa-id"), i64p(0)))
	if r == nil || r.ID != waID {
		t.Fatalf("whatsapp: %+v", r)
	}
	r = must(s.ResolveReactionTargetRow(ctx, "telegram", "tg", strp("44"), nil))
	if r == nil || r.ID != tgID || r.Timestamp != 4000 {
		t.Fatalf("telegram: %+v", r)
	}
	if must(s.ResolveReactionTargetRow(ctx, "signal", "+39", strp("missing"), i64p(9999))) != nil {
		t.Fatal("expected nil for unknown target")
	}
	// non-signal protocols never fall back to the timestamp
	if must(s.ResolveReactionTargetRow(ctx, "telegram", "tg", nil, i64p(4000))) != nil {
		t.Fatal("telegram must not resolve by timestamp")
	}
}

func TestResolveReactionTargetSignalPrefersMsgIDMatch(t *testing.T) {
	s := newStore(t)
	addMsg(t, s, "signal", "c", 500, nil)
	byID := addMsg(t, s, "signal", "c", 600, strp("X"))
	r := must(s.ResolveReactionTargetRow(ctx, "signal", "c", strp("X"), i64p(500)))
	if r == nil || r.ID != byID {
		t.Fatalf("msg_id match must win over timestamp match: %+v", r)
	}
}

func TestPruneOrphanReactionsKeepsExistingTargets(t *testing.T) {
	s := newStore(t)
	for _, m := range []struct {
		ts int64
		id *string
	}{{1000, strp("1000")}, {1001, strp("1001")}, {1002, nil}, {1003, nil}} {
		addMsg(t, s, "signal", "contact", m.ts, m.id)
	}
	for _, c := range []struct {
		id  string
		ts  int64
		key string
	}{{"1000", 1000, "first"}, {"1001", 1001, "second"}, {"1002", 1002, "idless"}, {"1003", 1003, "deleted-idless"}} {
		must(delta(s, func(d *ReactionDelta) {
			d.TargetMsgID = strp(c.id)
			d.TargetTimestamp = i64p(c.ts)
			d.AuthorKey = c.key
		}))
	}
	mustExec(t, s, "DELETE FROM messages WHERE msg_id = '1000'")
	mustExec(t, s, "DELETE FROM messages WHERE timestamp = 1003")

	eq(t, must(s.PruneOrphanReactions(ctx)), 2)
	var keys []string
	for _, r := range must(s.ReactionsForContact(ctx, "signal", "contact")) {
		keys = append(keys, r.AuthorKey)
	}
	eq(t, len(keys), 2)
	eq(t, keys[0], "second")
	eq(t, keys[1], "idless")
}
