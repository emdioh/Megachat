package messages

import (
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

const t0 = int64(1_700_000_000_000)

const (
	sig     = "+39111"
	waLid   = "123@lid"
	waGhost = "391@c.us"
	waGrp   = "g1@g.us"
	tgGrp   = "tg-group"
)

func at(n int64) int64 { return t0 + n }

// pageFixture is a rich database exercising the whole row builder.
func pageFixture(cfg *config.Config) []row {
	thumbs := filepath.Join(cfg.QuoteThumbsDir(), "q12.jpg")
	orig := filepath.Join(cfg.DataDir(), "orig", "quoted.jpg")
	in := func(r row) row { r["is_mine"] = 0; return r }
	out := func(r row) row { r["is_mine"] = 1; return r }
	rows := []row{
		// --- Signal 1:1 ---
		in(row{"contact_number": sig, "text": "ciao", "sender": sig, "timestamp": at(10000), "msg_id": "m1", "status": "read", "read": 1}),
		out(row{"contact_number": sig, "text": "hi", "timestamp": at(20000), "msg_id": "m2", "status": "delivered", "read": 1, "edited": 1}),
		out(row{"contact_number": sig, "text": "pending", "timestamp": at(30000), "status": "pending"}),
		out(row{"contact_number": sig, "text": "sent no id", "timestamp": at(40000), "status": "sent"}),
		in(row{"contact_number": sig, "text": "", "sender": sig, "timestamp": at(45000), "msg_id": "m5b", "msg_type": "image", "media_kind": "image",
			"attachment_id": "uuid-1303", "attachment_info": "IMG_1303.jpg", "content_type": "image/jpeg"}),
		in(row{"contact_number": sig, "text": "img1.jpg", "sender": sig, "timestamp": at(50000), "msg_id": "m5", "msg_type": "image", "media_kind": "image",
			"attachment_id": "img1.jpg", "attachment_info": "img1.jpg", "content_type": "image/jpeg"}),
		in(row{"contact_number": sig, "text": "Look at this", "sender": sig, "timestamp": at(60000), "msg_id": "m6", "msg_type": "image", "media_kind": "image",
			"attachment_id": "img2.png", "attachment_info": "img2.png"}),
		in(row{"contact_number": sig, "text": "doc.pdf: report", "sender": sig, "timestamp": at(70000), "msg_id": "m7", "msg_type": "attachment", "media_kind": "document",
			"attachment_id": "doc.pdf?x=1", "attachment_info": "doc.pdf"}),
		in(row{"contact_number": sig, "text": "", "sender": sig, "timestamp": at(80000), "msg_id": "m8", "msg_type": "voice", "media_kind": "voice",
			"attachment_id": "voice1.oga", "content_type": "audio/ogg", "attachment_info": "Great voice note"}),
		in(row{"contact_number": sig, "text": "\U0001f5bc️ Immagine", "sender": sig, "timestamp": at(90000), "msg_id": "m9", "msg_type": "attachment", "media_kind": "image",
			"attachment_id": "file.tif?v=2"}),
		in(row{"contact_number": sig, "text": "", "sender": sig, "timestamp": at(100000), "msg_id": "m10", "msg_type": "attachment", "media_kind": "document",
			"attachment_id": "weird.bin", "attachment_info": "weird.bin"}),
		// --- quote thumbnails ---
		out(row{"contact_number": sig, "text": "reply 1", "timestamp": at(110000), "msg_id": "m11", "status": "read",
			"quote_text": "photo.jpg — \U0001f5bc️ Immagine", "quote_timestamp": at(50000), "quote_author": sig}),
		out(row{"contact_number": sig, "text": "reply 2", "timestamp": at(120000), "msg_id": "m12", "status": "sent",
			"quote_text": "x", "quote_attachment_path": thumbs, "quote_content_type": "image/jpeg"}),
		out(row{"contact_number": sig, "text": "reply 3", "timestamp": at(130000), "msg_id": "m13", "status": "sent",
			"quote_text": "y", "quote_attachment_path": orig, "quote_content_type": "image/jpeg"}),
		out(row{"contact_number": sig, "text": "reply 4", "timestamp": at(140000), "msg_id": "m14", "status": "failed",
			"quote_text": "\U0001f3ac Video", "quote_attachment_id": "dir/q v.mp4", "quote_content_type": "video/mp4"}),
		out(row{"contact_number": sig, "text": "reply 5", "timestamp": at(150000), "msg_id": "m15", "status": "sent",
			"quote_text": "IMG_1303.jpg — \U0001f5bc️ Immagine"}),
		out(row{"contact_number": sig, "text": "reply 6", "timestamp": at(160000), "msg_id": "m16", "status": "sent",
			"quote_text": "\U0001f5bc️ Immagine", "quote_timestamp": at(50001)}),
		out(row{"contact_number": sig, "text": "reply 7", "timestamp": at(170000), "msg_id": "m17", "status": "sent",
			"quote_text": "\U0001f3ac Video", "quote_timestamp": at(10000)}),
		out(row{"contact_number": sig, "text": "reply 8", "timestamp": at(180000), "msg_id": "m18", "status": "sent",
			"quote_text": "x", "quote_content_type": "image/png", "reply_to_message_id": "m5"}),
		in(row{"contact_number": sig, "text": "legacy", "sender": sig, "timestamp": at(185000), "msg_id": "m19b",
			"quote_text": "z", "quote_attachment_id": "old/legacy.WEBP"}),
		in(row{"contact_number": sig, "text": "Security code changed", "timestamp": at(190000), "msg_id": "m19", "msg_type": "system"}),
		// multi-attachment batch: same msg_id, distinct attachments, kept apart
		out(row{"contact_number": sig, "text": "", "timestamp": at(200000), "msg_id": "mb", "status": "sent", "msg_type": "image", "media_kind": "image",
			"attachment_id": "b0.jpg", "content_type": "image/jpeg", "batch_id": "b1", "batch_index": 0}),
		out(row{"contact_number": sig, "text": "", "timestamp": at(200000), "msg_id": "mb", "status": "sent", "msg_type": "image", "media_kind": "image",
			"attachment_id": "b1.jpg", "content_type": "image/jpeg", "batch_id": "b1", "batch_index": 1}),
		// twins: caption on a text row + media row; two real captions; status merge
		in(row{"contact_number": sig, "text": "caption here", "sender": sig, "timestamp": at(210000), "msg_id": "tw1"}),
		in(row{"contact_number": sig, "text": "", "sender": sig, "timestamp": at(210000), "msg_id": "tw1", "msg_type": "image", "media_kind": "image",
			"attachment_id": "tw1.jpg", "attachment_info": "tw1.jpg", "content_type": "image/jpeg"}),
		in(row{"contact_number": sig, "text": "first", "sender": sig, "timestamp": at(220000), "msg_id": "tw2", "msg_type": "image", "media_kind": "image",
			"attachment_id": "tw2.jpg", "content_type": "image/jpeg"}),
		in(row{"contact_number": sig, "text": "second", "sender": sig, "timestamp": at(220000), "msg_id": "tw2", "msg_type": "image", "media_kind": "image",
			"attachment_id": "tw2.jpg", "content_type": "image/jpeg"}),
		out(row{"contact_number": sig, "text": "dup", "sender": "+39000@x", "timestamp": at(230000), "msg_id": "tw3", "status": "sent"}),
		out(row{"contact_number": sig, "text": "dup", "sender": "Me", "timestamp": at(230000), "msg_id": "tw3", "status": "delivered", "edited": 1,
			"quote_text": "filled", "quote_timestamp": at(10000)}),
		in(row{"contact_number": sig, "text": "a<b> & é \"q\" ☃ \U0001f600", "sender": sig, "timestamp": at(240000), "msg_id": "m22"}),
		in(row{"contact_number": "+39222", "text": "other chat", "sender": "+39222", "timestamp": at(15000), "msg_id": "o1"}),

		// --- WhatsApp 1:1 (ghost @c.us + active @lid) ---
		in(row{"protocol": "whatsapp", "contact_number": waGhost, "text": "old hello", "sender": waGhost, "timestamp": at(1000), "msg_id": "W1"}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "old hello", "sender": waLid, "timestamp": at(1000), "msg_id": "W1"}),
		in(row{"protocol": "whatsapp", "contact_number": waGhost, "text": "only on ghost", "sender": waGhost, "timestamp": at(2000), "msg_id": "W2"}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "", "sender": waLid, "timestamp": at(3000), "msg_id": "W3", "msg_type": "image", "media_kind": "image"}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "", "sender": waLid, "timestamp": at(4000), "msg_id": "bad id", "msg_type": "video", "media_kind": "video"}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "Media: https://example.com/x.jpg", "sender": waLid, "timestamp": at(5000), "msg_id": "W5",
			"msg_type": "image", "media_kind": "image", "attachment_id": "x.jpg", "attachment_info": "x.jpg", "content_type": "image/jpeg"}),
		out(row{"protocol": "whatsapp", "contact_number": waLid, "text": "mine", "timestamp": at(6000), "msg_id": "true_123@lid_AAA", "status": "sent"}),
		in(row{"protocol": "whatsapp", "contact_number": waGhost, "text": "same", "sender": waGhost, "timestamp": at(7000)}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "same", "sender": waLid, "timestamp": at(7000)}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "Media: 12345", "sender": waLid, "timestamp": at(8000), "msg_id": "W8", "msg_type": "document",
			"media_kind": "document", "attachment_id": "12345", "attachment_info": "report.pdf"}),
		in(row{"protocol": "whatsapp", "contact_number": waLid, "text": "Nota vocale", "sender": waLid, "timestamp": at(9000), "msg_id": "W9", "msg_type": "voice",
			"media_kind": "voice", "attachment_info": "Nota vocale"}),

		// --- WhatsApp group ---
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "hi all", "sender": "111@lid", "timestamp": at(100), "msg_id": "G1"}),
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "me too", "sender": "222@c.us", "timestamp": at(200), "msg_id": "G2"}),
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "quoting", "sender": "333@lid", "timestamp": at(300), "msg_id": "G3",
			"quote_text": "hi all", "quote_timestamp": at(100), "quote_author": "111@lid"}),
		// G4/G6 deliberately keep their @lid: for an unresolved @lid the row
		// builder diverges from Python (see lidPlaceholderFields in
		// parity_test.go), and dropping that here would lose the parity
		// coverage of the divergence instead of pinning it.
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "who", "sender": "Bob", "timestamp": at(400), "msg_id": "G4",
			"quote_text": "hello", "quote_timestamp": at(500), "quote_author": "999@lid"}),
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "pushname", "sender": "Zed", "timestamp": at(500), "msg_id": "G5"}),
		in(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "stranger", "sender": "555@lid", "timestamp": at(600), "msg_id": "G6",
			"quote_text": "q", "quote_author": "444@c.us"}),
		out(row{"protocol": "whatsapp", "contact_number": waGrp, "text": "my message", "timestamp": at(700), "msg_id": "G7", "status": "read"}),

		// --- Telegram ---
		in(row{"protocol": "telegram", "contact_number": "t1", "text": "tg hello", "sender": "Anna", "timestamp": at(10), "msg_id": "42"}),
		out(row{"protocol": "telegram", "contact_number": "t1", "text": "tg reply", "timestamp": at(20), "msg_id": "43", "status": "sent",
			"quote_text": "tg hello", "quote_timestamp": at(10), "quote_author": "Anna", "reply_to_message_id": "42"}),
		in(row{"protocol": "telegram", "contact_number": tgGrp, "text": "from anna", "sender": "Anna", "timestamp": at(30), "msg_id": "50",
			"quote_author": "Boris", "quote_text": "q", "quote_timestamp": at(31)}),
		in(row{"protocol": "telegram", "contact_number": tgGrp, "text": "from boris", "sender": "Boris", "timestamp": at(31), "msg_id": "51"}),
	}
	return rows
}

func pageReactions() []reactionRow {
	return []reactionRow{
		{"signal", sig, "m1", nil, "\U0001f44d", "Alice", 0, 1, at(11000)},
		{"signal", sig, "m1", nil, "\U0001f44d", "Bob", 0, 1, at(10500)},
		{"signal", sig, "m1", nil, "❤️", "You", 1, 1, at(12000)},
		{"signal", sig, nil, at(20000), "\U0001f525", "Carl", 0, 1, at(21000)},
		{"signal", sig, "nope", nil, "x", "Dan", 0, 1, at(1)},
		{"whatsapp", waLid, "true_123@lid_AAA", nil, "\U0001f44d", "Mario", 0, 2, at(6500)},
	}
}

func pageContacts() (contacts, book map[string][]proto.Contact, jid map[string]string) {
	contacts = map[string][]proto.Contact{
		"whatsapp": {
			{ID: waLid, DisplayName: "Mario", Protocol: "whatsapp", Extras: map[string]any{"phone": "391", "lid": waLid}},
			{ID: waGrp, DisplayName: "The group", Protocol: "whatsapp", Extras: map[string]any{"is_group": true}},
		},
		"telegram": {
			{ID: tgGrp, DisplayName: "TG group", Protocol: "telegram", Extras: map[string]any{"is_group": true}},
			{ID: "t1", DisplayName: "Anna", Protocol: "telegram", Extras: map[string]any{}},
		},
	}
	book = map[string][]proto.Contact{
		"whatsapp": {
			{ID: "111@lid", DisplayName: "Ann", Protocol: "whatsapp", Extras: map[string]any{"phone": "39111"}},
			{ID: "222@c.us", DisplayName: "Bea", Protocol: "whatsapp", Extras: map[string]any{"phone": "222"}},
			{ID: "777@c.us", DisplayName: "Carl", Protocol: "whatsapp", Extras: map[string]any{"phone": "4455"}},
			{ID: "888@c.us", DisplayName: "Dora", Protocol: "whatsapp", Extras: map[string]any{"phone": "888", "lid": "444@lid"}},
			{ID: "nameless@c.us", DisplayName: "", Protocol: "whatsapp", Extras: map[string]any{"phone": "999"}},
		},
	}
	jid = map[string]string{"333@lid": "4455"}
	return
}

// seedPageEnv creates the page fixture environment with its files on disk.
func seedPageEnv(t *testing.T, vars map[string]string) *env {
	t.Helper()
	e := newEnv(t, envOpts{rows: pageFixture, reactions: pageReactions(), vars: vars})
	e.contacts, e.addressBook, e.jidPhone = pageContacts()
	writeFile(t, filepath.Join(e.cfg.SignalCLIAttachmentsDir(), "img1.jpg"), "jpegdata")
	writeFile(t, filepath.Join(e.cfg.SignalCLIAttachmentsDir(), "doc.pdf"), "pdf")
	writeFile(t, filepath.Join(e.cfg.MediaDir(), "x.jpg"), "wa-jpeg")
	writeFile(t, filepath.Join(e.cfg.QuoteThumbsDir(), "q12.jpg"), "thumb")
	writeFile(t, filepath.Join(e.cfg.DataDir(), "orig", "quoted.jpg"), "orig")
	return e
}

var pageCalls = []map[string]any{
	{"name": "signal", "op": "page", "protocol": "signal", "contact_id": sig},
	{"name": "signal_limit", "op": "page", "protocol": "signal", "contact_id": sig, "limit": 7},
	{"name": "signal_since", "op": "page", "protocol": "signal", "contact_id": sig, "since": at(100000)},
	{"name": "signal_other", "op": "page", "protocol": "signal", "contact_id": "+39222"},
	{"name": "signal_empty", "op": "page", "protocol": "signal", "contact_id": "+nobody"},
	{"name": "wa_lid", "op": "page", "protocol": "whatsapp", "contact_id": waLid},
	{"name": "wa_ghost", "op": "page", "protocol": "whatsapp", "contact_id": waGhost},
	{"name": "wa_ghost_limit", "op": "page", "protocol": "whatsapp", "contact_id": waGhost, "limit": 4},
	{"name": "wa_group", "op": "page", "protocol": "whatsapp", "contact_id": waGrp},
	{"name": "tg", "op": "page", "protocol": "telegram", "contact_id": "t1"},
	{"name": "tg_group", "op": "page", "protocol": "telegram", "contact_id": tgGrp},
}

var _ = api.OpMessagesPage
