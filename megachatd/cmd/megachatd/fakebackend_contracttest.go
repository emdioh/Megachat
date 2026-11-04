//go:build contracttest

package main

// This file exists only in contract-test builds (-tags contracttest). The
// default build compiles fakebackend_stub.go instead, so a stray
// MEGACHAT_FAKE_BACKEND on a real install cannot replace the real backends.

import (
	"log/slog"
	"os"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

// FakeBackendEnv, when set to a protocol id ("signal", "whatsapp",
// "telegram"), makes "serve" register the in-memory proto/fake backend for
// that protocol instead of the configured ones and seeds the contacts below.
// It is a contract-test hook: unset (the default) leaves the production wiring
// untouched.
const (
	FakeBackendEnv = "MEGACHAT_FAKE_BACKEND"
	FakeContactID  = "fake-contact"
	// FakeEmptyIDContactID is a seeded contact whose sends succeed with no
	// backend message id (message_ids: []).
	FakeEmptyIDContactID = "fake-empty-id"
)

// applyFakeBackend points opts at the in-memory fake when FakeBackendEnv is
// set and reports whether it did. log may be nil.
func applyFakeBackend(opts *BuildOptions, log *slog.Logger) bool {
	protocol := strings.TrimSpace(os.Getenv(FakeBackendEnv))
	if protocol == "" {
		return false
	}
	fb := fake.New(protocol)
	fb.SetContacts(
		proto.Contact{ID: FakeContactID, DisplayName: "Fake Contact", Protocol: protocol},
		proto.Contact{ID: FakeEmptyIDContactID, DisplayName: "Fake Empty Id", Protocol: protocol},
	)
	fb.SetEmptySendIDs(FakeEmptyIDContactID)
	opts.ExtraBackends = []proto.Backend{fb}
	opts.OnlyExtraBackends = true
	if log != nil {
		log.Warn("test-only fake backend registered", "protocol", protocol, "env", FakeBackendEnv)
	}
	return true
}
