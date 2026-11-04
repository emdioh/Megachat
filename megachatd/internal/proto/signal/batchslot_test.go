package signal

import (
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

var _ BatchSlotWriter = (*store.Store)(nil)

// With the real store the repair stamps slotless rows and the rollback deletes
// the mirror rows (no more "store lacks BatchSlotWriter" fallbacks).
func TestBatchSlotWriterWithRealStore(t *testing.T) {
	h := newHarness(t)
	h.offline()
	for _, a := range []string{"a.png", "b.png"} {
		if _, _, err := h.st.AddMessage(bg, store.NewMessage{Protocol: proto.ProtocolSignal, ContactNumber: "+1",
			Text: "", IsMine: true, Timestamp: 5, MsgID: store.Ptr("m1"), AttachmentID: store.Ptr(a)}); err != nil {
			t.Fatal(err)
		}
	}
	h.b.ingestMu.Lock()
	h.b.repairBatchSlots(bg, "+1", "m1", "B1", 2)
	h.b.ingestMu.Unlock()
	got := rows(t, h, "+1")
	if len(got) != 2 || store.Deref(got[0].BatchID) != "B1" || got[0].BatchIndex == nil || *got[0].BatchIndex != 0 ||
		*got[1].BatchIndex != 1 {
		t.Fatalf("%+v", got)
	}
	if err := h.st.DeleteMirrorRows(bg, proto.ProtocolSignal, "+1", "m1", []string{"a.png"}); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, h, "+1")); n != 1 {
		t.Fatal(n)
	}
}
