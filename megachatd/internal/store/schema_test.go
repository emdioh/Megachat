package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const legacyMessagesV0 = `CREATE TABLE messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            contact_number TEXT NOT NULL,
            text TEXT,
            is_mine INTEGER NOT NULL DEFAULT 0,
            sender TEXT,
            timestamp INTEGER NOT NULL,
            quote_text TEXT,
            msg_type TEXT DEFAULT 'text',
            attachment_info TEXT,
            attachment_id TEXT,
            read INTEGER DEFAULT 0,
            status TEXT DEFAULT 'read'
        )`

const messagesV2 = `CREATE TABLE messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            protocol TEXT NOT NULL DEFAULT 'signal',
            contact_number TEXT NOT NULL,
            text TEXT,
            is_mine INTEGER NOT NULL DEFAULT 0,
            sender TEXT,
            timestamp INTEGER NOT NULL,
            quote_text TEXT,
            msg_type TEXT DEFAULT 'text',
            attachment_info TEXT,
            attachment_id TEXT,
            read INTEGER DEFAULT 0,
            status TEXT DEFAULT 'read',
            msg_id TEXT,
            quote_timestamp INTEGER,
            quote_author TEXT,
            reply_to_message_id TEXT
        )`

// currentMessagesNoMediaKind is today's table minus media_kind.
const currentMessagesNoMediaKind = `CREATE TABLE messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            protocol TEXT NOT NULL DEFAULT 'signal',
            contact_number TEXT NOT NULL,
            text TEXT,
            is_mine INTEGER NOT NULL DEFAULT 0,
            sender TEXT,
            timestamp INTEGER NOT NULL,
            quote_text TEXT,
            msg_type TEXT DEFAULT 'text',
            attachment_info TEXT,
            attachment_id TEXT,
            content_type TEXT,
            quote_attachment_id TEXT,
            quote_attachment_path TEXT,
            quote_content_type TEXT,
            read INTEGER DEFAULT 0,
            status TEXT DEFAULT 'read',
            msg_id TEXT,
            quote_timestamp INTEGER,
            quote_author TEXT,
            reply_to_message_id TEXT,
            edited INTEGER NOT NULL DEFAULT 0
        )`

const (
	legacyIdx = `CREATE INDEX idx_messages_contact ON messages(contact_number, timestamp)`
	protoIdx  = `CREATE INDEX idx_messages_contact ON messages(protocol, contact_number, timestamp)`
)

// attachment rows exercising every media_kind backfill rule.
func mediaRowsLegacy() []string {
	ins := "INSERT INTO messages (contact_number, text, timestamp, msg_type, attachment_info, attachment_id%s) VALUES (%s)"
	_ = ins
	return []string{
		`INSERT INTO messages (contact_number, text, timestamp, msg_type, attachment_info, attachment_id) VALUES
		 ('a','',1,'sticker','🎨',NULL),
		 ('a','',2,'image','x.png','x.png'),
		 ('a','',3,'attachment','Video: clip','clip.mp4'),
		 ('a','',4,'attachment','🎬 v','v1'),
		 ('a','',5,'attachment','audio/ogg','voice.oga'),
		 ('a','',6,'attachment','🎤 note','n1'),
		 ('a','',7,'attachment',NULL,'song.MP3'),
		 ('a','',8,'attachment','report','report.pdf'),
		 ('a','',9,'attachment','image/jpeg','photo.JPG'),
		 ('a','',10,'attachment',NULL,NULL),
		 ('a','plain',11,'text',NULL,NULL),
		 ('a','',12,'attachment','videoMessage','vm1')`,
	}
}

func mediaRowsWithContentType() []string {
	return []string{
		`INSERT INTO messages (protocol, contact_number, text, timestamp, msg_type, attachment_info, attachment_id, content_type) VALUES
		 ('whatsapp','a','',1,'attachment',NULL,'f1','video/mp4'),
		 ('whatsapp','a','',2,'attachment',NULL,'f2','audio/ogg'),
		 ('whatsapp','a','',3,'attachment',NULL,'f3','image/png'),
		 ('whatsapp','a','',4,'attachment',NULL,'f4','image/gif'),
		 ('whatsapp','a','',5,'attachment',NULL,'f5','application/pdf'),
		 ('whatsapp','a','',6,'text',NULL,NULL,'image/gif'),
		 ('telegram','b','',7,'attachment','doc.docx','d1',NULL),
		 ('telegram','b','',8,'image',NULL,'i1',NULL)`,
	}
}

type legacyFixture struct {
	name  string
	stmts []string
}

func legacyFixtures() []legacyFixture {
	v0 := append([]string{legacyMessagesV0, legacyIdx,
		`INSERT INTO messages (contact_number, text, timestamp) VALUES ('+391234567890','ciao',1000)`},
		mediaRowsLegacy()...)
	return []legacyFixture{
		{"v0-legacy-no-protocol", []string{legacyMessagesV0, legacyIdx,
			`INSERT INTO messages (contact_number, text, timestamp) VALUES ('+391234567890','ciao',1000)`}},
		{"v0-legacy-media-backfill", v0},
		{"v2-no-edited", []string{messagesV2, protoIdx,
			`INSERT INTO messages (protocol, contact_number, text, timestamp, is_mine) VALUES ('signal','+391234567890','ciao',1000,1)`,
			`PRAGMA user_version = 2`}},
		{"v3-no-edited", []string{messagesV2, protoIdx,
			`INSERT INTO messages (protocol, contact_number, text, timestamp, is_mine) VALUES ('signal','+391234567890','ciao',1000,1)`,
			`PRAGMA user_version = 3`}},
		{"v2-media-with-content-type", append([]string{
			`CREATE TABLE messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            protocol TEXT NOT NULL DEFAULT 'signal',
            contact_number TEXT NOT NULL,
            text TEXT,
            is_mine INTEGER NOT NULL DEFAULT 0,
            sender TEXT,
            timestamp INTEGER NOT NULL,
            quote_text TEXT,
            msg_type TEXT DEFAULT 'text',
            attachment_info TEXT,
            attachment_id TEXT,
            content_type TEXT,
            read INTEGER DEFAULT 0,
            status TEXT DEFAULT 'read',
            msg_id TEXT,
            edited INTEGER NOT NULL DEFAULT 0
        )`, protoIdx, `PRAGMA user_version = 2`}, mediaRowsWithContentType()...)},
		{"v4-minimal-table", []string{`CREATE TABLE messages (
                id INTEGER PRIMARY KEY,
                edited INTEGER NOT NULL DEFAULT 0,
                content_type TEXT,
                quote_attachment_id TEXT,
                quote_content_type TEXT,
                quote_attachment_path TEXT
            )`, `PRAGMA user_version = 4`}},
		{"v4-no-media-kind-with-docs", append([]string{currentMessagesNoMediaKind, protoIdx, `PRAGMA user_version = 4`},
			mediaRowsWithContentType()...)},
		{"v3-no-media-kind-with-docs", append([]string{currentMessagesNoMediaKind, protoIdx, `PRAGMA user_version = 3`},
			mediaRowsWithContentType()...)},
		{"v5-document-reclassified-as-audio", []string{
			`CREATE TABLE messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            protocol TEXT NOT NULL DEFAULT 'signal',
            contact_number TEXT NOT NULL,
            text TEXT,
            is_mine INTEGER NOT NULL DEFAULT 0,
            sender TEXT,
            timestamp INTEGER NOT NULL,
            msg_type TEXT DEFAULT 'text',
            attachment_info TEXT,
            attachment_id TEXT,
            content_type TEXT,
            media_kind TEXT,
            edited INTEGER NOT NULL DEFAULT 0
        )`, protoIdx,
			`INSERT INTO messages (contact_number, text, timestamp, msg_type, attachment_id, media_kind) VALUES
			 ('a','',1,'attachment','a.oga','document'),
			 ('a','',2,'attachment','b.pdf','document'),
			 ('a','',3,'attachment','c.mp4',NULL),
			 ('a','',4,'attachment','d.png',NULL),
			 ('a','',5,'attachment','e.bin',NULL)`,
			`PRAGMA user_version = 5`}},
	}
}

func TestFreshSchemaMatchesGolden(t *testing.T) {
	s := newStore(t)
	got := goDump(t, s)

	raw, err := os.ReadFile(filepath.Join("testdata", "schema_fresh.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want dbDump
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want.Schema, got.Schema) {
		t.Fatalf("schema differs from golden\n want: %v\n got:  %v", want.Schema, got.Schema)
	}
	eq(t, got.UserVersion, want.UserVersion)
	eq(t, got.UserVersion, SchemaVersion)
}

func TestFreshSchemaMatchesPythonExactly(t *testing.T) {
	requirePython(t)
	s := newStore(t)
	py, err := pyRun("init", filepath.Join(t.TempDir(), "py.db"))
	if err != nil {
		t.Fatal(err)
	}
	sameDump(t, "fresh", py, goDump(t, s), true)
}

func TestPragmasMatchPython(t *testing.T) {
	s := newStore(t)
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	eq(t, mode, "wal")
	var bt int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&bt); err != nil {
		t.Fatal(err)
	}
	eq(t, bt, 5000)
}

func TestLegacyMigrationsMatchPython(t *testing.T) {
	for _, fx := range legacyFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			path := fixtureFile(t, fx.stmts...)
			pyPath := filepath.Join(t.TempDir(), "py.db")
			copyFile(t, path, pyPath)

			s, err := Open(path, quiet)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			g := goDump(t, s)
			if g.UserVersion != SchemaVersion {
				t.Errorf("user_version = %d, want %d", g.UserVersion, SchemaVersion)
			}
			if !pyAvailable(t) {
				return
			}
			py, err := pyRun("init", pyPath)
			if err != nil {
				t.Fatal(err)
			}
			sameDump(t, fx.name, py, g, true)
		})
	}
}

func TestLegacyMigrationAssertions(t *testing.T) {
	path := fixtureFile(t, append([]string{legacyMessagesV0, legacyIdx,
		`INSERT INTO messages (contact_number, text, timestamp) VALUES ('+391234567890','ciao',1000)`},
		mediaRowsLegacy()...)...)
	s, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cols, err := messageColumnSet(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"protocol", "msg_id", "reply_to_message_id", "media_kind", "edited",
		"content_type", "batch_id", "batch_index", "quote_attachment_path"} {
		if !cols[c] {
			t.Errorf("missing column %s", c)
		}
	}
	kinds := map[int64]string{}
	for _, r := range allRows(t, s) {
		if k := r.str("media_kind"); k != nil {
			kinds[r.int("timestamp")] = *k
		}
	}
	want := map[int64]string{1: "sticker", 2: "image", 3: "video", 4: "video", 5: "audio",
		6: "audio", 7: "audio", 8: "document", 9: "image", 10: "document", 12: "video"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("media_kind backfill = %v, want %v", kinds, want)
	}
	// Index is namespaced by protocol.
	var idxSQL string
	if err := s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE name='idx_messages_contact'").Scan(&idxSQL); err != nil {
		t.Fatal(err)
	}
	eq(t, idxSQL, "CREATE INDEX idx_messages_contact ON messages(protocol, contact_number, timestamp)")
	// Existing row keeps default protocol.
	eq(t, count(t, s, "protocol = 'signal' AND edited = 0"), 13)
}

func TestReopenIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	s, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	cookie := func(s *Store) int {
		var v int
		if err := s.db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := goDump(t, s)
	c1 := cookie(s)
	_ = s.Close()
	for i := 0; i < 3; i++ {
		s, err = Open(path, quiet)
		if err != nil {
			t.Fatal(err)
		}
		// The schema cookie only moves on DDL: no DROP/CREATE INDEX churn.
		eq(t, cookie(s), c1)
		sameDump(t, "reopen", before, goDump(t, s), true)
		_ = s.Close()
	}
}

func TestOpenDefaultsAndDirectoryCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".local", "share", "signal-tui-client", "messages.db")
	eq(t, DefaultPath(), want)
	s, err := Open("", quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eq(t, s.Path(), want)
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "we ird#dir?x%20y")
	s, err := Open(filepath.Join(dir, "messages.db"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(filepath.Join(dir, "messages.db")); err != nil {
		t.Fatal(err)
	}
}
