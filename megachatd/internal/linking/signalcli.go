package linking

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FindSignalCLI returns <binDir>/signal-cli-*/bin/signal-cli (executable).
func FindSignalCLI(binDir string) (string, error) {
	entries, err := os.ReadDir(binDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "signal-cli-") {
				continue
			}
			exe := filepath.Join(binDir, e.Name(), "bin", "signal-cli")
			if st, err := os.Stat(exe); err == nil && st.Mode()&0o111 != 0 {
				return exe, nil
			}
		}
	}
	return "", errors.New("signal-cli not found in ./bin/")
}

var accountRE = regexp.MustCompile(`(?:^|[^\d])(\+\d{7,15})(?:[^\d]|$)`)

// ParseAccounts extracts canonical E.164 numbers, deduplicated in order.
func ParseAccounts(out string) []string {
	var res []string
	seen := map[string]bool{}
	// overlapping-safe scan: re-run on the remainder after each match
	rest := out
	for {
		m := accountRE.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		n := rest[m[2]:m[3]]
		if !seen[n] {
			seen[n] = true
			res = append(res, n)
		}
		rest = rest[m[3]:]
	}
	return res
}

type execCLI struct{ binDir string }

// NewExecCLI is the default SignalCLI: it executes the binary under binDir.
func NewExecCLI(binDir string) SignalCLI { return &execCLI{binDir: binDir} }

func (c *execCLI) ListAccounts(ctx context.Context) ([]string, error) {
	exe, err := FindSignalCLI(c.binDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "listAccounts").Output()
	if err != nil {
		return nil, errors.New("signal-cli could not list the linked accounts")
	}
	return ParseAccounts(string(out)), nil
}

type execProc struct {
	cmd  *exec.Cmd
	r    *os.File
	br   *bufio.Reader
	done chan struct{}
	mu   sync.Mutex
	code int
}

func (c *execCLI) StartLink(deviceName string) (LinkProcess, error) {
	exe, err := FindSignalCLI(c.binDir)
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "link", "-n", deviceName)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	p := &execProc{cmd: cmd, r: r, br: bufio.NewReader(r), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		p.mu.Lock()
		p.code = cmd.ProcessState.ExitCode()
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *execProc) ReadLine() (string, bool) {
	line, err := p.br.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

func (p *execProc) Drain() {
	go func() {
		for {
			if _, ok := p.ReadLine(); !ok {
				p.r.Close()
				return
			}
		}
	}()
}

func (p *execProc) Exited() (bool, int) {
	select {
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return true, p.code
	default:
		return false, 0
	}
}

func (p *execProc) Terminate() error {
	if done, _ := p.Exited(); done {
		return nil
	}
	return p.cmd.Process.Signal(syscall.SIGTERM)
}
