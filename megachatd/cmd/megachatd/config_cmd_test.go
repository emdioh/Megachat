package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrateRun(t *testing.T, dir string, extra ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"config", "migrate", "--project-dir", dir}, extra...)
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func writeProject(t *testing.T, configJSON, dotenv string) string {
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

const legacyConfig = `{
  "user_number": "+391",
  "openai_api_key": "sk-secret",
  "web": {"token": "web-secret"},
  "ai_provider": "openai"
}`

func TestConfigMigrateDryRun(t *testing.T) {
	dir := writeProject(t, legacyConfig, "SIGNAL_ENABLED=1\nWAHA_API_KEY=waha-secret\n")
	configPath := filepath.Join(dir, "config.json")
	envPath := filepath.Join(dir, ".env")
	before := readTestFile(t, configPath)
	envBefore := readTestFile(t, envPath)

	code, out, errOut := migrateRun(t, dir, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	for _, secret := range []string{"sk-secret", "web-secret", "waha-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q leaked in the dry-run dump:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("secrets must be masked:\n%s", out)
	}
	if !strings.Contains(errOut, "would write") || !strings.Contains(errOut, "would rename .env -> .env.migrated") {
		t.Fatalf("dry-run report:\n%s", errOut)
	}
	if got := readTestFile(t, configPath); got != before {
		t.Fatal("dry run modified config.json")
	}
	if got := readTestFile(t, envPath); got != envBefore {
		t.Fatal("dry run modified .env")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.pre-nested.bak")); err == nil {
		t.Fatal("dry run created the backup")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.migrated")); err == nil {
		t.Fatal("dry run renamed .env")
	}
}

func TestConfigMigrateWritesAndIsIdempotent(t *testing.T) {
	dir := writeProject(t, legacyConfig, "SIGNAL_ENABLED=1\nWAHA_API_KEY=waha-secret\n")
	code, out, errOut := migrateRun(t, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"move config.json key user_number -> signal.user_number",
		"move .env key SIGNAL_ENABLED -> signal.enabled",
		"drop .env key WAHA_API_KEY (setting removed)",
		"wrote", "created", "renamed .env -> .env.migrated"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	st, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.migrated")); err != nil {
		t.Fatal(err)
	}

	before := readTestFile(t, filepath.Join(dir, "config.json"))
	backup := readTestFile(t, filepath.Join(dir, "config.json.pre-nested.bak"))
	code, out, errOut = migrateRun(t, dir)
	if code != 0 {
		t.Fatalf("second run exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "no changes to config.json") || !strings.Contains(out, "no .env found") {
		t.Fatalf("second run:\n%s", out)
	}
	if got := readTestFile(t, filepath.Join(dir, "config.json")); got != before {
		t.Fatal("second run rewrote config.json")
	}
	if got := readTestFile(t, filepath.Join(dir, "config.json.pre-nested.bak")); got != backup {
		t.Fatal("second run rewrote the backup")
	}
}

func TestConfigHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"config", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "migrate") {
		t.Fatalf("config help:\n%s", out.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"config", "migrate", "-h"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "--dry-run") || !strings.Contains(errb.String(), "--project-dir") {
		t.Fatalf("migrate help:\n%s%s", out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "config migrate") {
		t.Fatalf("root help:\n%s", out.String())
	}
}

func TestConfigErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"config"}, &out, &errb); code != 2 {
		t.Fatalf("bare config exit %d", code)
	}
	if code := run([]string{"config", "bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command exit %d", code)
	}
	dir := writeProject(t, legacyConfig, "")
	if code, _, _ := migrateRun(t, dir, "extra"); code != 2 {
		t.Fatalf("unexpected argument exit %d", code)
	}
	broken := writeProject(t, "{bad", "")
	code, _, errOut := migrateRun(t, broken)
	if code != 1 || !strings.Contains(errOut, "not a JSON object") {
		t.Fatalf("broken config exit %d: %s", code, errOut)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
