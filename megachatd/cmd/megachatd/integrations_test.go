package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

// buildIntegrationApp builds the full (never started) app over a temp HOME and
// project, collecting the log lines.
func buildIntegrationApp(t *testing.T, env map[string]string, configJSON string) (*App, string) {
	t.Helper()
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, d := range []string{home, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if configJSON != "" {
		if err := os.WriteFile(filepath.Join(project, "config.json"), []byte(configJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	cfg := config.NewWith(project, home, env)
	app, err := Build(BuildOptions{
		Config: cfg, Log: slog.New(slog.NewTextHandler(&logs, nil)), DownloadPort: freePort(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app, logs.String()
}

func TestDefaultOffRegistersNoBackend(t *testing.T) {
	app, logs := buildIntegrationApp(t, nil, "")
	if app.Signal != nil || app.WhatsApp != nil || app.Telegram != nil {
		t.Fatal("a fresh install must register no chat backend")
	}
	if len(app.Notices) != 0 {
		t.Fatalf("no migration notice on a fresh install: %v", app.Notices)
	}
	if app.Core.Gmail() != nil {
		t.Fatal("the Gmail unread watcher must not run when Gmail is off")
	}
	if configured, _, _ := app.Gmail.StatusSnapshot(); configured {
		t.Fatal("Gmail must report not configured")
	}
	for _, flag := range []string{"SIGNAL_ENABLED=1", "WHATSAPP_ENABLED=1", "TELEGRAM_ENABLED=1", "GMAIL_ENABLED=1"} {
		if !strings.Contains(logs, flag) {
			t.Fatalf("the disabled line must name %s:\n%s", flag, logs)
		}
	}
}

func TestExplicitFlagStartsOnlyItsIntegration(t *testing.T) {
	t.Run("signal", func(t *testing.T) {
		app, _ := buildIntegrationApp(t, map[string]string{"SIGNAL_ENABLED": "1"}, "")
		if app.Signal == nil || app.WhatsApp != nil || app.Telegram != nil {
			t.Fatal("only Signal must be registered")
		}
	})
	t.Run("whatsapp", func(t *testing.T) {
		app, _ := buildIntegrationApp(t, map[string]string{"WHATSAPP_ENABLED": "1"}, "")
		if app.WhatsApp == nil || app.Signal != nil || app.Telegram != nil {
			t.Fatal("only WhatsApp must be registered")
		}
	})
	t.Run("telegram", func(t *testing.T) {
		app, _ := buildIntegrationApp(t, map[string]string{
			"TELEGRAM_ENABLED": "1", "TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": "abc"}, "")
		if app.Telegram == nil || app.Signal != nil || app.WhatsApp != nil {
			t.Fatal("only Telegram must be registered")
		}
	})
	t.Run("gmail", func(t *testing.T) {
		app, _ := buildIntegrationApp(t, map[string]string{
			"GMAIL_ENABLED": "1", "GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD": "pw"}, "")
		if app.Signal != nil || app.WhatsApp != nil || app.Telegram != nil {
			t.Fatal("Gmail must not register a chat backend")
		}
		if app.Core.Gmail() == nil {
			t.Fatal("Gmail ON with credentials must start the unread watcher")
		}
		if configured, addr, _ := app.Gmail.StatusSnapshot(); !configured || addr != "a@b" {
			t.Fatalf("configured=%v addr=%q", configured, addr)
		}
	})
}

func TestEnabledWithoutCredentialsDoesNotStart(t *testing.T) {
	t.Run("telegram", func(t *testing.T) {
		app, logs := buildIntegrationApp(t, map[string]string{"TELEGRAM_ENABLED": "1"}, "")
		if app.Telegram != nil {
			t.Fatal("Telegram without api id/hash must not start")
		}
		if !strings.Contains(logs, "TELEGRAM_API_ID") {
			t.Fatalf("the warning must name the missing settings:\n%s", logs)
		}
	})
	t.Run("gmail", func(t *testing.T) {
		app, logs := buildIntegrationApp(t, map[string]string{"GMAIL_ENABLED": "1"}, "")
		if app.Core.Gmail() != nil {
			t.Fatal("Gmail without credentials must not run the watcher")
		}
		if configured, _, _ := app.Gmail.StatusSnapshot(); configured {
			t.Fatal("Gmail without credentials must not be configured")
		}
		if !strings.Contains(logs, "GMAIL_ADDRESS") {
			t.Fatalf("the warning must name the missing settings:\n%s", logs)
		}
	})
}

func TestOffWithEvidenceAddsOneNoticePerIntegration(t *testing.T) {
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for _, d := range []string{home, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Evidence of earlier use for all four, with every flag explicitly off.
	cfg := config.NewWith(project, home, map[string]string{
		"SIGNAL_ENABLED": "0", "WHATSAPP_ENABLED": "0", "TELEGRAM_ENABLED": "0", "GMAIL_ENABLED": "0",
		"TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": "abc",
		"GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD": "pw",
	})
	if err := os.WriteFile(cfg.ConfigFile(), []byte(`{"user_number":"+391"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir(), "whatsmeow.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	app, err := Build(BuildOptions{Config: cfg, Log: slog.New(slog.NewTextHandler(&logs, nil)), DownloadPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	want := []string{SignalDisabledNotice, WhatsAppDisabledNotice, TelegramDisabledNotice, GmailDisabledNotice}
	if len(app.Notices) != len(want) {
		t.Fatalf("notices = %v", app.Notices)
	}
	for i, notice := range want {
		if app.Notices[i] != notice {
			t.Fatalf("notice %d = %q, want %q", i, app.Notices[i], notice)
		}
		if !strings.Contains(logs.String(), notice) {
			t.Fatalf("notice %d was not logged as a warning", i)
		}
	}
	if app.Signal != nil || app.WhatsApp != nil || app.Telegram != nil || app.Core.Gmail() != nil {
		t.Fatal("an off integration with evidence must still not start")
	}
}
