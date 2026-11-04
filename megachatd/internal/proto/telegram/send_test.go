package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestSendMessage(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.reply(&tg.MessagesSendMessageRequest{}, &tg.UpdateShortSentMessage{ID: 123})
	id, err := e.b.SendMessage(bg, "7", "hello **x**", proto.Quote{ReplyToMessageID: "55"})
	if err != nil || id != "123" {
		t.Fatalf("%q %v", id, err)
	}
	req := e.f.last(&tg.MessagesSendMessageRequest{}).(*tg.MessagesSendMessageRequest)
	if p := req.Peer.(*tg.InputPeerUser); p.UserID != 7 || p.AccessHash != 99 {
		t.Fatalf("%+v", req.Peer)
	}
	if req.Message != "hello x" || len(req.Entities) != 1 {
		t.Fatalf("markdown must be parsed like Telethon: %q %v", req.Message, req.Entities)
	}
	if r, ok := req.ReplyTo.(*tg.InputReplyToMessage); !ok || r.ReplyToMsgID != 55 {
		t.Fatalf("reply %+v", req.ReplyTo)
	}
	if _, err := e.b.SendMessage(bg, "7", "x", proto.Quote{ReplyToMessageID: "abc"}); !errors.Is(err, ErrInvalidReplyID) {
		t.Fatalf("%v", err)
	}
	if _, err := e.b.SendMessage(bg, "7", "x", proto.Quote{ReplyToMessageID: "0"}); !errors.Is(err, ErrInvalidReplyID) {
		t.Fatalf("%v", err)
	}
	if _, err := e.b.SendMessage(bg, "nope", "x", proto.Quote{}); err == nil || !strings.Contains(err.Error(), "Invalid Telegram contact id") {
		t.Fatalf("%v", err)
	}
	if _, err := New(Options{}).SendMessage(bg, "7", "x", proto.Quote{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("%v", err)
	}
	if err := e.b.MarkRead(bg, "7"); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageViaUpdates(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.on(&tg.MessagesSendMessageRequest{}, func(in bin.Encoder) (bin.Encoder, error) {
		r := in.(*tg.MessagesSendMessageRequest)
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 321, RandomID: r.RandomID}}}, nil
	})
	if id, err := e.b.SendMessage(bg, "7", "x", proto.Quote{}); err != nil || id != "321" {
		t.Fatalf("%q %v", id, err)
	}
}

func TestEditMessage(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.reply(&tg.MessagesEditMessageRequest{}, &tg.Updates{})
	if ok, err := e.b.EditMessage(bg, "7", "40", "new"); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	r := e.f.last(&tg.MessagesEditMessageRequest{}).(*tg.MessagesEditMessageRequest)
	if r.ID != 40 || r.Message != "new" {
		t.Fatalf("%+v", r)
	}
	for _, c := range [][2]string{{"x", "1"}, {"7", "x"}, {"7", "0"}, {"7", "-3"}} {
		if _, err := e.b.EditMessage(bg, c[0], c[1], "t"); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if _, err := New(Options{}).EditMessage(bg, "7", "1", "t"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("%v", err)
	}
}

func TestSendReactionAndAvailable(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.reply(&tg.MessagesSendReactionRequest{}, &tg.Updates{})
	if ok, _ := e.b.SendReaction(bg, "7", "40", "👍", ""); !ok {
		t.Fatal("send reaction")
	}
	r := e.f.last(&tg.MessagesSendReactionRequest{}).(*tg.MessagesSendReactionRequest)
	reaction, _ := r.GetReaction()
	if r.MsgID != 40 || len(reaction) != 1 || reaction[0].(*tg.ReactionEmoji).Emoticon != "👍" {
		t.Fatalf("%+v", r)
	}
	for _, c := range [][2]string{{"x", "1"}, {"7", "x"}, {"7", "0"}} {
		if ok, err := e.b.SendReaction(bg, c[0], c[1], "👍", ""); ok || err != nil {
			t.Errorf("%v: %v %v", c, ok, err)
		}
	}
	e.f.on(&tg.MessagesSendReactionRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("boom") })
	if ok, err := e.b.SendReaction(bg, "7", "40", "👍", ""); ok || err != nil {
		t.Fatal("failures must be swallowed")
	}

	e.f.reply(&tg.MessagesGetAvailableReactionsRequest{}, &tg.MessagesAvailableReactions{Reactions: []tg.AvailableReaction{availReaction("👍"), availReaction("❤️"), availReaction("")}})
	got, _ := e.b.AvailableReactions(bg)
	if len(got) != 2 || got[0] != "👍" {
		t.Fatalf("%v", got)
	}
	e.b.AvailableReactions(bg)
	if e.f.count(&tg.MessagesGetAvailableReactionsRequest{}) != 1 {
		t.Fatal("not cached")
	}
	e2 := newEnv(t)
	e2.f.on(&tg.MessagesGetAvailableReactionsRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("x") })
	if got, err := e2.b.AvailableReactions(bg); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSetChatArchived(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.reply(&tg.FoldersEditPeerFoldersRequest{}, &tg.Updates{})
	if ok, err := e.b.SetChatArchived(bg, "7", true); !ok || err != nil {
		t.Fatal(ok, err)
	}
	r := e.f.last(&tg.FoldersEditPeerFoldersRequest{}).(*tg.FoldersEditPeerFoldersRequest)
	if len(r.FolderPeers) != 1 || r.FolderPeers[0].FolderID != 1 {
		t.Fatalf("%+v", r)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeUpload(e *env) {
	e.f.reply(&tg.UploadSaveFilePartRequest{}, &tg.BoolTrue{})
	e.f.reply(&tg.UploadSaveBigFilePartRequest{}, &tg.BoolTrue{})
}

func TestSendAttachment(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	fakeUpload(e)
	e.f.on(&tg.MessagesSendMediaRequest{}, func(in bin.Encoder) (bin.Encoder, error) {
		r := in.(*tg.MessagesSendMediaRequest)
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 88, RandomID: r.RandomID}}}, nil
	})
	pdf := writeFile(t, e.dir, "doc.pdf", "%PDF-1.4 data")
	id, err := e.b.SendAttachment(bg, "7", proto.Attachment{Path: pdf, Caption: "cap", MimeType: "application/pdf", Filename: "renamed.pdf"}, proto.Quote{ReplyToMessageID: "3"})
	if err != nil || id != "88" {
		t.Fatalf("%q %v", id, err)
	}
	r := e.f.last(&tg.MessagesSendMediaRequest{}).(*tg.MessagesSendMediaRequest)
	m := r.Media.(*tg.InputMediaUploadedDocument)
	if r.Message != "cap" || !m.ForceFile || m.MimeType != "application/pdf" || m.Attributes[0].(*tg.DocumentAttributeFilename).FileName != "renamed.pdf" {
		t.Fatalf("document must be forced: %+v", m)
	}
	if rt := r.ReplyTo.(*tg.InputReplyToMessage); rt.ReplyToMsgID != 3 {
		t.Fatal("reply")
	}
	png := writeFile(t, e.dir, "p.png", "\x89PNG")
	e.b.SendAttachment(bg, "7", proto.Attachment{Path: png, MimeType: "image/png"}, proto.Quote{})
	if _, ok := e.f.last(&tg.MessagesSendMediaRequest{}).(*tg.MessagesSendMediaRequest).Media.(*tg.InputMediaUploadedPhoto); !ok {
		t.Fatal("png must be sent as a photo")
	}
	ogg := writeFile(t, e.dir, "v.ogg", "OggS")
	e.b.SendAttachment(bg, "7", proto.Attachment{Path: ogg, MimeType: "audio/ogg; codecs=opus", MediaKind: "voice"}, proto.Quote{})
	d := e.f.last(&tg.MessagesSendMediaRequest{}).(*tg.MessagesSendMediaRequest).Media.(*tg.InputMediaUploadedDocument)
	voice := false
	for _, a := range d.Attributes {
		if au, ok := a.(*tg.DocumentAttributeAudio); ok && au.Voice {
			voice = true
		}
	}
	if !voice || d.ForceFile {
		t.Fatalf("voice note: %+v", d)
	}
	if _, err := e.b.SendAttachment(bg, "7", proto.Attachment{Path: filepath.Join(e.dir, "missing")}, proto.Quote{}); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestSendAttachmentsAlbum(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	fakeUpload(e)
	n := 0
	e.f.on(&tg.MessagesUploadMediaRequest{}, func(bin.Encoder) (bin.Encoder, error) {
		n++
		return &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(n), AccessHash: 5}}, nil
	})
	e.f.on(&tg.MessagesSendMultiMediaRequest{}, func(in bin.Encoder) (bin.Encoder, error) {
		r := in.(*tg.MessagesSendMultiMediaRequest)
		// Answer in reverse order: ids must still follow the send order.
		var ups []tg.UpdateClass
		for i := len(r.MultiMedia) - 1; i >= 0; i-- {
			ups = append(ups, &tg.UpdateMessageID{ID: 200 + i, RandomID: r.MultiMedia[i].RandomID})
		}
		return &tg.Updates{Updates: ups}, nil
	})
	a := writeFile(t, e.dir, "a.jpg", "jpg1")
	b := writeFile(t, e.dir, "b.jpg", "jpg2")
	ids, err := e.b.SendAttachments(bg, "7", []proto.Attachment{
		{Path: a, MimeType: "image/jpeg", Caption: "album"}, {Path: b, MimeType: "image/jpeg", Caption: "ignored"},
	}, "batch1", proto.Quote{})
	if err != nil || len(ids) != 2 || ids[0] != "200" || ids[1] != "201" {
		t.Fatalf("%v %v", ids, err)
	}
	r := e.f.last(&tg.MessagesSendMultiMediaRequest{}).(*tg.MessagesSendMultiMediaRequest)
	if len(r.MultiMedia) != 2 || r.MultiMedia[0].Message != "album" || r.MultiMedia[1].Message != "" {
		t.Fatalf("caption applies to the first media only: %+v", r.MultiMedia)
	}
	if ids, err := e.b.SendAttachments(bg, "7", nil, "b", proto.Quote{}); ids != nil || err != nil {
		t.Fatal("empty batch")
	}
}

func TestEnqueueSentMessage(t *testing.T) {
	e := newEnv(t)
	src := writeFile(t, e.dir, "Photo.PNG", "png-bytes")
	err := e.b.EnqueueSentMessage(bg, proto.SentMessage{
		ContactID: "7", MessageID: "88", Text: "cap", AttachmentPath: src, MimeType: "image/png", Filename: "Photo.PNG",
		BatchID: "b1", Quote: proto.Quote{Message: "q", Timestamp: 5, Author: "Bob", ReplyToMessageID: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	evs := e.b.PollOnce()
	if len(evs) != 1 || evs[0].Type != proto.EventMessage || evs[0].ContactID != "7" {
		t.Fatalf("%+v", evs)
	}
	p := evs[0].Payload
	dst := filepath.Join(e.media, "7-88-sent.png")
	if p["id"] != "88" || p["is_mine"] != true || p["sender"] != "You" || p["msg_type"] != "image" || p["text"] != "" ||
		p["attachment_info"] != "cap" || p["attachment_id"] != dst || p["media_kind"] != "image" || p["content_type"] != "image/png" ||
		p["batch_id"] != "b1" || p["quote_text"] != "q" || p["quote_timestamp"] != int64(5) || p["quote_author"] != "Bob" || p["reply_to_message_id"] != "3" {
		t.Fatalf("%v", p)
	}
	if raw, _ := os.ReadFile(dst); string(raw) != "png-bytes" {
		t.Fatal("sent copy missing")
	}
	// Plain text mirror.
	e.b.EnqueueSentMessage(bg, proto.SentMessage{ContactID: "7", MessageID: "89", Text: "hi"})
	p = e.b.PollOnce()[0].Payload
	if p["msg_type"] != "text" || p["text"] != "hi" || p["attachment_id"] != nil || p["attachment_info"] != nil || p["media_kind"] != nil {
		t.Fatalf("%v", p)
	}
	// A document keeps the filename as info.
	pdf := writeFile(t, e.dir, "r.pdf", "x")
	e.b.EnqueueSentMessage(bg, proto.SentMessage{ContactID: "7", MessageID: "90", Text: "t", AttachmentPath: pdf, MimeType: "application/pdf", Filename: "r.pdf"})
	p = e.b.PollOnce()[0].Payload
	if p["msg_type"] != "attachment" || p["attachment_info"] != "r.pdf" || p["media_kind"] != "document" {
		t.Fatalf("%v", p)
	}
	if err := e.b.EnqueueSentNotification(bg, "7", "1", 1, ""); !errors.Is(err, proto.ErrUnsupported) {
		t.Fatal(err)
	}
}

func photoMsg(id int, date int) *tg.Message {
	m := textMsg(id, &tg.PeerUser{UserID: 7}, "", false)
	m.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 5, AccessHash: 6, Date: date,
		Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", Size: 10}, &tg.PhotoSize{Type: "y", Size: 99}}}}
	return m
}

func TestLiveFilenameFollowsTelethonNaming(t *testing.T) {
	dir := t.TempDir()
	mi := messageMedia(photoMsg(1, 1700000000))
	want := filepath.Join(dir, "photo_2023-11-14_22-13-20.jpg")
	if got := liveFilename(dir, mi); got != want {
		t.Fatalf("%q", got)
	}
	os.WriteFile(want, []byte("x"), 0o644)
	if got := liveFilename(dir, mi); got != filepath.Join(dir, "photo_2023-11-14_22-13-20 (1).jpg") {
		t.Fatalf("collision: %q", got)
	}
	doc := mediaInfo{Doc: &tg.Document{MimeType: "application/pdf", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "../evil/report.pdf"}}}}
	if got := liveFilename(dir, doc); got != filepath.Join(dir, "report.pdf") {
		t.Fatalf("%q", got)
	}
	voice := mediaInfo{Doc: &tg.Document{MimeType: "audio/ogg", Date: 1700000000, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true}}}}
	if got := liveFilename(dir, voice); got != filepath.Join(dir, "voice_2023-11-14_22-13-20.oga") {
		t.Fatalf("%q", got)
	}
	song := mediaInfo{Doc: &tg.Document{MimeType: "audio/mpeg", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Performer: "A", Title: "B"}}}}
	if got := liveFilename(dir, song); got != filepath.Join(dir, "A - B.mp3") {
		t.Fatalf("%q", got)
	}
}

func serveFile(e *env, content []byte) {
	e.f.on(&tg.UploadGetFileRequest{}, func(in bin.Encoder) (bin.Encoder, error) {
		r := in.(*tg.UploadGetFileRequest)
		if int(r.Offset) >= len(content) {
			return &tg.UploadFile{Type: &tg.StorageFileUnknown{}}, nil
		}
		end := int(r.Offset) + r.Limit
		if end > len(content) {
			end = len(content)
		}
		return &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: content[r.Offset:end]}, nil
	})
}

func TestHandleNewMessageDownloadsMedia(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	serveFile(e, []byte("JPEGDATA"))
	e.b.handleNewMessage(bg, photoMsg(9, 1700000000), entityMaps{})
	evs := e.b.PollOnce()
	if len(evs) != 1 {
		t.Fatalf("%+v", evs)
	}
	path := evs[0].Payload["attachment_id"].(string)
	if filepath.Base(path) != "photo_2023-11-14_22-13-20.jpg" || filepath.Dir(path) != e.media {
		t.Fatalf("%q", path)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "JPEGDATA" {
		t.Fatalf("%q", raw)
	}
	// A failed download falls back to the tgref (still queued).
	e.f.on(&tg.UploadGetFileRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("boom") })
	e.b.handleNewMessage(bg, photoMsg(10, 1700000001), entityMaps{})
	evs = e.b.PollOnce()
	if len(evs) != 1 || evs[0].Payload["attachment_id"] != "tgref:7:10" {
		t.Fatalf("%+v", evs)
	}
}

func TestAttachmentPath(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	if _, ok := e.b.AttachmentPath(bg, ""); ok {
		t.Fatal("empty")
	}
	local := writeFile(t, e.dir, "local.png", "x")
	if p, ok := e.b.AttachmentPath(bg, local); !ok || p != local {
		t.Fatal("existing file")
	}
	// A path from another machine resolves by basename inside the media dir.
	moved := writeFile(t, e.media, "report.pdf", "x")
	if p, ok := e.b.AttachmentPath(bg, "/old/host/tmp/report.pdf"); !ok || p != moved {
		t.Fatalf("%q %v", p, ok)
	}
	for _, bad := range []string{"/old/..", "..", "/old/host/missing.pdf", "/x/../"} {
		if p, ok := e.b.AttachmentPath(bg, bad); ok {
			t.Errorf("%q resolved to %q", bad, p)
		}
	}
	// Sent copies win over a download.
	sent := writeFile(t, e.media, "7-5-sent.jpg", "x")
	if p, ok := e.b.AttachmentPath(bg, "tgref:7:5"); !ok || p != sent {
		t.Fatalf("%q %v", p, ok)
	}
	// Lazy download of a document: <chat>-<msg>-<name>, then reused.
	doc := textMsg(6, &tg.PeerUser{UserID: 7}, "", false)
	doc.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, MimeType: "application/pdf", Size: 4,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "x.pdf"}}}}
	e.f.reply(&tg.MessagesGetMessagesRequest{}, &tg.MessagesMessages{Messages: []tg.MessageClass{doc}})
	serveFile(e, []byte("DATA"))
	p, ok := e.b.AttachmentPath(bg, "tgref:7:6")
	if !ok || p != filepath.Join(e.media, "7-6-x.pdf") {
		t.Fatalf("%q %v", p, ok)
	}
	if raw, _ := os.ReadFile(p); string(raw) != "DATA" {
		t.Fatalf("%q", raw)
	}
	before := e.f.count(&tg.MessagesGetMessagesRequest{})
	e.b.AttachmentPath(bg, "tgref:7:6")
	if e.f.count(&tg.MessagesGetMessagesRequest{}) != before+1 {
		// the lazy path always re-reads the message before reusing the file
		t.Fatal("unexpected request count")
	}
	// A photo without a name is stored as photo.jpg; a vanished or media-less message yields nothing.
	e.f.reply(&tg.MessagesGetMessagesRequest{}, &tg.MessagesMessages{Messages: []tg.MessageClass{photoMsg(8, 1700000000)}})
	if p, ok := e.b.AttachmentPath(bg, "tgref:7:8"); !ok || filepath.Base(p) != "7-8-photo.jpg" {
		t.Fatalf("%q %v", p, ok)
	}
	e.f.reply(&tg.MessagesGetMessagesRequest{}, &tg.MessagesMessages{Messages: []tg.MessageClass{textMsg(11, &tg.PeerUser{UserID: 7}, "text only", false)}})
	if _, ok := e.b.AttachmentPath(bg, "tgref:7:11"); ok {
		t.Fatal("no media")
	}
	e.f.reply(&tg.MessagesGetMessagesRequest{}, &tg.MessagesMessages{})
	if _, ok := e.b.AttachmentPath(bg, "tgref:7:12"); ok {
		t.Fatal("message gone")
	}
	e.f.on(&tg.MessagesGetMessagesRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("down") })
	if _, ok := e.b.AttachmentPath(bg, "tgref:7:13"); ok {
		t.Fatal("download failure")
	}
	if _, ok := New(Options{MediaDir: e.media}).AttachmentPath(bg, "tgref:7:14"); ok {
		t.Fatal("not connected")
	}
	if _, ok := e.b.AttachmentPath(bg, "tgref:bad"); ok {
		t.Fatal("bad ref")
	}
}

func TestAttachmentChunk(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	local := writeFile(t, e.dir, "f.bin", "0123456789")
	if d, _ := e.b.AttachmentChunk(bg, local, 2, 3); string(d) != "234" {
		t.Fatalf("%q", d)
	}
	if d, _ := e.b.AttachmentChunk(bg, local, -1, 4); string(d) != "6789" {
		t.Fatalf("tail %q", d)
	}
	if d, _ := e.b.AttachmentChunk(bg, local, 0, 0); d != nil {
		t.Fatal("zero length")
	}
	if d, _ := e.b.AttachmentChunk(bg, "/nope/file", 0, 4); d != nil {
		t.Fatal("missing")
	}
	// Remote document range, honouring the 4 KiB alignment of upload.getFile.
	content := make([]byte, 10000)
	for i := range content {
		content[i] = byte(i % 251)
	}
	doc := textMsg(6, &tg.PeerUser{UserID: 7}, "", false)
	doc.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, MimeType: "video/mp4", Size: int64(len(content))}}
	e.f.reply(&tg.MessagesGetMessagesRequest{}, &tg.MessagesMessages{Messages: []tg.MessageClass{doc}})
	serveFile(e, content)
	got, err := e.b.AttachmentChunk(bg, "tgref:7:6", 5000, 100)
	if err != nil || !bytes.Equal(got, content[5000:5100]) {
		t.Fatalf("range: %v len=%d", err, len(got))
	}
	got, _ = e.b.AttachmentChunk(bg, "tgref:7:6", -1, 300)
	if !bytes.Equal(got, content[len(content)-300:]) {
		t.Fatalf("tail len=%d", len(got))
	}
	got, _ = e.b.AttachmentChunk(bg, "tgref:7:6", 9900, 5000)
	if !bytes.Equal(got, content[9900:]) {
		t.Fatalf("clamped len=%d", len(got))
	}
	if d, _ := e.b.AttachmentChunk(bg, "tgref:7:6", 20000, 10); len(d) != 0 {
		t.Fatalf("past the end: %d", len(d))
	}
}

func TestProfilePhoto(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	e.f.reply(&tg.UsersGetUsersRequest{}, &tg.UserClassVector{Elems: []tg.UserClass{
		&tg.User{ID: 7, Photo: &tg.UserProfilePhoto{PhotoID: 12}},
	}})
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}
	serveFile(e, jpeg)
	data, ct, err := e.b.ProfilePhoto(bg, "7")
	if err != nil || !bytes.Equal(data, jpeg) || ct != "image/jpeg" {
		t.Fatalf("%v %q %v", data, ct, err)
	}
	loc := e.f.last(&tg.UploadGetFileRequest{}).(*tg.UploadGetFileRequest).Location.(*tg.InputPeerPhotoFileLocation)
	if !loc.Big || loc.PhotoID != 12 {
		t.Fatalf("%+v", loc)
	}
	if d, _, _ := e.b.ProfilePhoto(bg, "abc"); d != nil {
		t.Fatal("non numeric id")
	}
	e.f.reply(&tg.UsersGetUsersRequest{}, &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 7}}})
	if d, _, err := e.b.ProfilePhoto(bg, "7"); d != nil || err != nil {
		t.Fatal("no photo")
	}
	e.f.on(&tg.UsersGetUsersRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("x") })
	if d, _, err := e.b.ProfilePhoto(bg, "7"); d != nil || err != nil {
		t.Fatal("errors are swallowed")
	}
	if d, _, _ := New(Options{}).ProfilePhoto(bg, "7"); d != nil {
		t.Fatal("not connected")
	}
}

func TestFetchHistoryIngests(t *testing.T) {
	e := newEnv(t)
	e.contact(7, "Bob")
	m2 := textMsg(2, &tg.PeerUser{UserID: 7}, "second", false)
	m1 := textMsg(1, &tg.PeerUser{UserID: 7}, "first", true)
	e.f.reply(&tg.MessagesGetHistoryRequest{}, &tg.MessagesMessages{
		Messages: []tg.MessageClass{m2, m1, &tg.MessageEmpty{ID: 3}, textMsg(4, &tg.PeerUser{UserID: 7}, "", false)},
		Users:    []tg.UserClass{&tg.User{ID: 7, FirstName: "Bob", AccessHash: 3}},
	})
	got, err := e.b.FetchHistory(bg, "7", 20)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if *got[0].ID != "2" || *got[0].Text != "second" || *got[0].Sender != "Bob" || *got[0].Timestamp != 1700000002000 || *got[1].ID != "1" || !*got[1].IsMine {
		t.Fatalf("%+v %+v", got[0], got[1])
	}
	rows, _ := e.st.LoadCache(bg, "telegram")
	if len(rows["7"]) != 2 {
		t.Fatalf("%+v", rows)
	}
	// A second fetch is a no-op for the store; an edited text is applied.
	m2.Message = "second (edited)"
	e.b.FetchHistory(bg, "7", 20)
	rows, _ = e.st.LoadCache(bg, "telegram")
	if len(rows["7"]) != 2 {
		t.Fatal("duplicates after refetch")
	}
	for _, m := range rows["7"] {
		if *m.MsgID == "2" && (*m.Text != "second (edited)" || !m.Edited) {
			t.Fatalf("edit not applied: %+v", m)
		}
	}
	if n := e.b.FetchRecentHistory(bg, 20); n != 2 {
		t.Fatalf("recent %d", n)
	}
	if n, err := e.b.ResyncHistory(bg, 0); n != 1 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	if got, _ := e.b.FetchHistory(bg, "x", 5); got != nil {
		t.Fatal("bad id")
	}
	if got, _ := New(Options{}).FetchHistory(bg, "7", 5); got != nil {
		t.Fatal("not connected")
	}
}

func TestRefreshContactsNotConnected(t *testing.T) {
	res := New(Options{}).RefreshContacts(bg, true)
	if res.Errors != "not connected" || res.Protocol != "telegram" {
		t.Fatalf("%+v", res)
	}
}

func TestRefreshContactsEmitsUpdates(t *testing.T) {
	e := newEnv(t)
	e.b.RegisterContact(proto.Contact{ID: "7", DisplayName: "7", Protocol: "telegram", Extras: map[string]any{}})
	e.f.reply(&tg.MessagesGetDialogsRequest{}, &tg.MessagesDialogs{
		Dialogs:  []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 7}, TopMessage: 1}, &tg.Dialog{Peer: &tg.PeerUser{UserID: 8}, TopMessage: 2}},
		Messages: []tg.MessageClass{&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 7}, Date: 100}, &tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: 8}, Date: 200}},
		Users:    []tg.UserClass{&tg.User{ID: 7, FirstName: "Bob"}, &tg.User{ID: 8, FirstName: "Ann"}},
	})
	e.f.reply(&tg.ContactsGetContactsRequest{}, &tg.ContactsContacts{})
	res := e.b.RefreshContacts(bg, true)
	if res.Errors != "" || len(res.NewContacts) != 1 || res.NewContacts[0].ID != "8" || len(res.RenamedContacts) != 1 || res.RenamedContacts[0].DisplayName != "Bob" {
		t.Fatalf("%+v", res)
	}
	evs := e.b.PollOnce()
	if len(evs) != 2 || evs[0].Type != proto.EventContactUpdate || evs[0].Payload["display_name"] != "Ann" {
		t.Fatalf("%+v", evs)
	}
	e.f.on(&tg.MessagesGetDialogsRequest{}, func(bin.Encoder) (bin.Encoder, error) { return nil, errors.New("flood") })
	if res := e.b.RefreshContacts(bg, true); res.Errors == "" {
		t.Fatal("remote failure must be reported, nothing committed")
	}
}

func TestReceiveStreamsAndStops(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(bg)
	ch, err := e.b.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.b.handleTyping("7", &tg.SendMessageTypingAction{})
	select {
	case ev := <-ch:
		if ev.Type != proto.EventTyping {
			t.Fatal(ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel must close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not closed")
	}
}

func TestLoginGuards(t *testing.T) {
	e := newEnv(t)
	if ok, err := e.b.Complete2FA(bg, "pw"); ok || err != nil {
		t.Fatal("2FA without a pending login")
	}
	if e.b.Needs2FA() || e.b.NeedsPairing() || !e.b.IsConnected() {
		t.Fatal("state")
	}
	nb := New(Options{})
	if qr, err := nb.PairingQR(bg); qr != "" || err != nil {
		t.Fatalf("%q %v", qr, err)
	}
	if err := nb.Connect(bg); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("%v", err)
	}
	if _, err := nb.SignInCode(bg, "123"); err == nil {
		t.Fatal("sign in without a login")
	}
	if err := e.b.Connect(bg); err != nil {
		t.Fatal("injected client connects trivially")
	}
	if err := e.b.Disconnect(bg); err != nil {
		t.Fatal(err)
	}
	if p := (&Backend{}).Protocol(); p != "telegram" {
		t.Fatal(p)
	}
	if d, err := e.b.EnsureMediaDir(); err != nil || d != e.media {
		t.Fatalf("%q %v", d, err)
	}
}

func availReaction(emoji string) tg.AvailableReaction {
	d := &tg.Document{}
	return tg.AvailableReaction{Reaction: emoji, Title: emoji, StaticIcon: d, AppearAnimation: d, SelectAnimation: d,
		ActivateAnimation: d, EffectAnimation: d}
}
