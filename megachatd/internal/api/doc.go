// Package api mirrors the Megachat wire contract byte-for-byte: the JSON-RPC
// 2.0 operations spoken over the Unix socket (TUI), the REST routes and the
// WebSocket frames spoken over HTTP (web SPA), and the bus events pushed to
// both.
//
// The authoritative human-readable reference is megachatd/CONTRACTS.md; this
// package is its machine-checked twin. Rules used throughout:
//
//   - JSON tags equal the Python field names exactly (snake_case).
//   - Operation results are produced by pydantic without exclude_none, so an
//     Optional result field is a pointer WITHOUT omitempty (emits null).
//   - omitempty is used only where Python really omits the key: event payloads
//     (serialised with exclude_unset), keys that are conditionally added to a
//     dict (e.g. "reactions" on a message row), and request fields, which are
//     only ever decoded by the Go server.
//   - Python never emits null for list/dict fields that default to empty; the
//     types that carry such fields normalise nil to [] / {} in MarshalJSON.
//     For top-level list results use [EmptyIfNil].
package api

// EmptyIfNil returns s, or an empty non-nil slice when s is nil, so that it
// marshals as [] (Python never answers null where it answers a list).
func EmptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// EmptyMapIfNil is the map counterpart of [EmptyIfNil].
func EmptyMapIfNil[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}
