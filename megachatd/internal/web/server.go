// Package web is the HTTP/WebSocket adapter of the Go server: a port of
// web/server.py, api.py, gmail_api.py, ws.py, auth.py, ops.py,
// legacy_events.py and uploads.py. Every REST route is a thin translation to
// one ops.Dispatcher operation; domain decisions live in the operations.
package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// Timing constants of ws.py.
const (
	HeartbeatInterval = 5 * time.Second
	SubscribeGrace    = 500 * time.Millisecond
	gmailImageConc    = 4
)

// QRRenderer renders the data of a link QR (kind url/text) as PNG bytes.
// TODO(mc-zao.2.3): no QR library is in go.mod; until a renderer is supplied
// (WithQRRenderer) url/text QRs are returned unconverted.
type QRRenderer interface {
	PNG(data string) ([]byte, error)
}

// UploadGuard supplies the incremental per-file limit applied while a
// multipart file is copied (server.uploads.UploadGuard). It is domain logic
// (media sniffing, per-kind caps) that lives outside web; optional. The
// returned function is fed every chunk and returns an error to reject the
// file as too large. maxBytes < 0 means no per-file cap; mediaLimits is false
// for mail attachments.
type UploadGuard interface {
	NewGuard(filename, contentType string, maxBytes int64, mediaLimits bool) func(chunk []byte) error
}

// clientCounterSetter is implemented by *ops.Registry.
type clientCounterSetter interface {
	SetClientCounter(adapter string, f func() int)
}

// Option configures a Server.
type Option func(*Server)

// WithToken overrides the Bearer token (default cfg.WebToken()).
func WithToken(t string) Option { return func(s *Server) { s.token = t } }

// WithNoAuth disables all authentication (--web-no-auth).
func WithNoAuth() Option { return func(s *Server) { s.authRequired = false } }

// WithHost overrides the bind host setting (comma list allowed).
func WithHost(h string) Option { return func(s *Server) { s.host = h } }

// WithPort overrides the port (default cfg.WebPort()).
func WithPort(p int) Option { return func(s *Server) { s.port = p } }

// WithStaticFS serves the SPA from fsys instead of <ProjectDir>/web/static.
func WithStaticFS(fsys fs.FS) Option { return func(s *Server) { s.static = fsys } }

// WithUploadDir overrides the upload staging directory.
func WithUploadDir(d string) Option { return func(s *Server) { s.uploadDir = d } }

// WithQRRenderer installs the PNG renderer for link QR codes.
func WithQRRenderer(q QRRenderer) Option { return func(s *Server) { s.qr = q } }

// WithUploadGuard installs the per-file upload guard.
func WithUploadGuard(g UploadGuard) Option { return func(s *Server) { s.guard = g } }

// WithTimers overrides the heartbeat interval and the /ws/v2 subscribe grace
// period (tests).
func WithTimers(heartbeat, grace time.Duration) Option {
	return func(s *Server) { s.heartbeat, s.grace = heartbeat, grace }
}

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.log = l } }

// Server is the web adapter.
type Server struct {
	cfg  *config.Config
	d    ops.Dispatcher
	ev   ops.EventSource
	log  *slog.Logger
	http http.Handler

	token        string
	authRequired bool
	host         string
	port         int
	static       fs.FS
	uploadDir    string
	qr           QRRenderer
	guard        UploadGuard
	heartbeat    time.Duration
	grace        time.Duration

	imageSem chan struct{}

	mu      sync.Mutex
	legacy  map[*wsClient]struct{}
	v2      map[*websocket.Conn]struct{}
	hubStop chan struct{}
	lns     []net.Listener
}

// NewServer builds the adapter. d runs every operation; ev feeds /ws and
// /ws/v2 (may be nil: sockets then only send heartbeats).
func NewServer(cfg *config.Config, d ops.Dispatcher, ev ops.EventSource, opts ...Option) *Server {
	s := &Server{
		cfg: cfg, d: d, ev: ev, log: slog.Default(),
		authRequired: true, heartbeat: HeartbeatInterval, grace: SubscribeGrace,
		imageSem: make(chan struct{}, gmailImageConc),
		legacy:   map[*wsClient]struct{}{}, v2: map[*websocket.Conn]struct{}{},
	}
	if cfg != nil {
		s.token = cfg.WebToken()
		s.host = cfg.WebHost()
		s.port = cfg.WebPort()
		s.uploadDir = filepath.Join(cfg.CacheDir(), "web-uploads")
		s.static = os.DirFS(filepath.Join(cfg.ProjectDir, "web", "static"))
	} else {
		s.host, s.port = api.DefaultWebHost, api.DefaultWebPort
	}
	for _, o := range opts {
		o(s)
	}
	if s.uploadDir == "" {
		s.uploadDir = filepath.Join(os.TempDir(), "megachat-web-uploads")
	}
	if cs, ok := d.(clientCounterSetter); ok {
		cs.SetClientCounter("web", s.ClientCount)
	}
	s.http = s.buildHandler()
	return s
}

// Handler is the full HTTP handler (middleware, routes, sockets, static).
func (s *Server) Handler() http.Handler { return s.http }

// ClientCount is the number of live /ws and /ws/v2 connections.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.legacy) + len(s.v2)
}

// ParseHosts splits a bind host setting (web/server.py parse_hosts).
func ParseHosts(host string) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, part := range strings.Split(host, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			hosts = append(hosts, part)
		}
	}
	if len(hosts) == 0 {
		return []string{"0.0.0.0"}
	}
	return hosts
}

func listenNetwork(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return "tcp4"
		}
		return "tcp6"
	}
	return "tcp"
}

// Listen validates the settings and binds every configured host. Serve (or
// ListenAndServe) then accepts connections; splitting the two lets a host
// report a bind failure synchronously.
func (s *Server) Listen(ctx context.Context) error {
	if s.authRequired && s.token == "" {
		return errors.New("web UI requires a Bearer token; configure SIGNAL_TUI_WEB_TOKEN, web.token, or disable auth (web down)")
	}
	if s.port < 1 || s.port > 65535 {
		return fmt.Errorf("invalid web server port %d (web down)", s.port)
	}
	s.prepareUploadDir()
	var lns []net.Listener
	var lc net.ListenConfig
	for _, h := range ParseHosts(s.host) {
		ln, err := lc.Listen(ctx, listenNetwork(h), net.JoinHostPort(h, strconv.Itoa(s.port)))
		if err != nil {
			for _, l := range lns {
				l.Close()
			}
			return fmt.Errorf("web server failed to bind %s:%d (port may be in use): %w", h, s.port, err)
		}
		lns = append(lns, ln)
	}
	s.mu.Lock()
	s.lns = lns
	s.mu.Unlock()
	return nil
}

// ListenAndServe binds every configured host and serves until ctx is
// cancelled (graceful stop, WebSockets are closed) or a listener fails.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.Listen(ctx); err != nil {
		return err
	}
	return s.Serve(ctx)
}

// Serve serves on the listeners bound by Listen until ctx is cancelled or a
// listener fails.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	lns := s.lns
	s.lns = nil
	s.mu.Unlock()
	if len(lns) == 0 {
		return errors.New("web server: Listen has not been called")
	}
	srv := &http.Server{Handler: s.http, ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func() { errc <- srv.Serve(ln) }()
	}
	if s.authRequired {
		s.log.Info("web server listening", "hosts", s.host, "port", s.port)
	} else {
		s.log.Warn("web server listening with NO AUTH", "hosts", s.host, "port", s.port)
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	s.closeSockets()
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (s *Server) buildHandler() http.Handler {
	r := chi.NewRouter()
	s.routes(r)
	r.Get(api.RouteHealth, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, 200, api.HealthResponse{Status: "ok", Port: s.port, AuthRequired: s.authRequired})
	})
	r.Get(api.RouteWS, s.handleWS)
	r.Get(api.RouteWSV2, s.handleWSV2)
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, 405, api.HTTPError{Detail: "Method Not Allowed"})
	})
	r.NotFound(s.serveStatic)

	var h http.Handler = r
	h = s.authMiddleware(h)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil && rec != http.ErrAbortHandler {
				s.log.Error("web handler panic", "panic", rec, "path", req.URL.Path)
				http.Error(w, "Internal Server Error", 500)
			}
		}()
		// Route on the decoded path, like Starlette.
		u := *req.URL
		u.RawPath = ""
		req = req.Clone(req.Context())
		req.URL = &u
		if u.Path == "/" || u.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, req)
	})
}
