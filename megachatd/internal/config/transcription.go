package config

import (
	"fmt"
	"strings"
)

// Transcription providers (DeepSeek has no audio input).
var (
	TranscriptionProviders = []string{"openai", "openrouter", "gemini"}
	// AutoTranscribeProtocols have automatic voice-note transcription.
	AutoTranscribeProtocols = []string{"whatsapp", "signal", "telegram"}
)

const (
	DefaultTranscriptionProvider = "openai"
	autoGlobalKey                = "auto_transcribe"
	autoChatsKey                 = "auto_transcribe_chats"
)

type apiKeySpec struct {
	configKey string
	envs      []string
}

var apiKeys = map[string]apiKeySpec{
	"openai":     {"openai_api_key", []string{"OPENAI_API_KEY"}},
	"openrouter": {"openrouter_api_key", []string{"OPENROUTER_API_KEY"}},
	"gemini":     {"gemini_api_key", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
}

type keyEnv struct{ key, env string }

type providerSetting struct {
	def   string
	pairs []keyEnv // priority order
}

var providerSettings = map[string]map[string]providerSetting{
	"openai": {
		"model": {"gpt-transcribe", []keyEnv{
			{"openai_transcription_model", "OPENAI_TRANSCRIPTION_MODEL"},
			{"transcription_model", "TRANSCRIPTION_MODEL"}}},
		"base_url": {"https://api.openai.com/v1", []keyEnv{
			{"transcription_base_url", "TRANSCRIPTION_BASE_URL"}}},
	},
	"openrouter": {
		"model":        {"openai/whisper-large-v3", []keyEnv{{"openrouter_transcription_model", "OPENROUTER_TRANSCRIPTION_MODEL"}}},
		"base_url":     {"https://openrouter.ai/api/v1", []keyEnv{{"openrouter_base_url", "OPENROUTER_BASE_URL"}}},
		"http_referer": {"", []keyEnv{{"openrouter_http_referer", "OPENROUTER_HTTP_REFERER"}}},
		"app_title":    {"", []keyEnv{{"openrouter_app_title", "OPENROUTER_APP_TITLE"}}},
	},
	"gemini": {
		"model":    {"gemini-flash-latest", []keyEnv{{"gemini_transcription_model", "GEMINI_TRANSCRIPTION_MODEL"}}},
		"base_url": {"https://generativelanguage.googleapis.com/v1beta", []keyEnv{{"gemini_base_url", "GEMINI_BASE_URL"}}},
	},
}

// SharedSettings: setting name -> (config.json key, env var).
var SharedSettings = map[string]keyEnv{
	"provider": {"ai_provider", "AI_PROVIDER"},
	"language": {"transcription_language", "TRANSCRIPTION_LANGUAGE"},
	"timeout":  {"transcription_timeout", "TRANSCRIPTION_TIMEOUT"},
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func checkProvider(p string) error {
	if !contains(TranscriptionProviders, p) {
		return fmt.Errorf("unsupported transcription provider: %q", p)
	}
	return nil
}

// TranscriptionProvider: AI_PROVIDER / ai_provider; unknown -> openai.
func (c *Config) TranscriptionProvider() string {
	v := strings.ToLower(strings.TrimSpace(c.Get("ai_provider", "AI_PROVIDER", "")))
	if contains(TranscriptionProviders, v) {
		return v
	}
	return DefaultTranscriptionProvider
}

// APIKey returns the provider's API key (env, config.json, extra envs, .env)
// or "" when unset. provider "" means the selected provider.
func (c *Config) APIKey(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	if err := checkProvider(provider); err != nil {
		return "", err
	}
	spec := apiKeys[provider]
	v := strings.TrimSpace(c.Get(spec.configKey, spec.envs[0], ""))
	if v == "" {
		for _, e := range spec.envs[1:] {
			if v = strings.TrimSpace(c.Env(e)); v != "" {
				break
			}
		}
	}
	if v == "" {
		dot := c.LoadDotenv()
		for _, e := range spec.envs {
			if v = strings.TrimSpace(dot[e]); v != "" {
				break
			}
		}
	}
	return v, nil
}

// chain: first non-empty env, then first non-empty string config key, else def.
func (c *Config) chain(pairs []keyEnv, def string) string {
	for _, p := range pairs {
		if v := strings.TrimSpace(c.Env(p.env)); v != "" {
			return v
		}
	}
	cfg := c.LoadFile()
	for _, p := range pairs {
		if s, ok := cfg[p.key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return def
}

func (c *Config) providerSetting(provider, name string) (string, error) {
	if err := checkProvider(provider); err != nil {
		return "", err
	}
	s := providerSettings[provider][name]
	v := c.chain(s.pairs, s.def)
	if name == "base_url" {
		v = strings.TrimRight(v, "/")
	}
	return v, nil
}

// ProviderModel returns the model of provider ("" = selected provider).
func (c *Config) ProviderModel(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	return c.providerSetting(provider, "model")
}

// ProviderBaseURL returns the base URL of provider ("" = selected provider).
func (c *Config) ProviderBaseURL(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	return c.providerSetting(provider, "base_url")
}

// OpenRouterHeaders are the optional attribution headers (only when set).
func (c *Config) OpenRouterHeaders() map[string]string {
	h := map[string]string{}
	if v, _ := c.providerSetting("openrouter", "http_referer"); v != "" {
		h["HTTP-Referer"] = v
	}
	if v, _ := c.providerSetting("openrouter", "app_title"); v != "" {
		h["X-OpenRouter-Title"] = v
	}
	return h
}

// TranscriptionLanguage ("" when unset).
func (c *Config) TranscriptionLanguage() string {
	return strings.TrimSpace(c.Get("transcription_language", "TRANSCRIPTION_LANGUAGE", ""))
}

// TranscriptionTimeoutS defaults to 120; non-positive -> 120.
func (c *Config) TranscriptionTimeoutS() int {
	return positive(c.GetInt("transcription_timeout", "TRANSCRIPTION_TIMEOUT", 120), 120)
}

// TranscriptionEnabled: the selected provider has an API key.
func (c *Config) TranscriptionEnabled() bool {
	k, _ := c.APIKey("")
	return k != ""
}

// AutoTranscribeGlobal is the per-protocol global switch (false if unset).
func (c *Config) AutoTranscribeGlobal(protocol string) bool {
	sec, ok := c.LoadFile()[autoGlobalKey].(map[string]any)
	if !ok {
		return false
	}
	b, _ := sec[protocol].(bool)
	return b
}

// AutoTranscribeGlobals returns all protocols' switches.
func (c *Config) AutoTranscribeGlobals() map[string]bool {
	m := map[string]bool{}
	for _, p := range AutoTranscribeProtocols {
		m[p] = c.AutoTranscribeGlobal(p)
	}
	return m
}

// AutoTranscribeChat returns the per-chat override; nil when not set.
func (c *Config) AutoTranscribeChat(protocol, contactID string) *bool {
	sec, ok := c.LoadFile()[autoChatsKey].(map[string]any)
	if !ok {
		return nil
	}
	if b, ok := sec[protocol+":"+contactID].(bool); ok {
		return &b
	}
	return nil
}

// AutoTranscribeEffective: chat override if set, else the protocol global.
func (c *Config) AutoTranscribeEffective(protocol, contactID string) bool {
	if !contains(AutoTranscribeProtocols, protocol) {
		return false
	}
	if o := c.AutoTranscribeChat(protocol, contactID); o != nil {
		return *o
	}
	return c.AutoTranscribeGlobal(protocol)
}

func section(data map[string]any, key string) map[string]any {
	if s, ok := data[key].(map[string]any); ok {
		return s
	}
	return map[string]any{}
}

// SetAutoTranscribeGlobal persists a protocol's global switch.
func (c *Config) SetAutoTranscribeGlobal(protocol string, enabled bool) error {
	if !contains(AutoTranscribeProtocols, protocol) {
		return fmt.Errorf("unsupported protocol: %q", protocol)
	}
	return c.UpdateFile(func(d map[string]any) {
		s := section(d, autoGlobalKey)
		s[protocol] = enabled
		d[autoGlobalKey] = s
	})
}

// SetAutoTranscribeChat sets the per-chat override; nil removes it.
func (c *Config) SetAutoTranscribeChat(protocol, contactID string, enabled *bool) error {
	if !contains(AutoTranscribeProtocols, protocol) {
		return fmt.Errorf("unsupported protocol: %q", protocol)
	}
	key := protocol + ":" + contactID
	return c.UpdateFile(func(d map[string]any) {
		s := section(d, autoChatsKey)
		if enabled == nil {
			delete(s, key)
		} else {
			s[key] = *enabled
		}
		d[autoChatsKey] = s
	})
}

// SetAPIKey stores a provider's API key in config.json.
func (c *Config) SetAPIKey(provider, apiKey string) error {
	if err := checkProvider(provider); err != nil {
		return err
	}
	return c.UpdateFile(func(d map[string]any) { d[apiKeys[provider].configKey] = apiKey })
}

// TranscriptionEnvOverrides lists settings forced by an environment variable:
// "provider", "language", "timeout", "<provider>.model", "<provider>.base_url".
func (c *Config) TranscriptionEnvOverrides() []string {
	var out []string
	for _, n := range []string{"provider", "language", "timeout"} {
		if c.Env(SharedSettings[n].env) != "" {
			out = append(out, n)
		}
	}
	for _, p := range TranscriptionProviders {
		for _, n := range []string{"model", "base_url", "http_referer", "app_title"} {
			s, ok := providerSettings[p][n]
			if !ok {
				continue
			}
			for _, pe := range s.pairs {
				if c.Env(pe.env) != "" {
					out = append(out, p+"."+n)
					break
				}
			}
		}
	}
	return out
}

// SetTranscriptionSettings persists settings; a nil value removes the key
// (and legacy aliases). Names: provider, language, timeout, <provider>.model,
// <provider>.base_url.
func (c *Config) SetTranscriptionSettings(values map[string]any) error {
	keysFor := func(name string) ([]string, error) {
		if s, ok := SharedSettings[name]; ok {
			return []string{s.key}, nil
		}
		prov, setting, ok := strings.Cut(name, ".")
		if ok {
			if s, ok := providerSettings[prov][setting]; ok {
				var ks []string
				for _, p := range s.pairs {
					ks = append(ks, p.key)
				}
				return ks, nil
			}
		}
		return nil, fmt.Errorf("unknown transcription setting %q", name)
	}
	for name := range values {
		if _, err := keysFor(name); err != nil {
			return err
		}
	}
	return c.UpdateFile(func(d map[string]any) {
		for name, v := range values {
			ks, _ := keysFor(name)
			if v == nil {
				for _, k := range ks {
					delete(d, k)
				}
			} else {
				d[ks[0]] = v
			}
		}
	})
}
