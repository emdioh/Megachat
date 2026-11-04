package core

import (
	"context"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// The Python backends owned their contact objects; the Go backends return
// values. The core therefore keeps two overlays: contacts it registered
// itself (ghosts, book-only) and the recency (last_message_ts) it advanced.

func (c *Core) applyRecency(ct proto.Contact) proto.Contact {
	c.mu.Lock()
	ts, ok := c.recency[ct.CacheKey()]
	c.mu.Unlock()
	if ok && ts > ct.LastMessageTS() {
		return ct.WithExtra("last_message_ts", ts)
	}
	return ct
}

// bumpRecency raises last_message_ts of the contact for cacheKey (ints only).
func (c *Core) bumpRecency(cacheKey string, ts any) bool {
	n, ok := asInt64(ts)
	if !ok {
		if _, isBool := ts.(bool); isBool || ts == nil {
			return false
		}
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > c.recency[cacheKey] {
		c.recency[cacheKey] = n
	}
	return true
}

// Contacts returns the contacts of one protocol: what the backend lists plus
// the contacts registered through the core, with recency applied.
func (c *Core) Contacts(ctx context.Context, protocol string) ([]proto.Contact, bool) {
	b, ok := c.mgr.Get(protocol)
	if !ok {
		return nil, false
	}
	list, err := b.ListContacts(ctx)
	if err != nil {
		c.log.Debug("list contacts failed", "protocol", protocol, "err", err)
	}
	seen := map[string]bool{}
	out := make([]proto.Contact, 0, len(list))
	for _, ct := range list {
		seen[ct.CacheKey()] = true
		out = append(out, c.applyRecency(ct))
	}
	c.mu.Lock()
	var extra []proto.Contact
	for k, ct := range c.registered {
		if ct.Protocol == protocol && !seen[k] {
			extra = append(extra, ct)
		}
	}
	c.mu.Unlock()
	sortContacts(extra)
	for _, ct := range extra {
		out = append(out, c.applyRecency(ct))
	}
	return out, true
}

// AllContacts merges the active chats of every backend (BackendManager.list_contacts).
func (c *Core) AllContacts(ctx context.Context) []proto.Contact {
	var out []proto.Contact
	for _, p := range c.mgr.Protocols() {
		cs, _ := c.Contacts(ctx, p)
		out = append(out, cs...)
	}
	return out
}

func sortContacts(l []proto.Contact) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && l[j].CacheKey() < l[j-1].CacheKey(); j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

// KnownContact reports whether the backend (or the core) lists the contact.
func (c *Core) KnownContact(ctx context.Context, protocol, id string) bool {
	key := proto.ContactCacheKey(protocol, id)
	c.mu.Lock()
	_, reg := c.registered[key]
	c.mu.Unlock()
	if reg {
		return true
	}
	b, ok := c.mgr.Get(protocol)
	if !ok {
		return false
	}
	list, _ := b.ListContacts(ctx)
	for _, ct := range list {
		if ct.ID == id {
			return true
		}
	}
	return false
}

// FindContact is backend.find_contact: memory only (backend finder, the
// backend's own list, then the contacts the core registered).
func (c *Core) FindContact(ctx context.Context, protocol, id string) (proto.Contact, bool) {
	if ct, ok := c.mgr.FindContact(protocol, id); ok {
		return c.applyRecency(ct), true
	}
	c.mu.Lock()
	ct, ok := c.registered[proto.ContactCacheKey(protocol, id)]
	c.mu.Unlock()
	if ok {
		return c.applyRecency(ct), true
	}
	if b, ok := c.mgr.Get(protocol); ok {
		list, _ := b.ListContacts(ctx)
		for _, ct := range list {
			if ct.ID == id {
				return c.applyRecency(ct), true
			}
		}
	}
	return proto.Contact{}, false
}

// RegisterContact makes a contact known to its backend (register_contact).
// Backends implementing ContactRegistrar decide; otherwise the core keeps it.
func (c *Core) RegisterContact(ct proto.Contact) bool {
	if b, ok := c.mgr.Get(ct.Protocol); ok {
		if r, ok := b.(ContactRegistrar); ok {
			return r.RegisterContact(ct)
		}
	}
	key := ct.CacheKey()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.registered[key]; ok {
		return false
	}
	c.registered[key] = ct
	return true
}

// syncRecency recovers last_message_ts from the stored messages (sync_recency).
func (c *Core) syncRecency(ctx context.Context, protocol string) {
	cs, ok := c.Contacts(ctx, protocol)
	if !ok {
		return
	}
	for _, ct := range cs {
		best := ct.LastMessageTS()
		rows, err := c.st.ListMessages(ctx, storeQuery(protocol, ContactStorageKeys(ct), 1))
		if err != nil {
			continue
		}
		for _, m := range rows {
			if m.Timestamp > best {
				best = m.Timestamp
			}
		}
		if best > ct.LastMessageTS() {
			c.bumpRecency(ct.CacheKey(), best)
		}
	}
}
