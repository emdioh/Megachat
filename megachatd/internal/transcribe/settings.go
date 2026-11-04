package transcribe

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

const (
	modelMax = 100
	urlMax   = 300
	keyError = "Chiave non valida"
)

var (
	languageRE    = regexp.MustCompile(`^[a-z]{2,3}$`)
	geminiModelRE = regexp.MustCompile(`^[a-z0-9.-]+$`)
)

func invalid(msg string) error { return ops.Errorf(api.CodeInvalidRequest, "%s", msg) }

func isProvider(s string) bool {
	for _, p := range config.TranscriptionProviders {
		if p == s {
			return true
		}
	}
	return false
}

// jsonString decodes raw as a JSON string; ok is false for absent/null/other.
func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	return s, true
}

type kv struct {
	key string
	val json.RawMessage
}

// orderedObject parses a JSON object keeping the document order (Python dicts
// are ordered, and the first invalid entry decides the error message).
func orderedObject(raw json.RawMessage) ([]kv, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if d, ok := tok.(json.Delim); err != nil || !ok || d != '{' {
		return nil, false
	}
	var out []kv
	for dec.More() {
		kt, err := dec.Token()
		k, ok := kt.(string)
		if err != nil || !ok {
			return nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, kv{k, v})
	}
	return out, true
}

func objectGet(obj []kv, key string) (json.RawMessage, bool) {
	var v json.RawMessage
	found := false
	for _, e := range obj {
		if e.key == key {
			v, found = e.val, true
		}
	}
	return v, found
}

func validatedAPIKey(provider, apiKey json.RawMessage) (string, string, error) {
	p, ok := jsonString(provider)
	if !ok || !isProvider(p) {
		return "", "", invalid("Provider non valido")
	}
	key, ok := jsonString(apiKey)
	if !ok {
		return "", "", invalid(keyError)
	}
	key = pyStrip(key)
	if key == "" || strings.IndexFunc(key, unicode.IsSpace) >= 0 || utf8.RuneCountInString(key) > 500 {
		return "", "", invalid(keyError)
	}
	switch {
	case p == "openai" && !strings.HasPrefix(key, "sk-"):
		return "", "", invalid(keyError)
	case p == "openrouter" && !strings.HasPrefix(key, "sk-or-"):
		return "", "", invalid(keyError)
	case p == "gemini" && utf8.RuneCountInString(key) < 20:
		return "", "", invalid(keyError)
	}
	return p, key, nil
}

func pyStrip(s string) string { return strings.TrimFunc(s, unicode.IsSpace) }

func validURL(v string) bool {
	if strings.IndexFunc(v, unicode.IsSpace) >= 0 || utf8.RuneCountInString(v) > urlMax {
		return false
	}
	u, err := url.Parse(v)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func validatedModel(provider string, raw json.RawMessage) (string, error) {
	m, ok := jsonString(raw)
	if !ok || pyStrip(m) == "" {
		return "", invalid("Modello non valido")
	}
	m = pyStrip(m)
	if utf8.RuneCountInString(m) > modelMax {
		return "", invalid("Modello troppo lungo (massimo 100 caratteri)")
	}
	switch provider {
	case "gemini":
		m = strings.TrimPrefix(m, "models/")
		if !geminiModelRE.MatchString(m) {
			return "", invalid("Modello Gemini non valido (es. gemini-flash-latest)")
		}
	case "openrouter":
		if !strings.Contains(m, "/") {
			return "", invalid("Modello OpenRouter non valido (formato fornitore/modello)")
		}
	}
	return m, nil
}

// nilIfEmpty: "" -> nil (the setting is removed).
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func validatedBaseURL(raw json.RawMessage) (any, error) {
	v, ok := jsonString(raw)
	if !ok {
		return nil, invalid("URL non valido")
	}
	v = pyStrip(v)
	if v != "" && !validURL(v) {
		return nil, invalid("URL non valido: deve iniziare con http:// o https://")
	}
	return nilIfEmpty(strings.TrimRight(v, "/")), nil
}

func validatedReferer(raw json.RawMessage) (any, error) {
	v, ok := jsonString(raw)
	if !ok {
		return nil, invalid("Referer non valido")
	}
	v = pyStrip(v)
	if v != "" && !validURL(v) {
		return nil, invalid("Referer non valido: deve essere un URL http:// o https://")
	}
	return nilIfEmpty(v), nil
}

func validatedTitle(raw json.RawMessage) (any, error) {
	v, ok := jsonString(raw)
	if !ok {
		return nil, invalid("Titolo non valido")
	}
	v = pyStrip(v)
	printable := strings.IndexFunc(v, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
	if utf8.RuneCountInString(v) > 100 || strings.ContainsAny(v, "\r\n") || !printable {
		return nil, invalid("Titolo non valido (una riga, massimo 100 caratteri)")
	}
	return nilIfEmpty(v), nil
}

// validatedSettings returns the values to store (nil = remove the key) for the
// fields present in req: provider, language, timeout, <provider>.model,
// <provider>.base_url, openrouter.http_referer, openrouter.app_title.
func validatedSettings(cfg Config, req api.TranscribeSettingsRequest) (map[string]any, error) {
	out := map[string]any{}
	selected := cfg.TranscriptionProvider()
	if len(req.Provider) > 0 {
		p, ok := jsonString(req.Provider)
		if !ok || !isProvider(p) {
			return nil, invalid("Provider non valido")
		}
		out["provider"] = p
		selected = p
	}
	if len(req.Model) > 0 {
		m, err := validatedModel(selected, req.Model)
		if err != nil {
			return nil, err
		}
		out[selected+".model"] = m
	}
	if len(req.BaseURL) > 0 {
		u, err := validatedBaseURL(req.BaseURL)
		if err != nil {
			return nil, err
		}
		out[selected+".base_url"] = u
	}
	if len(req.Providers) > 0 {
		providers, ok := orderedObject(req.Providers)
		if !ok {
			return nil, invalid("Provider non valido")
		}
		for _, e := range providers {
			values, ok := orderedObject(e.val)
			if !isProvider(e.key) || !ok {
				return nil, invalid("Provider non valido")
			}
			if v, has := objectGet(values, "model"); has {
				m, err := validatedModel(e.key, v)
				if err != nil {
					return nil, err
				}
				out[e.key+".model"] = m
			}
			if v, has := objectGet(values, "base_url"); has {
				u, err := validatedBaseURL(v)
				if err != nil {
					return nil, err
				}
				out[e.key+".base_url"] = u
			}
			if e.key == "openrouter" {
				if v, has := objectGet(values, "http_referer"); has {
					r, err := validatedReferer(v)
					if err != nil {
						return nil, err
					}
					out["openrouter.http_referer"] = r
				}
				if v, has := objectGet(values, "app_title"); has {
					t, err := validatedTitle(v)
					if err != nil {
						return nil, err
					}
					out["openrouter.app_title"] = t
				}
			}
		}
	}
	if len(req.Language) > 0 {
		l, ok := jsonString(req.Language)
		if !ok {
			return nil, invalid("Lingua non valida")
		}
		l = pyStrip(l)
		if l != "" && !languageRE.MatchString(l) {
			return nil, invalid("Lingua non valida: usa un codice di 2-3 lettere minuscole (es. it)")
		}
		out["language"] = nilIfEmpty(l)
	}
	if len(req.Timeout) > 0 {
		t, ok := jsonInt(req.Timeout)
		if !ok {
			return nil, invalid("Timeout non valido")
		}
		if t < 5 || t > 600 {
			return nil, invalid("Timeout non valido: da 5 a 600 secondi")
		}
		out["timeout"] = t
	}
	return out, nil
}

// jsonInt accepts only integer literals (Python rejects bool and float).
func jsonInt(raw json.RawMessage) (int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || strings.ContainsAny(s, ".eE\"tfn[{") {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}
