package media

import (
	"context"
	"crypto/sha1" //nolint:gosec // cache key, not security
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	_ "image/png" // decoder

	_ "golang.org/x/image/bmp" // decoders: BMP, TIFF and WebP thumbnails like Pillow
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // (see also internal/imgfmt)
	_ "golang.org/x/image/webp"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ThumbWidths are the accepted thumbnail widths.
var ThumbWidths = map[int]bool{96: true, 240: true, 480: true}

const (
	thumbCacheLimit = 500 * 1024 * 1024
	thumbQuality    = 78
)

var (
	thumbLocksMu sync.Mutex
	thumbLocks   = map[string]*sync.Mutex{}
)

func pathLock(p string) *sync.Mutex {
	thumbLocksMu.Lock()
	defer thumbLocksMu.Unlock()
	l, ok := thumbLocks[p]
	if !ok {
		l = &sync.Mutex{}
		thumbLocks[p] = l
	}
	return l
}

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(h[:])
}

func touch(p string) {
	now := time.Now()
	_ = os.Chtimes(p, now, now)
}

// pruneThumbCache removes the oldest *.jpg under cacheRoot until the total
// is under the limit.
func pruneThumbCache(cacheRoot string) { pruneThumbCacheLimit(cacheRoot, thumbCacheLimit) }

func pruneThumbCacheLimit(cacheRoot string, limit int64) {
	type ent struct {
		mtime int64
		size  int64
		path  string
	}
	var ents []ent
	var total int64
	_ = filepath.WalkDir(cacheRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jpg") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		ents = append(ents, ent{info.ModTime().UnixNano(), info.Size(), p})
		total += info.Size()
		return nil
	})
	sort.Slice(ents, func(i, j int) bool { return ents[i].mtime < ents[j].mtime }) // oldest first
	for _, e := range ents {
		if total <= limit {
			break
		}
		_ = os.Remove(e.path)
		total -= e.size
	}
}

// thumbnailPath returns the cached JPEG thumbnail of the image at path,
// generating it. ok is false for anything that cannot be thumbnailed
// (not an image candidate, GIF, a format the stdlib cannot decode...).
func (d *Deps) thumbnailPath(ctx context.Context, path, proto, attachmentID string, width int) (string, bool) {
	if !d.isThumbnailCandidate(ctx, path, proto, attachmentID) {
		return "", false
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	thumbDir := d.Config.WebThumbDir(proto)
	digest := sha1hex(fmt.Sprintf("%s|%d|%d|%d", path, st.ModTime().UnixNano(), st.Size(), width))
	thumb := filepath.Join(thumbDir, digest+".jpg")
	l := pathLock(thumb)
	l.Lock()
	defer l.Unlock()
	if isRegularFile(thumb) {
		touch(thumb)
		return thumb, true
	}
	tmp := strings.TrimSuffix(thumb, ".jpg") + ".tmp"
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	img, format, err := image.Decode(f)
	f.Close()
	if err != nil || format == "gif" {
		return "", false
	}
	if err := writeJPEGThumb(img, width, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", false
	}
	if err := os.Rename(tmp, thumb); err != nil {
		_ = os.Remove(tmp)
		return "", false
	}
	pruneThumbCache(filepath.Dir(thumbDir))
	return thumb, true
}

var _ = gif.Decode // keep the GIF decoder registered (animated GIFs are refused by format)

func writeJPEGThumb(img image.Image, width int, out string) error {
	small := fitRGB(img, width)
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, small, &jpeg.Options{Quality: thumbQuality}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// fitRGB converts to RGB (alpha dropped, like Pillow convert("RGB")) and
// shrinks to fit width x width keeping the aspect ratio; it never enlarges.
func fitRGB(src image.Image, maxSide int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw > maxSide || sh > maxSide {
		if sw >= sh {
			dw = maxSide
			dh = int(math.Round(float64(sh) * float64(maxSide) / float64(sw)))
		} else {
			dh = maxSide
			dw = int(math.Round(float64(sw) * float64(maxSide) / float64(sh)))
		}
		if dw < 1 {
			dw = 1
		}
		if dh < 1 {
			dh = 1
		}
	}
	// Pillow's convert("RGB") drops alpha without compositing: make the
	// source opaque first, then resample with a bilinear (triangle) filter whose
	// support widens when shrinking, like Image.thumbnail(..., Image.BILINEAR).
	var opaque image.Image
	switch src.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		opaque = src
	default:
		get := rgbAccessor(src)
		rgb := image.NewRGBA(image.Rect(0, 0, sw, sh))
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				r, g, bl := get(b.Min.X+x, b.Min.Y+y)
				i := rgb.PixOffset(x, y)
				rgb.Pix[i], rgb.Pix[i+1], rgb.Pix[i+2], rgb.Pix[i+3] = r, g, bl, 0xff
			}
		}
		opaque = rgb
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), opaque, opaque.Bounds(), xdraw.Src, nil)
	return dst
}

func rgbAccessor(src image.Image) func(x, y int) (uint8, uint8, uint8) {
	switch s := src.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint8, uint8, uint8) {
			yi := s.YOffset(x, y)
			ci := s.COffset(x, y)
			return color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
		}
	case *image.NRGBA:
		return func(x, y int) (uint8, uint8, uint8) {
			i := s.PixOffset(x, y)
			return s.Pix[i], s.Pix[i+1], s.Pix[i+2]
		}
	case *image.Gray:
		return func(x, y int) (uint8, uint8, uint8) {
			v := s.GrayAt(x, y).Y
			return v, v, v
		}
	}
	return func(x, y int) (uint8, uint8, uint8) {
		c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
		return c.R, c.G, c.B
	}
}
