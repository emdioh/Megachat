package media

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

func mkBox(typ string, payload int) []byte {
	n := 8 + payload
	return append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n), typ[0], typ[1], typ[2], typ[3]}, make([]byte, payload)...)
}

func TestMP4HasMoov(t *testing.T) {
	faststart := append(append(mkBox("ftyp", 12), mkBox("moov", 20)...), mkBox("mdat", 100)...)
	if !mp4HasMoov(faststart) {
		t.Error("moov in front must be found")
	}
	tail := append(mkBox("ftyp", 12), mkBox("mdat", 1000)...)
	if mp4HasMoov(tail[:200]) {
		t.Error("truncated mdat, no moov")
	}
	if mp4HasMoov(nil) || mp4HasMoov([]byte("short")) {
		t.Error("garbage")
	}
	// 64-bit size box
	big := append([]byte{0, 0, 0, 1, 'm', 'd', 'a', 't', 0, 0, 0, 0, 0, 0, 0, 20}, make([]byte, 4)...)
	big = append(big, mkBox("moov", 0)...)
	if !mp4HasMoov(big) {
		t.Error("extended size")
	}
}

func TestVideoCandidate(t *testing.T) {
	e := newEnv(t)
	d := &e.deps
	ctx := context.Background()
	e.st.contentType["whatsapp|a"] = "audio/ogg"
	e.st.contentType["whatsapp|v"] = "video/mp4"
	e.st.mediaKind["signal|voice.webm"] = "audio"
	cases := []struct {
		path, proto, id string
		want            bool
	}{
		{"x.mp4", "whatsapp", "none", true},
		{"x.mov", "whatsapp", "none", true},
		{"x.bin", "whatsapp", "v", true},
		{"x.mp4", "whatsapp", "a", false},
		{"voice.webm", "signal", "voice.webm", false},
		{"clip.webm", "signal", "clip.webm", true},
		{"x.png", "signal", "x.png", false},
	}
	for _, c := range cases {
		if got := d.isVideoCandidate(ctx, c.path, c.proto, c.id); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func makeVideo(t *testing.T, path string, faststart bool) {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=5:duration=1", "-pix_fmt", "yuv420p", "-c:v", "mpeg4"}
	if faststart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-y", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
}

func skipWithoutFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

func TestVideoThumbnailViaFFmpeg(t *testing.T) {
	skipWithoutFFmpeg(t)
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	os.MkdirAll(root, 0o755)
	vid := filepath.Join(root, "clip.mp4")
	makeVideo(t, vid, false)
	e.sig.AddAttachment("clip.mp4", vid, nil)
	e.st.contentType["signal|clip.mp4"] = "video/mp4"

	out, err := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "clip.mp4", "width": 96})
	if err != nil {
		t.Fatal(err)
	}
	if out["content_type"] != "image/jpeg" || out["immutable"] != true {
		t.Fatalf("%v", out)
	}
	f, _ := os.Open(out["path"].(string))
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 96 || cfg.Height != 54 {
		t.Fatalf("%+v %v", cfg, err)
	}
	out2, _ := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "clip.mp4", "width": 96})
	if out2["path"] != out["path"] {
		t.Fatal("not cached")
	}
	// a file that is not a video: unprocessable
	bad := filepath.Join(root, "bad.mp4")
	os.WriteFile(bad, []byte("not a video"), 0o644)
	e.sig.AddAttachment("bad.mp4", bad, nil)
	_, err = e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "bad.mp4", "width": 96})
	wantCode(t, err, api.CodeUnprocessable, "Video thumbnail unavailable")
}

func TestVideoThumbnailWithoutFFmpegIsUnprocessable(t *testing.T) {
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	os.MkdirAll(root, 0o755)
	vid := filepath.Join(root, "clip.mp4")
	os.WriteFile(vid, []byte("whatever"), 0o644)
	e.sig.AddAttachment("clip.mp4", vid, nil)
	saved := ffmpegPath
	_ = saved
	// force "unavailable" for this run only
	old := ffmpegExecutable
	defer func() { ffmpegExecutable = old }()
	ffmpegPath() // initialise the Once
	ffmpegExecutable = ""
	_, err := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "clip.mp4", "width": 96})
	wantCode(t, err, api.CodeUnprocessable, "Video thumbnail unavailable")
}

func TestChunkPipelineCachesByStableIdentity(t *testing.T) {
	skipWithoutFFmpeg(t)
	e := newEnv(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "v.mp4")
	makeVideo(t, full, false) // moov at the end
	data, _ := os.ReadFile(full)
	e.wa.AddAttachment("wa-video", filepath.Join(dir, "never-downloaded.mp4"), data)
	// the resolver knows the id, but the file is absent: only chunks can serve it
	e.st.contentType["whatsapp|wa-video"] = "video/mp4"

	out, err := e.call("media.thumbnail", map[string]any{"protocol": "whatsapp", "attachment_id": "wa-video", "width": 240})
	if err != nil {
		t.Fatal(err)
	}
	if out["content_type"] != "image/jpeg" || out["immutable"] != true {
		t.Fatalf("%v", out)
	}
	// the whole (small) file fits in the head chunk, moov included
	if n := len(e.wa.CallsTo("AttachmentChunk")); n != 1 {
		t.Fatalf("head only expected, got %d chunk calls", n)
	}
	// second request: stable cache identity, no new chunk fetch
	out2, _ := e.call("media.thumbnail", map[string]any{"protocol": "whatsapp", "attachment_id": "wa-video", "width": 240})
	if out2["path"] != out["path"] || len(e.wa.CallsTo("AttachmentChunk")) != 1 {
		t.Fatal("cache miss on the second request")
	}
}

func TestChunkPipelineFastStartUsesOnlyHead(t *testing.T) {
	skipWithoutFFmpeg(t)
	e := newEnv(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "v.mp4")
	makeVideo(t, full, true)
	data, _ := os.ReadFile(full)
	if !bytes.Equal(data[4:8], []byte("ftyp")) || !mp4HasMoov(data[:min(len(data), headBytes)]) {
		t.Skip("encoder did not produce a faststart file")
	}
	e.wa.AddAttachment("wa-fast", filepath.Join(dir, "absent.mp4"), data)
	e.st.contentType["whatsapp|wa-fast"] = "video/mp4"
	if _, err := e.call("media.thumbnail", map[string]any{"protocol": "whatsapp", "attachment_id": "wa-fast", "width": 96}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.wa.CallsTo("AttachmentChunk")); n != 1 {
		t.Fatalf("faststart needs only the head, got %d", n)
	}
}

func TestInsufficientChunkWithoutFileIsUnprocessable(t *testing.T) {
	e := newEnv(t)
	e.wa.AddAttachment("wa-bad", filepath.Join(t.TempDir(), "absent.mp4"), []byte("garbage that is no video at all"))
	e.st.contentType["whatsapp|wa-bad"] = "video/mp4"
	_, err := e.call("media.thumbnail", map[string]any{"protocol": "whatsapp", "attachment_id": "wa-bad", "width": 96})
	wantCode(t, err, api.CodeUnprocessable, "Video thumbnail unavailable")
	// not an MP4 with moov in the head: the tail is requested too
	if n := len(e.wa.CallsTo("AttachmentChunk")); n != 2 {
		t.Fatalf("head+tail expected, got %d", n)
	}
	// without any chunk support and without the file: plain 404
	_, err = e.call("media.thumbnail", map[string]any{"protocol": "whatsapp", "attachment_id": "unknown.mp4", "width": 96})
	wantCode(t, err, api.CodeNotFound, "Not Found")
}

type fakeDownloader struct{ files, texts []string }

func (f *fakeDownloader) ServeFilePath(p string) (string, error) {
	f.files = append(f.files, p)
	return "http://host:10042/" + filepath.Base(p), nil
}
func (f *fakeDownloader) ServeTextAsFile(text, name string) (string, error) {
	f.texts = append(f.texts, name)
	if name == "boom.txt" {
		return "ERROR: disk full", nil
	}
	return "http://host:10042/" + name, nil
}

func TestDownloadOps(t *testing.T) {
	e := newEnv(t)
	dl := &fakeDownloader{}
	d := e.deps
	d.Downloader = dl
	reg := newRegistry(d)
	root := e.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(root, "f.pdf"), []byte("x"))
	e.sig.AddAttachment("f.pdf", filepath.Join(root, "f.pdf"), nil)
	out := callRaw(t, reg, "download.serve_attachment", map[string]any{"protocol": "signal", "attachment_id": "f.pdf"})
	if out["url"] != "http://host:10042/f.pdf" {
		t.Fatalf("%v", out)
	}
	call := func(name string, p any) error {
		_, err := reg.Call(context.Background(), name, mustJSON(p))
		return err
	}
	wantCode(t, call("download.serve_attachment", map[string]any{"protocol": "signal", "attachment_id": "nope"}),
		api.CodeNotFound, "ERROR: Attachment file not found (id=nope)")
	wantCode(t, call("download.serve_attachment", map[string]any{"protocol": "xx", "attachment_id": "nope"}),
		api.CodeNotFound, "ERROR: Attachment file not found (id=nope)")
	if out := callRaw(t, reg, "download.serve_text", map[string]any{"text": "hi", "filename": "a.txt"}); out["url"] != "http://host:10042/a.txt" {
		t.Fatalf("%v", out)
	}
	if out := callRaw(t, reg, "download.serve_text", map[string]any{"text": "hi"}); out["url"] != "http://host:10042/message.txt" {
		t.Fatalf("default filename: %v", out)
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x"} {
		wantCode(t, call("download.serve_text", map[string]any{"text": "hi", "filename": bad}), api.CodeInvalidRequest, "Invalid request")
	}
	wantCode(t, call("download.serve_text", map[string]any{"text": "hi", "filename": "boom.txt"}), api.CodeBackendError, "ERROR: disk full")
	// not registered without a downloader
	if e.reg.Has("download.serve_text") {
		t.Fatal("download ops registered without a Downloader")
	}
}
