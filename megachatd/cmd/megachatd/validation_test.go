package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

func TestBuildRejectsIncompleteEnabledIntegrations(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config.json"),
		[]byte(`{"gmail":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewWith(project, filepath.Join(root, "home"), map[string]string{
		"TELEGRAM_ENABLED": "1",
	})
	app, err := Build(BuildOptions{Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil {
		app.Close()
		t.Fatal("Build must reject enabled integrations without their settings")
	}
	for _, want := range []string{
		"telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID), telegram.api_hash (TELEGRAM_API_HASH)",
		"gmail is enabled but missing: gmail.address (GMAIL_ADDRESS), one of gmail.app_password / gmail.app_password_file / gmail.app_password_cmd",
		"telegram.enabled=false / gmail.enabled=false",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	// Validation runs before the store is opened.
	if _, statErr := os.Stat(cfg.DBFile()); !os.IsNotExist(statErr) {
		t.Fatalf("store was opened before validation: %v", statErr)
	}
}

func TestServeExitsWithoutListeningOnInvalidIntegration(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config.json"),
		[]byte(`{"telegram":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "server.sock")
	lock := filepath.Join(root, "lock")
	t.Setenv("MEGACHAT_PROJECT_DIR", "")
	t.Setenv("MEGACHAT_SOCKET", socket)
	t.Setenv("MEGACHAT_LOCK_FILE", lock)
	t.Setenv("MEGACHAT_LOG_FILE", filepath.Join(root, "server.log"))
	for _, name := range []string{"SIGNAL_ENABLED", "WHATSAPP_ENABLED", "GMAIL_ENABLED"} {
		t.Setenv(name, "0")
	}
	t.Setenv("TELEGRAM_ENABLED", "1")
	t.Setenv("TELEGRAM_API_ID", "")
	t.Setenv("TELEGRAM_API_HASH", "")

	var stdout, stderr bytes.Buffer
	code := serve([]string{"serve", "--project-dir", project, "--web-port", strconv.Itoa(freePort(t))}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("serve exit code = %d, want 1", code)
	}
	want := "telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID), telegram.api_hash (TELEGRAM_API_HASH)"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr lacks the startup error:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "telegram.enabled=false") {
		t.Fatalf("stderr does not say how to proceed:\n%s", stderr.String())
	}
	// No adapter ever listened and the instance lock was released.
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket was created despite the startup error: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock was not released: %v", err)
	}
}
