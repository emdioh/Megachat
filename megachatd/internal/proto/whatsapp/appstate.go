package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	wastore "go.mau.fi/whatsmeow/store"
)

// appStateTimeout bounds the startup app-state sync (network work run after
// connect).
const appStateTimeout = 2 * time.Minute

// appStateMarker records that the one-time full app-state heal ran, so later
// starts only sync the deltas (the server pushes those anyway).
const appStateMarker = ".whatsapp-appstate-v1"

// startAppStateSync runs appStateSync once per process, in the background.
func (b *Backend) startAppStateSync() {
	b.appStateOnce.Do(func() { go b.appStateSync() })
}

// appStateSync pulls the app state the chat list depends on. whatsmeow keeps
// the state in its DB and only pushes updates as live events, so a fresh
// process knew nothing about it:
//
//   - critical_unblock_low carries the contact names (the address book), the
//     reason some chats stayed at "<n>@lid";
//   - regular_low carries the archive flags and the chats read on the phone
//     ("markChatAsRead", with the last-read timestamp that bounds the local
//     flush), the reason archived chats showed up in the main list and the
//     unread badge was inflated.
//
// The first run (or the first after this marker existed) does a full sync, so
// pre-existing databases are healed too; later runs only fetch what the store
// has not seen. The archive flags are also seeded from whatsmeow's
// chat-settings table, which survives restarts.
func (b *Backend) appStateSync() {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("WhatsApp app state sync panicked", "err", r)
		}
	}()
	cli := b.currentClient()
	if cli == nil || !b.connected.Load() {
		return
	}
	marker := b.appStateMarkerPath()
	full := marker == "" || !fileExists(marker)
	ctx, cancel := context.WithTimeout(context.Background(), appStateTimeout)
	defer cancel()
	ok := true
	for _, name := range []appstate.WAPatchName{
		appstate.WAPatchCriticalUnblockLow, // contact names
		appstate.WAPatchRegularLow,         // archive + markChatAsRead
	} {
		// A full sync re-emits every mutation (EnableAppStateEvents); the
		// incremental path is a no-op unless the stored version is stale.
		// whatsmeow's own "Full sync of app state ... completed" line is a
		// Debugf that the slog adapter drops, so log the outcome here.
		if err := cli.FetchAppState(ctx, name, full, !full); err != nil {
			ok = false
			b.log.Warn("WhatsApp app state sync failed", "patch", string(name), "full", full, "err", err)
		} else {
			b.log.Info("WhatsApp app state sync completed", "patch", string(name), "full", full)
		}
	}
	if full && ok && marker != "" {
		if err := os.WriteFile(marker, []byte("ok\n"), 0o644); err != nil {
			b.log.Debug("WhatsApp app state marker not written", "err", err)
		}
	}
	b.syncArchiveFlags(ctx)
	// The contact store changed under us: re-read the address book (emits the
	// renames), then import the LID links learned meanwhile, resolve the chats
	// still stuck at "<n>@lid" and flush the chats the phone reports read.
	b.RefreshContacts(ctx, true)
	b.lidBulkImport(ctx)
	b.lidResolveCandidates()
	// Forced: the markChatAsRead mutations above are already in readOnPhone,
	// but a contacts.list may have spent the 20 s throttle window just before
	// the app state arrived.
	b.reconcileUnread(ctx, true)
}

func (b *Backend) appStateMarkerPath() string {
	if b.dataDir == "" {
		return ""
	}
	return filepath.Join(b.dataDir, appStateMarker)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// syncArchiveFlags seeds the chat registry from whatsmeow's chat-settings
// store. The live events only carry changes made while this process runs, so
// without this the archived chats of a fresh process land in the main list.
func (b *Backend) syncArchiveFlags(ctx context.Context) {
	b.seedArchivedChats(ctx)
	d := b.device()
	if d == nil || d.ChatSettings == nil {
		return
	}
	b.cmu.RLock()
	ids := make([]string, 0, len(b.chats))
	for id := range b.chats {
		ids = append(ids, id)
	}
	b.cmu.RUnlock()
	done := map[string]bool{}
	for _, id := range ids {
		if done[id] {
			continue
		}
		b.cmu.RLock()
		twins := b.chatTwinsLocked(id)
		b.cmu.RUnlock()
		// The flag is per chat: any JID form of it may carry the settings row
		// (whatsmeow keys by whatever the phone sent, @lid or phone).
		for _, t := range twins {
			done[t] = true
		}
		for _, t := range twins {
			j, err := JIDFromWAHA(t)
			if err != nil {
				continue
			}
			ls, err := d.ChatSettings.GetChatSettings(ctx, j)
			if err != nil || !ls.Found {
				continue
			}
			b.setChatArchivedLocal(id, ls.Archived)
			break
		}
	}
}

// seedArchivedChats registers the chats whatsmeow has archived but that have no
// local history (no cached message, no contact). The registry is rebuilt from
// the message cache and the contacts, so without this those chats disappear
// from the chat list on every restart even though whatsmeow_chat_settings still
// has them archived. whatsmeow's store interface only offers point lookups, so
// the table is read straight from the device DB (like lidBulkImport does with
// whatsmeow_lid_map). Names come from the contact table, then "+<phone>".
// The table is keyed by (our_jid, chat_jid), so the read is scoped to the
// current device: a whatsmeow.db reused across pairings (or holding several
// accounts) would otherwise leak another account's archived chats into this
// account's chat list. whatsmeow's own ChatSettings statements bind
// client.Store.ID.String() as our_jid (the device JID, e.g.
// "393331234567:12@s.whatsapp.net"), so the same value is used here. Unpaired
// devices have no rows of their own and seed nothing.
func (b *Backend) seedArchivedChats(ctx context.Context) {
	b.lifeMu.Lock()
	db := b.db
	b.lifeMu.Unlock()
	d := b.device()
	if db == nil || d == nil || d.ID == nil {
		return
	}
	rows, err := db.QueryContext(ctx, "SELECT chat_jid FROM whatsmeow_chat_settings WHERE our_jid = ? AND archived", d.ID.String())
	if err != nil {
		b.log.Debug("archived chat settings unavailable", "err", err)
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		j, err := JIDFromWAHA(raw)
		if err != nil {
			continue
		}
		id := WAHAFromJID(j)
		if id == "" || isBroadcastJID(id) {
			continue
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		b.cmu.RLock()
		known := b.chats[id] != nil
		b.cmu.RUnlock()
		if !known {
			name := b.nameForChat(ctx, id)
			if name == "" {
				if phone := b.contactPhone(id); phone != "" {
					name = "+" + phone
				}
			}
			b.noteChat(id, 0, name)
		}
		b.setChatArchivedLocal(id, true)
	}
}

// device is the whatsmeow device of the current client, or nil.
func (b *Backend) device() *wastore.Device {
	cli := b.currentClient()
	if cli == nil {
		return nil
	}
	return cli.Device()
}
