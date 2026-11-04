package core

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Port of the validation half of server/uploads.py (validate_upload): the
// sniffed kind, per-kind size limits and filename/extension coherence. The
// media/upload package may supply a richer validator via WithUploadValidator.

// Upload is an accepted attachment.
type Upload struct {
	Path      string
	Filename  string // sanitised
	MimeType  string
	MediaKind string
}

// UploadError: Status 413 (too large) or 400 (unsupported / incoherent).
type UploadError struct{ Status int }

func (e *UploadError) Error() string { return "Invalid media upload" }

// UploadValidator classifies and checks the stored file at path.
type UploadValidator func(path, filename, contentType string) (Upload, error)

const sniffBytes = 4096

var maxBytesByKind = map[string]int64{
	"image": 20 << 20, "video": 100 << 20, "audio": 50 << 20, "document": 50 << 20,
}

var extensionsByMime = map[string][]string{
	"image/png":       {".png"},
	"image/jpeg":      {".jpg", ".jpeg"},
	"image/gif":       {".gif"},
	"image/webp":      {".webp"},
	"video/mp4":       {".mp4", ".m4v"},
	"video/quicktime": {".mov"},
	"video/webm":      {".webm"},
	"audio/mpeg":      {".mp3"},
	"audio/ogg":       {".ogg", ".opus"},
	"audio/mp4":       {".m4a"},
	"audio/webm":      {".webm"},
	"audio/wav":       {".wav"},
	"application/pdf": {".pdf"},
	"application/zip": {".zip", ".docx", ".xlsx", ".pptx"},
}

var (
	webmVideoCodecs = [][]byte{[]byte("V_VP8"), []byte("V_VP9"), []byte("V_AV1"), []byte("V_MPEG4/ISO/AVC")}
	webmAudioCodecs = [][]byte{[]byte("A_OPUS"), []byte("A_VORBIS"), []byte("A_AAC"), []byte("A_MPEG/L3"), []byte("A_FLAC"), []byte("A_PCM")}
)

func sniffMedia(h []byte) (mime, kind string, ok bool) {
	switch {
	case bytes.HasPrefix(h, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "image", true
	case bytes.HasPrefix(h, []byte("\xff\xd8\xff")):
		return "image/jpeg", "image", true
	case bytes.HasPrefix(h, []byte("GIF87a")) || bytes.HasPrefix(h, []byte("GIF89a")):
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
		case bytes.HasPrefix(brand, []byte("iso")) || bytes.HasPrefix(brand, []byte("mp4")) ||
			bytes.HasPrefix(brand, []byte("m4a")) || string(brand) == "avc1" || string(brand) == "avc3":
			return "video/mp4", "video", true
		}
		return "", "", false
	case len(h) >= 8 && string(h[4:8]) == "moof":
		return "video/mp4", "video", true
	case bytes.HasPrefix(h, []byte("\x1aE\xdf\xa3")):
		return "video/webm", "video", true
	case bytes.HasPrefix(h, []byte("OggS")):
		return "audio/ogg", "audio", true
	case bytes.HasPrefix(h, []byte("ID3")) || bytes.HasPrefix(h, []byte("\xff\xfb")):
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

type box struct {
	typ        string
	start, end int
}

func iterBoxes(data []byte, start, end int) []box {
	var out []box
	off := start
	for off+8 <= end {
		size := int(be(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		hdr := 8
		if size == 1 {
			if off+16 > end {
				return out
			}
			size = int(be(data[off+8 : off+16]))
			hdr = 16
		} else if size == 0 {
			size = end - off
		}
		if size < hdr {
			return out
		}
		bend := min(off+size, end)
		out = append(out, box{typ, off, bend})
		if bend <= off {
			return out
		}
		off = bend
	}
	return out
}

func be(b []byte) uint64 {
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return n
}

func mp4Kind(data []byte) string {
	video, audio := false, false
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
				for _, h := range iterBoxes(data, mdia.start+8, mdia.end) {
					if h.typ != "hdlr" || h.start+20 > h.end {
						continue
					}
					switch string(data[h.start+16 : h.start+20]) {
					case "vide":
						video = true
					case "soun":
						audio = true
					}
				}
			}
		}
	}
	if video {
		return "video"
	}
	if audio {
		return "audio"
	}
	return ""
}

func webmKind(h []byte) string {
	for _, c := range webmVideoCodecs {
		if bytes.Contains(h, c) {
			return "video"
		}
	}
	for _, c := range webmAudioCodecs {
		if bytes.Contains(h, c) {
			return "audio"
		}
	}
	return ""
}

func classifyUpload(header []byte, declared, filename string) (string, string, error) {
	mime, kind, ok := sniffMedia(header)
	if !ok {
		return "", "", &UploadError{400}
	}
	switch mime {
	case "video/webm":
		switch webmKind(header) {
		case "video":
			return "video/webm", "video", nil
		case "audio":
			return "audio/webm", "audio", nil
		}
	case "video/mp4":
		switch mp4Kind(header) {
		case "video":
			return "video/mp4", "video", nil
		case "audio":
			return "audio/mp4", "audio", nil
		}
	}
	d := strings.TrimSpace(strings.SplitN(strings.ToLower(declared), ";", 2)[0])
	if (d == "audio/webm" || d == "audio/mp4") && kind != "audio" &&
		(mime == "video/webm" || mime == "video/mp4" || mime == "video/quicktime") {
		suffix := strings.ToLower(filepath.Ext(filename))
		if (d == "audio/webm" && suffix == ".webm") || (d == "audio/mp4" && suffix == ".m4a") {
			return d, "audio", nil
		}
	}
	return mime, kind, nil
}

var (
	unsafeNameRe = regexp.MustCompile(`[^\w .()\-]`)
	spacesRe     = regexp.MustCompile(`\s+`)
)

// SanitizeFilename ports filename_utils.sanitize_filename.
func SanitizeFilename(name string) string {
	n := strings.ReplaceAll(norm.NFKC.String(name), "\\", "/")
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	// Python \w is Unicode letters, digits and underscore.
	var sb strings.Builder
	for _, r := range n {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '.' || r == '(' || r == ')' || r == '-' || unicode.IsSpace(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	_ = unsafeNameRe
	s := strings.Trim(spacesRe.ReplaceAllString(sb.String(), " "), " .")
	r := []rune(s)
	if len(r) <= 255 {
		return s
	}
	ext := filepath.Ext(s)
	head := []rune(s)[:255-len([]rune(ext))]
	return strings.TrimRight(string(head), " .") + ext
}

// DefaultUploadValidator is validate_upload.
func DefaultUploadValidator(path, filename, contentType string) (Upload, error) {
	f, err := os.Open(path)
	if err != nil {
		return Upload{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Upload{}, err
	}
	header := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Upload{}, err
	}
	header = header[:n]
	mime, kind, err := classifyUpload(header, contentType, filename)
	if err != nil {
		return Upload{}, err
	}
	limitKind := kind
	if kind == "gif" {
		limitKind = "image"
	}
	if info.Size() > maxBytesByKind[limitKind] {
		return Upload{}, &UploadError{413}
	}
	clean := SanitizeFilename(filename)
	suffix := strings.ToLower(filepath.Ext(clean))
	okExt := false
	for _, e := range extensionsByMime[mime] {
		if e == suffix {
			okExt = true
		}
	}
	if !okExt {
		return Upload{}, &UploadError{400}
	}
	return Upload{Path: path, Filename: clean, MimeType: mime, MediaKind: kind}, nil
}
