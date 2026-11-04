package gmail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service is the Gmail core: pooled IMAP connections, caches and the
// operations of server/gmail.py. It is safe for concurrent use.
type Service struct {
	cfg    Config
	log    *slog.Logger
	tls    *tls.Config
	images ImageFetcher
	key    []byte
	now    func() time.Time

	pool *imapPool

	inboxMu    sync.Mutex
	inbox      map[inboxKey]*inboxEntry
	inboxOrder []inboxKey // LRU, oldest first
	inboxEpoch uint64

	abMu    sync.Mutex
	abCache *addrBookCache

	inlineMu    sync.Mutex
	inlineCache map[inlineKey]*inlineEntry
	inlineOrder []inlineKey
	inlineBytes int

	remote remoteImages

	watcher *Watcher
}

// NewService builds a Service. cfg is required.
func NewService(cfg Config, d Deps) *Service {
	key := []byte(cfg.WebToken())
	if len(key) == 0 {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	lg := d.Logger
	if lg == nil {
		lg = slog.Default()
	}
	s := &Service{
		cfg: cfg, log: lg, tls: d.TLS, images: d.Images, key: key,
		now:         time.Now,
		pool:        newPool(),
		inbox:       map[inboxKey]*inboxEntry{},
		inlineCache: map[inlineKey]*inlineEntry{},
	}
	s.remote.init()
	return s
}

// Close releases the IMAP pool and wipes the temporary blobs.
func (s *Service) Close() {
	if s.watcher != nil {
		s.watcher.Stop()
	}
	s.pool.closeAll()
	s.pruneBlobs(0)
}

// ---- settings --------------------------------------------------------------------------------

func (s *Service) loadSettings() (settings, error) {
	addr, pw := s.cfg.GmailAddress(), s.cfg.GmailAppPassword()
	if addr == "" || pw == "" {
		return settings{}, notConfiguredError{}
	}
	return settings{
		Address: addr, Password: pw,
		IMAPHost: s.cfg.GmailIMAPHost(), IMAPPort: s.cfg.GmailIMAPPort(),
		SMTPHost: s.cfg.GmailSMTPHost(), SMTPPort: s.cfg.GmailSMTPPort(),
		Folder:  s.cfg.GmailFolder(),
		Timeout: time.Duration(s.cfg.GmailTimeoutS()) * time.Second,
	}, nil
}

// accountFingerprint is an opaque account key (never log the address).
func accountFingerprint(st settings) string {
	h := sha256.Sum256([]byte(st.Address + "\x00" + st.IMAPHost + "\x00" + itoa(st.IMAPPort)))
	return hex.EncodeToString(h[:])
}

// StatusSnapshot is status_snapshot(): no network.
func (s *Service) StatusSnapshot() (configured bool, address, folder string) {
	configured = s.cfg.GmailEnabled()
	if configured {
		address = s.cfg.GmailAddress()
	}
	return configured, address, s.cfg.GmailFolder()
}

// ---- validation -------------------------------------------------------------------------------

var folderRE = regexp.MustCompile(`^[A-Za-z0-9._/\- ]+$`)

// validateFolder returns a safe mailbox name (nil = configured folder).
func (s *Service) validateFolder(folder *string) (string, error) {
	var cand string
	if folder != nil {
		cand = *folder
	} else {
		cand = s.cfg.GmailFolder()
	}
	cand = pyStrip(cand)
	if cand == "" || cand == "." || cand == ".." || strings.ContainsAny(cand, `*%"`) || !folderRE.MatchString(cand) {
		return "", validation("Cartella non valida")
	}
	return cand, nil
}

// folderOr is validate_folder(folder or settings.folder).
func (s *Service) folderOr(folder *string, st settings) (string, error) {
	if folder == nil || *folder == "" {
		return s.validateFolder(&st.Folder)
	}
	return s.validateFolder(folder)
}

func validateHeaderValue(v, field string) (string, error) {
	if strings.ContainsAny(v, "\r\n") {
		return "", validation("Intestazione " + field + " non valida")
	}
	return pyStrip(v), nil
}

func validateUID(uid int) (int64, error) {
	if uid <= 0 {
		return 0, validation("uid non valido")
	}
	return int64(uid), nil
}

func validateLimit(limit int) (int, error) {
	if limit < 1 || limit > maxLimit {
		return 0, validation("Limit non valido")
	}
	return limit, nil
}

func validateOffset(offset int) (int, error) {
	if offset < 0 {
		return 0, validation("Offset non valido")
	}
	return offset, nil
}

// ---- IMAP pool ----------------------------------------------------------------------------------

type imapPool struct {
	mu    sync.Mutex
	idle  []*imapConn
	fp    string
	gen   int
	sem   chan struct{}
	close bool
}

func newPool() *imapPool { return &imapPool{sem: make(chan struct{}, poolMaxConns)} }

func (p *imapPool) closeAll() {
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.gen++
	p.mu.Unlock()
	for _, c := range idle {
		c.close()
	}
}

func (p *imapPool) ensureFingerprint(fp string) []*imapConn {
	if p.fp == fp {
		return nil
	}
	old := p.idle
	p.idle = nil
	p.fp = fp
	p.gen++
	return old
}

func (s *Service) acquire(ctx context.Context, st settings) (*imapConn, error) {
	p := s.pool
	fp := accountFingerprint(st)
	p.mu.Lock()
	stale := p.ensureFingerprint(fp)
	p.mu.Unlock()
	for _, c := range stale {
		c.close()
	}
	timer := time.NewTimer(poolAcquireTimeout)
	defer timer.Stop()
	select {
	case p.sem <- struct{}{}:
	case <-timer.C:
		return nil, upstream("busy")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-p.sem }
	for {
		p.mu.Lock()
		var c *imapConn
		var reaped []*imapConn
		now := time.Now()
		for len(p.idle) > 0 {
			cand := p.idle[len(p.idle)-1]
			p.idle = p.idle[:len(p.idle)-1]
			if now.Sub(cand.lastUsed) > poolMaxIdle {
				reaped = append(reaped, cand)
				continue
			}
			c = cand
			break
		}
		gen := p.gen
		p.mu.Unlock()
		for _, r := range reaped {
			r.close()
		}
		if c == nil {
			nc, err := dialIMAP(st.IMAPHost, st.IMAPPort, st.Timeout, s.tls)
			if err == nil {
				nc.timeout = st.Timeout
				err = nc.login(st.Address, st.Password)
			}
			if err != nil {
				if nc != nil {
					nc.close()
				}
				release()
				return nil, err
			}
			nc.gen = gen
			return nc, nil
		}
		if time.Since(c.lastUsed) > poolNoopIdle {
			c.timeout = time.Second
			err := c.noop()
			c.timeout = st.Timeout
			if err != nil {
				c.close()
				continue // try the next idle one, or a fresh connection
			}
		}
		c.timeout = st.Timeout
		return c, nil
	}
}

func (s *Service) releaseConn(c *imapConn, st settings, discard bool) {
	p := s.pool
	reusable := false
	p.mu.Lock()
	if !discard && !c.broken && c.conn != nil && c.gen == p.gen && p.fp == accountFingerprint(st) && !p.close {
		reusable = true
		c.lastUsed = time.Now()
		p.idle = append(p.idle, c)
	}
	p.mu.Unlock()
	if !reusable {
		c.close()
	}
	<-p.sem
}

func isKeepError(err error) bool {
	var v *validationError
	var nf *notFoundError
	var nc notConfiguredError
	return errors.As(err, &v) || errors.As(err, &nf) || errors.As(err, &nc)
}

// withConn runs fn on a pooled, authenticated connection.
func (s *Service) withConn(ctx context.Context, st settings, fn func(c *imapConn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := s.acquire(ctx, st)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() {
		if c.conn != nil {
			_ = c.conn.SetDeadline(time.Now())
		}
	})
	err = fn(c)
	stop()
	s.releaseConn(c, st, err != nil && !isKeepError(err))
	if err == nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
	}
	return err
}

// cmd runs a command mapping a NO/BAD completion to upstream(failMsg).
func (c *imapConn) cmd(failMsg, name string, args ...cmdArg) (*imapResult, error) {
	res, err := c.command(name, args...)
	if err != nil {
		var se *imapStatusError
		if errors.As(err, &se) {
			return res, upstream(failMsg)
		}
		return res, err
	}
	return res, nil
}

func (c *imapConn) selectOrFail(folder string, readonly bool) error {
	if err := c.selectFolder(folder, readonly); err != nil {
		var se *imapStatusError
		if errors.As(err, &se) {
			return upstream("Cartella non disponibile")
		}
		return err
	}
	return nil
}

// ---- inbox list cache ---------------------------------------------------------------------------

type inboxKey struct{ folder, fp string }

type listMsg struct {
	UID            int
	From, To       string
	Subject        string
	DateTS         *int64
	HasAttachments bool
	Seen           bool
	Flags          []string
}

type inboxEntry struct {
	uidvalidity int64
	messages    []listMsg
	expires     time.Time
}

func canonicalFlags(flags []string) []string {
	set := map[string]bool{}
	for _, f := range flags {
		set[f] = true
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func addFlag(flags []string, flag string) []string {
	cur := append([]string(nil), flags...)
	low := strings.ToLower(flag)
	for _, e := range cur {
		if strings.ToLower(e) == low {
			return cur
		}
	}
	i := 0
	for i < len(cur) && cur[i] <= flag {
		i++
	}
	cur = append(cur, "")
	copy(cur[i+1:], cur[i:])
	cur[i] = flag
	return cur
}

func removeFlag(flags []string, flag string) []string {
	low := strings.ToLower(flag)
	out := []string{}
	for _, e := range flags {
		if strings.ToLower(e) != low {
			out = append(out, e)
		}
	}
	return out
}

func (s *Service) inboxTouchLocked(k inboxKey) {
	for i, o := range s.inboxOrder {
		if o == k {
			s.inboxOrder = append(s.inboxOrder[:i], s.inboxOrder[i+1:]...)
			break
		}
	}
	s.inboxOrder = append(s.inboxOrder, k)
}

func (s *Service) inboxDeleteLocked(k inboxKey) {
	delete(s.inbox, k)
	for i, o := range s.inboxOrder {
		if o == k {
			s.inboxOrder = append(s.inboxOrder[:i], s.inboxOrder[i+1:]...)
			break
		}
	}
}

func (s *Service) inboxGetLocked(k inboxKey, now time.Time) *inboxEntry {
	e := s.inbox[k]
	if e == nil {
		return nil
	}
	if !e.expires.After(now) {
		s.inboxDeleteLocked(k)
		return nil
	}
	s.inboxTouchLocked(k)
	return e
}

func (s *Service) inboxPutLocked(k inboxKey, e *inboxEntry, epoch0 uint64) bool {
	if s.inboxEpoch != epoch0 {
		return false
	}
	s.inbox[k] = e
	s.inboxTouchLocked(k)
	for len(s.inboxOrder) > inboxCacheMaxEntries {
		oldest := s.inboxOrder[0]
		s.inboxOrder = s.inboxOrder[1:]
		delete(s.inbox, oldest)
	}
	return true
}

func (s *Service) invalidateInboxAll() {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inboxEpoch++
	s.inbox = map[inboxKey]*inboxEntry{}
	s.inboxOrder = nil
}

// ClearInboxCache drops every cached inbox window.
func (s *Service) ClearInboxCache() { s.invalidateInboxAll() }

func (s *Service) invalidateInbox(k inboxKey) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inboxEpoch++
	s.inboxDeleteLocked(k)
}

func (s *Service) setSeenCached(k inboxKey, uid int, seen bool) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inboxEpoch++
	e := s.inbox[k]
	if e == nil {
		return
	}
	msgs := make([]listMsg, len(e.messages))
	copy(msgs, e.messages)
	for i := range msgs {
		if msgs[i].UID == uid && msgs[i].Seen != seen {
			if seen {
				msgs[i].Flags = canonicalFlags(addFlag(msgs[i].Flags, `\Seen`))
			} else {
				msgs[i].Flags = canonicalFlags(removeFlag(msgs[i].Flags, `\Seen`))
			}
			msgs[i].Seen = seen
		}
	}
	ne := *e
	ne.messages = msgs
	s.inbox[k] = &ne
}

// ---- blobs ----------------------------------------------------------------------------------------

const (
	blobDirName = "gmail-blobs"
	blobMaxAge  = 15 * time.Minute
)

func (s *Service) blobDir() string { return filepath.Join(s.cfg.CacheDir(), blobDirName) }

// pruneBlobs deletes temporary blobs older than maxAge (0 wipes all).
func (s *Service) pruneBlobs(maxAge time.Duration) {
	entries, err := os.ReadDir(s.blobDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if maxAge <= 0 || info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.blobDir(), e.Name()))
		}
	}
}

// writeBlob stores data as a private temporary file and returns its path.
func (s *Service) writeBlob(data []byte) (string, error) {
	dir := s.blobDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	s.pruneBlobs(blobMaxAge)
	f, err := os.CreateTemp(dir, "blob-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
