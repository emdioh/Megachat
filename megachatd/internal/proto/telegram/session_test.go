package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"
)

func testKey() []byte {
	k := make([]byte, 256)
	for i := range k {
		k[i] = byte(i*7 + 3)
	}
	return k
}

func makeTelethonDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE version (version integer primary key)",
		"CREATE TABLE sessions (dc_id integer primary key, server_address text, port integer, auth_key blob, takeout_id integer)",
		"CREATE TABLE entities (id integer primary key, hash integer not null, username text, phone integer, name text, date integer)",
		"CREATE TABLE update_state (id integer primary key, pts integer, qts integer, date integer, seq integer)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO sessions VALUES (2, '149.154.167.51', 443, ?, NULL)", testKey()); err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO entities VALUES (42, 4242, 'bob', NULL, 'Bob', 0)")
	db.Exec("INSERT INTO entities VALUES (-1001234, 777, NULL, NULL, 'Chan', 0)")
	db.Exec("INSERT INTO update_state VALUES (0, 100, 5, 1700000000, 9)")
}

func fileHash(t *testing.T, p string) [32]byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func TestImportTelethonSQLiteSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signal-tui-client", "telegram.session")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	makeTelethonDB(t, path)
	before := fileHash(t, path)

	kind, err := DetectSessionKind(path)
	if err != nil || kind != SessionSQLite {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	imp, err := LoadTelethonSession(path)
	if err != nil {
		t.Fatal(err)
	}
	d := imp.Data
	if d.DC != 2 || d.Addr != "149.154.167.51:443" || !bytes.Equal(d.AuthKey, testKey()) || len(d.AuthKeyID) != 8 {
		t.Fatalf("data: %+v", d)
	}
	if imp.Peers[42] != 4242 || imp.Peers[-1001234] != 777 {
		t.Fatalf("peers: %v", imp.Peers)
	}
	if st := imp.UpdateState[0]; st.Pts != 100 || st.Qts != 5 || st.Date != 1700000000 || st.Seq != 9 {
		t.Fatalf("state: %+v", st)
	}

	storage, imp2, imported, err := ImportSession(context.Background(), path)
	if err != nil || !imported || imp2 == nil {
		t.Fatalf("import: %v %v", imported, err)
	}
	if want := filepath.Join(filepath.Dir(path), "telegram.gotd.json"); storage.Path != want {
		t.Fatalf("storage path %q want %q", storage.Path, want)
	}
	loaded, err := (&session.Loader{Storage: storage}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DC != 2 || !bytes.Equal(loaded.AuthKey, testKey()) || loaded.Addr != d.Addr {
		t.Fatalf("stored session differs: %+v", loaded)
	}
	// The second call reuses the gotd file; Telethon's file is untouched.
	if _, _, again, err := ImportSession(context.Background(), path); err != nil || again {
		t.Fatalf("second import: %v %v", again, err)
	}
	if fileHash(t, path) != before {
		t.Fatal("the Telethon session file was modified")
	}
}

func stringSession(dc byte, ip []byte, port uint16, key []byte) string {
	raw := append([]byte{dc}, ip...)
	raw = append(raw, byte(port>>8), byte(port))
	raw = append(raw, key...)
	return "1" + base64.URLEncoding.EncodeToString(raw)
}

func TestImportTelethonStringSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.session")
	s := stringSession(4, []byte{149, 154, 167, 91}, 443, testKey())
	if err := os.WriteFile(path, []byte(s+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kind, _ := DetectSessionKind(path)
	if kind != SessionString {
		t.Fatalf("kind %q", kind)
	}
	storage, imp, imported, err := ImportSession(context.Background(), path)
	if err != nil || !imported {
		t.Fatalf("%v %v", imported, err)
	}
	if imp.Kind != SessionString || imp.Data.DC != 4 || imp.Data.Addr != "149.154.167.91:443" || !bytes.Equal(imp.Data.AuthKey, testKey()) {
		t.Fatalf("%+v", imp.Data)
	}
	if _, err := os.Stat(storage.Path); err != nil {
		t.Fatal(err)
	}
	// IPv6 form (275 bytes).
	ip6 := bytes.Repeat([]byte{0x20}, 16)
	d, err := ParseStringSession(stringSession(1, ip6, 80, testKey()))
	if err != nil || d.DC != 1 || d.Addr == "" {
		t.Fatalf("%v %+v", err, d)
	}
	if _, err := ParseStringSession("2abc"); err == nil {
		t.Fatal("bad version accepted")
	}
	if _, err := ParseStringSession(""); err == nil {
		t.Fatal("empty accepted")
	}
}

func TestImportRejectsSessionWithoutAuthKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.session")
	db, _ := sql.Open("sqlite", path)
	db.Exec("CREATE TABLE sessions (dc_id integer primary key, server_address text, port integer, auth_key blob, takeout_id integer)")
	db.Exec("INSERT INTO sessions VALUES (2, '1.2.3.4', 443, NULL, NULL)")
	db.Close()
	if _, err := LoadTelethonSession(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestSessionPathHonoursXDG(t *testing.T) {
	e := newEnv(t)
	b := New(Options{Config: e.b.cfg, APIID: 1, APIHash: "h"})
	want := filepath.Join(e.dir, "xdg", "signal-tui-client", "telegram.session")
	if b.sessionPath != want {
		t.Fatalf("%q != %q", b.sessionPath, want)
	}
	if got := gotdSessionPath(want); filepath.Base(got) != "telegram.gotd.json" {
		t.Fatal(got)
	}
	if !b.NeedsPairing() {
		t.Fatal("no session: pairing needed")
	}
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	makeTelethonDB(t, want)
	if b.NeedsPairing() {
		t.Fatal("session present: no pairing")
	}
}
