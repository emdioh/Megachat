// Package rpc is the Go port of the server side of server/socket_adapter.py:
// newline-delimited JSON-RPC 2.0 over a Unix socket (design doc section 4.2,
// CONTRACTS.md section 3). It depends only on internal/ops, internal/api,
// internal/bus and internal/config.
//
// Not covered here (done by the caller): the single-instance lock
// (/tmp/signal-tui.lock, a CLI concern in Python too) and the attachment
// staging policy of messages.send, which can be plugged in with WithPreCall.
package rpc
