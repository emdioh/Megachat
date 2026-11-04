package transcribe

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

type opsEnv struct {
	reg *ops.Registry
	svc *Service
	st  *memStore
	api *fakeAPI
	cfg interface {
		SetAPIKey(string, string) error
	}
}

func newOpsEnv(t *testing.T, withKey bool, audio string) *opsEnv {
	t.Helper()
	e := &opsEnv{reg: ops.NewRegistry(), st: newMemStore(), api: newFakeAPI(t)}
	base := ""
	if withKey {
		base = e.api.URL
	}
	cfg := newConfig(t, base)
	e.cfg = cfg
	deps := Deps{Store: e.st, Config: cfg, Media: fakeMedia{path: audio}}
	e.svc = New(deps)
	deps.Service = e.svc
	if err := e.svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.svc.Stop)
	RegisterOps(e.reg, deps)
	return e
}

func (e *opsEnv) call(t *testing.T, name, params string) (map[string]any, *ops.Error) {
	t.Helper()
	res, err := e.reg.Call(context.Background(), name, json.RawMessage(params))
	if err != nil {
		return nil, ops.AsError(err)
	}
	return mustJSON(t, res), nil
}

func (e *opsEnv) ok(t *testing.T, name, params string) map[string]any {
	t.Helper()
	m, err := e.call(t, name, params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func (e *opsEnv) fail(t *testing.T, name, params, code, msg string) {
	t.Helper()
	_, err := e.call(t, name, params)
	if err == nil || err.Code != code || err.Message != msg {
		t.Fatalf("%s %s: err = %v, want %s %q", name, params, err, code, msg)
	}
}

func TestRegisteredOps(t *testing.T) {
	e := newOpsEnv(t, true, "")
	for _, n := range []string{api.OpTranscribeConfig, api.OpTranscribeStatus, api.OpTranscribeSubmit,
		api.OpTranscribeSetApiKey, api.OpConfigSetOpenaiKey, api.OpTranscribeSettings, api.OpTranscribeSetSettings,
		api.OpTranscribeDiagnostics, api.OpTranscribeCheck, api.OpAutotranscribeConfig,
		api.OpAutotranscribeSetGlobal, api.OpAutotranscribeChat, api.OpAutotranscribeSetChat} {
		if !e.reg.Has(n) {
			t.Errorf("%s not registered", n)
		}
	}
}

func TestConfigAndUnconfigured(t *testing.T) {
	e := newOpsEnv(t, false, "")
	if m := e.ok(t, "transcribe.config", `{}`); m["enabled"] != false {
		t.Fatal(m)
	}
	e.fail(t, "transcribe.status", `{"protocol":"whatsapp","attachment_id":"a"}`, "backend_down", unconfigured)
	e.fail(t, "transcribe.submit", `{"protocol":"whatsapp","attachment_id":"a"}`, "backend_down", unconfigured)
	if m := e.ok(t, "autotranscribe.config", `{}`); m["configured"] != false {
		t.Fatal(m)
	}
}

func TestStatusUnknownAndStored(t *testing.T) {
	e := newOpsEnv(t, true, "")
	if m := e.ok(t, "transcribe.status", `{"protocol":"whatsapp","attachment_id":"zz"}`); len(m) != 1 || m["status"] != "unknown" {
		t.Fatal(m)
	}
	text, model := "ciao", "gpt-transcribe"
	_ = e.st.SetTranscription(ctx(), store.Transcription{Protocol: "whatsapp", AttachmentID: "a", Status: "ok", Text: &text, Model: &model})
	m := e.ok(t, "transcribe.status", `{"protocol":"whatsapp","attachment_id":"a"}`)
	if m["status"] != "ok" || m["text"] != "ciao" || m["error"] != nil || m["model"] != "gpt-transcribe" || m["updated_at"] == nil {
		t.Fatal(m)
	}
	e.fail(t, "transcribe.status", `{"protocol":"gmail","attachment_id":"a"}`, "invalid_request", "Invalid request")
	e.fail(t, "transcribe.status", `{"protocol":"whatsapp"}`, "invalid_request", "Invalid request")
}

func TestSubmitFlowStoresOkButRepliesDone(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	e := newOpsEnv(t, true, audio)
	if m := e.ok(t, "transcribe.submit", `{"protocol":"whatsapp","attachment_id":" att1 "}`); m["status"] != "pending" || len(m) != 1 {
		t.Fatal(m)
	}
	waitFor(t, "stored ok", func() bool { return statusOf(e.st, "att1") == "ok" })
	m := e.ok(t, "transcribe.submit", `{"protocol":"whatsapp","attachment_id":"att1"}`)
	if m["status"] != "done" || m["text"] != "testo trascritto" {
		t.Fatal(m)
	}
	if s := e.ok(t, "transcribe.status", `{"protocol":"whatsapp","attachment_id":"att1"}`); s["status"] != "ok" {
		t.Fatalf("stored status must be ok: %v", s)
	}
}

func TestSubmitPendingAndErrors(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	e := newOpsEnv(t, true, audio)
	_ = e.st.SetTranscription(ctx(), store.Transcription{Protocol: "signal", AttachmentID: "p", Status: "pending"})
	if m := e.ok(t, "transcribe.submit", `{"protocol":"signal","attachment_id":"p"}`); m["status"] != "pending" {
		t.Fatal(m)
	}
	for _, p := range []string{`{}`, `{"protocol":"gmail","attachment_id":"a"}`, `{"protocol":1,"attachment_id":"a"}`,
		`{"protocol":"signal","attachment_id":7}`, `{"protocol":"signal","attachment_id":"   "}`, `{"protocol":null,"attachment_id":"a"}`} {
		e.fail(t, "transcribe.submit", p, "invalid_request", "Invalid request")
	}
}

func TestSubmitMediaMissingIsNotFound(t *testing.T) {
	e := newOpsEnv(t, true, "")
	reg := ops.NewRegistry()
	RegisterOps(reg, Deps{Store: e.st, Config: e.svc.deps.Config, Media: fakeMedia{err: context.Canceled}, Service: e.svc})
	_, err := reg.Call(ctx(), "transcribe.submit", json.RawMessage(`{"protocol":"signal","attachment_id":"x"}`))
	if oe := ops.AsError(err); oe.Code != "not_found" || oe.Message != "Allegato non trovato" {
		t.Fatal(err)
	}
}

func TestSetAPIKeyValidationAndActivation(t *testing.T) {
	e := newOpsEnv(t, false, "")
	bad := map[string]string{
		`{"provider":"deepseek","api_key":"sk-abcdefghij"}`:                     "Provider non valido",
		`{"provider":1,"api_key":"sk-abcdefghij"}`:                              "Provider non valido",
		`{"provider":"openai","api_key":5}`:                                     "Chiave non valida",
		`{"provider":"openai","api_key":"  "}`:                                  "Chiave non valida",
		`{"provider":"openai","api_key":"sk-ab cd"}`:                            "Chiave non valida",
		`{"provider":"openai","api_key":"abcdef"}`:                              "Chiave non valida",
		`{"provider":"openrouter","api_key":"sk-abcdef"}`:                       "Chiave non valida",
		`{"provider":"gemini","api_key":"short"}`:                               "Chiave non valida",
		`{"provider":"openai","api_key":"sk-` + strings.Repeat("a", 500) + `"}`: "Chiave non valida",
	}
	for p, msg := range bad {
		e.fail(t, "transcribe.set_api_key", p, "invalid_request", msg)
	}
	if m := e.ok(t, "transcribe.set_api_key", `{"provider":"openai","api_key":" sk-abcdefghij "}`); m["status"] != "ok" {
		t.Fatal(m)
	}
	if !e.svc.Active() {
		t.Fatal("service not started by set_api_key")
	}
	if m := e.ok(t, "transcribe.settings", `{}`); m["configured"] != true || m["api_key_set"] != true {
		t.Fatal(m)
	}
	e.fail(t, "config.set_openai_key", `{"api_key":"nope"}`, "invalid_request", "Chiave non valida")
	if m := e.ok(t, "config.set_openai_key", `{"api_key":"sk-zzzzzzzzzz"}`); m["status"] != "ok" {
		t.Fatal(m)
	}
	if m := e.ok(t, "transcribe.set_api_key", `{"provider":"gemini","api_key":"`+strings.Repeat("g", 20)+`"}`); m["status"] != "ok" {
		t.Fatal(m)
	}
	s := e.ok(t, "transcribe.settings", `{}`)
	if s["provider"] != "openai" || s["providers"].(map[string]any)["gemini"].(map[string]any)["api_key_set"] != true {
		t.Fatal(s)
	}
}

func TestSettingsDefaultsPartialUpdateAndReset(t *testing.T) {
	e := newOpsEnv(t, false, "")
	s := e.ok(t, "transcribe.settings", `{}`)
	if s["provider"] != "openai" || s["model"] != "gpt-transcribe" || s["language"] != "" || s["timeout"] != float64(120) ||
		s["base_url"] != "https://api.openai.com/v1" || s["configured"] != false || s["api_key_set"] != false {
		t.Fatal(s)
	}
	or := s["providers"].(map[string]any)["openrouter"].(map[string]any)
	if or["http_referer"] != "" || or["app_title"] != "" || or["model"] != "openai/whisper-large-v3" {
		t.Fatal(or)
	}
	if _, has := s["providers"].(map[string]any)["openai"].(map[string]any)["http_referer"]; has {
		t.Fatal("openai has http_referer")
	}
	s = e.ok(t, "transcribe.set_settings", `{"model":"whisper-1","language":"it","timeout":60,"base_url":"https://x.test/v1/"}`)
	if s["model"] != "whisper-1" || s["language"] != "it" || s["timeout"] != float64(60) || s["base_url"] != "https://x.test/v1" {
		t.Fatal(s)
	}
	s = e.ok(t, "transcribe.set_settings", `{"language":"","base_url":""}`)
	if s["language"] != "" || s["base_url"] != "https://api.openai.com/v1" || s["model"] != "whisper-1" {
		t.Fatal(s)
	}
	// provider switch: top-level model goes to the new provider
	s = e.ok(t, "transcribe.set_settings", `{"provider":"gemini","model":"models/gemini-2.5-flash"}`)
	if s["provider"] != "gemini" || s["model"] != "gemini-2.5-flash" {
		t.Fatal(s)
	}
	s = e.ok(t, "transcribe.set_settings", `{"providers":{"openrouter":{"model":"a/b","http_referer":"https://r.test","app_title":"T"}}}`)
	or = s["providers"].(map[string]any)["openrouter"].(map[string]any)
	if or["model"] != "a/b" || or["http_referer"] != "https://r.test" || or["app_title"] != "T" {
		t.Fatal(or)
	}
	s = e.ok(t, "transcribe.set_settings", `{"providers":{"openrouter":{"http_referer":"","app_title":""}}}`)
	or = s["providers"].(map[string]any)["openrouter"].(map[string]any)
	if or["http_referer"] != "" || or["app_title"] != "" {
		t.Fatal(or)
	}
}

func TestSettingsInvalidValuesSaveNothing(t *testing.T) {
	e := newOpsEnv(t, false, "")
	cases := map[string]string{
		`{"provider":"deepseek"}`: "Provider non valido",
		`{"provider":null}`:       "Provider non valido",
		`{"model":""}`:            "Modello non valido",
		`{"model":5}`:             "Modello non valido",
		`{"model":"` + strings.Repeat("m", 101) + `"}`: "Modello troppo lungo (massimo 100 caratteri)",
		`{"provider":"gemini","model":"Bad Model"}`:    "Modello Gemini non valido (es. gemini-flash-latest)",
		`{"provider":"openrouter","model":"whisper"}`:  "Modello OpenRouter non valido (formato fornitore/modello)",
		`{"base_url":5}`:             "URL non valido",
		`{"base_url":"ftp://x"}`:     "URL non valido: deve iniziare con http:// o https://",
		`{"base_url":"http://"}`:     "URL non valido: deve iniziare con http:// o https://",
		`{"base_url":"http://a b"}`:  "URL non valido: deve iniziare con http:// o https://",
		`{"language":"ITA"}`:         "Lingua non valida: usa un codice di 2-3 lettere minuscole (es. it)",
		`{"language":3}`:             "Lingua non valida",
		`{"timeout":4}`:              "Timeout non valido: da 5 a 600 secondi",
		`{"timeout":601}`:            "Timeout non valido: da 5 a 600 secondi",
		`{"timeout":true}`:           "Timeout non valido",
		`{"timeout":"60"}`:           "Timeout non valido",
		`{"timeout":60.0}`:           "Timeout non valido",
		`{"providers":[]}`:           "Provider non valido",
		`{"providers":{"x":{}}}`:     "Provider non valido",
		`{"providers":{"openai":5}}`: "Provider non valido",
		`{"providers":{"openrouter":{"http_referer":"x"}}}`: "Referer non valido: deve essere un URL http:// o https://",
		`{"providers":{"openrouter":{"http_referer":3}}}`:   "Referer non valido",
		`{"providers":{"openrouter":{"app_title":"a\nb"}}}`: "Titolo non valido (una riga, massimo 100 caratteri)",
		`{"providers":{"openrouter":{"app_title":3}}}`:      "Titolo non valido",
	}
	for p, msg := range cases {
		e.fail(t, "transcribe.set_settings", p, "invalid_request", msg)
	}
	if s := e.ok(t, "transcribe.settings", `{}`); s["model"] != "gpt-transcribe" || s["timeout"] != float64(120) {
		t.Fatal(s)
	}
	// valid bounds
	for _, p := range []string{`{"timeout":5}`, `{"timeout":600}`, `{"language":"ita"}`} {
		e.ok(t, "transcribe.set_settings", p)
	}
}

func TestEnvOverridesEmptyByDefault(t *testing.T) {
	e := newOpsEnv(t, false, "")
	if s := e.ok(t, "transcribe.settings", `{}`); len(s["env_overrides"].([]any)) != 0 {
		t.Fatal(s)
	}
}

func TestDiagnostics(t *testing.T) {
	e := newOpsEnv(t, true, "")
	text := "x"
	_ = e.st.SetTranscription(ctx(), store.Transcription{Protocol: "signal", AttachmentID: "d", Status: "ok", Text: &text})
	d := e.ok(t, "transcribe.diagnostics", `{}`)
	if d["provider"] != "openai" || d["api_key_set"] != true || d["service_active"] != true ||
		d["service_client"] != "CloudTranscriptionClient" || d["service_model"] != "gpt-transcribe" ||
		d["worker_alive"] != true || d["queue_size"] != float64(0) || d["auto_listener"] != false {
		t.Fatal(d)
	}
	if _, ok := d["ffmpeg"].(bool); !ok {
		t.Fatal(d["ffmpeg"])
	}
	if len(d["recent"].([]any)) != 1 || len(d["auto_transcribe"].(map[string]any)) != 3 {
		t.Fatal(d)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "sk-test") {
		t.Fatal("key leaked")
	}
	e.st.fail = context.Canceled
	d = e.ok(t, "transcribe.diagnostics", `{}`)
	rec := d["recent"].([]any)[0].(map[string]any)
	if rec["status"] != "db_error" || rec["error"] == "" {
		t.Fatal(rec)
	}
	e.st.fail = nil
	e.svc.Stop()
	d = e.ok(t, "transcribe.diagnostics", `{}`)
	if d["service_active"] != false || d["service_client"] != nil || d["queue_size"] != nil || d["worker_alive"] != false {
		t.Fatal(d)
	}
}

func TestCheck(t *testing.T) {
	e := newOpsEnv(t, false, "")
	m := e.ok(t, "transcribe.check", `{}`)
	if m["ok"] != false || m["detail"] != "Chiave API mancante" || m["provider"] != "openai" || m["url"] != nil {
		t.Fatal(m)
	}
	e = newOpsEnv(t, true, "")
	e.api.handler = func(_ int, u upload, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"echo":"` + u.Auth + `"}`))
	}
	m = e.ok(t, "transcribe.check", `{}`)
	if m["ok"] != true || m["http_status"] != float64(200) || m["url"] != e.api.URL+"/models" {
		t.Fatal(m)
	}
	if strings.Contains(m["detail"].(string), "sk-test-key") || !strings.Contains(m["detail"].(string), "Bearer ***") {
		t.Fatalf("detail not redacted: %v", m["detail"])
	}
	e.api.handler = func(_ int, _ upload, w http.ResponseWriter) { w.WriteHeader(401); _, _ = w.Write([]byte("nope")) }
	m = e.ok(t, "transcribe.check", `{}`)
	if m["ok"] != false || m["http_status"] != float64(401) || m["detail"] != "nope" {
		t.Fatal(m)
	}
	// network error: base URL nobody listens on
	e.ok(t, "transcribe.set_settings", `{"base_url":"http://127.0.0.1:1"}`)
	m = e.ok(t, "transcribe.check", `{}`)
	if m["ok"] != false || !strings.HasPrefix(m["detail"].(string), "Errore di rete: ") || m["http_status"] != nil {
		t.Fatal(m)
	}
}

func TestCheckRequestPerProvider(t *testing.T) {
	u, h := checkRequest("openrouter", "https://o/api/v1", "k")
	if u != "https://o/api/v1/key" || h["Authorization"] != "Bearer k" {
		t.Fatal(u, h)
	}
	u, h = checkRequest("gemini", "https://g/v1beta", "k")
	if u != "https://g/v1beta/models?pageSize=1" || h["x-goog-api-key"] != "k" {
		t.Fatal(u, h)
	}
	u, _ = checkRequest("openai", "https://a/v1", "k")
	if u != "https://a/v1/models" {
		t.Fatal(u)
	}
}

func TestRedact(t *testing.T) {
	in := "sk-abcdef123 AIzaSyA1234567890 Bearer token: abc Bearer abcdefgh12 Bearer short"
	want := "sk-*** AIza*** Bearer token: *** Bearer *** Bearer short"
	if got := Redact(in); got != want {
		t.Fatalf("%q", got)
	}
}

func TestAutotranscribeOps(t *testing.T) {
	e := newOpsEnv(t, true, "")
	if m := e.ok(t, "autotranscribe.config", `{}`); m["configured"] != true || len(m["globals"].(map[string]any)) != 3 {
		t.Fatal(m)
	}
	g := e.ok(t, "autotranscribe.set_global", `{"protocol":"signal","enabled":true}`)
	if g["signal"] != true || g["whatsapp"] != false || g["telegram"] != false {
		t.Fatal(g)
	}
	c := e.ok(t, "autotranscribe.chat", `{"protocol":"signal","contact_id":"+1"}`)
	if c["global"] != true || c["override"] != nil || c["effective"] != true {
		t.Fatal(c)
	}
	c = e.ok(t, "autotranscribe.set_chat", `{"protocol":"signal","contact_id":"+1","enabled":false}`)
	if c["override"] != false || c["effective"] != false {
		t.Fatal(c)
	}
	c = e.ok(t, "autotranscribe.set_chat", `{"protocol":"signal","contact_id":"+1","enabled":null}`)
	if c["override"] != nil || c["effective"] != true {
		t.Fatal(c)
	}
	for _, tc := range [][2]string{
		{"autotranscribe.set_global", `{"protocol":"gmail","enabled":true}`},
		{"autotranscribe.set_global", `{"protocol":"signal","enabled":"true"}`},
		{"autotranscribe.set_global", `{"protocol":"signal"}`},
		{"autotranscribe.chat", `{"protocol":"signal","contact_id":""}`},
		{"autotranscribe.set_chat", `{"protocol":"signal","contact_id":"+1"}`},
	} {
		e.fail(t, tc[0], tc[1], "invalid_request", "Invalid request")
	}
}
