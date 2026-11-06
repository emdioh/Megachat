package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SettingKind is the JSON type of a setting's value.
type SettingKind int

const (
	KindString SettingKind = iota
	KindInt
	KindBool
	KindFloat
	// KindMap is an object-valued setting (transcription auto-transcribe
	// switches). Map settings have no environment override.
	KindMap
)

// Setting is one entry of the configuration schema: where it lives in
// config.json (Domain.Name), which environment variable overrides it, and the
// legacy flat config.json keys / environment variables it replaced.
//
// Environment variables are derived from the location: UPPER(Domain_Name).
// A Setting with an empty Domain is a legacy source that no Go code reads any
// more (the WAHA-only settings): it is reported at startup and removed by the
// migrate command, but it does not resolve to anything.
type Setting struct {
	Domain string
	Name   string
	Env    string
	Kind   SettingKind

	// LegacyKeys are the flat config.json keys that used to carry the setting.
	LegacyKeys []string
	// LegacyEnvs are the environment variable names that used to carry it.
	LegacyEnvs []string
}

// Path is the config.json location, "domain.setting" ("" for a removed one).
func (s Setting) Path() string {
	if s.Domain == "" {
		return ""
	}
	return s.Domain + "." + s.Name
}

// Removed reports whether the setting no longer exists in the schema.
func (s Setting) Removed() bool { return s.Domain == "" }

// Parse converts a string value (an environment variable or a .env line) into
// the JSON value of the setting's kind. Map settings cannot be set that way.
func (s Setting) Parse(raw string) (any, error) {
	v := strings.TrimSpace(raw)
	switch s.Kind {
	case KindInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s: not an integer: %q", s.Path(), raw)
		}
		return n, nil
	case KindFloat:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: not a number: %q", s.Path(), raw)
		}
		return f, nil
	case KindBool:
		b, ok := lookupBool(v)
		if !ok {
			return nil, fmt.Errorf("%s: not a boolean: %q", s.Path(), raw)
		}
		return b, nil
	case KindMap:
		return nil, fmt.Errorf("%s: object settings have no environment override", s.Path())
	}
	return v, nil
}

// envName is the environment variable that overrides (domain, setting):
// UPPER(domain_setting).
func envName(domain, setting string) string {
	return strings.ToUpper(domain + "_" + setting)
}

// def describes a setting whose environment variable is derived from its
// location.
func def(domain, name string, kind SettingKind) Setting {
	return Setting{Domain: domain, Name: name, Env: envName(domain, name), Kind: kind}
}

// legacy adds the old flat config.json keys and environment variables.
func (s Setting) legacy(keys []string, envs ...string) Setting {
	s.LegacyKeys = keys
	s.LegacyEnvs = envs
	return s
}

// settings is the single schema table of megachatd: every configuration
// setting, its config.json location, its environment override and the legacy
// names it replaces. The startup warnings and the "config migrate" command
// both read it; nothing else keeps a copy of these names.
var settings = []Setting{
	// signal
	def("signal", "enabled", KindBool).legacy([]string{"signal_enabled"}),
	def("signal", "user_number", KindString).legacy([]string{"user_number"}),

	// whatsapp
	def("whatsapp", "enabled", KindBool).legacy([]string{"whatsapp_enabled"}),
	def("whatsapp", "session_name", KindString).legacy([]string{"whatsapp_session_name"}),
	def("whatsapp", "media_dir", KindString).legacy([]string{"whatsapp_media_dir"}),
	def("whatsapp", "browser", KindString).legacy([]string{"whatsapp_browser"}),
	def("whatsapp", "browser_version", KindString).legacy([]string{"whatsapp_browser_version"}),
	def("whatsapp", "browser_os", KindString).legacy([]string{"whatsapp_browser_os"}),
	def("whatsapp", "lazy_media", KindBool).legacy([]string{"wa_lazy_media"}, "WA_LAZY_MEDIA"),
	def("whatsapp", "lazy_media_negative_ttl_s", KindInt).
		legacy([]string{"wa_lazy_media_negative_ttl_s"}, "WA_LAZY_MEDIA_NEGATIVE_TTL_S"),
	def("whatsapp", "lid_cache_ttl_days", KindInt).legacy([]string{"wa_lid_cache_ttl_days"}, "WA_LID_CACHE_TTL_DAYS"),
	def("whatsapp", "lid_retry_cooldown_s", KindInt).legacy([]string{"wa_lid_retry_cooldown_s"}, "WA_LID_RETRY_COOLDOWN_S"),
	def("whatsapp", "resync_download_media", KindBool).legacy([]string{"wa_resync_download_media"}, "WA_RESYNC_DOWNLOAD_MEDIA"),
	def("whatsapp", "resync_max_chats", KindInt).legacy([]string{"wa_resync_max_chats"}, "WA_RESYNC_MAX_CHATS"),
	def("whatsapp", "resync_limit", KindInt).legacy([]string{"wa_resync_limit"}, "WA_RESYNC_LIMIT"),
	def("whatsapp", "resync_workers", KindInt).legacy([]string{"wa_resync_workers"}, "WA_RESYNC_WORKERS"),
	def("whatsapp", "session_guard", KindBool).legacy([]string{"wa_session_guard"}, "WA_SESSION_GUARD"),
	def("whatsapp", "session_poll_s", KindInt).legacy([]string{"wa_session_poll_s"}, "WA_SESSION_POLL_S"),
	def("whatsapp", "session_restart_timeout_s", KindInt).
		legacy([]string{"wa_session_restart_timeout_s"}, "WA_SESSION_RESTART_TIMEOUT_S"),
	def("whatsapp", "session_max_restarts_per_hour", KindInt).
		legacy([]string{"wa_session_max_restarts_per_hour"}, "WA_SESSION_MAX_RESTARTS_PER_HOUR"),
	def("whatsapp", "session_backoff_s", KindString).legacy([]string{"wa_session_backoff_s"}, "WA_SESSION_BACKOFF_S"),
	def("whatsapp", "session_stable_reset_s", KindInt).legacy([]string{"wa_session_stable_reset_s"}, "WA_SESSION_STABLE_RESET_S"),
	def("whatsapp", "session_failed_grace_s", KindInt).legacy([]string{"wa_session_failed_grace_s"}, "WA_SESSION_FAILED_GRACE_S"),
	def("whatsapp", "session_unreachable_after", KindInt).
		legacy([]string{"wa_session_unreachable_after"}, "WA_SESSION_UNREACHABLE_AFTER"),
	def("whatsapp", "presence_subscribe", KindBool).legacy(nil, "WAHA_PRESENCE_SUBSCRIBE"),

	// telegram
	def("telegram", "enabled", KindBool).legacy([]string{"telegram_enabled"}),
	def("telegram", "api_id", KindInt).legacy([]string{"telegram_api_id"}),
	def("telegram", "api_hash", KindString).legacy([]string{"telegram_api_hash"}),
	def("telegram", "reactions_notify", KindBool),

	// gmail
	def("gmail", "enabled", KindBool).legacy([]string{"gmail_enabled"}),
	def("gmail", "address", KindString).legacy([]string{"gmail_address"}),
	def("gmail", "app_password", KindString).legacy([]string{"gmail_app_password"}),
	def("gmail", "app_password_file", KindString).legacy([]string{"gmail_app_password_file"}),
	def("gmail", "app_password_cmd", KindString).legacy([]string{"gmail_app_password_cmd"}),
	def("gmail", "imap_host", KindString).legacy([]string{"gmail_imap_host"}),
	def("gmail", "imap_port", KindInt).legacy([]string{"gmail_imap_port"}),
	def("gmail", "smtp_host", KindString).legacy([]string{"gmail_smtp_host"}),
	def("gmail", "smtp_port", KindInt).legacy([]string{"gmail_smtp_port"}),
	def("gmail", "folder", KindString).legacy([]string{"gmail_folder"}),
	def("gmail", "timeout_s", KindInt).legacy([]string{"gmail_timeout_s"}),
	def("gmail", "unread_poll_s", KindFloat),
	def("gmail", "html_styles", KindBool),
	def("gmail", "html_inline_css", KindBool),
	def("gmail", "image_prefetch", KindBool),

	// web
	def("web", "enabled", KindBool),
	def("web", "host", KindString),
	def("web", "port", KindInt),
	def("web", "token", KindString).legacy(nil, "SIGNAL_TUI_WEB_TOKEN"),

	// transcription
	def("transcription", "provider", KindString).legacy([]string{"ai_provider"}, "AI_PROVIDER"),
	def("transcription", "language", KindString).legacy([]string{"transcription_language"}),
	def("transcription", "timeout", KindInt).legacy([]string{"transcription_timeout"}),
	// Map-valued settings have no environment override.
	{Domain: "transcription", Name: "auto", Kind: KindMap, LegacyKeys: []string{"auto_transcribe"}},
	{Domain: "transcription", Name: "auto_chats", Kind: KindMap, LegacyKeys: []string{"auto_transcribe_chats"}},

	// transcription providers
	def("openai", "api_key", KindString).legacy([]string{"openai_api_key"}),
	def("openai", "transcription_model", KindString).
		legacy([]string{"openai_transcription_model", "transcription_model"}, "TRANSCRIPTION_MODEL"),
	def("openai", "base_url", KindString).legacy([]string{"transcription_base_url"}, "TRANSCRIPTION_BASE_URL"),
	def("openrouter", "api_key", KindString).legacy([]string{"openrouter_api_key"}),
	def("openrouter", "transcription_model", KindString).legacy([]string{"openrouter_transcription_model"}),
	def("openrouter", "base_url", KindString).legacy([]string{"openrouter_base_url"}),
	def("openrouter", "http_referer", KindString).legacy([]string{"openrouter_http_referer"}),
	def("openrouter", "app_title", KindString).legacy([]string{"openrouter_app_title"}),
	def("gemini", "api_key", KindString).legacy([]string{"gemini_api_key"}, "GOOGLE_API_KEY"),
	def("gemini", "transcription_model", KindString).legacy([]string{"gemini_transcription_model"}),
	def("gemini", "base_url", KindString).legacy([]string{"gemini_base_url"}),

	// core tunables
	def("messages", "retention_per_contact", KindInt).
		legacy([]string{"message_retention_per_contact"}, "MESSAGE_RETENTION_PER_CONTACT"),
	def("address_book", "ttl_s", KindInt).legacy([]string{"address_book_ttl_s"}),
	def("dynamic_refresh", "interval_s", KindInt).legacy([]string{"dynamic_refresh_interval_s"}),
	def("dynamic_refresh", "cooldown_s", KindInt).legacy([]string{"dynamic_refresh_cooldown_s"}),
	def("picker", "max_results", KindInt).legacy([]string{"picker_max_results"}),
	def("picker", "preferred_backend", KindString).legacy([]string{"picker_preferred_backend"}),
	def("image", "protocol", KindString).legacy([]string{"image_protocol"}),
	def("thumbnail", "max_lines", KindInt).legacy([]string{"thumbnail_max_lines"}),
	def("thumbnail", "max_cols", KindInt).legacy([]string{"thumbnail_max_cols"}),

	// Removed WAHA-only settings: no Go code reads them any more. They are kept
	// here so startup can report them and "config migrate" can drop them.
	{Domain: "", Env: "", LegacyKeys: []string{"whatsapp_api_url"}, LegacyEnvs: []string{"WHATSAPP_API_URL"}},
	{Domain: "", Env: "", LegacyKeys: []string{"whatsapp_api_key"},
		LegacyEnvs: []string{"WHATSAPP_API_KEY", "WAHA_API_KEY"}},
	{LegacyEnvs: []string{"WHATSAPP_API_PORT", "CLIENT_WEBHOOK_PORT", "WAHA_WEBHOOK_URL"}},
}

var (
	settingsByPath  = map[string]Setting{}
	settingsByEnv   = map[string]Setting{}
	settingsByKey   = map[string]Setting{}
	settingsByLEnv  = map[string]Setting{}
	legacySettingLs = []Setting{}
	legacyEnvNames  = []string{}
)

func init() {
	for _, s := range settings {
		if !s.Removed() {
			settingsByPath[s.Path()] = s
			if s.Env != "" {
				settingsByEnv[s.Env] = s
			}
		}
		for _, k := range s.LegacyKeys {
			settingsByKey[k] = s
		}
		for _, e := range s.LegacyEnvs {
			settingsByLEnv[e] = s
			legacyEnvNames = append(legacyEnvNames, e)
		}
		if len(s.LegacyKeys) > 0 || len(s.LegacyEnvs) > 0 {
			legacySettingLs = append(legacySettingLs, s)
		}
	}
}

// Settings returns the whole schema table. The table is read-only for callers
// (it backs the resolution and migration logic), so treat it as immutable.
func Settings() []Setting { return settings }

// LegacySettings returns the schema entries that replaced something.
func LegacySettings() []Setting { return legacySettingLs }

// SettingByPath looks a setting up by its config.json location.
func SettingByPath(domain, setting string) (Setting, bool) {
	s, ok := settingsByPath[domain+"."+setting]
	return s, ok
}

// SettingByEnv looks a setting up by its current environment variable.
func SettingByEnv(name string) (Setting, bool) {
	s, ok := settingsByEnv[name]
	return s, ok
}

// SettingByLegacyKey looks a setting up by the legacy flat config.json key.
func SettingByLegacyKey(key string) (Setting, bool) {
	s, ok := settingsByKey[key]
	return s, ok
}

// SettingByLegacyEnv looks a setting up by a legacy environment variable.
func SettingByLegacyEnv(name string) (Setting, bool) {
	s, ok := settingsByLEnv[name]
	return s, ok
}

// LegacyEntry is one legacy source found in the project: a flat config.json
// key or an environment variable.
type LegacyEntry struct {
	Setting Setting
	// Name is the legacy key or environment variable.
	Name string
}

// LegacyConfigEntries returns the legacy flat keys present in config.json,
// sorted by key.
func (c *Config) LegacyConfigEntries() []LegacyEntry {
	file := c.LoadFile()
	var out []LegacyEntry
	for k := range file {
		if s, ok := SettingByLegacyKey(k); ok {
			out = append(out, LegacyEntry{Setting: s, Name: k})
		}
	}
	sortEntries(out)
	return out
}

// LegacyEnvEntries returns the legacy environment variables that are set
// (present and non-blank), sorted by name.
func (c *Config) LegacyEnvEntries() []LegacyEntry {
	var out []LegacyEntry
	for _, name := range legacyEnvNames {
		if v, ok := c.envSet(name); ok && strings.TrimSpace(v) != "" {
			s, _ := SettingByLegacyEnv(name)
			out = append(out, LegacyEntry{Setting: s, Name: name})
		}
	}
	sortEntries(out)
	return out
}

func sortEntries(es []LegacyEntry) {
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
}
