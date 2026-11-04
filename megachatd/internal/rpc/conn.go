package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

type rpcErr struct {
	code int
	msg  string
	data *api.ErrorData
}

func (e *rpcErr) Error() string { return e.msg }

type conn struct {
	s  *Server
	nc net.Conn

	wmu       sync.Mutex
	closeOnce sync.Once
	done      chan struct{}

	mu       sync.Mutex
	sub      *bus.Subscription
	inflight int
	wg       sync.WaitGroup
}

func (s *Server) onConnect(nc net.Conn) {
	c := &conn{s: s, nc: nc, done: make(chan struct{})}
	if uid, ok := peerUID(nc); ok && uid != os.Geteuid() {
		s.cfg.log.Warn("Socket connection refused", "uid", uid)
		nc.Close()
		return
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		nc.Close()
		return
	}
	if len(s.conns) >= s.cfg.maxConns {
		s.mu.Unlock()
		_ = nc.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = nc.Write(errorLine(nil, api.RPCTooManyConnections, "Too many connections", nil))
		nc.Close()
		return
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.close()
	}()
	c.run()
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.unsubscribe()
		c.nc.Close()
	})
}

func (c *conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *conn) send(data []byte) {
	if c.closed() {
		return
	}
	c.wmu.Lock()
	_ = c.nc.SetWriteDeadline(time.Now().Add(c.s.cfg.writeTimeout))
	_, err := c.nc.Write(data)
	c.wmu.Unlock()
	if err != nil {
		c.s.cfg.log.Info("Socket client dropped (write failed or timed out)")
		c.close()
	}
}

func (c *conn) unsubscribe() bool {
	c.mu.Lock()
	sub := c.sub
	c.sub = nil
	c.mu.Unlock()
	if sub == nil {
		return false
	}
	sub.Close()
	return true
}

var errOverrun = errors.New("line too long")

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		switch {
		case err == nil:
			if buf == nil && len(chunk)-1 <= max {
				return append([]byte(nil), chunk[:len(chunk)-1]...), nil
			}
			buf = append(buf, chunk[:len(chunk)-1]...)
			if len(buf) > max {
				return nil, errOverrun
			}
			return buf, nil
		case errors.Is(err, bufio.ErrBufferFull):
			buf = append(buf, chunk...)
			if len(buf) > max {
				return nil, errOverrun
			}
		default:
			return nil, err // EOF: a partial last line is dropped
		}
	}
}

const asciiWS = " \t\n\r\v\f"

func (c *conn) run() {
	defer c.wg.Wait()
	r := bufio.NewReaderSize(c.nc, min(64*1024, c.s.cfg.maxLine+1))
	for !c.closed() {
		raw, err := readLine(r, c.s.cfg.maxLine)
		if errors.Is(err, errOverrun) {
			c.send(errorLine(nil, api.RPCTooLarge,
				fmt.Sprintf("Request line exceeds %d bytes", c.s.cfg.maxLine),
				&api.ErrorData{Code: api.CodeTooLarge, Message: "Request line too large"}))
			c.drainBeforeClose(r)
			return
		}
		if err != nil {
			return // EOF/OSError: wait for in-flight requests (deferred), then close
		}
		raw = bytes.Trim(raw, asciiWS)
		if len(raw) == 0 {
			continue
		}
		if !utf8.Valid(raw) || !json.Valid(raw) {
			c.send(errorLine(nil, api.RPCParseError, "Parse error", nil))
			continue
		}
		var obj map[string]json.RawMessage
		batch := raw[0] == '['
		if raw[0] == '{' {
			_ = json.Unmarshal(raw, &obj)
		}
		c.mu.Lock()
		full := c.inflight >= c.s.cfg.maxInflight
		if !full {
			c.inflight++
		}
		c.mu.Unlock()
		if full {
			var id idJSON
			if obj != nil {
				if rid, ok := obj["id"]; ok {
					if p, ok := parseID(rid); ok {
						id = p
					}
				}
			}
			c.send(errorLine(id, api.RPCTooManyRequests, "Too many requests in flight", nil))
			continue
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer func() { c.mu.Lock(); c.inflight--; c.mu.Unlock() }()
			c.handle(obj, batch)
		}()
	}
}

// drainBeforeClose half-closes, then discards pending input (bounded) so the
// error line is not lost to a connection reset.
func (c *conn) drainBeforeClose(r *bufio.Reader) {
	if uc, ok := c.nc.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	_ = c.nc.SetReadDeadline(time.Now().Add(drainOnClose))
	_, _ = io.CopyN(io.Discard, r, int64(MaxLineBytes))
}

func (c *conn) handle(obj map[string]json.RawMessage, batch bool) {
	var reqID idJSON
	hasID := false
	res, after, e := c.process(obj, batch, &reqID, &hasID)
	if e != nil {
		if hasID || e.code == api.RPCInvalidRequest || e.code == api.RPCParseError {
			c.send(errorLine(reqID, e.code, e.msg, e.data))
		}
		return
	}
	if hasID {
		c.send(resultLine(reqID, res))
	}
	if after != nil {
		after()
	}
}

func (c *conn) process(obj map[string]json.RawMessage, batch bool, reqID *idJSON, hasID *bool) (res []byte, after func(), e *rpcErr) {
	defer func() {
		if r := recover(); r != nil {
			c.s.cfg.log.Error("Socket request failed", "panic", r)
			res, after, e = nil, nil, internalErr()
		}
	}()
	if batch {
		return nil, nil, &rpcErr{code: api.RPCInvalidRequest, msg: "Batch requests are not supported"}
	}
	if obj == nil {
		return nil, nil, &rpcErr{code: api.RPCInvalidRequest, msg: "Invalid request"}
	}
	if rid, ok := obj["id"]; ok {
		*hasID = true
		id, ok := parseID(rid)
		if !ok || isBoolID(id) {
			*reqID = nil
			return nil, nil, &rpcErr{code: api.RPCInvalidRequest, msg: "id must be a string or an integer"}
		}
		*reqID = id
	}
	if v, ok := jsonString(obj["jsonrpc"]); !ok || v != api.JSONRPCVersion {
		return nil, nil, &rpcErr{code: api.RPCInvalidRequest, msg: "jsonrpc must be '2.0'"}
	}
	method, ok := jsonString(obj["method"])
	if !ok {
		return nil, nil, &rpcErr{code: api.RPCInvalidRequest, msg: "method must be a string"}
	}
	params := json.RawMessage("{}")
	if p := bytes.TrimSpace(obj["params"]); len(p) > 0 && string(p) != "null" {
		if p[0] != '{' {
			return nil, nil, &rpcErr{code: api.RPCInvalidParams, msg: "params must be an object"}
		}
		params = p
	}
	timeout, terr := timeoutOf(obj["timeout"], c.s.cfg.defTimeout)
	if terr != nil {
		return nil, nil, terr
	}
	return c.execute(method, params, timeout)
}

func jsonString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

func timeoutOf(raw json.RawMessage, def time.Duration) (time.Duration, *rpcErr) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return def, nil
	}
	bad := &rpcErr{code: api.RPCInvalidRequest,
		msg: fmt.Sprintf("timeout must be a number in (0, %s]", gfmt(MaxTimeout.Seconds()))}
	if raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return 0, bad
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || !(f > 0 && f <= MaxTimeout.Seconds()) {
		return 0, bad
	}
	return time.Duration(f * float64(time.Second)), nil
}

// gfmt formats like Python's "{:g}".
func gfmt(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

func internalErr() *rpcErr {
	return &rpcErr{code: api.RPCInternalError, msg: "Internal error",
		data: &api.ErrorData{Code: api.CodeInternal, Message: "Internal error"}}
}

type opResult struct {
	body []byte
	err  *rpcErr
}

func (c *conn) execute(method string, params json.RawMessage, timeout time.Duration) ([]byte, func(), *rpcErr) {
	s := c.s
	switch method {
	case api.MethodHello:
		b, _ := marshal(api.HelloResult{APIVersion: api.APIVersion, ServerVersion: api.ServerVersion,
			Operations: sortedNames(s.d)})
		return b, nil, nil
	case api.MethodPing:
		b, _ := marshal(struct {
			Pong bool      `json:"pong"`
			Ts   jsonFloat `json:"ts"`
		}{true, jsonFloat(float64(time.Now().UnixNano()) / 1e9)})
		return b, nil, nil
	case api.MethodUnsubscribe:
		c.unsubscribe()
		return []byte(`{"subscribed":false}`), nil, nil
	case api.MethodSubscribe:
		return c.subscribe(params)
	}
	if !s.d.Has(method) {
		m := fmt.Sprintf("Unknown method %s", pyRepr(method))
		return nil, nil, &rpcErr{code: api.RPCMethodNotFound, msg: m,
			data: &api.ErrorData{Code: api.CodeMethodNotFound, Message: m}}
	}
	// The operation outlives this request and its connection; a timeout only
	// stops waiting.
	ch := make(chan opResult, 1)
	s.opsWG.Add(1)
	go func() {
		defer s.opsWG.Done()
		ch <- s.runOp(method, params)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, nil, r.err
		}
		return r.body, nil, nil
	case <-timer.C:
		go func() {
			if r := <-ch; r.err != nil && r.err.code == api.RPCInternalError {
				s.cfg.log.Error("Abandoned socket call failed", "method", method)
			}
		}()
		m := fmt.Sprintf("%s timed out after %ss", method, gfmt(timeout.Seconds()))
		return nil, nil, &rpcErr{code: api.RPCTimeout, msg: m,
			data: &api.ErrorData{Code: api.CodeTimeout, Message: method + " timed out"}}
	case <-s.stopCh:
		return nil, nil, internalErr()
	}
}

func (s *Server) runOp(method string, params json.RawMessage) (out opResult) {
	defer func() {
		if r := recover(); r != nil {
			s.cfg.log.Error("Socket operation panicked", "method", method, "panic", r)
			out = opResult{err: internalErr()}
		}
	}()
	if s.cfg.preCall != nil {
		p, cleanup, err := s.cfg.preCall(s.opCtx, method, params)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return opResult{err: mapError(err)}
		}
		params = p
	}
	v, err := s.d.Call(s.opCtx, method, params)
	if err != nil {
		return opResult{err: mapError(err)}
	}
	b, merr := marshal(v)
	if merr != nil {
		s.cfg.log.Error("Socket result not encodable", "method", method, "err", merr)
		return opResult{err: internalErr()}
	}
	return opResult{body: b}
}

// mapError turns an operation error into its JSON-RPC form.
func mapError(err error) *rpcErr {
	var oe *ops.Error
	if !errors.As(err, &oe) {
		return internalErr()
	}
	code, ok := api.RPCCodeFor[oe.Code]
	if !ok {
		code = api.RPCInternalError
	}
	return &rpcErr{code: code, msg: oe.Message, data: &api.ErrorData{
		Code: oe.Code, Message: oe.Message, Details: detailsJSON(oe.Code, oe.Details)}}
}

func detailsJSON(code string, details any) json.RawMessage {
	b, err := marshal(details)
	if err != nil {
		b, _ = marshal(fmt.Sprint(details))
	}
	if code != api.CodeInvalidRequest || len(b) == 0 || b[0] != '[' {
		return b
	}
	var list []json.RawMessage
	if json.Unmarshal(b, &list) != nil {
		return b
	}
	out := []byte{'['}
	first := true
	for _, el := range list {
		if len(el) == 0 || el[0] != '{' {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(el, &m) != nil {
			continue
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		out = append(out, '{')
		for i, k := range []string{"type", "loc", "msg"} {
			v := m[k]
			if len(v) == 0 {
				v = json.RawMessage("null")
			}
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, '"')
			out = append(out, k...)
			out = append(out, '"', ':')
			out = append(out, v...)
		}
		out = append(out, '}')
	}
	return append(out, ']')
}

func (c *conn) subscribe(params json.RawMessage) ([]byte, func(), *rpcErr) {
	s := c.s
	if s.ev == nil {
		return nil, nil, mapError(ops.Errorf(api.CodeNotSupported, "This server has no event bus"))
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(params, &m)
	var opts []bus.SubOption
	if raw := bytes.TrimSpace(m["since"]); len(raw) > 0 && string(raw) != "null" {
		bad := &rpcErr{code: api.RPCInvalidParams, msg: "since must be an integer >= 0"}
		if !isIntegerLiteral(raw) {
			return nil, nil, bad
		}
		n, ok := new(big.Int).SetString(string(raw), 10)
		if !ok || n.Sign() < 0 {
			return nil, nil, bad
		}
		since := uint64(math.MaxUint64)
		if n.IsUint64() {
			since = n.Uint64()
		}
		opts = append(opts, bus.Since(since))
	}
	c.unsubscribe()
	sub := s.ev.Subscribe(opts...)
	c.mu.Lock()
	c.sub = sub
	c.mu.Unlock()
	b, _ := marshal(api.SubscribeResult{Subscribed: true, LastSeq: int64(s.ev.LastSeq())})
	// Notifications start only after the response is written.
	return b, func() { go c.pump(sub) }, nil
}

type eventFrame struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  eventParams `json:"params"`
}

type eventParams struct {
	Seq     uint64         `json:"seq"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
	Ts      jsonFloat      `json:"ts"`
}

type heartbeatFrame struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Ts jsonFloat `json:"ts"`
	} `json:"params"`
}

func (c *conn) pump(sub *bus.Subscription) {
	hb := c.s.cfg.heartbeat
	timer := time.NewTimer(hb)
	defer timer.Stop()
	for {
		var data []byte
		select {
		case <-c.done:
			return
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			p := ev.Payload
			if p == nil {
				p = map[string]any{}
			}
			b, err := marshal(eventFrame{api.JSONRPCVersion, api.MethodEvent, eventParams{
				ev.Seq, string(ev.Type), p, jsonFloat(float64(ev.TS.UnixNano()) / 1e9)}})
			if err != nil {
				c.s.cfg.log.Warn("Socket: dropped non-JSON event", "seq", ev.Seq)
				continue
			}
			data = b
		case <-timer.C:
			f := heartbeatFrame{JSONRPC: api.JSONRPCVersion, Method: api.MethodHeartbeat}
			f.Params.Ts = jsonFloat(float64(time.Now().UnixNano()) / 1e9)
			data, _ = marshal(f)
		}
		c.send(append(data, '\n'))
		timer.Reset(hb)
	}
}
