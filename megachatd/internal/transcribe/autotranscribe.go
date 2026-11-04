package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

const (
	// RecentWindow: only messages younger than this are handled (resyncs
	// re-emit old history).
	RecentWindow = 600 * time.Second
	// StartMargin: ...and not older than the start minus this margin.
	StartMargin = 60 * time.Second

	TranscriptionPrefix = "🎤 Trascrizione:\n\n"
	emptyText           = "(nessun testo riconosciuto)"
	errorText           = "⚠️ Trascrizione non riuscita. Riprova più tardi con !t."
	notConfiguredText   = "⚠️ Trascrizione non configurata (manca la chiave API del provider)."
	tHint               = "ℹ️ Rispondi a una nota vocale con !t per trascriverla."
	maxSeen             = 1000
)

// quote is the message a reply quotes.
type quote struct {
	protocol, contactID string
	messageID           string // "" = none
	timestamp           int64
	text                string
}

var voiceExtensions = []string{".oga", ".ogg", ".opus"}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func isVoice(m map[string]any) bool {
	kind := m["media_kind"]
	if kind == "voice" {
		return true
	}
	if kind != "audio" {
		return false
	}
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(str(m, "content_type"), ";", 2)[0]))
	if mime == "audio/ogg" || mime == "audio/opus" {
		return true
	}
	for _, f := range []string{"attachment_id", "attachment_info", "filename"} {
		v := strings.ToLower(strings.SplitN(str(m, f), "?", 2)[0])
		for _, e := range voiceExtensions {
			if strings.HasSuffix(v, e) {
				return true
			}
		}
	}
	return false
}

func onoff(b bool) string {
	if b {
		return "attiva"
	}
	return "disattiva"
}

func usage(protocol string) string {
	return "ℹ️ Comandi:\n" +
		"• !t (in risposta a una nota vocale): trascrive la nota\n" +
		"• !transcription: mostra lo stato\n" +
		"• !transcription on|off: trascrizione automatica in questa chat\n" +
		"• !transcription global on|off: per tutte le chat " + protocol
}

func numMs(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

type pairKey struct{ protocol, attachmentID string }

// AutoTranscriber consumes "message" bus events: it transcribes new voice
// notes of chats whose effective setting is on and answers "!" commands.
type AutoTranscriber struct {
	svc       *Service
	deps      Deps
	startedAt time.Time

	mu      sync.Mutex
	pending map[pairKey]quote
	posted  map[pairKey]bool
	seen    map[string]bool
	seenQ   []string
	warned  bool

	sub  *bus.Subscription
	quit chan struct{}
	done chan struct{}
	jobs sync.WaitGroup
}

func newAutoTranscriber(s *Service) *AutoTranscriber {
	return &AutoTranscriber{svc: s, deps: s.deps, startedAt: s.deps.now(),
		pending: map[pairKey]quote{}, posted: map[pairKey]bool{}, seen: map[string]bool{}}
}

func (a *AutoTranscriber) start() {
	a.sub = a.deps.Bus.Subscribe(bus.Types(bus.Message))
	a.quit = make(chan struct{})
	a.done = make(chan struct{})
	a.svc.SetOnDone(a.onTranscriptionDone)
	go a.loop()
}

func (a *AutoTranscriber) stop() {
	a.svc.SetOnDone(nil)
	if a.sub != nil {
		a.sub.Close()
	}
	if a.quit != nil {
		close(a.quit)
		select {
		case <-a.done:
		case <-time.After(time.Second):
		}
	}
}

func (a *AutoTranscriber) loop() {
	defer close(a.done)
	for {
		select {
		case <-a.quit:
			return
		case ev, ok := <-a.sub.C():
			if !ok {
				return
			}
			if ev.Type == bus.Gap {
				a.deps.log().Warn("Auto-trascrizione: eventi persi", "payload", ev.Payload)
				continue
			}
			if ev.Type != bus.Message {
				continue
			}
			a.safeHandle(ev.Payload)
		}
	}
}

func (a *AutoTranscriber) safeHandle(payload map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			a.deps.log().Error("Auto-trascrizione: evento non gestito", "panic", r)
		}
	}()
	a.HandleMessage(payload)
}

// run executes fn off the current goroutine (inline in tests); never panics.
func (a *AutoTranscriber) run(fn func()) {
	job := func() {
		defer func() {
			if r := recover(); r != nil {
				a.deps.log().Error("Auto-trascrizione: operazione fallita", "panic", r)
			}
		}()
		fn()
	}
	if a.deps.Inline {
		job()
		return
	}
	a.jobs.Add(1)
	go func() { defer a.jobs.Done(); job() }()
}

func (a *AutoTranscriber) isRecent(tsMs any) bool {
	ms, ok := numMs(tsMs)
	if !ok {
		return false
	}
	ts := time.UnixMilli(ms)
	now := a.deps.now()
	if now.Sub(ts) > RecentWindow {
		return false
	}
	return !ts.Before(a.startedAt.Add(-StartMargin))
}

// normalize turns a bus payload (maps or api structs) into plain JSON maps.
func normalize(payload map[string]any) map[string]any {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// HandleMessage reacts to one "message" payload (cheap checks, jobs off-thread).
func (a *AutoTranscriber) HandleMessage(payload map[string]any) {
	p := normalize(payload)
	if p == nil || p["added"] != true {
		return
	}
	protocol, _ := p["protocol"].(string)
	contactID, _ := p["contact_id"].(string)
	message, ok := p["message"].(map[string]any)
	if !isAutoProtocol(protocol) || contactID == "" || !ok {
		return
	}
	ts, has := message["timestamp"]
	if !has {
		ts = p["timestamp"]
	}
	if kind := message["media_kind"]; kind != nil && kind != "" {
		a.deps.log().Info("Auto-trascrizione: messaggio", "protocol", protocol, "contact", contactID,
			"media_kind", kind, "mio", message["is_mine"], "attachment_id", message["attachment_id"],
			"recente", a.isRecent(ts))
	}
	if !a.isRecent(ts) {
		return
	}
	identity := fmt.Sprintf("%s|%s|%v|%v|%v", protocol, contactID, message["id"], ts, message["text"])
	a.mu.Lock()
	if a.seen[identity] {
		a.mu.Unlock()
		return
	}
	a.seen[identity] = true
	a.seenQ = append(a.seenQ, identity)
	if len(a.seenQ) > maxSeen {
		delete(a.seen, a.seenQ[0])
		a.seenQ = a.seenQ[1:]
	}
	a.mu.Unlock()

	if isVoice(message) {
		a.run(func() { a.onVoice(protocol, contactID, message) })
		return
	}
	text, _ := message["text"].(string)
	if message["is_mine"] == true && strings.HasPrefix(strings.TrimLeftFunc(text, isSpaceRune), "!") {
		a.run(func() { a.onCommand(protocol, contactID, message, pyStrip(text)) })
	}
}

func isSpaceRune(r rune) bool { return strings.ContainsRune(" \t\n\r\v\f  ", r) }

func isAutoProtocol(p string) bool {
	for _, x := range config.AutoTranscribeProtocols {
		if x == p {
			return true
		}
	}
	return false
}

// ---- voice notes -------------------------------------------------------------

func msgIDQuote(protocol, contactID string, id any, ts any) quote {
	q := quote{protocol: protocol, contactID: contactID}
	if id != nil && id != "" {
		q.messageID = fmt.Sprint(id)
	}
	if ms, ok := numMs(ts); ok {
		q.timestamp = ms
	}
	return q
}

func (a *AutoTranscriber) onVoice(protocol, contactID string, message map[string]any) {
	cfg := a.deps.Config
	if !cfg.AutoTranscribeEffective(protocol, contactID) {
		a.deps.log().Info("Auto-trascrizione disattivata: nota vocale ignorata",
			"protocol", protocol, "contact", contactID)
		return
	}
	if !a.svc.Active() {
		a.mu.Lock()
		first := !a.warned
		a.warned = true
		a.mu.Unlock()
		if first {
			a.deps.log().Warn("Auto-trascrizione ignorata: trascrizione non configurata",
				"provider", cfg.TranscriptionProvider())
		}
		return
	}
	outcome := a.request(msgIDQuote(protocol, contactID, message["id"], message["timestamp"]), message)
	a.deps.log().Info("Auto-trascrizione", "protocol", protocol, "contact", contactID,
		"attachment_id", message["attachment_id"], "esito", outcome)
}

// request queues the transcription of message's voice note. It returns
// "queued", "posted", "duplicate", "unconfigured" or "error".
func (a *AutoTranscriber) request(q quote, message map[string]any) string {
	log := a.deps.log()
	raw, _ := message["attachment_id"].(string)
	id := pyStrip(raw)
	if id == "" {
		log.Warn("Auto-trascrizione: nota vocale senza attachment_id")
		return "error"
	}
	key := pairKey{q.protocol, id}
	if (q.protocol == "whatsapp" || q.protocol == "telegram") && q.messageID == "" {
		log.Warn("Auto-trascrizione: nota vocale senza id, ignorata")
		return "error"
	}
	if !a.svc.Active() {
		return "unconfigured"
	}
	a.mu.Lock()
	if a.posted[key] {
		a.mu.Unlock()
		return "duplicate"
	}
	if _, ok := a.pending[key]; ok {
		a.mu.Unlock()
		return "duplicate"
	}
	a.pending[key] = q
	a.mu.Unlock()

	fail := func(err error) string {
		a.mu.Lock()
		delete(a.pending, key)
		a.mu.Unlock()
		log.Error("Auto-trascrizione: invio fallito", "protocol", key.protocol,
			"attachment_id", key.attachmentID, "error", err)
		return "error"
	}
	ctx := context.Background()
	cur, err := a.svc.Status(ctx, key.protocol, key.attachmentID)
	if err != nil {
		return fail(err)
	}
	if cur != nil && cur.Status == "ok" {
		a.finish(key, *cur)
		return "posted"
	}
	if cur == nil || cur.Status != "pending" {
		if a.deps.Media == nil {
			return fail(fmt.Errorf("media non disponibile"))
		}
		blob, err := a.deps.Media.AttachmentBlob(ctx, key.protocol, key.attachmentID)
		if err != nil {
			return fail(err)
		}
		log.Info("Auto-trascrizione: file in coda", "file", blob.Path)
		if err := a.svc.Submit(ctx, key.protocol, key.attachmentID, blob.Path); err != nil {
			return fail(err)
		}
	}
	return "queued"
}

func (a *AutoTranscriber) onTranscriptionDone(protocol, attachmentID string, row store.Transcription) {
	key := pairKey{protocol, attachmentID}
	a.mu.Lock()
	_, known := a.pending[key]
	a.mu.Unlock()
	if known {
		a.run(func() { a.finish(key, row) })
	}
}

func (a *AutoTranscriber) finish(key pairKey, row store.Transcription) {
	a.mu.Lock()
	q, ok := a.pending[key]
	if !ok || a.posted[key] {
		a.mu.Unlock()
		return
	}
	delete(a.pending, key)
	good := row.Status == "ok"
	if good {
		a.posted[key] = true
	}
	a.mu.Unlock()
	if !good && row.Status != "failed" {
		return
	}
	errStr := ""
	if row.Error != nil {
		errStr = *row.Error
	}
	a.deps.log().Info("Auto-trascrizione: esito", "protocol", key.protocol,
		"attachment_id", key.attachmentID, "status", row.Status, "error", errStr)
	var text string
	if good {
		body := ""
		if row.Text != nil {
			body = pyStrip(*row.Text)
		}
		if body == "" {
			body = emptyText
		}
		text = TranscriptionPrefix + body
	} else {
		text = errorText
	}
	if !a.post(q, text) && good {
		a.mu.Lock() // let a later !t retry
		delete(a.posted, key)
		a.mu.Unlock()
	}
}

// ---- commands ------------------------------------------------------------------

func (a *AutoTranscriber) onCommand(protocol, contactID string, message map[string]any, text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	name := strings.ToLower(parts[0])
	args := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		args = append(args, strings.ToLower(p))
	}
	reply := msgIDQuote(protocol, contactID, message["id"], message["timestamp"])
	reply.text = text
	switch name {
	case "!t":
		a.cmdT(protocol, contactID, message, reply)
	case "!transcription":
		a.cmdTranscription(protocol, contactID, args, reply)
	}
}

func (a *AutoTranscriber) cmdT(protocol, contactID string, message map[string]any, reply quote) {
	var qm *QuotedMessage
	if a.deps.Quoted != nil {
		rid, _ := message["reply_to_message_id"].(string)
		qts := int64(0)
		if f, ok := message["quote_timestamp"].(float64); ok {
			qts = int64(f)
		}
		var err error
		qm, err = a.deps.Quoted.FindQuoted(context.Background(), protocol, contactID, pyStrip(rid), qts)
		if err != nil {
			a.deps.log().Debug("Lookup del messaggio citato fallito", "error", err)
			qm = nil
		}
	}
	if qm == nil || (qm.MediaKind != "voice" && qm.MediaKind != "audio") || qm.AttachmentID == "" {
		a.post(reply, tHint)
		return
	}
	if !a.svc.Active() {
		a.post(reply, notConfiguredText)
		return
	}
	voice := quote{protocol: protocol, contactID: contactID, timestamp: qm.Timestamp, messageID: qm.MsgID}
	m := map[string]any{"attachment_id": qm.AttachmentID}
	if a.request(voice, m) == "error" {
		a.post(reply, errorText)
	}
}

func (a *AutoTranscriber) cmdTranscription(protocol, contactID string, args []string, reply quote) {
	cfg := a.deps.Config
	switch {
	case len(args) == 0:
		glob := cfg.AutoTranscribeGlobal(protocol)
		override := cfg.AutoTranscribeChat(protocol, contactID)
		effective := cfg.AutoTranscribeEffective(protocol, contactID)
		chat := "non impostato"
		if override != nil {
			chat = onoff(*override)
		}
		a.post(reply, fmt.Sprintf("ℹ️ Trascrizione automatica (%s)\n• Globale: %s\n• Questa chat: %s\n• Effettivo: %s\n\n",
			protocol, onoff(glob), chat, onoff(effective))+usage(protocol))
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		enabled := args[0] == "on"
		if err := cfg.SetAutoTranscribeChat(protocol, contactID, &enabled); err != nil {
			a.deps.log().Error("Auto-trascrizione: salvataggio fallito", "error", err)
			return
		}
		a.post(reply, "✅ Trascrizione automatica "+onoff(enabled)+" in questa chat.")
	case len(args) == 2 && args[0] == "global" && (args[1] == "on" || args[1] == "off"):
		enabled := args[1] == "on"
		if err := cfg.SetAutoTranscribeGlobal(protocol, enabled); err != nil {
			a.deps.log().Error("Auto-trascrizione: salvataggio fallito", "error", err)
			return
		}
		a.post(reply, "✅ Trascrizione automatica "+onoff(enabled)+" per tutte le chat "+protocol+".")
	default:
		a.post(reply, "⚠️ Argomenti non validi.\n\n"+usage(protocol))
	}
}

// ---- posting ---------------------------------------------------------------------

// post sends text as a quoted reply from the user's account via messages.send.
// Replies never start with "!" (they echo back as own messages). It reports
// whether the send succeeded.
func (a *AutoTranscriber) post(q quote, text string) bool {
	if strings.HasPrefix(strings.TrimLeftFunc(text, isSpaceRune), "!") {
		text = "ℹ️ " + text
	}
	req := map[string]any{"protocol": q.protocol, "contact_id": q.contactID, "text": text}
	if q.protocol == "signal" {
		req["quote_timestamp"] = q.timestamp
		req["quote_author"] = q.contactID
	} else if q.messageID == "" {
		req["reply_to_message_id"] = nil
	} else {
		req["reply_to_message_id"] = q.messageID
	}
	if q.text != "" {
		req["quote_message"] = q.text
	}
	params, _ := json.Marshal(req)
	d := a.svc.dispatcher()
	if d == nil {
		a.deps.log().Error("Auto-trascrizione: risposta non inviata (nessun dispatcher)")
		return false
	}
	if _, err := d.Call(context.Background(), api.OpMessagesSend, params); err != nil {
		a.deps.log().Error("Auto-trascrizione: risposta non inviata", "protocol", q.protocol,
			"contact", q.contactID, "error", err)
		return false
	}
	return true
}

// Wait blocks until the in-flight jobs finished (tests).
func (a *AutoTranscriber) Wait() { a.jobs.Wait() }
