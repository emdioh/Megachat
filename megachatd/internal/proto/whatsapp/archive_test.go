package whatsapp

import (
	"context"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

// The phone archives the chat under its @lid while the contact list shows the
// folded @c.us contact: the flag must land on the listed contact and be
// announced (and an unarchive must clear every JID form).
func TestArchiveEventOnLIDTwinFlagsListedContact(t *testing.T) {
	e := newEnv(t)
	e.lids.put("189025889575055", "391234567890")
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{"phone": "391234567890", "lid": "189025889575055@lid"}))
	e.b.noteChat("391234567890@c.us", 1000, "Mario")
	e.b.noteChat("189025889575055@lid", 900, "")
	drain(e.b)
	e.b.handleEvent(&events.Archive{JID: types.NewJID("189025889575055", types.HiddenUserServer),
		Action: &waSyncAction.ArchiveChatAction{Archived: gproto.Bool(true)}})
	ev := one(t, drain(e.b))
	if ev.Type != proto.EventContactUpdate || ev.ContactID != "391234567890@c.us" {
		t.Fatalf("archive event: %+v", ev)
	}
	c, _ := e.b.FindContact("391234567890@c.us")
	if !exBool(&c, "archived") {
		t.Fatal("the listed contact must be archived")
	}
	e.b.cmu.RLock()
	for _, id := range []string{"391234567890@c.us", "189025889575055@lid"} {
		if !e.b.chats[id].Archived {
			t.Errorf("%s: chat registry entry must be archived", id)
		}
	}
	e.b.cmu.RUnlock()
	// Unarchive through the phone-JID form clears the LID twin too, so a later
	// registry rebuild cannot resurrect the flag.
	e.b.handleEvent(&events.Archive{JID: types.NewJID("391234567890", types.DefaultUserServer),
		Action: &waSyncAction.ArchiveChatAction{Archived: gproto.Bool(false)}})
	one(t, drain(e.b))
	e.b.connected.Store(true)
	e.b.RefreshContacts(ctx, true)
	c, _ = e.b.FindContact("391234567890@c.us")
	if exBool(&c, "archived") {
		t.Error("unarchived chat must not come back archived after a refresh")
	}
}

// A refresh that learns the phone archived a chat (flag only in the registry)
// must publish contact_update, or the SPA never moves the chat.
func TestRefreshContactsPublishesArchiveChange(t *testing.T) {
	e := newEnv(t)
	e.b.connected.Store(true)
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{}))
	e.b.noteChat("391234567890@c.us", 1000, "Mario")
	e.b.loadContacts(ctx)
	drain(e.b)
	e.b.cmu.Lock()
	e.b.chats["391234567890@c.us"].Archived = true
	e.b.cmu.Unlock()
	e.b.RefreshContacts(ctx, true)
	found := false
	for _, ev := range drain(e.b) {
		if ev.Type == proto.EventContactUpdate && ev.ContactID == "391234567890@c.us" {
			if c, ok := ev.Payload["contact"].(proto.Contact); ok && exBool(&c, "archived") {
				found = true
			}
		}
	}
	if !found {
		t.Error("an archive change found by the refresh must be published")
	}
	c, _ := e.b.FindContact("391234567890@c.us")
	if !exBool(&c, "archived") {
		t.Error("contact must be archived")
	}
}

// The settings row is keyed by @lid; the chat is listed under the phone JID.
func TestSyncArchiveFlagsResolvesLIDTwin(t *testing.T) {
	e := newEnv(t)
	e.lids.put("189025889575055", "391234567890")
	e.b.RegisterContact(*newContact("391234567890@c.us", "Mario", map[string]any{"phone": "391234567890", "lid": "189025889575055@lid"}))
	e.b.noteChat("391234567890@c.us", 1000, "Mario")
	e.b.db = archivedDeviceStore(t, "189025889575055@lid")
	drain(e.b)
	e.b.syncArchiveFlags(context.Background())
	c, _ := e.b.FindContact("391234567890@c.us")
	if !exBool(&c, "archived") {
		t.Error("the listed phone contact must carry the archive flag stored under its @lid")
	}
}
