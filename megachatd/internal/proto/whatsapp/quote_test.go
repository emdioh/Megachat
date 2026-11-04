package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	gproto "google.golang.org/protobuf/proto"
)

const (
	quoteChat = "120363000000000000@g.us"
	quoteID   = "false_120363000000000000@g.us_3EB0AAAA_20508603076702@lid"
	// What the web client sends as quote_message for a media reply.
	badQuoteName = "true_120363000000000000@g.us_3EB0AAAA_20508603076702@lid.jpeg"
)

func writePNG(t *testing.T, path string, w, h int) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replyCtx(t *testing.T, e *env, q proto.Quote) *waE2E.ContextInfo {
	t.Helper()
	ci := e.b.contextFor(jid("120363000000000000", "g.us"), quoteChat, q)
	if ci == nil {
		t.Fatal("no context info")
	}
	if ci.GetStanzaID() != "3EB0AAAA" || ci.GetParticipant() != "20508603076702@lid" {
		t.Fatalf("stanza/participant: %v / %v", ci.GetStanzaID(), ci.GetParticipant())
	}
	return ci
}

func TestContextForText(t *testing.T) {
	e := newEnv(t)
	seed(e.b, quoteChat, &entry{ID: quoteID, Text: "hello there", MsgType: "text"})
	ci := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: "client text"})
	if ci.GetQuotedMessage().GetConversation() != "hello there" {
		t.Fatalf("the cached text wins: %v", ci.GetQuotedMessage())
	}
}

func TestContextForImageWithoutCaption(t *testing.T) {
	e := newEnv(t)
	writePNG(t, filepath.Join(e.b.mediaDir, "pic.png"), 400, 200)
	seed(e.b, quoteChat, &entry{ID: quoteID, MsgType: "image", MediaKind: "image", ContentType: "image/png",
		AttachmentID: "pic.png", AttachmentInfo: badQuoteName})
	for _, clientText := range []string{badQuoteName, "pic.png", "Allegato", ""} {
		ci := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: clientText})
		qm := ci.GetQuotedMessage()
		if qm.GetConversation() != "" || qm.GetImageMessage() == nil {
			t.Fatalf("client text %q: want a typed image quote, got %v", clientText, qm)
		}
		im := qm.GetImageMessage()
		if im.Caption != nil || im.GetMimetype() != "image/png" {
			t.Fatalf("image without caption: %v", im)
		}
		th, err := jpeg.Decode(bytes.NewReader(im.GetJPEGThumbnail()))
		if err != nil {
			t.Fatalf("thumbnail: %v", err)
		}
		if b := th.Bounds(); b.Dx() != 72 || b.Dy() != 36 {
			t.Fatalf("thumbnail size %v", b)
		}
	}
	// No local file: still a typed quote, without thumbnail.
	seed(e.b, quoteChat, &entry{ID: "false_120363000000000000@g.us_3EB0BBBB_20508603076702@lid", MsgType: "image", MediaKind: "image", AttachmentID: "gone.jpg"})
	ci := e.b.contextFor(jid("120363000000000000", "g.us"), quoteChat, proto.Quote{
		ReplyToMessageID: "false_120363000000000000@g.us_3EB0BBBB_20508603076702@lid", Message: "gone.jpg"})
	if im := ci.GetQuotedMessage().GetImageMessage(); im == nil || im.JPEGThumbnail != nil || im.GetMimetype() != "image/jpeg" {
		t.Fatalf("no local file: %v", ci.GetQuotedMessage())
	}
}

func TestContextForImageWithCaption(t *testing.T) {
	e := newEnv(t)
	seed(e.b, quoteChat, &entry{ID: quoteID, MsgType: "image", MediaKind: "image", ContentType: "image/jpeg",
		AttachmentID: "x.jpg", AttachmentInfo: "Sunset at the lake"})
	im := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: "Sunset at the lake"}).GetQuotedMessage().GetImageMessage()
	if im == nil || im.GetCaption() != "Sunset at the lake" || im.GetMimetype() != "image/jpeg" {
		t.Fatalf("image with caption: %v", im)
	}
}

func TestContextForMediaKinds(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		row   entry
		check func(*waE2E.Message) bool
	}{
		{entry{MsgType: "attachment", MediaKind: "video", ContentType: "video/mp4", AttachmentID: "v.mp4", Text: "clip!"},
			func(m *waE2E.Message) bool {
				return m.GetVideoMessage().GetCaption() == "clip!" && m.GetVideoMessage().GetMimetype() == "video/mp4"
			}},
		{entry{MsgType: "attachment", MediaKind: "voice", ContentType: "audio/ogg; codecs=opus", AttachmentID: "v.ogg"},
			func(m *waE2E.Message) bool {
				return m.GetAudioMessage().GetPTT() && m.GetAudioMessage().GetMimetype() != ""
			}},
		{entry{MsgType: "attachment", MediaKind: "audio", ContentType: "audio/mpeg", AttachmentID: "a.mp3"},
			func(m *waE2E.Message) bool { return m.GetAudioMessage() != nil && !m.GetAudioMessage().GetPTT() }},
		{entry{MsgType: "sticker", MediaKind: "sticker", ContentType: "image/webp", AttachmentID: "s.webp"},
			func(m *waE2E.Message) bool { return m.GetStickerMessage().GetMimetype() == "image/webp" }},
		{entry{MsgType: "attachment", MediaKind: "document", ContentType: "application/pdf", AttachmentID: badQuoteName + ".pdf",
			AttachmentInfo: "fattura 2024.pdf", Text: "ecco"},
			func(m *waE2E.Message) bool {
				d := m.GetDocumentMessage()
				return d.GetFileName() == "fattura 2024.pdf" && d.GetCaption() == "ecco" && d.GetMimetype() == "application/pdf"
			}},
		// The internal attachment id is never the file name.
		{entry{MsgType: "attachment", MediaKind: "document", ContentType: "application/pdf", AttachmentID: "internal-id.pdf",
			AttachmentInfo: "internal-id.pdf"},
			func(m *waE2E.Message) bool {
				return m.GetDocumentMessage() != nil && m.GetDocumentMessage().FileName == nil
			}},
	}
	for i, c := range cases {
		row := c.row
		row.ID = quoteID
		e.b.cache = map[string][]*entry{quoteChat: {&row}}
		qm := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: badQuoteName}).GetQuotedMessage()
		if qm.GetConversation() != "" || !c.check(qm) {
			t.Errorf("case %d (%s): %v", i, row.MediaKind, qm)
		}
	}
}

func addStoreRow(t *testing.T, e *env, m store.NewMessage) {
	t.Helper()
	m.Protocol = proto.ProtocolWhatsApp
	if _, _, err := e.st.AddMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func sp(s string) *string { return &s }

func TestContextForStoreFallback(t *testing.T) {
	e := newEnv(t)
	// Persisted under the serialized id with another participant segment form.
	addStoreRow(t, e, store.NewMessage{ContactNumber: quoteChat, Timestamp: 1000, MsgType: "attachment",
		MsgID: sp(quoteID), MediaKind: sp("document"), ContentType: sp("application/pdf"),
		AttachmentID: sp("abc123.pdf"), AttachmentInfo: sp("contratto.pdf"), Text: "da firmare"})
	if len(cached(e.b, quoteChat)) != 0 {
		t.Fatal("the cache must be empty for this test")
	}
	d := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: "abc123.pdf"}).GetQuotedMessage().GetDocumentMessage()
	if d == nil || d.GetFileName() != "contratto.pdf" || d.GetCaption() != "da firmare" {
		t.Fatalf("store fallback: %v", d)
	}
	// A different id of the chat is not matched.
	ci := e.b.contextFor(jid("120363000000000000", "g.us"), quoteChat, proto.Quote{
		ReplyToMessageID: "false_120363000000000000@g.us_3EB0CCCC_20508603076702@lid", Message: "abc123.pdf"})
	if ci.GetQuotedMessage() != nil {
		t.Fatalf("unknown id must not borrow another row: %v", ci.GetQuotedMessage())
	}
}

func TestContextForUnknownQuote(t *testing.T) {
	e := newEnv(t)
	for _, text := range []string{badQuoteName, "true_391234567890@c.us_3EB0A01", "photo.jpeg", "Allegato", ""} {
		ci := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: text})
		if ci.QuotedMessage != nil {
			t.Errorf("text %q: want no QuotedMessage, got %v", text, ci.QuotedMessage)
		}
	}
	// Real text of an unknown row is kept (the TUI path).
	ci := replyCtx(t, e, proto.Quote{ReplyToMessageID: quoteID, Message: "what the user wrote"})
	if ci.GetQuotedMessage().GetConversation() != "what the user wrote" {
		t.Fatalf("real text dropped: %v", ci.QuotedMessage)
	}
}

// capturedCtx returns the ContextInfo of the last sent message of any kind.
func capturedCtx(m *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetContextInfo()
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetContextInfo()
	}
	return nil
}

// Regression (mc-zao.5.5): a web reply to a picture sends the attachment name
// as quote_message; the recipient must not see it as the quoted text.
func TestMessagesSendReplyToPictureThroughCore(t *testing.T) {
	e := newEnv(t)
	writePNG(t, filepath.Join(e.b.mediaDir, "pic.png"), 100, 100)
	addStoreRow(t, e, store.NewMessage{ContactNumber: quoteChat, Timestamp: 1000, MsgType: "image",
		MsgID: sp(quoteID), MediaKind: sp("image"), ContentType: sp("image/jpeg"),
		AttachmentID: sp("pic.png"), AttachmentInfo: sp(badQuoteName)})
	e.b.RegisterContact(*newContact(quoteChat, "Group", map[string]any{"is_group": true}))

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := proto.New(proto.WithLogger(log))
	if err := mgr.Register(e.b); err != nil {
		t.Fatal(err)
	}
	c := core.New(e.cfg, e.st, mgr, bus.New(bus.WithLogger(log)), core.WithLogger(log),
		core.WithSleep(func(context.Context, time.Duration) error { return nil }))
	reg := ops.NewRegistry()
	c.RegisterOps(reg)

	quote, _ := json.Marshal(badQuoteName)
	body := `{"protocol":"whatsapp","contact_id":"` + quoteChat + `","text":"nice one","client_msg_id":"q1",` +
		`"reply_to_message_id":"` + quoteID + `","quote_message":` + string(quote) + `,"quote_timestamp":1000}`
	res, err := reg.Call(context.Background(), "messages.send", json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	var out struct {
		OK         bool     `json:"ok"`
		MessageIDs []string `json:"message_ids"`
	}
	if json.Unmarshal(raw, &out) != nil || !out.OK || len(out.MessageIDs) != 1 || out.MessageIDs[0] == "" {
		t.Fatalf("response: %s", raw)
	}
	s := lastSent(t, e)
	ext := s.Msg.GetExtendedTextMessage()
	ci := ext.GetContextInfo()
	if ext.GetText() != "nice one" || ci.GetStanzaID() != "3EB0AAAA" || ci.GetParticipant() != "20508603076702@lid" {
		t.Fatalf("sent: %v", s.Msg)
	}
	qm := ci.GetQuotedMessage()
	if qm.GetConversation() != "" || qm.GetImageMessage() == nil || qm.GetImageMessage().Caption != nil ||
		len(qm.GetImageMessage().GetJPEGThumbnail()) == 0 {
		t.Fatalf("the recipient must see a captionless image quote, got %v", qm)
	}
	if bytes.Contains([]byte(s.Msg.String()), []byte("jpeg@")) || bytes.Contains([]byte(s.Msg.String()), []byte(".jpeg")) {
		t.Fatalf("the attachment name leaked into the message: %s", s.Msg)
	}
	if out.MessageIDs[0] != "true_"+quoteChat+"_"+string(s.Extra.ID)+"_555555@lid" {
		t.Fatalf("message_ids %v do not carry the sent id", out.MessageIDs)
	}
	// The attachment path goes through the same context.
	img := writeFile(t, e.dir, "up.png", "PNG")
	if _, err := e.b.SendAttachment(ctx, quoteChat, proto.Attachment{Path: img, MimeType: "image/png"},
		proto.Quote{ReplyToMessageID: quoteID, Message: badQuoteName}); err != nil {
		t.Fatal(err)
	}
	aci := capturedCtx(lastSent(t, e).Msg)
	if aci.GetQuotedMessage().GetConversation() != "" || aci.GetQuotedMessage().GetImageMessage() == nil {
		t.Fatalf("attachment reply: %v", aci.GetQuotedMessage())
	}
}

func TestIncomingQuoteText(t *testing.T) {
	// A typed image quote without caption shows the typed placeholder.
	got, ok := quoteText(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: gproto.String("image/jpeg")}})
	if !ok || got != mediaQuotePlaceholder("image", "") {
		t.Fatalf("typed image quote: %q", got)
	}
	// A legacy text quote carrying a serialized attachment name is not shown.
	got, ok = quoteText(&waE2E.Message{Conversation: gproto.String(badQuoteName)})
	if !ok || got != mediaQuotePlaceholder("attachment", "") {
		t.Fatalf("legacy name quote: %q", got)
	}
	got, _ = quoteText(&waE2E.Message{Conversation: gproto.String("plain")})
	if got != "plain" {
		t.Fatalf("plain quote: %q", got)
	}
}
