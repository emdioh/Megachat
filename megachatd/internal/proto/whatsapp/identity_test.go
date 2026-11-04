package whatsapp

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waWa6"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/util/keys"
)

// registrationPayload builds the payload WhatsApp receives when this device
// registers (no JID yet), reading the process-wide props the client sends.
func registrationPayload() *waWa6.ClientPayload {
	idKey := keys.NewKeyPair()
	dev := &wastore.Device{
		NoiseKey:       keys.NewKeyPair(),
		IdentityKey:    idKey,
		SignedPreKey:   idKey.CreateSignedPreKey(1),
		RegistrationID: 1234,
	}
	return dev.GetClientPayload()
}

// checkIdentity asserts the fields WhatsApp uses to label the linked device
// (DeviceProps os + platform type) and to build the QR client type, plus the
// browser strings carried by the WebInfo payload.
func checkIdentity(t *testing.T, id browserIdentity) {
	t.Helper()
	applyBrowserIdentity(id)
	payload := registrationPayload()

	web := payload.GetWebInfo()
	if got := web.GetBrowser(); got != id.Browser {
		t.Errorf("web info browser = %q, want %q", got, id.Browser)
	}
	if got := web.GetBrowserVersion(); got != id.Version {
		t.Errorf("web info browser version = %q, want %q", got, id.Version)
	}
	var props waCompanionReg.DeviceProps
	if err := proto.Unmarshal(payload.GetDevicePairingData().GetDeviceProps(), &props); err != nil {
		t.Fatalf("unmarshal device props: %v", err)
	}
	if got := props.GetOs(); got != id.OS {
		t.Errorf("device props os = %q, want %q", got, id.OS)
	}
	if got, want := props.GetPlatformType(), browserPlatforms[strings.ToLower(id.Browser)]; got != want {
		t.Errorf("device props platform type = %v, want %v", got, want)
	}
}

// TestBrowserIdentityDefaults checks the built-in identity: a recent desktop
// Chrome rather than whatsmeow (Os "whatsmeow", PlatformType UNKNOWN).
func TestBrowserIdentityDefaults(t *testing.T) {
	cfg := config.NewWith(t.TempDir(), t.TempDir(), map[string]string{})
	checkIdentity(t, resolveBrowserIdentity(cfg, quietLogger()))
}

// TestBrowserIdentityOverrides checks config.json can replace the browser,
// its version and the OS reported to WhatsApp.
func TestBrowserIdentityOverrides(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, map[string]string{})
	if err := cfg.UpdateFile(func(m map[string]any) {
		m["whatsapp_browser"] = "Firefox"
		m["whatsapp_browser_version"] = "155.0"
		m["whatsapp_browser_os"] = "Windows"
	}); err != nil {
		t.Fatal(err)
	}
	id := resolveBrowserIdentity(cfg, quietLogger())
	if id != (browserIdentity{OS: "Windows", Browser: "Firefox", Version: "155.0"}) {
		t.Fatalf("resolved identity = %+v", id)
	}
	checkIdentity(t, id)
}

// TestBrowserIdentityUnknownBrowser checks that a browser WhatsApp does not
// list is reported and ignored instead of being sent verbatim.
func TestBrowserIdentityUnknownBrowser(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, map[string]string{})
	if err := cfg.UpdateFile(func(m map[string]any) {
		m["whatsapp_browser"] = "Brave"
	}); err != nil {
		t.Fatal(err)
	}
	id := resolveBrowserIdentity(cfg, quietLogger())
	if id.Browser != defaultBrowserIdentity.Browser {
		t.Fatalf("browser = %q, want default %q", id.Browser, defaultBrowserIdentity.Browser)
	}
}
