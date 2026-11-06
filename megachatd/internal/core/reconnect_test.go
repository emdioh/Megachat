package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
	"github.com/emdioh/megachat/megachatd/internal/proto/signal"
)

// staleBackend reports a successful Connect while its session is dead (the
// Signal silent-fallback bug) until linked is set.
type staleBackend struct {
	*fake.Backend
	linked atomic.Bool
}

func (s *staleBackend) Connect(ctx context.Context) error {
	if !s.linked.Load() {
		return nil
	}
	return s.Backend.Connect(ctx)
}

// TestReconnectForcesAStaleConnectedBackend is the half-connected half of
// mc-tih: the manager tracks the backend as StateConnected (Connect returned
// nil), so an idempotent StartBackend would skip it and link.reconnect stayed
// a no-op. Reconnect must disconnect and connect again.
func TestReconnectForcesAStaleConnectedBackend(t *testing.T) {
	e := newEnv(t, api.ProtocolSignal)
	stale := &staleBackend{Backend: fake.New(api.ProtocolSignal)}
	if err := e.mgr.Register(stale); err != nil {
		t.Fatal(err)
	}
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	defer e.core.Stop(bg)

	ready := func(ev bus.Event) bool {
		return ev.Type == bus.BackendReady && ev.Payload["protocol"] == api.ProtocolSignal
	}
	collectUntil(t, e.sub, ready)
	if stale.IsConnected() {
		t.Fatal("test setup: the stale backend must not be connected")
	}
	stale.linked.Store(true)
	e.core.Reconnect(bg, api.ProtocolSignal)
	collectUntil(t, e.sub, ready)
	if !stale.IsConnected() {
		t.Fatal("reconnect skipped a backend tracked as connected")
	}
}

// collectUntil drains sub until pred matches (returning everything it saw,
// the match included) or fails the test after 5s.
func collectUntil(t *testing.T, sub *bus.Subscription, pred func(bus.Event) bool) []bus.Event {
	t.Helper()
	var got []bus.Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.C():
			got = append(got, ev)
			if pred(ev) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a bus event; saw %+v", got)
		}
	}
}

// TestReconnectRestartsSignalAfterBootDaemonFailure is mc-tih: the server
// boots with signal.user_number set but the account not linked yet, so the
// spawned signal-cli daemon exits at once. Connect must fail (status +
// backend_done), not pretend success; once the account is linked,
// link.reconnect (Core.Reconnect) must restart Signal and publish
// backend_ready.
func TestReconnectRestartsSignalAfterBootDaemonFailure(t *testing.T) {
	// The fake daemon endpoint answers only after the account is linked.
	var linked atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/rpc", func(w http.ResponseWriter, r *http.Request) {
		if !linked.Load() {
			http.Error(w, "daemon not there", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any = []any{}
		switch req.Method {
		case "listContacts":
			result = []any{map[string]any{"number": "+391234567890", "name": "Mario", "uuid": "uuid-1"}}
		case "sendSyncRequest":
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	})
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("FAKE_SIGNAL_DAEMON_FAIL", "-390000000000 is not registered")
	t.Setenv("FAKE_SIGNAL_DAEMON_EXIT", "3")
	cli, err := filepath.Abs("../proto/signal/testdata/fake-signal-cli.sh")
	if err != nil {
		t.Fatal(err)
	}

	e := newEnv(t, api.ProtocolSignal)
	sb := signal.New(signal.Options{
		Config: e.cfg, Store: e.st, Logger: quietLog(),
		UserNumber:     "+390000000000",
		CLIPath:        cli,
		DaemonURL:      srv.URL + "/api/v1/rpc",
		SSEURL:         srv.URL + "/api/v1/events",
		ProbeAttempts:  200,
		ProbeInterval:  5 * time.Millisecond,
		SSEReadTimeout: 200 * time.Millisecond,
		SSERetryDelay:  20 * time.Millisecond,
	})
	if err := e.mgr.Register(sb); err != nil {
		t.Fatal(err)
	}

	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	defer e.core.Stop(bg)

	boot := collectUntil(t, e.sub, func(ev bus.Event) bool {
		return ev.Type == bus.BackendDone && ev.Payload["protocol"] == api.ProtocolSignal
	})
	warned := false
	for _, ev := range boot {
		if ev.Type == bus.BackendReady && ev.Payload["protocol"] == api.ProtocolSignal {
			t.Fatalf("boot published backend_ready for a dead daemon: %+v", ev)
		}
		if ev.Type == bus.BackendStatus && ev.Payload["protocol"] == api.ProtocolSignal {
			if txt, _ := ev.Payload["text"].(string); strings.Contains(txt, "daemon exited") {
				warned = true
			}
		}
	}
	if !warned {
		t.Fatalf("boot did not report the daemon failure: %+v", boot)
	}
	if sb.IsConnected() {
		t.Fatal("boot reported signal as connected although the daemon died")
	}
	for _, st := range e.mgr.Status() {
		if st.Protocol == api.ProtocolSignal && st.State != proto.StateFailed {
			t.Fatalf("manager state after the failed boot: %+v", st)
		}
	}

	// The user links the account; the SPA calls link.reconnect.
	linked.Store(true)
	e.core.Reconnect(bg, api.ProtocolSignal)
	collectUntil(t, e.sub, func(ev bus.Event) bool {
		return ev.Type == bus.BackendReady && ev.Payload["protocol"] == api.ProtocolSignal
	})
	if !sb.IsConnected() {
		t.Fatal("signal daemon not up after the reconnect")
	}
	for _, st := range e.mgr.Status() {
		if st.Protocol == api.ProtocolSignal && st.State != proto.StateConnected {
			t.Fatalf("manager state after the reconnect: %+v", st)
		}
	}
}
