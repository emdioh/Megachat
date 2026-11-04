package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// MarkedIDsBackupSuffix is appended to config.json for the pre-migration copy.
const MarkedIDsBackupSuffix = ".pre-telegram-marked-ids.bak"

// MarkedIDsMigration reports what MigrateConfigToMarkedIDs did.
type MarkedIDsMigration struct {
	Rewritten  map[string]string // old config key -> new config key
	Dropped    []string          // raw keys removed because the marked key already existed
	Ambiguous  []string          // raw keys left alone: a user (or nothing) could own the id too
	BackupPath string            // "" when nothing was rewritten
}

// MarkedPeerCandidates returns the marked ids a raw positive Telegram id could
// stand for as a group (-id) or a channel (-100id).
func MarkedPeerCandidates(raw int64) (group, channel int64) {
	return -raw, -channelOffset - raw
}

// MigrateConfigToMarkedIDs is the one-time, idempotent fix of the per-chat
// auto-transcribe overrides ("auto_transcribe_chats" in config.json, keys
// "telegram:<contact_id>").
//
// The Python backend listed groups/channels with their raw positive id, and
// the web UI keyed the override on that contact id, while messages (and the
// voice-note commands) used Telethon's marked id. megachatd uses marked ids
// everywhere, so a raw key "telegram:123" that belongs to a group/channel is
// rewritten to "telegram:-123" / "telegram:-1000000000123".
//
// known is the set of marked peer ids this account is known to have: the
// entities of the Telethon session plus the telegram contact_numbers of the
// messages table. A raw id is rewritten only when exactly one of its
// group/channel forms is known and the id is not also a known user; anything
// else (user, unknown, ambiguous) is left untouched. If the marked key already
// exists it wins (that is what voice notes actually used) and the raw key is
// dropped. config.json is copied to <config.json>.pre-telegram-marked-ids.bak
// before the first change (an existing backup is never overwritten).
func MigrateConfigToMarkedIDs(cfg *config.Config, known map[int64]bool) (MarkedIDsMigration, error) {
	res := MarkedIDsMigration{Rewritten: map[string]string{}}
	const section = "auto_transcribe_chats"
	chats, _ := cfg.LoadFile()[section].(map[string]any)
	if len(chats) == 0 {
		return res, nil
	}
	plan := map[string]string{} // old key -> new key ("" = drop)
	for key := range chats {
		rest, ok := strings.CutPrefix(key, "telegram:")
		if !ok {
			continue
		}
		raw, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || raw <= 0 {
			continue
		}
		group, channel := MarkedPeerCandidates(raw)
		var target int64
		switch {
		case known[raw]:
			res.Ambiguous = append(res.Ambiguous, key)
			continue
		case known[group] && !known[channel]:
			target = group
		case known[channel] && !known[group]:
			target = channel
		default:
			res.Ambiguous = append(res.Ambiguous, key)
			continue
		}
		newKey := "telegram:" + strconv.FormatInt(target, 10)
		if _, exists := chats[newKey]; exists {
			plan[key] = ""
		} else {
			plan[key] = newKey
		}
	}
	if len(plan) == 0 {
		return res, nil
	}

	backup := cfg.ConfigFile() + MarkedIDsBackupSuffix
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		data, err := os.ReadFile(cfg.ConfigFile())
		if err != nil {
			return res, fmt.Errorf("telegram: backing up config.json: %w", err)
		}
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			return res, fmt.Errorf("telegram: backing up config.json: %w", err)
		}
	}
	res.BackupPath = backup
	err := cfg.UpdateFile(func(d map[string]any) {
		cur, _ := d[section].(map[string]any)
		for oldKey, newKey := range plan {
			v, ok := cur[oldKey]
			if !ok {
				continue
			}
			delete(cur, oldKey)
			if newKey == "" {
				res.Dropped = append(res.Dropped, oldKey)
				continue
			}
			cur[newKey] = v
			res.Rewritten[oldKey] = newKey
		}
	})
	return res, err
}

// KnownMarkedIDs gathers the evidence MigrateConfigToMarkedIDs needs: the
// entity ids of the Telethon session (read-only) and the telegram
// contact_numbers of the message store. Either source may be missing.
func KnownMarkedIDs(ctx context.Context, sessionPath string, st *store.Store) map[int64]bool {
	known := map[int64]bool{}
	if sessionPath != "" {
		if _, err := os.Stat(sessionPath); err == nil {
			if imp, err := LoadTelethonSession(sessionPath); err == nil {
				for id := range imp.Peers {
					known[id] = true
				}
			}
		}
	}
	if st != nil {
		if ids, err := st.DistinctContacts(ctx, "telegram"); err == nil {
			for _, s := range ids {
				if n, err := strconv.ParseInt(s, 10, 64); err == nil {
					known[n] = true
				}
			}
		}
	}
	return known
}

// RunMarkedIDsMigration is the startup hook: it never fails the start.
func RunMarkedIDsMigration(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	res, err := MigrateConfigToMarkedIDs(cfg, KnownMarkedIDs(ctx, cfg.TelegramSessionPath(), st))
	if err != nil {
		log.Warn("Telegram: id migration of config.json failed", "err", err)
		return
	}
	if len(res.Rewritten)+len(res.Dropped) > 0 {
		log.Info("Telegram: auto-transcribe overrides moved to marked ids",
			"rewritten", len(res.Rewritten), "dropped", len(res.Dropped), "backup", res.BackupPath)
	}
	if len(res.Ambiguous) > 0 {
		log.Debug("Telegram: raw ids left as they are (users or unknown peers)", "keys", res.Ambiguous)
	}
}
