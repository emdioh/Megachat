package store

import "testing"

// Python re-ran the media_kind correction on every _init_db(): rows saved
// with media_kind NULL were classified by the next operation.
func TestMediaKindClassifiedByNextOperation(t *testing.T) {
	s := newStore(t)
	voice := add(t, s, NewMessage{ContactNumber: "c", Text: "", Timestamp: 1, MsgType: "attachment", AttachmentID: strp("note.oga")})
	// not yet classified: the insert is the last operation
	if rowByID(t, s, voice)["media_kind"] != nil {
		t.Fatal("classification must happen on the next operation, like Python")
	}
	vid := add(t, s, NewMessage{ContactNumber: "c", Text: "", Timestamp: 2, MsgType: "attachment", AttachmentID: strp("clip.MP4")})
	eq(t, *rowByID(t, s, voice).str("media_kind"), "audio") // the second insert settled the first
	img := add(t, s, NewMessage{ContactNumber: "c", Text: "", Timestamp: 3, MsgType: "attachment", AttachmentID: strp("p.png")})
	doc := add(t, s, NewMessage{ContactNumber: "c", Text: "", Timestamp: 4, MsgType: "attachment", AttachmentID: strp("r.pdf")})
	txt := add(t, s, NewMessage{ContactNumber: "c", Text: "plain", Timestamp: 5})
	if _, err := s.CountUnread(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{voice: "audio", vid: "video", img: "image", doc: "document"} {
		if got := rowByID(t, s, id).str("media_kind"); got == nil || *got != want {
			t.Errorf("row %d media_kind = %v, want %s", id, got, want)
		}
	}
	if rowByID(t, s, txt)["media_kind"] != nil {
		t.Error("plain text must stay unclassified")
	}
	// explicit media_kind is never overwritten (except document -> audio/...)
	keep := add(t, s, NewMessage{ContactNumber: "c", Text: "", Timestamp: 6, MsgType: "attachment", AttachmentID: strp("v.png"), MediaKind: strp("sticker")})
	check(s.RepairMediaKinds(ctx))
	eq(t, *rowByID(t, s, keep).str("media_kind"), "sticker")
}

func TestMediaKindDocumentReclassifiedAsAudio(t *testing.T) {
	s := newStore(t)
	id := add(t, s, NewMessage{ContactNumber: "c", Timestamp: 1, MsgType: "attachment", AttachmentID: strp("a.opus"), MediaKind: strp("document")})
	check(s.RepairMediaKinds(ctx))
	eq(t, *rowByID(t, s, id).str("media_kind"), "audio")
}
