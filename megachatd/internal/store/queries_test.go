package store

import (
	"fmt"
	"testing"
)

func TestListMessagesOrderingLimitAndAliases(t *testing.T) {
	s := newStore(t)
	for i := 1; i <= 10; i++ {
		key := "A@c.us"
		if i%2 == 0 {
			key = "A@lid"
		}
		insertRow(t, s, row{"protocol": "whatsapp", "contact_number": key, "text": fmt.Sprint("m", i), "timestamp": i})
	}
	insertRow(t, s, row{"protocol": "signal", "contact_number": "A@c.us", "text": "other-proto", "timestamp": 5})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "B", "text": "other-chat", "timestamp": 5})

	texts := func(ms []Message) (out []string) {
		for _, m := range ms {
			out = append(out, *m.Text)
		}
		return
	}
	all := must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp", Keys: []string{"A@c.us", "A@lid"}}))
	eq(t, len(all), 10)
	eq(t, texts(all)[0], "m1")
	eq(t, texts(all)[9], "m10")

	one := must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp", Keys: []string{"A@c.us"}}))
	eq(t, len(one), 5)

	// newest N, returned chronologically
	last := must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp", Keys: []string{"A@c.us", "A@lid"}, SQLLimit: 3}))
	eq(t, fmt.Sprint(texts(last)), "[m8 m9 m10]")

	since := must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp", Keys: []string{"A@c.us", "A@lid"}, Since: i64p(9)}))
	eq(t, fmt.Sprint(texts(since)), "[m9 m10]")

	// ties on timestamp keep id order in both directions
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "T", "text": "t1", "timestamp": 50})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "T", "text": "t2", "timestamp": 50})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "T", "text": "t3", "timestamp": 50})
	tie := must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp", Keys: []string{"T"}, SQLLimit: 2}))
	eq(t, fmt.Sprint(texts(tie)), "[t2 t3]")

	eq(t, len(must(s.ListMessages(ctx, MessageQuery{Protocol: "whatsapp"}))), 0)
}

func TestQuotedMediaAttachmentID(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "signal", "timestamp": 10_000, "attachment_id": "photo.jpg", "content_type": "image/jpeg"})
	insertRow(t, s, row{"protocol": "signal", "timestamp": 20_000, "text": "just text"})
	insertRow(t, s, row{"protocol": "signal", "timestamp": 30_000, "attachment_id": "doc.pdf", "content_type": "application/pdf"})
	insertRow(t, s, row{"protocol": "signal", "timestamp": 40_000, "attachment_id": "clip.mp4"})

	q := func(ts int64, kind MediaFilter) string {
		return must(s.QuotedMediaAttachmentID(ctx, "signal", "c", ts, kind))
	}
	eq(t, q(10_000, MediaImage), "photo.jpg")
	eq(t, q(10_000, MediaAny), "photo.jpg")
	eq(t, q(10_000, MediaVideo), "")          // exists but wrong kind: nothing
	eq(t, q(11_500, MediaImage), "photo.jpg") // drift: within +-2s, no exact message
	eq(t, q(12_001, MediaImage), "")          // outside the window
	eq(t, q(20_000, MediaImage), "")          // exact timestamp exists (text): never a neighbour
	eq(t, q(30_000, MediaImage), "")          // pdf is not image/video
	eq(t, q(40_000, MediaVideo), "clip.mp4")  // by extension
	eq(t, q(0, MediaAny), "")
	eq(t, must(s.QuotedMediaAttachmentID(ctx, "signal", "other", 10_000, MediaImage)), "")
}

func TestQuotedMediaByMessageIDAndFilename(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "true_1@c.us_ABC", "attachment_id": "pic.PNG", "timestamp": 100, "attachment_info": "IMG_1303.jpg"})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "XYZ", "attachment_id": "notes.pdf", "timestamp": 200})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "Q1", "attachment_id": "IMG_1303.jpg", "timestamp": 300, "content_type": "image/jpeg"})

	eq(t, must(s.QuotedMediaByMessageID(ctx, "whatsapp", "w", "ABC", MediaImage)), "pic.PNG") // "..._ABC" suffix
	eq(t, must(s.QuotedMediaByMessageID(ctx, "whatsapp", "w", "true_1@c.us_ABC", MediaImage)), "pic.PNG")
	eq(t, must(s.QuotedMediaByMessageID(ctx, "whatsapp", "w", "XYZ", MediaImage)), "") // pdf
	eq(t, must(s.QuotedMediaByMessageID(ctx, "whatsapp", "w", "", MediaImage)), "")

	// closest media strictly older than the quoting message
	eq(t, must(s.QuotedMediaByFilename(ctx, "whatsapp", "w", "IMG_1303.jpg", 250, MediaImage)), "pic.PNG")
	eq(t, must(s.QuotedMediaByFilename(ctx, "whatsapp", "w", "IMG_1303.jpg", 400, MediaImage)), "IMG_1303.jpg")
	eq(t, must(s.QuotedMediaByFilename(ctx, "whatsapp", "w", "IMG_1303.jpg", 100, MediaImage)), "")
	eq(t, must(s.QuotedMediaByFilename(ctx, "whatsapp", "w", "", 400, MediaImage)), "")
}

func TestQuotedSender(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"timestamp": 5, "sender": "Alice"})
	insertRow(t, s, row{"timestamp": 6})
	eq(t, must(s.QuotedSender(ctx, "signal", "c", 5)), "Alice")
	eq(t, must(s.QuotedSender(ctx, "signal", "c", 6)), "")
	eq(t, must(s.QuotedSender(ctx, "signal", "c", 7)), "")
	eq(t, must(s.QuotedSender(ctx, "signal", "c", 0)), "")
}

func TestMessageRowForEdit(t *testing.T) {
	s := newStore(t)
	byID := insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w@c.us", "msg_id": "W1", "text": "hello", "is_mine": 1, "status": "sent"})
	idless := insertRow(t, s, row{"protocol": "signal", "contact_number": "+39", "timestamp": 1234, "text": "sig"})
	r := must(s.MessageRowForEdit(ctx, "whatsapp", "w@lid", "W1", "w@c.us"))
	if r == nil || r.RowID != byID || !r.IsMine || *r.Text != "hello" {
		t.Fatalf("alias lookup: %+v", r)
	}
	r = must(s.MessageRowForEdit(ctx, "signal", "+39", "1234"))
	if r == nil || r.RowID != idless {
		t.Fatalf("signal id-less by timestamp: %+v", r)
	}
	if must(s.MessageRowForEdit(ctx, "whatsapp", "w@c.us", "1234")) != nil {
		t.Fatal("non-signal protocols must not match by timestamp")
	}
	if must(s.MessageRowForEdit(ctx, "whatsapp", "other", "W1")) != nil {
		t.Fatal("wrong chat")
	}
}

func TestMessageRowForReactionAndPersistEdit(t *testing.T) {
	s := newStore(t)
	a := insertRow(t, s, row{"protocol": "signal", "contact_number": "+39", "msg_id": "9000", "timestamp": 9000})
	b := insertRow(t, s, row{"protocol": "signal", "contact_number": "+39", "timestamp": 7000, "text": "idless"})
	r := must(s.MessageRowForReaction(ctx, "signal", "+39", "9000"))
	if r == nil || r.RowID != a {
		t.Fatalf("by msg_id: %+v", r)
	}
	r = must(s.MessageRowForReaction(ctx, "signal", "+39", "7000"))
	if r == nil || r.RowID != b {
		t.Fatalf("by timestamp: %+v", r)
	}
	r = must(s.MessageRowForReaction(ctx, "signal", "+39", fmt.Sprint(b)))
	if r == nil || r.RowID != b {
		t.Fatalf("by row id: %+v", r)
	}
	if must(s.MessageRowForReaction(ctx, "telegram", "+39", "9000")) != nil {
		t.Fatal("protocol scoping")
	}

	n := must(s.PersistMessageEdit(ctx, "signal", "+39", "7000", "fixed"))
	eq(t, n, int64(1))
	eq(t, *rowByID(t, s, b).str("text"), "fixed")
	eq(t, rowByID(t, s, b).int("edited"), 1)
	eq(t, must(s.PersistMessageEdit(ctx, "signal", "+39", "424242", "x")), int64(0))
	eq(t, must(s.PersistMessageEdit(ctx, "telegram", "+39", "7000", "x")), int64(0))
}

func TestAttachmentLookups(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"attachment_id": "a1", "content_type": nil, "msg_type": "image", "media_kind": "image"})
	insertRow(t, s, row{"attachment_id": "a2", "content_type": "audio/ogg", "media_kind": "voice"})
	insertRow(t, s, row{"attachment_id": "a2", "content_type": "audio/mpeg", "media_kind": "audio"}) // newest id wins
	insertRow(t, s, row{"attachment_id": "a3", "content_type": nil, "msg_type": "attachment"})

	eq(t, *must(s.AttachmentContentType(ctx, "signal", "a1")), "image/*")
	eq(t, *must(s.AttachmentContentType(ctx, "signal", "a2")), "audio/mpeg")
	if must(s.AttachmentContentType(ctx, "signal", "a3")) != nil || must(s.AttachmentContentType(ctx, "signal", "none")) != nil {
		t.Fatal("expected nil")
	}
	eq(t, *must(s.AttachmentMediaKind(ctx, "signal", "a2")), "audio")
	if must(s.AttachmentMediaKind(ctx, "signal", "a3")) != nil {
		t.Fatal("NULL media_kind -> nil")
	}
	if must(s.AttachmentMediaKind(ctx, "telegram", "a2")) != nil {
		t.Fatal("protocol scoping")
	}
}

func TestQuoteAttachmentPath(t *testing.T) {
	s := newStore(t)
	id := insertRow(t, s, row{"quote_attachment_path": "/c/quote-thumbs/x.png"})
	eq(t, *must(s.QuoteAttachmentPath(ctx, "signal", id)), "/c/quote-thumbs/x.png")
	if must(s.QuoteAttachmentPath(ctx, "telegram", id)) != nil || must(s.QuoteAttachmentPath(ctx, "signal", id+99)) != nil {
		t.Fatal("expected nil")
	}
}

func TestFindQuotedMessage(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "true_1_ABC", "timestamp": 100, "media_kind": "image", "attachment_id": "i.png"})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "VOICE", "timestamp": 200, "media_kind": "voice", "attachment_id": "v.ogg", "is_mine": 1})
	insertRow(t, s, row{"protocol": "whatsapp", "contact_number": "w", "msg_id": "TXT", "timestamp": 200})

	r := must(s.FindQuotedMessage(ctx, "whatsapp", []string{"w"}, "ABC", 0))
	if r == nil || *r.AttachmentID != "i.png" {
		t.Fatalf("by id suffix: %+v", r)
	}
	// by timestamp, audio/voice rows preferred
	r = must(s.FindQuotedMessage(ctx, "whatsapp", []string{"w", "w2"}, "  ", 200))
	if r == nil || *r.MediaKind != "voice" || !r.IsMine {
		t.Fatalf("by timestamp: %+v", r)
	}
	// reply id miss falls through to the timestamp query
	r = must(s.FindQuotedMessage(ctx, "whatsapp", []string{"w"}, "NOPE", 200))
	if r == nil || *r.MsgID != "VOICE" {
		t.Fatalf("fallthrough: %+v", r)
	}
	if must(s.FindQuotedMessage(ctx, "whatsapp", []string{"w"}, "", 0)) != nil {
		t.Fatal("no id and no timestamp")
	}
}

func TestOutgoingRowCountsAndMediaReferences(t *testing.T) {
	s := newStore(t)
	insertRow(t, s, row{"text": "hi", "is_mine": 1, "timestamp": 5})
	insertRow(t, s, row{"text": "hi", "is_mine": 1, "timestamp": 6})
	insertRow(t, s, row{"text": "hi", "is_mine": 0, "timestamp": 5})
	byText, byTS, err := s.OutgoingRowCounts(ctx, "signal", "c", "hi", 5)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, byText, 2)
	eq(t, byTS, 1)

	insertRow(t, s, row{"protocol": "signal", "attachment_id": "a.png", "quote_attachment_path": "/q/1.png"})
	insertRow(t, s, row{"protocol": "signal", "quote_attachment_id": "qa"})
	insertRow(t, s, row{"protocol": "telegram", "attachment_id": "t.mp4"})
	insertRow(t, s, row{"protocol": "whatsapp", "attachment_id": "w.ogg"})
	refs := must(s.MediaReferences(ctx, []string{"signal", "telegram", "signal", "empty"}))
	eq(t, len(refs), 3)
	eq(t, len(refs["signal"]), 3)
	_, ok := refs["signal"]["/q/1.png"]
	eq(t, ok, true)
	eq(t, len(refs["telegram"]), 1)
	eq(t, len(refs["empty"]), 0)
	eq(t, len(must(s.MediaReferences(ctx, nil))), 0)
}

func TestMessagesByWAID(t *testing.T) {
	s := newStore(t)
	for i, id := range []string{"false_1203@g.us_3EB0AAAA_55@lid", "false_1203@g.us_3EB0BBBB_55@lid"} {
		id := id
		if _, _, err := s.AddMessage(ctx, NewMessage{Protocol: "whatsapp", ContactNumber: "1203@g.us", Text: "x", Timestamp: int64(100 + i), MsgID: &id}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.MessagesByWAID(ctx, "whatsapp", []string{"1203@g.us"}, "3EB0BBBB")
	if err != nil || len(rows) != 1 || Deref(rows[0].MsgID) != "false_1203@g.us_3EB0BBBB_55@lid" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows, _ := s.MessagesByWAID(ctx, "whatsapp", []string{"other"}, "3EB0BBBB"); len(rows) != 0 {
		t.Fatalf("other chat: %v", rows)
	}
}
