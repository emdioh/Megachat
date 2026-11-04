package core

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

// Port of server/logbuffer.py: an in-memory ring of recent, redacted records.

const (
	// LogBufferCapacity is the ring size (2000 records).
	LogBufferCapacity = 2000
	maxLogMessage     = 4000
)

var logRedactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{6,}`), "sk-***"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{10,}`), "AIza***"},
	{regexp.MustCompile(`(Bearer token:\s*)\S+`), "${1}***"},
	{regexp.MustCompile(`(Bearer\s+)[A-Za-z0-9._\-]{8,}`), "${1}***"},
}

// Redact masks API keys and bearer tokens.
func Redact(text string) string {
	for _, r := range logRedactions {
		text = r.re.ReplaceAllString(text, r.repl)
	}
	return text
}

// Python logging levels used by server.logs.
var levelNumbers = map[string]int{
	"DEBUG": 10, "INFO": 20, "WARNING": 30, "WARN": 30, "ERROR": 40, "CRITICAL": 50, "FATAL": 50, "NOTSET": 0,
}

func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARNING"
	default:
		return "ERROR"
	}
}

// LogBuffer is the ring buffer. It is also a slog.Handler (see Handler).
type LogBuffer struct {
	mu          sync.Mutex
	records     []api.LogRecord
	seq         int64
	installedAt time.Time
}

// NewLogBuffer creates an empty buffer.
func NewLogBuffer() *LogBuffer { return &LogBuffer{installedAt: time.Now()} }

// Add records a message (redacted, cut to 4000 characters).
func (b *LogBuffer) Add(t time.Time, level, logger, message string) {
	message = Redact(message)
	if r := []rune(message); len(r) > maxLogMessage {
		message = string(r[:maxLogMessage])
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.records = append(b.records, api.LogRecord{
		Seq: b.seq, Time: float64(t.UnixNano()) / 1e9, Level: level, Logger: logger, Message: message,
	})
	if len(b.records) > LogBufferCapacity {
		b.records = append([]api.LogRecord(nil), b.records[len(b.records)-LogBufferCapacity:]...)
	}
}

// Snapshot returns the last limit records with seq > after and level >= minLevel.
func (b *LogBuffer) Snapshot(after int64, limit, minLevel int) []api.LogRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	var rows []api.LogRecord
	for _, r := range b.records {
		if r.Seq > after && levelNumbers[r.Level] >= minLevel {
			rows = append(rows, r)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows
}

// Clear empties the buffer (sequence numbers keep counting).
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	b.records = nil
	b.mu.Unlock()
}

// InstalledAt is the buffer creation time (server.logs "since").
func (b *LogBuffer) InstalledAt() time.Time { return b.installedAt }

// Handler returns a slog.Handler that records into the buffer and forwards to
// next (which may be nil).
func (b *LogBuffer) Handler(next slog.Handler) slog.Handler {
	return &bufferHandler{buf: b, next: next}
}

type bufferHandler struct {
	buf   *LogBuffer
	next  slog.Handler
	attrs []slog.Attr
	group string
}

func (h *bufferHandler) Enabled(ctx context.Context, l slog.Level) bool { return true }

func (h *bufferHandler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	add := func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.buf.Add(t, levelName(r.Level), "megachat", sb.String())
	if h.next != nil && h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	if h.next != nil {
		n.next = h.next.WithAttrs(attrs)
	}
	return &n
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	n := *h
	if h.next != nil {
		n.next = h.next.WithGroup(name)
	}
	return &n
}
