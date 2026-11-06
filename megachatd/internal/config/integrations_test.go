package config

import (
	"os"
	"path/filepath"
	"testing"
)

// newFlagCfg builds a Config over a temp project dir with optional config.json
// and .env contents.
func newFlagCfg(t *testing.T, env map[string]string, configJSON, dotenv string) *Config {
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
	return NewWith(dir, t.TempDir(), env)
}

func TestIntegrationFlagsDefaultOff(t *testing.T) {
	c := newFlagCfg(t, nil, "", "")
	if c.SignalEnabled() || c.WhatsAppEnabled() || c.TelegramEnabled() || c.GmailEnabled() {
		t.Fatal("every integration must be off without an explicit flag")
	}
	if c.SignalPreviouslyConfigured() || c.WhatsAppPreviouslyConfigured() ||
		c.TelegramPreviouslyConfigured() || c.GmailPreviouslyConfigured() {
		t.Fatal("nothing is previously configured in an empty project")
	}
}

func TestIntegrationFlagPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		domain  string
		setting string
		enabled func(*Config) bool
	}{
		{"signal", "signal", "enabled", (*Config).SignalEnabled},
		{"whatsapp", "whatsapp", "enabled", (*Config).WhatsAppEnabled},
		{"telegram", "telegram", "enabled", (*Config).TelegramEnabled},
		// GmailEnabled also requires credentials (tested separately), so the
		// precedence table probes the flag itself.
		{"gmail", "gmail", "enabled", func(c *Config) bool {
			return c.ExplicitFlag("gmail", "enabled")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := envName(tc.domain, tc.setting)
			jsonKey := `{"` + tc.domain + `":{"` + tc.setting + `":`
			// Each source on its own.
			if !tc.enabled(newFlagCfg(t, map[string]string{ev: "1"}, "", "")) {
				t.Fatal("env must enable")
			}
			if !tc.enabled(newFlagCfg(t, map[string]string{ev: "on"}, "", "")) {
				t.Fatal("env on must enable")
			}
			if !tc.enabled(newFlagCfg(t, nil, jsonKey+`true}}`, "")) {
				t.Fatal("config.json bool must enable")
			}
			if !tc.enabled(newFlagCfg(t, nil, jsonKey+`"yes"}}`, "")) {
				t.Fatal("config.json string must enable")
			}
			// Off spellings.
			if tc.enabled(newFlagCfg(t, map[string]string{ev: "0"}, "", "")) {
				t.Fatal("env 0 must disable")
			}
			if tc.enabled(newFlagCfg(t, nil, jsonKey+`false}}`, "")) {
				t.Fatal("config.json false must disable")
			}
			// env > config.json.
			if tc.enabled(newFlagCfg(t, map[string]string{ev: "0"}, jsonKey+`true}}`, "")) {
				t.Fatal("environment must win")
			}
			// Blank and unparseable values fall through.
			if !tc.enabled(newFlagCfg(t, map[string]string{ev: "  "}, jsonKey+`true}}`, "")) {
				t.Fatal("blank env must fall through to config.json")
			}
			if !tc.enabled(newFlagCfg(t, map[string]string{ev: "banana"}, jsonKey+`true}}`, "")) {
				t.Fatal("unparseable env must fall through to config.json")
			}
			if tc.enabled(newFlagCfg(t, nil, jsonKey+`""}}`, "")) {
				t.Fatal("blank config.json value must be unconfigured")
			}
			// The project .env never enables anything.
			if tc.enabled(newFlagCfg(t, nil, "", ev+"=1\n")) {
				t.Fatal(".env must not enable " + tc.name)
			}
		})
	}
}

func TestSignalExplicitFlagBeatsUserNumber(t *testing.T) {
	c := newFlagCfg(t, nil, `{"signal":{"user_number":"+391"}}`, "")
	if c.SignalEnabled() || !c.SignalPreviouslyConfigured() || c.SignalUserNumber() != "+391" {
		t.Fatal("a user number is evidence, not an enable")
	}
	// The legacy flat key is ignored and reported.
	legacy := newFlagCfg(t, nil, `{"user_number":"+391"}`, "")
	if legacy.SignalEnabled() || legacy.SignalPreviouslyConfigured() || legacy.SignalUserNumber() != "" {
		t.Fatal("the legacy user_number key must be ignored")
	}
	if len(legacy.LegacyConfigEntries()) != 1 {
		t.Fatalf("%+v", legacy.LegacyConfigEntries())
	}
	if !newFlagCfg(t, map[string]string{"SIGNAL_ENABLED": "1"}, `{"signal":{"user_number":"+391"}}`, "").SignalEnabled() {
		t.Fatal("SIGNAL_ENABLED=1 must enable Signal")
	}
	if !newFlagCfg(t, map[string]string{"SIGNAL_ENABLED": "yes"}, `{"signal":{"user_number":"+391"}}`, "").SignalEnabled() {
		t.Fatal("SIGNAL_ENABLED=yes must enable Signal")
	}
	if newFlagCfg(t, map[string]string{"SIGNAL_ENABLED": "0"}, `{"signal":{"user_number":"+391"}}`, "").SignalEnabled() {
		t.Fatal("explicit disable must beat the user number")
	}
}

func TestWhatsAppDeviceStoreIsEvidenceNotEnable(t *testing.T) {
	c := newFlagCfg(t, nil, "", "")
	if err := os.MkdirAll(c.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.DataDir(), "whatsmeow.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if c.WhatsAppEnabled() || !c.WhatsAppPreviouslyConfigured() {
		t.Fatal("an existing whatsmeow.db is evidence, not an enable")
	}
	if !newFlagCfg(t, map[string]string{"WHATSAPP_ENABLED": "1"}, "", "").WhatsAppEnabled() {
		t.Fatal("WHATSAPP_ENABLED=1 must enable WhatsApp")
	}
	if newFlagCfg(t, map[string]string{"WHATSAPP_ENABLED": "0"}, "", "").WhatsAppEnabled() {
		t.Fatal("explicit disable must win")
	}
	// The removed WAHA API URL is no longer evidence of anything.
	legacy := newFlagCfg(t, map[string]string{"WHATSAPP_API_URL": "http://127.0.0.1:3005"}, "", "")
	if legacy.WhatsAppEnabled() || legacy.WhatsAppPreviouslyConfigured() {
		t.Fatal("a removed WHATSAPP_API_URL is neither an enable nor evidence")
	}
	if len(legacy.LegacyEnvEntries()) != 1 || !legacy.LegacyEnvEntries()[0].Setting.Removed() {
		t.Fatalf("%+v", legacy.LegacyEnvEntries())
	}
}

func TestTelegramFlagAndCredentials(t *testing.T) {
	creds := newFlagCfg(t, nil, `{"telegram":{"api_id":123,"api_hash":"abc"}}`, "")
	if creds.TelegramEnabled() || !creds.TelegramCredentials() || !creds.TelegramPreviouslyConfigured() {
		t.Fatal("credentials are evidence, not an enable")
	}
	envCreds := newFlagCfg(t, map[string]string{"TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": "abc"}, "", "")
	if !envCreds.TelegramCredentials() {
		t.Fatal("env credentials")
	}
	if newFlagCfg(t, map[string]string{"TELEGRAM_ENABLED": "0"}, `{"telegram":{"api_id":123,"api_hash":"abc"}}`, "").TelegramEnabled() {
		t.Fatal("explicit disable must beat the credentials")
	}
	// ON without credentials reports not startable.
	flagOnly := newFlagCfg(t, map[string]string{"TELEGRAM_ENABLED": "1"}, "", "")
	if !flagOnly.TelegramEnabled() || flagOnly.TelegramCredentials() {
		t.Fatal("TELEGRAM_ENABLED alone must be on but not startable")
	}
	if newFlagCfg(t, map[string]string{"TELEGRAM_REACTIONS_NOTIFY": "0"}, "", "").TelegramReactionsNotify() {
		t.Fatal("reactions notify from env")
	}
}

func TestGmailFlagAndCredentials(t *testing.T) {
	creds := newFlagCfg(t, map[string]string{"GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD": "pw"}, "", "")
	if creds.GmailEnabled() || !creds.GmailCredentials() || !creds.GmailPreviouslyConfigured() {
		t.Fatal("credentials are evidence, not an enable")
	}
	nested := newFlagCfg(t, nil, `{"gmail":{"address":"a@b","app_password":"pw"}}`, "")
	if nested.GmailEnabled() || !nested.GmailCredentials() {
		t.Fatal("nested credentials are evidence, not an enable")
	}
	// ON without credentials reports not startable.
	flagOnly := newFlagCfg(t, map[string]string{"GMAIL_ENABLED": "1"}, "", "")
	if flagOnly.GmailEnabled() || flagOnly.GmailCredentials() {
		t.Fatal("GMAIL_ENABLED alone must not enable a service without credentials")
	}
	// OFF wins even with credentials.
	off := newFlagCfg(t, map[string]string{"GMAIL_ENABLED": "0",
		"GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD": "pw"}, "", "")
	if off.GmailEnabled() {
		t.Fatal("explicit disable must beat the credentials")
	}
	if !newFlagCfg(t, map[string]string{"GMAIL_ENABLED": "1",
		"GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD": "pw"}, "", "").GmailEnabled() {
		t.Fatal("flag plus credentials must enable Gmail")
	}
}
