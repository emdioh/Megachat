package media

import (
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

func TestAttachmentBlobAndConfinement(t *testing.T) {
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(root, "a.m4a"), []byte("xx"))
	writeFile(t, filepath.Join(root, "v.webm"), []byte("yy"))
	outside := filepath.Join(e.home, "secret.txt")
	writeFile(t, outside, []byte("s"))
	e.sig.AddAttachment("a.m4a", filepath.Join(root, "a.m4a"), nil)
	e.sig.AddAttachment("v.webm", filepath.Join(root, "v.webm"), nil)
	e.sig.AddAttachment("evil", outside, nil)
	e.sig.AddAttachment("trav", filepath.Join(root, "..", "..", "secret.txt"), nil)
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	e.sig.AddAttachment("link", filepath.Join(root, "link.txt"), nil)

	out, err := e.call("media.attachment", map[string]any{"protocol": "signal", "attachment_id": "a.m4a"})
	if err != nil {
		t.Fatal(err)
	}
	if out["content_type"] != "audio/mp4" || out["immutable"] != false || out["size"] != float64(2) {
		t.Fatalf("%v", out)
	}
	if v, _ := out["version"].(string); v == "" {
		t.Fatalf("no version: %v", out)
	}
	// .webm is video unless the persisted media_kind says audio
	out, _ = e.call("media.attachment", map[string]any{"protocol": "signal", "attachment_id": "v.webm"})
	if out["content_type"] != "video/webm" {
		t.Fatalf("%v", out)
	}
	e.st.mediaKind["signal|v.webm"] = "audio"
	out, _ = e.call("media.attachment", map[string]any{"protocol": "signal", "attachment_id": "v.webm"})
	if out["content_type"] != "audio/webm" {
		t.Fatalf("%v", out)
	}
	for _, id := range []string{"evil", "trav", "link", "missing"} {
		_, err = e.call("media.attachment", map[string]any{"protocol": "signal", "attachment_id": id})
		wantCode(t, err, api.CodeNotFound, "Not Found")
	}
	_, err = e.call("media.attachment", map[string]any{"protocol": "matrix", "attachment_id": "a.m4a"})
	wantCode(t, err, api.CodeNotFound, "Not Found")
	_, err = e.call("media.attachment", map[string]any{"protocol": "signal"})
	wantCode(t, err, api.CodeInvalidRequest, "Invalid request")
}

func TestThumbnailIsSmallJPEGAndCached(t *testing.T) {
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	p := filepath.Join(root, "big.png")
	os.MkdirAll(root, 0o755)
	writePNG(t, p, 400, 200)
	writeFile(t, filepath.Join(root, "doc.txt"), []byte("hello"))
	e.sig.AddAttachment("big.png", p, nil)
	e.sig.AddAttachment("doc.txt", filepath.Join(root, "doc.txt"), nil)

	out, err := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "big.png", "width": 96})
	if err != nil {
		t.Fatal(err)
	}
	if out["content_type"] != "image/jpeg" || out["immutable"] != true {
		t.Fatalf("%v", out)
	}
	tp := out["path"].(string)
	f, _ := os.Open(tp)
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 96 || cfg.Height != 48 {
		t.Fatalf("thumb %v %v", cfg, err)
	}
	out2, _ := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "big.png", "width": "96"})
	if out2["path"] != tp {
		t.Fatalf("not cached: %v vs %v", out2["path"], tp)
	}
	// unusual width: the original; non-image: the original
	out, _ = e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "big.png", "width": 100})
	if out["path"] != resolvePath(p) || out["content_type"] != "image/png" || out["immutable"] != false {
		t.Fatalf("%v", out)
	}
	out, _ = e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "doc.txt", "width": 96})
	if out["content_type"] != "text/plain" || out["immutable"] != false {
		t.Fatalf("%v", out)
	}
	// a changed source (same mtime, other size) invalidates
	st, _ := os.Stat(p)
	writePNG(t, p, 300, 300)
	_ = os.Chtimes(p, st.ModTime(), st.ModTime())
	out3, _ := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "big.png", "width": 96})
	if out3["path"] == tp {
		t.Fatalf("stale thumbnail served")
	}
	// missing
	_, err = e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "nope", "width": 96})
	wantCode(t, err, api.CodeNotFound, "Not Found")
}

func TestConcurrentThumbnailGeneratesOnce(t *testing.T) {
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	os.MkdirAll(root, 0o755)
	p := filepath.Join(root, "x.png")
	writePNG(t, p, 300, 300)
	e.sig.AddAttachment("x.png", p, nil)
	var wg sync.WaitGroup
	paths := make([]string, 8)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := e.call("media.thumbnail", map[string]any{"protocol": "signal", "attachment_id": "x.png", "width": 240})
			if err == nil {
				paths[i], _ = out["path"].(string)
			}
		}()
	}
	wg.Wait()
	for _, q := range paths {
		if q == "" || q != paths[0] {
			t.Fatalf("paths %v", paths)
		}
	}
	files, _ := filepath.Glob(filepath.Join(e.cfg.WebThumbDir("signal"), "*"))
	if len(files) != 1 {
		t.Fatalf("files %v", files)
	}
}

func TestQuoteBlob(t *testing.T) {
	e := newEnv(t)
	qd := e.cfg.QuoteThumbsDir()
	img := filepath.Join(qd, "q.png")
	os.MkdirAll(qd, 0o755)
	writePNG(t, img, 300, 100)
	other := filepath.Join(e.home, "other.png")
	writePNG(t, other, 10, 10)
	nested := filepath.Join(qd, "sub", "n.png")
	os.MkdirAll(filepath.Dir(nested), 0o755)
	writePNG(t, nested, 10, 10)
	e.st.quotePath[1] = img
	e.st.quotePath[2] = other
	e.st.quotePath[3] = nested
	e.st.quotePath[4] = filepath.Join(qd, "gone.png")

	out, err := e.call("media.quote", map[string]any{"protocol": "signal", "message_row_id": 1})
	if err != nil || out["path"] != resolvePath(img) || out["content_type"] != "image/png" {
		t.Fatalf("%v %v", out, err)
	}
	out, err = e.call("media.quote", map[string]any{"protocol": "signal", "message_row_id": 1, "width": 96})
	if err != nil || out["content_type"] != "image/jpeg" || out["immutable"] != true {
		t.Fatalf("%v %v", out, err)
	}
	for _, row := range []int{2, 3, 4, 99} {
		_, err = e.call("media.quote", map[string]any{"protocol": "signal", "message_row_id": row})
		wantCode(t, err, api.CodeNotFound, "Not Found")
	}
	e.st.quoteErr = errors.New("boom")
	_, err = e.call("media.quote", map[string]any{"protocol": "signal", "message_row_id": 1})
	wantCode(t, err, api.CodeInternal, "Database error")
}

func TestQuotedAttachmentLookup(t *testing.T) {
	e := newEnv(t)
	e.st.byTS[1000] = "img-a"
	e.st.tsExists[2000] = true // exists but not an image
	e.st.byFilename["IMG_1303.jpg"] = "img-b"
	e.st.byMsgID["m1"] = "img-c"
	cases := []struct {
		name   string
		params map[string]any
		want   any
	}{
		{"exact ts", map[string]any{"protocol": "signal", "contact_key": "c", "quote_timestamp": 1000}, "img-a"},
		{"existing non-image never falls back", map[string]any{"protocol": "signal", "contact_key": "c", "quote_timestamp": 2000, "quote_text": "IMG_1303.jpg", "content_type": "image/jpeg"}, nil},
		{"unknown ts falls back to filename", map[string]any{"protocol": "signal", "contact_key": "c", "quote_timestamp": 3000, "quote_text": "IMG_1303.jpg — 🖼️ Immagine", "content_type": "image/jpeg"}, "img-b"},
		{"no ts, message id", map[string]any{"protocol": "telegram", "contact_key": "c", "reply_to_message_id": "m1"}, "img-c"},
		{"filename needs image content type", map[string]any{"protocol": "signal", "contact_key": "c", "quote_text": "IMG_1303.jpg", "content_type": "video/mp4"}, nil},
		{"nothing", map[string]any{"protocol": "signal", "contact_key": "c"}, nil},
	}
	for _, c := range cases {
		out, err := e.call("media.quoted_attachment", c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, present := out["attachment_id"]
		if !present || got != c.want {
			t.Errorf("%s: got %v (present=%v) want %v", c.name, got, present, c.want)
		}
	}
}

func TestAttachmentVersion(t *testing.T) {
	e := newEnv(t)
	root := e.cfg.SignalCLIAttachmentsDir()
	writeFile(t, filepath.Join(root, "f.bin"), []byte("abc"))
	writeFile(t, filepath.Join(e.home, "outside.bin"), []byte("abc"))
	rr, _ := AllowedMediaRoot(e.mgr, e.cfg, "signal")
	v := AttachmentVersion(rr, "signal", "f.bin")
	st, _ := os.Stat(filepath.Join(root, "f.bin"))
	if want := itoa(st.ModTime().UnixNano()) + "-3"; v != want {
		t.Fatalf("%q != %q", v, want)
	}
	if AttachmentVersion(rr, "signal", "f.bin?x=1") != v {
		t.Fatal("query must be ignored")
	}
	if AttachmentVersion(rr, "signal", "../../outside.bin") != "" || AttachmentVersion(rr, "signal", "missing") != "" || AttachmentVersion(rr, "matrix", "f.bin") != "" {
		t.Fatal("unexpected version")
	}
	atts := []*api.MessageAttachment{{AttachmentID: "f.bin"}, {AttachmentID: "zzz"}, nil}
	AttachMediaVersions(e.mgr, e.cfg, "signal", atts)
	if atts[0].Version != v || atts[1].Version != "" {
		t.Fatalf("%+v", atts)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

var _ image.Image
