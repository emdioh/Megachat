package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Upload policy (server/uploads.py): which attachments are acceptable and how
// they are classified.
const (
	MaxAttachments              = 10
	MaxUploadBytes              = 20 * 1024 * 1024
	GmailMaxAttachments         = 10
	GmailMaxAttachmentBytes     = 25 * 1024 * 1024
	GmailMaxTotalAttachmentByte = 25 * 1024 * 1024
	sniffBytes                  = 4096
	// MaxStagedTotalBytes caps what one request may stage.
	MaxStagedTotalBytes = 250 * 1024 * 1024
	// StagedMaxAge is the age after which staged files are pruned.
	StagedMaxAge = time.Hour
	copyChunk    = 256 * 1024
)

var maxBytesByKind = map[string]int64{
	"image": MaxUploadBytes, "video": 100 * 1024 * 1024, "audio": 50 * 1024 * 1024, "document": 50 * 1024 * 1024,
}

var extensionsByMime = map[string][]string{
	"image/png": {".png"}, "image/jpeg": {".jpg", ".jpeg"}, "image/gif": {".gif"}, "image/webp": {".webp"},
	"video/mp4": {".mp4", ".m4v"}, "video/quicktime": {".mov"}, "video/webm": {".webm"},
	"audio/mpeg": {".mp3"}, "audio/ogg": {".ogg", ".opus"}, "audio/mp4": {".m4a"},
	"audio/webm": {".webm"}, "audio/wav": {".wav"}, "application/pdf": {".pdf"},
	"application/zip": {".zip", ".docx", ".xlsx", ".pptx"},
}

// AllowedExtensions is every extension an accepted upload may carry.
var AllowedExtensions = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range extensionsByMime {
		for _, e := range v {
			m[e] = true
		}
	}
	return m
}()

var (
	webmVideoCodecs = [][]byte{[]byte("V_VP8"), []byte("V_VP9"), []byte("V_AV1"), []byte("V_MPEG4/ISO/AVC")}
	webmAudioCodecs = [][]byte{[]byte("A_OPUS"), []byte("A_VORBIS"), []byte("A_AAC"), []byte("A_MPEG/L3"), []byte("A_FLAC"), []byte("A_PCM")}
)

// UploadValidationError carries the HTTP-ish status: 413 (too large) or 400
// (unsupported/incoherent media).
type UploadValidationError struct{ Status int }

func (e *UploadValidationError) Error() string { return "Invalid media upload" }

// ValidatedUpload is an accepted attachment.
type ValidatedUpload struct {
	Path      string
	Filename  string // sanitized
	MimeType  string
	MediaKind string
}

var wordRE = regexp.MustCompile(`[^\p{L}\p{N}_ .()\-]`)
var spacesRE = regexp.MustCompile(`\s+`)

// SanitizeFilename is filename_utils.sanitize_filename (max 255 characters).
func SanitizeFilename(filename string) string {
	return sanitizeFilenameMax(filename, 255)
}

func sanitizeFilenameMax(filename string, maxLen int) string {
	n := strings.ReplaceAll(norm.NFKC.String(filename), "\\", "/")
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	s := wordRE.ReplaceAllString(n, "_")
	s = spacesRE.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .")
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	suffix := pySuffix(s)
	r := []rune(s)
	cut := string(r[:maxLen-utf8.RuneCountInString(suffix)])
	return strings.TrimRight(cut, " .") + suffix
}

func sniffMedia(h []byte) (mime, kind string, ok bool) {
	switch {
	case bytes.HasPrefix(h, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "image", true
	case bytes.HasPrefix(h, []byte("\xff\xd8\xff")):
		return "image/jpeg", "image", true
	case bytes.HasPrefix(h, []byte("GIF87a")), bytes.HasPrefix(h, []byte("GIF89a")):
		return "image/gif", "gif", true
	case len(h) >= 12 && bytes.HasPrefix(h, []byte("RIFF")) && string(h[8:12]) == "WEBP":
		return "image/webp", "image", true
	case len(h) >= 12 && (string(h[4:8]) == "ftyp" || string(h[4:8]) == "styp"):
		brand := bytes.ToLower(h[8:12])
		switch {
		case string(brand) == "qt  ":
			return "video/quicktime", "video", true
		case string(brand) == "m4a ":
			return "audio/mp4", "audio", true
		case bytes.HasPrefix(brand, []byte("iso")), bytes.HasPrefix(brand, []byte("mp4")), bytes.HasPrefix(brand, []byte("m4a")),
			string(brand) == "avc1", string(brand) == "avc3":
			return "video/mp4", "video", true
		}
		return "", "", false
	case len(h) >= 8 && string(h[4:8]) == "moof":
		return "video/mp4", "video", true
	case bytes.HasPrefix(h, []byte("\x1aE\xdf\xa3")):
		return "video/webm", "video", true
	case bytes.HasPrefix(h, []byte("OggS")):
		return "audio/ogg", "audio", true
	case bytes.HasPrefix(h, []byte("ID3")), bytes.HasPrefix(h, []byte("\xff\xfb")):
		return "audio/mpeg", "audio", true
	case len(h) >= 12 && bytes.HasPrefix(h, []byte("RIFF")) && string(h[8:12]) == "WAVE":
		return "audio/wav", "audio", true
	case bytes.HasPrefix(h, []byte("%PDF-")):
		return "application/pdf", "document", true
	case bytes.HasPrefix(h, []byte("PK\x03\x04")):
		return "application/zip", "document", true
	}
	return "", "", false
}

func maxBytesForKind(kind string) int64 {
	if kind == "gif" {
		kind = "image"
	}
	return maxBytesByKind[kind]
}

func containsAny(h []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(h, n) {
			return true
		}
	}
	return false
}

func sniffWebmKind(h []byte) string {
	if containsAny(h, webmVideoCodecs) {
		return "video"
	}
	if containsAny(h, webmAudioCodecs) {
		return "audio"
	}
	return ""
}

type box struct {
	typ        string
	start, end int
}

// iterBoxes lists the boxes of data[start:end] (32-bit, 64-bit and to-end sizes).
func iterBoxes(data []byte, start, end int) []box {
	var out []box
	offset := start
	for offset+8 <= end {
		size := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		typ := string(data[offset+4 : offset+8])
		header := int64(8)
		if size == 1 {
			if offset+16 > end {
				return out
			}
			s := binary.BigEndian.Uint64(data[offset+8 : offset+16])
			if s > 1<<62 {
				s = 1 << 62
			}
			size = int64(s)
			header = 16
		} else if size == 0 {
			size = int64(end - offset)
		}
		if size < header {
			return out
		}
		boxEnd := end
		if int64(offset)+size < int64(end) {
			boxEnd = offset + int(size)
		}
		out = append(out, box{typ, offset, boxEnd})
		if boxEnd <= offset {
			return out
		}
		offset = boxEnd
	}
	return out
}

// mp4VideoTrack classifies an MP4 by walking moov/trak/mdia/hdlr: any video
// track wins; "audio" only with a soun track and no video; "" no evidence.
func mp4VideoTrack(data []byte) string {
	foundVideo, foundAudio := false, false
	for _, moov := range iterBoxes(data, 0, len(data)) {
		if moov.typ != "moov" {
			continue
		}
		for _, trak := range iterBoxes(data, moov.start+8, moov.end) {
			if trak.typ != "trak" {
				continue
			}
			for _, mdia := range iterBoxes(data, trak.start+8, trak.end) {
				if mdia.typ != "mdia" {
					continue
				}
				for _, hdlr := range iterBoxes(data, mdia.start+8, mdia.end) {
					if hdlr.typ != "hdlr" || hdlr.start+20 > hdlr.end {
						continue
					}
					switch string(data[hdlr.start+16 : hdlr.start+20]) {
					case "vide":
						foundVideo = true
					case "soun":
						foundAudio = true
					}
				}
			}
		}
	}
	if foundVideo {
		return "video"
	}
	if foundAudio {
		return "audio"
	}
	return ""
}

// classifyUpload returns (mime, kind) for an upload header. The declared MIME
// is only a hint for ambiguous audio containers.
func classifyUpload(header []byte, declared, filename string) (string, string, error) {
	mime, kind, ok := sniffMedia(header)
	if !ok {
		return "", "", &UploadValidationError{400}
	}
	switch mime {
	case "video/webm":
		switch sniffWebmKind(header) {
		case "video":
			return "video/webm", "video", nil
		case "audio":
			return "audio/webm", "audio", nil
		}
	case "video/mp4":
		switch mp4VideoTrack(header) {
		case "video":
			return "video/mp4", "video", nil
		case "audio":
			return "audio/mp4", "audio", nil
		}
	}
	d, _, _ := strings.Cut(strings.ToLower(declared), ";")
	d = strings.TrimSpace(d)
	if (d == "audio/webm" || d == "audio/mp4") && kind != "audio" &&
		(mime == "video/webm" || mime == "video/mp4" || mime == "video/quicktime") {
		suffix := strings.ToLower(pySuffix(filename))
		if (d == "audio/webm" && suffix == ".webm") || (d == "audio/mp4" && suffix == ".m4a") {
			return d, "audio", nil
		}
	}
	return mime, kind, nil
}

// UploadGuard applies the size/kind limits while an upload is being copied.
type UploadGuard struct {
	MaxBytes      int64 // 0 = no per-file cap
	DeclaredType  string
	Filename      string
	NoMediaLimits bool // mail attachments: only MaxBytes applies
	header        []byte
	Total         int64
}

// Feed accounts one chunk; it returns a 413 error once a limit is exceeded.
func (g *UploadGuard) Feed(chunk []byte) error {
	g.Total += int64(len(chunk))
	if g.MaxBytes > 0 && g.Total > g.MaxBytes {
		return &UploadValidationError{413}
	}
	if g.NoMediaLimits {
		return nil
	}
	var biggest int64
	for _, v := range maxBytesByKind {
		if v > biggest {
			biggest = v
		}
	}
	if g.Total > biggest {
		return &UploadValidationError{413}
	}
	if len(g.header) < sniffBytes {
		n := sniffBytes - len(g.header)
		if n > len(chunk) {
			n = len(chunk)
		}
		g.header = append(g.header, chunk[:n]...)
	}
	_, kind, err := classifyUpload(g.header, g.DeclaredType, g.Filename)
	if err != nil {
		return nil // an incomplete header is not classifiable: validate decides
	}
	if g.Total > maxBytesForKind(kind) {
		return &UploadValidationError{413}
	}
	return nil
}

// ValidateUpload classifies and checks the stored file at path. 413 over the
// kind limit, 400 for an unrecognised or incoherent file. filename is the
// client's (sanitized here); contentType is only a hint.
func ValidateUpload(path, filename, contentType string) (ValidatedUpload, error) {
	f, err := os.Open(path)
	if err != nil {
		return ValidatedUpload{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ValidatedUpload{}, err
	}
	header := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return ValidatedUpload{}, err
	}
	header = header[:n]
	mime, kind, err := classifyUpload(header, contentType, filename)
	if err != nil {
		return ValidatedUpload{}, err
	}
	if st.Size() > maxBytesForKind(kind) {
		return ValidatedUpload{}, &UploadValidationError{413}
	}
	clean := SanitizeFilename(filename)
	suffix := strings.ToLower(pySuffix(clean))
	ok := false
	for _, e := range extensionsByMime[mime] {
		if e == suffix {
			ok = true
		}
	}
	if !ok {
		return ValidatedUpload{}, &UploadValidationError{400}
	}
	return ValidatedUpload{Path: path, Filename: clean, MimeType: mime, MediaKind: kind}, nil
}

// UploadDirectory is the staging directory of adapter uploads.
func UploadDirectory(cacheDir string) string { return filepath.Join(cacheDir, "web-uploads") }

// StageOptions tune StageLocalFile.
type StageOptions struct {
	Filename      string
	ContentType   string
	MaxBytes      int64 // 0 = none
	NoMediaLimits bool
}

// StageLocalFile copies a client-named local file into dir (the upload
// directory) and returns the copy. source must be absolute and is opened with
// O_NOFOLLOW|O_NONBLOCK; the open descriptor must be a regular file and the
// copy is read from it. Errors: *UploadValidationError (413/400) or an
// os error on storage failures. The caller deletes the copy.
func StageLocalFile(dir, source string, opt StageOptions) (string, error) {
	if source == "" || !filepath.IsAbs(source) {
		return "", &UploadValidationError{400}
	}
	fd, err := syscall.Open(source, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", &UploadValidationError{400}
	}
	in := os.NewFile(uintptr(fd), source)
	defer in.Close()
	st, err := in.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return "", &UploadValidationError{400}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	guard := &UploadGuard{MaxBytes: opt.MaxBytes, DeclaredType: opt.ContentType, Filename: opt.Filename, NoMediaLimits: opt.NoMediaLimits}
	suffix := strings.ToLower(pySuffix(SanitizeFilename(opt.Filename)))
	if !AllowedExtensions[suffix] {
		suffix = ""
	}
	out, err := os.CreateTemp(dir, "upload-*"+suffix)
	if err != nil {
		return "", err
	}
	tmp := out.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.Chmod(tmp, 0o600); err != nil {
		out.Close()
		return "", err
	}
	buf := make([]byte, copyChunk)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if err := guard.Feed(buf[:n]); err != nil {
				out.Close()
				return "", err
			}
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return "", err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return "", rerr
		}
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	ok = true
	return tmp, nil
}

// PruneUploadDirectory deletes staged regular files (not symlinks) older than
// StagedMaxAge; a missing directory is fine.
func PruneUploadDirectory(dir string, now time.Time) {
	cutoff := now.Add(-StagedMaxAge)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

var _ = fmt.Sprint
