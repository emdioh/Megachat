package signal

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func msg(text string, mine bool, extra map[string]any) map[string]any {
	m := map[string]any{"text": text, "is_mine": mine, "sender": "x", "msg_type": "text",
		"quote_text": nil, "attachment_info": nil, "attachment_id": nil}
	if mine {
		m["sender"] = "You"
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func rows(t *testing.T, h *harness, contact string) []store.Message {
	t.Helper()
	r, err := h.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{contact}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ingest(t *testing.T, h *harness, c string, m map[string]any, ts int64) proto.IngestResult {
	t.Helper()
	r, err := h.b.IngestMessage(bg, c, m, ts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIngestIncomingIdempotent(t *testing.T) {
	h := envBackend(t)
	if r := ingest(t, h, "+1", msg("hi", false, nil), 1000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	// same message again, and a re-delivery with a slightly different ts
	if r := ingest(t, h, "+1", msg("hi", false, nil), 1000); r != proto.IngestUnchanged {
		t.Fatal(r)
	}
	if r := ingest(t, h, "+1", msg("hi", false, nil), 2500); r != proto.IngestUnchanged {
		t.Fatal(r)
	}
	if r := ingest(t, h, "+1", msg("hi", false, nil), 4000); r != proto.IngestAdded {
		t.Fatal("outside the 2 s window is a new message")
	}
	if n := len(rows(t, h, "+1")); n != 2 {
		t.Fatal(n)
	}
}

func TestEchoUpgradesOptimisticTwin(t *testing.T) {
	h := envBackend(t)
	// optimistic row (no id), as the sent-mirror produces before the echo
	if r := ingest(t, h, "+1", msg("hello", true, nil), 1000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	// the sync echo carries the real id and a slightly different timestamp
	if r := ingest(t, h, "+1", msg("hello", true, map[string]any{"id": "1003"}), 1003); r != proto.IngestUnchanged {
		t.Fatalf("upgrade without changes reports unchanged, got %v", r)
	}
	got := rows(t, h, "+1")
	if len(got) != 1 || store.Deref(got[0].MsgID) != "1003" || got[0].Timestamp != 1000 {
		t.Fatalf("id attached, optimistic ts kept: %+v", got)
	}
	// second echo is idempotent
	ingest(t, h, "+1", msg("hello", true, map[string]any{"id": "1003"}), 1003)
	if n := len(rows(t, h, "+1")); n != 1 {
		t.Fatal(n)
	}
}

func TestSyncSentWithoutTwinAddsRowWithID(t *testing.T) {
	h := envBackend(t)
	if r := ingest(t, h, "+1", msg("from phone", true, map[string]any{"id": "5000"}), 5000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	got := rows(t, h, "+1")
	if len(got) != 1 || store.Deref(got[0].MsgID) != "5000" || got[0].Status == nil || *got[0].Status != "sent" {
		t.Fatalf("%+v", got)
	}
}

func TestTwoIdenticalConfirmedTextsNotMerged(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("ok", true, map[string]any{"id": "100"}), 100)
	ingest(t, h, "+1", msg("ok", true, map[string]any{"id": "200"}), 200)
	if n := len(rows(t, h, "+1")); n != 2 {
		t.Fatal(n)
	}
}

func TestEchoPicksClosestIdlessRow(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("same", true, nil), 1000)
	ingest(t, h, "+1", msg("same", true, nil), 30000) // beyond the 5 s send-dedup window
	ingest(t, h, "+1", msg("same", true, map[string]any{"id": "30001"}), 30001)
	got := rows(t, h, "+1")
	if len(got) != 2 || got[0].MsgID != nil || store.Deref(got[1].MsgID) != "30001" {
		t.Fatalf("%+v", got)
	}
}

func TestOutgoingMultiAttachmentSameIDNotCollapsed(t *testing.T) {
	h := envBackend(t)
	dir := h.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(dir, "a1.png"), "x")
	writeFile(t, filepath.Join(dir, "a2.png"), "y")
	for i, id := range []string{"a1.png", "a2.png"} {
		m := msg("", true, map[string]any{"id": "7000", "msg_type": "image", "attachment_id": id, "attachment_info": "cap"})
		if r := ingest(t, h, "+1", m, 7000); r != proto.IngestAdded {
			t.Fatalf("attachment %d: %v", i, r)
		}
	}
	if n := len(rows(t, h, "+1")); n != 2 {
		t.Fatal(n)
	}
}

func TestExistingRowGetsMissingQuoteFields(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("reply", true, map[string]any{"id": "900"}), 900)
	enriched := msg("reply", true, map[string]any{"id": "900", "quote_text": "orig", "quote_timestamp": int64(800), "quote_author": "+39"})
	if r := ingest(t, h, "+1", enriched, 900); r != proto.IngestChanged {
		t.Fatalf("got %v", r)
	}
	got := rows(t, h, "+1")
	if len(got) != 1 || store.Deref(got[0].QuoteText) != "orig" || got[0].QuoteTimestamp == nil || *got[0].QuoteTimestamp != 800 || store.Deref(got[0].QuoteAuthor) != "+39" {
		t.Fatalf("%+v", got)
	}
	// idempotent, and never overwrites
	if r := ingest(t, h, "+1", enriched, 900); r != proto.IngestUnchanged {
		t.Fatal(r)
	}
	other := msg("reply", true, map[string]any{"id": "900", "quote_text": "DIFFERENT"})
	ingest(t, h, "+1", other, 900)
	if store.Deref(rows(t, h, "+1")[0].QuoteText) != "orig" {
		t.Fatal("quote text overwritten")
	}
}

func TestMirrorToRemoteAttachmentUpgrade(t *testing.T) {
	h := envBackend(t)
	dir := h.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(dir, "sent-abc.png"), "local")
	writeFile(t, filepath.Join(dir, "remote-id"), "remote")
	// mirror row with the local "sent-" copy and the real id
	m := msg("", true, map[string]any{"id": "4000", "msg_type": "image", "attachment_id": "sent-abc.png", "attachment_info": "photo.png"})
	if r := ingest(t, h, "+1", m, 4000); r != proto.IngestAdded {
		t.Fatal(r)
	}
	// sync echo: same message, signal-cli's attachment id, real caption
	echo := msg("", true, map[string]any{"id": "4000", "msg_type": "image", "attachment_id": "remote-id", "attachment_info": "a real caption"})
	r := ingest(t, h, "+1", echo, 4000)
	got := rows(t, h, "+1")
	// the legacy "sent-" copy still exists on disk, so no upgrade; the caption heals
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	if r != proto.IngestChanged || store.Deref(got[0].AttachmentInfo) != "a real caption" {
		t.Fatalf("r=%v %+v", r, got[0])
	}
	// once the local copy is gone the remote id replaces it
	if err := os.Remove(filepath.Join(dir, "sent-abc.png")); err != nil {
		t.Fatal(err)
	}
	if r := ingest(t, h, "+1", echo, 4000); r != proto.IngestChanged {
		t.Fatal(r)
	}
	if got := rows(t, h, "+1"); store.Deref(got[0].AttachmentID) != "remote-id" {
		t.Fatalf("%+v", got[0])
	}
}

func TestOutgoingMissingAttachmentFileIsDropped(t *testing.T) {
	h := envBackend(t)
	m := msg("", true, map[string]any{"id": "1", "msg_type": "image", "attachment_id": "ghost.png"})
	ingest(t, h, "+1", m, 1)
	if got := rows(t, h, "+1"); got[0].AttachmentID != nil {
		t.Fatalf("%+v", got[0])
	}
}

func TestConnectLoadsCacheForDedup(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "+391234567890", Text: "old", Sender: "M", Timestamp: 500}); err != nil {
		t.Fatal(err)
	}
	h.connect(t)
	cs, _ := h.b.ListContacts(bg)
	if cs[0].LastMessageTS() != 500 {
		t.Fatalf("last_message_ts should come from the store: %+v", cs[0].Extras)
	}
	if r := ingest(t, h, "+391234567890", msg("old", false, nil), 500); r != proto.IngestUnchanged {
		t.Fatal("cached message must dedup:", r)
	}
}

func TestProcessReceipt(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("a", true, map[string]any{"id": "1000"}), 1000)
	ingest(t, h, "+1", msg("b", false, nil), 1500) // incoming never updated
	pl := func(m map[string]any) map[string]any { return map[string]any{"receipt": m} }
	ups, err := h.b.ProcessReceipt(bg, "+1", pl(map[string]any{"isDelivery": true, "timestamps": []any{int64(2100)}}))
	if err != nil || len(ups) != 0 {
		t.Fatal("outside the 1 s tolerance:", ups, err)
	}
	ups, _ = h.b.ProcessReceipt(bg, "+1", pl(map[string]any{"isDelivery": true, "timestamps": []any{int64(1900)}}))
	if len(ups) != 1 || ups[0]["status"] != "delivered" || ups[0]["id"] != "1000" {
		t.Fatalf("%v", ups)
	}
	if got := rows(t, h, "+1"); store.Deref(got[0].Status) != "delivered" {
		t.Fatal(*got[0].Status)
	}
	// read supersedes delivered; a later delivered receipt cannot downgrade
	ups, _ = h.b.ProcessReceipt(bg, "+1", pl(map[string]any{"isRead": true, "isDelivery": true, "timestamps": []any{int64(1000)}}))
	if len(ups) != 1 || ups[0]["status"] != "read" {
		t.Fatalf("%v", ups)
	}
	if ups, _ = h.b.ProcessReceipt(bg, "+1", pl(map[string]any{"isDelivery": true, "timestamps": []any{int64(1000)}})); len(ups) != 0 {
		t.Fatal("downgrade")
	}
	// neither delivery nor read (e.g. viewed) / no timestamps / unknown contact
	for _, p := range []map[string]any{pl(map[string]any{"timestamps": []any{int64(1000)}}), pl(map[string]any{"isRead": true}), {}} {
		if ups, _ := h.b.ProcessReceipt(bg, "+1", p); len(ups) != 0 {
			t.Fatal(ups)
		}
	}
	if ups, _ := h.b.ProcessReceipt(bg, "+nobody", pl(map[string]any{"isRead": true, "timestamps": []any{int64(1000)}})); len(ups) != 0 {
		t.Fatal(ups)
	}
}

func TestApplyEdit(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("first", true, map[string]any{"id": "1000"}), 990) // optimistic ts differs from the id
	ingest(t, h, "+1", msg("pic", false, map[string]any{"msg_type": "image", "attachment_id": "x"}), 2000)
	mine := true
	res, err := h.b.ApplyEdit("+1", "1000", "second", proto.ApplyEditOptions{IsMine: &mine, MarkEdited: true})
	if err != nil || res == nil || res.OldText != "first" || res.Text != "second" || res.Timestamp != 990 || !res.IsMine || res.MessageID != "1000" {
		t.Fatalf("%+v %v", res, err)
	}
	got := rows(t, h, "+1")
	if store.Deref(got[0].Text) != "second" || !got[0].Edited {
		t.Fatalf("%+v", got[0])
	}
	// identical text -> nil (idempotent echo of our own edit)
	if res, _ := h.b.ApplyEdit("+1", "1000", "second", proto.ApplyEditOptions{MarkEdited: true}); res != nil {
		t.Fatal("idempotent")
	}
	// is_mine mismatch, unknown, non numeric, media
	no := false
	if res, _ := h.b.ApplyEdit("+1", "1000", "x", proto.ApplyEditOptions{IsMine: &no, MarkEdited: true}); res != nil {
		t.Fatal("is_mine mismatch")
	}
	if res, _ := h.b.ApplyEdit("+1", "424242", "x", proto.ApplyEditOptions{MarkEdited: true}); res != nil {
		t.Fatal("unknown")
	}
	if res, _ := h.b.ApplyEdit("+1", "zzz", "x", proto.ApplyEditOptions{MarkEdited: true}); res != nil {
		t.Fatal("non numeric")
	}
	if res, _ := h.b.ApplyEdit("+1", strconv.Itoa(2000), "x", proto.ApplyEditOptions{MarkEdited: true}); res != nil {
		t.Fatal("media rows are never rewritten")
	}
}

func TestApplyEditIdlessRowMatchesTimestamp(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("old", true, nil), 1234)
	res, _ := h.b.ApplyEdit("+1", "1234", "new", proto.ApplyEditOptions{MarkEdited: true})
	if res == nil || res.Timestamp != 1234 {
		t.Fatalf("%+v", res)
	}
}

func TestApplyReaction(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("target", true, map[string]any{"id": "1000"}), 1000)
	delta := func(emoji, key string, remove bool, ts int64) map[string]any {
		return map[string]any{"mode": "delta", "target_message_id": "1000", "target_timestamp": int64(1000),
			"emoji": emoji, "is_remove": remove, "author": "Name " + key, "author_key": key, "is_mine": key == "me", "timestamp": ts}
	}
	info, err := h.b.ApplyReaction("+1", delta("👍", "+391", false, 10))
	if err != nil || info == nil || info["message_id"] != "1000" || info["timestamp"] != int64(1000) {
		t.Fatalf("%v %v", info, err)
	}
	rs := info["reactions"].([]map[string]any)
	if len(rs) != 1 || rs[0]["emoji"] != "👍" || rs[0]["count"] != int64(1) || rs[0]["is_mine"] != false {
		t.Fatalf("%v", rs)
	}
	// second author, same emoji -> count 2, authors sorted
	info, _ = h.b.ApplyReaction("+1", delta("👍", "me", false, 20))
	rs = info["reactions"].([]map[string]any)
	if rs[0]["count"] != int64(2) || rs[0]["is_mine"] != true {
		t.Fatalf("%v", rs)
	}
	if a := rs[0]["authors"].([]string); len(a) != 2 || a[0] != "Name +391" || a[1] != "Name me" {
		t.Fatalf("%v", a)
	}
	// change +391's reaction -> two emojis, ordered by count desc then first ts
	info, _ = h.b.ApplyReaction("+1", delta("❤️", "+391", false, 30))
	rs = info["reactions"].([]map[string]any)
	if len(rs) != 2 || rs[0]["emoji"] != "👍" || rs[1]["emoji"] != "❤️" {
		t.Fatalf("%v", rs)
	}
	// removal of a reaction that does not exist is a no-op
	if info, _ = h.b.ApplyReaction("+1", delta("", "+999", true, 40)); info != nil {
		t.Fatal("noop remove")
	}
	info, _ = h.b.ApplyReaction("+1", delta("", "me", true, 50))
	if rs = info["reactions"].([]map[string]any); len(rs) != 1 || rs[0]["emoji"] != "❤️" || rs[0]["count"] != int64(1) {
		t.Fatalf("%v", rs)
	}
	// unknown target: persisted, no aggregate
	unknown := delta("👍", "+391", false, 60)
	unknown["target_message_id"], unknown["target_timestamp"] = "555", int64(555)
	if info, _ = h.b.ApplyReaction("+1", unknown); info != nil {
		t.Fatal("unknown target")
	}
	// non-delta mode is ignored
	snap := delta("👍", "+391", false, 70)
	snap["mode"] = "snapshot"
	if info, _ = h.b.ApplyReaction("+1", snap); info != nil {
		t.Fatal("snapshot")
	}
}

func TestApplyReactionIdlessTargetByTimestamp(t *testing.T) {
	h := envBackend(t)
	ingest(t, h, "+1", msg("target", false, nil), 777)
	info, err := h.b.ApplyReaction("+1", map[string]any{"mode": "delta", "target_message_id": "777", "target_timestamp": int64(777),
		"emoji": "😀", "author": "A", "author_key": "+39", "timestamp": int64(1)})
	if err != nil || info == nil || info["message_id"] != "777" {
		t.Fatalf("%v %v", info, err)
	}
}

func TestAttachmentPath(t *testing.T) {
	h := newHarness(t)
	dir := h.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(dir, "good.bin"), "x")
	writeFile(t, filepath.Join(h.cfg.dir, "secret.txt"), "s")
	if p, ok := h.b.AttachmentPath(bg, "good.bin"); !ok || p != filepath.Join(dir, "good.bin") {
		t.Fatal(p, ok)
	}
	// directory components are stripped by the first lookup
	if _, ok := h.b.AttachmentPath(bg, "sub/good.bin"); !ok {
		t.Fatal("basename lookup")
	}
	for _, bad := range []string{"", "missing", "../secret.txt", filepath.Join(h.cfg.dir, "secret.txt"), "."} {
		if p, ok := h.b.AttachmentPath(bg, bad); ok {
			t.Errorf("%q must not resolve (%q)", bad, p)
		}
	}
}

func TestProfilePhoto(t *testing.T) {
	h := newHarness(t)
	av := h.cfg.SignalCLIAvatarsDir()
	png := "\x89PNG\r\n\x1a\nrest"
	writeFile(t, filepath.Join(av, "contact-+391234567890"), "\xff\xd8\xffjpeg-thumb")
	data, ct, err := h.b.ProfilePhoto(bg, "+391234567890")
	if err != nil || ct != "image/jpeg" || string(data) != "\xff\xd8\xffjpeg-thumb" {
		t.Fatal(string(data), ct, err)
	}
	// the profile photo wins over the contact thumbnail
	writeFile(t, filepath.Join(av, "profile-+391234567890"), png)
	if _, ct, _ := h.b.ProfilePhoto(bg, "+391234567890"); ct != "image/png" {
		t.Fatal(ct)
	}
	// bare digits for + ids
	writeFile(t, filepath.Join(av, "contact-4444"), "RIFFxxxxWEBPyy")
	if _, ct, _ := h.b.ProfilePhoto(bg, "+4444"); ct != "image/webp" {
		t.Fatal(ct)
	}
	// ACI uuid resolved through the address-book phone
	h.b.setContacts([]proto.Contact{contact("uuid-1", "U", "uuid-1").WithExtra("phone", "5555")})
	writeFile(t, filepath.Join(av, "contact-+5555"), "\xff\xd8\xffu")
	if d, _, _ := h.b.ProfilePhoto(bg, "uuid-1"); string(d) != "\xff\xd8\xffu" {
		t.Fatal(string(d))
	}
	// unsafe ids, oversized files, no picture
	writeFile(t, filepath.Join(h.cfg.dir, "contact-x"), "secret")
	for _, id := range []string{"", "../contact-x", "a/b", "nope"} {
		if d, _, _ := h.b.ProfilePhoto(bg, id); len(d) != 0 {
			t.Errorf("%q -> %q", id, d)
		}
	}
	big := make([]byte, maxAvatarBytes+1)
	if err := os.WriteFile(filepath.Join(av, "contact-+6666"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := h.b.ProfilePhoto(bg, "+6666"); len(d) != 0 {
		t.Fatal("oversized avatar")
	}
}
