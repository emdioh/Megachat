package rpc

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// TestInteropPythonSocketClient runs the real client/socket_client.py (the
// TUI's socket client) against the Go server. Skipped without Python or the
// client imports.
func TestInteropPythonSocketClient(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	py := pythonForRepo(t, repo)
	if _, err := os.Stat(filepath.Join(repo, "client", "socket_client.py")); err != nil {
		t.Skip("client/socket_client.py not found")
	}
	script, _ := filepath.Abs("testdata/interop_client.py")
	e := newEnv(t)

	cmd := exec.Command(py, "-I", script, repo, e.path)
	stdout, _ := cmd.StdoutPipe()
	var stderr limitedBuf
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	lines := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				lines <- m
			}
		}
		close(lines)
	}()
	next := func() map[string]any {
		select {
		case m, ok := <-lines:
			if !ok {
				return nil
			}
			return m
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
			t.Fatal("interop timeout; stderr: " + stderr.String())
			return nil
		}
	}
	m := next()
	if m == nil {
		err := cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
			t.Skip("python imports unavailable: " + stderr.String())
		}
		t.Fatalf("no output: %v %s", err, stderr.String())
	}
	if m["step"] != "hello" {
		t.Fatal(m)
	}
	info := m["info"].(map[string]any)
	if info["api_version"].(float64) != api.APIVersion {
		t.Fatal(info)
	}
	m = next()
	echo := m["result"].(map[string]any)["echo"].(map[string]any)
	if m["step"] != "call" || echo["text"] != "héllo \U0001f600" {
		t.Fatal(m)
	}
	m = next()
	if m["step"] != "error" || m["raised"] != true || m["code"] != "conflict" || m["message"] != "Nope è" {
		t.Fatal(m)
	}
	m = next()
	if m["step"] != "unknown" || m["code"] != "not_found" {
		t.Fatal(m)
	}
	m = next()
	if m["step"] != "subscribed" {
		t.Fatal(m)
	}
	for i := 1; i <= 3; i++ {
		e.bus.Publish(bus.Message, map[string]any{"i": i, "t": "café"})
	}
	m = next()
	evs := m["events"].([]any)
	if m["step"] != "events" || len(evs) != 3 {
		t.Fatal(m, stderr.String())
	}
	for i, ev := range evs {
		ev := ev.(map[string]any)
		if ev["type"] != "message" || ev["payload"].(map[string]any)["t"] != "café" ||
			ev["payload"].(map[string]any)["i"].(float64) != float64(i+1) {
			t.Fatal(ev)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("python exit: %v %s", err, stderr.String())
	}
}

// pythonForRepo prefers the repo's virtualenv interpreter, where the client's
// dependencies live, and falls back to python3 on PATH.
func pythonForRepo(t *testing.T, repo string) string {
	t.Helper()
	venv := filepath.Join(repo, ".venv", "bin", "python")
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	return py
}

type limitedBuf struct{ b []byte }

func (l *limitedBuf) Write(p []byte) (int, error) {
	if len(l.b) < 8192 {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}
func (l *limitedBuf) String() string { return string(l.b) }
