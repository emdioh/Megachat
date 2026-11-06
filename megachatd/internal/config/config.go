// Package config ports the configuration loading of the Python server
// (protocols/config.py, transcription/config.py, server/paths.py,
// server/lock.py, server/main.py, server/thumbnails.py, protocols/db.py ...).
//
// megachatd has exactly two settings sources:
//
//  1. config.json in the project directory, one JSON object per domain:
//     {"telegram": {"enabled": true}}. See schema.go for every domain and
//     setting.
//  2. Environment variables overrides of a single setting, named
//     UPPER(domain_setting) (TELEGRAM_ENABLED, WHATSAPP_SESSION_GUARD, ...).
//
// The project .env is never read as a settings source. The order is: env
// variable (when set and non-blank), then config.json[domain][setting], then
// the default. A value that cannot be coerced to the setting's type falls
// through to the next source. config.json is re-read on every access so edits
// take effect without a restart.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ProjectDirEnv is a Go-only override for the project directory (Python uses
// the repository root, derived from __file__).
const ProjectDirEnv = "MEGACHAT_PROJECT_DIR"

// Config resolves settings from env and config.json.
type Config struct {
	// ProjectDir holds config.json and bin/ (Python: the repo root).
	ProjectDir string
	// Home is the user's home directory.
	Home string
	// LookupEnv looks up an environment variable (default os.LookupEnv).
	LookupEnv func(string) (string, bool)

	writeMu sync.Mutex
}

// New builds a Config from the process environment. ProjectDir defaults to
// $MEGACHAT_PROJECT_DIR, else the working directory.
func New() *Config {
	c := &Config{LookupEnv: os.LookupEnv}
	c.Home, _ = os.UserHomeDir()
	if v, ok := os.LookupEnv(ProjectDirEnv); ok && v != "" {
		c.ProjectDir = v
	} else if wd, err := os.Getwd(); err == nil {
		c.ProjectDir = wd
	}
	return c
}

// NewWith builds a Config with explicit directories and environment map
// (used by tests; env nil means an empty environment).
func NewWith(projectDir, home string, env map[string]string) *Config {
	return &Config{
		ProjectDir: projectDir,
		Home:       home,
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
}

// Env returns the variable or "" (python os.environ.get(name, "")).
func (c *Config) Env(name string) string {
	if c.LookupEnv == nil {
		return ""
	}
	v, _ := c.LookupEnv(name)
	return v
}

func (c *Config) envSet(name string) (string, bool) {
	if c.LookupEnv == nil {
		return "", false
	}
	return c.LookupEnv(name)
}

// ---- file loading ---------------------------------------------------------

// ConfigFile is the path of config.json.
func (c *Config) ConfigFile() string { return filepath.Join(c.ProjectDir, "config.json") }

// DotenvPath is the path of the project .env. The file is never a settings
// source; it is only inspected by the startup warning and "config migrate".
func (c *Config) DotenvPath() string { return filepath.Join(c.ProjectDir, ".env") }

// LoadFile returns the parsed config.json, or an empty map when missing,
// unreadable, invalid or not a JSON object.
func (c *Config) LoadFile() map[string]any {
	data, err := os.ReadFile(c.ConfigFile())
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// ReadDotenv best-effort parses the project .env (KEY=VALUE lines; no quote
// handling) and returns it. Settings never come from here; the startup warning
// uses the key names and "config migrate" folds the values into config.json.
func (c *Config) ReadDotenv() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(c.DotenvPath())
	if err != nil {
		return out
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// DotenvExists reports whether <project>/.env exists (read or not).
func (c *Config) DotenvExists() bool {
	_, err := os.Stat(c.DotenvPath())
	return err == nil
}

// UpdateFile loads the whole config.json, applies mutate and writes it back
// (indent 2, trailing newline). Go sorts keys; Python kept insertion order.
func (c *Config) UpdateFile(mutate func(map[string]any)) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data := c.LoadFile()
	mutate(data)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	return os.WriteFile(c.ConfigFile(), buf.Bytes(), 0o644)
}

// domainObject returns config.json's d[domain] object, creating it. Writers
// use it to keep the one-object-per-domain shape.
func domainObject(d map[string]any, domain string) map[string]any {
	dom, ok := d[domain].(map[string]any)
	if !ok || dom == nil {
		dom = map[string]any{}
		d[domain] = dom
	}
	return dom
}

// nestedSection returns config.json's d[domain][setting] object, creating the
// intermediate objects.
func nestedSection(d map[string]any, domain, setting string) map[string]any {
	dom := domainObject(d, domain)
	sec, ok := dom[setting].(map[string]any)
	if !ok || sec == nil {
		sec = map[string]any{}
		dom[setting] = sec
	}
	return sec
}

// ---- resolution -----------------------------------------------------------

// environment returns the environment override of (domain, setting) when the
// variable is set and non-blank.
func (c *Config) environment(domain, setting string) (string, bool) {
	raw, ok := c.envSet(envName(domain, setting))
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return raw, true
}

// fileValue returns config.json[domain][setting] when present and not null.
func (c *Config) fileValue(domain, setting string) (any, bool) {
	dom, ok := c.LoadFile()[domain].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := dom[setting]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// rawValue is the string view of the first usable source: the env override,
// else the config.json value. It is what the string getters return.
func (c *Config) rawValue(domain, setting string) (string, bool) {
	if v, ok := c.environment(domain, setting); ok {
		return v, true
	}
	if v, ok := c.fileValue(domain, setting); ok {
		return pyStr(v), true
	}
	return "", false
}

// GetString returns the env override (non-blank), else config.json, else def.
func (c *Config) GetString(domain, setting, def string) string {
	if v, ok := c.rawValue(domain, setting); ok {
		return v
	}
	return def
}

// GetInt returns an int: env (non-blank), else config.json, else def. A value
// that is not an integer falls through to the next source.
func (c *Config) GetInt(domain, setting string, def int) int {
	if raw, ok := c.environment(domain, setting); ok {
		if n, ok := pyInt(raw); ok {
			return n
		}
	}
	if raw, ok := c.fileValue(domain, setting); ok {
		if n, ok := pyInt(raw); ok {
			return n
		}
	}
	return def
}

// GetBool returns a bool: env (non-blank), else config.json, else def.
// Truthy 1/true/yes/on, falsy 0/false/no/off (case-insensitive); a value that
// is not a boolean spelling falls through to the next source.
func (c *Config) GetBool(domain, setting string, def bool) bool {
	if raw, ok := c.environment(domain, setting); ok {
		if v, ok := lookupBool(raw); ok {
			return v
		}
	}
	if raw, ok := c.fileValue(domain, setting); ok {
		if v, ok := boolValue(raw); ok {
			return v
		}
	}
	return def
}

// GetFloat returns a float: env (non-blank), else config.json, else def.
func (c *Config) GetFloat(domain, setting string, def float64) float64 {
	if raw, ok := c.environment(domain, setting); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			return f
		}
	}
	if raw, ok := c.fileValue(domain, setting); ok {
		if f, ok := floatValue(raw); ok {
			return f
		}
	}
	return def
}

// GetMap returns an object-valued setting (config.json only: map settings have
// no env override). Missing or non-object values yield an empty map.
func (c *Config) GetMap(domain, setting string) map[string]any {
	if m, ok := c.fileValue(domain, setting); ok {
		if sec, ok := m.(map[string]any); ok && sec != nil {
			return sec
		}
	}
	return map[string]any{}
}

// boolValue coerces a JSON value to a bool: JSON booleans, the lookupBool
// spellings and the numbers 0/1.
func boolValue(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		return lookupBool(t)
	case float64:
		switch t {
		case 0:
			return false, true
		case 1:
			return true, true
		}
	}
	return false, false
}

// floatValue coerces a JSON value to a float: numbers and numeric strings.
func floatValue(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

// ---- python-ish coercions -------------------------------------------------

// pyStr mimics str(x) for JSON-decoded values (nil handled by callers).
func pyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case nil:
		return "None"
	default:
		return fmt.Sprint(t)
	}
}

// pyInt mimics int(x): ints, truncated floats, bools and integer strings.
func pyInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	case int:
		return t, true
	}
	return 0, false
}
