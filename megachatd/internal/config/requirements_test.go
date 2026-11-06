package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const disableHint = "Fill in the missing settings in config.json (or set their environment overrides)"

func TestValidateIntegrationsTelegram(t *testing.T) {
	complete := `{"telegram":{"enabled":true,"api_id":123,"api_hash":"hash"}}`
	if err := newFlagCfg(t, nil, complete, "").ValidateIntegrations(); err != nil {
		t.Fatalf("complete Telegram must pass: %v", err)
	}
	envComplete := map[string]string{
		"TELEGRAM_ENABLED": "1", "TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": "hash",
	}
	if err := newFlagCfg(t, envComplete, "", "").ValidateIntegrations(); err != nil {
		t.Fatalf("complete Telegram from env must pass: %v", err)
	}

	cases := []struct {
		name, configJSON string
		want             []string
	}{
		{"missing id", `{"telegram":{"enabled":true,"api_hash":"hash"}}`,
			[]string{"telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID)"}},
		{"missing hash", `{"telegram":{"enabled":true,"api_id":123}}`,
			[]string{"telegram is enabled but missing: telegram.api_hash (TELEGRAM_API_HASH)"}},
		{"zero id", `{"telegram":{"enabled":true,"api_id":0,"api_hash":"hash"}}`,
			[]string{"telegram.api_id (TELEGRAM_API_ID)"}},
		{"missing both", `{"telegram":{"enabled":true}}`,
			[]string{"telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID), telegram.api_hash (TELEGRAM_API_HASH)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newFlagCfg(t, nil, tc.configJSON, "").ValidateIntegrations()
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), disableHint) || !strings.Contains(err.Error(), "telegram.enabled=false") {
				t.Fatalf("error does not say how to proceed: %q", err)
			}
		})
	}
}

func TestValidateIntegrationsGmail(t *testing.T) {
	complete := `{"gmail":{"enabled":true,"address":"me@example.com","app_password":"secret"}}`
	if err := newFlagCfg(t, nil, complete, "").ValidateIntegrations(); err != nil {
		t.Fatalf("complete Gmail must pass: %v", err)
	}

	// app_password_file: the file must exist and be non-empty.
	cfg := newFlagCfg(t, nil, `{"gmail":{"enabled":true,"address":"me@example.com","app_password_file":"pw.txt"}}`, "")
	name := filepath.Join(cfg.ProjectDir, "pw.txt")
	if err := cfg.ValidateIntegrations(); err == nil || !strings.Contains(err.Error(), "one of gmail.app_password") {
		t.Fatalf("missing password file must be reported: %v", err)
	}
	if err := os.WriteFile(name, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateIntegrations(); err == nil || !strings.Contains(err.Error(), "one of gmail.app_password") {
		t.Fatalf("empty password file must be reported: %v", err)
	}
	if err := os.WriteFile(name, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateIntegrations(); err != nil {
		t.Fatalf("non-empty password file must pass: %v", err)
	}

	// app_password_cmd resolves at validation time like at startup.
	cmd := newFlagCfg(t, nil, `{"gmail":{"enabled":true,"address":"me@example.com","app_password_cmd":"printf secret"}}`, "")
	if err := cmd.ValidateIntegrations(); err != nil {
		t.Fatalf("working password command must pass: %v", err)
	}
	failing := newFlagCfg(t, nil, `{"gmail":{"enabled":true,"address":"me@example.com","app_password_cmd":"false"}}`, "")
	if err := failing.ValidateIntegrations(); err == nil || !strings.Contains(err.Error(), "one of gmail.app_password") {
		t.Fatalf("failing password command must be reported: %v", err)
	}

	// Each missing field separately, then both together.
	cases := []struct {
		name, configJSON string
		want             []string
	}{
		{"missing address", `{"gmail":{"enabled":true,"app_password":"secret"}}`,
			[]string{"gmail is enabled but missing: gmail.address (GMAIL_ADDRESS)"}},
		{"missing password", `{"gmail":{"enabled":true,"address":"me@example.com"}}`,
			[]string{"gmail is enabled but missing: one of gmail.app_password / gmail.app_password_file / gmail.app_password_cmd"}},
		{"missing both", `{"gmail":{"enabled":true}}`,
			[]string{"gmail is enabled but missing: gmail.address (GMAIL_ADDRESS), one of gmail.app_password / gmail.app_password_file / gmail.app_password_cmd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newFlagCfg(t, nil, tc.configJSON, "").ValidateIntegrations()
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), "gmail.enabled=false") {
				t.Fatalf("error does not say how to proceed: %q", err)
			}
		})
	}
}

func TestValidateIntegrationsReportsEveryProblemAtOnce(t *testing.T) {
	cfg := newFlagCfg(t, nil,
		`{"telegram":{"enabled":true},"gmail":{"enabled":true}}`, "")
	err := cfg.ValidateIntegrations()
	if err == nil {
		t.Fatal("expected an error")
	}
	text := err.Error()
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("want two problem lines and the hint, got %q", text)
	}
	if !strings.HasPrefix(lines[0], "telegram is enabled but missing: ") {
		t.Fatalf("first line is not Telegram: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "gmail is enabled but missing: ") {
		t.Fatalf("second line is not Gmail: %q", lines[1])
	}
	if !strings.Contains(lines[2], "telegram.enabled=false / gmail.enabled=false") ||
		!strings.Contains(lines[2], "disable them") {
		t.Fatalf("hint does not name both flags: %q", lines[2])
	}
}

func TestValidateIntegrationsSkipsDisabled(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		json string
	}{
		{"absent flags", nil, `{"telegram":{"api_id":0},"gmail":{"address":""}}`},
		{"explicit off", map[string]string{"TELEGRAM_ENABLED": "0", "GMAIL_ENABLED": "off"},
			`{"telegram":{"enabled":true},"gmail":{"enabled":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := newFlagCfg(t, tc.env, tc.json, "").ValidateIntegrations(); err != nil {
				t.Fatalf("disabled integrations must not be validated: %v", err)
			}
		})
	}
}

func TestValidateIntegrationsWhatsAppAndSignalHaveNoRequirements(t *testing.T) {
	cfg := newFlagCfg(t, map[string]string{"SIGNAL_ENABLED": "1", "WHATSAPP_ENABLED": "1"}, "", "")
	if err := cfg.ValidateIntegrations(); err != nil {
		t.Fatalf("Signal and WhatsApp must start without settings: %v", err)
	}
}

func TestIntegrationRequirementsMatchSchema(t *testing.T) {
	for _, req := range integrationRequirements {
		if _, ok := SettingByPath(req.domain, "enabled"); !ok {
			t.Errorf("%s has no enabled setting in schema.go", req.domain)
		}
	}
}
