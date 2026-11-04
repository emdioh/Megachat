package transcribe

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// sentDispatcher records messages.send calls.
type sentDispatcher struct {
	mu   sync.Mutex
	sent []map[string]any
	err  error
}

func (d *sentDispatcher) Call(_ context.Context, name string, p json.RawMessage) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if name != "messages.send" {
		return nil, ops.Errorf("not_found", "x")
	}
	var m map[string]any
	_ = json.Unmarshal(p, &m)
	d.sent = append(d.sent, m)
	return map[string]any{"ok": true}, d.err
}
func (d *sentDispatcher) Has(string) bool { return true }
func (d *sentDispatcher) Names() []string { return nil }
func (d *sentDispatcher) all() []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]any(nil), d.sent...)
}

type fakeQuoted struct {
	qm   *QuotedMessage
	args []any
}

func (f *fakeQuoted) FindQuoted(_ context.Context, p, c, rid string, ts int64) (*QuotedMessage, error) {
	f.args = []any{p, c, rid, ts}
	return f.qm, nil
}

type autoEnv struct {
	svc  *Service
	auto *AutoTranscriber
	disp *sentDispatcher
	api  *fakeAPI
	cfg  *config.Config
	now  time.Time
}

func newAutoEnv(t *testing.T, key bool, audio string, q QuotedFinder) *autoEnv {
	t.Helper()
	e := &autoEnv{disp: &sentDispatcher{}, api: newFakeAPI(t), now: time.Now()}
	base := ""
	if key {
		base = e.api.URL
	}
	e.cfg = newConfig(t, base)
	b := bus.New()
	t.Cleanup(b.Close)
	e.svc = New(Deps{Store: newMemStore(), Config: e.cfg, Bus: b, Media: fakeMedia{path: audio},
		Dispatcher: e.disp, Quoted: q, Now: func() time.Time { return e.now }})
	if err := e.svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.svc.Stop)
	e.auto = e.svc.auto
	return e
}

func (e *autoEnv) voice(protocol, contact, id, att string) map[string]any {
	return map[string]any{"added": true, "protocol": protocol, "contact_id": contact,
		"message": map[string]any{"id": id, "timestamp": e.now.UnixMilli(), "media_kind": "voice",
			"attachment_id": att, "is_mine": false, "text": nil}}
}

func (e *autoEnv) command(protocol, contact, id, text string, extra map[string]any) map[string]any {
	m := map[string]any{"id": id, "timestamp": e.now.UnixMilli(), "is_mine": true, "text": text}
	for k, v := range extra {
		m[k] = v
	}
	return map[string]any{"added": true, "protocol": protocol, "contact_id": contact, "message": m}
}

func (e *autoEnv) waitSent(t *testing.T, n int) []map[string]any {
	t.Helper()
	waitFor(t, "messages.send", func() bool { return len(e.disp.all()) >= n })
	return e.disp.all()
}

func TestAutoTranscribesVoiceAndPostsQuotedReply(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	e := newAutoEnv(t, true, audio, nil)
	_ = e.cfg.SetAutoTranscribeGlobal("whatsapp", true)
	e.auto.HandleMessage(e.voice("whatsapp", "39@c.us", "wa-1", "att-1"))
	sent := e.waitSent(t, 1)
	m := sent[0]
	if m["text"] != "🎤 Trascrizione:\n\ntesto trascritto" || m["protocol"] != "whatsapp" ||
		m["contact_id"] != "39@c.us" || m["reply_to_message_id"] != "wa-1" {
		t.Fatalf("%v", m)
	}
	if _, has := m["quote_timestamp"]; has {
		t.Fatal("whatsapp must quote by id")
	}
	// the same event again is deduplicated; same attachment is not re-posted
	e.auto.HandleMessage(e.voice("whatsapp", "39@c.us", "wa-1", "att-1"))
	e.auto.HandleMessage(e.voice("whatsapp", "39@c.us", "wa-2", "att-1"))
	time.Sleep(100 * time.Millisecond)
	if len(e.disp.all()) != 1 || len(e.api.all()) != 1 {
		t.Fatalf("sent=%d uploads=%d", len(e.disp.all()), len(e.api.all()))
	}
}

func TestAutoSignalQuotesByTimestamp(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	e := newAutoEnv(t, true, audio, nil)
	_ = e.cfg.SetAutoTranscribeGlobal("signal", true)
	p := e.voice("signal", "+39", "", "sig-att.oga")
	e.auto.HandleMessage(p)
	m := e.waitSent(t, 1)[0]
	if m["quote_timestamp"] != float64(e.now.UnixMilli()) || m["quote_author"] != "+39" {
		t.Fatalf("%v", m)
	}
	if _, has := m["reply_to_message_id"]; has {
		t.Fatal("signal must not use reply_to_message_id")
	}
}

func TestAutoRespectsSettingsAndFreshness(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	e := newAutoEnv(t, true, audio, nil)
	// disabled everywhere
	e.auto.HandleMessage(e.voice("whatsapp", "c", "i1", "a1"))
	// chat override on wins over global off
	_ = e.cfg.SetAutoTranscribeChat("whatsapp", "c2", ptr(true))
	// too old
	old := e.voice("whatsapp", "c2", "i2", "a2")
	old["message"].(map[string]any)["timestamp"] = e.now.Add(-11 * time.Minute).UnixMilli()
	e.auto.HandleMessage(old)
	// not added
	na := e.voice("whatsapp", "c2", "i3", "a3")
	na["added"] = false
	e.auto.HandleMessage(na)
	// gmail ignored
	e.auto.HandleMessage(e.voice("gmail", "c2", "i4", "a4"))
	time.Sleep(100 * time.Millisecond)
	if len(e.disp.all()) != 0 || len(e.api.all()) != 0 {
		t.Fatal("must not transcribe")
	}
	e.auto.HandleMessage(e.voice("whatsapp", "c2", "i5", "a5"))
	e.waitSent(t, 1)
}

func ptr[T any](v T) *T { return &v }

func TestAutoFailureReplyAndRetryViaCommand(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	q := &fakeQuoted{qm: &QuotedMessage{MsgID: "wa-9", Timestamp: 5, MediaKind: "voice", AttachmentID: "att-9"}}
	e := newAutoEnv(t, true, audio, q)
	calls := 0
	e.api.handler = func(_ int, _ upload, w http.ResponseWriter) {
		calls++
		if calls == 1 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"text":"riprovato"}`))
	}
	_ = e.cfg.SetAutoTranscribeGlobal("whatsapp", true)
	e.auto.HandleMessage(e.voice("whatsapp", "c", "wa-9", "att-9"))
	sent := e.waitSent(t, 1)
	if sent[0]["text"] != "⚠️ Trascrizione non riuscita. Riprova più tardi con !t." {
		t.Fatalf("%v", sent[0])
	}
	e.auto.HandleMessage(e.command("whatsapp", "c", "cmd-1", "!t", map[string]any{"reply_to_message_id": "wa-9"}))
	sent = e.waitSent(t, 2)
	if sent[1]["text"] != "🎤 Trascrizione:\n\nriprovato" || sent[1]["reply_to_message_id"] != "wa-9" {
		t.Fatalf("%v", sent[1])
	}
	if q.args[2] != "wa-9" {
		t.Fatalf("%v", q.args)
	}
}

func TestCommandTWithoutQuoteOrNotVoiceGivesHint(t *testing.T) {
	e := newAutoEnv(t, true, "", &fakeQuoted{})
	e.auto.HandleMessage(e.command("signal", "+1", "", "!t", nil))
	m := e.waitSent(t, 1)[0]
	if m["text"] != "ℹ️ Rispondi a una nota vocale con !t per trascriverla." || m["quote_message"] != "!t" {
		t.Fatalf("%v", m)
	}
}

func TestCommandTNotConfigured(t *testing.T) {
	q := &fakeQuoted{qm: &QuotedMessage{MsgID: "m", MediaKind: "audio", AttachmentID: "a"}}
	e := newAutoEnv(t, false, "", q)
	e.auto.HandleMessage(e.command("telegram", "5", "9", "!t", nil))
	if m := e.waitSent(t, 1)[0]; !strings.Contains(m["text"].(string), "Trascrizione non configurata") {
		t.Fatalf("%v", m)
	}
}

func TestTranscriptionCommands(t *testing.T) {
	e := newAutoEnv(t, true, "", nil)
	e.auto.HandleMessage(e.command("whatsapp", "c", "1", "!transcription", nil))
	s := e.waitSent(t, 1)[0]["text"].(string)
	for _, want := range []string{"Trascrizione automatica (whatsapp)", "Globale: disattiva", "Questa chat: non impostato",
		"Effettivo: disattiva", "ℹ️ Comandi:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("lacks %q: %s", want, s)
		}
	}
	e.auto.HandleMessage(e.command("whatsapp", "c", "2", "!Transcription ON", nil))
	if m := e.waitSent(t, 2)[1]; m["text"] != "✅ Trascrizione automatica attiva in questa chat." || m["quote_message"] != "!Transcription ON" {
		t.Fatalf("%v", m)
	}
	if o := e.cfg.AutoTranscribeChat("whatsapp", "c"); o == nil || !*o {
		t.Fatal("override not saved")
	}
	e.auto.HandleMessage(e.command("whatsapp", "c", "3", "!transcription global on", nil))
	if m := e.waitSent(t, 3)[2]; m["text"] != "✅ Trascrizione automatica attiva per tutte le chat whatsapp." {
		t.Fatalf("%v", m)
	}
	if !e.cfg.AutoTranscribeGlobal("whatsapp") {
		t.Fatal("global not saved")
	}
	e.auto.HandleMessage(e.command("whatsapp", "c", "4", "!transcription bogus", nil))
	if m := e.waitSent(t, 4)[3]; !strings.HasPrefix(m["text"].(string), "⚠️ Argomenti non validi.\n\nℹ️ Comandi:") {
		t.Fatalf("%v", m)
	}
}

func TestOnlyOwnBangMessagesAreCommands(t *testing.T) {
	e := newAutoEnv(t, true, "", nil)
	p := e.command("whatsapp", "c", "1", "!transcription", nil)
	p["message"].(map[string]any)["is_mine"] = false
	e.auto.HandleMessage(p)
	e.auto.HandleMessage(e.command("whatsapp", "c", "2", "hello !t", nil))
	e.auto.HandleMessage(e.command("whatsapp", "c", "3", "!unknown", nil))
	time.Sleep(100 * time.Millisecond)
	if n := len(e.disp.all()); n != 0 {
		t.Fatalf("%d replies", n)
	}
}

func TestPostNeverStartsWithBang(t *testing.T) {
	e := newAutoEnv(t, true, "", nil)
	e.auto.post(quote{protocol: "whatsapp", contactID: "c", messageID: "m"}, "!boom")
	if m := e.disp.all()[0]; m["text"] != "ℹ️ !boom" {
		t.Fatalf("%v", m)
	}
}

func TestEmptyTranscriptionText(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	e := newAutoEnv(t, true, audio, nil)
	e.api.handler = func(_ int, _ upload, w http.ResponseWriter) { _, _ = w.Write([]byte(`{"text":"  "}`)) }
	_ = e.cfg.SetAutoTranscribeGlobal("telegram", true)
	e.auto.HandleMessage(e.voice("telegram", "5", "m1", "a1"))
	if m := e.waitSent(t, 1)[0]; m["text"] != "🎤 Trascrizione:\n\n(nessun testo riconosciuto)" {
		t.Fatalf("%v", m)
	}
}

func TestSendFailureAllowsRetry(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	e := newAutoEnv(t, true, audio, nil)
	_ = e.cfg.SetAutoTranscribeGlobal("telegram", true)
	e.disp.err = ops.Errorf(api.CodeBackendDown, "down")
	e.auto.HandleMessage(e.voice("telegram", "5", "m1", "a1"))
	e.waitSent(t, 1)
	waitFor(t, "posted flag cleared", func() bool {
		e.auto.mu.Lock()
		defer e.auto.mu.Unlock()
		return len(e.auto.posted) == 0 && len(e.auto.pending) == 0
	})
	e.disp.mu.Lock()
	e.disp.err = nil
	e.disp.mu.Unlock()
	e.auto.HandleMessage(e.voice("telegram", "5", "m2", "a1")) // different event, same attachment
	e.waitSent(t, 2)
}

func TestBusDrivenFlowAndGapIgnored(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	b := bus.New()
	defer b.Close()
	disp := &sentDispatcher{}
	fa := newFakeAPI(t)
	cfg := newConfig(t, fa.URL)
	_ = cfg.SetAutoTranscribeGlobal("signal", true)
	svc := New(Deps{Store: newMemStore(), Config: cfg, Bus: b, Media: fakeMedia{path: audio}, Dispatcher: disp})
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	if !svc.hasOnDone() {
		t.Fatal("auto listener not installed")
	}
	// payload built from api structs (what the core publishes)
	id, ts, att, kind, mine := "", time.Now().UnixMilli(), "x.oga", "voice", false
	b.Publish(bus.Message, map[string]any{"added": true, "protocol": "signal", "contact_id": "+1", "timestamp": ts,
		"message": api.CacheMessage{ID: &id, Timestamp: &ts, AttachmentID: &att, MediaKind: &kind, IsMine: &mine}})
	waitFor(t, "reply", func() bool { return len(disp.all()) == 1 })
	svc.Stop()
	if svc.hasOnDone() {
		t.Fatal("hook not removed on stop")
	}
}
