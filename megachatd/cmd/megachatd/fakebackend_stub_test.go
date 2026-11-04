//go:build !contracttest

package main

import (
	"io"
	"log/slog"
	"testing"
)

// The fake-backend hook lives behind //go:build contracttest: this (default)
// build must ignore a stray MEGACHAT_FAKE_BACKEND entirely. The env var is
// spelled out on purpose: the constant does not exist without the tag.
func TestFakeBackendEnvIgnoredWithoutContracttestTag(t *testing.T) {
	t.Setenv("MEGACHAT_FAKE_BACKEND", "signal")
	e := newTestEnv(t)
	opts := BuildOptions{Config: e.cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if applyFakeBackend(&opts, opts.Log) {
		t.Fatal("applyFakeBackend registered a fake backend in the default build")
	}
	if opts.ExtraBackends != nil || opts.OnlyExtraBackends {
		t.Fatalf("MEGACHAT_FAKE_BACKEND leaked into the default build: %+v", opts)
	}
}
