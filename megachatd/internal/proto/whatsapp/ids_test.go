package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestWAHAFromJID(t *testing.T) {
	cases := []struct {
		in   types.JID
		want string
	}{
		{types.NewJID("391234567890", types.DefaultUserServer), "391234567890@c.us"},
		{types.NewADJID("391234567890", 0, 7), "391234567890@c.us"},
		{types.NewJID("391234567890", types.LegacyUserServer), "391234567890@c.us"},
		{types.NewJID("189025889575055", types.HiddenUserServer), "189025889575055@lid"},
		{types.NewJID("120363000000000000", types.GroupServer), "120363000000000000@g.us"},
		{types.NewJID("391234567890-1600000000", types.GroupServer), "391234567890-1600000000@g.us"},
		{types.StatusBroadcastJID, "status@broadcast"},
		{types.EmptyJID, ""},
	}
	for _, c := range cases {
		if got := WAHAFromJID(c.in); got != c.want {
			t.Errorf("WAHAFromJID(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJIDFromWAHA(t *testing.T) {
	cases := []struct{ in, want string }{
		{"391234567890@c.us", "391234567890@s.whatsapp.net"},
		{"391234567890@s.whatsapp.net", "391234567890@s.whatsapp.net"},
		{"189025889575055@lid", "189025889575055@lid"},
		{"120363000000000000@g.us", "120363000000000000@g.us"},
		{"391234567890-1600000000@g.us", "391234567890-1600000000@g.us"},
	}
	for _, c := range cases {
		j, err := JIDFromWAHA(c.in)
		if err != nil {
			t.Fatalf("JIDFromWAHA(%q): %v", c.in, err)
		}
		if j.String() != c.want {
			t.Errorf("JIDFromWAHA(%q) = %q, want %q", c.in, j.String(), c.want)
		}
	}
	for _, bad := range []string{"", "nodomain", "@c.us", "123@weird.example", "wa:1"} {
		if _, err := JIDFromWAHA(bad); err == nil {
			t.Errorf("JIDFromWAHA(%q) should fail", bad)
		}
	}
}

func TestWAHARoundTrip(t *testing.T) {
	for _, id := range []string{"391234567890@c.us", "189025889575055@lid", "120363000000000000@g.us", "1-2@g.us"} {
		j, err := JIDFromWAHA(id)
		if err != nil {
			t.Fatal(err)
		}
		if back := WAHAFromJID(j); back != id {
			t.Errorf("round trip %q -> %q", id, back)
		}
	}
}

// The ids below are the real shapes used in the Python tests.
func TestCanonicalMsgID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"true_189025889575055@lid_3A268CF00E4ECCEA4474", "3A268CF00E4ECCEA4474"},
		{"true_123456789@g.us_3EB0D9A373425F46E2A5F6_987654321@lid", "3EB0D9A373425F46E2A5F6"},
		{"false_120363000000000000@g.us_3EB0ABC_20508603076702@lid", "3EB0ABC"},
		{"true_391234567890@s.whatsapp.net_3A268CF00E4ECCEA4474", "3A268CF00E4ECCEA4474"},
		{"false_391234567890@c.us_ABC", "ABC"},
		{"3eb0d9a373425f46e2a5f6", "3EB0D9A373425F46E2A5F6"},
		{"3EB0D9A373425F46E2A5F6", "3EB0D9A373425F46E2A5F6"},
		{"msg_abc", "msg_abc"},
		{"m1", "m1"},
		{"m", "m"},
		{"msg-1", "msg-1"},
		{"outgoing-parent", "outgoing-parent"},
		{"BAYES-123", "BAYES-123"},
		{"true_reverse_189025889575055@lid", "true_reverse_189025889575055@lid"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := CanonicalMsgID(c.in); got != c.want {
			t.Errorf("CanonicalMsgID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if CanonicalMsgID("true_189025889575055@lid_3A268CF00E4ECCEA4474") != CanonicalMsgID("3A268CF00E4ECCEA4474") {
		t.Error("DM serialized and hex ids must be equivalent")
	}
}

func TestParseAndBuildMsgID(t *testing.T) {
	cases := []struct {
		in   string
		want MsgID
	}{
		{"true_189025889575055@lid_3A268CF00E4ECCEA4474", MsgID{FromMe: true, Chat: "189025889575055@lid", ID: "3A268CF00E4ECCEA4474", Serialized: true}},
		{"false_391234567890@c.us_ABC", MsgID{Chat: "391234567890@c.us", ID: "ABC", Serialized: true}},
		{"true_123456789@g.us_3EB0D9A373425F46E2A5F6_987654321@lid", MsgID{FromMe: true, Chat: "123456789@g.us", ID: "3EB0D9A373425F46E2A5F6", Participant: "987654321@lid", Serialized: true}},
		{"false_120363000000000000@g.us_3EB0ABC_20508603076702@lid", MsgID{Chat: "120363000000000000@g.us", ID: "3EB0ABC", Participant: "20508603076702@lid", Serialized: true}},
		{"true_391234567890@s.whatsapp.net_3A26", MsgID{FromMe: true, Chat: "391234567890@c.us", ID: "3A26", Serialized: true}},
		{"3EB0D9A373425F46E2A5F6", MsgID{ID: "3EB0D9A373425F46E2A5F6"}},
		{"msg_abc", MsgID{ID: "msg_abc"}},
	}
	for _, c := range cases {
		if got := ParseMsgID(c.in); got != c.want {
			t.Errorf("ParseMsgID(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	// Building is the inverse for the canonical shapes.
	for _, id := range []string{
		"true_189025889575055@lid_3A268CF00E4ECCEA4474",
		"false_391234567890@c.us_ABC",
		"true_123456789@g.us_3EB0D9A373425F46E2A5F6_987654321@lid",
		"false_120363000000000000@g.us_3EB0ABC_20508603076702@lid",
	} {
		p := ParseMsgID(id)
		if got := BuildMsgID(p.FromMe, p.Chat, p.ID, p.Participant); got != id {
			t.Errorf("BuildMsgID round trip %q -> %q", id, got)
		}
	}
	// A participant is only appended in groups.
	if got := BuildMsgID(true, "1@c.us", "AB", "9@lid"); got != "true_1@c.us_AB" {
		t.Errorf("DM id carries no participant, got %q", got)
	}
}

func TestResolveMediaChatID(t *testing.T) {
	cases := []struct{ in, chat, name string }{
		// Like Python, the "jid" group swallows the trailing _<id>.
		{"false_391234567890@c.us_ABC.jpeg", "391234567890@c.us_ABC", "false_391234567890@c.us_ABC"},
		{"http://waha:3000/api/files/default/true_189025889575055@lid_3EB0.oga", "189025889575055@lid_3EB0", "true_189025889575055@lid_3EB0"},
		{"false_123@g.us_AC22_456@lid.pdf", "123@g.us_AC22_456@lid", "false_123@g.us_AC22_456@lid"},
		{"sent-abc.png", "", "sent-abc"},
	}
	for _, c := range cases {
		chat, name := resolveMediaChatID(c.in)
		if chat != c.chat || name != c.name {
			t.Errorf("resolveMediaChatID(%q) = (%q, %q), want (%q, %q)", c.in, chat, name, c.chat, c.name)
		}
	}
}

func TestFetchableAndBroadcast(t *testing.T) {
	for _, ok := range []string{"391234567890@c.us", "1-2@g.us", "189025889575055@lid"} {
		if !isFetchableJID(ok) {
			t.Errorf("%q should be fetchable", ok)
		}
	}
	for _, bad := range []string{"status@broadcast", "abc@c.us", "123@newsletter", ""} {
		if isFetchableJID(bad) {
			t.Errorf("%q must not be fetchable", bad)
		}
	}
	if !isBroadcastJID("status@broadcast") || !isBroadcastJID("1@newsletter") || isBroadcastJID("1@c.us") {
		t.Error("isBroadcastJID")
	}
}

func TestLazyTokenRoundTrip(t *testing.T) {
	tok, ok := lazyMediaToken("189025889575055@lid", "true_189025889575055@lid_3A26")
	if !ok {
		t.Fatal("token")
	}
	// Must match the Python shape: lazy-wa-<b64url(contact)>~<b64url(msg)>.
	if want := "lazy-wa-MTg5MDI1ODg5NTc1MDU1QGxpZA~dHJ1ZV8xODkwMjU4ODk1NzUwNTVAbGlkXzNBMjY"; tok != want {
		t.Errorf("token = %q, want %q", tok, want)
	}
	c, m, ok := parseLazyMediaToken(tok)
	if !ok || c != "189025889575055@lid" || m != "true_189025889575055@lid_3A26" {
		t.Errorf("parse = %q %q %v", c, m, ok)
	}
	for _, bad := range []string{"", "lazy-wa-", "lazy-wa-abc", "lazy-wa-!!~!!", "other"} {
		if _, _, ok := parseLazyMediaToken(bad); ok {
			t.Errorf("parse(%q) should fail", bad)
		}
	}
	if _, ok := lazyMediaToken("a/b", "x"); ok {
		t.Error("slash must be rejected")
	}
}

func TestPhoneHelpers(t *testing.T) {
	if got := jidDigits("+39 123@c.us"); got != "39123" {
		t.Errorf("jidDigits = %q", got)
	}
	if !looksLikePhone("+39 333 1112222", "393331112222") || looksLikePhone("Mario", "393331112222") {
		t.Error("looksLikePhone")
	}
	if !isPlaceholderDisplayName("", "1@c.us", "") || !isPlaceholderDisplayName("1@c.us", "1@c.us", "") ||
		!isPlaceholderDisplayName("205086", "205086@lid", "") ||
		!isPlaceholderDisplayName("Contatto WhatsApp (ID 205086)", "205086@lid", "") ||
		!isPlaceholderDisplayName("Contatto WhatsApp (ID 205086)", "205086@lid", "393331112222") ||
		isPlaceholderDisplayName("Mario", "1@c.us", "") ||
		isPlaceholderDisplayName("Contatto WhatsApp (ID 205087)", "205086@lid", "") ||
		isPlaceholderDisplayName("Contatto WhatsApp (ID 205086)", "1@c.us", "") ||
		!isPlaceholderDisplayName("+393331112222", "393331112222@c.us", "") {
		t.Error("isPlaceholderDisplayName")
	}
}
