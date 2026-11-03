// Package store is the Go port of the Python message cache (protocols/db.py).
//
// It opens the SAME SQLite database file the Python server creates
// (~/.local/share/signal-tui-client/messages.db), upgrades older schemas in
// place exactly as Python does, and exposes the same queries with the same
// semantics (ordering, dedup rules, status ranking, reactions, pruning,
// transcriptions, unread counts).
//
// A Store is safe for concurrent use: every statement runs on a single
// pooled connection, so goroutines are serialised exactly like the Python
// _DB_LOCK serialised its threads.  Multi-statement operations run in one
// IMMEDIATE transaction.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite"
)

const (
	// SchemaVersion is the PRAGMA user_version this code leaves behind.
	SchemaVersion = 5
	// LegacyMigrationVersion is the user_version at (and above) which the
	// legacy protocol/msg_id migration is skipped.
	LegacyMigrationVersion = 4

	// MinPruneLimit is the floor of the per-contact retention cap: it may not
	// drop below the largest re-fetch window, or pruned rows would be
	// re-inserted as new unread rows.
	MinPruneLimit = 100

	// EchoMatchWindowMs is the window (ms) within which an id-less row can be
	// the echo of a message carrying a real id.
	EchoMatchWindowMs int64 = 600_000

	// busyTimeoutMs mirrors Python's sqlite3.connect default timeout (5 s).
	busyTimeoutMs = 5000
)

// Store is a handle on the message cache database.
type Store struct {
	db    *sql.DB
	path  string
	log   *slog.Logger
	clock func() time.Time
	// dirty: this process wrote rows that RepairMediaKinds may need to classify.
	dirty atomic.Bool
}

// Option customises Open.
type Option func(*Store)

// WithLogger sets the logger used for dedup/prune diagnostics.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) {
		if l != nil {
			s.log = l
		}
	}
}

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.clock = now
		}
	}
}

// DefaultCacheDir is ~/.local/share/signal-tui-client (Python CACHE_DIR).
func DefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "signal-tui-client")
}

// DefaultPath is <DefaultCacheDir>/messages.db (Python DB_FILE).
func DefaultPath() string {
	return filepath.Join(DefaultCacheDir(), "messages.db")
}

// Open opens (creating and migrating if needed) the database at path.  An
// empty path selects DefaultPath().  The parent directory is created.
func Open(path string, opts ...Option) (*Store, error) {
	return OpenContext(context.Background(), path, opts...)
}

// OpenContext is Open with a context for the schema initialisation.
func OpenContext(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o777); err != nil {
		return nil, fmt.Errorf("store: create cache dir: %w", err)
	}

	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Set("_txlock", "immediate")
	dsn := "file:" + (&url.URL{Path: abs}).EscapedPath() + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", abs, err)
	}
	// One connection == the Python RLock: all access is serialised.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: abs, log: slog.Default(), clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	if err := s.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: init schema: %w", err)
	}
	return s, nil
}

// Path returns the absolute database path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// withTx runs fn in an IMMEDIATE transaction, committing on success.
func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func rowsAffected(res sql.Result) int64 {
	n, err := res.RowsAffected()
	if err != nil {
		return 0
	}
	return n
}

// Ptr returns a pointer to v; handy for the optional (NULL-able) fields.
func Ptr[T any](v T) *T { return &v }

// Deref returns *p, or the zero value when p is nil.
func Deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// placeholders returns "?, ?, ..." with n marks.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, '?')
	}
	return string(b)
}

func strArgs(vals []string) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

// statusRankCase is the SQL spelling of the delivery-status rank
// (pending/failed/unknown 0, sent 1, delivered 2, read 3).  Kept textually
// identical to the Python queries.
const statusRankCase = "CASE status WHEN 'pending' THEN 0 WHEN 'failed' THEN 0 " +
	"WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END"
