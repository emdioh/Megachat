package signal

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A WebP quote thumbnail is now really decoded: Pillow-made WebP (lossy and
// lossless) is accepted, truncated/garbage WebP behind a valid header is not.
func TestQuoteThumbnailWebPDecodes(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	script := `
import sys
from PIL import Image
im = Image.new("RGB", (64, 48), (200, 30, 90))
im.save(sys.argv[1] + "/lossy.webp", "WEBP", quality=70)
im.save(sys.argv[1] + "/lossless.webp", "WEBP", lossless=True)
`
	if err := exec.Command(py, "-c", script, dir).Run(); err != nil {
		t.Skipf("Pillow unavailable: %v", err)
	}
	for _, name := range []string{"lossy.webp", "lossless.webp"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		q := map[string]any{"attachments": []any{map[string]any{"thumbnail": base64.StdEncoding.EncodeToString(raw)}}}
		p := extractQuoteThumbnail(q, filepath.Join(dir, "qt"))
		if p == "" || !strings.HasSuffix(p, ".webp") {
			t.Errorf("%s: path %q", name, p)
		}
		// Truncated payload: a header-only check would have accepted it.
		q = map[string]any{"attachments": []any{map[string]any{"thumbnail": base64.StdEncoding.EncodeToString(raw[:len(raw)/2])}}}
		if p := extractQuoteThumbnail(q, filepath.Join(dir, "qt2")); p != "" {
			t.Errorf("%s truncated accepted: %s", name, p)
		}
	}
	bad := []byte("RIFF\x10\x00\x00\x00WEBPVP8 garbagegarbage")
	q := map[string]any{"attachments": []any{map[string]any{"thumbnail": base64.StdEncoding.EncodeToString(bad)}}}
	if p := extractQuoteThumbnail(q, filepath.Join(dir, "qt3")); p != "" {
		t.Errorf("garbage WebP accepted: %s", p)
	}
}
