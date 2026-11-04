// Package ops is the Go port of server/api.py: the operation registry that
// every transport adapter (internal/rpc, internal/web) translates.
//
// This file is the shared contract between the core (which registers
// handlers) and the adapters (which call them). Adapters depend only on
// Dispatcher, Error and EventSource; they never import internal/core.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// Error is the neutral operation error (Python ApiError). Code is one of
// api.ErrorCodes; Message is the exact user-facing text (HTTP "detail").
type Error struct {
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Errorf builds an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError converts any error into an *Error (non-ops errors become "internal").
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var oe *Error
	if errors.As(err, &oe) {
		return oe
	}
	return &Error{Code: api.CodeInternal, Message: err.Error()}
}

// Handler runs one operation. params is the raw JSON request object ({} when
// absent). It returns a value that marshals to the exact wire response.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Op is one registered operation.
type Op struct {
	Name    string
	Doc     string
	Handler Handler
}

// Dispatcher is what transport adapters need from the server.
type Dispatcher interface {
	// Call runs operation name. Unknown name -> *Error{Code: not_found}.
	// Invalid params -> *Error{Code: invalid_request}.
	Call(ctx context.Context, name string, params json.RawMessage) (any, error)
	// Has reports whether name is registered.
	Has(name string) bool
	// Names lists every registered operation, sorted.
	Names() []string
}

// EventSource is what adapters need to push events to clients.
type EventSource interface {
	Subscribe(opts ...bus.SubOption) *bus.Subscription
	LastSeq() uint64
}

// Registry implements Dispatcher. It is safe for concurrent use.
type Registry struct {
	mu  sync.RWMutex
	ops map[string]Op

	// ShutdownHook is called once by server.shutdown after acknowledgement.
	// nil -> server.shutdown answers not_supported.
	ShutdownHook func()

	countersMu     sync.Mutex
	clientCounters map[string]func() int
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{ops: map[string]Op{}, clientCounters: map[string]func() int{}}
}

// Register adds op; it panics on a duplicate name (as Python raises).
func (r *Registry) Register(op Op) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.ops[op.Name]; dup {
		panic(fmt.Sprintf("operation %q already registered", op.Name))
	}
	r.ops[op.Name] = op
}

// Call implements Dispatcher.
func (r *Registry) Call(ctx context.Context, name string, params json.RawMessage) (any, error) {
	r.mu.RLock()
	op, ok := r.ops[name]
	r.mu.RUnlock()
	if !ok {
		return nil, Errorf(api.CodeNotFound, "Unknown operation %q", name)
	}
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	params, verr := validateRequest(name, params)
	if verr != nil {
		return nil, verr
	}
	return op.Handler(ctx, params)
}

// Has implements Dispatcher.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.ops[name]
	return ok
}

// Names implements Dispatcher.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.ops))
	for n := range r.ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Ops returns all ops sorted by name.
func (r *Registry) Ops() []Op {
	names := r.Names()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Op, len(names))
	for i, n := range names {
		out[i] = r.ops[n]
	}
	return out
}

// SetClientCounter registers (or with nil removes) a live-connection counter
// for an adapter ("socket", "web"); server.clients sums them.
func (r *Registry) SetClientCounter(adapter string, f func() int) {
	r.countersMu.Lock()
	defer r.countersMu.Unlock()
	if f == nil {
		delete(r.clientCounters, adapter)
		return
	}
	r.clientCounters[adapter] = f
}

// ClientCounts returns the current count per adapter.
func (r *Registry) ClientCounts() map[string]int {
	r.countersMu.Lock()
	defer r.countersMu.Unlock()
	out := make(map[string]int, len(r.clientCounters))
	for k, f := range r.clientCounters {
		out[k] = f()
	}
	return out
}

// Decode unmarshals params into T, mapping failures to invalid_request.
// Unknown fields are ignored (pydantic default).
func Decode[T any](params json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &Error{Code: api.CodeInvalidRequest, Message: "Invalid request", Details: decodeDetails(err)}
	}
	return v, nil
}

// Typed wraps a typed handler as a Handler.
func Typed[Req any, Resp any](f func(ctx context.Context, req Req) (Resp, error)) Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := Decode[Req](params)
		if err != nil {
			return nil, err
		}
		return f(ctx, req)
	}
}

// decodeDetails maps a Go decoding failure to pydantic-shaped details (the
// wire never carries Go error texts).
func decodeDetails(err error) []api.ValidationDetail {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		loc := []any{}
		if te.Field != "" {
			loc = append(loc, te.Field)
		}
		typ, msg := "value_error", "Input is not valid"
		switch te.Type.Kind().String() {
		case "string":
			typ, msg = "string_type", "Input should be a valid string"
		case "bool":
			typ, msg = "bool_type", "Input should be a valid boolean"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			typ, msg = "int_type", "Input should be a valid integer"
		case "float32", "float64":
			typ, msg = "float_type", "Input should be a valid number"
		case "slice", "array":
			typ, msg = "list_type", "Input should be a valid list"
		case "map", "struct":
			typ, msg = "dict_type", "Input should be a valid dictionary"
		}
		return []api.ValidationDetail{{Type: typ, Loc: loc, Msg: msg}}
	}
	return []api.ValidationDetail{{Type: "json_invalid", Loc: []any{}, Msg: "Invalid JSON"}}
}
