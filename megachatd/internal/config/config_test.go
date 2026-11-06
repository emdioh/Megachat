package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func newCfg(t *testing.T, env map[string]string, file string) *Config {
	t.Helper()
	dir := t.TempDir()
	if file != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewWith(dir, "/home/u", env)
}

// sample returns a JSON value and its environment spelling for a kind.
func sample(k SettingKind) (any, string) {
	switch k {
	case KindInt:
		return 7, "11"
	case KindBool:
		return true, "on"
	case KindFloat:
		return 1.5, "2.5"
	}
	return "file", "env"
}

func TestDefaults(t *testing.T) {
	c := newCfg(t, nil, "")
	if c.WebPort() != 4242 || c.WebHost() != "127.0.0.1" || c.WebEnabled() || c.WebToken() != "" {
		t.Fatal("web defaults")
	}
	if c.WhatsAppSessionName() != "default" || c.WhatsAppPresenceSubscribe() {
		t.Fatal("whatsapp defaults")
	}
	if c.MessageRetentionPerContact() != 300 || c.AddressBookTTLS() != 300 || !c.WALazyMedia() ||
		c.WAResyncMaxChats() != 20 || c.WAResyncLimit() != 30 || c.WASessionMaxRestartsPerHour() != 3 {
		t.Fatal("core defaults")
	}
	if !reflect.DeepEqual(c.WASessionBackoffS(), []int{5, 30, 120, 300}) {
		t.Fatal("backoff")
	}
	if c.GmailIMAPHost() != "imap.gmail.com" || c.GmailIMAPPort() != 993 || c.GmailSMTPPort() != 587 ||
		c.GmailFolder() != "INBOX" || c.GmailTimeoutS() != 30 || c.GmailEnabled() {
		t.Fatal("gmail defaults")
	}
	if !c.GmailHTMLStyles() || !c.GmailHTMLInlineCSS() || !c.GmailImagePrefetch() || c.GmailUnreadPollS() != 60 {
		t.Fatal("gmail switches default on")
	}
	if c.ImageProtocol() != "auto" || c.ThumbnailMaxLines() != 12 || c.ThumbnailMaxCols() != 60 {
		t.Fatal("image defaults")
	}
	if c.TranscriptionProvider() != "openai" || c.TranscriptionTimeoutS() != 120 || c.TranscriptionEnabled() {
		t.Fatal("transcription defaults")
	}
	if !c.TelegramReactionsNotify() {
		t.Fatal("reactions notify defaults on")
	}
	if m, _ := c.ProviderModel(""); m != "gpt-transcribe" {
		t.Fatal(m)
	}
	if u, _ := c.ProviderBaseURL("gemini"); u != "https://generativelanguage.googleapis.com/v1beta" {
		t.Fatal(u)
	}
	if c.SignalEnabled() || c.TelegramEnabled() {
		t.Fatal("enabled defaults")
	}
}

// TestResolutionEverySetting walks the schema: each setting resolves from
// config.json, then from its environment override, and blank env falls
// through to config.json.
func TestResolutionEverySetting(t *testing.T) {
	for _, s := range Settings() {
		if s.Removed() || s.Kind == KindMap {
			continue
		}
		t.Run(s.Path(), func(t *testing.T) {
			file, env := sample(s.Kind)
			c := newCfg(t, nil, `{"`+s.Domain+`":{"`+s.Name+`":`+jsonOf(file)+`}}`)
			if got := generic(c, s.Kind, s.Domain, s.Name); got != file {
				t.Fatalf("config.json value: got %v want %v", got, file)
			}
			var want any = env
			if s.Kind == KindInt {
				want = 11
			} else if s.Kind == KindFloat {
				want = 2.5
			} else if s.Kind == KindBool {
				want = true
			}
			c = newCfg(t, map[string]string{s.Env: env},
				`{"`+s.Domain+`":{"`+s.Name+`":`+jsonOf(file)+`}}`)
			if got := generic(c, s.Kind, s.Domain, s.Name); got != want {
				t.Fatalf("env override %s: got %v want %v", s.Env, got, want)
			}
			// A blank env var (and an unparseable one) falls through.
			c = newCfg(t, map[string]string{s.Env: "   "},
				`{"`+s.Domain+`":{"`+s.Name+`":`+jsonOf(file)+`}}`)
			if got := generic(c, s.Kind, s.Domain, s.Name); got != file {
				t.Fatalf("blank env must fall through: got %v", got)
			}
			if s.Kind != KindString { // any string is a valid string setting
				c = newCfg(t, map[string]string{s.Env: "not-a-value"},
					`{"`+s.Domain+`":{"`+s.Name+`":`+jsonOf(file)+`}}`)
				if got := generic(c, s.Kind, s.Domain, s.Name); got != file {
					t.Fatalf("unparseable env must fall through: got %v", got)
				}
			}
		})
	}
}

func generic(c *Config, k SettingKind, domain, name string) any {
	switch k {
	case KindInt:
		return c.GetInt(domain, name, -1)
	case KindBool:
		return c.GetBool(domain, name, false)
	case KindFloat:
		return c.GetFloat(domain, name, -1)
	}
	return c.GetString(domain, name, "def")
}

func jsonOf(v any) string {
	if b, ok := v.(string); ok {
		return `"` + b + `"`
	}
	if v == true {
		return "true"
	}
	if v == false {
		return "false"
	}
	if n, ok := v.(int); ok {
		return itoa(n)
	}
	return "1.5"
}

func itoa(n int) string {
	return []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}[n]
}

func TestTypedFallthroughToDefault(t *testing.T) {
	c := newCfg(t, map[string]string{"ADDRESS_BOOK_TTL_S": "abc"}, `{"address_book":{"ttl_s":"nope"}}`)
	if c.AddressBookTTLS() != 300 {
		t.Fatal(c.AddressBookTTLS())
	}
	c = newCfg(t, nil, `{"whatsapp":{"lazy_media":42}}`)
	if !c.WALazyMedia() { // unparseable JSON value -> default
		t.Fatal("bad bool -> default")
	}
	c = newCfg(t, map[string]string{"WHATSAPP_LAZY_MEDIA": "garbage"}, `{"whatsapp":{"lazy_media":false}}`)
	if c.WALazyMedia() { // unparseable env -> next source
		t.Fatal("garbage env should fall to file")
	}
	c = newCfg(t, map[string]string{"WHATSAPP_LAZY_MEDIA": "  "}, `{"whatsapp":{"lazy_media":false}}`)
	if c.WALazyMedia() { // blank env falls through to file
		t.Fatal("blank env should fall to file")
	}
}

func TestClamps(t *testing.T) {
	c := newCfg(t, map[string]string{
		"WHATSAPP_RESYNC_MAX_CHATS": "-1", "WHATSAPP_RESYNC_LIMIT": "0", "WHATSAPP_SESSION_FAILED_GRACE_S": "0",
		"WHATSAPP_SESSION_BACKOFF_S": "1, 2,x", "GMAIL_IMAP_PORT": "70000", "TRANSCRIPTION_TIMEOUT": "-5",
	}, "")
	if c.WAResyncMaxChats() != 20 || c.WAResyncLimit() != 30 || c.WASessionFailedGraceS() != 0 {
		t.Fatal("clamps")
	}
	if !reflect.DeepEqual(c.WASessionBackoffS(), []int{5, 30, 120, 300}) {
		t.Fatal("bad backoff -> default")
	}
	if c.GmailIMAPPort() != 993 || c.TranscriptionTimeoutS() != 120 {
		t.Fatal("port/timeout clamps")
	}
	c = newCfg(t, map[string]string{"WHATSAPP_SESSION_BACKOFF_S": "1, 2,3"}, "")
	if !reflect.DeepEqual(c.WASessionBackoffS(), []int{1, 2, 3}) {
		t.Fatal("backoff parse")
	}
}

func TestWebSection(t *testing.T) {
	c := newCfg(t, map[string]string{"WEB_TOKEN": "envtok", "WEB_PORT": "8081", "WEB_HOST": "0.0.0.0"},
		`{"web":{"enabled":"Yes","port":99999,"host":["127.0.0.1","10.0.0.2"],"token":"filetok"}}`)
	if !c.WebEnabled() || c.WebPort() != 8081 || c.WebHost() != "0.0.0.0" || c.WebToken() != "envtok" {
		t.Fatalf("%v %v %v %v", c.WebEnabled(), c.WebPort(), c.WebHost(), c.WebToken())
	}
	c = newCfg(t, nil, `{"web":{"enabled":true,"port":8080,"host":["127.0.0.1","10.0.0.2"],"token":"filetok"}}`)
	if !c.WebEnabled() || c.WebPort() != 8080 || c.WebHost() != "127.0.0.1,10.0.0.2" || c.WebToken() != "filetok" {
		t.Fatal("file web")
	}
	c = newCfg(t, nil, `{"web":{"port":99999}}`)
	if c.WebPort() != 4242 {
		t.Fatal("port clamp")
	}
}

func TestWhatsAppSettings(t *testing.T) {
	c := newCfg(t, map[string]string{
		"WHATSAPP_SESSION_NAME": "s2", "WHATSAPP_MEDIA_DIR": "/m", "WHATSAPP_BROWSER": " Firefox ",
		"WHATSAPP_PRESENCE_SUBSCRIBE": "yes",
	}, `{"whatsapp":{"browser":"Chrome","lid_cache_ttl_days":7}}`)
	if c.WhatsAppSessionName() != "s2" || c.WhatsAppMediaDirConfigured() != "/m" ||
		c.WhatsAppBrowser() != "Firefox" || c.WALidCacheTTLDays() != 7 || !c.WhatsAppPresenceSubscribe() {
		t.Fatal("whatsapp settings")
	}
	c = newCfg(t, nil, `{"whatsapp":{"presence_subscribe":0}}`)
	if c.WhatsAppPresenceSubscribe() {
		t.Fatal("presence off")
	}
}

func TestInvalidFileIgnored(t *testing.T) {
	for _, body := range []string{`{bad`, `[1,2]`, `null`} {
		c := newCfg(t, nil, body)
		if c.AddressBookTTLS() != 300 {
			t.Fatalf("body %q", body)
		}
	}
}

func TestLegacyFlatKeysIgnoredAndReported(t *testing.T) {
	for _, s := range LegacySettings() {
		if s.Removed() {
			continue
		}
		for _, key := range s.LegacyKeys {
			file, _ := sample(s.Kind)
			c := newCfg(t, nil, `{"`+key+`":`+jsonOf(file)+`}`)
			if got := generic(c, s.Kind, s.Domain, s.Name); got != defaultOf(c, s) {
				t.Fatalf("%s: legacy key %q must be ignored, got %v", s.Path(), key, got)
			}
			entries := c.LegacyConfigEntries()
			if len(entries) != 1 || entries[0].Name != key || entries[0].Setting.Path() != s.Path() {
				t.Fatalf("%s: legacy key %q not reported: %+v", s.Path(), key, entries)
			}
		}
	}
}

// defaultOf is what the generic getter returns when nothing resolves.
func defaultOf(c *Config, s Setting) any { return generic(c, s.Kind, s.Domain, s.Name) }

func TestLegacyEnvIgnoredAndReported(t *testing.T) {
	for _, s := range LegacySettings() {
		if s.Removed() {
			continue
		}
		for _, name := range s.LegacyEnvs {
			c := newCfg(t, map[string]string{name: "legacy-value"}, "")
			if got := generic(c, s.Kind, s.Domain, s.Name); got != defaultOf(c, s) {
				t.Fatalf("%s: legacy env %s must be ignored, got %v", s.Path(), name, got)
			}
			entries := c.LegacyEnvEntries()
			if len(entries) != 1 || entries[0].Name != name {
				t.Fatalf("%s: legacy env %s not reported: %+v", s.Path(), name, entries)
			}
		}
	}
}

func TestLegacyRemovedSourcesReported(t *testing.T) {
	c := newCfg(t, map[string]string{"WAHA_API_KEY": "k", "CLIENT_WEBHOOK_PORT": "9"},
		`{"whatsapp_api_url":"http://x","whatsapp_api_key":"k2"}`)
	var keys []string
	for _, e := range c.LegacyConfigEntries() {
		keys = append(keys, e.Name)
		if !e.Setting.Removed() {
			t.Fatalf("%s should be reported as removed", e.Name)
		}
	}
	if !reflect.DeepEqual(keys, []string{"whatsapp_api_key", "whatsapp_api_url"}) {
		t.Fatalf("keys %v", keys)
	}
	var envs []string
	for _, e := range c.LegacyEnvEntries() {
		envs = append(envs, e.Name)
	}
	if !reflect.DeepEqual(envs, []string{"CLIENT_WEBHOOK_PORT", "WAHA_API_KEY"}) {
		t.Fatalf("envs %v", envs)
	}
}

func TestDotenvIsNotASettingsSource(t *testing.T) {
	c := newCfg(t, nil, "")
	if err := os.WriteFile(c.DotenvPath(), []byte("# c\nTELEGRAM_API_ID=123\nTELEGRAM_API_HASH = abc \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !c.DotenvExists() {
		t.Fatal("exists")
	}
	if c.TelegramAPIID() != 0 || c.TelegramAPIHash() != "" {
		t.Fatal(".env must never be a settings source")
	}
	keys := []string{}
	for k := range c.ReadDotenv() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"TELEGRAM_API_HASH", "TELEGRAM_API_ID"}) {
		t.Fatalf("dotenv keys %v", keys)
	}
}

func TestPaths(t *testing.T) {
	c := NewWith("/p", "/home/u", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"})
	if c.SocketPath() != "/run/user/1000/megachat/server.sock" {
		t.Fatal(c.SocketPath())
	}
	c = NewWith("/p", "/home/u", map[string]string{"XDG_RUNTIME_DIR": "rel"})
	if c.SocketPath() != "/home/u/.local/state/megachat/server.sock" {
		t.Fatal(c.SocketPath())
	}
	c = NewWith("/p", "/home/u", map[string]string{"MEGACHAT_SOCKET": "~/x.sock", "XDG_RUNTIME_DIR": "/run"})
	if c.SocketPath() != "/home/u/x.sock" || RuntimeFilePath(c.SocketPath()) != "/home/u/x.json" {
		t.Fatal(c.SocketPath())
	}
	c = NewWith("/p", "/home/u", nil)
	if c.DataDir() != "/home/u/.local/share/signal-tui-client" || c.DBFile() != c.DataDir()+"/messages.db" ||
		c.MediaDir() != c.DataDir()+"/whatsapp-media" || c.LockFile() != "/tmp/signal-tui.lock" ||
		c.TelegramSessionPath() != "/home/u/.local/share/signal-tui-client/telegram.session" ||
		c.WebThumbDir("signal") != "/home/u/.cache/signal-tui-client/web-thumbs/signal" {
		t.Fatal("default paths")
	}
	c = NewWith("/p", "/home/u", map[string]string{"XDG_DATA_HOME": "/xd", "XDG_CACHE_HOME": "/xc",
		"WHATSAPP_MEDIA_DIR": "/media", "MEGACHAT_LOCK_FILE": "/l"})
	if c.TelegramSessionPath() != "/xd/signal-tui-client/telegram.session" ||
		c.WebThumbDir("x") != "/xc/signal-tui-client/web-thumbs/x" || c.MediaDir() != "/media" ||
		c.LockFile() != "/l" || c.DataDir() != "/home/u/.local/share/signal-tui-client" {
		t.Fatal("overridden paths")
	}
}

func TestTranscription(t *testing.T) {
	c := newCfg(t, map[string]string{"TRANSCRIPTION_PROVIDER": "Gemini", "GEMINI_API_KEY": "g"},
		`{"openrouter":{"base_url":"http://x/v1/"}}`)
	if c.TranscriptionProvider() != "gemini" || !c.TranscriptionEnabled() {
		t.Fatal("provider/key")
	}
	if k, _ := c.APIKey("gemini"); k != "g" {
		t.Fatal(k)
	}
	if u, _ := c.ProviderBaseURL("openrouter"); u != "http://x/v1" {
		t.Fatal(u)
	}
	if _, err := c.APIKey("deepseek"); err == nil {
		t.Fatal("expected error")
	}
	if got := c.TranscriptionEnvOverrides(); !reflect.DeepEqual(got, []string{"provider"}) {
		t.Fatalf("%v", got)
	}
	// Nested config values.
	c = newCfg(t, nil, `{"openai":{"transcription_model":"m","base_url":"http://y/v1/"}}`)
	if m, _ := c.ProviderModel("openai"); m != "m" {
		t.Fatal(m)
	}
	if u, _ := c.ProviderBaseURL("openai"); u != "http://y/v1" {
		t.Fatal(u)
	}
	// Legacy generic env vars are reported, not read.
	c = newCfg(t, map[string]string{"TRANSCRIPTION_MODEL": "legacy"}, "")
	if m, _ := c.ProviderModel("openai"); m != "gpt-transcribe" {
		t.Fatal("legacy TRANSCRIPTION_MODEL must be ignored")
	}
	if len(c.LegacyEnvEntries()) != 1 || c.LegacyEnvEntries()[0].Name != "TRANSCRIPTION_MODEL" {
		t.Fatalf("%+v", c.LegacyEnvEntries())
	}
}

func TestAutoTranscribeAndNestedWrites(t *testing.T) {
	c := newCfg(t, nil, `{"keep":1}`)
	if c.AutoTranscribeEffective("signal", "x") {
		t.Fatal("default off")
	}
	if err := c.SetAutoTranscribeGlobal("signal", true); err != nil {
		t.Fatal(err)
	}
	if !c.AutoTranscribeEffective("signal", "x") || c.AutoTranscribeEffective("gmail", "x") {
		t.Fatal("global")
	}
	tr, _ := c.LoadFile()["transcription"].(map[string]any)
	auto, _ := tr["auto"].(map[string]any)
	if auto["signal"] != true {
		t.Fatalf("nested auto: %v", c.LoadFile())
	}
	f := false
	c.SetAutoTranscribeChat("signal", "x", &f)
	if c.AutoTranscribeEffective("signal", "x") || c.AutoTranscribeChat("signal", "x") == nil {
		t.Fatal("override")
	}
	c.SetAutoTranscribeChat("signal", "x", nil)
	if c.AutoTranscribeChat("signal", "x") != nil || !c.AutoTranscribeEffective("signal", "x") {
		t.Fatal("override removed")
	}
	if c.SetAutoTranscribeGlobal("gmail", true) == nil {
		t.Fatal("unsupported protocol")
	}
	c.SetAPIKey("openrouter", "sk")
	if k, _ := c.APIKey("openrouter"); k != "sk" {
		t.Fatal("api key")
	}
	if _, ok := c.LoadFile()["openrouter"].(map[string]any)["api_key"]; !ok {
		t.Fatalf("api key must be nested: %v", c.LoadFile())
	}
	c.SetTranscriptionSettings(map[string]any{"provider": "gemini", "openai.model": "m"})
	if c.TranscriptionProvider() != "gemini" {
		t.Fatal("provider set")
	}
	file := c.LoadFile()
	openai, _ := file["openai"].(map[string]any)
	if openai["transcription_model"] != "m" {
		t.Fatalf("model not nested: %v", file)
	}
	c.SetTranscriptionSettings(map[string]any{"openai.model": nil})
	file = c.LoadFile()
	openai, _ = file["openai"].(map[string]any)
	if _, ok := openai["transcription_model"]; ok {
		t.Fatalf("model not removed: %v", file)
	}
	if file["keep"] != float64(1) {
		t.Fatal("unrelated keys preserved")
	}
	if c.SetTranscriptionSettings(map[string]any{"bogus": 1}) == nil {
		t.Fatal("unknown setting")
	}
	if c.SetTranscriptionSettings(map[string]any{"openai.http_referer": "x"}) == nil {
		t.Fatal("setting not supported by the provider")
	}
}

func TestGmailSecrets(t *testing.T) {
	c := newCfg(t, nil, "")
	os.WriteFile(filepath.Join(c.ProjectDir, "pw"), []byte(" secret\n"), 0o600)
	c.LookupEnv = func(k string) (string, bool) {
		m := map[string]string{"GMAIL_ENABLED": "1", "GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD_FILE": "pw"}
		v, ok := m[k]
		return v, ok
	}
	if c.GmailAppPassword() != "secret" || !c.GmailEnabled() {
		t.Fatal("file secret relative to project dir")
	}
	c.LookupEnv = func(k string) (string, bool) {
		m := map[string]string{"GMAIL_APP_PASSWORD_CMD": `echo "p w"`}
		v, ok := m[k]
		return v, ok
	}
	if c.GmailAppPassword() != "p w" {
		t.Fatal(c.GmailAppPassword())
	}
	// Nested config value.
	c = newCfg(t, nil, `{"gmail":{"address":"f@b","app_password":"pw"}}`)
	if c.GmailAddress() != "f@b" || c.GmailAppPassword() != "pw" {
		t.Fatal("nested gmail")
	}
}

func TestSchemaLookups(t *testing.T) {
	if s, ok := SettingByPath("whatsapp", "lazy_media"); !ok || s.Env != "WHATSAPP_LAZY_MEDIA" {
		t.Fatalf("%+v", s)
	}
	if s, ok := SettingByEnv("WEB_TOKEN"); !ok || s.Path() != "web.token" {
		t.Fatalf("%+v", s)
	}
	if s, ok := SettingByLegacyKey("wa_lazy_media"); !ok || s.Path() != "whatsapp.lazy_media" {
		t.Fatalf("%+v", s)
	}
	if s, ok := SettingByLegacyEnv("WA_LAZY_MEDIA"); !ok || s.Path() != "whatsapp.lazy_media" {
		t.Fatalf("%+v", s)
	}
	if _, ok := SettingByLegacyKey("auto_transcribe_chats"); !ok {
		t.Fatal("auto_transcribe_chats must be a legacy key")
	}
	if s, ok := SettingByPath("transcription", "auto_chats"); !ok || s.Kind != KindMap {
		t.Fatalf("%+v", s)
	}
	// Every current env name is unique.
	seen := map[string]string{}
	for _, s := range Settings() {
		if s.Removed() || s.Env == "" {
			continue
		}
		if prev, dup := seen[s.Env]; dup {
			t.Fatalf("duplicate env %s: %s and %s", s.Env, prev, s.Path())
		}
		seen[s.Env] = s.Path()
	}
}

func TestSettingParse(t *testing.T) {
	s, _ := SettingByPath("whatsapp", "lazy_media")
	if v, err := s.Parse("off"); err != nil || v != false {
		t.Fatalf("%v %v", v, err)
	}
	s, _ = SettingByPath("gmail", "unread_poll_s")
	if v, err := s.Parse("0"); err != nil || v != float64(0) {
		t.Fatalf("%v %v", v, err)
	}
	s, _ = SettingByPath("messages", "retention_per_contact")
	if v, err := s.Parse("300"); err != nil || v != 300 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := s.Parse("abc"); err == nil {
		t.Fatal("expected error")
	}
	s, _ = SettingByPath("transcription", "auto")
	if _, err := s.Parse("true"); err == nil {
		t.Fatal("map settings have no env value")
	}
}

func TestNewUsesEnv(t *testing.T) {
	t.Setenv(ProjectDirEnv, "/proj")
	if New().ProjectDir != "/proj" {
		t.Fatal("project dir env")
	}
}
