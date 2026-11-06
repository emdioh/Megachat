package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func migProject(t *testing.T, configJSON, dotenv string) string {
	t.Helper()
	dir := t.TempDir()
	if configJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if dotenv != "" {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFileString(t, path)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func sampleFor(k SettingKind) any {
	switch k {
	case KindInt:
		return 7
	case KindBool:
		return true
	case KindFloat:
		return 1.5
	case KindMap:
		return map[string]any{"whatsapp": true}
	}
	return "legacy-value"
}

func sampleEnv(k SettingKind) string {
	switch k {
	case KindInt:
		return "7"
	case KindBool:
		return "on"
	case KindFloat:
		return "1.5"
	}
	return "legacy-value"
}

func assertSetting(t *testing.T, c *Config, s Setting, want any) {
	t.Helper()
	switch s.Kind {
	case KindInt:
		if got := c.GetInt(s.Domain, s.Name, -999); got != want {
			t.Fatalf("%s = %d, want %v", s.Path(), got, want)
		}
	case KindBool:
		if got := c.GetBool(s.Domain, s.Name, !want.(bool)); got != want {
			t.Fatalf("%s = %v, want %v", s.Path(), got, want)
		}
	case KindFloat:
		if got := c.GetFloat(s.Domain, s.Name, -999); got != want {
			t.Fatalf("%s = %v, want %v", s.Path(), got, want)
		}
	case KindMap:
		if got := c.GetMap(s.Domain, s.Name); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %v, want %v", s.Path(), got, want)
		}
	default:
		if got := c.GetString(s.Domain, s.Name, ""); got != want {
			t.Fatalf("%s = %q, want %q", s.Path(), got, want)
		}
	}
}

// TestMigrateEveryLegacySource walks the schema table: every legacy config.json
// key and every legacy environment variable ends up at its new location, and
// every removed WAHA-only source is dropped and reported.
func TestMigrateEveryLegacySource(t *testing.T) {
	for _, s := range LegacySettings() {
		for _, key := range s.LegacyKeys {
			t.Run("config/"+key, func(t *testing.T) {
				dir := migProject(t, `{"`+key+`":`+jsonText(sampleFor(s.Kind))+`}`, "")
				m, err := PlanMigration(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := m.Apply(); err != nil {
					t.Fatal(err)
				}
				c := NewWith(dir, "", nil)
				if s.Removed() {
					if _, present := c.LoadFile()[key]; present {
						t.Fatalf("removed key %s survived", key)
					}
					if len(m.DroppedKeys) != 1 || m.DroppedKeys[0].Key != key {
						t.Fatalf("dropped %+v", m.DroppedKeys)
					}
					return
				}
				assertSetting(t, c, s, sampleFor(s.Kind))
				if _, present := c.LoadFile()[key]; present {
					t.Fatalf("legacy key %s survived", key)
				}
				if len(m.MovedKeys) != 1 || m.MovedKeys[0].To != s.Path() {
					t.Fatalf("moved %+v", m.MovedKeys)
				}
			})
		}
		for _, name := range s.LegacyEnvs {
			t.Run("env/"+name, func(t *testing.T) {
				dir := migProject(t, "", name+"="+sampleEnv(s.Kind)+"\n")
				m, err := PlanMigration(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := m.Apply(); err != nil {
					t.Fatal(err)
				}
				c := NewWith(dir, "", nil)
				if s.Removed() {
					if len(m.RemovedEnv) != 1 || m.RemovedEnv[0] != name {
						t.Fatalf("removed env %+v", m.RemovedEnv)
					}
					return
				}
				assertSetting(t, c, s, sampleFor(s.Kind))
				if m.MigratedEnv[0].To != s.Path() {
					t.Fatalf("migrated %+v", m.MigratedEnv)
				}
				if fileExists(filepath.Join(dir, ".env")) {
					t.Fatal(".env must be renamed")
				}
			})
		}
	}
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestMigrateFlatConfig(t *testing.T) {
	const original = `{
  "user_number": "+391",
  "signal_enabled": true,
  "web": {"token": "t0k", "port": 8080},
  "auto_transcribe": {"whatsapp": true},
  "auto_transcribe_chats": {"whatsapp:1@c.us": false},
  "openai_api_key": "sk-1",
  "openrouter_api_key": "or-1",
  "ai_provider": "gemini",
  "transcription_timeout": 90,
  "message_retention_per_contact": 500,
  "whatsapp_api_url": "http://127.0.0.1:3005",
  "custom_unknown": {"keep": true}
}`
	dir := migProject(t, original, "")
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.ConfigChanged || !m.BackupNeeded || m.BackupCreated {
		t.Fatalf("plan %+v", m)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}

	c := NewWith(dir, "", nil)
	file := c.LoadFile()
	signal, _ := file["signal"].(map[string]any)
	if signal["user_number"] != "+391" || signal["enabled"] != true {
		t.Fatalf("signal %v", file["signal"])
	}
	transcription, _ := file["transcription"].(map[string]any)
	if transcription["provider"] != "gemini" || transcription["timeout"] != float64(90) {
		t.Fatalf("transcription %v", transcription)
	}
	if auto, _ := transcription["auto"].(map[string]any); auto["whatsapp"] != true {
		t.Fatalf("auto %v", transcription["auto"])
	}
	if chats, _ := transcription["auto_chats"].(map[string]any); chats["whatsapp:1@c.us"] != false {
		t.Fatalf("auto_chats %v", transcription["auto_chats"])
	}
	openai, _ := file["openai"].(map[string]any)
	if openai["api_key"] != "sk-1" {
		t.Fatalf("openai %v", file["openai"])
	}
	openrouter, _ := file["openrouter"].(map[string]any)
	if openrouter["api_key"] != "or-1" {
		t.Fatalf("openrouter %v", file["openrouter"])
	}
	messages, _ := file["messages"].(map[string]any)
	if messages["retention_per_contact"] != float64(500) {
		t.Fatalf("messages %v", file["messages"])
	}
	web, _ := file["web"].(map[string]any)
	if web["token"] != "t0k" || web["port"] != float64(8080) {
		t.Fatalf("web %v", file["web"])
	}
	if _, present := file["custom_unknown"]; !present {
		t.Fatal("unknown config.json keys must be preserved")
	}
	if _, present := file["whatsapp_api_url"]; present {
		t.Fatal("removed setting survived")
	}
	if len(m.DroppedKeys) != 1 || m.DroppedKeys[0].Reason != "setting removed" {
		t.Fatalf("dropped %+v", m.DroppedKeys)
	}
	if fileMode(t, m.ConfigFile) != 0o600 {
		t.Fatalf("config mode %o", fileMode(t, m.ConfigFile))
	}
	if !m.BackupCreated {
		t.Fatal("backup not created")
	}
	if got := readFileString(t, m.BackupPath); got != original {
		t.Fatalf("backup differs:\n%s", got)
	}
	if fileMode(t, m.BackupPath) != 0o600 {
		t.Fatalf("backup mode %o", fileMode(t, m.BackupPath))
	}
}

func TestMigrateDotenvOnly(t *testing.T) {
	const env = "SIGNAL_ENABLED=1\nWHATSAPP_ENABLED=0\nWAHA_API_KEY=waha-secret\nGMAIL_ADDRESS=a@b\nUNKNOWN=x\n"
	dir := migProject(t, "", env)
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.DotenvExists || !m.DotenvSafeToRename || m.DotenvTo != filepath.Join(dir, ".env.migrated") {
		t.Fatalf("plan %+v", m)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	c := NewWith(dir, "", nil)
	if !c.SignalEnabled() || c.WhatsAppEnabled() || c.GmailAddress() != "a@b" {
		t.Fatalf("config %v", c.LoadFile())
	}
	if !reflect.DeepEqual(m.UnknownEnv, []string{"UNKNOWN"}) {
		t.Fatalf("unknown %v", m.UnknownEnv)
	}
	if !reflect.DeepEqual(m.RemovedEnv, []string{"WAHA_API_KEY"}) {
		t.Fatalf("removed %v", m.RemovedEnv)
	}
	if _, present := c.LoadFile()["whatsapp_api_key"]; present {
		t.Fatal("removed env value copied")
	}
	if fileExists(filepath.Join(dir, ".env")) {
		t.Fatal(".env still present")
	}
	if got := readFileString(t, filepath.Join(dir, ".env.migrated")); got != env {
		t.Fatalf("migrated .env = %q", got)
	}
	if m.BackupCreated {
		t.Fatal("no config.json existed: no backup expected")
	}
	if fileMode(t, m.ConfigFile) != 0o600 {
		t.Fatalf("mode %o", fileMode(t, m.ConfigFile))
	}
}

func TestMigrateConfigWinsOverDotenv(t *testing.T) {
	dir := migProject(t, `{"ai_provider":"openrouter"}`, "AI_PROVIDER=gemini\nTRANSCRIPTION_LANGUAGE=it\n")
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	c := NewWith(dir, "", nil)
	if c.TranscriptionProvider() != "openrouter" || c.TranscriptionLanguage() != "it" {
		t.Fatalf("provider %q language %q", c.TranscriptionProvider(), c.TranscriptionLanguage())
	}
	if len(m.SkippedEnv) != 1 || m.SkippedEnv[0].From != "AI_PROVIDER" {
		t.Fatalf("skipped %+v", m.SkippedEnv)
	}

	// The already nested value also beats a legacy flat key.
	dir = migProject(t, `{"transcription":{"provider":"openai"},"ai_provider":"gemini"}`, "")
	m, err = PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := NewWith(dir, "", nil).TranscriptionProvider(); got != "openai" {
		t.Fatalf("nested value must win, got %q", got)
	}
	if len(m.DroppedKeys) != 1 || !strings.Contains(m.DroppedKeys[0].Reason, "superseded") {
		t.Fatalf("dropped %+v", m.DroppedKeys)
	}
}

func TestMigrateNewEnvNameBeatsLegacyEnv(t *testing.T) {
	dir := migProject(t, "", "WA_LAZY_MEDIA=off\nWHATSAPP_LAZY_MEDIA=on\n")
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if !NewWith(dir, "", nil).WALazyMedia() {
		t.Fatal("WHATSAPP_LAZY_MEDIA must win")
	}
	if len(m.SkippedEnv) != 1 || m.SkippedEnv[0].From != "WA_LAZY_MEDIA" {
		t.Fatalf("skipped %+v", m.SkippedEnv)
	}
}

func TestMigrateBackupNeverOverwritten(t *testing.T) {
	dir := migProject(t, `{"user_number":"+391"}`, "")
	backup := filepath.Join(dir, "config.json"+MigrationBackupSuffix)
	if err := os.WriteFile(backup, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.BackupNeeded {
		t.Fatal("an existing backup must not be replaced")
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if m.BackupCreated {
		t.Fatal("backup reported as created")
	}
	if got := readFileString(t, backup); got != "sentinel" {
		t.Fatalf("backup overwritten: %q", got)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := migProject(t, `{"user_number":"+391","ai_provider":"gemini"}`,
		"SIGNAL_ENABLED=1\nOPENAI_API_KEY=sk-x\n")
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	configBefore := readFileString(t, m.ConfigFile)
	backupBefore := readFileString(t, m.BackupPath)
	backupMode := fileMode(t, m.BackupPath)

	m2, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.ConfigChanged || m2.DotenvExists || m2.BackupNeeded {
		t.Fatalf("second plan must be a no-op: %+v", m2)
	}
	if err := m2.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, m.ConfigFile); got != configBefore {
		t.Fatalf("config rewritten:\n%s", got)
	}
	if got := readFileString(t, m.BackupPath); got != backupBefore {
		t.Fatal("backup rewritten")
	}
	if fileMode(t, m.BackupPath) != backupMode || fileMode(t, m.ConfigFile) != 0o600 {
		t.Fatal("modes changed")
	}
}

func TestMigrateRefusesBrokenConfig(t *testing.T) {
	for _, body := range []string{`{bad`, `[1,2]`, `null`} {
		dir := migProject(t, body, "SIGNAL_ENABLED=1\n")
		if _, err := PlanMigration(dir); err == nil {
			t.Fatalf("%q: expected an error", body)
		}
		if got := readFileString(t, filepath.Join(dir, "config.json")); got != body {
			t.Fatalf("%q rewritten as %q", body, got)
		}
		if fileExists(filepath.Join(dir, "config.json"+MigrationBackupSuffix)) {
			t.Fatalf("%q: unexpected backup", body)
		}
		if !fileExists(filepath.Join(dir, ".env")) {
			t.Fatalf("%q: .env must be untouched", body)
		}
	}
}

func TestMigrateRefusesDomainThatIsNotAnObject(t *testing.T) {
	dir := migProject(t, `{"signal":"oops","signal_enabled":true}`, "")
	if _, err := PlanMigration(dir); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("err = %v", err)
	}
}

func TestMigrateInvalidDotenvValues(t *testing.T) {
	env := "WHATSAPP_LAZY_MEDIA=banana\nGMAIL_IMAP_PORT=abc\nFOO=1\n"
	dir := migProject(t, "", env)
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	c := NewWith(dir, "", nil)
	if !c.WALazyMedia() || c.GmailIMAPPort() != 993 {
		t.Fatal("invalid values must not be stored")
	}
	if len(m.InvalidEnv) != 2 {
		t.Fatalf("invalid %+v", m.InvalidEnv)
	}
	if !reflect.DeepEqual(m.UnknownEnv, []string{"FOO"}) {
		t.Fatalf("unknown %v", m.UnknownEnv)
	}
	if fileExists(filepath.Join(dir, ".env")) {
		t.Fatal("invalid keys are reported, so .env is renamed")
	}
}

func TestMigrateMaskedJSON(t *testing.T) {
	dir := migProject(t, `{
	  "openai_api_key": "sk-secret",
	  "openrouter_api_key": "or-secret",
	  "web": {"token": "web-secret"},
	  "telegram": {"api_id": 1, "api_hash": "hash-secret"},
	  "gmail": {"address": "a@b", "app_password": "pw-secret"},
	  "user_number": "+391"
	}`, "")
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	masked, err := m.MaskedJSON()
	if err != nil {
		t.Fatal(err)
	}
	out := string(masked)
	for _, secret := range []string{"sk-secret", "or-secret", "web-secret", "hash-secret", "pw-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q leaked:\n%s", secret, out)
		}
	}
	if strings.Count(out, MaskedSecret) != 5 {
		t.Fatalf("expected 5 masked values:\n%s", out)
	}
	if !strings.Contains(out, `"+391"`) && !strings.Contains(out, `+391`) {
		t.Fatalf("non-secret values must stay visible:\n%s", out)
	}
	// The plan itself (and the file on disk) still has the real values.
	if _, err := PlanMigration(dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFileString(t, filepath.Join(dir, "config.json")), "sk-secret") {
		t.Fatal("masking must not touch the file")
	}
}

func TestMigrateDotenvTargetCollision(t *testing.T) {
	dir := migProject(t, "", "SIGNAL_ENABLED=1\n")
	if err := os.WriteFile(filepath.Join(dir, ".env.migrated"), []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := PlanMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, filepath.Join(dir, ".env.migrated")); got != "older" {
		t.Fatalf("older .env.migrated overwritten: %q", got)
	}
	if got := readFileString(t, filepath.Join(dir, ".env.migrated.2")); got != "SIGNAL_ENABLED=1\n" {
		t.Fatalf("second target %q", got)
	}
}

func TestMigrateNoProjectDir(t *testing.T) {
	if _, err := PlanMigration("  "); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := PlanMigration(filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing directory: %v", err)
	}
}
