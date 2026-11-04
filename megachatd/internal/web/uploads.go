package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	maxTotalBytes    = 250 << 20 // request-wide cap of /api/send
	maxKindBytes     = 100 << 20 // largest per-kind media limit
	maxSendFiles     = 10
	uploadMaxAge     = time.Hour
	copyChunk        = 256 * 1024
	gmailMaxFile     = 25 << 20
	gmailMaxTotal    = 25 << 20
	gmailMaxFiles    = 10
	formMaxFields    = 20
	maxFieldValueLen = 1 << 20
)

var (
	errTooManyFiles  = errors.New("too many files")
	errTooManyFields = errors.New("too many fields")
	errTooLarge      = errors.New("upload too large")
)

var allowedExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mp3": true,
	".ogg": true, ".opus": true, ".m4a": true, ".wav": true, ".pdf": true,
	".zip": true, ".docx": true, ".xlsx": true, ".pptx": true,
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}_ .()\-]`)
var spaces = regexp.MustCompile(`\s+`)

// sanitizeFilename ports filename_utils.sanitize_filename (without NFKC).
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = unsafeName.ReplaceAllString(name, "_")
	name = strings.Trim(spaces.ReplaceAllString(name, " "), " .")
	if len([]rune(name)) > 255 {
		ext := path.Ext(name)
		r := []rune(name)
		name = strings.TrimRight(string(r[:255-len([]rune(ext))]), " .") + ext
	}
	return name
}

func tempSuffix(filename string) string {
	ext := strings.ToLower(path.Ext(sanitizeFilename(filename)))
	if allowedExt[ext] {
		return ext
	}
	return ""
}

func (s *Server) prepareUploadDir() {
	if err := os.MkdirAll(s.uploadDir, 0o700); err != nil {
		s.log.Warn("cannot create upload dir", "dir", s.uploadDir, "err", err)
		return
	}
	entries, err := os.ReadDir(s.uploadDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-uploadMaxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(path.Join(s.uploadDir, e.Name()))
	}
}

// formSlot is one multipart entry with the watched field name: a file
// spooled to disk, or a plain field (isFile false).
type formSlot struct {
	isFile      bool
	path        string
	size        int64
	filename    string
	contentType string
}

type parsedForm struct {
	fields map[string]string // last value wins (Starlette dict(form))
	slots  []formSlot        // entries named fileField, in order
	temps  []string          // every spooled file, deleted by the caller
}

func (f *parsedForm) cleanup() {
	for _, p := range f.temps {
		_ = os.Remove(p)
	}
}

// parseForm streams a multipart body: fields are collected, file parts named
// fileField are spooled to the upload dir (others are discarded), with the
// Starlette max_files / max_fields limits.
func (s *Server) parseForm(w http.ResponseWriter, r *http.Request, fileField string, maxFiles int, limit int64) (*parsedForm, error) {
	if err := os.MkdirAll(s.uploadDir, 0o700); err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+maxFieldValueLen*formMaxFields)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	f := &parsedForm{fields: map[string]string{}}
	nFiles, nFields := 0, 0
	var total int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return f, nil
		}
		if err != nil {
			f.cleanup()
			return nil, err
		}
		name := part.FormName()
		_, params, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		_, hasFilename := params["filename"]
		if !hasFilename {
			nFields++
			if nFields > formMaxFields {
				f.cleanup()
				return nil, errTooManyFields
			}
			b, err := io.ReadAll(io.LimitReader(part, maxFieldValueLen))
			if err != nil {
				f.cleanup()
				return nil, err
			}
			total += int64(len(b))
			if name == fileField {
				f.slots = append(f.slots, formSlot{})
			} else {
				f.fields[name] = string(b)
			}
			continue
		}
		nFiles++
		if nFiles > maxFiles {
			f.cleanup()
			return nil, errTooManyFiles
		}
		if name != fileField {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		tmp, err := os.CreateTemp(s.uploadDir, "upload-*"+tempSuffix(params["filename"]))
		if err != nil {
			f.cleanup()
			return nil, err
		}
		f.temps = append(f.temps, tmp.Name())
		n, err := io.Copy(tmp, io.LimitReader(part, limit-total+1))
		tmp.Close()
		if err != nil {
			f.cleanup()
			return nil, err
		}
		total += n
		if total > limit {
			f.cleanup()
			return nil, errTooLarge
		}
		ct := part.Header.Get("Content-Type")
		f.slots = append(f.slots, formSlot{isFile: true, path: tmp.Name(), size: n, filename: params["filename"], contentType: ct})
	}
}

// guardFile feeds a spooled file through the guard (the mid-copy limit of
// store_temp_upload). maxBytes < 0: no per-file cap.
func (s *Server) guardFile(sl formSlot, maxBytes int64, media bool) error {
	if maxBytes >= 0 && sl.size > maxBytes {
		return errTooLarge
	}
	if media && sl.size > maxKindBytes {
		return errTooLarge
	}
	if s.guard == nil {
		return nil
	}
	g := s.guard.NewGuard(sl.filename, sl.contentType, maxBytes, media)
	if g == nil {
		return nil
	}
	f, err := os.Open(sl.path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, copyChunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if gerr := g(buf[:n]); gerr != nil {
				return errTooLarge
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
