package signal

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func envBackend(t *testing.T) *harness {
	h := newHarness(t)
	h.offline(contact("+391234567890", "Mario", "uuid-123"))
	return h
}

func TestEnvelopeIncomingText(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"+391234567890","sourceNumber":"+391234567890","sourceName":"Mario","timestamp":1700000000000,
		"dataMessage":{"message":"ciao","quote":{"id":1699999999000,"text":"  hi  "}}}`))
	if len(evs) != 1 {
		t.Fatal(evs)
	}
	p := evs[0].Payload
	if p["text"] != "ciao" || p["sender"] != "Mario" || p["is_mine"] != false || p["quote_text"] != "hi" ||
		p["quote_timestamp"] != int64(1699999999000) || p["timestamp"] != int64(1700000000000) || p["msg_type"] != "text" {
		t.Fatalf("%+v", p)
	}
	if _, has := p["id"]; has {
		t.Fatal("incoming messages carry no id")
	}
	// the contact is found by ACI alone
	evs = h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceUuid":"uuid-123","timestamp":5,"dataMessage":{"message":"x"}}`))
	if len(evs) != 1 || evs[0].ContactID != "+391234567890" {
		t.Fatal(evs)
	}
	// unknown contact -> nothing
	if evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"+000","timestamp":5,"dataMessage":{"message":"x"}}`)); len(evs) != 0 {
		t.Fatal(evs)
	}
}

func TestEnvelopeSyncSent(t *testing.T) {
	h := envBackend(t)
	for _, dest := range []string{`"destinationNumber":"+391234567890"`, `"destination":"+391234567890"`, `"destinationUuid":"uuid-123"`} {
		evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"`+testNumber+`","timestamp":1700000000500,"syncMessage":{"sentMessage":{`+dest+`,"timestamp":1700000000500,"message":"mine"}}}`))
		if len(evs) != 1 || evs[0].ContactID != "+391234567890" {
			t.Fatalf("%s: %+v", dest, evs)
		}
		p := evs[0].Payload
		if p["is_mine"] != true || p["sender"] != "You" || p["id"] != "1700000000500" || p["timestamp"] != int64(1700000000500) {
			t.Fatalf("%+v", p)
		}
	}
	// a sent envelope for an unknown destination is not attributed to its source
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"+391234567890","syncMessage":{"sentMessage":{"destinationNumber":"+000","timestamp":1,"message":"m"}}}`))
	if len(evs) != 0 {
		t.Fatal(evs)
	}
}

func TestEnvelopeAttachments(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","timestamp":10,"dataMessage":{"message":"look",
		"attachments":[{"contentType":"image/png","id":"a1.png"},{"contentType":"image/jpeg","id":"a2.jpg"},
		{"contentType":"audio/ogg","id":"a3","voiceNote":true},{"contentType":"application/pdf","id":"a4","filename":"doc.pdf"},
		{"contentType":"video/mp4","id":"a5"}]}}`))
	if len(evs) != 5 {
		t.Fatalf("%d events", len(evs))
	}
	p := func(i int, k string) any { return evs[i].Payload[k] }
	if p(0, "text") != "look" || p(0, "attachment_info") != "look" || p(0, "msg_type") != "image" || p(0, "media_kind") != "image" || p(0, "attachment_id") != "a1.png" {
		t.Fatalf("%+v", evs[0].Payload)
	}
	// later attachments get "<label>: <id>" so they are not collapsed by dedup
	if p(1, "text") != "🖼️ Image: a2.jpg" || p(1, "attachment_info") != "🖼️ Image" {
		t.Fatalf("%+v", evs[1].Payload)
	}
	if p(2, "media_kind") != "voice" || p(2, "msg_type") != "attachment" || p(2, "text") != "🎵 Audio: a3" {
		t.Fatalf("%+v", evs[2].Payload)
	}
	if p(3, "attachment_info") != "doc.pdf" || p(3, "media_kind") != "document" {
		t.Fatalf("%+v", evs[3].Payload)
	}
	if p(4, "attachment_info") != "🎬 Video" {
		t.Fatalf("%+v", evs[4].Payload)
	}
}

func TestEnvelopeSticker(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","timestamp":10,"dataMessage":{"sticker":{"packId":"abcdef0123456789","stickerId":7}}}`))
	if len(evs) != 1 {
		t.Fatal(evs)
	}
	p := evs[0].Payload
	if p["msg_type"] != "sticker" || p["media_kind"] != "sticker" || p["text"] != "Sticker #7 (pack:abcdef01…)" || p["attachment_info"] != p["text"] {
		t.Fatalf("%+v", p)
	}
	if _, has := p["attachment_id"]; has {
		t.Fatal("sticker dicts have no attachment_id")
	}
}

func TestEnvelopeQuoteMediaFallback(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","timestamp":10,"dataMessage":{"message":"re",
		"quote":{"targetSentTimestamp":55,"id":9,"attachments":[{"contentType":"image/png","filename":"p.png","id":"att-9"}]}}}`))
	p := evs[0].Payload
	if p["quote_text"] != "p.png — 🖼️ Immagine" || p["quote_timestamp"] != int64(55) || p["quote_attachment_id"] != "att-9" || p["quote_content_type"] != "image/png" {
		t.Fatalf("%+v", p)
	}
}

func TestEnvelopeQuoteThumbnail(t *testing.T) {
	h := envBackend(t)
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","timestamp":10,"dataMessage":{"message":"re",
		"quote":{"id":9,"attachments":[{"contentType":"image/png","thumbnail":"`+b64+`"}]}}}`))
	path, _ := evs[0].Payload["quote_attachment_path"].(string)
	if path == "" || !strings.HasPrefix(path, h.cfg.QuoteThumbsDir()) || !strings.HasSuffix(path, ".png") {
		t.Fatalf("%q", path)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, buf.Bytes()) {
		t.Fatal("thumbnail content")
	}
	// garbage is dropped silently
	evs = h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","timestamp":10,"dataMessage":{"message":"re",
		"quote":{"id":9,"attachments":[{"contentType":"image/png","thumbnail":"bm90LWFuLWltYWdl"}]}}}`))
	if evs[0].Payload["quote_attachment_path"] != nil {
		t.Fatal("invalid image must be ignored")
	}
}

func TestEnvelopeTypingAndReceipt(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","typingMessage":{"action":"STARTED"}}`))
	if len(evs) != 1 || evs[0].Type != proto.EventTyping || evs[0].ContactID != "+391234567890" || evs[0].Payload["action"] != "STARTED" {
		t.Fatal(evs)
	}
	if evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+39","typingMessage":{"action":"WEIRD"}}`)); len(evs) != 0 {
		t.Fatal(evs)
	}
	evs = h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"+391234567890","receiptMessage":{"isDelivery":true,"timestamps":[1,2]}}`))
	if len(evs) != 1 || evs[0].Type != proto.EventReceipt || evs[0].ContactID != "+391234567890" {
		t.Fatal(evs)
	}
	if r := evs[0].Payload["receipt"].(map[string]any); r["isDelivery"] != true {
		t.Fatal(r)
	}
}

func TestEnvelopeEdit(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","sourceName":"Mario","timestamp":2000,
		"editMessage":{"targetSentTimestamp":1000,"dataMessage":{"timestamp":2000,"message":"new text"}}}`))
	if len(evs) != 1 || evs[0].Type != proto.EventMessageEdit {
		t.Fatal(evs)
	}
	p := evs[0].Payload
	if p["edit_message_id"] != "1000" || p["text"] != "new text" || p["timestamp"] != int64(1000) || p["edit_timestamp"] != int64(2000) ||
		p["is_mine"] != false || p["sender"] != "Mario" || p["msg_type"] != "text" {
		t.Fatalf("%+v", p)
	}
	// our own edit from another device
	evs = h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"`+testNumber+`","timestamp":2000,"syncMessage":{"sentMessage":{"destinationNumber":"+391234567890",
		"editMessage":{"targetSentTimestamp":1000,"dataMessage":{"timestamp":2000,"message":"mine new"}}}}}`))
	if len(evs) != 1 || evs[0].Payload["is_mine"] != true || evs[0].Payload["sender"] != "You" {
		t.Fatalf("%+v", evs)
	}
	// edits that cannot be processed never fall through to a message
	for _, raw := range []string{
		`{"sourceNumber":"+391234567890","editMessage":{"dataMessage":{"message":"x"}}}`,                                                    // no target
		`{"sourceNumber":"+391234567890","editMessage":{"targetSentTimestamp":1,"dataMessage":{"message":""}}}`,                             // empty text
		`{"sourceNumber":"+391234567890","editMessage":{"targetSentTimestamp":1,"dataMessage":{"message":"x","attachments":[{"id":"a"}]}}}`, // media
		`{"sourceNumber":"+000","editMessage":{"targetSentTimestamp":1,"dataMessage":{"message":"x"}}}`,                                     // unknown contact
		`{"sourceNumber":"+391234567890","dataMessage":{"message":"hello"},"editMessage":{}}`,                                               // present but empty
	} {
		if evs := h.b.EnvelopeToEvents(jsonEnv(t, raw)); len(evs) != 0 {
			t.Errorf("%s -> %+v", raw, evs)
		}
	}
}

func TestEnvelopeReaction(t *testing.T) {
	h := envBackend(t)
	evs := h.b.EnvelopeToEvents(jsonEnv(t, `{"sourceNumber":"+391234567890","sourceName":"Mario","timestamp":3000,
		"dataMessage":{"reaction":{"emoji":"👍","targetSentTimestamp":1000,"isRemove":false}}}`))
	if len(evs) != 1 || evs[0].Type != proto.EventReactionUpdate {
		t.Fatal(evs)
	}
	p := evs[0].Payload
	if p["target_message_id"] != "1000" || p["target_timestamp"] != int64(1000) || p["mode"] != "delta" || p["emoji"] != "👍" ||
		p["is_remove"] != false || p["author"] != "Mario" || p["author_key"] != "+391234567890" || p["is_mine"] != false || p["timestamp"] != int64(3000) {
		t.Fatalf("%+v", p)
	}
	evs = h.b.EnvelopeToEvents(jsonEnv(t, `{"source":"`+testNumber+`","timestamp":3100,"syncMessage":{"sentMessage":{"destinationNumber":"+391234567890",
		"reaction":{"emoji":"","targetSentTimestamp":1000,"isRemove":true}}}}`))
	p = evs[0].Payload
	if p["is_mine"] != true || p["author"] != "You" || p["author_key"] != "me" || p["is_remove"] != true || p["emoji"] != "" {
		t.Fatalf("%+v", p)
	}
	// missing target / empty emoji without remove -> dropped, not a message
	for _, raw := range []string{
		`{"sourceNumber":"+391234567890","timestamp":1,"dataMessage":{"message":"x","reaction":{"emoji":"👍"}}}`,
		`{"sourceNumber":"+391234567890","timestamp":1,"dataMessage":{"reaction":{"emoji":"","targetSentTimestamp":5}}}`,
	} {
		if evs := h.b.EnvelopeToEvents(jsonEnv(t, raw)); len(evs) != 0 {
			t.Errorf("%s -> %+v", raw, evs)
		}
	}
}

func TestQuoteHelpers(t *testing.T) {
	if quoteTimestamp(nil) != nil || quoteText(nil) != "" {
		t.Fatal("nil quote")
	}
	if ts := quoteTimestamp(map[string]any{"id": "42"}); ts == nil || *ts != 42 {
		t.Fatal("string id")
	}
	if quoteTimestamp(map[string]any{"id": "abc"}) != nil {
		t.Fatal("garbage id")
	}
	if quoteText(map[string]any{"attachments": []any{map[string]any{"contentType": "audio/ogg"}}}) != "🎵 Audio" {
		t.Fatal("audio placeholder")
	}
	if quoteText(map[string]any{"attachments": []any{}}) != "" {
		t.Fatal("empty quote")
	}
}

func TestIsCaptionLike(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, "hello there": true, "photo.png": false, "Image": false, "🖼️ Immagine": false,
		"x.jpg — 🖼️ Immagine": false, "image/png": false, "media:abc": false, "media: two words": true, "ciao": true,
	} {
		if got := isCaptionLike(in); got != want {
			t.Errorf("isCaptionLike(%q)=%v want %v", in, got, want)
		}
	}
}

func TestPlaceholdersMatchModels(t *testing.T) {
	// The literals must stay byte-identical with models.py (the TUI's domain
	// module, still installed alongside the Go server).
	py, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "models.py"))
	if err != nil {
		t.Skip("models.py not available")
	}
	for _, v := range mediaQuotePlaceholders {
		if !strings.Contains(string(py), `"`+v+`"`) {
			t.Errorf("placeholder %q not found in models.py", v)
		}
	}
}
