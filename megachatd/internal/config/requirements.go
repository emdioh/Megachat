package config

import (
	"fmt"
	"strings"
)

// An enabled integration that lacks a required setting cannot start. This
// table is the startup contract and sits next to schema.go because every path
// and environment override comes from that table.
//
// WhatsApp and Signal have no required settings: WhatsApp is paired through
// link.* (QR) and Signal may start unlinked (link.finalize then writes
// signal.user_number). Telegram and Gmail need the settings below before the
// server opens the store or registers anything.
type integrationRequirement struct {
	domain string
	// missing returns the references of the settings that are absent or
	// unusable, in report order. Empty means the integration can start.
	missing func(*Config) []string
}

var integrationRequirements = []integrationRequirement{
	{domain: "signal"},   // linked through link.* while the server runs
	{domain: "whatsapp"}, // paired through link.* (QR)
	{domain: "telegram", missing: func(c *Config) []string {
		var out []string
		if c.TelegramAPIID() == 0 {
			out = append(out, settingRef("telegram", "api_id"))
		}
		if c.TelegramAPIHash() == "" {
			out = append(out, settingRef("telegram", "api_hash"))
		}
		return out
	}},
	{domain: "gmail", missing: func(c *Config) []string {
		var out []string
		if c.GmailAddress() == "" {
			out = append(out, settingRef("gmail", "address"))
		}
		if c.GmailAppPassword() == "" {
			out = append(out, oneOf("gmail", "app_password", "app_password_file", "app_password_cmd"))
		}
		return out
	}},
}

// settingRef renders one setting as "config.json.path (ENV_OVERRIDE)" so the
// startup error names both sources. The override comes from schema.go.
func settingRef(domain, name string) string {
	path := domain + "." + name
	if s, ok := SettingByPath(domain, name); ok && s.Env != "" {
		return path + " (" + s.Env + ")"
	}
	return path
}

// oneOf renders an alternative group with bare config.json paths, e.g.
// "one of gmail.app_password / gmail.app_password_file / gmail.app_password_cmd".
// The environment overrides are omitted because any one of the alternatives is
// enough (and the group would get long).
func oneOf(domain string, names ...string) string {
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = domain + "." + n
	}
	return "one of " + strings.Join(paths, " / ")
}

// MissingSettings is one enabled integration and the settings it lacks.
type MissingSettings struct {
	Domain  string
	Missing []string
}

// MissingSettingsError lists every enabled integration that cannot start. It
// reports all problems at once instead of only the first one.
type MissingSettingsError struct {
	Problems []MissingSettings
}

// Error renders one line per incomplete integration plus how to proceed.
func (e *MissingSettingsError) Error() string {
	var b strings.Builder
	off := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		fmt.Fprintf(&b, "%s is enabled but missing: %s\n", p.Domain, strings.Join(p.Missing, ", "))
		off[i] = p.Domain + ".enabled=false"
	}
	pronoun := "it"
	if len(e.Problems) > 1 {
		pronoun = "them"
	}
	fmt.Fprintf(&b, "Fill in the missing settings in config.json (or set their environment overrides), "+
		"or set %s in config.json to disable %s.", strings.Join(off, " / "), pronoun)
	return b.String()
}

// ValidateIntegrations reports every enabled integration that lacks settings
// required to start, as a single error that names all of them. Disabled
// integrations are never validated (their settings are not read).
func (c *Config) ValidateIntegrations() error {
	var problems []MissingSettings
	for _, req := range integrationRequirements {
		if !c.ExplicitFlag(req.domain, "enabled") || req.missing == nil {
			continue
		}
		if missing := req.missing(c); len(missing) > 0 {
			problems = append(problems, MissingSettings{Domain: req.domain, Missing: missing})
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &MissingSettingsError{Problems: problems}
}
