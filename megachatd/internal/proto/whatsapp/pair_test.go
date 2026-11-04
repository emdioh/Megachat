package whatsapp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// unpairedEnv is a backend over an unpaired fake device.
func unpairedEnv(t *testing.T) *env {
	e := newEnv(t)
	e.fc.dev.ID = nil
	e.fc.connected = false
	e.b.connected.Store(false)
	return e
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPairingQRThroughQRChannel(t *testing.T) {
	e := unpairedEnv(t)
	if !e.b.NeedsPairing() {
		t.Fatal("an unpaired device needs pairing")
	}
	done := make(chan string, 1)
	go func() {
		code, err := e.b.PairingQR(context.Background())
		if err != nil {
			t.Error(err)
		}
		done <- code
	}()
	eventually(t, "the QR channel", func() bool { e.fc.mu.Lock(); defer e.fc.mu.Unlock(); return e.fc.qr != nil })
	e.fc.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@abc,def,ghi", Timeout: time.Minute}
	select {
	case code := <-done:
		if code != "2@abc,def,ghi" {
			t.Errorf("QR = %q", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no QR")
	}
	if e.fc.connects != 1 {
		t.Errorf("pairing connects once, got %d", e.fc.connects)
	}
	// A second call reuses the session and sees the rotated code.
	e.fc.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@rotated"}
	eventually(t, "rotation", func() bool {
		c, _ := e.b.PairingQR(context.Background())
		return c == "2@rotated"
	})
	if e.fc.connects != 1 {
		t.Error("the active session is not reopened")
	}
	// Success ends the session; the pairing flag clears.
	e.fc.qr <- whatsmeow.QRChannelSuccess
	eventually(t, "pairing end", func() bool {
		e.b.pairMu.Lock()
		defer e.b.pairMu.Unlock()
		return !e.b.pairing
	})
}

func TestPairingTimeoutAllowsANewSession(t *testing.T) {
	e := unpairedEnv(t)
	go func() {
		eventually(t, "qr", func() bool { e.fc.mu.Lock(); defer e.fc.mu.Unlock(); return e.fc.qr != nil })
		e.fc.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "first"}
	}()
	if c, _ := e.b.PairingQR(context.Background()); c != "first" {
		t.Fatalf("first code = %q", c)
	}
	e.fc.qr <- whatsmeow.QRChannelTimeout
	eventually(t, "session end", func() bool {
		e.b.pairMu.Lock()
		defer e.b.pairMu.Unlock()
		return !e.b.pairing
	})
	e.fc.mu.Lock()
	e.fc.qr = nil
	e.fc.mu.Unlock()
	go func() {
		eventually(t, "qr", func() bool { e.fc.mu.Lock(); defer e.fc.mu.Unlock(); return e.fc.qr != nil })
		e.fc.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "second"}
	}()
	if c, _ := e.b.PairingQR(context.Background()); c != "second" {
		t.Fatalf("a fresh session after the timeout: %q", c)
	}
}

func TestPairingQRWhenAlreadyLinked(t *testing.T) {
	e := newEnv(t)
	if e.b.NeedsPairing() {
		t.Fatal("a paired device does not need pairing")
	}
	code, err := e.b.PairingQR(ctx)
	if err != nil || code != "" {
		t.Fatalf("linked: code=%q err=%v", code, err)
	}
}

func TestPairingConnectError(t *testing.T) {
	e := unpairedEnv(t)
	f := &failingConnect{fakeClient: e.fc}
	e.b.cli = f
	if _, err := e.b.PairingQR(ctx); err == nil {
		t.Error("connect failure must be reported")
	}
}

type failingConnect struct{ *fakeClient }

func (f *failingConnect) Connect() error { return errors.New("dial failed") }

func TestLinkerBackendSessionStatusAndQR(t *testing.T) {
	e := unpairedEnv(t)
	l := e.b.AsLinker()
	st, me, err := l.SessionStatus(ctx)
	if err != nil || st != "scan_qr_code" || me {
		t.Fatalf("unpaired status = %q %v %v", st, me, err)
	}
	go func() {
		eventually(t, "qr", func() bool { e.fc.mu.Lock(); defer e.fc.mu.Unlock(); return e.fc.qr != nil })
		e.fc.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@xyz"}
	}()
	q, err := l.FreshPairingQR(ctx, false)
	if err != nil || !q.OK || q.Text != "2@xyz" || q.PNG != nil {
		t.Fatalf("fresh QR = %+v err=%v", q, err)
	}
	q, err = l.PairingQR(ctx)
	if err != nil || !q.OK || q.Text != "2@xyz" {
		t.Fatalf("current QR = %+v err=%v", q, err)
	}

	// Once linked and working there is no QR to show.
	e2 := newEnv(t)
	l2 := e2.b.AsLinker()
	st, me, _ = l2.SessionStatus(ctx)
	if st != "working" || !me {
		t.Fatalf("working status = %q %v", st, me)
	}
	if q, _ := l2.FreshPairingQR(ctx, false); q.OK {
		t.Error("no QR for a linked session")
	}
	// A dead (failed) linked session is reconnected, never un-linked.
	e2.b.connected.Store(false)
	e2.b.setDisconnected(true)
	e2.b.discMu.Lock()
	e2.b.discAt = nowFunc().Add(-time.Hour)
	e2.b.discMu.Unlock()
	if st, _, _ := l2.SessionStatus(ctx); st != "failed" {
		t.Fatalf("status = %q", st)
	}
	if q, _ := l2.FreshPairingQR(ctx, true); q.OK || e2.fc.connects != 1 || e2.fc.dev.ID == nil {
		t.Errorf("reset reconnects keeping the pairing: %+v connects=%d", q, e2.fc.connects)
	}
}

func TestConnectIdempotentAndLoadsContacts(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_A1", "text": "x", "is_mine": false}, 1000)
	if err := e.b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.b.IsConnected() {
		t.Fatal("connected")
	}
	if e.fc.connects != 0 {
		t.Error("an already connected client is not connected again")
	}
	list, _ := e.b.ListContacts(ctx)
	if len(list) != 1 || list[0].ID != "391234567890@c.us" {
		t.Fatalf("contacts after connect: %+v", list)
	}
	if err := e.b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Disconnect, then Connect again (Core.Reconnect).
	if err := e.b.Disconnect(ctx); err != nil || e.b.IsConnected() {
		t.Fatal("disconnect")
	}
	e.fc.mu.Lock()
	e.fc.loggedIn = true
	e.fc.mu.Unlock()
	if err := e.b.Connect(ctx); err != nil || e.fc.connects != 1 || !e.b.IsConnected() {
		t.Fatalf("reconnect: err=%v connects=%d connected=%v", err, e.fc.connects, e.b.IsConnected())
	}
	ch, err := e.b.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.b.emit(proto.EventReceipt, "x@c.us", map[string]any{"message_ids": []any{}})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == proto.EventLidWarmupDone {
				continue // the background LID resolver may announce itself first
			}
			if ev.Type != proto.EventReceipt {
				t.Errorf("event %+v", ev)
			}
			return
		case <-deadline:
			t.Fatal("Receive must work again after a reconnect")
		}
	}
}

// ---- through the real core ----------------------------------------------------------------------

func TestBackendEventsFlowThroughCore(t *testing.T) {
	e := newEnv(t)
	mgr := proto.New(proto.WithLogger(quietLogger()))
	if err := mgr.Register(e.b); err != nil {
		t.Fatal(err)
	}
	c := core.New(e.cfg, e.st, mgr, bus.New(), core.WithLogger(quietLogger()), core.WithoutStartupMaintenance())

	// 1. an incoming message is ingested by the backend (not the store fallback).
	evs := e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0AA", false, text("Ciao dal telefono")), false)
	res, err := c.Process(ctx, evs[0])
	if err != nil || len(res) != 1 || res[0].Type != bus.Message {
		t.Fatalf("process message: %+v %v", res, err)
	}
	p := res[0].Payload
	if p["protocol"] != "whatsapp" || p["contact_id"] != "391234567890@c.us" || p["added"] != true {
		t.Fatalf("message payload: %+v", p)
	}
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 1 || store0(rows[0].MsgID) != "false_391234567890@c.us_3EB0AA" {
		t.Fatalf("stored rows: %+v", rows)
	}
	// The same event again is deduplicated by the backend.
	res, _ = c.Process(ctx, evs[0])
	if len(res) != 1 || res[0].Payload["added"] != false {
		t.Errorf("second delivery: %+v", res[0].Payload)
	}

	// 2. our message gets delivered/read receipts through ProcessReceipt.
	if _, err := e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "true_391234567890@c.us_3EB0BB", "text": "mio", "is_mine": true}, 2000); err != nil {
		t.Fatal(err)
	}
	rc := &events.Receipt{Type: types.ReceiptTypeRead, MessageIDs: []types.MessageID{"3EB0BB"}}
	rc.Chat = types.NewJID("391234567890", types.DefaultUserServer)
	e.b.onReceipt(rc)
	var receipt proto.Event
	for _, ev := range drain(e.b) {
		if ev.Type == proto.EventReceipt {
			receipt = ev
		}
	}
	res, err = c.Process(ctx, receipt)
	if err != nil || len(res) != 1 || res[0].Type != bus.Receipt {
		t.Fatalf("receipt: %+v %v", res, err)
	}
	m, _ := e.st.LoadCache(ctx, "whatsapp")
	var status string
	for _, r := range m["391234567890@c.us"] {
		if store0(r.MsgID) == "true_391234567890@c.us_3EB0BB" {
			status = store0(r.Status)
		}
	}
	if status != "read" {
		t.Errorf("status = %q", status)
	}

	// 3. an incoming reaction becomes a reaction_update with the aggregate.
	rm := dmEvent("391234567890", types.DefaultUserServer, "RX1", false, reactionMsg("3EB0BB", true, "👍"))
	rev := e.b.messageEvents(rm, false)
	res, err = c.Process(ctx, rev[0])
	if err != nil || len(res) != 1 || res[0].Type != bus.ReactionUpdate {
		t.Fatalf("reaction: %+v %v", res, err)
	}
	info := res[0].Payload["info"].(map[string]any)
	if info["message_id"] != "true_391234567890@c.us_3EB0BB" {
		t.Errorf("reaction info: %+v", info)
	}

	// 4. an edit becomes a message_edit.
	em := dmEvent("391234567890", types.DefaultUserServer, "ED1", false, editMsg("3EB0AA", "Ciao (modificato)"))
	em.IsEdit = true
	eev := e.b.messageEvents(em, false)
	res, err = c.Process(ctx, eev[0])
	if err != nil || len(res) != 1 || res[0].Type != bus.MessageEdit {
		t.Fatalf("edit: %+v %v", res, err)
	}
	if res[0].Payload["text"] != "Ciao (modificato)" {
		t.Errorf("edit payload: %+v", res[0].Payload)
	}
	var _ = store.Message{}
	var _ = filepath.Join
}
