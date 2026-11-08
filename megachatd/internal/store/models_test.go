package store

import (
	"testing"
)

func TestIsWhatsAppSyntheticMediaText(t *testing.T) {
	cases := []struct {
		text string
		att  *string
		want bool
	}{
		{"Media: bella foto", nil, false},
		{"Media: http://x", nil, true},
		{"MEDIA: x", nil, false},
		{"MEDIA: x", strp("x"), true},
		{"", nil, false},
		{"media:", nil, false},
		{"Media: true_123@c.us_ABC", nil, true},
		{"Media: 12:34", nil, true},
		{"Media: a/b/file.ogg", strp("/cache/a/b/file.ogg"), false}, // neither the id nor its basename
		{"Media: file.ogg", strp("/cache/a/b/file.ogg"), true},
	}
	for _, c := range cases {
		if got := isWhatsAppSyntheticMediaText(c.text, c.att, false); got != c.want {
			t.Errorf("%q att=%v: got %v want %v", c.text, Deref(c.att), got, c.want)
		}
	}
	// require_attachment_match ignores the canonical patterns
	if isWhatsAppSyntheticMediaText("Media: http://x", nil, true) {
		t.Error("require match: url without attachment id must not be synthetic")
	}
	if !isWhatsAppSyntheticMediaText("Media: http://x", strp("http://x"), true) {
		t.Error("require match: own attachment id must match")
	}
}

func TestIsCaptionLike(t *testing.T) {
	for text, want := range map[string]bool{
		"":                        false,
		"   ":                     false,
		"ciao a tutti":            true,
		"IMG_1303.jpg":            false,
		"photo":                   false,
		"Documento":               false,
		"image/png":               false,
		"audio/ogg; codecs=opus":  false,
		"Media: abc":              false,
		"Media: bella foto":       true,
		"https://example.com/x":   true,
		"report finale.pdf":       false, // extension
		"un file.xyz":             true,  // whitespace in name, unknown ext
		"archive.tar.gz":          false, // filename regex
		"\U0001f3a8 Sticker":      false,
		"nota — \U0001f3b5 Audio": false,
	} {
		if got := isCaptionLike(&text); got != want {
			t.Errorf("isCaptionLike(%q) = %v, want %v", text, got, want)
		}
	}
	if isCaptionLike(nil) {
		t.Error("nil is not a caption")
	}
}

func TestIsSentMirrorAttachmentID(t *testing.T) {
	for v, want := range map[string]bool{
		"sent-abc.pdf": true, "SENT-abc": true, "/cache/x/sent-1.png": true, "dir/notsent-1": false,
		"": false, "https://h/sent-1.pdf": true, "sent": false,
	} {
		if got := isSentMirrorAttachmentID(&v); got != want {
			t.Errorf("%q: got %v want %v", v, got, want)
		}
	}
}

func TestPreferDisplaySenderAndStatusRank(t *testing.T) {
	eq(t, *preferDisplaySender(strp("1@lid"), strp("Alice")), "Alice")
	eq(t, *preferDisplaySender(strp("Bob"), strp("Alice")), "Bob")
	eq(t, *preferDisplaySender(strp("1@lid"), strp("2@c.us")), "1@lid")
	eq(t, *preferDisplaySender(strp(""), strp("2@c.us")), "2@c.us")
	if preferDisplaySender(nil, nil) != nil {
		t.Error("nil,nil must stay nil")
	}
	for s, want := range map[string]int{"pending": 0, "failed": 0, "sent": 1, "Delivered": 2, "READ": 3, "x": 0} {
		eq(t, statusRank(strp(s)), want)
	}
	eq(t, statusRank(nil), 0)
}

func TestPickSurvivorAndMerge(t *testing.T) {
	rows := []rowMap{
		{"id": int64(1), "status": "sent", "timestamp": int64(0), "sender": "1@lid", "edited": int64(0), "quote_text": nil, "attachment_info": ""},
		{"id": int64(2), "status": "read", "timestamp": int64(50), "sender": "Alice", "edited": int64(1), "quote_text": "q", "attachment_info": "f.pdf"},
		{"id": int64(3), "status": "read", "timestamp": int64(0), "sender": "", "edited": int64(0), "quote_text": "zz", "attachment_info": "g.pdf"},
	}
	si, ov := pickSurvivorAndMerge(rows)
	eq(t, si, 1) // highest status, real timestamp beats 0, then lowest id
	eq(t, ov.get("quote_text").(string), "q")
	eq(t, ov.get("attachment_info").(string), "f.pdf")
	eq(t, ov.get("sender").(string), "Alice")
	eq(t, ov.get("edited").(int64), int64(1))
	if ov.get("timestamp") != nil {
		t.Error("survivor has a real timestamp; no override expected")
	}
	// survivor with timestamp 0 adopts the first real one
	rows = []rowMap{
		{"id": int64(1), "status": "read", "timestamp": int64(0)},
		{"id": int64(2), "status": "sent", "timestamp": int64(77)},
	}
	si, ov = pickSurvivorAndMerge(rows)
	eq(t, si, 0)
	eq(t, ov.get("timestamp").(int64), int64(77))
}
