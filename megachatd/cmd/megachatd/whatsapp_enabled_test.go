package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

func newWhatsAppCfg(t *testing.T, env map[string]string, cfgJSON, dotenv string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if cfgJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfgJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if dotenv != "" {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config.NewWith(dir, t.TempDir(), env)
}

func TestWhatsAppEnabledFromDotenv(t *testing.T) {
	// The README/install.sh/link_whatsapp.py all say `WHATSAPP_ENABLED=1` in .env.
	if !whatsappEnabled(newWhatsAppCfg(t, nil, "", "WHATSAPP_ENABLED=1\n")) {
		t.Fatal(".env WHATSAPP_ENABLED=1 must enable WhatsApp")
	}
	if whatsappEnabled(newWhatsAppCfg(t, nil, "", "WHATSAPP_ENABLED=0\n")) {
		t.Fatal(".env WHATSAPP_ENABLED=0 must disable WhatsApp")
	}
}

func TestWhatsAppEnabledDotenvPrecedence(t *testing.T) {
	// Environment wins over config.json, which wins over .env.
	if whatsappEnabled(newWhatsAppCfg(t, map[string]string{"WHATSAPP_ENABLED": "0"}, `{"whatsapp_enabled":true}`, "WHATSAPP_ENABLED=1\n")) {
		t.Fatal("environment must win")
	}
	if whatsappEnabled(newWhatsAppCfg(t, nil, `{"whatsapp_enabled":false}`, "WHATSAPP_ENABLED=1\n")) {
		t.Fatal("config.json must win over .env")
	}
	// A blank environment value falls through, like Python's empty-env handling.
	if !whatsappEnabled(newWhatsAppCfg(t, map[string]string{"WHATSAPP_ENABLED": "  "}, "", "WHATSAPP_ENABLED=1\n")) {
		t.Fatal("blank environment value must fall through to .env")
	}
}

func TestWhatsAppEnabledDeviceStoreBeatsAbsentFlag(t *testing.T) {
	cfg := newWhatsAppCfg(t, nil, "", "")
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir(), "whatsmeow.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !whatsappEnabled(cfg) {
		t.Fatal("an existing whatsmeow.db must enable WhatsApp")
	}
	// An explicit disable still wins over the device store.
	if err := os.WriteFile(filepath.Join(cfg.ProjectDir, ".env"), []byte("WHATSAPP_ENABLED=no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if whatsappEnabled(cfg) {
		t.Fatal("explicit disable must win over the device store")
	}
}

func TestWhatsAppEnabledDefaultOff(t *testing.T) {
	if whatsappEnabled(newWhatsAppCfg(t, nil, "", "")) {
		t.Fatal("WhatsApp must be off without configuration")
	}
}
