package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Migration file names.
const (
	// MigrationBackupSuffix is appended to config.json for the pre-migration
	// copy. An existing backup is never overwritten.
	MigrationBackupSuffix = ".pre-nested.bak"
	// MigratedDotenvSuffix is appended to the project .env once every key has
	// been migrated or reported.
	MigratedDotenvSuffix = ".migrated"
	// MaskedSecret replaces secret values in a --dry-run dump.
	MaskedSecret = "***"
)

// MigrationMapping records one moved source: From is the legacy config.json
// key or environment variable, To the new "domain.setting" path.
type MigrationMapping struct {
	From string
	To   string
}

// MigrationDrop records a source that was removed or ignored.
type MigrationDrop struct {
	Key    string
	Reason string
}

// Migration is the plan (and, after Apply, the result) of converting a project
// to the nested config.json schema. PlanMigration never writes; Apply performs
// the backup, the config.json rewrite and the .env rename.
type Migration struct {
	ProjectDir string
	ConfigFile string
	BackupPath string

	// ConfigExisted reports whether config.json was present.
	ConfigExisted bool
	// BackupNeeded reports whether Apply creates the backup (content changed,
	// file existed, no backup yet).
	BackupNeeded bool
	// BackupCreated is set by Apply when it wrote the backup.
	BackupCreated bool

	// Config is the resulting nested config.json (unknown keys preserved).
	Config map[string]any
	// ConfigChanged reports whether the result differs from the input.
	ConfigChanged bool

	// MovedKeys are the flat config.json keys rewritten to domain.setting.
	MovedKeys []MigrationMapping
	// DroppedKeys are flat config.json keys dropped (removed setting, empty
	// value, or superseded by an already nested value).
	DroppedKeys []MigrationDrop

	// .env
	DotenvPath         string
	DotenvExists       bool
	DotenvTo           string
	DotenvSafeToRename bool
	DotenvRenamed      bool
	MigratedEnv        []MigrationMapping // .env key folded into config.json
	SkippedEnv         []MigrationMapping // .env key ignored: config.json already sets it
	RemovedEnv         []string           // .env key for a removed setting
	InvalidEnv         []MigrationDrop    // .env key whose value does not parse
	UnknownEnv         []string           // .env key that maps to no setting

	original []byte
}

// readConfigObject reads config.json strictly: a missing file is an empty
// object, but invalid JSON or a non-object is an error (the migration must
// never overwrite a file it does not understand).
func readConfigObject(path string) (map[string]any, []byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("config migrate: reading %s: %w", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, true, fmt.Errorf("config migrate: %s is not a JSON object: %w", path, err)
	}
	if m == nil {
		return nil, nil, true, fmt.Errorf("config migrate: %s must contain a JSON object", path)
	}
	return m, data, true, nil
}

// nestedLookup returns config.json's [domain][setting] when present and not
// null.
func nestedLookup(d map[string]any, domain, setting string) (any, bool) {
	dom, ok := d[domain].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := dom[setting]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// nestedSet writes config.json's [domain][setting], creating the domain
// object. A domain that exists but is not an object is an error: silently
// replacing it could lose user data.
func nestedSet(d map[string]any, domain, setting string, v any) error {
	dom, ok := d[domain].(map[string]any)
	if !ok {
		if existing, present := d[domain]; present && existing != nil {
			return fmt.Errorf("%s in config.json is not an object", domain)
		}
		dom = map[string]any{}
		d[domain] = dom
	}
	dom[setting] = v
	return nil
}

// PlanMigration reads config.json and .env and computes the nested result
// without writing anything. It fails when config.json is invalid or a domain
// that must be written is not an object.
func PlanMigration(projectDir string) (*Migration, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, errors.New("config migrate: project directory not set")
	}
	if st, err := os.Stat(projectDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("config migrate: project directory %s not found", projectDir)
	}
	m := &Migration{
		ProjectDir: projectDir,
		ConfigFile: filepath.Join(projectDir, "config.json"),
		DotenvPath: filepath.Join(projectDir, ".env"),
	}
	m.BackupPath = m.ConfigFile + MigrationBackupSuffix

	file, original, existed, err := readConfigObject(m.ConfigFile)
	if err != nil {
		return nil, err
	}
	m.Config, m.original, m.ConfigExisted = file, original, existed

	// 1. Legacy flat config.json keys, in schema order so a more specific key
	// (openai_transcription_model) wins over a generic one
	// (transcription_model) and the nested form wins over both.
	for _, s := range LegacySettings() {
		for _, key := range s.LegacyKeys {
			raw, present := m.Config[key]
			if !present {
				continue
			}
			delete(m.Config, key)
			m.ConfigChanged = true
			if s.Removed() {
				m.DroppedKeys = append(m.DroppedKeys, MigrationDrop{Key: key, Reason: "setting removed"})
				continue
			}
			if _, set := nestedLookup(m.Config, s.Domain, s.Name); set {
				m.DroppedKeys = append(m.DroppedKeys, MigrationDrop{Key: key, Reason: "superseded by " + s.Path()})
				continue
			}
			if raw == nil {
				m.DroppedKeys = append(m.DroppedKeys, MigrationDrop{Key: key, Reason: "empty value"})
				continue
			}
			if err := nestedSet(m.Config, s.Domain, s.Name, raw); err != nil {
				return nil, fmt.Errorf("config migrate: %w", err)
			}
			m.MovedKeys = append(m.MovedKeys, MigrationMapping{From: key, To: s.Path()})
		}
	}

	// 2. The project .env, in schema order: the new env name wins over a
	// legacy alias for the same setting, and config.json wins over both.
	dotenv, err := readDotenvFile(m.DotenvPath)
	if err != nil {
		return nil, err
	}
	if dotenv != nil {
		m.DotenvExists = true
		handled := map[string]bool{}
		for _, s := range Settings() {
			if s.Removed() {
				for _, name := range s.LegacyEnvs {
					if _, present := dotenv[name]; present {
						m.RemovedEnv = append(m.RemovedEnv, name)
						handled[name] = true
					}
				}
				continue
			}
			names := make([]string, 0, 1+len(s.LegacyEnvs))
			if s.Env != "" {
				names = append(names, s.Env)
			}
			names = append(names, s.LegacyEnvs...)
			for _, name := range names {
				raw, present := dotenv[name]
				if !present {
					continue
				}
				handled[name] = true
				if _, set := nestedLookup(m.Config, s.Domain, s.Name); set {
					m.SkippedEnv = append(m.SkippedEnv, MigrationMapping{From: name, To: s.Path()})
					continue
				}
				v, err := s.Parse(raw)
				if err != nil {
					m.InvalidEnv = append(m.InvalidEnv, MigrationDrop{Key: name, Reason: err.Error()})
					continue
				}
				if err := nestedSet(m.Config, s.Domain, s.Name, v); err != nil {
					return nil, fmt.Errorf("config migrate: %w", err)
				}
				m.MigratedEnv = append(m.MigratedEnv, MigrationMapping{From: name, To: s.Path()})
				m.ConfigChanged = true
			}
		}
		for _, name := range sortedKeys(dotenv) {
			if !handled[name] {
				m.UnknownEnv = append(m.UnknownEnv, name)
			}
		}
		to, err := freeDotenvTarget(m.DotenvPath)
		if err != nil {
			return nil, err
		}
		m.DotenvTo = to
		// Every parsed key was migrated, skipped (reported) or dropped
		// (reported), so the file can be renamed out of the way.
		m.DotenvSafeToRename = true
	}

	sort.Strings(m.RemovedEnv)
	m.BackupNeeded = m.ConfigChanged && m.ConfigExisted && !fileExists(m.BackupPath)
	return m, nil
}

// readDotenvFile returns nil when the file does not exist.
func readDotenvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config migrate: reading %s: %w", path, err)
	}
	return ParseDotenv(data), nil
}

// freeDotenvTarget picks .env.migrated, or the first free numbered variant when
// an earlier migration already left one behind.
func freeDotenvTarget(path string) (string, error) {
	base := path + MigratedDotenvSuffix
	if !fileExists(base) {
		return base, nil
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s.%d", base, i)
		if !fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("config migrate: no free name for the migrated .env next to %s", path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MaskedJSON renders the result with secret values replaced by MaskedSecret.
func (m *Migration) MaskedJSON() ([]byte, error) {
	return marshalConfig(maskSecrets(m.Config))
}

// maskSecrets copies config.json and masks every setting whose name holds a
// credential (api_key, token, app_password, api_hash).
func maskSecrets(d map[string]any) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		dom, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		masked := make(map[string]any, len(dom))
		for sk, sv := range dom {
			if s, ok := SettingByPath(k, sk); ok && isSecretName(s.Name) {
				masked[sk] = MaskedSecret
				continue
			}
			masked[sk] = sv
		}
		out[k] = masked
	}
	return out
}

func isSecretName(name string) bool {
	switch name {
	case "api_key", "token", "app_password", "api_hash":
		return true
	}
	return false
}

// marshalConfig emits the canonical config.json form used by UpdateFile.
func marshalConfig(d map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Apply performs the planned migration: config.json.pre-nested.bak (created
// once, never overwritten), config.json rewritten with mode 0600, then .env
// renamed to .env.migrated. A plan with no changes writes nothing.
func (m *Migration) Apply() error {
	if m.ConfigChanged {
		if m.ConfigExisted && m.original != nil {
			if err := m.writeBackup(); err != nil {
				return err
			}
		}
		body, err := marshalConfig(m.Config)
		if err != nil {
			return fmt.Errorf("config migrate: encoding config.json: %w", err)
		}
		if err := writeConfig0600(m.ConfigFile, body); err != nil {
			return fmt.Errorf("config migrate: writing %s: %w", m.ConfigFile, err)
		}
	}
	if m.DotenvExists {
		if !m.DotenvSafeToRename {
			return errors.New("config migrate: .env has unhandled keys; left in place")
		}
		if err := os.Rename(m.DotenvPath, m.DotenvTo); err != nil {
			return fmt.Errorf("config migrate: renaming %s: %w", m.DotenvPath, err)
		}
		m.DotenvRenamed = true
	}
	return nil
}

// writeBackup creates config.json.pre-nested.bak with mode 0600. An existing
// backup is kept as it is.
func (m *Migration) writeBackup() error {
	f, err := os.OpenFile(m.BackupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config migrate: writing backup: %w", err)
	}
	if _, err := f.Write(m.original); err != nil {
		f.Close()
		return fmt.Errorf("config migrate: writing backup: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config migrate: writing backup: %w", err)
	}
	m.BackupCreated = true
	return nil
}

// writeConfig0600 writes body atomically and leaves the file with mode 0600:
// config.json now holds credentials.
func writeConfig0600(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if _, err := os.Stat(name); err == nil {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
