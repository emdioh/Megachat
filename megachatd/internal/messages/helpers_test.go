package messages

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var bg = context.Background()

// row is a partial messages row; missing columns take the table defaults
// (status 'read', msg_type 'text').
type row map[string]any

type reactionRow struct {
	protocol, contact string
	targetMsgID       any
	targetTS          any
	emoji, author     string
	isMine            int
	count             int
	ts                int64
}

// env is a configured store + manager + registry over a temp directory.
type env struct {
	t        *testing.T
	cfg      *config.Config
	st       *store.Store
	mgr      *proto.Manager
	reg      *ops.Registry
	backends map[string]*fake.Backend

	contacts    map[string][]proto.Contact
	addressBook map[string][]proto.Contact
	jidPhone    map[string]string
}

// deps implements Deps over env.
type deps struct{ e *env }

func (d deps) Store() *store.Store     { return d.e.st }
func (d deps) Manager() *proto.Manager { return d.e.mgr }
func (d deps) Config() *config.Config  { return d.e.cfg }
func (d deps) Contacts(_ context.Context, protocol string) ([]proto.Contact, bool) {
	if _, ok := d.e.mgr.Get(protocol); !ok {
		return nil, false
	}
	return d.e.contacts[protocol], true
}

// FindContact resolves an id directly, else a contact whose lid or phone@c.us
// alias is the id.
func (d deps) FindContact(_ context.Context, protocol, id string) (proto.Contact, bool) {
	list := d.e.contacts[protocol]
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	for _, c := range list {
		if s, _ := c.Extras["lid"].(string); s == id {
			return c, true
		}
		if p, _ := c.Extras["phone"].(string); p != "" && p+"@c.us" == id {
			return c, true
		}
	}
	return proto.Contact{}, false
}

type envOpts struct {
	rows      func(cfg *config.Config) []row
	reactions []reactionRow
	// env variables of the config (WHATSAPP_LAZY_MEDIA=0 turns lazy media off)
	vars map[string]string
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	cfg := config.NewWith(dir, home, o.vars)
	dbPath := cfg.DBFile()
	quiet := store.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Create the schema, insert the fixture rows with a plain connection, then
	// reopen so the store sees them like rows written by another process.
	st, err := store.Open(dbPath, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture []row
	if o.rows != nil {
		fixture = o.rows(cfg)
	}
	for _, r := range fixture {
		full := row{"protocol": "signal", "contact_number": "c", "timestamp": 1}
		for k, v := range r {
			full[k] = v
		}
		insertMap(t, raw, "messages", full)
	}
	for _, r := range o.reactions {
		insertMap(t, raw, "reactions", row{
			"protocol": r.protocol, "contact_number": r.contact, "target_msg_id": r.targetMsgID,
			"target_timestamp": r.targetTS, "emoji": r.emoji, "author_key": r.author,
			"author": r.author, "is_mine": r.isMine, "count": r.count, "timestamp": r.ts,
		})
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dbPath, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	e := &env{
		t: t, cfg: cfg, st: st, mgr: proto.New(), reg: ops.NewRegistry(),
		backends: map[string]*fake.Backend{}, contacts: map[string][]proto.Contact{},
		addressBook: map[string][]proto.Contact{}, jidPhone: map[string]string{},
	}
	for _, p := range []string{api.ProtocolSignal, api.ProtocolWhatsApp, api.ProtocolTelegram} {
		fb := fake.New(p)
		e.backends[p] = fb
		if err := e.mgr.Register(&testBackend{Backend: fb, protocol: p, e: e}); err != nil {
			t.Fatal(err)
		}
	}
	RegisterOps(e.reg, deps{e})
	return e
}

// testBackend is a fake backend whose address book and @lid -> phone cache
// come from env.
type testBackend struct {
	*fake.Backend
	protocol string
	e        *env
}

func (b *testBackend) ListAddressBook(_ context.Context, _ bool) ([]proto.Contact, error) {
	return b.e.addressBook[b.protocol], nil
}

func (b *testBackend) JIDToPhone(jid string) string { return b.e.jidPhone[jid] }

func insertMap(t *testing.T, db *sql.DB, table string, r row) {
	t.Helper()
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = r[k]
	}
	q := "INSERT INTO " + table + " (" + strings.Join(keys, ", ") + ") VALUES (" +
		strings.TrimSuffix(strings.Repeat("?, ", len(keys)), ", ") + ")"
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("insert %v: %v", r, err)
	}
}

// compactJSON marshals v like the transports do (compact, HTML not escaped).
func compactJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// call runs an operation and returns its compact JSON result.
func (e *env) call(name string, params any) ([]byte, error) {
	e.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		e.t.Fatal(err)
	}
	out, err := e.reg.Call(bg, name, raw)
	if err != nil {
		return nil, err
	}
	return compactJSON(e.t, out), nil
}

func (e *env) mustCall(name string, params any) []byte {
	e.t.Helper()
	b, err := e.call(name, params)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return b
}

// page decodes a messages.page result.
func (e *env) page(params map[string]any) []map[string]any {
	e.t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(e.mustCall(api.OpMessagesPage, params), &rows); err != nil {
		e.t.Fatal(err)
	}
	return rows
}
