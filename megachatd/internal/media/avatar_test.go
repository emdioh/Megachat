package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

var jpegBytes = []byte("\xff\xd8\xff\xe0JFIFfake")

// waBackend / photoBackend wrap a fake backend with the avatar capabilities.
type waBackend struct {
	proto.Backend
	url     string
	noPhoto bool
	err     error
	calls   atomic.Int32
}

func (b *waBackend) ProfilePictureURL(context.Context, string) (string, bool, error) {
	b.calls.Add(1)
	return b.url, b.noPhoto, b.err
}

type photoBackend struct {
	proto.Backend
	data  []byte
	ct    string
	calls atomic.Int32
}

func (b *photoBackend) ProfilePhoto(context.Context, string) ([]byte, string, error) {
	b.calls.Add(1)
	return b.data, b.ct, nil
}

type oneBackend map[string]proto.Backend

func (m oneBackend) Get(p string) (proto.Backend, bool) { b, ok := m[p]; return b, ok }

func avatarServer(t *testing.T, hits *atomic.Int32, status int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		w.Write(jpegBytes)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/pic.jpg"
}

func TestWhatsAppAvatarDownloadCacheAndTTL(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	wa := &waBackend{Backend: e.wa, url: avatarServer(t, &hits, 200)}
	now := time.Now()
	ac := NewAvatarCache(e.cfg)
	ac.Now = func() time.Time { return now }
	b := oneBackend{"whatsapp": wa}

	p, ct, ok := ac.ResolveFile(context.Background(), b, "whatsapp", "39123@c.us")
	if !ok || ct != "image/jpeg" {
		t.Fatal("no avatar")
	}
	if got, _ := os.ReadFile(p); string(got) != string(jpegBytes) {
		t.Fatalf("content %q", got)
	}
	// fresh: served from disk, no WAHA call, no download
	ac.ResolveFile(context.Background(), b, "whatsapp", "39123@c.us")
	if wa.calls.Load() != 1 || hits.Load() != 1 {
		t.Fatalf("calls=%d hits=%d", wa.calls.Load(), hits.Load())
	}
	// after the TTL it is fetched again
	now = now.Add(AvatarTTL + time.Hour)
	ac.ResolveFile(context.Background(), b, "whatsapp", "39123@c.us")
	if wa.calls.Load() != 2 || hits.Load() != 2 {
		t.Fatalf("calls=%d hits=%d", wa.calls.Load(), hits.Load())
	}
}

func TestWhatsAppAvatarNegativeMarker(t *testing.T) {
	e := newEnv(t)
	wa := &waBackend{Backend: e.wa, noPhoto: true}
	ac := NewAvatarCache(e.cfg)
	now := time.Now()
	ac.Now = func() time.Time { return now }
	b := oneBackend{"whatsapp": wa}
	if _, _, ok := ac.ResolveFile(context.Background(), b, "whatsapp", "x"); ok {
		t.Fatal("unexpected avatar")
	}
	if _, _, ok := ac.ResolveFile(context.Background(), b, "whatsapp", "x"); ok || wa.calls.Load() != 1 {
		t.Fatalf("marker not used: calls=%d", wa.calls.Load())
	}
	now = now.Add(NoPhotoTTL + time.Second)
	ac.ResolveFile(context.Background(), b, "whatsapp", "x")
	if wa.calls.Load() != 2 {
		t.Fatalf("marker must expire: calls=%d", wa.calls.Load())
	}
}

func TestWhatsAppAvatarErrorsDoNotPoison(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	bad := avatarServer(t, &hits, 500)
	wa := &waBackend{Backend: e.wa, url: bad}
	ac := NewAvatarCache(e.cfg)
	b := oneBackend{"whatsapp": wa}
	if _, _, ok := ac.ResolveFile(context.Background(), b, "whatsapp", "x"); ok {
		t.Fatal("download error must give no avatar")
	}
	path, _ := ac.avatarPath("whatsapp-avatars", "x")
	if isRegularFile(noPhotoMarker(path)) || isRegularFile(path) {
		t.Fatal("failure must leave no marker or file")
	}
	wa.url, wa.err = "", context.DeadlineExceeded
	ac.ResolveFile(context.Background(), b, "whatsapp", "x")
	if isRegularFile(noPhotoMarker(path)) {
		t.Fatal("lookup error must not write the marker")
	}
}

func TestConcurrentAvatarFetchesOnce(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	wa := &waBackend{Backend: e.wa, url: avatarServer(t, &hits, 200)}
	ac := NewAvatarCache(e.cfg)
	b := oneBackend{"whatsapp": wa}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ac.ResolveFile(context.Background(), b, "whatsapp", "same") }()
	}
	wg.Wait()
	if wa.calls.Load() != 1 || hits.Load() != 1 {
		t.Fatalf("calls=%d hits=%d", wa.calls.Load(), hits.Load())
	}
}

func TestSignalAvatarIsContentAddressedBlob(t *testing.T) {
	e := newEnv(t)
	png := []byte("\x89PNG\r\n\x1a\nxyz")
	sig := &photoBackend{Backend: e.sig, data: png, ct: "image/png"}
	e.deps.Backends = oneBackend{"signal": sig}
	e.reg = nil
	d := e.deps
	d.Avatars = NewAvatarCache(e.cfg)
	blob, err := d.AvatarBlob(context.Background(), "signal", "+391")
	if err != nil {
		t.Fatal(err)
	}
	if *blob.ContentType != "image/png" || filepath.Ext(blob.Path) != ".img" || filepath.Dir(blob.Path) != e.cfg.AvatarBlobsDir("signal") {
		t.Fatalf("%+v", blob)
	}
	blob2, _ := d.AvatarBlob(context.Background(), "signal", "+392")
	if blob2.Path != blob.Path {
		t.Fatal("same bytes must share one blob")
	}
}

func TestTelegramAvatarCacheAndNegativeMarker(t *testing.T) {
	e := newEnv(t)
	tg := &photoBackend{Backend: e.sig, data: []byte("\x89PNG\r\n\x1a\nxyz")}
	now := time.Now()
	ac := NewAvatarCache(e.cfg)
	ac.Now = func() time.Time { return now }
	b := oneBackend{"telegram": tg}
	p, ct, ok := ac.ResolveFile(context.Background(), b, "telegram", "42")
	if !ok || ct != "image/png" || filepath.Dir(p) != e.cfg.AvatarDir("telegram-avatars") {
		t.Fatalf("%q %q %v", p, ct, ok)
	}
	ac.ResolveFile(context.Background(), b, "telegram", "42")
	if tg.calls.Load() != 1 {
		t.Fatalf("cache hit expected, calls=%d", tg.calls.Load())
	}
	now = now.Add(TelegramAvatarTTL + time.Minute)
	ac.ResolveFile(context.Background(), b, "telegram", "42")
	if tg.calls.Load() != 2 {
		t.Fatalf("ttl refresh expected, calls=%d", tg.calls.Load())
	}
	none := &photoBackend{Backend: e.sig}
	ac = NewAvatarCache(e.cfg)
	b2 := oneBackend{"telegram": none}
	for i := 0; i < 3; i++ {
		if _, _, ok := ac.ResolveFile(context.Background(), b2, "telegram", "nopic"); ok {
			t.Fatal("unexpected avatar")
		}
	}
	if none.calls.Load() != 1 {
		t.Fatalf("negative marker not used, calls=%d", none.calls.Load())
	}
}

func TestContactsAvatarOp(t *testing.T) {
	e := newEnv(t)
	_, err := e.call("contacts.avatar", map[string]any{"protocol": "nope", "contact_id": "x"})
	wantCode(t, err, api.CodeNotFound, "Not Found")
	_, err = e.call("contacts.avatar", map[string]any{"protocol": "signal", "contact_id": "  "})
	wantCode(t, err, api.CodeInvalidRequest, "Invalid request")
	_, err = e.call("contacts.avatar", map[string]any{"protocol": "signal", "contact_id": "x"})
	wantCode(t, err, api.CodeNotFound, "No profile picture")
}
