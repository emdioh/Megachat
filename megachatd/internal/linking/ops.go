package linking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// mapError is _link_errors: keep the user-facing text, pick the code.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var oe *ops.Error
	if errors.As(err, &oe) {
		return err
	}
	var nl *NoLinkInProgress
	if errors.As(err, &nl) {
		return ops.Errorf(api.CodeConflict, "%s", nl.Msg)
	}
	var lu *LinkUnavailable
	if errors.As(err, &lu) {
		return ops.Errorf(api.CodeBackendDown, "%s", lu.Msg)
	}
	return ops.Errorf(api.CodeBackendError, "%s", err.Error())
}

func invalid() error { return ops.Errorf(api.CodeInvalidRequest, "Invalid request") }

// decode checks required keys (non-null) then decodes strictly typed fields.
func decode[T any](params json.RawMessage, required ...string) (T, error) {
	var zero T
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		return zero, invalid()
	}
	for _, k := range required {
		if v, ok := m[k]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return zero, invalid()
		}
	}
	return ops.Decode[T](params)
}

func handler[T any](required []string, f func(ctx context.Context, r T) (any, error)) ops.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		r, err := decode[T](params, required...)
		if err != nil {
			return nil, err
		}
		return f(ctx, r)
	}
}

func emptyHandler(f func(ctx context.Context) (any, error)) ops.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) { return f(ctx) }
}

func validProtocol(p string) error {
	if !api.IsProtocol(p) {
		return invalid()
	}
	return nil
}

// RegisterOps registers link.status, link.required, link.start, link.poll,
// link.finalize, link.submit_2fa, link.cancel and link.reconnect over a new
// Service, which it returns (the core cancels it on shutdown).
func RegisterOps(reg *ops.Registry, deps Deps) *Service {
	svc := New(deps)
	reg.Register(ops.Op{Name: api.OpLinkStatus, Doc: "Which protocols can be linked, the linked Signal number (no I/O).",
		Handler: emptyHandler(func(ctx context.Context) (any, error) { return svc.Status(), nil })})
	reg.Register(ops.Op{Name: api.OpLinkRequired, Doc: "Protocols whose session needs a new pairing QR (no I/O).",
		Handler: emptyHandler(func(ctx context.Context) (any, error) { return svc.Required(), nil })})
	reg.Register(ops.Op{Name: api.OpLinkStart, Doc: "Begin linking a protocol and return the QR payload to show.",
		Handler: handler([]string{"protocol"}, func(ctx context.Context, r api.LinkStartRequest) (any, error) {
			if err := validProtocol(r.Protocol); err != nil {
				return nil, err
			}
			q, err := svc.Start(ctx, r.Protocol, r.Phone, r.DeviceName)
			if err != nil {
				return nil, mapError(err)
			}
			return api.LinkStartOut{QR: q}, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinkPoll, Doc: "Progress of a started link (call every ~2 s).",
		Handler: handler([]string{"protocol"}, func(ctx context.Context, r api.LinkProtocolRequest) (any, error) {
			if err := validProtocol(r.Protocol); err != nil {
				return nil, err
			}
			out, err := svc.Poll(ctx, r.Protocol)
			if err != nil {
				return nil, mapError(err)
			}
			return out, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinkFinalize, Doc: "Signal: save and activate the newly linked account.",
		Handler: handler(nil, func(ctx context.Context, r api.LinkFinalizeRequest) (any, error) {
			if r.Protocol != api.ProtocolSignal {
				return nil, invalid()
			}
			acc, err := svc.FinalizeSignal(ctx, r.Phone)
			if err != nil {
				return nil, mapError(err)
			}
			return api.LinkFinalizeOut{Account: acc}, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinkSubmit2fa, Doc: "Telegram: finish the login with the 2FA password.",
		Handler: handler([]string{"password"}, func(ctx context.Context, r api.Link2faRequest) (any, error) {
			ok, err := svc.Submit2FA(ctx, r.Password)
			if err != nil {
				return nil, mapError(err)
			}
			return api.Link2faOut{OK: ok}, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinkCancel, Doc: "Abandon a link in progress.",
		Handler: handler([]string{"protocol"}, func(ctx context.Context, r api.LinkProtocolRequest) (any, error) {
			if err := validProtocol(r.Protocol); err != nil {
				return nil, err
			}
			svc.Cancel(r.Protocol)
			return api.OkOut{OK: true}, nil
		})})
	reg.Register(ops.Op{Name: api.OpLinkReconnect, Doc: "Reconnect the backends whose link flow was started (non-blocking).",
		Handler: handler([]string{"protocols"}, func(ctx context.Context, r api.LinkReconnectRequest) (any, error) {
			if deps.Reconnect == nil {
				return nil, ops.Errorf(api.CodeNotSupported, "This server cannot reconnect backends")
			}
			deps.Reconnect(r.Protocols)
			return api.OkOut{OK: true}, nil
		})})
	return svc
}
