package config

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// trimmedString is the string getter with surrounding whitespace removed; a
// blank value falls back to def.
func (c *Config) trimmedString(domain, setting, def string) string {
	if v := strings.TrimSpace(c.GetString(domain, setting, "")); v != "" {
		return v
	}
	return def
}

// ---- WhatsApp -------------------------------------------------------------

// WhatsAppSessionName defaults to "default".
func (c *Config) WhatsAppSessionName() string {
	return c.trimmedString("whatsapp", "session_name", "default")
}

// WhatsAppMediaDirConfigured is whatsapp.media_dir ("" if unset).
func (c *Config) WhatsAppMediaDirConfigured() string {
	return c.trimmedString("whatsapp", "media_dir", "")
}

// WhatsAppBrowser is the browser name announced to WhatsApp at registration
// (whatsapp.browser). "" leaves the backend default (Chrome); accepted names
// are the browsers WhatsApp itself lists, see the whatsapp package.
func (c *Config) WhatsAppBrowser() string {
	return c.trimmedString("whatsapp", "browser", "")
}

// WhatsAppBrowserVersion overrides the browser version string
// (whatsapp.browser_version); "" = backend default.
func (c *Config) WhatsAppBrowserVersion() string {
	return c.trimmedString("whatsapp", "browser_version", "")
}

// WhatsAppBrowserOS overrides the OS part of the device name
// (whatsapp.browser_os); "" = backend default.
func (c *Config) WhatsAppBrowserOS() string {
	return c.trimmedString("whatsapp", "browser_os", "")
}

// WhatsAppPresenceSubscribe is whatsapp.presence_subscribe: the typing
// indicator stays off by default because announcing "available" mutes the
// phone's push notifications.
func (c *Config) WhatsAppPresenceSubscribe() bool {
	return c.GetBool("whatsapp", "presence_subscribe", false)
}

// ---- Telegram -------------------------------------------------------------

// TelegramAPIID is telegram.api_id (0 = not configured).
func (c *Config) TelegramAPIID() int { return c.GetInt("telegram", "api_id", 0) }

// TelegramAPIHash is telegram.api_hash ("" = not configured).
func (c *Config) TelegramAPIHash() string {
	return c.trimmedString("telegram", "api_hash", "")
}

// TelegramReactionsNotify is telegram.reactions_notify (default on).
func (c *Config) TelegramReactionsNotify() bool {
	return c.GetBool("telegram", "reactions_notify", true)
}

// ---- Signal ---------------------------------------------------------------

// SignalUserNumber is signal.user_number ("" when unset).
func (c *Config) SignalUserNumber() string {
	return c.GetString("signal", "user_number", "")
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

// MessageRetentionPerContact is messages.retention_per_contact (default 300;
// 0 disables).
func (c *Config) MessageRetentionPerContact() int {
	return c.GetInt("messages", "retention_per_contact", 300)
}

func (c *Config) AddressBookTTLS() int { return c.GetInt("address_book", "ttl_s", 300) }
func (c *Config) DynamicRefreshIntervalS() int {
	return c.GetInt("dynamic_refresh", "interval_s", 300)
}
func (c *Config) DynamicRefreshCooldownS() int {
	return c.GetInt("dynamic_refresh", "cooldown_s", 60)
}
func (c *Config) WALidCacheTTLDays() int { return c.GetInt("whatsapp", "lid_cache_ttl_days", 30) }
func (c *Config) WALidRetryCooldownS() int {
	return c.GetInt("whatsapp", "lid_retry_cooldown_s", 300)
}
func (c *Config) WAResyncDownloadMedia() bool {
	return c.GetBool("whatsapp", "resync_download_media", false)
}
func (c *Config) WALazyMedia() bool { return c.GetBool("whatsapp", "lazy_media", true) }

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
	return nonNeg(c.GetInt("whatsapp", "lazy_media_negative_ttl_s", 30), 30)
}

// WAResyncMaxChats: 0 means unlimited; negatives fall back to 20.
func (c *Config) WAResyncMaxChats() int {
	return nonNeg(c.GetInt("whatsapp", "resync_max_chats", 20), 20)
}
func (c *Config) WAResyncLimit() int {
	return positive(c.GetInt("whatsapp", "resync_limit", 30), 30)
}
func (c *Config) WAResyncWorkers() int {
	return positive(c.GetInt("whatsapp", "resync_workers", 2), 2)
}
func (c *Config) WASessionGuard() bool {
	return c.GetBool("whatsapp", "session_guard", true)
}
func (c *Config) WASessionPollS() int {
	return positive(c.GetInt("whatsapp", "session_poll_s", 60), 60)
}
func (c *Config) WASessionRestartTimeoutS() int {
	return positive(c.GetInt("whatsapp", "session_restart_timeout_s", 120), 120)
}

// WASessionMaxRestartsPerHour: 0 means never restart.
func (c *Config) WASessionMaxRestartsPerHour() int {
	return nonNeg(c.GetInt("whatsapp", "session_max_restarts_per_hour", 3), 3)
}

// WASessionBackoffS: comma-separated positive ints, default 5,30,120,300.
func (c *Config) WASessionBackoffS() []int {
	def := []int{5, 30, 120, 300}
	raw := strings.TrimSpace(c.GetString("whatsapp", "session_backoff_s", ""))
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
	return positive(c.GetInt("whatsapp", "session_stable_reset_s", 600), 600)
}
func (c *Config) WASessionFailedGraceS() int {
	return nonNeg(c.GetInt("whatsapp", "session_failed_grace_s", 10), 10)
}
func (c *Config) WASessionUnreachableAfter() int {
	return positive(c.GetInt("whatsapp", "session_unreachable_after", 5), 5)
}
func (c *Config) PickerMaxResults() int { return c.GetInt("picker", "max_results", 50) }
func (c *Config) PickerPreferredBackend() string {
	return c.trimmedString("picker", "preferred_backend", "")
}

// ImageProtocol: auto|kitty|catimg|off (default auto).
func (c *Config) ImageProtocol() string {
	return c.trimmedString("image", "protocol", "auto")
}

// Thumbnail defaults (models.DEFAULT_THUMBNAIL_MAX_*).
const (
	DefaultThumbnailMaxLines = 12
	DefaultThumbnailMaxCols  = 60
)

func (c *Config) ThumbnailMaxLines() int {
	return c.GetInt("thumbnail", "max_lines", DefaultThumbnailMaxLines)
}
func (c *Config) ThumbnailMaxCols() int {
	return c.GetInt("thumbnail", "max_cols", DefaultThumbnailMaxCols)
}

// GmailUnreadPollS is gmail.unread_poll_s in seconds (default 60; <0 -> 0).
func (c *Config) GmailUnreadPollS() float64 {
	return max(0, c.GetFloat("gmail", "unread_poll_s", 60))
}

// ---- Web ------------------------------------------------------------------

// WebEnabled is web.enabled (bool or a lookupBool spelling); default off.
func (c *Config) WebEnabled() bool { return c.GetBool("web", "enabled", false) }

// WebPort is web.port in 1..65535, default 4242.
func (c *Config) WebPort() int {
	if p := c.GetInt("web", "port", 4242); p >= 1 && p <= 65535 {
		return p
	}
	return 4242
}

// WebHost is web.host (string, or list joined by commas); default 127.0.0.1.
func (c *Config) WebHost() string {
	if v, ok := c.environment("web", "host"); ok {
		return strings.TrimSpace(v)
	}
	v, ok := c.fileValue("web", "host")
	if !ok {
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

// WebToken is web.token ("" when unset).
func (c *Config) WebToken() string { return c.GetString("web", "token", "") }

// ---- Gmail ----------------------------------------------------------------

// gmailString resolves env (non-blank), config.json (non-blank), then def.
func (c *Config) gmailString(setting, def string) string {
	return c.trimmedString("gmail", setting, def)
}

func (c *Config) GmailAddress() string { return c.gmailString("address", "") }
func (c *Config) GmailAppPasswordFile() string {
	return c.gmailString("app_password_file", "")
}
func (c *Config) GmailAppPasswordCmd() string {
	return c.gmailString("app_password_cmd", "")
}
func (c *Config) GmailIMAPHost() string { return c.gmailString("imap_host", "imap.gmail.com") }
func (c *Config) GmailIMAPPort() int {
	if p := c.GetInt("gmail", "imap_port", 993); p >= 1 && p <= 65535 {
		return p
	}
	return 993
}
func (c *Config) GmailSMTPHost() string { return c.gmailString("smtp_host", "smtp.gmail.com") }
func (c *Config) GmailSMTPPort() int {
	if p := c.GetInt("gmail", "smtp_port", 587); p >= 1 && p <= 65535 {
		return p
	}
	return 587
}
func (c *Config) GmailFolder() string { return c.gmailString("folder", "INBOX") }
func (c *Config) GmailTimeoutS() int {
	return positive(c.GetInt("gmail", "timeout_s", 30), 30)
}

// GmailHTMLStyles / GmailHTMLInlineCSS / GmailImagePrefetch default on.
func (c *Config) GmailHTMLStyles() bool    { return c.GetBool("gmail", "html_styles", true) }
func (c *Config) GmailHTMLInlineCSS() bool { return c.GetBool("gmail", "html_inline_css", true) }
func (c *Config) GmailImagePrefetch() bool { return c.GetBool("gmail", "image_prefetch", true) }

// GmailAppPassword resolves the App Password: file, then command, then the
// plaintext gmail.app_password. Unlike Python the command output is not
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
	return c.gmailString("app_password", "")
}

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
