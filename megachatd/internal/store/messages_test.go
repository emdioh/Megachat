package store

import (
	"fmt"
	"sync"
	"testing"
)

func add(t *testing.T, s *Store, m NewMessage) int64 {
	t.Helper()
	id, _, err := s.AddMessage(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddMessageDefaultsAndDedup(t *testing.T) {
	s := newStore(t)
	id1, ins1, err := s.AddMessage(ctx, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Sender: "me", Timestamp: 100})
	if err != nil || !ins1 {
		t.Fatalf("first insert: %v %v", ins1, err)
	}
	r := rowByID(t, s, id1)
	eq(t, *r.str("protocol"), "signal")
	eq(t, *r.str("msg_type"), "text")
	eq(t, *r.str("status"), "sent")
	eq(t, r.int("read"), 1) // read = is_mine
	eq(t, r.int("is_mine"), 1)

	// Same identity again: returns the existing id, nothing inserted.
	id2, ins2, err := s.AddMessage(ctx, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Sender: "other", Timestamp: 100})
	if err != nil || ins2 || id2 != id1 {
		t.Fatalf("dup: id=%d ins=%v err=%v", id2, ins2, err)
	}

	// Incoming defaults: status read, read=0.
	id3 := add(t, s, NewMessage{ContactNumber: "+39", Text: "yo", Sender: "A", Timestamp: 101})
	r = rowByID(t, s, id3)
	eq(t, *r.str("status"), "read")
	eq(t, r.int("read"), 0)

	// A different msg_id or attachment_id is a different message; NULLs compare equal.
	add(t, s, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Timestamp: 100, MsgID: strp("m1")})
	add(t, s, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Timestamp: 100, MsgID: strp("m1")})
	add(t, s, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Timestamp: 100, MsgID: strp("m1"), AttachmentID: strp("a1")})
	add(t, s, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Timestamp: 100, MsgID: strp("m1"), AttachmentID: strp("a1")})
	add(t, s, NewMessage{ContactNumber: "+39", Text: "hi", IsMine: true, Timestamp: 100, MsgID: strp("m1"), AttachmentID: strp("a2")})
	eq(t, count(t, s, "text = 'hi'"), 4)
}

func TestAddMessageExplicitStatusAndBatch(t *testing.T) {
	s := newStore(t)
	id := add(t, s, NewMessage{ContactNumber: "c", Text: "x", IsMine: true, Timestamp: 1, Status: "pending",
		BatchID: strp("b1"), BatchIndex: i64p(2), QuoteAttachmentPath: strp("/p/q.png"), MediaKind: strp("image")})
	r := rowByID(t, s, id)
	eq(t, *r.str("status"), "pending")
	eq(t, *r.str("batch_id"), "b1")
	eq(t, r.int("batch_index"), 2)
	eq(t, *r.str("quote_attachment_path"), "/p/q.png")
	// batch slot is not part of the dedup key
	id2, ins, _ := s.AddMessage(ctx, NewMessage{ContactNumber: "c", Text: "x", IsMine: true, Timestamp: 1, BatchID: strp("b2"), BatchIndex: i64p(3)})
	if ins || id2 != id {
		t.Fatal("batch slot must not affect dedup")
	}
}

func TestLoadCacheOrderingFiltersAndFlags(t *testing.T) {
	s := newStore(t)
	add(t, s, NewMessage{Protocol: "telegram", ContactNumber: "42", Text: "reply", IsMine: true, Sender: "You", Timestamp: 100,
		MsgID: strp("99"), QuoteText: strp("original"), QuoteTimestamp: i64p(90), QuoteAuthor: strp("42"), ReplyToMessageID: strp("12")})
	add(t, s, NewMessage{Protocol: "signal", ContactNumber: "A", Text: "later", Timestamp: 300})
	add(t, s, NewMessage{Protocol: "signal", ContactNumber: "A", Text: "earlier", Timestamp: 200})
	// same timestamp -> insertion (id) order
	add(t, s, NewMessage{Protocol: "signal", ContactNumber: "A", Text: "tie-1", Timestamp: 250, AttachmentID: strp("x1")})
	add(t, s, NewMessage{Protocol: "signal", ContactNumber: "A", Text: "tie-2", Timestamp: 250, AttachmentID: strp("x2")})
	add(t, s, NewMessage{Protocol: "signal", ContactNumber: "A", Text: "caption", Timestamp: 260, MsgType: "image"})
	mustExec(t, s, "UPDATE messages SET edited = 1 WHERE text = 'later'")

	tg := must(s.LoadCache(ctx, "telegram"))
	if len(tg) != 1 || len(tg["42"]) != 1 {
		t.Fatalf("telegram cache = %v", tg)
	}
	m := tg["42"][0]
	eq(t, *m.ReplyToMessageID, "12")
	eq(t, *m.QuoteTimestamp, int64(90))
	eq(t, *m.MsgID, "99")
	eq(t, m.IsMine, true)

	all := must(s.LoadCache(ctx, ""))
	var texts []string
	for _, m := range all["A"] {
		texts = append(texts, *m.Text)
	}
	want := []string{"earlier", "tie-1", "tie-2", "", "later"} // image text blanked
	if fmt.Sprint(texts) != fmt.Sprint(want) {
		t.Fatalf("order = %q, want %q", texts, want)
	}
	if !all["A"][4].Edited || all["A"][0].Edited {
		t.Fatal("edited flag not exposed as bool")
	}
	if all["A"][0].Read || !tg["42"][0].Read {
		t.Fatal("read flag wrong (incoming unread, outgoing read)")
	}
}

func TestLoadCacheRunsDedupByID(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"contact_number": "c", "text": "t", "msg_id": "m", "status": "sent", "timestamp": 10})
	insertRow(t, s, row{"contact_number": "c", "text": "t", "msg_id": "m", "status": "read", "timestamp": 11})
	cache := must(s.LoadCache(ctx, ""))
	if len(cache["c"]) != 1 || *cache["c"][0].Status != "read" {
		t.Fatalf("expected a single read survivor, got %+v", cache["c"])
	}
}

func TestUpdateMessageIDPicksClosestIdlessRow(t *testing.T) {
	s := newStore(t)
	a := insertRow(t, s, row{"contact_number": "c", "text": "same", "is_mine": 1, "timestamp": 1000, "status": "failed"})
	b := insertRow(t, s, row{"contact_number": "c", "text": "same", "is_mine": 1, "timestamp": 5000, "status": "sent", "msg_id": ""})
	c := insertRow(t, s, row{"contact_number": "c", "text": "same", "is_mine": 1, "timestamp": 9000, "status": "sent"})

	ok := must(s.UpdateMessageID(ctx, "signal", "c", "same", true, 5100, "real-1"))
	eq(t, ok, true)
	r := rowByID(t, s, b)
	eq(t, *r.str("msg_id"), "real-1")
	eq(t, r.int("timestamp"), 5100)
	if rowByID(t, s, a)["msg_id"] != nil || rowByID(t, s, c)["msg_id"] != nil {
		t.Fatal("only the closest id-less row may receive the id")
	}
	// A row that already has an id is never retargeted.
	ok = must(s.UpdateMessageID(ctx, "signal", "c", "same", true, 5100, "real-2"))
	eq(t, ok, true) // goes to the next closest id-less row (c at 9000? outside window? 3900<600000 yes)
	eq(t, *rowByID(t, s, c).str("msg_id"), "real-2")
}

func TestUpdateMessageIDOutsideWindowAndScoping(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"contact_number": "c", "text": "x", "is_mine": 1, "timestamp": 1000})
	eq(t, must(s.UpdateMessageID(ctx, "signal", "c", "x", true, 1000+EchoMatchWindowMs+1, "id")), false)
	eq(t, must(s.UpdateMessageID(ctx, "signal", "c", "x", true, 1000+EchoMatchWindowMs, "id")), true)
	eq(t, must(s.UpdateMessageID(ctx, "signal", "other", "x", true, 1000, "id2")), false)
	eq(t, must(s.UpdateMessageID(ctx, "telegram", "c", "x", true, 1000, "id2")), false)
	eq(t, must(s.UpdateMessageID(ctx, "signal", "c", "x", false, 1000, "id2")), false)
}

func TestUpdateMessageAttachmentID(t *testing.T) {
	s := newStore(t)
	r1 := insertRow(t, s, row{"msg_id": "m", "attachment_id": "sent-1.png", "timestamp": 100})
	r2 := insertRow(t, s, row{"msg_id": "m", "attachment_id": "sent-2.png", "timestamp": 100})
	// expected id targets exactly that slot
	eq(t, must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "m", 100, "real-2", strp("sent-2.png"))), true)
	eq(t, *rowByID(t, s, r2).str("attachment_id"), "real-2")
	eq(t, *rowByID(t, s, r1).str("attachment_id"), "sent-1.png")
	// without expected: closest row, lowest rowid
	eq(t, must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "m", 100, "real-1", nil)), true)
	eq(t, *rowByID(t, s, r1).str("attachment_id"), "real-1")
	// unknown expected / empty msg id
	eq(t, must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "m", 100, "z", strp("nope"))), false)
	eq(t, must(s.UpdateMessageAttachmentID(ctx, "signal", "c", "", 100, "z", nil)), false)
}

func TestUpdateMessageAttachmentInfoAndMediaIdentity(t *testing.T) {
	s := newStore(t)
	id := insertRow(t, s, row{"protocol": "whatsapp", "msg_id": "w1", "timestamp": 50, "msg_type": "text"})
	eq(t, must(s.UpdateMessageAttachmentInfo(ctx, "whatsapp", "c", "w1", 50, "doc.pdf")), true)
	eq(t, *rowByID(t, s, id).str("attachment_info"), "doc.pdf")
	eq(t, must(s.UpdateMessageAttachmentInfo(ctx, "whatsapp", "c", "", 50, "x")), false)

	eq(t, must(s.UpdateMessageMediaIdentity(ctx, "whatsapp", "c", "w1", 50, "file.pdf", "attachment", strp("document"))), true)
	r := rowByID(t, s, id)
	eq(t, *r.str("attachment_id"), "file.pdf")
	eq(t, *r.str("msg_type"), "attachment")
	eq(t, *r.str("media_kind"), "document")
	// already has an attachment id -> untouched
	eq(t, must(s.UpdateMessageMediaIdentity(ctx, "whatsapp", "c", "w1", 50, "other.pdf", "image", nil)), false)
	eq(t, *rowByID(t, s, id).str("attachment_id"), "file.pdf")
}

func TestMessageMediaRowPrefersRowWithAttachment(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "whatsapp", "msg_id": "w", "timestamp": 1, "msg_type": "text"})
	insertRow(t, s, row{"protocol": "whatsapp", "msg_id": "w", "timestamp": 2, "msg_type": "attachment",
		"attachment_id": "a.ogg", "media_kind": "voice", "content_type": "audio/ogg"})
	m := must(s.MessageMediaRow(ctx, "whatsapp", "c", "w"))
	if m == nil {
		t.Fatal("nil")
	}
	eq(t, *m.AttachmentID, "a.ogg")
	eq(t, m.Timestamp, int64(2))
	eq(t, *m.MediaKind, "voice")
	if must(s.MessageMediaRow(ctx, "whatsapp", "c", "missing")) != nil {
		t.Fatal("expected nil")
	}
	if must(s.MessageMediaRow(ctx, "whatsapp", "c", "")) != nil {
		t.Fatal("expected nil for empty id")
	}
}

func TestFillMessageQuoteFieldsOnlyFillsEmpty(t *testing.T) {
	s := newStore(t)
	id := insertRow(t, s, row{"msg_id": "m", "timestamp": 10, "quote_text": "", "quote_author": "keep"})
	q := QuoteFill{QuoteText: strp("qt"), QuoteTimestamp: i64p(5), QuoteAuthor: strp("new"), ReplyToMessageID: strp("r"),
		QuoteAttachmentID: strp("qa"), QuoteAttachmentPath: strp("/qp"), QuoteContentType: strp("image/png")}
	eq(t, must(s.FillMessageQuoteFields(ctx, "signal", "c", "m", 10, q)), true)
	r := rowByID(t, s, id)
	eq(t, *r.str("quote_text"), "qt")
	eq(t, *r.str("quote_author"), "keep")
	eq(t, r.int("quote_timestamp"), 5)
	eq(t, *r.str("reply_to_message_id"), "r")
	// idempotent, never overwrites
	q2 := QuoteFill{QuoteText: strp("zzz"), QuoteTimestamp: i64p(6)}
	must(s.FillMessageQuoteFields(ctx, "signal", "c", "m", 10, q2))
	r = rowByID(t, s, id)
	eq(t, *r.str("quote_text"), "qt")
	eq(t, r.int("quote_timestamp"), 5)
	eq(t, must(s.FillMessageQuoteFields(ctx, "signal", "c", "nope", 10, q)), false)
}

func TestMarkAsReadAndUnreadCounts(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 3; i++ {
		add(t, s, NewMessage{ContactNumber: "A@c.us", Protocol: "whatsapp", Text: fmt.Sprint("a", i), Timestamp: int64(i)})
	}
	add(t, s, NewMessage{ContactNumber: "A@lid", Protocol: "whatsapp", Text: "lid", Timestamp: 10})
	add(t, s, NewMessage{ContactNumber: "B", Protocol: "signal", Text: "b", Timestamp: 1})
	add(t, s, NewMessage{ContactNumber: "B", Protocol: "telegram", Text: "b", Timestamp: 1})
	add(t, s, NewMessage{ContactNumber: "B", Protocol: "signal", Text: "mine", IsMine: true, Timestamp: 2})

	un := must(s.UnreadCounts(ctx))
	eq(t, un[ContactKey{"whatsapp", "A@c.us"}], 3)
	eq(t, un[ContactKey{"whatsapp", "A@lid"}], 1)
	eq(t, un[ContactKey{"signal", "B"}], 1)
	eq(t, len(un), 4) // outgoing rows never count
	cu := must(s.CountUnread(ctx))
	eq(t, cu["B"], 2) // protocols merged, like Python
	eq(t, cu["A@c.us"], 3)

	check(s.MarkAsRead(ctx, "whatsapp", "A@c.us", "A@lid", ""))
	check(s.MarkAsRead(ctx, "signal", "B"))
	un = must(s.UnreadCounts(ctx))
	if len(un) != 1 || un[ContactKey{"telegram", "B"}] != 1 {
		t.Fatalf("after mark-as-read: %v", un)
	}
}

func TestMarkAsReadAliasKeyOnlyWhenListed(t *testing.T) {
	s := newStore(t)
	add(t, s, NewMessage{ContactNumber: "A@c.us", Protocol: "whatsapp", Text: "1", Timestamp: 1})
	add(t, s, NewMessage{ContactNumber: "A@lid", Protocol: "whatsapp", Text: "2", Timestamp: 2})
	check(s.MarkAsRead(ctx, "whatsapp", "A@c.us"))
	eq(t, len(must(s.UnreadCounts(ctx))), 1)
}

func TestUpdateMessageStatusRankGuard(t *testing.T) {
	s := newStore(t)
	id := insertRow(t, s, row{"text": "t", "is_mine": 1, "timestamp": 100, "status": "pending"})
	ok := func(status string, text *string, exp []string) bool {
		return must(s.UpdateMessageStatus(ctx, 100, status, "signal", "c", text, exp))
	}
	eq(t, ok("sent", nil, nil), true)
	eq(t, ok("delivered", strp("t"), nil), true)
	eq(t, ok("sent", nil, nil), false) // downgrade refused
	eq(t, *rowByID(t, s, id).str("status"), "delivered")
	eq(t, ok("read", strp("other text"), nil), false)
	eq(t, ok("read", nil, []string{"pending", "failed"}), false) // expected status mismatch
	eq(t, ok("read", nil, []string{"delivered"}), true)
	eq(t, ok("failed", nil, nil), false) // failed is rank 0
	// same rank transitions are allowed (failed -> pending)
	mustExec(t, s, "UPDATE messages SET status='failed'")
	eq(t, ok("pending", nil, nil), true)
	// scoped by protocol and contact
	eq(t, must(s.UpdateMessageStatus(ctx, 100, "read", "telegram", "c", nil, nil)), false)
	eq(t, must(s.UpdateMessageStatus(ctx, 100, "read", "signal", "x", nil, nil)), false)
}

func TestUpdateMessageStatusUnknownAndNullRank(t *testing.T) {
	s := newStore(t)
	id := insertRow(t, s, row{"timestamp": 5, "status": "weird"})
	// unknown status ranks as 0, so it may move to "sent"
	eq(t, must(s.UpdateMessageStatus(ctx, 5, "sent", "signal", "c", nil, nil)), true)
	eq(t, *rowByID(t, s, id).str("status"), "sent")
	// an unknown NEW status has rank 0 and cannot overwrite a higher one
	eq(t, must(s.UpdateMessageStatus(ctx, 5, "bogus", "signal", "c", nil, nil)), false)
}

func TestUpdateMessageStatusByID(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "telegram", "contact_number": "chat1", "msg_id": "7", "status": "sent", "is_mine": 1})
	insertRow(t, s, row{"protocol": "telegram", "contact_number": "chat2", "msg_id": "7", "status": "sent", "is_mine": 1})
	eq(t, must(s.UpdateMessageStatusByID(ctx, "7", "read", "telegram", strp("chat1"))), true)
	eq(t, count(t, s, "status = 'read'"), 1)
	eq(t, must(s.UpdateMessageStatusByID(ctx, "7", "delivered", "telegram", strp("chat1"))), false)
	eq(t, must(s.UpdateMessageStatusByID(ctx, "7", "read", "telegram", nil)), true) // chat2
	eq(t, count(t, s, "status = 'read'"), 2)
}

func TestUpdateMessageStatusByTextFallback(t *testing.T) {
	s := newStore(t)
	old := insertRow(t, s, row{"text": "hello", "is_mine": 1, "timestamp": 1, "status": "pending"})
	newer := insertRow(t, s, row{"text": "hello", "is_mine": 1, "timestamp": 2, "status": "pending"})
	insertRow(t, s, row{"text": "hello", "is_mine": 0, "timestamp": 3, "status": "pending"})
	insertRow(t, s, row{"contact_number": "other", "text": "hello", "is_mine": 1, "timestamp": 4, "status": "pending"})

	eq(t, must(s.UpdateMessageStatusByText(ctx, "hello", "sent", "signal", "c", []string{"pending"})), true)
	eq(t, *rowByID(t, s, newer).str("status"), "sent")
	eq(t, *rowByID(t, s, old).str("status"), "pending") // only the most recent row
	eq(t, count(t, s, "is_mine = 0 AND status = 'sent'"), 0)
	eq(t, count(t, s, "contact_number = 'other' AND status = 'sent'"), 0)
	// expected status filter is applied inside the subselect: newest row is now
	// 'sent' so a pending-only filter picks the older row.
	eq(t, must(s.UpdateMessageStatusByText(ctx, "hello", "sent", "signal", "c", []string{"pending"})), true)
	eq(t, *rowByID(t, s, old).str("status"), "sent")
	// no downgrade
	eq(t, must(s.UpdateMessageStatusByText(ctx, "hello", "pending", "signal", "c", nil)), false)
	eq(t, must(s.UpdateMessageStatusByText(ctx, "nothing", "sent", "signal", "c", nil)), false)
}

func TestUpdateMessageText(t *testing.T) {
	s := newStore(t)
	byID := insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "WA-9", "text": "old", "timestamp": 123456, "is_mine": 1})
	byTS := insertRow(t, s, row{"text": "old", "timestamp": 77, "is_mine": 0})

	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "whatsapp", ContactNumber: "w", MsgID: strp("WA-9"), NewText: "new"})), true)
	r := rowByID(t, s, byID)
	eq(t, *r.str("text"), "new")
	eq(t, r.int("edited"), 1)
	eq(t, r.int("timestamp"), 123456) // identity untouched
	eq(t, *r.str("msg_id"), "WA-9")

	upd := TextUpdate{Protocol: "signal", ContactNumber: "c", Timestamp: i64p(77), NewText: "edited", OldText: strp("wrong")}
	eq(t, must(s.UpdateMessageText(ctx, upd)), false)
	upd.OldText = strp("old")
	upd.IsMine = Ptr(true)
	eq(t, must(s.UpdateMessageText(ctx, upd)), false) // is_mine guard
	upd.IsMine = Ptr(false)
	eq(t, must(s.UpdateMessageText(ctx, upd)), true)
	eq(t, *rowByID(t, s, byTS).str("text"), "edited")

	// rollback path clears the flag
	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "signal", ContactNumber: "c", Timestamp: i64p(77), NewText: "old", ClearEdited: true})), true)
	eq(t, rowByID(t, s, byTS).int("edited"), 0)

	// no key at all
	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "signal", ContactNumber: "c", NewText: "x"})), false)
	// scoping by protocol / contact
	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "telegram", ContactNumber: "c", Timestamp: i64p(77), NewText: "x"})), false)
	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "signal", ContactNumber: "zz", Timestamp: i64p(77), NewText: "x"})), false)
	// msg_id wins over timestamp when both are given
	eq(t, must(s.UpdateMessageText(ctx, TextUpdate{Protocol: "whatsapp", ContactNumber: "w", MsgID: strp("WA-9"), Timestamp: i64p(1), NewText: "again"})), true)
}

func TestDedupMessagesExact(t *testing.T) {
	s := newStore(t)
	a := insertRow(t, s, row{"text": "x", "timestamp": 5})
	insertRow(t, s, row{"text": "x", "timestamp": 5})
	insertRow(t, s, row{"text": "x", "timestamp": 5, "is_mine": 1})
	insertRow(t, s, row{"text": "x", "timestamp": 5, "protocol": "telegram"})
	n := must(s.DedupMessages(ctx))
	eq(t, n, 1)
	eq(t, count(t, s, "id = ?", a), 1)
	eq(t, count(t, s, "1=1"), 3)
}

func TestConcurrentUse(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, _, err := s.AddMessage(ctx, NewMessage{ContactNumber: fmt.Sprint("c", g), Text: fmt.Sprint(i), Timestamp: int64(i), Sender: "s"}); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.CountUnread(ctx); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.UpdateMessageStatus(ctx, int64(i), "read", "signal", fmt.Sprint("c", g), nil, nil); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.ApplyReactionDelta(ctx, ReactionDelta{Protocol: "signal", Contact: fmt.Sprint("c", g), TargetTimestamp: i64p(int64(i)),
					Emoji: "x", AuthorKey: "a", Timestamp: 1}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	eq(t, count(t, s, "1=1"), 200)
}

func TestTwoStoresShareOneFile(t *testing.T) {
	s1 := newStore(t)
	s2, err := Open(s1.Path(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	add(t, s1, NewMessage{ContactNumber: "c", Text: "from1", Timestamp: 1})
	add(t, s2, NewMessage{ContactNumber: "c", Text: "from2", Timestamp: 2})
	eq(t, len(must(s1.LoadCache(ctx, ""))["c"]), 2)
	eq(t, len(must(s2.LoadCache(ctx, ""))["c"]), 2)
}
