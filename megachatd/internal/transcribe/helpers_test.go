package transcribe

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// memStore is an in-memory Store.
type memStore struct {
	mu   sync.Mutex
	rows map[string]store.Transcription
	seq  float64
	fail error
}

func newMemStore() *memStore { return &memStore{rows: map[string]store.Transcription{}} }

func (m *memStore) GetTranscription(_ context.Context, p, id string) (*store.Transcription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	t, ok := m.rows[p+"|"+id]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (m *memStore) SetTranscription(_ context.Context, t store.Transcription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	t.UpdatedAt = 1700000000 + m.seq
	m.rows[t.Protocol+"|"+t.AttachmentID] = t
	return nil
}

func (m *memStore) RecentTranscriptions(_ context.Context, limit int) ([]store.Transcription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	var out []store.Transcription
	for _, t := range m.rows {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeMedia struct {
	path string
	err  error
}

func (f fakeMedia) AttachmentBlob(context.Context, string, string) (api.BlobOut, error) {
	return api.BlobOut{Path: f.path}, f.err
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// makeAudio renders a short sine with ffmpeg into dir/name (format by extension).
func makeAudio(t *testing.T, dir, name string, extra ...string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(dir, name)
	args := append([]string{"-y", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=0.3"}, extra...)
	args = append(args, path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	return path
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type upload struct {
	Path     string
	Auth     string
	Header   http.Header
	Fields   map[string]string
	Filename string
	FileType string
	Size     int
}

// fakeAPI is an OpenAI-compatible transcription server. handler may override
// the answer; the default is {"text":"  testo trascritto  "}.
type fakeAPI struct {
	*httptest.Server
	mu      sync.Mutex
	uploads []upload
	handler func(n int, u upload, w http.ResponseWriter)
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := upload{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Header: r.Header.Clone(),
			Fields: map[string]string{}}
		if mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && mt == "multipart/form-data" {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				if p.FormName() == "file" {
					u.Filename, u.FileType, u.Size = p.FileName(), p.Header.Get("Content-Type"), len(b)
				} else {
					u.Fields[p.FormName()] = string(b)
				}
			}
		} else {
			b, _ := io.ReadAll(r.Body)
			u.Fields["body"] = string(b)
		}
		f.mu.Lock()
		f.uploads = append(f.uploads, u)
		n := len(f.uploads)
		h := f.handler
		f.mu.Unlock()
		if h != nil {
			h(n, u, w)
			return
		}
		_, _ = w.Write([]byte(`{"text":"  testo trascritto  "}`))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) all() []upload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upload(nil), f.uploads...)
}

// newConfig is a real config.Config in a temp dir with an OpenAI key and the
// OpenAI base URL pointing at base.
func newConfig(t *testing.T, base string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewWith(dir, dir, nil)
	if base != "" {
		if err := cfg.SetAPIKey("openai", "sk-test-key-123456"); err != nil {
			t.Fatal(err)
		}
		if err := cfg.SetTranscriptionSettings(map[string]any{"openai.base_url": base}); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func mustJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}
