// Package config ports the configuration loading of the Python server
// (protocols/config.py, transcription/config.py, server/paths.py,
// server/lock.py, server/main.py, server/thumbnails.py, protocols/db.py ...).
//
// Resolution order, as in Python: environment variable (when non-empty),
// then “config.json“ in the project directory, then (for a few legacy
// settings only) the project “.env“, then the default. config.json is
// re-read on every access so edits take effect without a restart.
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

// Config resolves settings from env, config.json and .env.
type Config struct {
	// ProjectDir holds config.json, .env and bin/ (Python: the repo root).
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

// LoadDotenv best-effort parses the project .env (KEY=VALUE lines; no quote
// handling, like Python).
func (c *Config) LoadDotenv() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(filepath.Join(c.ProjectDir, ".env"))
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

// Get returns the env var if non-empty, else the config.json value, else def.
func (c *Config) Get(key, envName, def string) string {
	if v := c.Env(envName); v != "" {
		return v
	}
	if v, ok := c.LoadFile()[key]; ok && v != nil {
		return pyStr(v)
	}
	return def
}

// GetInt returns an int: env (non-empty), else config.json, else def.
// Invalid values yield def.
func (c *Config) GetInt(key, envName string, def int) int {
	var raw any = c.Env(envName)
	if raw == "" {
		if v, ok := c.LoadFile()[key]; ok {
			raw = v
		} else {
			return def
		}
	}
	if n, ok := pyInt(raw); ok {
		return n
	}
	return def
}

// GetBool returns a bool: env (non-blank), else config.json, else def.
// Truthy 1/true/yes/on, falsy 0/false/no/off (case-insensitive); anything
// else yields def.
func (c *Config) GetBool(key, envName string, def bool) bool {
	raw, ok := c.envSet(envName)
	var s string
	if !ok || strings.TrimSpace(raw) == "" {
		v, present := c.LoadFile()[key]
		if !present {
			return def
		}
		s = pyStr(v)
	} else {
		s = raw
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
