package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var ctx = context.Background()

func seed(b *Backend, contact string, es ...*entry) {
	b.ingestMu.Lock()
	b.cache[contact] = append(b.cache[contact], es...)
	b.ingestMu.Unlock()
}

func cached(b *Backend, contact string) []*entry {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return append([]*entry(nil), b.cache[contact]...)
}

func dbRows(t *testing.T, e *env, contact string) []store.Message {
	t.Helper()
	m, err := e.st.LoadCache(ctx, "whatsapp")
	if err != nil {
		t.Fatal(err)
	}
	return m[contact]
}

// ---- process_receipt (tests/test_whatsapp_receipt_id_match.py) -------------------------------

func TestProcessReceiptCanonicalMatch(t *testing.T) {
	e := newEnv(t)
	id := "true_189025889575055@lid_3A268CF00E4ECCEA4474"
	if _, err := e.b.IngestMessage(ctx, "1@lid", map[string]any{"id": id, "text": "hello", "is_mine": true}, 1000); err != nil {
		t.Fatal(err)
	}
	updated, err := e.b.ProcessReceipt(ctx, "", map[string]any{"message_ids": []any{"3A268CF00E4ECCEA4474"}, "is_read": true})
	if err != nil || len(updated) != 1 || updated[0]["id"] != id || updated[0]["status"] != "read" {
		t.Fatalf("updated = %+v err=%v", updated, err)
	}
	if got := cached(e.b, "1@lid")[0].Status; got != "read" {
		t.Errorf("cache status = %q", got)
	}
	rows := dbRows(t, e, "1@lid")
	if len(rows) != 1 || store.Deref(rows[0].Status) != "read" {
		t.Errorf("status not persisted by id: %+v", rows)
	}
}

func TestProcessReceiptGroupAndSerializedIDs(t *testing.T) {
	e := newEnv(t)
	gid := "true_123456789@g.us_3EB0D9A373425F46E2A5F6_987654321@lid"
	seed(e.b, "1@g.us", &entry{ID: gid, IsMine: true, Status: "sent", Timestamp: 2000, Text: "group msg"})
	updated, _ := e.b.ProcessReceipt(ctx, "1@g.us", map[string]any{"message_ids": []string{"3EB0D9A373425F46E2A5F6"}, "is_read": false})
	if len(updated) != 1 || cached(e.b, "1@g.us")[0].Status != "delivered" {
		t.Fatalf("group receipt: %+v", updated)
	}
	// A receipt carrying the serialized id matches too.
	updated, _ = e.b.ProcessReceipt(ctx, "1@g.us", map[string]any{"message_ids": []any{gid}, "is_read": true})
	if len(updated) != 1 || cached(e.b, "1@g.us")[0].Status != "read" {
		t.Fatalf("serialized receipt: %+v", updated)
	}
}

func TestProcessReceiptRankGuard(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "1@lid", &entry{ID: "true_189025889575055@lid_3A268CF00E4ECCEA4474", IsMine: true, Status: "read", Timestamp: 1000, Text: "hello"})
	updated, _ := e.b.ProcessReceipt(ctx, "1@lid", map[string]any{"message_ids": []any{"3A268CF00E4ECCEA4474"}, "is_read": false})
	if len(updated) != 0 || cached(e.b, "1@lid")[0].Status != "read" {
		t.Fatalf("a delivered receipt must not downgrade read: %+v", updated)
	}
}

func TestProcessReceiptTwinChatFallsBackToCanonicalSearch(t *testing.T) {
	e := newEnv(t)
	// The receipt arrives for the @lid chat, the message is cached under @c.us.
	seed(e.b, "391234567890@c.us", &entry{ID: "true_391234567890@c.us_3A26", IsMine: true, Status: "sent", Timestamp: 1, Text: "x"})
	updated, _ := e.b.ProcessReceipt(ctx, "189025889575055@lid", map[string]any{"message_ids": []any{"3A26"}, "is_read": true})
	if len(updated) != 1 {
		t.Fatalf("twin receipt not matched: %+v", updated)
	}
}

func TestProcessReceiptIdlessFallback(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.IngestMessage(ctx, "1@c.us", map[string]any{"text": "racing", "is_mine": true}, 3000); err != nil {
		t.Fatal(err)
	}
	updated, _ := e.b.ProcessReceipt(ctx, "", map[string]any{"message_ids": []any{"HEXID"}, "is_read": true})
	want := map[string]any{"id": "", "timestamp": int64(3000), "status": "read", "text": "racing", "is_mine": true}
	if len(updated) != 1 {
		t.Fatalf("updated = %+v", updated)
	}
	for k, v := range want {
		if updated[0][k] != v {
			t.Errorf("updated[%q] = %#v, want %#v", k, updated[0][k], v)
		}
	}
	rows := dbRows(t, e, "1@c.us")
	if len(rows) != 1 || store.Deref(rows[0].Status) != "read" {
		t.Errorf("id-less status persisted by timestamp/text: %+v", rows)
	}
}

func TestProcessReceiptIdlessAmbiguousOrNotSent(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "1@c.us",
		&entry{IsMine: true, Status: "sent", Timestamp: 3000, Text: "a"},
		&entry{IsMine: true, Status: "sent", Timestamp: 3001, Text: "b"})
	if u, _ := e.b.ProcessReceipt(ctx, "", map[string]any{"message_ids": []any{"HEXID"}, "is_read": true}); len(u) != 0 {
		t.Error("two id-less candidates must not be matched")
	}
	e2 := newEnv(t)
	seed(e2.b, "1@c.us", &entry{IsMine: true, Status: "delivered", Timestamp: 3000, Text: "d"})
	if u, _ := e2.b.ProcessReceipt(ctx, "", map[string]any{"message_ids": []any{"HEXID"}, "is_read": true}); len(u) != 0 {
		t.Error("a non-sent id-less entry must not be matched")
	}
	if u, _ := e2.b.ProcessReceipt(ctx, "", map[string]any{"message_ids": []any{}}); u != nil {
		t.Error("empty ids")
	}
}

// ---- ingest_message ---------------------------------------------------------------------------

func TestIngestAddsAndDeduplicates(t *testing.T) {
	e := newEnv(t)
	m := map[string]any{"id": "false_39@c.us_AB12", "text": "ciao", "is_mine": false, "sender": "Mario", "msg_type": "text"}
	r, _ := e.b.IngestMessage(ctx, "39@c.us", m, 10_000)
	if r != proto.IngestAdded {
		t.Fatalf("first ingest = %v", r)
	}
	// Same id again, and the bare-hex spelling of it.
	if r, _ = e.b.IngestMessage(ctx, "39@c.us", m, 10_000); r != proto.IngestUnchanged {
		t.Errorf("same message = %v", r)
	}
	m2 := map[string]any{"id": "AB12", "text": "ciao", "is_mine": false, "msg_type": "text"}
	if r, _ = e.b.IngestMessage(ctx, "39@c.us", m2, 10_000); r != proto.IngestUnchanged {
		t.Errorf("hex spelling = %v", r)
	}
	rows := dbRows(t, e, "39@c.us")
	if len(rows) != 1 || store.Deref(rows[0].Sender) != "Mario" || store.Deref(rows[0].Status) != "read" {
		t.Errorf("rows = %+v", rows)
	}
	// Different text, different id: a new message.
	m3 := map[string]any{"id": "false_39@c.us_CD34", "text": "altro", "is_mine": false, "msg_type": "text"}
	if r, _ = e.b.IngestMessage(ctx, "39@c.us", m3, 11_000); r != proto.IngestAdded {
		t.Errorf("distinct message = %v", r)
	}
}

func TestIngestOutgoingEchoGetsTheRealID(t *testing.T) {
	e := newEnv(t)
	if r, _ := e.b.IngestMessage(ctx, "39@c.us", map[string]any{"text": "ok", "is_mine": true}, 50_000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	// The echo carries the real id; the optimistic row keeps its timestamp.
	r, _ := e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "true_39@c.us_3EB0AA", "text": "ok", "is_mine": true}, 53_000)
	if r != proto.IngestUnchanged {
		t.Errorf("echo = %v", r)
	}
	es := cached(e.b, "39@c.us")
	if len(es) != 1 || es[0].ID != "true_39@c.us_3EB0AA" {
		t.Fatalf("cache = %+v", es)
	}
	rows := dbRows(t, e, "39@c.us")
	if len(rows) != 1 || store.Deref(rows[0].MsgID) != "true_39@c.us_3EB0AA" {
		t.Errorf("id not persisted: %+v", rows)
	}
}

func TestIngestTwoQuickIdenticalOutgoingWithDifferentIDsAreDistinct(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "true_39@c.us_AA01", "text": "OK", "is_mine": true}, 1000)
	r, _ := e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "true_39@c.us_BB02", "text": "OK", "is_mine": true}, 2000)
	if r != proto.IngestAdded || len(cached(e.b, "39@c.us")) != 2 {
		t.Fatalf("OK + OK must stay two messages (r=%v)", r)
	}
}

func TestIngestHealsMediaRaceAndCaption(t *testing.T) {
	e2 := newEnv(t)
	e2.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "true_39@c.us_AB12", "is_mine": true, "msg_type": "image"}, 1000)
	r, _ := e2.b.IngestMessage(ctx, "39@c.us", map[string]any{
		"id": "true_39@c.us_AB12", "is_mine": true, "msg_type": "image", "media_kind": "image",
		"attachment_id": "true_39@c.us_AB12.jpeg", "attachment_info": "una didascalia"}, 1000)
	if r != proto.IngestChanged {
		t.Fatalf("media race heal = %v", r)
	}
	es := cached(e2.b, "39@c.us")
	if len(es) != 1 || es[0].AttachmentID != "true_39@c.us_AB12.jpeg" || es[0].AttachmentInfo != "una didascalia" {
		t.Fatalf("healed entry: %+v", es[0])
	}
	rows := dbRows(t, e2, "39@c.us")
	if store.Deref(rows[0].AttachmentID) != "true_39@c.us_AB12.jpeg" {
		t.Errorf("attachment not persisted: %+v", rows[0])
	}
}

func TestIngestFillsQuoteFields(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "false_39@c.us_Q1", "text": "risposta", "is_mine": false}, 1000)
	r, _ := e.b.IngestMessage(ctx, "39@c.us", map[string]any{
		"id": "false_39@c.us_Q1", "text": "risposta", "is_mine": false, "quote_text": "orig",
		"quote_author": "39@c.us", "reply_to_message_id": "true_39@c.us_X"}, 1000)
	_ = r
	es := cached(e.b, "39@c.us")
	if es[0].QuoteText != "orig" || es[0].ReplyToMessageID != "true_39@c.us_X" {
		t.Fatalf("quote fields: %+v", es[0])
	}
	rows := dbRows(t, e, "39@c.us")
	if store.Deref(rows[0].QuoteText) != "orig" {
		t.Errorf("quote not persisted: %+v", rows[0])
	}
}

func TestHistoryReconcileKeepsOriginalID(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "39@c.us", &entry{ID: "true_39@c.us_OLD1", IsMine: true, Text: "x", Timestamp: 1000, Status: "sent"})
	e.b.ingestMu.Lock()
	r := e.b.ingestLocked(ctx, "39@c.us", inMsg{ID: "true_39@c.us_NEW2", Text: "x", IsMine: true, MsgType: "text"}, 1500, true, true, nil)
	e.b.ingestMu.Unlock()
	if r != proto.IngestUnchanged || cached(e.b, "39@c.us")[0].ID != "true_39@c.us_OLD1" {
		t.Fatalf("history reconcile must keep the original id (r=%v)", r)
	}
}

// ---- history reconcile ghost (48c5d46) -------------------------------------------------------

// The out-of-window/remote-page guards on the "unique outgoing text" merge:
// without them an identical message from weeks before swallowed a new send.
const (
	reconcileTestTS int64 = 1_700_000_000_000
	reconcileWeek   int64 = 7 * 24 * 3600 * 1000
)

func reconcileIngest(t *testing.T, b *Backend, contact string, d inMsg, ts int64, remoteIDs map[string]bool) proto.IngestResult {
	t.Helper()
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.ingestLocked(ctx, contact, d, ts, true, true, remoteIDs)
}

func cachedIDs(b *Backend, contact string) []string {
	var ids []string
	for _, m := range cached(b, contact) {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestReconcileSameTextWeeksApartIsNewMessage(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.IngestMessage(ctx, "39@c.us", map[string]any{
		"id": "true_39@c.us_OLD1", "text": "Grazie mille", "is_mine": true}, reconcileTestTS); err != nil {
		t.Fatal(err)
	}
	r := reconcileIngest(t, e.b, "39@c.us",
		inMsg{ID: "true_39@c.us_NEW2", Text: "Grazie mille", IsMine: true, MsgType: "text"}, reconcileTestTS+4*reconcileWeek, nil)
	if r != proto.IngestAdded {
		t.Fatalf("a send weeks after an identical one is a new message (r=%v)", r)
	}
	if got := cachedIDs(e.b, "39@c.us"); len(got) != 2 || got[0] != "true_39@c.us_OLD1" || got[1] != "true_39@c.us_NEW2" {
		t.Errorf("cache ids = %v", got)
	}
	if rows := dbRows(t, e, "39@c.us"); len(rows) != 2 {
		t.Errorf("db rows = %d", len(rows))
	}
}

func TestReconcileOldAmbiguousCandidatesDoNotSkip(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "39@c.us",
		&entry{ID: "X", IsMine: true, Text: "OK", Timestamp: reconcileTestTS, Status: "sent"},
		&entry{ID: "Y", IsMine: true, Text: "OK", Timestamp: reconcileTestTS + 1000, Status: "sent"})
	r := reconcileIngest(t, e.b, "39@c.us",
		inMsg{ID: "Z", Text: "OK", IsMine: true, MsgType: "text"}, reconcileTestTS+reconcileWeek, nil)
	if r != proto.IngestAdded {
		t.Fatalf("old candidates outside the echo window must not skip the message (r=%v)", r)
	}
	if got := cachedIDs(e.b, "39@c.us"); len(got) != 3 || got[2] != "Z" {
		t.Errorf("cache ids = %v", got)
	}
}

func TestReconcileCandidatePresentInRemotePageIsDistinct(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "39@c.us", &entry{ID: "X", IsMine: true, Text: "OK", Timestamp: reconcileTestTS, Status: "sent"})
	// X is in the same remote page: it is a distinct message, not Z's ghost.
	r := reconcileIngest(t, e.b, "39@c.us",
		inMsg{ID: "Z", Text: "OK", IsMine: true, MsgType: "text"}, reconcileTestTS+2000, map[string]bool{"X": true, "Z": true})
	if r != proto.IngestAdded {
		t.Fatalf("a candidate whose id is in the remote page must not be merged (r=%v)", r)
	}
	if got := cachedIDs(e.b, "39@c.us"); len(got) != 2 || got[0] != "X" || got[1] != "Z" {
		t.Errorf("cache ids = %v", got)
	}
}

func TestReconcileEchoWithinWindowMerges(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "39@c.us", &entry{ID: "local-id", IsMine: true, Text: "from web", Timestamp: reconcileTestTS, Status: "sent"})
	r := reconcileIngest(t, e.b, "39@c.us",
		inMsg{ID: "history-hex-id", Text: "from web", IsMine: true, MsgType: "text"}, reconcileTestTS+1000,
		map[string]bool{"history-hex-id": true})
	if r != proto.IngestUnchanged {
		t.Fatalf("a recent distant-id echo of the web send must merge (r=%v)", r)
	}
	if got := cachedIDs(e.b, "39@c.us"); len(got) != 1 || got[0] != "local-id" {
		t.Errorf("cache ids = %v", got)
	}
}

// ---- edits ------------------------------------------------------------------------------------

func TestDetectEdit(t *testing.T) {
	e := newEnv(t)
	seed(e.b, "39@c.us",
		&entry{ID: "false_39@c.us_E1", Text: "uno", Timestamp: 10_000},
		&entry{ID: "true_39@c.us_E2", Text: "due", IsMine: true, Timestamp: 20_000})
	e.b.ingestMu.Lock()
	defer e.b.ingestMu.Unlock()
	if h := e.b.detectEditLocked("39@c.us", "false_39@c.us_E1", "uno modificato", false, nil); h == nil || h.Text != "uno" {
		t.Error("id match with different text is an edit")
	}
	if h := e.b.detectEditLocked("39@c.us", "false_39@c.us_E1", "  uno ", false, nil); h != nil {
		t.Error("same text (whitespace-normalised) is not an edit")
	}
	if h := e.b.detectEditLocked("39@c.us", "", "", false, nil); h != nil {
		t.Error("empty text is never an edit")
	}
	if h := e.b.detectEditLocked("39@c.us", "", "uno", false, nil); h != nil {
		t.Error("an id-less message is never an edit")
	}
	// A different id is a different message, even within the old +-2 s window
	// and with the sender's other messages right next to it.
	if h := e.b.detectEditLocked("39@c.us", "OTHER", "nuovo", false, nil); h != nil {
		t.Error("a different id must never fold into another message")
	}
	e.b.cache["39@c.us"][0].FromHistory = true
	if h := e.b.detectEditLocked("39@c.us", "OTHER", "nuovo", false, nil); h != nil {
		t.Error("history rows are matched by id only")
	}
}

func TestApplyEdit(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "false_39@c.us_E1", "text": "vecchio", "is_mine": false}, 7000)
	res, err := e.b.ApplyEdit("39@c.us", "false_39@c.us_E1", "nuovo", proto.ApplyEditOptions{MarkEdited: true})
	if err != nil || res == nil || res.OldText != "vecchio" || res.Text != "nuovo" || res.Timestamp != 7000 || res.IsMine {
		t.Fatalf("edit result: %+v err=%v", res, err)
	}
	rows := dbRows(t, e, "39@c.us")
	if store.Deref(rows[0].Text) != "nuovo" || !rows[0].Edited {
		t.Errorf("edit not persisted: %+v", rows[0])
	}
	// Idempotent echo.
	if res, _ = e.b.ApplyEdit("39@c.us", "false_39@c.us_E1", "nuovo", proto.ApplyEditOptions{MarkEdited: true}); res != nil {
		t.Error("echo of the same text must be a no-op")
	}
	// is_mine guard and unknown id.
	no := false
	_ = no
	mine := true
	if res, _ = e.b.ApplyEdit("39@c.us", "false_39@c.us_E1", "x", proto.ApplyEditOptions{IsMine: &mine, MarkEdited: true}); res != nil {
		t.Error("is_mine mismatch must not edit")
	}
	if res, _ = e.b.ApplyEdit("39@c.us", "nope", "x", proto.ApplyEditOptions{}); res != nil {
		t.Error("unknown id")
	}
	// A media row is not editable.
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "false_39@c.us_M1", "text": "", "msg_type": "image", "attachment_id": "a.jpeg"}, 8000)
	if res, _ = e.b.ApplyEdit("39@c.us", "false_39@c.us_M1", "x", proto.ApplyEditOptions{}); res != nil {
		t.Error("media rows are not editable")
	}
}

// ---- reactions --------------------------------------------------------------------------------

func TestApplyReactionAggregates(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "true_39@c.us_3EB0AA", "text": "hi", "is_mine": true}, 4000)
	react := func(target, emoji, key, author string, mine, remove bool) map[string]any {
		out, err := e.b.ApplyReaction("39@c.us", map[string]any{
			"mode": "delta", "target_message_id": target, "emoji": emoji, "author_key": key, "author": author,
			"is_mine": mine, "is_remove": remove, "timestamp": int64(5000)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := react("true_39@c.us_3EB0AA", "👍", "39@c.us", "Mario", false, false)
	if out == nil || out["message_id"] != "true_39@c.us_3EB0AA" || out["timestamp"] != int64(4000) {
		t.Fatalf("first reaction: %+v", out)
	}
	rs := out["reactions"].([]any)
	if len(rs) != 1 || rs[0].(map[string]any)["emoji"] != "👍" || rs[0].(map[string]any)["count"] != int64(1) {
		t.Fatalf("reactions: %+v", rs)
	}
	// The bare hex spelling resolves to the same message; a second author adds up.
	out = react("3EB0AA", "👍", "me", "You", true, false)
	rs = out["reactions"].([]any)
	if len(rs) != 1 || rs[0].(map[string]any)["count"] != int64(2) || rs[0].(map[string]any)["is_mine"] != true {
		t.Fatalf("aggregate: %+v", rs)
	}
	// Mario changes his mind.
	out = react("true_39@c.us_3EB0AA", "❤️", "39@c.us", "Mario", false, false)
	rs = out["reactions"].([]any)
	if len(rs) != 2 {
		t.Fatalf("after change: %+v", rs)
	}
	// Removal.
	out = react("true_39@c.us_3EB0AA", "", "39@c.us", "Mario", false, true)
	rs = out["reactions"].([]any)
	if len(rs) != 1 || rs[0].(map[string]any)["emoji"] != "👍" {
		t.Fatalf("after removal: %+v", rs)
	}
	// Unknown target: stored for later, nothing to push.
	if out = react("true_39@c.us_UNKNOWN", "👍", "39@c.us", "Mario", false, false); out != nil {
		t.Errorf("unknown target should not push: %+v", out)
	}
	// Non-delta payloads are ignored.
	if out, _ := e.b.ApplyReaction("39@c.us", map[string]any{"mode": "snapshot"}); out != nil {
		t.Error("snapshot payloads are not WhatsApp's")
	}
}

// ---- read state, media dir, mirror ------------------------------------------------------------

func TestMarkReadSendsReceiptsAndPersists(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_AA01", "text": "uno", "is_mine": false}, 1000)
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_AA02", "text": "due", "is_mine": false}, 2000)
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "true_391234567890@c.us_AA03", "text": "mio", "is_mine": true}, 3000)
	if err := e.b.MarkRead(ctx, "391234567890@c.us"); err != nil {
		t.Fatal(err)
	}
	if len(e.fc.reads) != 1 || len(e.fc.reads[0].IDs) != 2 || e.fc.reads[0].IDs[0] != "AA01" || e.fc.reads[0].IDs[1] != "AA02" ||
		e.fc.reads[0].Chat.String() != "391234567890@s.whatsapp.net" {
		t.Fatalf("read receipts: %+v", e.fc.reads)
	}
	for _, m := range dbRows(t, e, "391234567890@c.us") {
		if !m.Read {
			t.Errorf("row not persisted as read: %+v", m)
		}
	}
	// Nothing unread left: no second receipt.
	_ = e.b.MarkRead(ctx, "391234567890@c.us")
	if len(e.fc.reads) != 1 {
		t.Errorf("second mark-read sent %d receipts", len(e.fc.reads))
	}
}

func TestMarkReadGroupGroupsBySender(t *testing.T) {
	e := newEnv(t)
	g := "120363000000000000@g.us"
	e.b.IngestMessage(ctx, g, map[string]any{"id": "false_" + g + "_A1_111@lid", "text": "a", "is_mine": false}, 1000)
	e.b.IngestMessage(ctx, g, map[string]any{"id": "false_" + g + "_A2_222@lid", "text": "b", "is_mine": false}, 2000)
	_ = e.b.MarkRead(ctx, g)
	if len(e.fc.reads) != 2 {
		t.Fatalf("one receipt per sender expected: %+v", e.fc.reads)
	}
}

func TestEnsureMediaDirAndAttachmentPath(t *testing.T) {
	e := newEnv(t)
	dir, err := e.b.EnsureMediaDir()
	if err != nil || dir != e.cfg.MediaDir() {
		t.Fatalf("media dir = %q err=%v", dir, err)
	}
	if _, ok := e.b.AttachmentPath(ctx, "nope.jpeg"); ok {
		t.Error("missing file without media info")
	}
	if err := os.WriteFile(filepath.Join(dir, "false_39@c.us_AB.jpeg"), []byte("jpegdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A WAHA URL (what old rows hold) resolves to the same local file by its name.
	p, ok := e.b.AttachmentPath(ctx, "http://waha:3000/api/files/default/false_39@c.us_AB.jpeg")
	if !ok || filepath.Base(p) != "false_39@c.us_AB.jpeg" {
		t.Fatalf("path = %q ok=%v", p, ok)
	}
	if p, ok := e.b.AttachmentPath(ctx, "../../etc/passwd"); ok {
		t.Errorf("traversal resolved to %q", p)
	}
	chunk, _ := e.b.AttachmentChunk(ctx, "false_39@c.us_AB.jpeg", 2, 3)
	if string(chunk) != "egd" {
		t.Errorf("chunk = %q", chunk)
	}
	tail, _ := e.b.AttachmentChunk(ctx, "false_39@c.us_AB.jpeg", -1, 4)
	if string(tail) != "data" {
		t.Errorf("tail = %q", tail)
	}
}

func TestEnqueueSentMessageMirror(t *testing.T) {
	e := newEnv(t)
	src := filepath.Join(e.dir, "pic.png")
	os.WriteFile(src, []byte("png"), 0o644)
	idx := 1
	if err := e.b.EnqueueSentMessage(ctx, proto.SentMessage{
		ContactID: "39@c.us", MessageID: "true_39@c.us_ZZ", Text: "cap", AttachmentPath: src, MimeType: "image/png",
		Filename: "pic.png", BatchID: "b1", BatchIndex: &idx, Quote: proto.Quote{Message: "q", ReplyToMessageID: "false_39@c.us_Q"},
	}); err != nil {
		t.Fatal(err)
	}
	ev := one(t, drain(e.b))
	p := ev.Payload
	att, _ := p["attachment_id"].(string)
	if ev.Type != proto.EventMessage || p["msg_type"] != "image" || p["text"] != "" || p["attachment_info"] != "cap" ||
		p["media_kind"] != "image" || p["batch_id"] != "b1" || p["batch_index"] != int64(1) || p["quote_text"] != "q" ||
		len(att) < 5 || att[:5] != "sent-" || !isSentMirror(att) {
		t.Fatalf("mirror payload: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.MediaDir(), att)); err != nil {
		t.Errorf("mirror file not copied: %v", err)
	}
	if err := e.b.EnqueueSentNotification(ctx, "39@c.us", "x", 1, ""); err == nil {
		t.Error("sent notification is unsupported")
	}
}

func TestReconcileUnread(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "39@c.us", map[string]any{"id": "false_39@c.us_U1", "text": "u", "is_mine": false}, 1000)
	if _, err := e.st.LoadCache(ctx, "whatsapp"); err != nil {
		t.Fatal(err)
	}
	// An unread row to start with (AddMessage stores incoming rows as read=is_mine).
	if rows := dbRows(t, e, "39@c.us"); rows[0].Read {
		t.Fatal("incoming rows start unread")
	}
	e.b.cmu.Lock()
	e.b.readOnPhone["39@c.us"] = 0
	e.b.cmu.Unlock()
	e.b.ReconcileUnread(ctx)
	if rows := dbRows(t, e, "39@c.us"); !rows[0].Read {
		t.Error("a chat read on the phone must be marked read locally")
	}
}

var (
	_ BatchSlotMerger = (*store.Store)(nil)
	_ FailedRowReuser = (*store.Store)(nil)
)

func TestMergeBatchSlotWithRealStore(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.st.AddMessage(ctx, store.NewMessage{Protocol: proto.ProtocolWhatsApp, ContactNumber: "39@c.us",
		Text: "x", IsMine: true, Timestamp: 5, MsgID: store.Ptr("m1")}); err != nil {
		t.Fatal(err)
	}
	ent := &entry{ID: "m1"}
	idx := int64(1)
	if !e.b.mergeBatchSlotLocked(ctx, "39@c.us", ent, inMsg{ID: "m1", BatchID: "B", BatchIndex: &idx}) {
		t.Fatal("merge should touch the row")
	}
	if r := dbRows(t, e, "39@c.us"); len(r) != 1 || store.Deref(r[0].BatchID) != "B" {
		t.Fatalf("%+v", r)
	}
}

// bug #44: the echo of a retried send reuses the single failed id-less row.
func TestEchoReusesFailedDBRow(t *testing.T) {
	e := newEnv(t)
	c := "39@c.us"
	if _, _, err := e.st.AddMessage(ctx, store.NewMessage{Protocol: proto.ProtocolWhatsApp, ContactNumber: c,
		Text: "retry me", IsMine: true, Timestamp: 1000, Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.b.IngestMessage(ctx, c, map[string]any{"id": "W1", "text": "retry me", "is_mine": true}, 2000)
	if err != nil || res != proto.IngestUnchanged {
		t.Fatalf("res=%v err=%v", res, err)
	}
	r := dbRows(t, e, c)
	if len(r) != 1 || store.Deref(r[0].MsgID) != "W1" || store.Deref(r[0].Status) != "sent" || r[0].Timestamp != 2000 {
		t.Fatalf("db not re-keyed: %+v", r)
	}
	if ce := cached(e.b, c); len(ce) != 1 || ce[0].ID != "W1" || ce[0].Status != "sent" || ce[0].Timestamp != 2000 {
		t.Fatalf("cache not mirrored: %+v", ce)
	}
	// with no failed row the echo inserts normally
	res, _ = e.b.IngestMessage(ctx, c, map[string]any{"id": "W2", "text": "other", "is_mine": true}, 3000)
	if res != proto.IngestAdded || len(dbRows(t, e, c)) != 2 {
		t.Fatalf("res=%v", res)
	}
}
