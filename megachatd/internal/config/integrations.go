package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Integration enable flags (mc-7zm). Every backend (Signal, WhatsApp,
// Telegram, Gmail) is OFF unless its explicit flag says otherwise:
//
//	Integration | env / .env        | config.json key
//	------------|-------------------|-----------------
//	Signal      | SIGNAL_ENABLED    | signal_enabled
//	WhatsApp    | WHATSAPP_ENABLED  | whatsapp_enabled
//	Telegram    | TELEGRAM_ENABLED  | telegram_enabled
//	Gmail       | GMAIL_ENABLED     | gmail_enabled
//
// Precedence is process environment > config.json (JSON bool or one of the
// lookupBool spellings) > project .env. Blank and unparseable values fall
// through to the next source; with no usable value anywhere the integration is
// disabled. In particular credentials never enable a backend: they only make
// it startable once the flag is on (Telegram/Gmail) and are otherwise evidence
// of earlier use for the migration notice (mc-7zm.2).

// lookupBool parses the boolean spellings shared by the enable flags:
// 1/true/yes/on and 0/false/no/off, case-insensitive around whitespace.
func lookupBool(s string) (value, set bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// flagValue resolves an explicit enable flag. It returns the first usable
// source (env, then config.json, then .env); set is false when no source has a
// parseable value, so callers can tell "off" from "not configured".
func (c *Config) flagValue(key, envName string) (value, set bool) {
	if raw, ok := c.envSet(envName); ok {
		if v, ok := lookupBool(raw); ok {
			return v, true
		}
	}
	if raw, ok := c.LoadFile()[key]; ok {
		switch t := raw.(type) {
		case bool:
			return t, true
		case string:
			if v, ok := lookupBool(t); ok {
				return v, true
			}
		}
	}
	if raw, ok := c.LoadDotenv()[envName]; ok {
		if v, ok := lookupBool(raw); ok {
			return v, true
		}
	}
	return false, false
}

// ExplicitFlag reports an enable flag with the integration precedence and
// defaults to false; it never consults credentials.
func (c *Config) ExplicitFlag(key, envName string) bool {
	v, _ := c.flagValue(key, envName)
	return v
}

// SignalEnabled is the explicit SIGNAL_ENABLED flag; a configured user number
// alone no longer enables Signal.
func (c *Config) SignalEnabled() bool {
	return c.ExplicitFlag("signal_enabled", "SIGNAL_ENABLED")
}

// SignalPreviouslyConfigured reports evidence of an earlier Signal setup (a
// user number), for the migration notice.
func (c *Config) SignalPreviouslyConfigured() bool {
	return strings.TrimSpace(c.SignalUserNumber()) != ""
}

// WhatsAppEnabled is the explicit WHATSAPP_ENABLED flag; the whatsmeow device
// store and a legacy WHATSAPP_API_URL are evidence of earlier use, not enables.
func (c *Config) WhatsAppEnabled() bool {
	return c.ExplicitFlag("whatsapp_enabled", "WHATSAPP_ENABLED")
}

// WhatsAppPreviouslyConfigured reports evidence of an earlier WhatsApp setup:
// a paired device store (<DataDir>/whatsmeow.db) or a legacy WAHA API URL.
func (c *Config) WhatsAppPreviouslyConfigured() bool {
	if _, err := os.Stat(filepath.Join(c.DataDir(), "whatsmeow.db")); err == nil {
		return true
	}
	return c.WhatsAppAPIURL() != ""
}

// TelegramEnabled is the explicit TELEGRAM_ENABLED flag. It does not imply the
// credentials are present; see TelegramCredentials.
func (c *Config) TelegramEnabled() bool {
	return c.ExplicitFlag("telegram_enabled", "TELEGRAM_ENABLED")
}

// TelegramCredentials reports whether the Telegram API id and hash needed to
// start the backend are configured.
func (c *Config) TelegramCredentials() bool {
	return c.TelegramAPIID() != 0 && c.TelegramAPIHash() != ""
}

// TelegramPreviouslyConfigured reports evidence of an earlier Telegram setup
// (the API credentials), for the migration notice.
func (c *Config) TelegramPreviouslyConfigured() bool { return c.TelegramCredentials() }

// GmailCredentials reports whether an address and an App Password are
// configured.
func (c *Config) GmailCredentials() bool {
	return c.GmailAddress() != "" && c.GmailAppPassword() != ""
}

// GmailPreviouslyConfigured reports evidence of an earlier Gmail setup (the
// address and App Password), for the migration notice.
func (c *Config) GmailPreviouslyConfigured() bool { return c.GmailCredentials() }

// GmailFlagEnabled is the explicit GMAIL_ENABLED flag alone, without the
// credential requirement; wiring uses it to tell "off" from "on but not
// startable".
func (c *Config) GmailFlagEnabled() bool {
	return c.ExplicitFlag("gmail_enabled", "GMAIL_ENABLED")
}

// GmailEnabled is ON only with the explicit GMAIL_ENABLED flag AND the
// credentials to start the service.
func (c *Config) GmailEnabled() bool {
	return c.GmailFlagEnabled() && c.GmailCredentials()
}
