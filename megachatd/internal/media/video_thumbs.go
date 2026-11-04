package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

const (
	videoThumbAcquireTimeout = 20 * time.Second
	ffmpegTimeout            = 15 * time.Second
	headBytes                = 512 * 1024
	tailBytes                = 2 * 1024 * 1024
)

var (
	videoThumbSem    = make(chan struct{}, 2)
	ffmpegOnce       sync.Once
	ffmpegExecutable string
	// ffmpegLookup is replaced by tests.
	ffmpegLookup = func() string { p, _ := exec.LookPath("ffmpeg"); return p }
)

func ffmpegPath() string {
	ffmpegOnce.Do(func() {
		ffmpegExecutable = ffmpegLookup()
		if ffmpegExecutable == "" {
			slog.Warn("ffmpeg unavailable; video thumbnails are disabled")
		}
	})
	return ffmpegExecutable
}

// mp4HasMoov reports whether the box walk of data reaches a moov box.
func mp4HasMoov(data []byte) bool {
	offset, length := uint64(0), uint64(len(data))
	for offset+8 <= length {
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		header := uint64(8)
		if size == 1 {
			if offset+16 > length {
				return false
			}
			size = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			header = 16
		} else if size == 0 {
			size = length - offset
		}
		if size < header {
			return false
		}
		if boxType == "moov" {
			return true
		}
		if size > length-offset {
			return false
		}
		offset += size
	}
	return false
}

// videoThumbnail returns the cached JPEG thumbnail of a video file, running
// ffmpeg on a miss. cacheIdentity overrides the cache key (chunk thumbnails).
func (d *Deps) videoThumbnail(ctx context.Context, path, proto, attachmentID string, width int, cacheIdentity string) (string, bool) {
	if path == "" || !isRegularFile(path) {
		return "", false
	}
	primitive := cacheIdentity
	if primitive == "" {
		st, err := os.Stat(path)
		if err != nil {
			return "", false
		}
		primitive = fmt.Sprintf("%s|%d|%d", path, st.ModTime().UnixNano(), width)
	}
	thumbDir := d.Config.WebThumbDir(proto)
	thumb := filepath.Join(thumbDir, sha1hex(primitive)+".jpg")
	l := pathLock(thumb)
	l.Lock()
	defer l.Unlock()
	if isRegularFile(thumb) {
		touch(thumb)
		return thumb, true
	}
	exe := ffmpegPath()
	if exe == "" {
		return "", false
	}
	select {
	case videoThumbSem <- struct{}{}:
	case <-time.After(videoThumbAcquireTimeout):
		return "", false
	case <-ctx.Done():
		return "", false
	}
	defer func() { <-videoThumbSem }()

	output := strings.TrimSuffix(thumb, ".jpg") + ".tmp"
	defer os.Remove(output)
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		return "", false
	}
	frame, err := os.CreateTemp(thumbDir, "video-frame-*.jpg")
	if err != nil {
		return "", false
	}
	frame.Close()
	defer os.Remove(frame.Name())

	cctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, exe, "-hide_banner", "-loglevel", "error", "-ss", "0",
		"-i", path, "-map", "0:v:0", "-frames:v", "1", "-f", "image2", "-vcodec", "mjpeg",
		"-y", frame.Name())
	if err := cmd.Run(); err != nil {
		return "", false
	}
	st, err := os.Stat(frame.Name())
	if err != nil || st.Size() == 0 {
		return "", false
	}
	fh, err := os.Open(frame.Name())
	if err != nil {
		return "", false
	}
	img, _, err := image.Decode(fh)
	fh.Close()
	if err != nil {
		return "", false
	}
	if err := writeJPEGThumb(img, width, output); err != nil {
		return "", false
	}
	if err := os.Rename(output, thumb); err != nil {
		return "", false
	}
	pruneThumbCache(filepath.Dir(thumbDir))
	return thumb, true
}

var _ = jpeg.Decode

// chunkVideoThumbnail builds a thumbnail from head/tail byte ranges of a
// remote (not downloaded) video. chunkAvailable says the backend does provide
// chunks for it (so a failure is "unprocessable", not "not found").
func (d *Deps) chunkVideoThumbnail(ctx context.Context, protocol, attachmentID string, width int) (thumb string, chunkAvailable bool) {
	if d.Backends == nil {
		return "", false
	}
	be, ok := d.Backends.Get(protocol)
	if !ok {
		return "", false
	}
	provider, ok := be.(proto.AttachmentChunker)
	if !ok {
		return "", false
	}
	identity := fmt.Sprintf("vid|%s|%s|%d", protocol, attachmentID, width)
	digest := sha1hex(identity)
	if c := d.cachedThumbnail(protocol, digest); c != "" {
		return c, true
	}
	head, err := provider.AttachmentChunk(ctx, attachmentID, 0, headBytes)
	if err != nil || len(head) == 0 {
		return "", false
	}
	data := append([]byte(nil), head...)
	isMP4 := len(data) >= 8 && bytes.Equal(data[4:8], []byte("ftyp"))
	if !(isMP4 && mp4HasMoov(data)) {
		tail, err := provider.AttachmentChunk(ctx, attachmentID, -1, tailBytes)
		if err != nil || len(tail) == 0 {
			return "", true
		}
		data = append(data, tail...)
	}
	f, err := os.CreateTemp("", "video-chunk-*.video")
	if err != nil {
		return "", true
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(data)
	f.Close()
	if werr != nil {
		return "", true
	}
	t, _ := d.videoThumbnail(ctx, f.Name(), protocol, attachmentID, width, identity)
	return t, true
}

func (d *Deps) cachedThumbnail(protocol, digest string) string {
	thumb := filepath.Join(d.Config.WebThumbDir(protocol), digest+".jpg")
	l := pathLock(thumb)
	l.Lock()
	defer l.Unlock()
	if !isRegularFile(thumb) {
		return ""
	}
	touch(thumb)
	return thumb
}
