package api

import "encoding/json"

// Neutral operation error codes (server/api.py ERROR_CODES). Every adapter
// maps them to its own form (HTTP status, JSON-RPC code).
const (
	CodeInvalidRequest = "invalid_request"
	CodeNotFound       = "not_found"
	CodeUnauthorized   = "unauthorized"
	CodeForbidden      = "forbidden"
	CodeTooLarge       = "too_large"
	CodeConflict       = "conflict"
	CodeUnprocessable  = "unprocessable"
	CodeNotSupported   = "not_supported"
	CodeBackendDown    = "backend_down"
	CodeBackendError   = "backend_error"
	CodeInternal       = "internal"
)

// ErrorCodes lists every neutral code in registry order.
var ErrorCodes = []string{
	CodeInvalidRequest, CodeNotFound, CodeUnauthorized, CodeForbidden,
	CodeTooLarge, CodeConflict, CodeUnprocessable, CodeNotSupported,
	CodeBackendDown, CodeBackendError, CodeInternal,
}

// Codes that only exist inside error.data.code on the socket / client side.
const (
	CodeMethodNotFound = "method_not_found" // data.code of JSON-RPC -32601
	CodeTimeout        = "timeout"          // data.code of JSON-RPC -32020
)

// JSON-RPC error numbers used on the Unix socket (server/socket_adapter.py).
const (
	RPCParseError         = -32700
	RPCInvalidRequest     = -32600
	RPCMethodNotFound     = -32601
	RPCInvalidParams      = -32602
	RPCInternalError      = -32603
	RPCTimeout            = -32020
	RPCTooManyRequests    = -32030
	RPCTooManyConnections = -32031
	RPCNotFound           = -32001
	RPCUnauthorized       = -32002
	RPCForbidden          = -32003
	RPCTooLarge           = -32004
	RPCConflict           = -32005
	RPCUnprocessable      = -32006
	RPCNotSupported       = -32007
	RPCBackendDown        = -32008
	RPCBackendError       = -32009
)

// RPCCodeFor maps an ApiError code to its JSON-RPC error number.
var RPCCodeFor = map[string]int{
	CodeInvalidRequest: RPCInvalidParams,
	CodeNotFound:       RPCNotFound,
	CodeUnauthorized:   RPCUnauthorized,
	CodeForbidden:      RPCForbidden,
	CodeTooLarge:       RPCTooLarge,
	CodeConflict:       RPCConflict,
	CodeUnprocessable:  RPCUnprocessable,
	CodeNotSupported:   RPCNotSupported,
	CodeBackendDown:    RPCBackendDown,
	CodeBackendError:   RPCBackendError,
	CodeInternal:       RPCInternalError,
}

// HTTPStatusFor maps an ApiError code to the HTTP status of the web adapter
// (web/ops.py HTTP_STATUS). Unknown codes answer 500.
var HTTPStatusFor = map[string]int{
	CodeInvalidRequest: 400,
	CodeUnauthorized:   401,
	CodeForbidden:      403,
	CodeNotFound:       404,
	CodeConflict:       409,
	CodeTooLarge:       413,
	CodeUnprocessable:  422,
	CodeNotSupported:   501,
	CodeInternal:       500,
	CodeBackendError:   502,
	CodeBackendDown:    503,
}

// HTTPError is the body of every error answered by the web adapter:
// {"detail": "<ApiError.message>"}. (FastAPI's own 422 validation errors use a
// list for detail, see ValidationDetail.)
type HTTPError struct {
	Detail string `json:"detail"`
}

// ValidationDetail is one element of the list FastAPI puts in "detail" for a
// 422 (bad query/path parameter or non-object JSON body).
type ValidationDetail struct {
	Type  string          `json:"type"`
	Loc   []any           `json:"loc"`
	Msg   string          `json:"msg"`
	Input json.RawMessage `json:"input,omitempty"`
	Ctx   map[string]any  `json:"ctx,omitempty"`
}

// HTTPValidationError is the 422 body produced by FastAPI.
type HTTPValidationError struct {
	Detail []ValidationDetail `json:"detail"`
}

// ErrorData is the "data" member of a JSON-RPC error produced from an
// ApiError. Details is present (possibly the literal null) for errors coming
// from operations and absent for protocol-level errors; for invalid_request it
// is a list of PydanticError.
type ErrorData struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

// PydanticError is the trimmed form of a pydantic error placed in
// ErrorData.Details for invalid_request (only type, loc and msg survive).
type PydanticError struct {
	Type string `json:"type"`
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
}

// OpDetailsError is ErrorData.Details of messages.send failures in the
// optimistic flavour (and of the refused re-arm conflict).
type OpDetailsError struct {
	StatusApplied bool    `json:"status_applied"`
	Error         *string `json:"error"`
}

// MessagesEditErrorDetails is ErrorData.Details of a messages.edit
// backend_error ("Message edit failed").
type MessagesEditErrorDetails struct {
	Error string `json:"error"`
}
