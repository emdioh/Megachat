package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestMarkedIDConventions(t *testing.T) {
	cases := []struct {
		kind PeerKind
		raw  int64
		want int64
	}{{PeerUser, 42, 42}, {PeerChat, 77, -77}, {PeerChannel, 1234, -1000000001234}}
	for _, c := range cases {
		got := markedID(c.kind, c.raw)
		if got != c.want {
			t.Errorf("markedID(%v,%d)=%d want %d", c.kind, c.raw, got, c.want)
		}
		k, r := splitMarked(got)
		if k != c.kind || r != c.raw {
			t.Errorf("splitMarked(%d)=%v,%d", got, k, r)
		}
	}
	for p, want := range map[tg.PeerClass]string{
		&tg.PeerUser{UserID: 5}: "5", &tg.PeerChat{ChatID: 9}: "-9", &tg.PeerChannel{ChannelID: 3}: "-1000000000003",
	} {
		if got, _ := peerContactID(p); got != want {
			t.Errorf("%T -> %q want %q", p, got, want)
		}
	}
}

func TestUnparseMarkdownMatchesTelethon(t *testing.T) {
	ents := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 5},
		&tg.MessageEntityItalic{Offset: 6, Length: 5},
	}
	if got := unparseMarkdown("hello world!", ents); got != "**hello** __world__!" {
		t.Fatal(got)
	}
	// Offsets are UTF-16 code units: the emoji counts as two.
	if got := unparseMarkdown("😀 hi", []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 3, Length: 2}}); got != "😀 **hi**" {
		t.Fatal(got)
	}
	url := []tg.MessageEntityClass{&tg.MessageEntityTextURL{Offset: 0, Length: 4, URL: "http://x"}, &tg.MessageEntityCode{Offset: 5, Length: 3}}
	if got := unparseMarkdown("link code", url); got != "[link](http://x) `cod`e" {
		t.Fatal(got)
	}
	if got := unparseMarkdown("plain", nil); got != "plain" {
		t.Fatal(got)
	}
}

func TestParseMarkdownRoundTrip(t *testing.T) {
	text, ents := parseMarkdown("**a** and __b__ [x](http://u) `c` ~~d~~")
	if text != "a and b x c d" || len(ents) != 5 {
		t.Fatalf("%q %d", text, len(ents))
	}
	if b, ok := ents[0].(*tg.MessageEntityBold); !ok || b.Offset != 0 || b.Length != 1 {
		t.Fatalf("%#v", ents[0])
	}
	if u, ok := ents[2].(*tg.MessageEntityTextURL); !ok || u.URL != "http://u" || u.Offset != 8 {
		t.Fatalf("%#v", ents[2])
	}
	if got, e := parseMarkdown("2 * 3 ** 4"); got != "2 * 3 ** 4" || len(e) != 0 {
		t.Fatalf("unmatched delimiter must stay literal: %q", got)
	}
	if got, e := parseMarkdown("no markup"); got != "no markup" || len(e) != 0 {
		t.Fatal(got)
	}
}

func docMsg(mime string, attrs ...tg.DocumentAttributeClass) *tg.Message {
	return &tg.Message{ID: 1, Media: &tg.MessageMediaDocument{Document: &tg.Document{MimeType: mime, Attributes: attrs}}}
}

func TestTgMediaKind(t *testing.T) {
	cases := []struct {
		name string
		msg  *tg.Message
		kind string
		file string
	}{
		{"photo", &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{}}}, "image", ""},
		{"sticker", docMsg("image/webp", &tg.DocumentAttributeSticker{}), "sticker", ""},
		{"gif", docMsg("video/mp4", &tg.DocumentAttributeAnimated{}), "gif", ""},
		{"voice", docMsg("audio/ogg", &tg.DocumentAttributeAudio{Voice: true}), "voice", ""},
		{"video", docMsg("video/mp4", &tg.DocumentAttributeVideo{}), "video", ""},
		{"audio", docMsg("audio/mpeg", &tg.DocumentAttributeAudio{}), "audio", ""},
		{"audio mime", docMsg("audio/x-foo"), "audio", ""},
		{"pdf", docMsg("application/pdf", &tg.DocumentAttributeFilename{FileName: "r.pdf"}), "document", "r.pdf"},
		{"no media", &tg.Message{}, "", ""},
	}
	for _, c := range cases {
		kind, file, _ := tgMediaKind(c.msg)
		if kind != c.kind || file != c.file {
			t.Errorf("%s: %q %q", c.name, kind, file)
		}
	}
	if tgLabelFor("voice", "") != "🎤 Voice" || tgLabelFor("image", "") != "Photo" || tgLabelFor("document", "a.pdf") != "📎 a.pdf" {
		t.Fatal("labels")
	}
}

func TestTgQuoteTextFromCached(t *testing.T) {
	cases := []struct {
		e    entry
		want string
		ok   bool
	}{
		{entry{MsgType: "text", Text: "hi"}, "hi", true},
		{entry{MsgType: "text"}, "", false},
		{entry{MsgType: "image", Text: " caption "}, "caption", true},
		{entry{MsgType: "image", AttachmentInfo: "Photo"}, "🖼️ Immagine", true},
		{entry{MsgType: "attachment", AttachmentInfo: "🎬 Video"}, "🎬 Video", true},
		{entry{MsgType: "attachment", AttachmentInfo: "🎤 Voice"}, "🎵 Audio", true},
		{entry{MsgType: "sticker", AttachmentInfo: "🎨 Sticker"}, "🎨 Sticker", true},
		{entry{MsgType: "attachment", AttachmentInfo: "📎 report.pdf"}, "📎 report.pdf — 📎 File", true},
		{entry{MsgType: "attachment"}, "📎 File", true},
	}
	for i, c := range cases {
		got, ok := tgQuoteTextFromCached(&c.e)
		if got != c.want || ok != c.ok {
			t.Errorf("%d: %q,%v want %q,%v", i, got, ok, c.want, c.ok)
		}
	}
}

func TestMediaHelpers(t *testing.T) {
	if mediaKindFromMime("image/gif") != "gif" || mediaKindFromMime("audio/ogg; codecs=opus") != "audio" ||
		mediaKindFromMime("application/x") != "document" || mediaKindFromMime("") != "" {
		t.Fatal("mediaKindFromMime")
	}
	if msgTypeForMediaKind("gif") != "image" || msgTypeForMediaKind("video") != "attachment" || msgTypeForMediaKind("sticker") != "sticker" {
		t.Fatal("msgTypeForMediaKind")
	}
	if !tgIsPlaceholder("", "1") || !tgIsPlaceholder("123", "9") || tgIsPlaceholder("Bob", "9") {
		t.Fatal("placeholder")
	}
	if isCaptionLike("photo.jpg") || !isCaptionLike("nice trip") || isCaptionLike("🎬 Video") {
		t.Fatal("isCaptionLike")
	}
	if !strings.HasPrefix(mediaRef("-100", 5), "tgref:") {
		t.Fatal("mediaRef")
	}
	c, m, ok := parseTgref("tgref:-1000000000003:77")
	if !ok || c != -1000000000003 || m != 77 {
		t.Fatal("parseTgref")
	}
	if _, _, ok := parseTgref("tgref:x:y"); ok {
		t.Fatal("parseTgref accepted junk")
	}
}

func TestEntityToContact(t *testing.T) {
	c := userToContact(&tg.User{ID: 5, FirstName: "Ann", LastName: "Lee", Username: "al", Phone: "39123"})
	if c.ID != "5" || c.DisplayName != "Ann Lee" || c.Extras["username"] != "al" || c.Extras["phone"] != "39123" || c.Extras["is_group"] != false {
		t.Fatalf("%+v", c)
	}
	if c := userToContact(&tg.User{ID: 6, Username: "nick"}); c.DisplayName != "nick" {
		t.Fatalf("%+v", c)
	}
	if c := userToContact(&tg.User{ID: 7}); c.DisplayName != "7" {
		t.Fatalf("%+v", c)
	}
	g := chatToContact(&tg.Chat{ID: 9, Title: "Grp"})
	if g.ID != "-9" || g.Extras["is_group"] != true {
		t.Fatalf("%+v", g)
	}
	ch := channelToContact(&tg.Channel{ID: 3, Title: "News"})
	if ch.ID != "-1000000000003" || ch.Extras["is_channel"] != true {
		t.Fatalf("%+v", ch)
	}
	ab := userToAddressBookContact(&tg.User{ID: 8, Phone: "+39 12-3", AccessHash: 55})
	if ab.DisplayName != "+39123" || ab.Extras["phone"] != "39123" || ab.Extras["access_hash"] != "55" ||
		ab.Extras["address_book"] != true || ab.Extras["source"] != "tg_book" {
		t.Fatalf("%+v", ab)
	}
}
