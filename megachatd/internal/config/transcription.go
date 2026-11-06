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
	// Stored setting names of the transcription provider objects.
	ProviderModelSetting   = "transcription_model"
	ProviderBaseURLSetting = "base_url"
)

// providerDefaults are the values used when config.json and the environment
// leave a provider setting unset.
var providerDefaults = map[string]map[string]string{
	"openai": {
		ProviderModelSetting:   "gpt-transcribe",
		ProviderBaseURLSetting: "https://api.openai.com/v1",
	},
	"openrouter": {
		ProviderModelSetting:   "openai/whisper-large-v3",
		ProviderBaseURLSetting: "https://openrouter.ai/api/v1",
		"http_referer":         "",
		"app_title":            "",
	},
	"gemini": {
		ProviderModelSetting:   "gemini-flash-latest",
		ProviderBaseURLSetting: "https://generativelanguage.googleapis.com/v1beta",
	},
}

// providerSettingNames lists the settable fields of a provider object.
var providerSettingNames = map[string][]string{
	"openai":     {ProviderModelSetting, ProviderBaseURLSetting},
	"openrouter": {ProviderModelSetting, ProviderBaseURLSetting, "http_referer", "app_title"},
	"gemini":     {ProviderModelSetting, ProviderBaseURLSetting},
}

// sharedTranscriptionSettings are the settings stored under "transcription".
var sharedTranscriptionSettings = map[string]bool{
	"provider": true, "language": true, "timeout": true,
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

// TranscriptionProvider is transcription.provider; unknown -> openai.
func (c *Config) TranscriptionProvider() string {
	v := strings.ToLower(c.trimmedString("transcription", "provider", ""))
	if contains(TranscriptionProviders, v) {
		return v
	}
	return DefaultTranscriptionProvider
}

// APIKey returns the provider's API key (<provider>.api_key) or "" when unset.
// provider "" means the selected provider.
func (c *Config) APIKey(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	if err := checkProvider(provider); err != nil {
		return "", err
	}
	return c.trimmedString(provider, "api_key", ""), nil
}

func (c *Config) providerSetting(provider, name string) (string, error) {
	if err := checkProvider(provider); err != nil {
		return "", err
	}
	if !contains(providerSettingNames[provider], name) {
		return "", fmt.Errorf("unknown %s setting %q", provider, name)
	}
	v := c.trimmedString(provider, name, providerDefaults[provider][name])
	if name == ProviderBaseURLSetting {
		v = strings.TrimRight(v, "/")
	}
	return v, nil
}

// ProviderModel returns the model of provider ("" = selected provider).
func (c *Config) ProviderModel(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	return c.providerSetting(provider, ProviderModelSetting)
}

// ProviderBaseURL returns the base URL of provider ("" = selected provider).
func (c *Config) ProviderBaseURL(provider string) (string, error) {
	if provider == "" {
		provider = c.TranscriptionProvider()
	}
	return c.providerSetting(provider, ProviderBaseURLSetting)
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

// TranscriptionLanguage is transcription.language ("" when unset).
func (c *Config) TranscriptionLanguage() string {
	return c.trimmedString("transcription", "language", "")
}

// TranscriptionTimeoutS defaults to 120; non-positive -> 120.
func (c *Config) TranscriptionTimeoutS() int {
	return positive(c.GetInt("transcription", "timeout", 120), 120)
}

// TranscriptionEnabled: the selected provider has an API key.
func (c *Config) TranscriptionEnabled() bool {
	k, _ := c.APIKey("")
	return k != ""
}

// AutoTranscribeGlobal is the per-protocol global switch (false if unset).
// It lives in transcription.auto and has no environment override.
func (c *Config) AutoTranscribeGlobal(protocol string) bool {
	b, _ := c.GetMap("transcription", "auto")[protocol].(bool)
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
// It lives in transcription.auto_chats and has no environment override.
func (c *Config) AutoTranscribeChat(protocol, contactID string) *bool {
	if b, ok := c.GetMap("transcription", "auto_chats")[protocol+":"+contactID].(bool); ok {
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

// SetAutoTranscribeGlobal persists a protocol's global switch.
func (c *Config) SetAutoTranscribeGlobal(protocol string, enabled bool) error {
	if !contains(AutoTranscribeProtocols, protocol) {
		return fmt.Errorf("unsupported protocol: %q", protocol)
	}
	return c.UpdateFile(func(d map[string]any) {
		nestedSection(d, "transcription", "auto")[protocol] = enabled
	})
}

// SetAutoTranscribeChat sets the per-chat override; nil removes it.
func (c *Config) SetAutoTranscribeChat(protocol, contactID string, enabled *bool) error {
	if !contains(AutoTranscribeProtocols, protocol) {
		return fmt.Errorf("unsupported protocol: %q", protocol)
	}
	key := protocol + ":" + contactID
	return c.UpdateFile(func(d map[string]any) {
		s := nestedSection(d, "transcription", "auto_chats")
		if enabled == nil {
			delete(s, key)
		} else {
			s[key] = *enabled
		}
	})
}

// SetAPIKey stores a provider's API key in config.json (<provider>.api_key).
func (c *Config) SetAPIKey(provider, apiKey string) error {
	if err := checkProvider(provider); err != nil {
		return err
	}
	return c.UpdateFile(func(d map[string]any) { domainObject(d, provider)["api_key"] = apiKey })
}

// TranscriptionEnvOverrides lists settings forced by an environment variable:
// "provider", "language", "timeout", "<provider>.model", "<provider>.base_url",
// "<provider>.http_referer", "<provider>.app_title".
func (c *Config) TranscriptionEnvOverrides() []string {
	var out []string
	for _, n := range []string{"provider", "language", "timeout"} {
		if _, ok := c.environment("transcription", n); ok {
			out = append(out, n)
		}
	}
	for _, p := range TranscriptionProviders {
		for _, n := range []string{"model", "base_url", "http_referer", "app_title"} {
			stored := n
			if n == "model" {
				stored = ProviderModelSetting
			}
			if !contains(providerSettingNames[p], stored) {
				continue
			}
			if _, ok := c.environment(p, stored); ok {
				out = append(out, p+"."+n)
			}
		}
	}
	return out
}

// transcriptionTarget maps a SetTranscriptionSettings name to its stored
// location. Names: provider, language, timeout, <provider>.model,
// <provider>.base_url, and openrouter.http_referer/app_title.
func transcriptionTarget(name string) (domain, setting string, ok bool) {
	if sharedTranscriptionSettings[name] {
		return "transcription", name, true
	}
	prov, field, ok := strings.Cut(name, ".")
	if !ok || checkProvider(prov) != nil {
		return "", "", false
	}
	stored := field
	if field == "model" {
		stored = ProviderModelSetting
	}
	if !contains(providerSettingNames[prov], stored) {
		return "", "", false
	}
	return prov, stored, true
}

// SetTranscriptionSettings persists settings; a nil value removes the key.
// Names: provider, language, timeout, <provider>.model, <provider>.base_url,
// <provider>.http_referer, <provider>.app_title.
func (c *Config) SetTranscriptionSettings(values map[string]any) error {
	for name := range values {
		if _, _, ok := transcriptionTarget(name); !ok {
			return fmt.Errorf("unknown transcription setting %q", name)
		}
	}
	return c.UpdateFile(func(d map[string]any) {
		for name, v := range values {
			domain, setting, _ := transcriptionTarget(name)
			if v == nil {
				if dom, ok := d[domain].(map[string]any); ok {
					delete(dom, setting)
				}
				continue
			}
			domainObject(d, domain)[setting] = v
		}
	})
}
