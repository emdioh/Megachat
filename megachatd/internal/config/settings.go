package config

import (
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- WhatsApp -------------------------------------------------------------

// WhatsAppAPIURL is the configured API base URL ("" when unset).
func (c *Config) WhatsAppAPIURL() string {
	return strings.TrimRight(strings.TrimSpace(c.Get("whatsapp_api_url", "WHATSAPP_API_URL", "")), "/")
}

// WhatsAppAPIPort is the host port of the local WAHA service (default 3005).
func (c *Config) WhatsAppAPIPort() int {
	raw := c.Env("WHATSAPP_API_PORT")
	if raw == "" {
		return 3005
	}
	return atoiOr(raw, 3005)
}

// ResolveWhatsAppAPIURL prefers the configured URL, else http://127.0.0.1:port.
func (c *Config) ResolveWhatsAppAPIURL() string {
	if u := c.WhatsAppAPIURL(); u != "" {
		return u
	}
	return "http://127.0.0.1:" + strconv.Itoa(c.WhatsAppAPIPort())
}

// WhatsAppAPIKey: WHATSAPP_API_KEY, config whatsapp_api_key, .env WAHA_API_KEY.
func (c *Config) WhatsAppAPIKey() string {
	if v := c.Env("WHATSAPP_API_KEY"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := c.LoadFile()["whatsapp_api_key"]; v != nil && pyStr(v) != "" {
		return strings.TrimSpace(pyStr(v))
	}
	return strings.TrimSpace(c.LoadDotenv()["WAHA_API_KEY"])
}

// WhatsAppSessionName defaults to "default".
func (c *Config) WhatsAppSessionName() string {
	if v := strings.TrimSpace(c.Get("whatsapp_session_name", "WHATSAPP_SESSION_NAME", "default")); v != "" {
		return v
	}
	return "default"
}

// WhatsAppMediaDirConfigured is WHATSAPP_MEDIA_DIR / whatsapp_media_dir ("" if unset).
func (c *Config) WhatsAppMediaDirConfigured() string {
	return strings.TrimSpace(c.Get("whatsapp_media_dir", "WHATSAPP_MEDIA_DIR", ""))
}

// WhatsAppEnabled is true when an API URL is configured or a local WAHA
// answers on the default port (auto-detect, 1s timeout).
func (c *Config) WhatsAppEnabled() bool {
	if c.WhatsAppAPIURL() != "" {
		return true
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.WhatsAppAPIPort())), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// WhatsAppWebhookPort is CLIENT_WEBHOOK_PORT (default 8088).
func (c *Config) WhatsAppWebhookPort() int {
	raw := c.Env("CLIENT_WEBHOOK_PORT")
	if raw == "" {
		return 8088
	}
	return atoiOr(raw, 8088)
}

// WhatsAppWebhookEvents are the events WAHA must POST to the webhook.
var WhatsAppWebhookEvents = []string{
	"message", "message.any", "message.ack", "message.ack.group",
	"presence.update", "message.reaction", "chat.archive",
}

// WhatsAppWebhookURL: WAHA_WEBHOOK_URL, else host.docker.internal default.
func (c *Config) WhatsAppWebhookURL() string {
	if v := strings.TrimSpace(c.Env("WAHA_WEBHOOK_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://host.docker.internal:" + strconv.Itoa(c.WhatsAppWebhookPort()) + "/webhook"
}

// ---- Telegram -------------------------------------------------------------

// TelegramAPIID: env, config telegram_api_id (non-zero), .env; 0 = disabled.
func (c *Config) TelegramAPIID() int {
	if raw := c.Env("TELEGRAM_API_ID"); raw != "" {
		return atoiOr(raw, 0)
	}
	if n, ok := pyInt(c.LoadFile()["telegram_api_id"]); ok && n != 0 {
		return n
	}
	if raw := c.LoadDotenv()["TELEGRAM_API_ID"]; raw != "" {
		return atoiOr(raw, 0)
	}
	return 0
}

// TelegramAPIHash: env, config telegram_api_hash, .env.
func (c *Config) TelegramAPIHash() string {
	if v := c.Env("TELEGRAM_API_HASH"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := c.LoadFile()["telegram_api_hash"]; v != nil && pyStr(v) != "" {
		return strings.TrimSpace(pyStr(v))
	}
	return strings.TrimSpace(c.LoadDotenv()["TELEGRAM_API_HASH"])
}

// TelegramEnabled: credentials configured.
func (c *Config) TelegramEnabled() bool {
	return c.TelegramAPIID() != 0 && c.TelegramAPIHash() != ""
}

// TelegramReactionsNotify is TELEGRAM_REACTIONS_NOTIFY (default on; read
// directly from the environment in protocols/telegram.py).
func (c *Config) TelegramReactionsNotify() bool {
	v, ok := c.envSet("TELEGRAM_REACTIONS_NOTIFY")
	if !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ---- Signal ---------------------------------------------------------------

// SignalUserNumber: SIGNAL_USER_NUMBER, else config user_number, else "".
func (c *Config) SignalUserNumber() string {
	if v := c.Env("SIGNAL_USER_NUMBER"); v != "" {
		return v
	}
	if v := c.LoadFile()["user_number"]; v != nil && pyStr(v) != "" {
		return pyStr(v)
	}
	return ""
}

// SignalEnabled: explicit SIGNAL_ENABLED (env, config signal_enabled, .env)
// wins; otherwise enabled only when a user number is configured.
func (c *Config) SignalEnabled() bool {
	var explicit string
	set := false
	if v, ok := c.envSet("SIGNAL_ENABLED"); ok {
		explicit, set = v, true
	} else if v, ok := c.LoadFile()["signal_enabled"]; ok {
		explicit, set = pyStr(v), true
	} else if v, ok := c.LoadDotenv()["SIGNAL_ENABLED"]; ok {
		explicit, set = v, true
	}
	if set && strings.TrimSpace(explicit) != "" {
		switch strings.ToLower(strings.TrimSpace(explicit)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	return strings.TrimSpace(c.SignalUserNumber()) != ""
}

// Signal daemon constants (protocols/rpc.py).
const (
	SignalDaemonHTTPPort     = 8080
	SignalSubprocessTimeoutS = 120
	SignalDaemonURL          = "http://127.0.0.1:8080/api/v1/rpc"
	SignalSSEURL             = "http://127.0.0.1:8080/api/v1/events"
)

// SignalCLIAttachmentsDir is ~/.local/share/signal-cli/attachments.
func (c *Config) SignalCLIAttachmentsDir() string {
	return filepath.Join(c.Home, ".local", "share", "signal-cli", "attachments")
}

// SignalCLIAvatarsDir is ~/.local/share/signal-cli/avatars.
func (c *Config) SignalCLIAvatarsDir() string {
	return filepath.Join(c.Home, ".local", "share", "signal-cli", "avatars")
}

// SignalCLIBinDir is <project>/bin (signal-cli-*/bin/signal-cli lives there).
func (c *Config) SignalCLIBinDir() string { return filepath.Join(c.ProjectDir, "bin") }

// ---- Core tunables --------------------------------------------------------

// MessageRetentionPerContact: env, config, .env; default 300 (0 disables).
func (c *Config) MessageRetentionPerContact() int {
	if raw := c.Env("MESSAGE_RETENTION_PER_CONTACT"); raw != "" {
		return atoiOr(raw, 300)
	}
	if v, ok := c.LoadFile()["message_retention_per_contact"]; ok {
		if n, ok := pyInt(v); ok {
			return n
		}
		return 300
	}
	if raw := c.LoadDotenv()["MESSAGE_RETENTION_PER_CONTACT"]; raw != "" {
		return atoiOr(raw, 300)
	}
	return 300
}

func (c *Config) AddressBookTTLS() int {
	return c.GetInt("address_book_ttl_s", "ADDRESS_BOOK_TTL_S", 300)
}
func (c *Config) DynamicRefreshIntervalS() int {
	return c.GetInt("dynamic_refresh_interval_s", "DYNAMIC_REFRESH_INTERVAL_S", 300)
}
func (c *Config) DynamicRefreshCooldownS() int {
	return c.GetInt("dynamic_refresh_cooldown_s", "DYNAMIC_REFRESH_COOLDOWN_S", 60)
}
func (c *Config) WALidCacheTTLDays() int {
	return c.GetInt("wa_lid_cache_ttl_days", "WA_LID_CACHE_TTL_DAYS", 30)
}
func (c *Config) WALidRetryCooldownS() int {
	return c.GetInt("wa_lid_retry_cooldown_s", "WA_LID_RETRY_COOLDOWN_S", 300)
}
func (c *Config) WAResyncDownloadMedia() bool {
	return c.GetBool("wa_resync_download_media", "WA_RESYNC_DOWNLOAD_MEDIA", false)
}
func (c *Config) WALazyMedia() bool { return c.GetBool("wa_lazy_media", "WA_LAZY_MEDIA", true) }

// positive returns v if v > 0 else def; nonNeg returns v if v >= 0 else def.
func positive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
func nonNeg(v, def int) int {
	if v >= 0 {
		return v
	}
	return def
}

func (c *Config) WALazyMediaNegativeTTLS() int {
	return nonNeg(c.GetInt("wa_lazy_media_negative_ttl_s", "WA_LAZY_MEDIA_NEGATIVE_TTL_S", 30), 30)
}

// WAResyncMaxChats: 0 means unlimited; negatives fall back to 20.
func (c *Config) WAResyncMaxChats() int {
	return nonNeg(c.GetInt("wa_resync_max_chats", "WA_RESYNC_MAX_CHATS", 20), 20)
}
func (c *Config) WAResyncLimit() int {
	return positive(c.GetInt("wa_resync_limit", "WA_RESYNC_LIMIT", 30), 30)
}
func (c *Config) WAResyncWorkers() int {
	return positive(c.GetInt("wa_resync_workers", "WA_RESYNC_WORKERS", 2), 2)
}
func (c *Config) WASessionGuard() bool {
	return c.GetBool("wa_session_guard", "WA_SESSION_GUARD", true)
}
func (c *Config) WASessionPollS() int {
	return positive(c.GetInt("wa_session_poll_s", "WA_SESSION_POLL_S", 60), 60)
}
func (c *Config) WASessionRestartTimeoutS() int {
	return positive(c.GetInt("wa_session_restart_timeout_s", "WA_SESSION_RESTART_TIMEOUT_S", 120), 120)
}

// WASessionMaxRestartsPerHour: 0 means never restart.
func (c *Config) WASessionMaxRestartsPerHour() int {
	return nonNeg(c.GetInt("wa_session_max_restarts_per_hour", "WA_SESSION_MAX_RESTARTS_PER_HOUR", 3), 3)
}

// WASessionBackoffS: comma-separated positive ints, default 5,30,120,300.
func (c *Config) WASessionBackoffS() []int {
	def := []int{5, 30, 120, 300}
	raw := strings.TrimSpace(c.Get("wa_session_backoff_s", "WA_SESSION_BACKOFF_S", ""))
	if raw == "" {
		return def
	}
	var out []int
	for _, p := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n <= 0 {
			return def
		}
		out = append(out, n)
	}
	return out
}
func (c *Config) WASessionStableResetS() int {
	return positive(c.GetInt("wa_session_stable_reset_s", "WA_SESSION_STABLE_RESET_S", 600), 600)
}
func (c *Config) WASessionFailedGraceS() int {
	return nonNeg(c.GetInt("wa_session_failed_grace_s", "WA_SESSION_FAILED_GRACE_S", 10), 10)
}
func (c *Config) WASessionUnreachableAfter() int {
	return positive(c.GetInt("wa_session_unreachable_after", "WA_SESSION_UNREACHABLE_AFTER", 5), 5)
}
func (c *Config) PickerMaxResults() int {
	return c.GetInt("picker_max_results", "PICKER_MAX_RESULTS", 50)
}
func (c *Config) PickerPreferredBackend() string {
	return strings.TrimSpace(c.Get("picker_preferred_backend", "PICKER_PREFERRED_BACKEND", ""))
}

// ImageProtocol: auto|kitty|catimg|off (default auto).
func (c *Config) ImageProtocol() string {
	if v := strings.ToLower(strings.TrimSpace(c.Get("image_protocol", "IMAGE_PROTOCOL", "auto"))); v != "" {
		return v
	}
	return "auto"
}

// Thumbnail defaults (models.DEFAULT_THUMBNAIL_MAX_*).
const (
	DefaultThumbnailMaxLines = 12
	DefaultThumbnailMaxCols  = 60
)

func (c *Config) ThumbnailMaxLines() int {
	return c.GetInt("thumbnail_max_lines", "THUMBNAIL_MAX_LINES", DefaultThumbnailMaxLines)
}
func (c *Config) ThumbnailMaxCols() int {
	return c.GetInt("thumbnail_max_cols", "THUMBNAIL_MAX_COLS", DefaultThumbnailMaxCols)
}

// GmailUnreadPollS is GMAIL_UNREAD_POLL_S in seconds (default 60; <0 -> 0).
func (c *Config) GmailUnreadPollS() float64 {
	raw := strings.TrimSpace(c.Env("GMAIL_UNREAD_POLL_S"))
	if raw == "" {
		return 60
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 60
	}
	return max(0, f)
}

// ---- Web ------------------------------------------------------------------

func (c *Config) webSection() map[string]any {
	m, _ := c.LoadFile()["web"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// WebEnabled: config web.enabled (bool true or truthy string); default off.
func (c *Config) WebEnabled() bool {
	switch t := c.webSection()["enabled"].(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// WebPort: config web.port in 1..65535, default 4242.
func (c *Config) WebPort() int {
	v, ok := c.webSection()["port"]
	if !ok {
		return 4242
	}
	n, ok := pyInt(v)
	if !ok || n < 1 || n > 65535 {
		return 4242
	}
	return n
}

// WebHost: config web.host (string, or list joined by commas); default 127.0.0.1.
func (c *Config) WebHost() string {
	v, ok := c.webSection()["host"]
	if !ok || v == nil {
		return "127.0.0.1"
	}
	if l, ok := v.([]any); ok {
		parts := make([]string, len(l))
		for i, x := range l {
			parts[i] = pyStr(x)
		}
		if s := strings.Join(parts, ","); s != "" {
			return s
		}
		return "127.0.0.1"
	}
	return pyStr(v)
}

// WebToken: SIGNAL_TUI_WEB_TOKEN, else config web.token.
func (c *Config) WebToken() string {
	if v := c.Env("SIGNAL_TUI_WEB_TOKEN"); v != "" {
		return v
	}
	if v := c.webSection()["token"]; v != nil {
		return pyStr(v)
	}
	return ""
}

// ---- Gmail ----------------------------------------------------------------

// getGmail resolves env (non-blank), config.json (non-blank), then .env.
func (c *Config) getGmail(key, envName, def string) string {
	if v := strings.TrimSpace(c.Env(envName)); v != "" {
		return v
	}
	if v := c.LoadFile()[key]; v != nil && strings.TrimSpace(pyStr(v)) != "" {
		return strings.TrimSpace(pyStr(v))
	}
	if v := strings.TrimSpace(c.LoadDotenv()[envName]); v != "" {
		return v
	}
	return def
}

func (c *Config) getGmailInt(key, envName string, def int) int {
	return atoiOr(c.getGmail(key, envName, strconv.Itoa(def)), def)
}

func (c *Config) GmailAddress() string { return c.getGmail("gmail_address", "GMAIL_ADDRESS", "") }
func (c *Config) GmailAppPasswordFile() string {
	return c.getGmail("gmail_app_password_file", "GMAIL_APP_PASSWORD_FILE", "")
}
func (c *Config) GmailAppPasswordCmd() string {
	return c.getGmail("gmail_app_password_cmd", "GMAIL_APP_PASSWORD_CMD", "")
}
func (c *Config) GmailIMAPHost() string {
	return c.getGmail("gmail_imap_host", "GMAIL_IMAP_HOST", "imap.gmail.com")
}
func (c *Config) GmailIMAPPort() int {
	if p := c.getGmailInt("gmail_imap_port", "GMAIL_IMAP_PORT", 993); p >= 1 && p <= 65535 {
		return p
	}
	return 993
}
func (c *Config) GmailSMTPHost() string {
	return c.getGmail("gmail_smtp_host", "GMAIL_SMTP_HOST", "smtp.gmail.com")
}
func (c *Config) GmailSMTPPort() int {
	if p := c.getGmailInt("gmail_smtp_port", "GMAIL_SMTP_PORT", 587); p >= 1 && p <= 65535 {
		return p
	}
	return 587
}
func (c *Config) GmailFolder() string {
	if f := c.getGmail("gmail_folder", "GMAIL_FOLDER", "INBOX"); f != "" {
		return f
	}
	return "INBOX"
}
func (c *Config) GmailTimeoutS() int {
	return positive(c.getGmailInt("gmail_timeout_s", "GMAIL_TIMEOUT_S", 30), 30)
}

// GmailHTMLStyles / GmailHTMLInlineCSS / GmailImagePrefetch are the
// "set to 0 to disable" env switches read in server/gmail.py and ops/gmail.py.
func (c *Config) envNotZero(name string) bool {
	v, ok := c.envSet(name)
	return !ok || strings.TrimSpace(v) != "0"
}
func (c *Config) GmailHTMLStyles() bool    { return c.envNotZero("GMAIL_HTML_STYLES") }
func (c *Config) GmailHTMLInlineCSS() bool { return c.envNotZero("GMAIL_HTML_INLINE_CSS") }
func (c *Config) GmailImagePrefetch() bool { return c.envNotZero("GMAIL_IMAGE_PREFETCH") }

// GmailAppPassword resolves the App Password: file, then command, then the
// plaintext GMAIL_APP_PASSWORD. Unlike Python the command output is not
// cached for 300s (callers may cache).
func (c *Config) GmailAppPassword() string {
	if p := c.GmailAppPasswordFile(); p != "" {
		if s := c.secretFromFile(p); s != "" {
			return s
		}
	}
	if cmd := c.GmailAppPasswordCmd(); cmd != "" {
		if s := secretFromCommand(cmd); s != "" {
			return s
		}
	}
	return c.getGmail("gmail_app_password", "GMAIL_APP_PASSWORD", "")
}

// GmailEnabled: address and App Password both configured.
func (c *Config) GmailEnabled() bool { return c.GmailAddress() != "" && c.GmailAppPassword() != "" }

func (c *Config) secretFromFile(raw string) string {
	p := expandHome(raw, c.Home)
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.ProjectDir, p)
	}
	data, err := readFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(data)
}

func secretFromCommand(command string) string {
	argv := shellSplit(command)
	if len(argv) == 0 {
		return ""
	}
	ctx, cancel := contextTimeout(10 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
