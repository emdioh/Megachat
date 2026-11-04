package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
)

// browserAuthorization returns the Authorization value to validate and the
// subprotocol to accept (ws.py _browser_authorization). ok is false when
// nothing usable was supplied.
func browserAuthorization(r *http.Request) (authorization, subprotocol string) {
	if a := r.Header.Get("Authorization"); a != "" {
		return a, ""
	}
	var protocols []string
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(h, ",") {
			protocols = append(protocols, strings.TrimSpace(p))
		}
	}
	for _, p := range protocols {
		if !strings.HasPrefix(p, api.WSTokenSubprotocolPrefix) {
			continue
		}
		enc := strings.TrimRight(strings.TrimPrefix(p, api.WSTokenSubprotocolPrefix), "=")
		dec, err := base64.RawURLEncoding.DecodeString(enc)
		if err != nil || !utf8Valid(dec) {
			return "", ""
		}
		accepted := ""
		for _, q := range protocols {
			if q == api.WSBearerSubprotocol {
				accepted = q
			}
		}
		return "Bearer " + string(dec), accepted
	}
	return "", ""
}

func utf8Valid(b []byte) bool { return strings.ToValidUTF8(string(b), "\xff") == string(b) }

// wsAccept authenticates and upgrades; it answers 403 itself when
// unauthorised (Starlette's close(1008) before accept surfaces as 403).
func (s *Server) wsAccept(w http.ResponseWriter, r *http.Request) *websocket.Conn {
	authz, sub := browserAuthorization(r)
	if s.authRequired && !isAuthorized(authz, s.token) {
		http.Error(w, "", http.StatusForbidden)
		return nil
	}
	opts := &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled}
	if sub != "" {
		opts.Subprotocols = []string{sub}
	}
	c, err := websocket.Accept(w, r, opts)
	if err != nil {
		return nil
	}
	c.SetReadLimit(1 << 20)
	return c
}

func sendJSON(ctx context.Context, c *websocket.Conn, v any, timeout time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

// closeSockets closes every open WebSocket (server stop).
func (s *Server) closeSockets() {
	s.mu.Lock()
	var conns []*websocket.Conn
	for c := range s.legacy {
		conns = append(conns, c.conn)
	}
	for c := range s.v2 {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusGoingAway, "")
	}
}

// ---- /ws (legacy) ----------------------------------------------------------

type wsClient struct{ conn *websocket.Conn }

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn := s.wsAccept(w, r)
	if conn == nil {
		return
	}
	cl := &wsClient{conn: conn}
	s.addLegacy(cl)
	defer s.removeLegacy(cl)
	defer conn.CloseNow()
	ctx := r.Context()
	for { // client messages are read and ignored
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

func (s *Server) addLegacy(c *wsClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.legacy[c] = struct{}{}
	if s.hubStop == nil {
		stop := make(chan struct{})
		s.hubStop = stop
		var sub *bus.Subscription
		if s.ev != nil {
			sub = s.ev.Subscribe() // subscribed before the first client is served
		}
		go s.broadcast(sub, stop)
	}
}

func (s *Server) removeLegacy(c *wsClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.legacy, c)
	if len(s.legacy) == 0 && s.hubStop != nil {
		close(s.hubStop)
		s.hubStop = nil
	}
}

func (s *Server) pollInterval(max time.Duration) time.Duration {
	if s.heartbeat < max {
		return s.heartbeat
	}
	return max
}

// broadcast fans the bus out to every /ws client: legacy frames, heartbeat
// when idle for HeartbeatInterval.
func (s *Server) broadcast(sub *bus.Subscription, stop <-chan struct{}) {
	if sub != nil {
		defer sub.Close()
	}
	var evc <-chan bus.Event
	if sub != nil {
		evc = sub.C()
	}
	tick := time.NewTicker(s.pollInterval(500 * time.Millisecond))
	defer tick.Stop()
	var lastSend time.Time
	for {
		var frame map[string]any
		select {
		case <-stop:
			return
		case ev, ok := <-evc:
			if !ok {
				evc = nil
				continue
			}
			frame, _ = toLegacy(ev)
		case <-tick.C:
		}
		if frame == nil {
			if !lastSend.IsZero() && time.Since(lastSend) < s.heartbeat {
				continue
			}
			frame = map[string]any{"type": api.FrameHeartbeat}
		}
		lastSend = time.Now()
		s.mu.Lock()
		clients := make([]*wsClient, 0, len(s.legacy))
		for c := range s.legacy {
			clients = append(clients, c)
		}
		s.mu.Unlock()
		for _, c := range clients {
			if err := sendJSON(context.Background(), c.conn, frame, 500*time.Millisecond); err != nil {
				s.mu.Lock()
				delete(s.legacy, c)
				s.mu.Unlock()
				_ = c.conn.CloseNow()
			}
		}
	}
}

// ---- /ws/v2 ----------------------------------------------------------------

type wireFrame struct {
	Seq     uint64         `json:"seq"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
	TS      float64        `json:"ts"`
}

// parseSinceString is _parse_since for the ?since= query value.
func parseSinceString(v string) (uint64, bool) {
	n, ok := parsePyInt(v)
	if !ok || n < 0 {
		return 0, false
	}
	return uint64(n), true
}

// subscribeSince parses a client text frame: isSubscribe, since (nil = live),
// ok=false when the subscribe frame is malformed.
func subscribeSince(text string) (isSub bool, since *uint64, ok bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &m) != nil || m == nil {
		return false, nil, true
	}
	var typ string
	if json.Unmarshal(m["type"], &typ) != nil || typ != api.V2Subscribe {
		return false, nil, true
	}
	raw, has := m["since"]
	if !has || api.RawIsNull(raw) {
		return true, nil, true
	}
	if s, isStr := api.RawString(raw); isStr {
		n, good := parseSinceString(s)
		if !good {
			return true, nil, false
		}
		return true, &n, true
	}
	n, good := api.RawInt(raw)
	if !good || n < 0 {
		return true, nil, false
	}
	u := uint64(n)
	return true, &u, true
}

func (s *Server) subscribe(since *uint64) *bus.Subscription {
	if s.ev == nil {
		return nil
	}
	if since != nil {
		return s.ev.Subscribe(bus.Since(*since))
	}
	return s.ev.Subscribe()
}

func (s *Server) handleWSV2(w http.ResponseWriter, r *http.Request) {
	authz, _ := browserAuthorization(r)
	if s.authRequired && !isAuthorized(authz, s.token) {
		http.Error(w, "", http.StatusForbidden)
		return
	}
	var since *uint64
	if q, ok := r.URL.Query()["since"]; ok {
		n, good := parseSinceString(q[len(q)-1])
		if !good {
			http.Error(w, "", http.StatusForbidden)
			return
		}
		since = &n
	}
	conn := s.wsAccept(w, r)
	if conn == nil {
		return
	}
	s.mu.Lock()
	s.v2[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.v2, conn)
		s.mu.Unlock()
		_ = conn.CloseNow()
	}()
	s.streamV2(r.Context(), conn, since)
}

func (s *Server) streamV2(ctx context.Context, conn *websocket.Conn, since *uint64) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan string, 16) // extras are dropped
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			select {
			case incoming <- string(data):
			default:
			}
		}
	}()

	sub := s.subscribe(since)
	defer func() {
		if sub != nil {
			sub.Close()
		}
	}()
	const wt = 10 * time.Second

	resubscribe := func(text string) bool {
		isSub, newSince, ok := subscribeSince(text)
		if !ok {
			return sendJSON(ctx, conn, api.V2ErrorFrame{Type: api.V2Error, Payload: api.V2ErrorPayload{Reason: api.V2ReasonInvalid}}, wt) == nil
		}
		if isSub && s.ev != nil {
			if sub != nil {
				sub.Close()
			}
			sub = s.subscribe(newSince)
		}
		return true
	}

	if since == nil { // grace period for a subscribe frame
		t := time.NewTimer(s.grace)
		select {
		case m := <-incoming:
			if !resubscribe(m) {
				t.Stop()
				return
			}
		case <-closed:
			t.Stop()
			return
		case <-t.C:
		}
		t.Stop()
	}

	tick := time.NewTicker(s.pollInterval(250 * time.Millisecond))
	defer tick.Stop()
	var lastSend time.Time
	for {
		var frame any
		var evc <-chan bus.Event
		if sub != nil {
			evc = sub.C()
		}
		select {
		case <-closed:
			return
		case <-ctx.Done():
			return
		case m := <-incoming:
			if !resubscribe(m) {
				return
			}
			continue
		case ev, ok := <-evc:
			if !ok {
				// Subscription ended (bus closed): idle with heartbeats.
				sub = nil
				continue
			}
			payload := ev.Payload
			if payload == nil {
				payload = map[string]any{}
			}
			frame = wireFrame{Seq: ev.Seq, Type: string(ev.Type), Payload: payload,
				TS: float64(ev.TS.UnixNano()) / 1e9}
		case <-tick.C:
			if !lastSend.IsZero() && time.Since(lastSend) < s.heartbeat {
				continue
			}
			frame = api.HeartbeatFrame{Type: api.FrameHeartbeat}
		}
		lastSend = time.Now()
		b, err := json.Marshal(frame)
		if err != nil {
			s.log.Warn("ws/v2: dropped non-JSON event")
			continue
		}
		wctx, wcancel := context.WithTimeout(ctx, wt)
		err = conn.Write(wctx, websocket.MessageText, b)
		wcancel()
		if err != nil {
			return
		}
	}
}

