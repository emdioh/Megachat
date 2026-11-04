package signal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func TestSendAttachmentsBatch(t *testing.T) {
	h := envBackend(t)
	h.d.handle("send", func(map[string]any) any { return map[string]any{"timestamp": 8000} })
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.png"), "AAA")
	writeFile(t, filepath.Join(src, "b.pdf"), "BBB")
	atts := []proto.Attachment{
		{Path: filepath.Join(src, "a.png"), Caption: "two files", MimeType: "image/png", Filename: "a.png"},
		{Path: filepath.Join(src, "b.pdf"), MimeType: "application/pdf", Filename: "b.pdf"},
	}
	ids, err := h.b.SendAttachments(bg, "+1", atts, "batch-1", proto.Quote{})
	if err != nil || len(ids) != 1 || ids[0] != "8000" {
		t.Fatal(ids, err)
	}
	p := h.d.callsOf("send")[0].Params
	list := p["attachments"].([]any)
	dir := h.cfg.SignalCLIAttachmentsDir()
	if len(list) != 2 || list[0] != filepath.Join(dir, "a.png") || p["message"] != "two files" {
		t.Fatalf("%v", p)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.pdf")); string(b) != "BBB" {
		t.Fatal("persistent copy missing")
	}
	got := rows(t, h, "+1")
	if len(got) != 2 || store.Deref(got[0].MsgID) != "8000" || got[0].BatchIndex == nil || *got[0].BatchIndex != 0 ||
		*got[1].BatchIndex != 1 || store.Deref(got[1].BatchID) != "batch-1" || store.Deref(got[1].MsgType) != "attachment" {
		t.Fatalf("%+v", got)
	}
	// the echo of the batch does not duplicate the mirror rows
	echo := msg("", true, map[string]any{"id": "8000", "msg_type": "image", "attachment_id": "a.png"})
	ingest(t, h, "+1", echo, 8000)
	if n := len(rows(t, h, "+1")); n != 2 {
		t.Fatal(n)
	}
	// sent-mirror notification
	if err := h.b.EnqueueSentNotification(bg, "+1", "8000", 8000, "batch-1"); err != nil {
		t.Fatal(err)
	}
	ev := h.b.PollOnce()[0]
	if ev.Type != proto.EventSentMirror || ev.Payload["batch_id"] != "batch-1" || ev.Payload["id"] != "8000" {
		t.Fatalf("%+v", ev)
	}
	// failing send removes the copies
	h.d.handle("send", func(map[string]any) any { return rpcFail("no") })
	if _, err := h.b.SendAttachments(bg, "+1", atts[:1], "b2", proto.Quote{}); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "a (1).png")); err == nil {
		t.Fatal("copy of a failed send must be removed")
	}
	if ids, err := h.b.SendAttachments(bg, "+1", nil, "", proto.Quote{}); err != nil || len(ids) != 0 {
		t.Fatal("empty batch")
	}
}

func TestSendAttachmentAndMirror(t *testing.T) {
	h := envBackend(t)
	h.d.handle("send", func(map[string]any) any { return map[string]any{"timestamp": 9000} })
	src := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "JPG")
	id, err := h.b.SendAttachment(bg, "+1", proto.Attachment{Path: src, Caption: "cap", MimeType: "image/jpeg", Filename: "photo.jpg"}, proto.Quote{})
	if err != nil || id != "9000" {
		t.Fatal(id, err)
	}
	if err := h.b.EnqueueSentMessage(bg, proto.SentMessage{ContactID: "+1", MessageID: id, Text: "cap", AttachmentPath: src,
		MimeType: "image/jpeg", Filename: "photo.jpg", QuoteContentType: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	ev := h.b.PollOnce()[0]
	p := ev.Payload
	if ev.Type != proto.EventMessage || p["attachment_id"] != "photo.jpg" || p["msg_type"] != "image" || p["text"] != "" ||
		p["attachment_info"] != "cap" || p["media_kind"] != "image" || p["quote_text"] != "🎵 Audio" || p["timestamp"] != int64(9000) {
		t.Fatalf("%+v", p)
	}
	// the registered copy is reused, not copied again
	entries, _ := os.ReadDir(h.cfg.SignalCLIAttachmentsDir())
	if len(entries) != 1 {
		t.Fatal(len(entries))
	}
	// text-only mirror
	_ = h.b.EnqueueSentMessage(bg, proto.SentMessage{ContactID: "+1", MessageID: "abc", Text: "plain"})
	p = h.b.PollOnce()[0].Payload
	if p["msg_type"] != "text" || p["text"] != "plain" || p["attachment_id"] != nil || p["media_kind"] != nil {
		t.Fatalf("%+v", p)
	}
	// missing source file surfaces the error (manager retries without it)
	if err := h.b.EnqueueSentMessage(bg, proto.SentMessage{ContactID: "+1", MessageID: "1", AttachmentPath: "/nonexistent/x.png", Filename: "x.png"}); err == nil {
		t.Fatal("want error")
	}
}
