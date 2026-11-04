package transcribe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

const unconfigured = "Trascrizione non configurata"

var errNotConfigured error = &ops.Error{Code: api.CodeBackendDown, Message: unconfigured}

// decodeReq decodes params into T; required keys must be present (pydantic
// required fields), otherwise invalid_request "Invalid request".
func decodeReq[T any](params json.RawMessage, required ...string) (T, error) {
	var zero T
	if len(required) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(params, &m); err != nil {
			return zero, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
		}
		for _, k := range required {
			if _, ok := m[k]; !ok {
				return zero, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
			}
		}
	}
	return ops.Decode[T](params)
}

func reg1[T any](reg *ops.Registry, name, doc string, required []string, f func(ctx context.Context, req T) (any, error)) {
	reg.Register(ops.Op{Name: name, Doc: doc, Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeReq[T](params, required...)
		if err != nil {
			return nil, err
		}
		return f(ctx, req)
	}})
}

func protoOK(p string) error {
	if !api.IsProtocol(p) {
		return ops.Errorf(api.CodeInvalidRequest, "Invalid request")
	}
	return nil
}

// RegisterOps registers transcribe.{config,status,submit,set_api_key,settings,
// set_settings,diagnostics,check}, config.set_openai_key and
// autotranscribe.{config,set_global,chat,set_chat}.
func RegisterOps(reg *ops.Registry, deps Deps) {
	svc := deps.Service
	if svc == nil {
		svc = New(deps)
	}
	svc.SetDispatcher(reg)
	o := &opsImpl{svc: svc, deps: deps}

	reg1(reg, api.OpTranscribeConfig, "Whether transcription is available (the selected provider has an API key).",
		nil, func(ctx context.Context, _ struct{}) (any, error) {
			return api.TranscribeConfigOut{Enabled: svc.Active()}, nil
		})
	reg1(reg, api.OpTranscribeStatus, "Stored transcription record of an attachment, or {\"status\": \"unknown\"}.",
		[]string{"protocol", "attachment_id"}, o.status)
	reg1(reg, api.OpTranscribeSubmit, "Queue an attachment for transcription (or report its current state).",
		nil, o.submit)
	reg1(reg, api.OpTranscribeSetApiKey, "Save a provider's API key to config.json and rebuild the service.",
		nil, o.setAPIKey)
	reg1(reg, api.OpConfigSetOpenaiKey, "Save the OpenAI key (transcribe.set_api_key for OpenAI).",
		nil, func(ctx context.Context, r api.OpenAIKeyRequest) (any, error) {
			return o.setAPIKey(ctx, api.TranscribeApiKeyRequest{
				Provider: json.RawMessage(`"openai"`), APIKey: r.APIKey})
		})
	reg1(reg, api.OpTranscribeSettings, "Effective transcription settings and which ones environment variables force.",
		nil, func(ctx context.Context, _ struct{}) (any, error) { return o.settingsOut(), nil })
	reg1(reg, api.OpTranscribeSetSettings, "Partially update the settings, then rebuild the service.",
		nil, o.setSettings)
	reg1(reg, api.OpTranscribeDiagnostics, "What the transcription pipeline sees right now (never the API key).",
		nil, o.diagnostics)
	reg1(reg, api.OpTranscribeCheck, "Call a cheap authenticated endpoint of the selected provider.",
		nil, o.check)

	reg1(reg, api.OpAutotranscribeConfig, "Per-protocol global switches and whether transcription is configured.",
		nil, func(ctx context.Context, _ struct{}) (any, error) {
			return api.AutoTranscribeConfigOut{Configured: svc.Active(),
				Globals: deps.Config.AutoTranscribeGlobals()}, nil
		})
	reg1(reg, api.OpAutotranscribeSetGlobal, "Set one protocol's global switch; returns all globals.",
		[]string{"protocol", "enabled"}, func(ctx context.Context, r api.AutoTranscribeSetGlobalRequest) (any, error) {
			if err := autoProtoOK(r.Protocol); err != nil {
				return nil, err
			}
			if err := deps.Config.SetAutoTranscribeGlobal(r.Protocol, r.Enabled); err != nil {
				return nil, saveError(err)
			}
			return deps.Config.AutoTranscribeGlobals(), nil
		})
	reg1(reg, api.OpAutotranscribeChat, "{global, override, effective} of a chat (override may be null).",
		[]string{"protocol", "contact_id"}, func(ctx context.Context, r api.AutoTranscribeChatRequest) (any, error) {
			if err := autoChatOK(r.Protocol, r.ContactID); err != nil {
				return nil, err
			}
			return o.autoChat(r.Protocol, r.ContactID), nil
		})
	reg1(reg, api.OpAutotranscribeSetChat, "Set (or, with enabled=null, clear) a chat's override.",
		[]string{"protocol", "contact_id", "enabled"}, func(ctx context.Context, r api.AutoTranscribeSetChatRequest) (any, error) {
			if err := autoChatOK(r.Protocol, r.ContactID); err != nil {
				return nil, err
			}
			if err := deps.Config.SetAutoTranscribeChat(r.Protocol, r.ContactID, r.Enabled); err != nil {
				return nil, saveError(err)
			}
			return o.autoChat(r.Protocol, r.ContactID), nil
		})
}

func autoProtoOK(p string) error {
	for _, x := range config.AutoTranscribeProtocols {
		if x == p {
			return nil
		}
	}
	return ops.Errorf(api.CodeInvalidRequest, "Invalid request")
}

func autoChatOK(p, contact string) error {
	if err := autoProtoOK(p); err != nil {
		return err
	}
	if contact == "" {
		return ops.Errorf(api.CodeInvalidRequest, "Invalid request")
	}
	return nil
}

func saveError(err error) error {
	slog.Error("Impossibile salvare la configurazione di trascrizione", "error", err)
	return ops.Errorf(api.CodeInternal, "Errore di salvataggio")
}

type opsImpl struct {
	svc  *Service
	deps Deps
}

func (o *opsImpl) autoChat(protocol, contact string) api.AutoTranscribeChatOut {
	c := o.deps.Config
	return api.AutoTranscribeChatOut{
		Global:    c.AutoTranscribeGlobal(protocol),
		Override:  c.AutoTranscribeChat(protocol, contact),
		Effective: c.AutoTranscribeEffective(protocol, contact),
	}
}

func (o *opsImpl) status(ctx context.Context, r api.TranscribeStatusRequest) (any, error) {
	if err := protoOK(r.Protocol); err != nil {
		return nil, err
	}
	if !o.svc.Active() {
		return nil, errNotConfigured
	}
	cur, err := o.svc.Status(ctx, r.Protocol, r.AttachmentID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return api.TranscribeStatusUnknownOut{Status: "unknown"}, nil
	}
	return statusOut(cur), nil
}

func statusOut(t *store.Transcription) api.TranscribeStatusOut {
	u := t.UpdatedAt
	return api.TranscribeStatusOut{Status: t.Status, Text: t.Text, Error: t.Error, Model: t.Model, UpdatedAt: &u}
}

func (o *opsImpl) submit(ctx context.Context, r api.TranscribeSubmitRequest) (any, error) {
	proto, ok1 := jsonString(r.Protocol)
	id, ok2 := jsonString(r.AttachmentID)
	if !ok1 || !api.IsProtocol(proto) || !ok2 {
		return nil, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
	}
	id = pyStrip(id)
	if id == "" {
		return nil, ops.Errorf(api.CodeInvalidRequest, "Invalid request")
	}
	if !o.svc.Active() {
		return nil, errNotConfigured
	}
	cur, err := o.svc.Status(ctx, proto, id)
	if err != nil {
		return nil, err
	}
	if cur != nil && cur.Status == "ok" {
		text := ""
		if cur.Text != nil {
			text = *cur.Text
		}
		return api.TranscribeSubmitOut{Status: "done", Text: &text}, nil
	}
	if cur != nil && cur.Status == "pending" {
		return api.TranscribeSubmitOut{Status: "pending"}, nil
	}
	if o.deps.Media == nil {
		return nil, ops.Errorf(api.CodeNotFound, "Allegato non trovato")
	}
	blob, err := o.deps.Media.AttachmentBlob(ctx, proto, id)
	if err != nil {
		return nil, ops.Errorf(api.CodeNotFound, "Allegato non trovato")
	}
	if err := o.svc.Submit(ctx, proto, id, blob.Path); err != nil {
		return nil, err
	}
	return api.TranscribeSubmitOut{Status: "pending"}, nil
}

func (o *opsImpl) reinit() error {
	if err := o.svc.Reinit(); err != nil {
		slog.Error("Impossibile inizializzare la trascrizione", "error", err)
		return ops.Errorf(api.CodeInternal, "Errore di inizializzazione")
	}
	return nil
}

func (o *opsImpl) setAPIKey(ctx context.Context, r api.TranscribeApiKeyRequest) (any, error) {
	provider, key, err := validatedAPIKey(r.Provider, r.APIKey)
	if err != nil {
		return nil, err
	}
	if err := o.deps.Config.SetAPIKey(provider, key); err != nil {
		return nil, saveError(err)
	}
	if err := o.reinit(); err != nil {
		return nil, err
	}
	return api.StatusOut{Status: "ok"}, nil
}

func (o *opsImpl) settingsOut() api.TranscribeSettingsOut {
	c := o.deps.Config
	provider := c.TranscriptionProvider()
	providers := map[string]api.TranscribeProviderSettings{}
	for _, name := range config.TranscriptionProviders {
		key, _ := c.APIKey(name)
		model, _ := c.ProviderModel(name)
		base, _ := c.ProviderBaseURL(name)
		ps := api.TranscribeProviderSettings{APIKeySet: key != "", Model: model, BaseURL: base}
		if name == "openrouter" {
			h := c.OpenRouterHeaders()
			ref, title := h["HTTP-Referer"], h["X-OpenRouter-Title"]
			ps.HTTPReferer, ps.AppTitle = &ref, &title
		}
		providers[name] = ps
	}
	sel := providers[provider]
	return api.TranscribeSettingsOut{
		Configured:   o.svc.Active(),
		Provider:     provider,
		Providers:    providers,
		APIKeySet:    sel.APIKeySet,
		Model:        sel.Model,
		Language:     c.TranscriptionLanguage(),
		BaseURL:      sel.BaseURL,
		Timeout:      c.TranscriptionTimeoutS(),
		EnvOverrides: c.TranscriptionEnvOverrides(),
	}
}

func (o *opsImpl) setSettings(ctx context.Context, r api.TranscribeSettingsRequest) (any, error) {
	values, err := validatedSettings(o.deps.Config, r)
	if err != nil {
		return nil, err
	}
	if len(values) > 0 {
		if err := o.deps.Config.SetTranscriptionSettings(values); err != nil {
			return nil, saveError(err)
		}
		if err := o.reinit(); err != nil {
			return nil, err
		}
	}
	return o.settingsOut(), nil
}

func (o *opsImpl) diagnostics(ctx context.Context, _ struct{}) (any, error) {
	c := o.deps.Config
	provider := c.TranscriptionProvider()
	key, _ := c.APIKey(provider)
	model, _ := c.ProviderModel(provider)
	base, _ := c.ProviderBaseURL(provider)
	out := api.TranscribeDiagnosticsOut{
		Provider: provider, APIKeySet: key != "", Model: model, BaseURL: base,
		AutoTranscribe: c.AutoTranscribeGlobals(),
		AutoListener:   o.svc.hasOnDone(),
		FFmpeg:         FFmpegAvailable(),
		EnvOverrides:   c.TranscriptionEnvOverrides(),
	}
	if w := o.svc.worker(); w != nil {
		out.ServiceActive = true
		kind, m, b := w.client.Kind(), w.client.Model(), w.client.BaseURL()
		out.ServiceClient, out.ServiceModel, out.ServiceBase = &kind, &m, &b
		select {
		case <-w.done:
		default:
			out.WorkerAlive = true
		}
		q := w.queueSize()
		out.QueueSize = &q
	}
	rows, err := o.deps.Store.RecentTranscriptions(ctx, 10)
	if err != nil {
		out.Recent = []api.TranscribeDBErrorRow{{Status: "db_error", Error: err.Error()}}
	} else {
		recent := make([]api.TranscribeRecentRow, 0, len(rows))
		for _, t := range rows {
			u := t.UpdatedAt
			recent = append(recent, api.TranscribeRecentRow{Protocol: t.Protocol, AttachmentID: t.AttachmentID,
				Status: t.Status, Text: t.Text, Error: t.Error, Model: t.Model, UpdatedAt: &u})
		}
		out.Recent = recent
	}
	return out, nil
}

func checkRequest(provider, base, key string) (string, map[string]string) {
	switch provider {
	case "openrouter":
		return base + "/key", map[string]string{"Authorization": "Bearer " + key}
	case "gemini":
		return base + "/models?pageSize=1", map[string]string{"x-goog-api-key": key}
	}
	return base + "/models", map[string]string{"Authorization": "Bearer " + key}
}

var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{6,}`), "sk-***"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{10,}`), "AIza***"},
	{regexp.MustCompile(`(Bearer token:\s*)\S+`), "${1}***"},
	{regexp.MustCompile(`(Bearer\s+)[A-Za-z0-9._\-]{8,}`), "${1}***"},
}

// Redact masks API keys and bearer tokens (server/logbuffer.py redact).
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

func (o *opsImpl) check(ctx context.Context, _ struct{}) (any, error) {
	c := o.deps.Config
	provider := c.TranscriptionProvider()
	key, _ := c.APIKey(provider)
	if key == "" {
		return api.TranscribeCheckOut{OK: false, Provider: provider, Detail: "Chiave API mancante"}, nil
	}
	base, _ := c.ProviderBaseURL(provider)
	url, headers := checkRequest(provider, base, key)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	var resp *http.Response
	if err == nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err = http.DefaultClient.Do(req)
	}
	var body []byte
	if err == nil {
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		kind := "ClientConnectionError"
		if errors.Is(err, context.DeadlineExceeded) {
			kind = "TimeoutError"
		}
		detail := "Errore di rete: " + kind + ": " + err.Error()
		slog.Warn("Verifica fallita", "provider", provider, "detail", detail)
		return api.TranscribeCheckOut{OK: false, Provider: provider, URL: url, Detail: detail}, nil
	}
	text := truncRunes(strings.ToValidUTF8(string(body), "�"), 500)
	text = strings.ReplaceAll(text, key, "***")
	status := resp.StatusCode
	ok := status == http.StatusOK
	lg := slog.Info
	if !ok {
		lg = slog.Warn
	}
	lg("Verifica provider", "provider", provider, "http_status", status, "body", truncRunes(Redact(text), 200))
	return api.TranscribeCheckOut{OK: ok, Provider: provider, URL: url, HTTPStatus: &status, Detail: Redact(text)}, nil
}
