package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

func TestGmailUnreadWatcher(t *testing.T) {
	var frames []map[string]any
	var types []string
	status := GmailStatus{Unread: 3, UIDNext: store.Ptr(int64(10))}
	var fetchErr error
	w := NewGmailUnreadWatcher(func(typ string, p any) {
		types = append(types, typ)
		frames = append(frames, p.(map[string]any))
	}, func(ctx context.Context) (GmailStatus, error) { return status, fetchErr }, 60, func() bool { return true })

	w.CheckOnce(bg)
	w.CheckOnce(bg) // unchanged: nothing published
	if len(frames) != 1 || types[0] != "gmail_unread" || frames[0]["unread"] != 3 || frames[0]["new_mail"] != false {
		t.Fatalf("first: %v", frames)
	}
	status = GmailStatus{Unread: 4, UIDNext: store.Ptr(int64(11))}
	w.CheckOnce(bg)
	if len(frames) != 2 || frames[1]["unread"] != 4 || frames[1]["new_mail"] != true {
		t.Fatalf("new mail: %v", frames)
	}
	// Unread dropped (mark read), UIDNEXT unchanged: not new mail.
	status = GmailStatus{Unread: 0, UIDNext: store.Ptr(int64(11))}
	w.CheckOnce(bg)
	if frames[2]["new_mail"] != false || frames[2]["unread"] != 0 {
		t.Fatalf("read: %v", frames)
	}
	fetchErr = errors.New("imap down")
	status = GmailStatus{Unread: 9}
	w.CheckOnce(bg)
	if len(frames) != 3 {
		t.Fatal("a failing fetch publishes nothing")
	}
}

func TestGmailWatcherOnBus(t *testing.T) {
	e := newEnv(t, "signal")
	n := 0
	e.core.gmail = NewGmailUnreadWatcher(e.core.PublishWebPush,
		func(ctx context.Context) (GmailStatus, error) { n++; return GmailStatus{Unread: n}, nil }, 0.05, nil)
	e.core.gmail.Start(bg)
	defer e.core.gmail.Stop()
	deadline := time.After(3 * time.Second)
	for {
		if p := e.webPushes(); len(p) > 0 {
			wantJSON(t, p[0], `{"event":{"type":"gmail_unread","payload":{"unread":1,"new_mail":false}}}`)
			return
		}
		select {
		case <-deadline:
			t.Fatal("no gmail_unread frame")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestPruneMedia(t *testing.T) {
	e := newEnv(t, "signal")
	dir := t.TempDir()
	tgDir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	mk := func(d, name string, mtime time.Time) {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, mtime, mtime)
	}
	mk(dir, "kept.png", old)
	mk(dir, "orphan.png", old)
	mk(dir, "young.png", time.Now())
	mk(tgDir, "5-77-photo.jpg", old)
	mk(tgDir, "6-1-orphan.jpg", old)
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "c", Text: "t", Timestamp: 1,
		AttachmentID: store.Ptr("/some/where/kept.png")})
	_, _, _ = e.st.AddMessage(bg, store.NewMessage{Protocol: "telegram", ContactNumber: "c", Text: "t", Timestamp: 1,
		AttachmentID: store.Ptr("tgref:5:77")})
	scopes := []MediaScope{{"signal", "signal", dir}, {"telegram", "telegram", tgDir}, {"signal", "whatsapp-missing", filepath.Join(dir, "nope")}}

	dry := PruneMedia(bg, e.st, MediaPruneOptions{Scopes: scopes, DryRun: true}, quietLog())
	if dry.TotalOrphans() != 2 || dry.Scopes[0].SkippedGrace != 1 || !dry.Scopes[2].SkippedDirMissing {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan.png")); err != nil {
		t.Fatal("dry run must not delete")
	}
	rep := PruneMedia(bg, e.st, MediaPruneOptions{Scopes: scopes}, quietLog())
	if rep.Scopes[0].OrphansDeleted != 1 || rep.Scopes[1].OrphansDeleted != 1 || rep.TotalBytes() != 2 {
		t.Fatalf("report: %+v", rep)
	}
	for _, gone := range []string{filepath.Join(dir, "orphan.png"), filepath.Join(tgDir, "6-1-orphan.jpg")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s should be deleted", gone)
		}
	}
	for _, kept := range []string{filepath.Join(dir, "kept.png"), filepath.Join(dir, "young.png"), filepath.Join(tgDir, "5-77-photo.jpg")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s should be kept", kept)
		}
	}
}

func TestStopPrunesWithConfiguredRetention(t *testing.T) {
	e := newEnv(t, "signal")
	e.core.retention = nil
	// MESSAGES_RETENTION_PER_CONTACT comes from config like in Python (min 100).
	e.cfg.LookupEnv = func(k string) (string, bool) {
		if k == "MESSAGES_RETENTION_PER_CONTACT" {
			return "100", true
		}
		return "", false
	}
	for i := 0; i < 130; i++ {
		_, _, err := e.st.AddMessage(bg, store.NewMessage{Protocol: "signal", ContactNumber: "c", Text: "m" + itoa(int64(i)),
			Sender: "x", Timestamp: int64(1000 + i), MsgID: store.Ptr("id" + itoa(int64(i)))})
		if err != nil {
			t.Fatal(err)
		}
	}
	e.core.scopes = []MediaScope{}
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	if err := e.core.Stop(bg); err != nil {
		t.Logf("stop: %v", err)
	}
	rows, _ := e.st.ListMessages(bg, store.MessageQuery{Protocol: "signal", Keys: []string{"c"}})
	if len(rows) != 100 || rows[0].Timestamp != 1030 {
		t.Fatalf("retention left %d rows (first ts %d)", len(rows), rows[0].Timestamp)
	}
	// Stop is idempotent.
	if err := e.core.Stop(bg); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectAndPairingSkip(t *testing.T) {
	e := newEnv(t, "telegram")
	e.fake("telegram").SetPairing(true, "qr")
	if err := e.core.Start(bg); err != nil {
		t.Fatal(err)
	}
	defer e.core.Stop(bg)
	ev := waitFor(t, e.sub, func(ev bus.Event) bool { return ev.Type == "backend_done" })
	if ev.Payload["protocol"] != "telegram" || ev.Payload["ok"] != false {
		t.Fatalf("%v", ev.Payload)
	}
	if e.fake("telegram").IsConnected() {
		t.Fatal("a backend needing pairing is not auto-connected")
	}
	// After linking, Reconnect connects it.
	e.fake("telegram").SetPairing(false, "")
	e.core.Reconnect(bg, "telegram", "nope")
	waitFor(t, e.sub, func(ev bus.Event) bool { return ev.Type == "backend_ready" })
	if !e.fake("telegram").IsConnected() {
		t.Fatal("reconnect should connect")
	}
}
