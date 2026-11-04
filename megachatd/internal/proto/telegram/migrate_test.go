package telegram

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

func migCfg(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.NewWith(dir, dir, nil)
}

func chatsOf(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()
	m, _ := cfg.LoadFile()["auto_transcribe_chats"].(map[string]any)
	return m
}

const fixtureConfig = `{
  "user_number": "+39000",
  "auto_transcribe_chats": {
    "telegram:123": true,
    "telegram:456": false,
    "telegram:789": true,
    "telegram:555": true,
    "telegram:-77": true,
    "telegram:99": false,
    "telegram:42": true,
    "signal:123": true,
    "whatsapp:123": false
  }
}`

// 123 = legacy group (-123 known), 456 = channel (-100456 known), 789 is a
// user that also exists as a group (ambiguous), 555 is unknown, -77 is already
// marked, 99 is a legacy group, 42 is a plain user.
func fixtureKnown() map[int64]bool {
	return map[int64]bool{
		-123: true, -1000000000456: true,
		789: true, -789: true,
		-99: true, 42: true,
	}
}

func TestMigrateConfigRewritesGroupsAndChannels(t *testing.T) {
	cfg := migCfg(t, fixtureConfig)
	res, err := MigrateConfigToMarkedIDs(cfg, fixtureKnown())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"telegram:123": "telegram:-123",
		"telegram:456": "telegram:-1000000000456",
		"telegram:99":  "telegram:-99",
	}
	if !reflect.DeepEqual(res.Rewritten, want) {
		t.Fatalf("rewritten %v", res.Rewritten)
	}
	got := chatsOf(t, cfg)
	expect := map[string]any{
		"telegram:-123": true, "telegram:-1000000000456": false,
		"telegram:789": true, "telegram:555": true, "telegram:-77": true,
		"telegram:-99": false, "telegram:42": true,
		"signal:123": true, "whatsapp:123": false,
	}
	if !reflect.DeepEqual(got, expect) {
		t.Fatalf("chats %v", got)
	}
	if cfg.LoadFile()["user_number"] != "+39000" {
		t.Fatal("other keys must survive")
	}
	sort.Strings(res.Ambiguous)
	if !reflect.DeepEqual(res.Ambiguous, []string{"telegram:42", "telegram:555", "telegram:789"}) {
		t.Fatalf("ambiguous: %v", res.Ambiguous)
	}
}

func TestMigrateConfigBackupAndIdempotent(t *testing.T) {
	cfg := migCfg(t, fixtureConfig)
	if _, err := MigrateConfigToMarkedIDs(cfg, fixtureKnown()); err != nil {
		t.Fatal(err)
	}
	backup := cfg.ConfigFile() + MarkedIDsBackupSuffix
	raw, err := os.ReadFile(backup)
	if err != nil || string(raw) != fixtureConfig {
		t.Fatalf("backup missing or altered: %v %q", err, raw)
	}
	before, _ := os.ReadFile(cfg.ConfigFile())

	res, err := MigrateConfigToMarkedIDs(cfg, fixtureKnown())
	if err != nil || len(res.Rewritten)+len(res.Dropped) != 0 || res.BackupPath != "" {
		t.Fatalf("second run must be a no-op: %+v %v", res, err)
	}
	after, _ := os.ReadFile(cfg.ConfigFile())
	if string(before) != string(after) {
		t.Fatal("second run rewrote config.json")
	}
	if b2, _ := os.ReadFile(backup); string(b2) != fixtureConfig {
		t.Fatal("backup overwritten")
	}
}

func TestMigrateConfigMarkedKeyWins(t *testing.T) {
	cfg := migCfg(t, `{"auto_transcribe_chats":{"telegram:99":true,"telegram:-99":false}}`)
	res, err := MigrateConfigToMarkedIDs(cfg, map[int64]bool{-99: true})
	if err != nil || len(res.Dropped) != 1 || res.Dropped[0] != "telegram:99" {
		t.Fatalf("%+v %v", res, err)
	}
	if !reflect.DeepEqual(chatsOf(t, cfg), map[string]any{"telegram:-99": false}) {
		t.Fatalf("%v", chatsOf(t, cfg))
	}
}

func TestMigrateConfigNoChangeNoBackupNoWrite(t *testing.T) {
	for _, body := range []string{`{}`, `{"auto_transcribe_chats":{"telegram:42":true,"telegram:-5":true}}`, `not json`} {
		cfg := migCfg(t, body)
		res, err := MigrateConfigToMarkedIDs(cfg, map[int64]bool{42: true})
		if err != nil || res.BackupPath != "" {
			t.Fatalf("%q: %+v %v", body, res, err)
		}
		if _, err := os.Stat(cfg.ConfigFile() + MarkedIDsBackupSuffix); err == nil {
			t.Fatalf("%q: unexpected backup", body)
		}
		if raw, _ := os.ReadFile(cfg.ConfigFile()); string(raw) != body {
			t.Fatalf("%q rewritten as %q", body, raw)
		}
	}
}

func TestMarkedPeerCandidates(t *testing.T) {
	g, c := MarkedPeerCandidates(1234567890)
	if g != -1234567890 || c != -1001234567890 {
		t.Fatal(g, c)
	}
}

func TestKnownMarkedIDsReadsSessionAndStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.session")
	makeTelethonDB(t, path)
	known := KnownMarkedIDs(bg, path, nil)
	if !known[42] || !known[-1001234] {
		t.Fatalf("%v", known)
	}
	if len(KnownMarkedIDs(bg, filepath.Join(dir, "none"), nil)) != 0 {
		t.Fatal("missing session must yield no ids")
	}
}
