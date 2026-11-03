package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestDefaults(t *testing.T) {
	c := newCfg(t, nil, "")
	if c.WebPort() != 4242 || c.WebHost() != "127.0.0.1" || c.WebEnabled() || c.WebToken() != "" {
		t.Fatal("web defaults")
	}
	if c.WhatsAppSessionName() != "default" || c.WhatsAppAPIPort() != 3005 ||
		c.ResolveWhatsAppAPIURL() != "http://127.0.0.1:3005" || c.WhatsAppWebhookPort() != 8088 {
		t.Fatal("whatsapp defaults")
	}
	if c.WhatsAppWebhookURL() != "http://host.docker.internal:8088/webhook" {
		t.Fatal(c.WhatsAppWebhookURL())
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
	if c.ImageProtocol() != "auto" || c.ThumbnailMaxLines() != 12 || c.ThumbnailMaxCols() != 60 {
		t.Fatal("image defaults")
	}
	if c.TranscriptionProvider() != "openai" || c.TranscriptionTimeoutS() != 120 || c.TranscriptionEnabled() {
		t.Fatal("transcription defaults")
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

func TestPrecedenceEnvOverFile(t *testing.T) {
	c := newCfg(t, map[string]string{"ADDRESS_BOOK_TTL_S": "10", "PICKER_PREFERRED_BACKEND": " signal "},
		`{"address_book_ttl_s": 99, "picker_preferred_backend":"x", "picker_max_results": 7.9, "wa_lazy_media": "off"}`)
	if c.AddressBookTTLS() != 10 || c.PickerPreferredBackend() != "signal" {
		t.Fatal("env should win")
	}
	if c.PickerMaxResults() != 7 || c.WALazyMedia() {
		t.Fatal("file values")
	}
	// invalid env int -> default (not file value), like Python
	c = newCfg(t, map[string]string{"ADDRESS_BOOK_TTL_S": "abc"}, `{"address_book_ttl_s": 99}`)
	if c.AddressBookTTLS() != 300 {
		t.Fatal(c.AddressBookTTLS())
	}
}

func TestBoolSemantics(t *testing.T) {
	c := newCfg(t, map[string]string{"WA_LAZY_MEDIA": "garbage"}, `{"wa_lazy_media": false}`)
	if !c.WALazyMedia() { // unrecognised -> default
		t.Fatal("garbage should give default true")
	}
	c = newCfg(t, map[string]string{"WA_LAZY_MEDIA": "  "}, `{"wa_lazy_media": false}`)
	if c.WALazyMedia() { // blank env falls through to file
		t.Fatal("blank env should fall to file")
	}
	c = newCfg(t, map[string]string{"WA_SESSION_GUARD": "No"}, "")
	if c.WASessionGuard() {
		t.Fatal("No -> false")
	}
}

func TestClamps(t *testing.T) {
	c := newCfg(t, map[string]string{
		"WA_RESYNC_MAX_CHATS": "-1", "WA_RESYNC_LIMIT": "0", "WA_SESSION_FAILED_GRACE_S": "0",
		"WA_SESSION_BACKOFF_S": "1, 2,x", "GMAIL_IMAP_PORT": "70000", "TRANSCRIPTION_TIMEOUT": "-5",
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
	c = newCfg(t, map[string]string{"WA_SESSION_BACKOFF_S": "1, 2,3"}, "")
	if !reflect.DeepEqual(c.WASessionBackoffS(), []int{1, 2, 3}) {
		t.Fatal("backoff parse")
	}
}

func TestWebSection(t *testing.T) {
	c := newCfg(t, map[string]string{"SIGNAL_TUI_WEB_TOKEN": "envtok"},
		`{"web":{"enabled":"Yes","port":99999,"host":["127.0.0.1","10.0.0.2"],"token":"filetok"}}`)
	if !c.WebEnabled() || c.WebPort() != 4242 || c.WebHost() != "127.0.0.1,10.0.0.2" || c.WebToken() != "envtok" {
		t.Fatalf("%v %v %v %v", c.WebEnabled(), c.WebPort(), c.WebHost(), c.WebToken())
	}
	c = newCfg(t, nil, `{"web":{"enabled":true,"port":8080,"token":"filetok"}}`)
	if !c.WebEnabled() || c.WebPort() != 8080 || c.WebToken() != "filetok" {
		t.Fatal("file web")
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

func TestSignalAndTelegramAndDotenv(t *testing.T) {
	c := newCfg(t, nil, `{"user_number":"+391"}`)
	if !c.SignalEnabled() || c.SignalUserNumber() != "+391" {
		t.Fatal("signal via number")
	}
	c = newCfg(t, map[string]string{"SIGNAL_ENABLED": "0"}, `{"user_number":"+391"}`)
	if c.SignalEnabled() {
		t.Fatal("explicit off")
	}
	c = newCfg(t, nil, "")
	os.WriteFile(filepath.Join(c.ProjectDir, ".env"),
		[]byte("# c\nTELEGRAM_API_ID=123\nTELEGRAM_API_HASH = abc \nWAHA_API_KEY=k1\nGMAIL_ADDRESS=a@b\n"), 0o644)
	if c.TelegramAPIID() != 123 || c.TelegramAPIHash() != "abc" || !c.TelegramEnabled() {
		t.Fatal("telegram from .env")
	}
	if c.WhatsAppAPIKey() != "k1" || c.GmailAddress() != "a@b" {
		t.Fatal("dotenv fallbacks")
	}
	// retention: file key beats .env
	os.WriteFile(filepath.Join(c.ProjectDir, ".env"), []byte("MESSAGE_RETENTION_PER_CONTACT=5\n"), 0o644)
	if c.MessageRetentionPerContact() != 5 {
		t.Fatal("retention dotenv")
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
	c := newCfg(t, map[string]string{"AI_PROVIDER": "Gemini", "GOOGLE_API_KEY": "g"}, `{"openrouter_base_url":"http://x/v1/"}`)
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
		t.Fatal(got)
	}
	// legacy key fallback for openai model
	c = newCfg(t, nil, `{"transcription_model":"legacy"}`)
	if m, _ := c.ProviderModel("openai"); m != "legacy" {
		t.Fatal(m)
	}
}

func TestAutoTranscribeAndWrites(t *testing.T) {
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
	c.SetTranscriptionSettings(map[string]any{"provider": "gemini", "openai.model": "m"})
	if c.TranscriptionProvider() != "gemini" {
		t.Fatal("provider set")
	}
	c.SetTranscriptionSettings(map[string]any{"openai.model": nil})
	if c.LoadFile()["keep"] != float64(1) {
		t.Fatal("unrelated keys preserved")
	}
	if c.SetTranscriptionSettings(map[string]any{"bogus": 1}) == nil {
		t.Fatal("unknown setting")
	}
}

func TestGmailSecrets(t *testing.T) {
	c := newCfg(t, nil, "")
	os.WriteFile(filepath.Join(c.ProjectDir, "pw"), []byte(" secret\n"), 0o600)
	c.LookupEnv = func(k string) (string, bool) {
		m := map[string]string{"GMAIL_ADDRESS": "a@b", "GMAIL_APP_PASSWORD_FILE": "pw"}
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
}

func TestNewUsesEnv(t *testing.T) {
	t.Setenv(ProjectDirEnv, "/proj")
	if New().ProjectDir != "/proj" {
		t.Fatal("project dir env")
	}
}
