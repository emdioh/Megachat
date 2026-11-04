package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// Wire limits (server/socket_adapter.py), re-exported from internal/api.
const (
	MaxLineBytes       = api.MaxLineBytes
	DefaultTimeout     = 120 * time.Second
	MaxTimeout         = 600 * time.Second
	MaxConnections     = api.MaxConnections
	MaxInflight        = api.MaxInflightPerConnection
	WriteTimeout       = 30 * time.Second
	HeartbeatInterval  = 5 * time.Second
	MaxSocketPathBytes = api.MaxSocketPathBytes
	drainOnClose       = time.Second
	stopWait           = 2 * time.Second
)

// PreCall is an optional hook run inside an operation call before the
// dispatcher (attachment staging lives here). It may rewrite params and
// returns a cleanup run when the call ends.
type PreCall func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, func(), error)

type config struct {
	log          *slog.Logger
	maxConns     int
	maxInflight  int
	maxLine      int
	defTimeout   time.Duration
	heartbeat    time.Duration
	writeTimeout time.Duration
	preCall      PreCall
}

// Option configures a Server.
type Option func(*config)

func WithLogger(l *slog.Logger) Option          { return func(c *config) { c.log = l } }
func WithMaxConnections(n int) Option           { return func(c *config) { c.maxConns = n } }
func WithMaxInflight(n int) Option              { return func(c *config) { c.maxInflight = n } }
func WithMaxLineBytes(n int) Option             { return func(c *config) { c.maxLine = n } }
func WithDefaultTimeout(d time.Duration) Option { return func(c *config) { c.defTimeout = d } }
func WithHeartbeatInterval(d time.Duration) Option {
	return func(c *config) { c.heartbeat = d }
}
func WithWriteTimeout(d time.Duration) Option { return func(c *config) { c.writeTimeout = d } }
func WithPreCall(f PreCall) Option            { return func(c *config) { c.preCall = f } }

// Server is the Unix-socket JSON-RPC adapter.
type Server struct {
	path    string
	runtime string
	d       ops.Dispatcher
	ev      ops.EventSource
	cfg     config

	mu       sync.Mutex
	ln       net.Listener
	listened bool
	stopping bool
	conns    map[*conn]struct{}
	identity [2]uint64
	haveID   bool

	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once
	opCtx     context.Context
	opCancel  context.CancelFunc
	opsWG     sync.WaitGroup
	connWG    sync.WaitGroup
	startedAt float64
}

// NewServer builds a server for the socket at path (see Listen/Serve).
func NewServer(path string, d ops.Dispatcher, ev ops.EventSource, opts ...Option) *Server {
	cfg := config{
		log: slog.Default(), maxConns: MaxConnections, maxInflight: MaxInflight,
		maxLine: MaxLineBytes, defTimeout: DefaultTimeout, heartbeat: HeartbeatInterval,
		writeTimeout: WriteTimeout,
	}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Server{path: path, d: d, ev: ev, cfg: cfg, conns: map[*conn]struct{}{},
		stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	s.opCtx, s.opCancel = context.WithCancel(context.Background())
	return s
}

// Path is the absolute socket path (valid after NewServer).
func (s *Server) Path() string {
	if abs, err := filepath.Abs(s.path); err == nil {
		return abs
	}
	return s.path
}

// ClientCount is the number of live connections (server.clients).
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

type counterSetter interface {
	SetClientCounter(adapter string, f func() int)
}

// Listen prepares the directory, clears a stale socket, binds (0600) and
// writes the runtime file. Idempotent. Serve calls it when needed.
func (s *Server) Listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listened {
		return nil
	}
	if s.stopping {
		return errors.New("rpc: server closed")
	}
	path := s.Path()
	if len(path) > MaxSocketPathBytes {
		return fmt.Errorf("socket path too long: %s", path)
	}
	for _, b := range api.BuiltinMethods {
		if s.d.Has(b) {
			return fmt.Errorf("operations shadow builtin methods: %s", b)
		}
	}
	if err := prepareDirectory(path, s.cfg.log); err != nil {
		return err
	}
	runtime := api.RuntimePathFor(path)
	if err := clearStale(path, runtime, s.cfg.log); err != nil {
		return err
	}
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if fi, err := os.Lstat(path); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			s.identity = [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			s.haveID = true
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		s.removeFilesLocked()
		return err
	}
	s.startedAt = float64(time.Now().UnixNano()) / 1e9
	s.runtime = runtime
	if err := writeRuntime(path, runtime, s.startedAt); err != nil {
		ln.Close()
		s.removeFilesLocked()
		return err
	}
	s.ln = ln
	s.listened = true
	if cs, ok := s.d.(counterSetter); ok {
		cs.SetClientCounter("socket", s.ClientCountLocked)
	}
	s.cfg.log.Info("Socket adapter listening", "path", path)
	return nil
}

// ClientCountLocked is ClientCount without taking the lock-free path issues:
// it takes the lock itself (name kept for the counter closure).
func (s *Server) ClientCountLocked() int { return s.ClientCount() }

// Serve listens (if needed) and accepts until ctx is done or Close is called.
// It returns after a complete shutdown.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.Listen(); err != nil {
		return err
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.stopCh:
		}
	}()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				<-s.doneCh
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			s.Close()
			return err
		}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			s.onConnect(nc)
		}()
	}
}

// Close stops accepting, removes the socket and runtime file, drops
// connections and cancels running operations. Idempotent; safe from an op.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.stopping = true
		close(s.stopCh)
		ln := s.ln
		wasListening := s.listened
		if wasListening {
			s.removeFilesLocked() // clients stop finding us before we close
		}
		conns := make([]*conn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.mu.Unlock()
		if ln != nil {
			ln.Close()
		}
		if cs, ok := s.d.(counterSetter); ok && wasListening {
			cs.SetClientCounter("socket", nil)
		}
		for _, c := range conns { // writes are synchronous: nothing left to flush
			c.close()
		}
		s.opCancel()
		waitTimeout(&s.opsWG, stopWait)
		waitTimeout(&s.connWG, stopWait)
		close(s.doneCh)
	})
	<-s.doneCh
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	select {
	case <-ch:
	case <-time.After(d):
	}
}

func (s *Server) removeFilesLocked() {
	if fi, err := os.Lstat(s.path2()); err == nil && s.haveID {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && [2]uint64{uint64(st.Dev), uint64(st.Ino)} == s.identity {
			os.Remove(s.path2())
		}
	}
	if s.runtime != "" {
		if b, err := os.ReadFile(s.runtime); err == nil {
			var rf api.RuntimeFile
			if json.Unmarshal(b, &rf) == nil && rf.PID == os.Getpid() {
				os.Remove(s.runtime)
			}
		}
	}
}

func (s *Server) path2() string { return s.Path() }

// -- filesystem preparation ----------------------------------------------------

func prepareDirectory(path string, log *slog.Logger) error {
	parent := filepath.Dir(path)
	_, statErr := os.Stat(parent)
	created := statErr != nil
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("cannot prepare %s: %w", parent, err)
	}
	fi, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("cannot prepare %s: %w", parent, err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by another user", parent)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if created || filepath.Base(parent) == "megachat" {
			return os.Chmod(parent, 0o700)
		}
		log.Warn("Socket directory is accessible by others; the socket itself is 0600", "dir", parent)
	}
	return nil
}

func probeLive(path string) (bool, error) {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		c.Close()
		return true, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true, nil // accept backlog full: somebody is there
	}
	return false, fmt.Errorf("cannot probe %s: %w", path, err)
}

func clearStale(path, runtime string, log *slog.Logger) error {
	fi, err := os.Lstat(path)
	if err == nil {
		st, _ := fi.Sys().(*syscall.Stat_t)
		if fi.Mode()&os.ModeSocket == 0 || (st != nil && int(st.Uid) != os.Geteuid()) {
			return fmt.Errorf("%s exists and is not our socket", path)
		}
		live, perr := probeLive(path)
		if perr != nil {
			return perr
		}
		if live {
			return fmt.Errorf("another server is already listening on %s", path)
		}
		log.Info("Removing stale socket", "path", path)
		os.Remove(path)
	}
	os.Remove(runtime)
	return nil
}

func writeRuntime(sock, runtime string, startedAt float64) error {
	b, err := marshal(struct {
		PID        int       `json:"pid"`
		Socket     string    `json:"socket"`
		Version    string    `json:"version"`
		APIVersion int       `json:"api_version"`
		StartedAt  jsonFloat `json:"started_at"`
	}{os.Getpid(), sock, api.ServerVersion, api.APIVersion, jsonFloat(startedAt)})
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(runtime), fmt.Sprintf(".%s.%d.tmp", filepath.Base(runtime), os.Getpid()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, runtime)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// helper shared by hello.
func sortedNames(d ops.Dispatcher) []string {
	n := append([]string{}, d.Names()...)
	sort.Strings(n)
	return n
}
