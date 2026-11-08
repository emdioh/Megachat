package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
)

var ctx = context.Background()

// quiet silences the dedup/prune diagnostics in test output.
var quiet = WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "messages.db"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	s.markDirty()
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// row is a partial messages row; missing columns take the table defaults.
type row map[string]any

var rowDefaults = row{"protocol": "signal", "contact_number": "c", "timestamp": 1}

func insertRow(t *testing.T, s *Store, r row) int64 {
	t.Helper()
	full := row{}
	for k, v := range rowDefaults {
		full[k] = v
	}
	for k, v := range r {
		full[k] = v
	}
	cols, marks := "", ""
	var args []any
	for k, v := range full {
		if cols != "" {
			cols += ", "
			marks += ", "
		}
		cols += k
		marks += "?"
		args = append(args, v)
	}
	s.markDirty() // like any write by another process: Python re-ran the migration per call
	res, err := s.db.ExecContext(ctx, "INSERT INTO messages ("+cols+") VALUES ("+marks+")", args...)
	if err != nil {
		t.Fatalf("insert %v: %v", r, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func count(t *testing.T, s *Store, where string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// allRows returns every messages row as column->value maps ordered by rowid.
func allRows(t *testing.T, s *Store) []rowMap {
	t.Helper()
	rows, err := queryRowMaps(ctx, s.db, "SELECT * FROM messages ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func rowByID(t *testing.T, s *Store, id int64) rowMap {
	t.Helper()
	rows, err := queryRowMaps(ctx, s.db, "SELECT * FROM messages WHERE id = ?", id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("row %d: %v (%d rows)", id, err, len(rows))
	}
	return rows[0]
}

// must unwraps a (value, error) pair; an error panics, which fails the test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func strp(s string) *string { return &s }
func i64p(n int64) *int64   { return &n }

// ---- database dump helpers ----------------------------------------------

// dbDump is a whole-database snapshot: schema rows sorted by name, the schema
// version and every row of the data tables ordered by rowid.
type dbDump struct {
	Schema      [][]any                     `json:"schema"`
	UserVersion int                         `json:"user_version"`
	Tables      map[string][]map[string]any `json:"tables"`
}

// goDump snapshots the database (JSON round-tripped so numbers compare as
// float64 across snapshots).
func goDump(t *testing.T, s *Store) *dbDump {
	t.Helper()
	d := &dbDump{Tables: map[string][]map[string]any{}}
	schema, err := queryRowMaps(ctx, s.db, "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range schema {
		d.Schema = append(d.Schema, []any{r["type"], r["name"], r["tbl_name"], r["sql"]})
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.UserVersion = v
	for _, table := range []string{"messages", "reactions", "transcriptions"} {
		rows, err := queryRowMaps(ctx, s.db, "SELECT * FROM "+table+" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		list := []map[string]any{}
		for _, r := range rows {
			list = append(list, r)
		}
		d.Tables[table] = list
	}
	b, _ := json.Marshal(d)
	var out dbDump
	_ = json.Unmarshal(b, &out)
	return &out
}

// fixtureFile builds a database file with raw SQL (no migration applied).
func fixtureFile(t *testing.T, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func sameDump(t *testing.T, label string, want, got *dbDump, withSchema bool) {
	t.Helper()
	if withSchema {
		if !reflect.DeepEqual(want.Schema, got.Schema) {
			t.Errorf("%s: schema differs\n want: %v\n got:  %v", label, want.Schema, got.Schema)
		}
		if want.UserVersion != got.UserVersion {
			t.Errorf("%s: user_version want=%d got=%d", label, want.UserVersion, got.UserVersion)
		}
	}
	for table, rows := range want.Tables {
		if !reflect.DeepEqual(rows, got.Tables[table]) {
			t.Errorf("%s: table %s differs\n want: %v\n got:  %v", label, table, rows, got.Tables[table])
		}
	}
}
