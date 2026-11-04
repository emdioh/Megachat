package media

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var (
	_ Store    = (*store.Store)(nil)
	_ Config   = (*config.Config)(nil)
	_ Backends = (*proto.Manager)(nil)
)

type stubStore struct {
	contentType map[string]string // proto|id
	mediaKind   map[string]string
	quotePath   map[int64]string
	quoteErr    error
	// quoted lookups
	byTS       map[int64]string
	tsExists   map[int64]bool
	byMsgID    map[string]string
	byFilename map[string]string
}

func (s *stubStore) AttachmentContentType(_ context.Context, p, id string) (*string, error) {
	if v, ok := s.contentType[p+"|"+id]; ok {
		return &v, nil
	}
	return nil, nil
}
func (s *stubStore) AttachmentMediaKind(_ context.Context, p, id string) (*string, error) {
	if v, ok := s.mediaKind[p+"|"+id]; ok {
		return &v, nil
	}
	return nil, nil
}
func (s *stubStore) QuoteAttachmentPath(_ context.Context, _ string, row int64) (*string, error) {
	if s.quoteErr != nil {
		return nil, s.quoteErr
	}
	if v, ok := s.quotePath[row]; ok {
		return &v, nil
	}
	return nil, nil
}
func (s *stubStore) QuotedMediaAttachmentID(_ context.Context, _, _ string, ts int64, _ store.MediaFilter) (string, error) {
	return s.byTS[ts], nil
}
func (s *stubStore) MessageExistsAtTimestamp(_ context.Context, _, _ string, ts int64) (bool, error) {
	return s.tsExists[ts], nil
}
func (s *stubStore) QuotedMediaByMessageID(_ context.Context, _, _, id string, _ store.MediaFilter) (string, error) {
	return s.byMsgID[id], nil
}
func (s *stubStore) QuotedImageByFilename(_ context.Context, _, _, name string) (string, error) {
	return s.byFilename[name], nil
}

type env struct {
	t    *testing.T
	home string
	cfg  *config.Config
	st   *stubStore
	mgr  *proto.Manager
	sig  *fake.Backend
	wa   *fake.Backend
	deps Deps
	reg  *ops.Registry
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	cfg := config.NewWith(filepath.Join(home, "project"), home, map[string]string{"XDG_CACHE_HOME": filepath.Join(home, "cache")})
	e := &env{t: t, home: home, cfg: cfg, st: &stubStore{
		contentType: map[string]string{}, mediaKind: map[string]string{}, quotePath: map[int64]string{},
		byTS: map[int64]string{}, tsExists: map[int64]bool{}, byMsgID: map[string]string{}, byFilename: map[string]string{},
	}}
	e.mgr = proto.New()
	e.sig, e.wa = fake.New("signal"), fake.New("whatsapp")
	for _, b := range []proto.Backend{e.sig, e.wa, fake.New("telegram")} {
		if err := e.mgr.Register(b); err != nil {
			t.Fatal(err)
		}
	}
	e.deps = Deps{Store: e.st, Backends: e.mgr, Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	e.reg = ops.NewRegistry()
	RegisterOps(e.reg, e.deps)
	return e
}

func (e *env) call(name string, params any) (map[string]any, error) {
	e.t.Helper()
	raw, _ := json.Marshal(params)
	res, err := e.reg.Call(context.Background(), name, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m, nil
}

func wantCode(t *testing.T, err error, code, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s %q, got success", code, msg)
	}
	oe := ops.AsError(err)
	if oe.Code != code || oe.Message != msg {
		t.Fatalf("got %s %q, want %s %q", oe.Code, oe.Message, code, msg)
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

var _ = api.CodeNotFound
