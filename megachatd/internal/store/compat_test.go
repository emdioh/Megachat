package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// cacheDict renders a Message the way Python's _load_cache builds its dicts.
func cacheDict(m Message) map[string]any {
	return map[string]any{
		"id": m.MsgID, "text": m.Text, "is_mine": m.IsMine, "sender": m.Sender, "timestamp": m.Timestamp,
		"quote_text": m.QuoteText, "msg_type": m.MsgType, "attachment_info": m.AttachmentInfo,
		"attachment_id": m.AttachmentID, "content_type": m.ContentType,
		"quote_attachment_id": m.QuoteAttachmentID, "quote_attachment_path": m.QuoteAttachmentPath,
		"quote_content_type": m.QuoteContentType, "quote_timestamp": m.QuoteTimestamp,
		"quote_author": m.QuoteAuthor, "reply_to_message_id": m.ReplyToMessageID, "edited": m.Edited,
		"read": m.Read, "status": m.Status, "protocol": m.Protocol, "batch_id": m.BatchID, "batch_index": m.BatchIndex,
	}
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A database written by the Python server opens in Go without data loss and
// LoadCache returns exactly what Python's _load_cache returns.
func TestPythonWrittenDatabaseOpensInGo(t *testing.T) {
	requirePython(t)
	path := filepath.Join(t.TempDir(), "messages.db")
	add := func(kwargs map[string]any, args ...any) {
		spec, _ := json.Marshal(map[string]any{"args": args, "kwargs": kwargs})
		if _, err := pyRun("run", path, "add_message_to_cache", string(spec)); err != nil {
			t.Fatal(err)
		}
	}
	add(map[string]any{"protocol": "whatsapp", "msg_id": "WA1", "status": "delivered"}, "wa@c.us", "hello", true, "me", 1700000000001)
	add(map[string]any{"protocol": "whatsapp", "msg_id": "WA2", "attachment_id": "voice.ogg", "msg_type": "attachment",
		"media_kind": "voice", "content_type": "audio/ogg", "batch_id": "b", "batch_index": 0}, "wa@c.us", "", false, "Alice", 1700000000002)
	add(map[string]any{"quote_text": "orig", "quote_timestamp": 5, "quote_author": "+39", "quote_attachment_path": "/p/q.png"}, "+39", "reply", False(), "Bob", 1700000000003)
	add(map[string]any{"msg_type": "image", "attachment_id": "x.png", "attachment_info": "cap"}, "+39", "caption", False(), "Bob", 1700000000004)
	if _, err := pyRun("run", path, "apply_reaction_delta", `["signal","+39","1700000000003",1700000000003,"👍","bob","Bob",false,false,9]`); err != nil {
		t.Fatal(err)
	}
	if _, err := pyRun("run", path, "mark_as_read", `["+39"]`); err != nil {
		t.Fatal(err)
	}
	py, err := pyRun("run", path, "load_cache")
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cache := must(s.LoadCache(ctx, ""))
	got := map[string]any{}
	for contact, msgs := range cache {
		var list []map[string]any
		for _, m := range msgs {
			list = append(list, cacheDict(m))
		}
		got[contact] = list
	}
	if !reflect.DeepEqual(roundTrip(t, got), py.Result) {
		t.Fatalf("LoadCache differs from Python\n python: %v\n go:     %v", py.Result, roundTrip(t, got))
	}
	eq(t, len(must(s.ReactionsForContact(ctx, "signal", "+39"))), 1)
	eq(t, len(must(s.CountUnread(ctx))), 1) // only wa@c.us still has the unread incoming voice note
}

func False() bool { return false }

// A database written by Go is readable by the Python code.
func TestGoWrittenDatabaseOpensInPython(t *testing.T) {
	requirePython(t)
	s := newStore(t)
	add(t, s, NewMessage{Protocol: "whatsapp", ContactNumber: "wa@c.us", Text: "hi", Sender: "A", Timestamp: 10, MsgID: strp("W1"),
		AttachmentID: strp("a.ogg"), MsgType: "attachment", MediaKind: strp("voice"), BatchID: strp("b"), BatchIndex: i64p(1)})
	add(t, s, NewMessage{ContactNumber: "+39", Text: "out", IsMine: true, Sender: "me", Timestamp: 20, QuoteText: strp("q")})
	check(s.SetTranscription(ctx, Transcription{Protocol: "whatsapp", AttachmentID: "a.ogg", Status: "ok", Text: strp("t")}))
	goCache := must(s.LoadCache(ctx, ""))
	_ = s.Close()

	py, err := pyRun("run", s.path, "load_cache")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for contact, msgs := range goCache {
		var list []map[string]any
		for _, m := range msgs {
			list = append(list, cacheDict(m))
		}
		got[contact] = list
	}
	if !reflect.DeepEqual(roundTrip(t, got), py.Result) {
		t.Fatalf("python cache differs\n python: %v\n go:     %v", py.Result, roundTrip(t, got))
	}
	// and Python's unread count agrees with Go's
	pc, err := pyRun("run", s.path, "count_unread")
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(s.path, quiet)
	defer s2.Close()
	if !reflect.DeepEqual(roundTrip(t, must(s2.CountUnread(ctx))), pc.Result) {
		t.Fatalf("unread differs: python %v go %v", pc.Result, must(s2.CountUnread(ctx)))
	}
}
