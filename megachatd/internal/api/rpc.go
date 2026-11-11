package api

import (
	"encoding/json"
	"path/filepath"

	"github.com/emdioh/megachat/megachatd"
)

// ServerVersion is the informational server version (clients must not parse
// it): the embedded megachatd/VERSION, which the binary's main refines at
// startup with the git commit ("0.2.1 (abc1234)").
var ServerVersion = megachatd.Version()

// Wire constants of the Unix-socket adapter (server/socket_adapter.py).
const (
	// JSONRPCVersion is the mandatory "jsonrpc" member.
	JSONRPCVersion = "2.0"
	// APIVersion is the wire protocol version announced by rpc.hello. Clients
	// refuse any other value.
	APIVersion = 1

	MaxLineBytes              = 16 * 1024 * 1024
	DefaultTimeoutSeconds     = 120.0
	MaxTimeoutSeconds         = 600.0
	MaxConnections            = 32
	MaxInflightPerConnection  = 64
	WriteTimeoutSeconds       = 30.0
	HeartbeatIntervalSeconds  = 5.0
	MaxSocketPathBytes        = 103
	EnvSocket                 = "MEGACHAT_SOCKET"
	SocketFileName            = "server.sock"
	RuntimeFileName           = "server.json"
	EventsRingSize            = 2000
	SubscriberQueueSize       = 1000
	ClientHeartbeatTimeoutSec = 15.0
)

// Built-in methods answered by the adapter itself (an operation may not shadow
// them).
const (
	MethodHello       = "rpc.hello"
	MethodPing        = "server.ping"
	MethodSubscribe   = "events.subscribe"
	MethodUnsubscribe = "events.unsubscribe"
	// Notifications written by the server on a subscribed connection.
	MethodEvent     = "event"
	MethodHeartbeat = "heartbeat"
)

// BuiltinMethods in registry order.
var BuiltinMethods = []string{MethodHello, MethodPing, MethodSubscribe, MethodUnsubscribe}

// DefaultSocketPath resolves the default socket location given the environment
// values (server/paths.py): $MEGACHAT_SOCKET, else
// $XDG_RUNTIME_DIR/megachat/server.sock when XDG_RUNTIME_DIR is absolute, else
// $HOME/.local/state/megachat/server.sock. A leading "~" in the override is
// expanded by the caller.
func DefaultSocketPath(megachatSocket, xdgRuntimeDir, home string) string {
	if megachatSocket != "" {
		return megachatSocket
	}
	if xdgRuntimeDir != "" && filepath.IsAbs(xdgRuntimeDir) {
		return filepath.Join(xdgRuntimeDir, "megachat", SocketFileName)
	}
	return filepath.Join(home, ".local", "state", "megachat", SocketFileName)
}

// RuntimePathFor returns the runtime file next to a socket
// (server.sock -> server.json).
func RuntimePathFor(socketPath string) string {
	ext := filepath.Ext(socketPath)
	return socketPath[:len(socketPath)-len(ext)] + ".json"
}

// RuntimeFile is the content of server.json (0600, written atomically after
// the socket listens, removed on shutdown).
type RuntimeFile struct {
	PID        int     `json:"pid"`
	Socket     string  `json:"socket"`
	Version    string  `json:"version"`
	APIVersion int     `json:"api_version"`
	StartedAt  float64 `json:"started_at"`
}

// RPCRequest is a request line. ID is absent for a notification (never
// answered); a present id must be a JSON string or integer (null/bool/float is
// -32600). Params is absent/null (= {}) or an object. Timeout is the
// non-standard per-call server-side limit in seconds, 0 < t <= 600.
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Timeout *float64        `json:"timeout,omitempty"`
}

// RPCResult is a success response line.
type RPCResult struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
}

// RPCError is the "error" member.
type RPCError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *ErrorData `json:"data,omitempty"`
}

// RPCErrorResponse is an error response line. ID is the literal null when the
// request id could not be determined (nil RawMessage marshals as null).
type RPCErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   RPCError        `json:"error"`
}

// HelloResult is the result of rpc.hello.
type HelloResult struct {
	APIVersion    int      `json:"api_version"`
	ServerVersion string   `json:"server_version"`
	Operations    []string `json:"operations"`
}

// PingResult is the result of server.ping (ts: Unix seconds, float).
type PingResult struct {
	Pong bool    `json:"pong"`
	Ts   float64 `json:"ts"`
}

// SubscribeParams are the params of events.subscribe; Since must be an
// integer >= 0 when present (else -32602 "since must be an integer >= 0").
type SubscribeParams struct {
	Since *int64 `json:"since,omitempty"`
}

// SubscribeResult is the result of events.subscribe.
type SubscribeResult struct {
	Subscribed bool  `json:"subscribed"`
	LastSeq    int64 `json:"last_seq"`
}

// UnsubscribeResult is the result of events.unsubscribe ({"subscribed":false}).
type UnsubscribeResult struct {
	Subscribed bool `json:"subscribed"`
}

// EventParams is the params of an "event" notification; it is exactly the
// /ws/v2 frame too. Payload is the bus payload (see events.go). Ts is Unix
// seconds (float).
type EventParams struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Ts      float64         `json:"ts"`
}

// EventNotification is {"jsonrpc":"2.0","method":"event","params":{...}}.
type EventNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  EventParams `json:"params"`
}

// HeartbeatParams is the params of a heartbeat notification.
type HeartbeatParams struct {
	Ts float64 `json:"ts"`
}

// HeartbeatNotification is written every 5 s on an idle subscribed connection.
type HeartbeatNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  HeartbeatParams `json:"params"`
}
