package whatsapp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// ---- HD dual upload (48c5d46) -----------------------------------------------------------------

func mediaWebMsg(chat, id string, fromMe bool, ts uint64, m *waE2E.Message) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{RemoteJID: gproto.String(chat), FromMe: gproto.Bool(fromMe), ID: gproto.String(id)},
		MessageTimestamp: gproto.Uint64(ts),
		Message:          m,
		Status:           waWeb.WebMessageInfo_READ.Enum(),
	}}
}

func TestHistorySyncSkipsHDChild(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0,
			mediaWebMsg(chat, "3A00000000000000000A", false, 1_700_000_001, hdParentMsg("foto con caption")),
			mediaWebMsg(chat, "3A00000000000000000B", false, 1_700_000_002, hdChildMsg("foto con caption")),
		)},
	}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 1 || store0(rows[0].MsgID) != "false_391234567890@c.us_3A00000000000000000A" {
		t.Fatalf("history must keep only the SD parent: %+v", rows)
	}
	// The child's media is not remembered either: nothing to download.
	childKey := CanonicalMsgID("false_391234567890@c.us_3A00000000000000000B")
	e.b.mediaInfo.mu.Lock()
	_, remembered := e.b.mediaInfo.mem[childKey]
	e.b.mediaInfo.mu.Unlock()
	if remembered {
		t.Error("the HD child media must not be remembered")
	}
	for _, ev := range drain(e.b) {
		if ev.Type == proto.EventMessage {
			t.Errorf("history must not publish message events: %+v", ev)
		}
	}
}

// ---- history reconcile ghost (48c5d46) -------------------------------------------------------

func TestHistorySyncKeepsRepeatedTextSentFromPhone(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	oldID, newID := "3A00000000000000000001", "3A00000000000000000002"
	if _, err := e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{
		"id": "true_391234567890@c.us_" + oldID, "text": "Grazie mille", "is_mine": true}, reconcileTestTS); err != nil {
		t.Fatal(err)
	}
	newTS := reconcileTestTS + 4*reconcileWeek
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 0,
			webMsg(chat, oldID, true, uint64(reconcileTestTS/1000), "Grazie mille", waWeb.WebMessageInfo_READ),
			webMsg(chat, newID, true, uint64(newTS/1000), "Grazie mille", waWeb.WebMessageInfo_READ),
		)},
	}})
	rows := dbRows(t, e, "391234567890@c.us")
	want := []string{"true_391234567890@c.us_" + oldID, "true_391234567890@c.us_" + newID}
	if len(rows) != len(want) {
		t.Fatalf("both sends must survive the reconcile: %+v", rows)
	}
	for i, w := range want {
		if got := store0(rows[i].MsgID); got != w {
			t.Errorf("row %d id = %q, want %q", i, got, w)
		}
	}
}

// ---- authoritative unread count (mc-zao.3.5.2) -----------------------------------------------

// unreadTimestamps is the timestamps of the chat's rows still flagged unread.
func unreadTimestamps(t *testing.T, e *env, contact string) []int64 {
	t.Helper()
	var out []int64
	for _, r := range dbRows(t, e, contact) {
		if !r.Read {
			out = append(out, r.Timestamp)
		}
	}
	return out
}

// A conversation without messages (PUSH_NAME/RECENT syncs) still reports the
// phone's unread count: dropping it left chats whose history was ingested
// unread — and whose read state never changes on the phone, so no
// markChatAsRead entry ever arrives — inflated forever.
func TestHistorySyncMessageLessConversationAppliesUnreadCount(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	seedIncoming(t, e, "391234567890@c.us",
		1_700_000_001_000, 1_700_000_002_000, 1_700_000_003_000, 1_700_000_004_000)
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{{
			ID:                    gproto.String(chat),
			UnreadCount:           gproto.Uint32(2),
			ConversationTimestamp: gproto.Uint64(1_700_000_100),
		}},
	}})
	got := unreadTimestamps(t, e, "391234567890@c.us")
	want := []int64{1_700_000_003_000, 1_700_000_004_000}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unread rows = %v, want %v", got, want)
	}
	// The count is remembered: the next pass applies it without asking the
	// phone and without moving the badge.
	e.b.ReconcileUnread(ctx)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 0 {
		t.Errorf("a chat with a trusted count must not be asked again: %d requests", len(e.fc.peerSent))
	}
	if got := unreadTimestamps(t, e, "391234567890@c.us"); len(got) != 2 {
		t.Fatalf("unread rows after a second pass = %v", got)
	}
}

// A chat whose local unread has no authoritative count is asked for its
// history: FetchHistory only requests sparse caches, so a full one would never
// hear the phone's count. Once per chat per process.
func TestReconcileUnreadAsksThePhoneForChatsWithoutASnapshot(t *testing.T) {
	e := newEnv(t)
	seedIncoming(t, e, "391234567890@c.us", 1_000, 2_000)
	e.b.ReconcileUnread(ctx)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 1 {
		t.Fatalf("the phone must be asked for the chat's count: %d requests", len(e.fc.peerSent))
	}
	req := e.fc.peerSent[0].GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
	if req.GetChatJID() != "391234567890@s.whatsapp.net" {
		t.Errorf("asked for %q", req.GetChatJID())
	}
	e.b.reconcileUnread(ctx, true)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 1 {
		t.Errorf("asked again: %d requests", len(e.fc.peerSent))
	}
}

// An on-demand page is old history: what it delivered is read, but the chat's
// newer unread messages must survive it (bounding the page by the conversation
// timestamp marked them read too, and the count cannot be restored).
func TestOnDemandHistoryKeepsTheNewerUnread(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	seedIncoming(t, e, "391234567890@c.us", 1_700_000_200_000, 1_700_000_201_000, 1_700_000_202_000)
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{
			// The phone's count (and its boundary) covers the whole chat: the
			// three unread messages are newer than the page it delivered.
			ID: gproto.String(chat), UnreadCount: gproto.Uint32(3),
			ConversationTimestamp: gproto.Uint64(1_700_000_300),
			Messages: []*waHistorySync.HistorySyncMsg{
				webMsg(chat, "C1", false, 1_700_000_001, "vecchio", waWeb.WebMessageInfo_READ),
				webMsg(chat, "C2", false, 1_700_000_002, "vecchio", waWeb.WebMessageInfo_READ),
			},
		}},
	}})
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 5 {
		t.Fatalf("rows = %d", len(rows))
	}
	got := unreadTimestamps(t, e, "391234567890@c.us")
	want := []int64{1_700_000_200_000, 1_700_000_201_000, 1_700_000_202_000}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("unread rows = %v, want %v", got, want)
	}
	// The page's own messages are read, and the count it reported is trusted.
	for _, r := range rows {
		if r.Timestamp <= 1_700_000_002_000 && !r.Read {
			t.Errorf("on-demand page message left unread: %+v", r)
		}
	}
	e.b.ReconcileUnread(ctx)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 0 {
		t.Errorf("the sync already reported the count: %d requests", len(e.fc.peerSent))
	}
}

// The authoritative count is not a one-shot: history that lands after the sync
// (an older build's database, a folded @lid copy) is reconciled by the next
// pass, or it re-inflates the badge.
func TestUnreadSnapshotIsReappliedToLateHistory(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	seedIncoming(t, e, "391234567890@c.us", 1_700_000_010_000, 1_700_000_011_000)
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conv(chat, 1,
			webMsg(chat, "D2", false, 1_700_000_011, "unread", waWeb.WebMessageInfo_READ))},
	}})
	if _, err := e.b.IngestMessage(ctx, "391234567890@c.us",
		map[string]any{"id": "false_391234567890@c.us_D1", "text": "vecchio", "is_mine": false}, 1_700_000_001_000); err != nil {
		t.Fatal(err)
	}
	e.b.reconcileUnread(ctx, true)
	e.b.unreadHealWG.Wait()
	got := unreadTimestamps(t, e, "391234567890@c.us")
	if len(got) != 1 || got[0] != 1_700_000_011_000 {
		t.Fatalf("unread rows = %v, want the newest one", got)
	}
}

// The reported case end to end: history ingested unread, no markChatAsRead
// entry, full local cache. The pass asks the phone, the on-demand answer's
// count becomes the badge, and the page it carried is read.
func TestUnreadHealAsksAndAppliesThePhoneCount(t *testing.T) {
	e := newEnv(t)
	chat := "391234567890@s.whatsapp.net"
	var times []int64
	for i := int64(1); i <= 12; i++ {
		times = append(times, 1_700_000_000_000+i*1_000)
	}
	seedIncoming(t, e, "391234567890@c.us", times...)
	e.b.ReconcileUnread(ctx)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 1 {
		t.Fatalf("history request for the untrusted chat: %d", len(e.fc.peerSent))
	}
	e.b.onHistorySync(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{
			ID: gproto.String(chat), UnreadCount: gproto.Uint32(2),
			ConversationTimestamp: gproto.Uint64(1_700_000_100),
			Messages: []*waHistorySync.HistorySyncMsg{
				webMsg(chat, "E1", false, 1_699_999_990, "vecchio", waWeb.WebMessageInfo_READ),
				webMsg(chat, "E2", false, 1_699_999_991, "vecchio", waWeb.WebMessageInfo_READ),
			},
		}},
	}})
	got := unreadTimestamps(t, e, "391234567890@c.us")
	want := []int64{1_700_000_011_000, 1_700_000_012_000}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unread rows = %v, want the phone's two %v", got, want)
	}
	// A second pass keeps the answer and asks nothing more.
	e.b.reconcileUnread(ctx, true)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != 1 {
		t.Errorf("asked again: %d", len(e.fc.peerSent))
	}
	if got := unreadTimestamps(t, e, "391234567890@c.us"); len(got) != 2 {
		t.Errorf("unread rows after a second pass = %v", got)
	}
}

// askChats maps the on-demand history requests the fake phone received to the
// chat JIDs asked, in send order.
func askChats(fc *fakeClient) []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	out := make([]string, 0, len(fc.peerSent))
	for _, m := range fc.peerSent {
		out = append(out, m.GetProtocolMessage().GetPeerDataOperationRequestMessage().
			GetHistorySyncOnDemandRequest().GetChatJID())
	}
	return out
}

// Every chat with local unread and no trusted count would otherwise get its
// own history request back to back: a burst of peer requests the phone may
// rate limit. The heal is capped per process, asks the worst chats first and
// spaces the requests.
func TestUnreadHealCapsAndPacesPhoneRequests(t *testing.T) {
	e := newEnv(t)
	seed := func(i int) string {
		chat := fmt.Sprintf("39333123%04d@c.us", i)
		times := make([]int64, i)
		for j := range times {
			times[j] = 1_700_000_000_000 + int64(j)*1_000
		}
		seedIncoming(t, e, chat, times...)
		return chat
	}
	chats := make([]string, 12)
	for i := 1; i <= 12; i++ {
		chats[i-1] = seed(i)
	}
	// Fake clock and no real sleeping: each wait advances the clock by the
	// interval it was asked for, and is recorded.
	fakeNow := time.Unix(1_700_000_000, 0)
	restoreNow := nowFunc
	nowFunc = func() time.Time { return fakeNow }
	defer func() { nowFunc = restoreNow }()
	var waits []time.Duration
	restoreWait := unreadAskWait
	unreadAskWait = func(_ context.Context, d time.Duration) bool {
		waits = append(waits, d)
		fakeNow = fakeNow.Add(d)
		return true
	}
	defer func() { unreadAskWait = restoreWait }()

	e.b.reconcileUnread(ctx, true)
	e.b.unreadHealWG.Wait()

	if len(e.fc.peerSent) != unreadAskMaxChats {
		t.Fatalf("history requests = %d, want the cap %d", len(e.fc.peerSent), unreadAskMaxChats)
	}
	// Highest local unread first: 12 down to 3 unread rows.
	want := make([]string, 0, unreadAskMaxChats)
	for i := 12; i > 12-unreadAskMaxChats; i-- {
		id := strings.TrimSuffix(chats[i-1], "@c.us")
		want = append(want, id+"@s.whatsapp.net")
	}
	if got := askChats(e.fc); !slices.Equal(got, want) {
		t.Fatalf("asked chats = %v, want the highest unread first %v", got, want)
	}
	if len(waits) != unreadAskMaxChats-1 {
		t.Fatalf("waits = %v, want one between every pair of requests", waits)
	}
	for i, d := range waits {
		if d != unreadAskSpacing {
			t.Errorf("wait %d = %v, want %v", i, d, unreadAskSpacing)
		}
	}
	// The cap is per process: the next pass must not spend it again.
	e.b.reconcileUnread(ctx, true)
	e.b.unreadHealWG.Wait()
	if len(e.fc.peerSent) != unreadAskMaxChats {
		t.Errorf("requests after a second pass = %d, want the cap %d", len(e.fc.peerSent), unreadAskMaxChats)
	}
}

// The heal runs in the background: Disconnect must cancel a pending ask instead
// of leaving the goroutine alive for the whole spacing/history timeout.
func TestDisconnectCancelsPendingUnreadAsks(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 3; i++ {
		seedIncoming(t, e, fmt.Sprintf("39333124%04d@c.us", i), 1_700_000_000_000+int64(i))
	}
	waiting := make(chan struct{})
	var once sync.Once
	restoreWait := unreadAskWait
	unreadAskWait = func(ctx context.Context, _ time.Duration) bool {
		once.Do(func() { close(waiting) })
		<-ctx.Done()
		return false
	}
	defer func() { unreadAskWait = restoreWait }()

	e.b.reconcileUnread(ctx, true)
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("the heal never reached the pacing wait")
	}
	if err := e.b.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { e.b.unreadHealWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect did not end the pending unread asks")
	}
	if got := len(e.fc.peerSent); got != 1 {
		t.Errorf("requests = %d, want only the one in flight", got)
	}
}
