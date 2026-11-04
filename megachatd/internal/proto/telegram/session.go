package telegram

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Session kinds reported by DetectSessionKind.
const (
	SessionNone   = ""
	SessionSQLite = "sqlite"
	SessionString = "string"
)

var sqliteMagic = []byte("SQLite format 3\x00")

// ImportedSession is a Telethon session converted for gotd.
type ImportedSession struct {
	Kind string // SessionSQLite or SessionString
	Data *session.Data
	// Peers are the (marked id -> access hash) rows of Telethon's entities
	// table (SQLite sessions only).
	Peers map[int64]int64
	// UpdateState is Telethon's update_state table keyed by id (0 = account).
	UpdateState map[int64]updates.State
}

// DetectSessionKind inspects path without modifying it.
func DetectSessionKind(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionNone, err
	}
	defer f.Close()
	head := make([]byte, len(sqliteMagic))
	n, _ := f.Read(head)
	if n == len(sqliteMagic) && bytes.Equal(head, sqliteMagic) {
		return SessionSQLite, nil
	}
	return SessionString, nil
}

// LoadTelethonSession reads a Telethon session file (SQLite ".session" file
// or a StringSession text file) and converts it to a gotd session.Data. The
// file is only ever opened read-only.
func LoadTelethonSession(path string) (*ImportedSession, error) {
	kind, err := DetectSessionKind(path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case SessionSQLite:
		return loadTelethonSQLite(path)
	default:
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data, err := ParseStringSession(string(raw))
		if err != nil {
			return nil, err
		}
		return &ImportedSession{Kind: SessionString, Data: data}, nil
	}
}

// ParseStringSession converts a Telethon StringSession (version byte '1' +
// url-safe base64) with gotd's session.TelethonSession.
func ParseStringSession(s string) (*session.Data, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("telegram: empty string session")
	}
	return session.TelethonSession(s)
}

// buildData makes a session.Data from Telethon's sessions-table columns.
func buildData(dc int, host string, port int, key []byte) (*session.Data, error) {
	if len(key) != 256 {
		return nil, fmt.Errorf("telegram: telethon auth_key has %d bytes, want 256", len(key))
	}
	var k crypto.Key
	copy(k[:], key)
	id := k.WithID().ID
	return &session.Data{
		DC:        dc,
		Addr:      net.JoinHostPort(host, strconv.Itoa(port)),
		AuthKey:   k[:],
		AuthKeyID: id[:],
	}, nil
}

func loadTelethonSQLite(path string) (*ImportedSession, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// mode=ro: never write to the Telethon file.
	db, err := sql.Open("sqlite", "file:"+abs+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var (
		dc   int
		host string
		port int
		key  []byte
	)
	err = db.QueryRow("SELECT dc_id, server_address, port, auth_key FROM sessions LIMIT 1").
		Scan(&dc, &host, &port, &key)
	if err != nil {
		return nil, fmt.Errorf("telegram: reading telethon sessions table: %w", err)
	}
	if len(key) == 0 {
		return nil, errors.New("telegram: telethon session has no auth_key (not logged in)")
	}
	data, err := buildData(dc, host, port, key)
	if err != nil {
		return nil, err
	}
	out := &ImportedSession{Kind: SessionSQLite, Data: data, Peers: map[int64]int64{}, UpdateState: map[int64]updates.State{}}

	if rows, err := db.Query("SELECT id, hash FROM entities"); err == nil {
		for rows.Next() {
			var id, hash sql.NullInt64
			if rows.Scan(&id, &hash) == nil && id.Valid && hash.Valid {
				out.Peers[id.Int64] = hash.Int64
			}
		}
		rows.Close()
	}
	if rows, err := db.Query("SELECT id, pts, qts, date, seq FROM update_state"); err == nil {
		for rows.Next() {
			var id, pts, qts, date, seq sql.NullInt64
			if rows.Scan(&id, &pts, &qts, &date, &seq) == nil && id.Valid {
				out.UpdateState[id.Int64] = updates.State{Pts: int(pts.Int64), Qts: int(qts.Int64), Date: int(date.Int64), Seq: int(seq.Int64)}
			}
		}
		rows.Close()
	}
	return out, nil
}

// gotdSessionPath is where the converted session lives, next to the Telethon
// file: telegram.session -> telegram.gotd.json.
func gotdSessionPath(telethonPath string) string {
	return strings.TrimSuffix(telethonPath, ".session") + ".gotd.json"
}

// peersPath is the access-hash cache file next to the gotd session.
func peersPath(telethonPath string) string {
	return strings.TrimSuffix(telethonPath, ".session") + ".gotd-peers.json"
}

// ImportSession makes sure a gotd session exists for the Telethon session at
// telethonPath. When the gotd file already exists nothing is converted
// (imported=false). The Telethon file is never modified. The imported peers
// and update state, if any, are returned for seeding.
func ImportSession(ctx context.Context, telethonPath string) (storage *session.FileStorage, imp *ImportedSession, imported bool, err error) {
	gp := gotdSessionPath(telethonPath)
	storage = &session.FileStorage{Path: gp}
	if _, serr := os.Stat(gp); serr == nil {
		return storage, nil, false, nil
	}
	if _, serr := os.Stat(telethonPath); serr != nil {
		return storage, nil, false, nil // nothing to import: fresh login needed
	}
	imp, err = LoadTelethonSession(telethonPath)
	if err != nil {
		return storage, nil, false, err
	}
	if err = os.MkdirAll(filepath.Dir(gp), 0o700); err != nil {
		return storage, nil, false, err
	}
	loader := session.Loader{Storage: storage}
	if err = loader.Save(ctx, imp.Data); err != nil {
		return storage, nil, false, err
	}
	return storage, imp, true, nil
}

// ---- peer (access hash) cache -----------------------------------------------

// peerStore caches access hashes by marked id; it replaces Telethon's
// entities table and is persisted as JSON next to the gotd session.
type peerStore struct {
	mu     sync.Mutex
	path   string
	hashes map[int64]int64
	dirty  bool
}

func newPeerStore(path string) *peerStore {
	p := &peerStore{path: path, hashes: map[int64]int64{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			m := map[string]int64{}
			if json.Unmarshal(raw, &m) == nil {
				for k, v := range m {
					if id, err := strconv.ParseInt(k, 10, 64); err == nil {
						p.hashes[id] = v
					}
				}
			}
		}
	}
	return p
}

func (p *peerStore) get(id int64) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.hashes[id]
	return h, ok
}

func (p *peerStore) set(id, hash int64) {
	if hash == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hashes[id] != hash {
		p.hashes[id] = hash
		p.dirty = true
	}
}

func (p *peerStore) merge(m map[int64]int64) {
	for k, v := range m {
		p.mu.Lock()
		_, have := p.hashes[k]
		p.mu.Unlock()
		if !have {
			p.set(k, v)
		}
	}
}

func (p *peerStore) learnUser(u *tg.User) {
	if u != nil && !u.Min {
		p.set(markedID(PeerUser, u.ID), u.AccessHash)
	}
}

func (p *peerStore) learnChannel(c *tg.Channel) {
	if c != nil && !c.Min {
		p.set(markedID(PeerChannel, c.ID), c.AccessHash)
	}
}

func (p *peerStore) learnEntities(e tg.Entities) {
	for _, u := range e.Users {
		p.learnUser(u)
	}
	for _, c := range e.Channels {
		p.learnChannel(c)
	}
}

func (p *peerStore) learnLists(users []tg.UserClass, chats []tg.ChatClass) {
	for _, u := range users {
		if uu, ok := u.(*tg.User); ok {
			p.learnUser(uu)
		}
	}
	for _, c := range chats {
		if cc, ok := c.(*tg.Channel); ok {
			p.learnChannel(cc)
		}
	}
}

// flush writes the cache when it changed (best effort).
func (p *peerStore) flush() {
	p.mu.Lock()
	if !p.dirty || p.path == "" {
		p.mu.Unlock()
		return
	}
	m := make(map[string]int64, len(p.hashes))
	for k, v := range p.hashes {
		m[strconv.FormatInt(k, 10)] = v
	}
	p.dirty = false
	path := p.path
	p.mu.Unlock()
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// ---- update state storage -------------------------------------------------------

// stateStorage is an updates.StateStorage persisted as JSON.
type stateStorage struct {
	mu       sync.Mutex
	path     string
	users    map[int64]updates.State
	channels map[[2]int64]int
}

type stateFile struct {
	Users    map[string]updates.State `json:"users"`
	Channels map[string]int           `json:"channels"`
}

func newStateStorage(path string) *stateStorage {
	s := &stateStorage{path: path, users: map[int64]updates.State{}, channels: map[[2]int64]int{}}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f stateFile
	if json.Unmarshal(raw, &f) != nil {
		return s
	}
	for k, v := range f.Users {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			s.users[id] = v
		}
	}
	for k, v := range f.Channels {
		parts := strings.SplitN(k, ":", 2)
		if len(parts) == 2 {
			a, e1 := strconv.ParseInt(parts[0], 10, 64)
			b, e2 := strconv.ParseInt(parts[1], 10, 64)
			if e1 == nil && e2 == nil {
				s.channels[[2]int64{a, b}] = v
			}
		}
	}
	return s
}

func (s *stateStorage) saveLocked() {
	if s.path == "" {
		return
	}
	f := stateFile{Users: map[string]updates.State{}, Channels: map[string]int{}}
	for k, v := range s.users {
		f.Users[strconv.FormatInt(k, 10)] = v
	}
	for k, v := range s.channels {
		f.Channels[strconv.FormatInt(k[0], 10)+":"+strconv.FormatInt(k[1], 10)] = v
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

var errNoState = errors.New("telegram: user state does not exist")

func (s *stateStorage) GetState(_ context.Context, userID int64) (updates.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.users[userID]
	return st, ok, nil
}

func (s *stateStorage) SetState(_ context.Context, userID int64, state updates.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[userID] = state
	s.saveLocked()
	return nil
}

func (s *stateStorage) mutate(userID int64, f func(*updates.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.users[userID]
	if !ok {
		return errNoState
	}
	f(&st)
	s.users[userID] = st
	s.saveLocked()
	return nil
}

func (s *stateStorage) SetPts(_ context.Context, u int64, v int) error {
	return s.mutate(u, func(st *updates.State) { st.Pts = v })
}
func (s *stateStorage) SetQts(_ context.Context, u int64, v int) error {
	return s.mutate(u, func(st *updates.State) { st.Qts = v })
}
func (s *stateStorage) SetDate(_ context.Context, u int64, v int) error {
	return s.mutate(u, func(st *updates.State) { st.Date = v })
}
func (s *stateStorage) SetSeq(_ context.Context, u int64, v int) error {
	return s.mutate(u, func(st *updates.State) { st.Seq = v })
}
func (s *stateStorage) SetDateSeq(_ context.Context, u int64, date, seq int) error {
	return s.mutate(u, func(st *updates.State) { st.Date, st.Seq = date, seq })
}

func (s *stateStorage) GetChannelPts(_ context.Context, userID, channelID int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.channels[[2]int64{userID, channelID}]
	return v, ok, nil
}

func (s *stateStorage) SetChannelPts(_ context.Context, userID, channelID int64, pts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[[2]int64{userID, channelID}] = pts
	s.saveLocked()
	return nil
}

func (s *stateStorage) ForEachChannels(ctx context.Context, userID int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	s.mu.Lock()
	type kv struct {
		id  int64
		pts int
	}
	var list []kv
	for k, v := range s.channels {
		if k[0] == userID {
			list = append(list, kv{k[1], v})
		}
	}
	s.mu.Unlock()
	for _, e := range list {
		if err := f(ctx, e.id, e.pts); err != nil {
			return err
		}
	}
	return nil
}

// seed stores the Telethon account state for userID when none exists yet, so
// the first run catches up on messages missed while Python was the client.
func (s *stateStorage) seed(userID int64, imp *ImportedSession) {
	if imp == nil || len(imp.UpdateState) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; ok {
		return
	}
	st, ok := imp.UpdateState[0]
	if !ok || st.Pts == 0 {
		return
	}
	s.users[userID] = st
	s.saveLocked()
}
