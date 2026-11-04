package media

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pillowScript writes a set of images in several formats and, for each, the
// thumbnail dimensions Pillow produces exactly as server/thumbnails.py does
// (convert RGB, thumbnail BILINEAR, JPEG quality 78).
const pillowScript = `
import sys, json
from PIL import Image
out = sys.argv[1]
res = {}
def gradient(w, h, mode="RGB"):
    im = Image.new(mode, (w, h))
    px = im.load()
    for y in range(h):
        for x in range(w):
            v = (x * 255 // max(w - 1, 1), y * 255 // max(h - 1, 1), 128)
            px[x, y] = v + ((200,) if mode == "RGBA" else ())
    return im
cases = {
    "a.webp": (gradient(640, 400), "WEBP"),
    "b.webp": (gradient(300, 700, "RGBA"), "WEBP"),
    "c.bmp": (gradient(500, 500), "BMP"),
    "d.tiff": (gradient(333, 111), "TIFF"),
    "e.png": (gradient(1000, 10, "RGBA"), "PNG"),
    "f.webp": (gradient(50, 40), "WEBP"),
}
for name, (im, fmt) in cases.items():
    p = out + "/" + name
    im.save(p, fmt)
    for width in (96, 240, 480):
        with Image.open(p) as image:
            assert image.format == fmt
            with image.convert("RGB") as c:
                c.thumbnail((width, width), Image.BILINEAR)
                res[name + "|%d" % width] = [c.size[0], c.size[1]]
print(json.dumps(res))
`

func TestThumbnailFormatsMatchPillow(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	out, err := exec.Command(py, "-c", pillowScript, dir).Output()
	if err != nil {
		t.Skipf("Pillow unavailable: %v", err)
	}
	var want map[string][2]int
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for key, dims := range want {
		name, w, _ := strings.Cut(key, "|")
		width, _ := strconv.Atoi(w)
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: decode: %v", name, err)
			continue
		}
		thumb := filepath.Join(dir, "t.jpg")
		if err := writeJPEGThumb(img, width, thumb); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(thumb)
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "jpeg" {
			t.Fatalf("%s: output is %q (%v)", key, format, err)
		}
		if cfg.Width != dims[0] || cfg.Height != dims[1] {
			t.Errorf("%s: got %dx%d, Pillow %dx%d", key, cfg.Width, cfg.Height, dims[0], dims[1])
		}
		if _, err := jpeg.Decode(bytes.NewReader(raw)); err != nil {
			t.Errorf("%s: %v", key, err)
		}
	}
}
