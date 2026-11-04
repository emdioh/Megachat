//go:build contracttest

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

// With -tags contracttest the env var does register the fake backend, seeds the
// two contract contacts and makes the empty-id contact report no id.
func TestFakeBackendHookEnabledByContracttestTag(t *testing.T) {
	t.Setenv(FakeBackendEnv, "signal")
	opts := BuildOptions{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if !applyFakeBackend(&opts, opts.Log) {
		t.Fatal("env var set but no fake backend registered")
	}
	if !opts.OnlyExtraBackends || len(opts.ExtraBackends) != 1 {
		t.Fatalf("opts = %+v", opts)
	}
	fb, ok := opts.ExtraBackends[0].(*fake.Backend)
	if !ok {
		t.Fatalf("want *fake.Backend, got %T", opts.ExtraBackends[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cs, err := fb.ListContacts(ctx)
	if err != nil || len(cs) != 2 || cs[0].ID != FakeContactID || cs[1].ID != FakeEmptyIDContactID {
		t.Fatalf("contacts = %+v, %v", cs, err)
	}
	if id, err := fb.SendMessage(ctx, FakeEmptyIDContactID, "x", proto.Quote{}); err != nil || id != "" {
		t.Fatalf("empty-id send = %q, %v", id, err)
	}
	if id, err := fb.SendMessage(ctx, FakeContactID, "x", proto.Quote{}); err != nil || id == "" {
		t.Fatalf("normal send = %q, %v", id, err)
	}

	// Unset: production wiring untouched.
	t.Setenv(FakeBackendEnv, "")
	off := BuildOptions{}
	if applyFakeBackend(&off, nil) || off.ExtraBackends != nil {
		t.Fatalf("hook fired without the env var: %+v", off)
	}
}
