// Package messages is the Go port of server/messages.py (the persisted-message
// row builder) and of the operations built on it: messages.page,
// messages.window and messages.snapshot (server/ops/messages.py).
//
// messages.page returns presentation rows read from SQLite (cross-key
// read-union, twin dedup, attachment and caption normalisation, quote
// thumbnails, group sender resolution, aggregated reactions).
// messages.window and messages.snapshot return the TUI's canonical cache rows;
// the Go server keeps no separate in-memory cache, so they are rendered from
// the store, which is what the Python cache is seeded from.
package messages

import (
	"context"

	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Deps are the collaborators of RegisterOps. *core.Core implements it.
type Deps interface {
	Store() *store.Store
	Manager() *proto.Manager
	Config() *config.Config
	// Contacts lists the contacts of one protocol (false: no such backend).
	Contacts(ctx context.Context, protocol string) ([]proto.Contact, bool)
	// FindContact is backend.find_contact: memory only.
	FindContact(ctx context.Context, protocol, id string) (proto.Contact, bool)
}
