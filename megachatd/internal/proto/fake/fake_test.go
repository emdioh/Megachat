package fake_test

import (
	"context"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

func TestFakeBasics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f := fake.New("signal")
	if f.IsConnected() {
		t.Fatal("starts disconnected")
	}
	if err := f.Connect(ctx); err != nil || !f.IsConnected() {
		t.Fatal(err)
	}
	id, _ := f.SendMessage(ctx, "c", "hi", proto.Quote{})
	if ok, _ := f.EditMessage(ctx, "c", id, "yo"); !ok {
		t.Fatal("edit own message")
	}
	if h, _ := f.FetchHistory(ctx, "c", 0); len(h) != 1 || h[0].Text != "yo" {
		t.Fatalf("%+v", h)
	}
	rctx, rcancel := context.WithCancel(ctx)
	ch, _ := f.Receive(rctx)
	f.Emit(proto.Event{Type: proto.EventTyping})
	if ev := <-ch; ev.Protocol != "signal" {
		t.Fatal("protocol defaulted")
	}
	rcancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should close with ctx")
	}
	f.DelayConnect(time.Hour)
	dctx, dcancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer dcancel()
	if err := f.Connect(dctx); err == nil {
		t.Fatal("delay must honour ctx")
	}
}
