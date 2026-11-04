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
	for _, id := range ids {
		j, err := JIDFromWAHA(id)
		if err != nil {
			continue
		}
		ls, err := d.ChatSettings.GetChatSettings(ctx, j)
		if err != nil || !ls.Found {
			continue
		}
		b.cmu.Lock()
		if m := b.chats[id]; m != nil && m.Archived != ls.Archived {
			m.Archived = ls.Archived
		}
		b.cmu.Unlock()
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
