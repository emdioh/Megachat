package telegram

import (
	"context"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var ctx = context.Background()

var _ BatchSlotMerger = (*store.Store)(nil)

func TestMergeBatchSlotWithRealStore(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.st.AddMessage(ctx, store.NewMessage{Protocol: proto.ProtocolTelegram, ContactNumber: "7",
		Text: "x", IsMine: true, Timestamp: 5, MsgID: store.Ptr("m1")}); err != nil {
		t.Fatal(err)
	}
	ent := &entry{ID: "m1"}
	idx := int64(2)
	if !e.b.mergeBatchSlot(ctx, "7", ent, inMsg{ID: "m1", BatchID: "B", BatchIndex: &idx}) {
		t.Fatal("merge should touch the row")
	}
	if ent.BatchID != "B" || ent.BatchIndex == nil || *ent.BatchIndex != 2 {
		t.Fatalf("%+v", ent)
	}
	m, _ := e.st.LoadCache(ctx, proto.ProtocolTelegram)
	if r := m["7"]; len(r) != 1 || store.Deref(r[0].BatchID) != "B" {
		t.Fatalf("%+v", r)
	}
	// a second merge touches no row
	if e.b.mergeBatchSlot(ctx, "7", &entry{ID: "m1"}, inMsg{ID: "m1", BatchID: "C"}) {
		t.Fatal("already batched")
	}
}
