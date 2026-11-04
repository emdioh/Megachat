package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/gmail"
	"github.com/emdioh/megachat/megachatd/internal/linking"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/messages"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/signal"
	"github.com/emdioh/megachat/megachatd/internal/proto/telegram"
	"github.com/emdioh/megachat/megachatd/internal/proto/whatsapp"
	"github.com/emdioh/megachat/megachatd/internal/rpc"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"github.com/emdioh/megachat/megachatd/internal/transcribe"
	"github.com/emdioh/megachat/megachatd/internal/web"
)

// BuildOptions are the inputs of Build.
type BuildOptions struct {
	Config *config.Config
	Log    *slog.Logger
	// LogBuffer backs server.logs (nil: a fresh one).
	LogBuffer *core.LogBuffer
	// ExtraBackends are registered after (and instead of nothing else) the
	// configured ones; tests pass the fake backend here together with
	// OnlyExtraBackends.
	ExtraBackends []proto.Backend
	// OnlyExtraBackends skips the config-driven signal/whatsapp/telegram
	// registration (tests).
	OnlyExtraBackends bool
	// DownloadPort overrides the download server port (tests; 0 = default).
	DownloadPort int
}

// App is the wired server: every component, not yet running.
type App struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Store    *store.Store
	Bus      *bus.Bus
	Manager  *proto.Manager
	Core     *core.Core
	Registry *ops.Registry

	Signal   *signal.Backend
	WhatsApp *whatsapp.Backend // nil unless enabled
	Telegram *telegram.Backend // nil unless configured

	Linking    *linking.Service
	Transcribe *transcribe.Service
	Gmail      *gmail.Service
	Media      *media.Deps
	Downloads  *downloader

	UploadDir string

	// Notices are one-time user-facing messages (Italian) collected by Build
	// and printed by Start, e.g. the WhatsApp re-link notice.
	Notices []string

	stopOnce sync.Once
}

// whatsappRelinkMarker records that the one-time re-link notice was shown.
const whatsappRelinkMarker = ".whatsapp-relink-notice"

// WhatsAppRelinkNotice is the one-time migration message: the old WAHA session
// cannot be carried over to whatsmeow, so WhatsApp must be linked again by QR.
const WhatsAppRelinkNotice = "ℹ️  WhatsApp: la sessione WAHA non è migrabile. " +
	"Collega di nuovo WhatsApp con un QR (python3 link_whatsapp.py, oppure dalla TUI: " +
	"Collega dispositivo). Telegram riusa la sessione esistente."

// whatsappRelinkNeeded reports whether the notice is due: WhatsApp is enabled,
// no whatsmeow.db exists yet and the notice was not shown before. It records
// the marker, so it returns true at most once per data directory.
func whatsappRelinkNeeded(cfg *config.Config) bool {
	dir := cfg.DataDir()
	if _, err := os.Stat(filepath.Join(dir, "whatsmeow.db")); err == nil {
		return false
	}
	marker := filepath.Join(dir, whatsappRelinkMarker)
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		_ = os.WriteFile(marker, []byte("shown\n"), 0o644)
	}
	return true
}

// whatsappEnabled decides whether the WhatsApp backend is registered. Python
// registered it when a WAHA URL was configured or a local WAHA answered; the Go
// backend is native (whatsmeow) and has no service to probe, so it is enabled
// by an explicit WHATSAPP_ENABLED (environment, config.json "whatsapp_enabled"
// or .env), by a previously paired device store (<data>/whatsmeow.db), or by a
// legacy WHATSAPP_API_URL (an existing WAHA install being migrated).
func whatsappEnabled(cfg *config.Config) bool {
	if v, ok := lookupBool(cfg.Env("WHATSAPP_ENABLED")); ok {
		return v
	}
	if raw, ok := cfg.LoadFile()["whatsapp_enabled"]; ok {
		switch t := raw.(type) {
		case bool:
			return t
		case string:
			if v, ok := lookupBool(t); ok {
				return v
			}
		}
	}
	// The documented way to enable WhatsApp is `echo WHATSAPP_ENABLED=1 >> .env`
	// (README, install.sh, link_whatsapp.py); the same source as SIGNAL_ENABLED.
	if v, ok := lookupBool(cfg.LoadDotenv()["WHATSAPP_ENABLED"]); ok {
		return v
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir(), "whatsmeow.db")); err == nil {
		return true
	}
	return cfg.WhatsAppAPIURL() != ""
}

func lookupBool(s string) (value, set bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// Build wires every package together (nothing runs until Start).
func Build(o BuildOptions) (*App, error) {
	cfg, log := o.Config, o.Log
	if log == nil {
		log = slog.Default()
	}
	a := &App{Cfg: cfg, Log: log}

	st, err := store.Open(cfg.DBFile())
	if err != nil {
		return nil, fmt.Errorf("open message store: %w", err)
	}
	a.Store = st
	a.Bus = bus.New(bus.WithLogger(log))
	a.Manager = proto.New(proto.WithLogger(log))

	register := func(b proto.Backend) error { return a.Manager.Register(b) }
	if !o.OnlyExtraBackends {
		// Signal is always registered (ChatCore.__init__); it only auto-connects
		// when signal_enabled(). WhatsApp / Telegram only when configured.
		a.Signal = signal.New(signal.Options{Config: cfg, Store: st, Logger: log})
		if err := register(a.Signal); err != nil {
			return nil, err
		}
		if whatsappEnabled(cfg) {
			if whatsappRelinkNeeded(cfg) {
				a.Notices = append(a.Notices, WhatsAppRelinkNotice)
				log.Warn(WhatsAppRelinkNotice)
			}
			a.WhatsApp = whatsapp.New(whatsapp.Options{Config: cfg, Store: st, Logger: log})
			if err := register(a.WhatsApp); err != nil {
				return nil, err
			}
		} else {
			log.Info("WhatsApp backend disabled (enable with WHATSAPP_ENABLED=1 in .env or \"whatsapp_enabled\": true in config.json)")
		}
		if cfg.TelegramEnabled() {
			telegram.RunMarkedIDsMigration(context.Background(), cfg, st, log)
			a.Telegram = telegram.New(telegram.Options{Config: cfg, Store: st, Logger: log})
			if err := register(a.Telegram); err != nil {
				return nil, err
			}
		}
	}
	for _, b := range o.ExtraBackends {
		if err := register(b); err != nil {
			return nil, err
		}
	}

	a.UploadDir = media.UploadDirectory(cfg.CacheDir())
	previewer := media.NewLinkPreviewer(cfg)
	a.Downloads = newDownloader(cfg.DownloadsDir())
	if o.DownloadPort != 0 {
		a.Downloads.port = o.DownloadPort
	}
	a.Gmail = gmail.NewService(cfg, gmail.Deps{Config: cfg, Bus: a.Bus, Images: previewer, Logger: log})

	copts := []core.Option{
		core.WithLogger(log),
		core.WithUploadValidator(uploadValidator),
		core.WithGmailFetch(func(ctx context.Context) (core.GmailStatus, error) {
			s, err := a.Gmail.FetchUnreadStatus(ctx, nil)
			return core.GmailStatus{Unread: s.Unread, UIDNext: s.UIDNext}, err
		}),
		core.WithAutoConnect(func(protocol string) bool {
			if protocol == api.ProtocolSignal && !o.OnlyExtraBackends {
				return cfg.SignalEnabled()
			}
			return true
		}),
	}
	if o.LogBuffer != nil {
		copts = append(copts, core.WithLogBuffer(o.LogBuffer))
	}
	a.Core = core.New(cfg, st, a.Manager, a.Bus, copts...)

	reg := ops.NewRegistry()
	a.Registry = reg
	a.Core.RegisterOps(reg)
	messages.RegisterOps(reg, a.Core)

	a.Media = &media.Deps{Store: st, Backends: a.Manager, Config: cfg, Downloader: a.Downloads,
		Previewer: previewer, Avatars: media.NewAvatarCache(cfg), Logger: log}
	media.RegisterOps(reg, *a.Media)

	a.Linking = linking.RegisterOps(reg, linking.Deps{
		Backends: linkBackends{mgr: a.Manager, wa: a.WhatsApp},
		Config:   cfg,
		Reconnect: func(protocols []string) {
			for _, p := range protocols {
				if p == api.ProtocolWhatsApp && a.WhatsApp != nil {
					a.WhatsApp.Guard().NoteManual()
				}
			}
			a.Core.Reconnect(context.Background(), protocols...)
		},
		LinkRequired: func() bool { return a.WhatsApp != nil && a.WhatsApp.Guard().NeedsPairing() },
	})

	tdeps := transcribe.Deps{Store: st, Config: cfg, Bus: a.Bus, Media: a.Media,
		Quoted: quotedFinder{core: a.Core, st: st}, Logger: log}
	a.Transcribe = transcribe.New(tdeps)
	tdeps.Service = a.Transcribe
	transcribe.RegisterOps(reg, tdeps)

	gdeps := gmail.Deps{Config: cfg, Bus: a.Bus, Images: previewer, Logger: log, Service: a.Gmail}
	if w := a.Core.Gmail(); w != nil {
		gdeps.Watcher = unreadWatcher{w}
	}
	gmail.RegisterOps(reg, gdeps)

	if a.WhatsApp != nil {
		a.WhatsApp.Guard().SetHooks(whatsapp.GuardHooks{
			Status: func(text string, persistent bool) {
				a.Core.Publish(bus.BackendStatus, map[string]any{
					"protocol": api.ProtocolWhatsApp, "text": text, "persistent": persistent})
			},
			Reconnect: func() { a.Core.Reconnect(context.Background(), api.ProtocolWhatsApp) },
			LinkRequired: func(required bool) {
				a.Core.PublishWebPush(api.FrameLinkRequired,
					api.LinkRequiredPayload{Protocol: api.ProtocolWhatsApp, Required: required})
			},
			Linking: func() bool { return a.Linking.IsActive(api.ProtocolWhatsApp) },
		})
	}

	if err := CheckCoverage(reg); err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}

// CheckCoverage asserts that the registry holds exactly the operations of
// api.Ops: nothing missing, nothing extra (duplicates panic in Register).
func CheckCoverage(reg *ops.Registry) error {
	want := map[string]bool{}
	for _, spec := range api.Ops {
		want[spec.Name] = true
	}
	var missing, extra []string
	for n := range want {
		if !reg.Has(n) {
			missing = append(missing, n)
		}
	}
	for _, n := range reg.Names() {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Errorf("operation registry mismatch: missing %v, unexpected %v", missing, extra)
}

// Close releases what Build opened (idempotent); Stop calls it last.
func (a *App) Close() {
	a.stopOnce.Do(func() {
		if a.Downloads != nil {
			a.Downloads.Close()
		}
		if a.Gmail != nil {
			a.Gmail.Close()
		}
		if a.Bus != nil {
			a.Bus.Close()
		}
		if a.Store != nil {
			_ = a.Store.Close()
		}
	})
}

// ---- running ---------------------------------------------------------------------

// RunOptions select the adapters of Run.
type RunOptions struct {
	Socket    string // "" with NoSocket false: Cfg.SocketPath()
	NoSocket  bool
	NoWeb     bool
	WebHost   *string
	WebPort   *int
	WebNoAuth bool
	Out, Err  func(format string, args ...any)
}

// Running is a started server.
type Running struct {
	App    *App
	RPC    *rpc.Server // nil with NoSocket
	Web    *web.Server // nil with NoWeb
	WebURL string

	cancel context.CancelFunc
	wg     sync.WaitGroup
	// Failed is set when an adapter could not start (exit code 1).
	Failed bool
}

// Start starts the core, the transcription service and the adapters. On an
// adapter failure it reports it, sets Failed and returns the (partially
// started) Running: the caller still calls Stop.
func (a *App) Start(ctx context.Context, o RunOptions, shutdown func()) *Running {
	out, errf := o.Out, o.Err
	if out == nil {
		out = func(string, ...any) {}
	}
	if errf == nil {
		errf = out
	}
	for _, n := range a.Notices {
		errf("%s\n", n)
	}
	r := &Running{App: a}
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	a.Registry.ShutdownHook = shutdown
	media.PruneUploadDirectory(a.UploadDir, time.Now())

	if err := a.Core.Start(ctx); err != nil {
		a.Log.Error("core start failed", "err", err)
	}
	if err := a.Transcribe.Start(); err != nil {
		a.Log.Error("Transcription service init failed (non-fatal)", "err", err)
	}
	if a.WhatsApp != nil {
		a.WhatsApp.Guard().Start(runCtx)
	}

	if !o.NoSocket {
		path := o.Socket
		if path == "" {
			path = a.Cfg.SocketPath()
		}
		srv := rpc.NewServer(path, a.Registry, a.Bus,
			rpc.WithLogger(a.Log), rpc.WithPreCall(stagingPreCall(a.Registry, a.UploadDir)))
		if err := srv.Listen(); err != nil {
			a.Log.Error("Socket adapter failed to start", "err", err)
			errf("❌ Socket adapter failed to start: %v\n", err)
			r.Failed = true
		} else {
			r.RPC = srv
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				if err := srv.Serve(runCtx); err != nil {
					a.Log.Error("socket adapter stopped", "err", err)
				}
			}()
			out("🔌 Socket: %s\n", srv.Path())
		}
	}

	if !r.Failed && o.NoWeb {
		if r.RPC == nil {
			a.Log.Warn("Serving without the web and socket adapters: no client can connect")
			errf("⚠️  Running without the web and socket adapters: no client can connect.\n")
		} else {
			a.Log.Info("Serving without the web adapter (--no-web)")
			errf("ℹ️  Running without the web adapter (--no-web): clients connect through the socket %s.\n", r.RPC.Path())
		}
	} else if !r.Failed {
		wopts := []web.Option{web.WithLogger(a.Log), web.WithUploadGuard(uploadGuard{}), web.WithQRRenderer(qrRenderer{})}
		host, port := a.Cfg.WebHost(), a.Cfg.WebPort()
		if o.WebHost != nil {
			host = *o.WebHost
			wopts = append(wopts, web.WithHost(host))
		}
		if o.WebPort != nil {
			port = *o.WebPort
			wopts = append(wopts, web.WithPort(port))
		}
		if o.WebNoAuth {
			wopts = append(wopts, web.WithNoAuth())
		}
		wsrv := web.NewServer(a.Cfg, a.Registry, a.Bus, wopts...)
		urls := formatHosts(web.ParseHosts(host), port)
		if err := wsrv.Listen(ctx); err != nil {
			a.Log.Error("Web adapter failed to start", "err", err)
			errf("❌ Web adapter failed to start on %s: %v\n", urls, err)
			r.Failed = true
		} else {
			r.Web, r.WebURL = wsrv, urls
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				if err := wsrv.Serve(runCtx); err != nil {
					a.Log.Error("web adapter stopped", "err", err)
				}
			}()
			line := "🌐 Web UI: " + urls + " — token: " + a.Cfg.WebToken()
			if o.WebNoAuth {
				line = "🌐 Web UI: " + urls + " — ⚠️ NO AUTH (--web-no-auth)"
			}
			a.Log.Info(line)
			out("%s\n", line)
		}
	}
	return r
}

// Stop stops everything in reverse order of start: the adapters (no new
// requests), the device links, transcription, the WhatsApp guard, the core
// (workers, backends, cache prune) and finally the resources behind them.
func (r *Running) Stop() {
	a := r.App
	r.cancel() // web (graceful), rpc serve loop, wa guard
	if r.RPC != nil {
		r.RPC.Close()
	}
	r.wg.Wait()
	a.Linking.CancelAll()
	a.Transcribe.Stop()
	if a.WhatsApp != nil {
		a.WhatsApp.Guard().Stop()
	}
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.Core.Stop(sctx); err != nil {
		a.Log.Debug("core stop", "err", err)
	}
	a.Close()
}

func formatHosts(hosts []string, port int) string {
	parts := make([]string, len(hosts))
	for i, h := range hosts {
		if strings.Contains(h, ":") {
			parts[i] = "http://[" + h + "]:" + strconv.Itoa(port)
		} else {
			parts[i] = "http://" + net.JoinHostPort(h, strconv.Itoa(port))
		}
	}
	return strings.Join(parts, ", ")
}
