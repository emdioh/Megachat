//go:build !contracttest

package main

import "log/slog"

// applyFakeBackend is a no-op: the MEGACHAT_FAKE_BACKEND hook is only compiled
// into contract-test builds (-tags contracttest, see
// fakebackend_contracttest.go). A stray env var on a real install is ignored.
func applyFakeBackend(*BuildOptions, *slog.Logger) bool { return false }
