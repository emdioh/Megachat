package media

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mp4Header(brand string) []byte {
	return append([]byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p'}, []byte(brand+"\x00\x00\x00\x00isom")...)
}

func uploadStatus(err error) int {
	var ue *UploadValidationError
	if errors.As(err, &ue) {
		return ue.Status
	}
	return 0
}

func TestValidateUploadSniffsKind(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, file, mime, kind string
		data                   []byte
	}{
		{"png", "a.png", "image/png", "image", append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)},
		{"jpeg", "a.JPG", "image/jpeg", "image", []byte("\xff\xd8\xff\xe0abc")},
		{"gif", "a.gif", "image/gif", "gif", []byte("GIF89a....")},
		{"webp", "a.webp", "image/webp", "image", []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")},
		{"mov", "a.mov", "video/quicktime", "video", mp4Header("qt  ")},
		{"m4a brand", "a.m4a", "audio/mp4", "audio", mp4Header("M4A ")},
		{"ogg", "a.ogg", "audio/ogg", "audio", []byte("OggS\x00abc")},
		{"pdf", "a.pdf", "application/pdf", "document", []byte("%PDF-1.4 x")},
		{"docx", "a.docx", "application/zip", "document", []byte("PK\x03\x04zz")},
		{"webm video", "a.webm", "video/webm", "video", append([]byte("\x1aE\xdf\xa3"), []byte("xxV_VP9xx")...)},
		{"webm audio", "a.webm", "audio/webm", "audio", append([]byte("\x1aE\xdf\xa3"), []byte("xxA_OPUSxx")...)},
	}
	for _, c := range cases {
		p := filepath.Join(dir, "up")
		os.WriteFile(p, c.data, 0o600)
		v, err := ValidateUpload(p, c.file, "")
		if err != nil || v.MimeType != c.mime || v.MediaKind != c.kind {
			t.Errorf("%s: %+v %v", c.name, v, err)
		}
	}
}

func TestValidateUploadRejections(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "up")
	os.WriteFile(p, []byte("\x89PNG\r\n\x1a\nxx"), 0o600)
	if _, err := ValidateUpload(p, "photo.jpg", ""); uploadStatus(err) != 400 {
		t.Errorf("incoherent extension: %v", err)
	}
	os.WriteFile(p, []byte("just text"), 0o600)
	if _, err := ValidateUpload(p, "a.txt", ""); uploadStatus(err) != 400 {
		t.Errorf("unknown signature: %v", err)
	}
	os.WriteFile(p, mp4Header("zzzz"), 0o600)
	if _, err := ValidateUpload(p, "a.mp4", ""); uploadStatus(err) != 400 {
		t.Errorf("unknown ftyp brand: %v", err)
	}
	// image over the 20 MiB kind limit
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxUploadBytes)...)
	os.WriteFile(p, big, 0o600)
	if _, err := ValidateUpload(p, "a.png", ""); uploadStatus(err) != 413 {
		t.Errorf("over limit: %v", err)
	}
}

func TestDeclaredAudioHintOnlyForAmbiguousContainers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "up")
	os.WriteFile(p, append([]byte("\x1aE\xdf\xa3"), make([]byte, 16)...), 0o600) // webm, no codec evidence
	v, err := ValidateUpload(p, "voice.webm", "audio/webm;codecs=opus")
	if err != nil || v.MimeType != "audio/webm" || v.MediaKind != "audio" {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = ValidateUpload(p, "clip.webm", "")
	if err != nil || v.MimeType != "video/webm" {
		t.Fatalf("%+v %v", v, err)
	}
	// hint with incoherent extension is ignored
	os.WriteFile(p, mp4Header("isom"), 0o600)
	v, err = ValidateUpload(p, "x.mp4", "audio/mp4")
	if err != nil || v.MediaKind != "video" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestMP4TrackWalk(t *testing.T) {
	hdlr := func(kind string) []byte {
		b := make([]byte, 0, 32)
		b = append(b, 0, 0, 0, 32, 'h', 'd', 'l', 'r', 0, 0, 0, 0, 0, 0, 0, 0)
		b = append(b, kind...)
		return append(b, make([]byte, 32-len(b))...)
	}
	wrap := func(typ string, inner []byte) []byte {
		n := uint32(8 + len(inner))
		return append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n), typ[0], typ[1], typ[2], typ[3]}, inner...)
	}
	trak := func(kind string) []byte { return wrap("trak", wrap("mdia", hdlr(kind))) }
	data := append(mp4Header("isom"), wrap("moov", append(trak("soun"), trak("vide")...))...)
	if got := mp4VideoTrack(data); got != "video" {
		t.Errorf("both tracks: %q", got)
	}
	data = append(mp4Header("isom"), wrap("moov", trak("soun"))...)
	if got := mp4VideoTrack(data); got != "audio" {
		t.Errorf("audio only: %q", got)
	}
	p := filepath.Join(t.TempDir(), "up")
	os.WriteFile(p, data, 0o600)
	v, err := ValidateUpload(p, "v.m4a", "")
	if err != nil || v.MimeType != "audio/mp4" {
		t.Errorf("%+v %v", v, err)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":          "photo.jpg",
		"../../etc/passwd":   "passwd",
		`C:\dir\my file.PNG`: "my file.PNG",
		"a<b>c?.png":         "a_b_c_.png",
		"  ..hidden.. ":      "hidden",
		"tab\tname.pdf":      "tab_name.pdf",
		"ｆｕｌｌ.png":           "full.png",
		"":                   "",
		"résumé (1).pdf":     "résumé (1).pdf",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 300) + ".png"
	got := SanitizeFilename(long)
	if len(got) != 255 || !strings.HasSuffix(got, ".png") {
		t.Errorf("long name: %d %q", len(got), got[len(got)-8:])
	}
}

func TestUploadGuardCutsOffOversize(t *testing.T) {
	g := &UploadGuard{DeclaredType: "", Filename: "a.png"}
	if err := g.Feed(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 1000)...)); err != nil {
		t.Fatal(err)
	}
	err := g.Feed(make([]byte, MaxUploadBytes))
	if uploadStatus(err) != 413 {
		t.Fatalf("%v", err)
	}
	g = &UploadGuard{MaxBytes: 10, Filename: "a.bin", NoMediaLimits: true}
	if g.Feed(make([]byte, 10)) != nil || uploadStatus(g.Feed([]byte{1})) != 413 {
		t.Fatal("max_bytes")
	}
	// an unclassifiable header is not an error yet
	g = &UploadGuard{Filename: "a.txt"}
	if err := g.Feed([]byte("hello")); err != nil {
		t.Fatal(err)
	}
}

func TestStageLocalFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "web-uploads")
	src := filepath.Join(root, "src.png")
	os.WriteFile(src, append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3), 0o644)

	staged, err := StageLocalFile(dir, src, StageOptions{Filename: "src.png"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(staged)
	if st.Mode().Perm() != 0o600 || !strings.HasPrefix(filepath.Base(staged), "upload-") || !strings.HasSuffix(staged, ".png") {
		t.Fatalf("%s %v", staged, st.Mode())
	}
	if b, _ := os.ReadFile(staged); !bytes.Equal(b[8:], []byte{1, 2, 3}) {
		t.Fatalf("content %v", b)
	}
	// unknown extension is not kept
	staged2, _ := StageLocalFile(dir, src, StageOptions{Filename: "weird.xyz"})
	if filepath.Ext(staged2) != "" {
		t.Fatalf("%s", staged2)
	}
	// refusals: relative, symlink, directory, FIFO-like (dir), missing
	link := filepath.Join(root, "link.png")
	os.Symlink(src, link)
	for _, bad := range []string{"relative.png", link, root, filepath.Join(root, "missing")} {
		if _, err := StageLocalFile(dir, bad, StageOptions{Filename: "x.png"}); uploadStatus(err) != 400 {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// over a cap: nothing left behind
	before, _ := os.ReadDir(dir)
	if _, err := StageLocalFile(dir, src, StageOptions{Filename: "src.png", MaxBytes: 5}); uploadStatus(err) != 413 {
		t.Errorf("cap: %v", err)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Errorf("temp file leaked: %d -> %d", len(before), len(after))
	}
}

func TestPruneUploadDirectory(t *testing.T) {
	dir := t.TempDir()
	old, fresh, target := filepath.Join(dir, "old"), filepath.Join(dir, "fresh"), filepath.Join(dir, "target")
	os.WriteFile(old, []byte("x"), 0o600)
	os.WriteFile(fresh, []byte("x"), 0o600)
	os.WriteFile(target, []byte("x"), 0o600)
	os.Symlink(target, filepath.Join(dir, "lnk"))
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(target, past, past)
	PruneUploadDirectory(dir, time.Now())
	if _, err := os.Stat(old); err == nil {
		t.Error("old not pruned")
	}
	if _, err := os.Lstat(filepath.Join(dir, "lnk")); err != nil {
		t.Error("symlink must be left alone")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh pruned")
	}
	PruneUploadDirectory(filepath.Join(dir, "nope"), time.Now()) // missing dir is fine
}
