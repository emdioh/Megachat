package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/config"
)

func TestWhatsAppRelinkNoticeOnce(t *testing.T) {
	root := t.TempDir()
	cfg := config.NewWith(filepath.Join(root, "p"), filepath.Join(root, "home"), nil)
	if !whatsappRelinkNeeded(cfg) {
		t.Fatal("first start without whatsmeow.db must show the notice")
	}
	if whatsappRelinkNeeded(cfg) {
		t.Fatal("the notice must be shown only once")
	}
}

func TestWhatsAppRelinkNoticeSkippedWithDevice(t *testing.T) {
	root := t.TempDir()
	cfg := config.NewWith(filepath.Join(root, "p"), filepath.Join(root, "home"), nil)
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir(), "whatsmeow.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if whatsappRelinkNeeded(cfg) {
		t.Fatal("an already paired device store needs no notice")
	}
}
