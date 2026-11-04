// Command megachatd is the Go port of the Megachat headless server
// (server/main.py + server/__main__.py): the chat core with its Unix-socket
// and web adapters.
//
//	megachatd serve [flags]     run the server until SIGINT/SIGTERM or server.shutdown
//	megachatd version           print the version
//
// The flags of "serve" are those of "megachat serve" plus --project-dir.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/rpc"
)

// Version is the server version (overridable with -ldflags "-X main.Version=...").
var Version = "0.1.0-go"

// LogFileEnv overrides the log file (Python: fixed /tmp/signal-tui.log).
const LogFileEnv = "MEGACHAT_LOG_FILE"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: megachatd <command> [flags]

commands:
  serve     run the chat core headless (web UI + Unix socket); see "megachatd serve -h"
  version   print the version
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stdout, stderr, nil)
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "megachatd", Version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	if strings.HasPrefix(args[0], "-") { // `python -m server` form: flags only
		return serve(args, stdout, stderr, nil)
	}
	fmt.Fprintf(stderr, "megachatd: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

type serveArgs struct {
	debug      bool
	webPort    int
	webHost    string
	webNoAuth  bool
	noWeb      bool
	noSocket   bool
	socket     string
	projectDir string
	webPortSet bool
	webHostSet bool
}

func parseServeArgs(args []string, stderr io.Writer) (*serveArgs, int, bool) {
	var a serveArgs
	fs := flag.NewFlagSet("megachatd serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&a.debug, "debug", false, "enable debug logging")
	fs.IntVar(&a.webPort, "web-port", 0, "web UI listen port")
	fs.StringVar(&a.webHost, "web-host", "", "web UI bind host (default 127.0.0.1); comma-separate several to listen on each, e.g. 127.0.0.1,192.168.1.10")
	fs.BoolVar(&a.webNoAuth, "web-no-auth", false, "disable the web UI Bearer token requirement (INSECURE: anyone who can reach --web-host/--web-port gets full read/send access with no credential)")
	fs.BoolVar(&a.noWeb, "no-web", false, "do not start the web adapter (clients can still use the socket)")
	fs.BoolVar(&a.noSocket, "no-socket", false, "do not start the Unix-socket adapter")
	fs.StringVar(&a.socket, "socket", "", "Unix socket path (default: $MEGACHAT_SOCKET, else $XDG_RUNTIME_DIR/megachat/server.sock, else ~/.local/state/megachat/server.sock)")
	fs.StringVar(&a.projectDir, "project-dir", "", "directory holding config.json and .env (default: $MEGACHAT_PROJECT_DIR, else the repo containing the executable, else the working directory)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, 0, false
		}
		return nil, 2, false
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	fail := func(msg string) (*serveArgs, int, bool) {
		fmt.Fprintf(stderr, "megachatd serve: error: %s\n", msg)
		return nil, 2, false
	}
	if set["web-port"] && (a.webPort < 1 || a.webPort > 65535) {
		return fail("--web-port must be between 1 and 65535")
	}
	if a.noSocket && a.socket != "" {
		return fail("--socket and --no-socket are mutually exclusive")
	}
	a.webPortSet, a.webHostSet = set["web-port"], set["web-host"]
	return &a, 0, true
}

// ResolveProjectDir picks the directory with config.json / .env: the flag,
// else $MEGACHAT_PROJECT_DIR, else the nearest ancestor of the executable that
// holds megachat.py or pyproject.toml, else the working directory.
func ResolveProjectDir(flagValue string, env func(string) string, exe string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := env(config.ProjectDirEnv); v != "" {
		return v
	}
	if exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		for dir := filepath.Dir(exe); ; dir = filepath.Dir(dir) {
			for _, marker := range []string{"megachat.py", "pyproject.toml"} {
				if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
					return dir
				}
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func configureLogging(cfg *config.Config, debug bool, buf *core.LogBuffer) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	path := cfg.Env(LogFileEnv)
	if path == "" {
		path = config.DefaultLogFile
	}
	var next slog.Handler
	closeFn := func() {}
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		next = slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})
		closeFn = func() { _ = f.Close() }
	} else {
		next = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	log := slog.New(buf.Handler(next))
	slog.SetDefault(log)
	return log, closeFn
}

// serve runs the server. stop, when non-nil, ends the wait like a signal
// (tests); ready, when set through serveHooks, receives the Running once up.
func serve(args []string, stdout, stderr io.Writer, hooks *serveHooks) int {
	sa, code, ok := parseServeArgs(args, stderr)
	if !ok {
		return int(code)
	}
	exe, _ := os.Executable()
	cfg := config.New()
	cfg.ProjectDir = ResolveProjectDir(sa.projectDir, cfg.Env, exe)
	if abs, err := filepath.Abs(cfg.ProjectDir); err == nil {
		cfg.ProjectDir = abs
	}

	logBuf := core.NewLogBuffer()
	log, closeLog := configureLogging(cfg, sa.debug, logBuf)
	defer closeLog()

	release, err := rpc.AcquireLock(cfg.LockFile())
	if err != nil {
		fmt.Fprintf(stderr, "❌ Megachat is already running (lock file %s).\n", cfg.LockFile())
		fmt.Fprintln(stderr, "   A server (or a --in-process TUI) is already running; delete the lock file only if you are sure nothing is.")
		return 1
	}
	defer release()

	opts := BuildOptions{Config: cfg, Log: log, LogBuffer: logBuf}
	applyFakeBackend(&opts, log) // no-op unless built with -tags contracttest
	app, err := Build(opts)
	if err != nil {
		log.Error("startup failed", "err", err)
		fmt.Fprintf(stderr, "❌ %v\n", err)
		return 1
	}

	stop := make(chan struct{})
	var once sync.Once
	stopFn := func() { once.Do(func() { close(stop) }) }
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	ro := RunOptions{
		NoSocket: sa.noSocket, NoWeb: sa.noWeb, WebNoAuth: sa.webNoAuth,
		Out: func(f string, a ...any) { fmt.Fprintf(stdout, f, a...) },
		Err: func(f string, a ...any) { fmt.Fprintf(stderr, f, a...) },
	}
	if sa.socket != "" {
		ro.Socket = expandHome(sa.socket, cfg.Home)
	}
	if sa.webPortSet {
		p := sa.webPort
		ro.WebPort = &p
	}
	if sa.webHostSet {
		h := sa.webHost
		ro.WebHost = &h
	}
	running := app.Start(context.Background(), ro, stopFn)
	exit := 0
	if running.Failed {
		exit = 1
	} else {
		log.Info("Megachat server running", "lock", cfg.LockFile(), "version", Version)
		if hooks != nil && hooks.ready != nil {
			hooks.ready(running, stopFn)
		}
		select {
		case <-sigs:
		case <-stop:
		}
		log.Info("Shutdown requested")
	}
	running.Stop()
	return exit
}

type serveHooks struct {
	ready func(r *Running, stop func())
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
