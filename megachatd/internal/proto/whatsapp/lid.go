package whatsapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/types"
)

// lidStore is the LID<->phone mapping of the device store
// (store.LIDStore, a subset).
type lidStore interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
	GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error)
	PutLIDMapping(ctx context.Context, lid, jid types.JID) error
}

// lidEntry is one wa_lid_map.json entry ({"phone", "name", "resolved_at"}).
// The file format is the one Python wrote, so the learned mappings carry over.
type lidEntry struct {
	Phone      string `json:"phone"`
	Name       string `json:"name"`
	ResolvedAt int64  `json:"resolved_at"`
}

func (e lidEntry) MarshalJSON() ([]byte, error) {
	m := map[string]any{"phone": nil, "name": nil, "resolved_at": e.ResolvedAt}
	if e.Phone != "" {
		m["phone"] = e.Phone
	}
	if e.Name != "" {
		m["name"] = e.Name
	}
	return json.Marshal(m)
}

type lidFile struct {
	Version int                        `json:"version"`
	Entries map[string]json.RawMessage `json:"entries"`
}

// lidCacheLoad is _lid_cache_load: idempotent, never fails.
func (b *Backend) lidCacheLoad() {
	b.lidMu.Lock()
	defer b.lidMu.Unlock()
	b.lidCacheLoadLocked()
}

func (b *Backend) lidCacheLoadLocked() {
	if b.lidLoaded {
		return
	}
	b.lidLoaded = true
	b.lidMap = map[string]lidEntry{}
	if b.lidMapPath == "" {
		return
	}
	data, err := os.ReadFile(b.lidMapPath)
	if err != nil {
		return
	}
	var f lidFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for k, raw := range f.Entries {
		var e struct {
			Phone      any   `json:"phone"`
			Name       any   `json:"name"`
			ResolvedAt int64 `json:"resolved_at"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		le := lidEntry{ResolvedAt: e.ResolvedAt}
		if s, ok := e.Phone.(string); ok {
			le.Phone = s
		}
		if s, ok := e.Name.(string); ok {
			le.Name = s
		}
		b.lidMap[k] = le
	}
}

// lidCacheSave is _lid_cache_save: atomic, never fails loudly.
func (b *Backend) lidCacheSave() {
	b.lidMu.Lock()
	entries := make(map[string]lidEntry, len(b.lidMap))
	for k, v := range b.lidMap {
		entries[k] = v
	}
	path := b.lidMapPath
	b.lidMu.Unlock()
	if path == "" {
		return
	}
	data, err := json.Marshal(map[string]any{"version": 1, "entries": entries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		b.log.Warn("Failed to persist WhatsApp lid cache", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// deviceLIDs is the LID store of the device (or the test override).
func (b *Backend) deviceLIDs() lidStore {
	if b.lidStoreForTest != nil {
		return b.lidStoreForTest
	}
	cli := b.currentClient()
	if cli == nil {
		return nil
	}
	if d := cli.Device(); d != nil && d.LIDs != nil {
		return d.LIDs
	}
	return nil
}

// lidLookup is _lid_lookup: the phone (digits) of a "<n>@lid" id, memory and
// the device's own LID table only (no network); "" = unknown. The device
// table is whatsmeow's authority, so a hit there refreshes the JSON cache.
func (b *Backend) lidLookup(lid string) string {
	b.lidMu.Lock()
	b.lidCacheLoadLocked()
	e, ok := b.lidMap[lid]
	b.lidMu.Unlock()
	if ok && e.Phone != "" && nowFunc().Unix()-e.ResolvedAt <= int64(b.lidTTL/time.Second) {
		return e.Phone
	}
	if s := b.deviceLIDs(); s != nil {
		if j, err := JIDFromWAHA(lid); err == nil {
			if pn, err := s.GetPNForLID(context.Background(), j); err == nil && !pn.IsEmpty() && pn.User != "" {
				b.lidPut(lid, pn.User, e.Name)
				return pn.User
			}
		}
	}
	return ""
}

// lidPut records a resolved mapping (positive entry).
func (b *Backend) lidPut(lid, phone, name string) {
	b.lidMu.Lock()
	b.lidCacheLoadLocked()
	if cur, ok := b.lidMap[lid]; ok && cur.Phone == phone && nowFunc().Unix()-cur.ResolvedAt < 3600 {
		b.lidMu.Unlock()
		return
	}
	b.lidMap[lid] = lidEntry{Phone: phone, Name: name, ResolvedAt: nowFunc().Unix()}
	b.lidMu.Unlock()
	b.lidCacheSave()
}

// lidName is the cached name of a lid ("" if none).
func (b *Backend) lidName(lid string) string {
	b.lidMu.Lock()
	defer b.lidMu.Unlock()
	b.lidCacheLoadLocked()
	return b.lidMap[lid].Name
}

// phoneToLID is _phone_to_lid: reverse lookup phone -> "<n>@lid" (no network).
func (b *Backend) phoneToLID(phone string) string {
	if phone == "" {
		return ""
	}
	b.lidMu.Lock()
	b.lidCacheLoadLocked()
	now := nowFunc().Unix()
	var found string
	for lid, e := range b.lidMap {
		if e.Phone == phone && now-e.ResolvedAt <= int64(b.lidTTL/time.Second) {
			found = lid
			break
		}
	}
	b.lidMu.Unlock()
	if found != "" {
		return found
	}
	if s := b.deviceLIDs(); s != nil {
		if lid, err := s.GetLIDForPN(context.Background(), types.NewJID(phone, types.DefaultUserServer)); err == nil && !lid.IsEmpty() && lid.User != "" {
			return lid.User + "@lid"
		}
	}
	return ""
}

// JIDToPhone implements messages.JIDPhoneResolver (_jid_to_phone): the
// best-effort phone (digits) of a WhatsApp id, or "". "@c.us" ids carry the
// phone; "@lid" ids go through the LID cache (memory only). Everything else
// (groups, broadcasts, ...) has no phone.
func (b *Backend) JIDToPhone(jid string) string {
	if jid == "" || !strings.Contains(jid, "@") {
		return ""
	}
	switch {
	case strings.HasSuffix(jid, "@lid"):
		return b.lidLookup(jid)
	case strings.HasSuffix(jid, "@c.us"), strings.HasSuffix(jid, "@s.whatsapp.net"):
		return jidDigits(strings.SplitN(jid, "@", 2)[0])
	}
	return ""
}

// lidResolveInterval throttles the resolver: mappings keep arriving with the
// live traffic and the app-state syncs, and every run is local (no network).
const lidResolveInterval = 30 * time.Second

// StartLIDResolver implements core.LIDResolverStarter (start_lid_resolver):
// imports the device's LID table into the cache and applies the phone/name of
// the chats still under an unresolved @lid, then publishes lid_warmup_done.
//
// It is re-runnable (throttled): whatsmeow keeps learning LID<->PN links from
// groups, live messages and app-state syncs after the startup pass, so a
// one-shot resolver left every later link unresolved ("<n>@lid" chats).
func (b *Backend) StartLIDResolver() {
	b.lidLastRunMu.Lock()
	if nowFunc().Sub(b.lidLastRun) < lidResolveInterval {
		b.lidLastRunMu.Unlock()
		return
	}
	b.lidLastRun = nowFunc()
	b.lidLastRunMu.Unlock()
	b.lidRunWG.Add(1)
	go func() {
		defer b.lidRunWG.Done()
		b.lidResolverRun()
	}()
}

func (b *Backend) lidResolverRun() {
	defer b.emit(proto.EventLidWarmupDone, "", nil)
	defer func() {
		if r := recover(); r != nil {
			b.log.Warn("WhatsApp lid resolver panicked", "err", r)
		}
	}()
	b.lidCacheLoad()
	b.lidBulkImport(context.Background())
	b.lidResolveCandidates()
}

// lidResolveCandidates resolves the chats still under an unresolved @lid from
// the device table (local lookups): a hit applies the phone and the address
// book name (and folds the identity twins), a miss leaves a 24 h negative
// entry.
func (b *Backend) lidResolveCandidates() {
	for _, id := range b.lidCandidates() {
		if phone := b.lidLookup(id); phone != "" {
			b.applyContactLIDResolution(id, phone)
			continue
		}
		b.lidMu.Lock()
		if _, ok := b.lidMap[id]; !ok {
			b.lidMap[id] = lidEntry{ResolvedAt: nowFunc().Unix()} // negative entry
		}
		b.lidMu.Unlock()
	}
	b.lidCacheSave()
	b.reapplyAddressBookNames()
}

func (b *Backend) lidCandidates() []string {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	var out []string
	for _, c := range b.contacts {
		if !strings.HasSuffix(c.ID, "@lid") {
			continue
		}
		// A raw "<n>@lid" display name is the symptom of a contact whose
		// mapping was applied without a name (RefreshContacts fills the phone
		// extra from the device table). It always gets a resolver pass, so the
		// name/book fallback can be applied; contacts already carrying a real
		// or "+phone" name are left alone to keep the pass idempotent.
		head, _, _ := strings.Cut(c.ID, "@")
		bareName := c.DisplayName == "" || c.DisplayName == c.ID || c.DisplayName == head
		if exStr(c, "phone") != "" && !bareName {
			continue // already resolved with a usable name
		}
		// A positive mapping may exist in the cache without ever having been
		// applied to the contact (rebuildAliasesLocked and JIDToPhone fill the
		// cache); only a fresh negative entry means "nothing to do yet".
		b.lidMu.Lock()
		b.lidCacheLoadLocked()
		e, ok := b.lidMap[c.ID]
		b.lidMu.Unlock()
		if ok && e.Phone == "" && nowFunc().Unix()-e.ResolvedAt <= 24*3600 {
			continue
		}
		out = append(out, c.ID)
	}
	return out
}

// lidBulkImport is _lid_bulk_import: whatsmeow keeps every LID<->PN link it
// learned in whatsmeow_lid_map (groups are the main source); import them all.
func (b *Backend) lidBulkImport(ctx context.Context) int {
	b.lifeMu.Lock()
	db := b.db
	b.lifeMu.Unlock()
	if db == nil {
		return 0
	}
	rows, err := db.QueryContext(ctx, "SELECT lid, pn FROM whatsmeow_lid_map")
	if err != nil {
		b.log.Debug("lid bulk import failed", "err", err)
		return 0
	}
	defer rows.Close()
	type pair struct{ lid, pn string }
	var pairs []pair
	for rows.Next() {
		var lid, pn string
		if rows.Scan(&lid, &pn) == nil && lid != "" && pn != "" {
			pairs = append(pairs, pair{lid, pn})
		}
	}
	b.lidMu.Lock()
	b.lidCacheLoadLocked()
	changed := 0
	now := nowFunc().Unix()
	for _, p := range pairs {
		id := p.lid + "@lid"
		phone := jidDigits(p.pn)
		if phone == "" {
			continue
		}
		if e, ok := b.lidMap[id]; ok && e.Phone == phone {
			continue
		}
		b.lidMap[id] = lidEntry{Phone: phone, Name: b.lidMap[id].Name, ResolvedAt: now}
		changed++
	}
	b.lidMu.Unlock()
	if changed > 0 {
		b.lidCacheSave()
	}
	return changed
}
