package whatsapp

import (
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

func webMsg(chat, id string, fromMe bool, ts uint64, text string, status waWeb.WebMessageInfo_Status) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{RemoteJID: gproto.String(chat), FromMe: gproto.Bool(fromMe), ID: gproto.String(id)},
		MessageTimestamp: gproto.Uint64(ts),
		Message:          &waE2E.Message{Conversation: gproto.String(text)},
		Status:           status.Enum(),
	}}
}

func conv(id string, unread uint32, msgs ...*waHistorySync.HistorySyncMsg) *waHistorySync.Conversation {
	return &waHistorySync.Conversation{
		ID: gproto.String(id), Messages: msgs, UnreadCount: gproto.Uint32(unread),
		ConversationTimestamp: gproto.Uint64(1_700_000_100),
	}
}

func TestHistorySyncIngestsWithoutPublishingMessages(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0,
			webMsg(chat, "3EB0A1", false, 1_700_000_001, "ciao", waWeb.WebMessageInfo_READ),
			webMsg(chat, "3EB0A2", true, 1_700_000_002, "dimmi", waWeb.WebMessageInfo_READ),
			webMsg(chat, "3EB0A3", true, 1_700_000_003, "ok?", waWeb.WebMessageInfo_DELIVERY_ACK),
			webMsg(chat, "3EB0A4", true, 1_700_000_004, "sent only", waWeb.WebMessageInfo_SERVER_ACK),
		)},
	}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].MsgID == nil || *rows[0].MsgID != "false_391234567890@c.us_3EB0A1" || rows[1].MsgID == nil || *rows[1].MsgID != "true_391234567890@c.us_3EB0A2" {
		t.Errorf("history ids use the WAHA format: %v %v", store0(rows[0].MsgID), store0(rows[1].MsgID))
	}
	if rows[0].Timestamp != 1_700_000_001_000 {
		t.Errorf("timestamp in ms: %d", rows[0].Timestamp)
	}
	evs := drain(e.b)
	var receipts []proto.Event
	for _, ev := range evs {
		if ev.Type == proto.EventMessage {
			t.Errorf("history must not publish message events: %+v", ev)
		}
		if ev.Type == proto.EventReceipt {
			receipts = append(receipts, ev)
		}
	}
	// delivered first, then read — only for our own messages with ack >= DEVICE.
	if len(receipts) != 2 || receipts[0].Payload["is_read"] != false || receipts[1].Payload["is_read"] != true {
		t.Fatalf("receipts: %+v", receipts)
	}
	// The chat became a known contact.
	if _, ok := e.b.FindContact("391234567890@c.us"); !ok {
		t.Error("a chat that came with the history must be listed")
	}
	// Re-ingesting the same history is idempotent.
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0, webMsg(chat, "3EB0A1", false, 1_700_000_001, "ciao", waWeb.WebMessageInfo_READ))}}})
	if len(dbRows(t, e, "391234567890@c.us")) != 4 {
		t.Error("duplicates after a second sync")
	}
}

func store0(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestHistorySyncAppliesEditedTextToKnownMessage(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_3EB0A1", "text": "vecchio", "is_mine": false}, 1_700_000_001_000)
	e.b.loadProtocolCache(ctx)
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0, webMsg(chat, "3EB0A1", false, 1_700_000_001, "nuovo", waWeb.WebMessageInfo_READ))}}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 1 || store0(rows[0].Text) != "nuovo" || !rows[0].Edited {
		t.Fatalf("history carrying edited text updates the row: %+v", rows)
	}
}

func TestHistorySyncUnreadZeroFlagsChatReadOnPhone(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0, webMsg(chat, "3EB0A1", false, 1_700_000_001, "ciao", waWeb.WebMessageInfo_READ))}}})
	e.b.ReconcileUnread(ctx)
	if rows := dbRows(t, e, "391234567890@c.us"); !rows[0].Read {
		t.Error("a chat that the phone reports fully read is marked read")
	}
	// Chats with unread messages are untouched.
	e2 := newEnv(t)
	e2.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 2, webMsg(chat, "3EB0A1", false, 1_700_000_001, "ciao", waWeb.WebMessageInfo_READ))}}})
	e2.b.ReconcileUnread(ctx)
	if rows := dbRows(t, e2, "391234567890@c.us"); rows[0].Read {
		t.Error("unread chat must stay unread")
	}
}

func TestHistorySyncKeepsOnlyTheUnreadOnThePhone(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	msg := func(id string, ts uint64) *waHistorySync.HistorySyncMsg {
		return webMsg(chat, id, false, ts, "m"+id, waWeb.WebMessageInfo_READ)
	}
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 2,
			msg("A1", 1_700_000_001), msg("A2", 1_700_000_002), msg("A3", 1_700_000_003), msg("A4", 1_700_000_004))}}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	var unread []int64
	for _, r := range rows {
		if !r.Read {
			unread = append(unread, r.Timestamp)
		}
	}
	// The phone reports 2 unread: only the newest two stay unread, the older
	// history (read on the phone) does not inflate the badge.
	if len(unread) != 2 || unread[0] != 1_700_000_003_000 || unread[1] != 1_700_000_004_000 {
		t.Fatalf("unread rows = %v", unread)
	}
}

func TestHistorySyncOnDemandIngestsAsRead(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{conv(chat, 3,
			webMsg(chat, "B1", false, 1_700_000_001, "a", waWeb.WebMessageInfo_READ),
			webMsg(chat, "B2", false, 1_700_000_002, "b", waWeb.WebMessageInfo_READ))}}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		if !r.Read {
			t.Fatalf("on-demand history is old, already-read history: %+v", r)
		}
	}
}

func TestHistorySyncSkipsBroadcastsAndGroupsKeepSenders(t *testing.T) {
	e := newEnv(t)
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			conv("status@broadcast", 0, webMsg("status@broadcast", "S1", false, 1, "story", waWeb.WebMessageInfo_READ)),
		}}})
	if len(dbRows(t, e, "status@broadcast")) != 0 {
		t.Error("broadcast chats are never ingested")
	}
}

func TestFetchHistoryReturnsCacheAndAsksPhoneOnlyWhenSparse(t *testing.T) {
	e := newEnv(t)
	for i, id := range []string{"AA01", "AA02", "AA03"} {
		e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_" + id, "text": "m" + id, "is_mine": false}, int64(1000*(i+1)))
	}
	e.b.loadProtocolCache(ctx)
	// Enough cached: no request to the phone.
	msgs, err := e.b.FetchHistory(ctx, "391234567890@c.us", 3)
	if err != nil || len(msgs) != 3 || len(e.fc.peerSent) != 0 {
		t.Fatalf("msgs=%d peer=%d err=%v", len(msgs), len(e.fc.peerSent), err)
	}
	if store0(msgs[0].ID) != "false_391234567890@c.us_AA01" || msgs[0].Protocol == nil || *msgs[0].Protocol != "whatsapp" {
		t.Errorf("oldest first: %+v", msgs[0])
	}
	// Sparse: an on-demand history request anchored on the OLDEST message.
	start := time.Now()
	msgs, _ = e.b.FetchHistory(ctx, "391234567890@c.us", 20)
	if len(msgs) != 3 || len(e.fc.peerSent) != 1 {
		t.Fatalf("msgs=%d peer=%d", len(msgs), len(e.fc.peerSent))
	}
	req := e.fc.peerSent[0].GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
	if req.GetOldestMsgID() != "AA01" || req.GetChatJID() != "391234567890@s.whatsapp.net" || req.GetOnDemandMsgCount() < 50 {
		t.Errorf("history request: %+v", req)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the wait is bounded")
	}
	// Throttled per chat.
	e.b.FetchHistory(ctx, "391234567890@c.us", 20)
	if len(e.fc.peerSent) != 1 {
		t.Error("requests are throttled per chat")
	}
	// Non-fetchable ids and an empty chat do nothing.
	if m, _ := e.b.FetchHistory(ctx, "status@broadcast", 20); m != nil {
		t.Error("non-fetchable")
	}
	if m, _ := e.b.FetchHistory(ctx, "999@c.us", 20); len(m) != 0 || len(e.fc.peerSent) != 1 {
		t.Error("no anchor, no request")
	}
}

func TestFetchHistoryWakesWhenTheAnswerArrives(t *testing.T) {
	e := newEnv(t)
	e.b.historyTimeout = 5 * time.Second
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{"id": "false_391234567890@c.us_AA09", "text": "recente", "is_mine": false}, 9_000_000_000_000/1000+1_700_000_100_000)
	e.b.loadProtocolCache(ctx)
	chat := "391234567890@s.whatsapp.net"
	go func() {
		for i := 0; i < 200; i++ {
			time.Sleep(10 * time.Millisecond)
			e.fc.mu.Lock()
			n := len(e.fc.peerSent)
			e.fc.mu.Unlock()
			if n > 0 {
				break
			}
		}
		e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
			Conversations: []*waHistorySync.Conversation{conv(chat, 0, webMsg(chat, "3EB0OLD", false, 1_000, "vecchio", waWeb.WebMessageInfo_READ))}}})
	}()
	start := time.Now()
	msgs, _ := e.b.FetchHistory(ctx, "391234567890@c.us", 20)
	if len(msgs) != 2 || time.Since(start) > 5*time.Second {
		t.Fatalf("the older message from the answer should be included (n=%d, %v)", len(msgs), time.Since(start))
	}
}

func TestSelectResyncTargetsAndResync(t *testing.T) {
	e := newEnv(t)
	e.b.IngestMessage(ctx, "391111111111@c.us", map[string]any{"id": "false_391111111111@c.us_A1", "text": "a", "is_mine": false}, 1_000)
	e.b.IngestMessage(ctx, "392222222222@c.us", map[string]any{"id": "false_392222222222@c.us_A2", "text": "b", "is_mine": false}, 3_000)
	e.b.IngestMessage(ctx, "393333333333@c.us", map[string]any{"id": "false_393333333333@c.us_A3", "text": "c", "is_mine": false}, 2_000)
	e.b.IngestMessage(ctx, "status@broadcast", map[string]any{"id": "x", "text": "s", "is_mine": false}, 9_000)
	e.b.loadProtocolCache(ctx)
	e.b.noteChat("391111111111@c.us", 0, "")
	e.b.cmu.Lock()
	e.b.chats["391111111111@c.us"] = &chatMeta{Unread: 3, LastTS: 1_000}
	e.b.cmu.Unlock()
	got := e.b.selectResyncTargets(0)
	want := []string{"391111111111@c.us", "392222222222@c.us", "393333333333@c.us"} // unread first, then by recency
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if capped := e.b.selectResyncTargets(2); len(capped) != 2 || capped[0] != want[0] {
		t.Errorf("cap = %v", capped)
	}
	n, err := e.b.ResyncHistory(ctx, 0)
	if err != nil || n != 3 {
		t.Fatalf("resync = %d %v", n, err)
	}
	e.b.connected.Store(false)
	if n, _ := e.b.ResyncHistory(ctx, 0); n != 0 {
		t.Error("not connected")
	}
}
