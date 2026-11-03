// Package bus is a port of server/bus.py: an in-process event bus with a
// bounded replay ring buffer and per-subscriber bounded queues.
//
// Slow-subscriber policy (identical to Python): producers NEVER block. When a
// subscriber's queue is full, its queued events are dropped and replaced by a
// single synthetic GAP event (reason "overflow"); the subscriber must resync.
// Events published afterwards are queued normally behind the GAP.
//
// Payloads are not copied: producers must not mutate a payload after
// publishing it and consumers must treat payloads as immutable.
package bus

import (
	"log/slog"
	"sync"
	"time"
)

// EventType is the name of an event on the bus.
type EventType string

// Event names, as published by the Python server (core.py, api.py,
// gmail_unread.py, domain.py) and declared in server/events_api.py.
const (
	Gap EventType = "gap" // synthetic: subscriber lost events and must resync

	BackendConnecting EventType = "backend_connecting"
	BackendStatus     EventType = "backend_status"
	BackendReady      EventType = "backend_ready"
	BackendDone       EventType = "backend_done"
	Message           EventType = "message"
	MessageEdit       EventType = "message_edit"
	ReactionUpdate    EventType = "reaction_update"
	Receipt           EventType = "receipt"
	Typing            EventType = "typing"
	ContactUpdate     EventType = "contact_update"
	SentMirror        EventType = "sent_mirror"
	LidWarmupDone     EventType = "lid_warmup_done"
	WebPush           EventType = "web_push"
	GmailUnread       EventType = "gmail_unread"
)

// Gap reasons (payload["reason"]).
const (
	GapReasonBuffer   = "buffer"   // "since" fell out of the ring buffer
	GapReasonOverflow = "overflow" // the subscriber's queue overflowed
)

// Defaults match EventBus.__init__.
const (
	DefaultBufferSize          = 2000
	DefaultSubscriberQueueSize = 1000
)

// Event is the immutable envelope. Seq is monotonic per bus and starts at 1.
type Event struct {
	Seq     uint64
	Type    EventType
	Payload map[string]any
	TS      time.Time
}

// Filter reports whether a subscriber wants the event. GAP events always
// bypass filters.
type Filter func(Event) bool

// Option configures a Bus.
type Option func(*Bus)

// WithBufferSize sets the replay ring-buffer size (default 2000, min 1).
func WithBufferSize(n int) Option { return func(b *Bus) { b.bufSize = max(1, n) } }

// WithSubscriberQueueSize sets the default per-subscriber queue size
// (default 1000). As in Python it is raised to the ring-buffer size so a full
// replay always fits.
func WithSubscriberQueueSize(n int) Option { return func(b *Bus) { b.queueSize = n } }

// WithLogger sets the logger used for overflow warnings.
func WithLogger(l *slog.Logger) Option { return func(b *Bus) { b.log = l } }

// Bus is a goroutine-safe multi-subscriber event bus with a replay buffer.
type Bus struct {
	mu        sync.Mutex
	bufSize   int
	queueSize int
	ring      []Event // oldest first, len <= bufSize
	subs      map[*Subscription]struct{}
	seq       uint64
	closed    bool
	log       *slog.Logger
	now       func() time.Time
}

// New creates a Bus.
func New(opts ...Option) *Bus {
	b := &Bus{
		bufSize:   DefaultBufferSize,
		queueSize: DefaultSubscriberQueueSize,
		subs:      make(map[*Subscription]struct{}),
		log:       slog.Default(),
		now:       time.Now,
	}
	for _, o := range opts {
		o(b)
	}
	b.queueSize = max(b.queueSize, b.bufSize)
	return b
}

// SubOption configures a subscription.
type SubOption func(*subConfig)

type subConfig struct {
	since    *uint64
	types    map[EventType]struct{}
	filter   Filter
	queue    int
	hasQueue bool
}

// Since replays buffered events with Seq > since before live events. If
// since predates the ring buffer a GAP{reason:"buffer"} is delivered first.
func Since(seq uint64) SubOption { return func(c *subConfig) { c.since = &seq } }

// Types restricts delivery to the given event types (GAP is always delivered).
func Types(types ...EventType) SubOption {
	return func(c *subConfig) {
		if c.types == nil {
			c.types = make(map[EventType]struct{})
		}
		for _, t := range types {
			c.types[t] = struct{}{}
		}
	}
}

// WithFilter restricts delivery to events for which f returns true. The
// filter runs under the bus lock: keep it fast and non-blocking.
func WithFilter(f Filter) SubOption { return func(c *subConfig) { c.filter = f } }

// QueueSize overrides the subscriber's queue size (min 1). When combined with
// Since it is raised to the ring-buffer size.
func QueueSize(n int) SubOption {
	return func(c *subConfig) { c.queue, c.hasQueue = max(1, n), true }
}

// Publish delivers an event to all matching subscribers. It never blocks on
// consumers. A nil payload becomes an empty map.
func (b *Bus) Publish(t EventType, payload map[string]any) Event {
	if payload == nil {
		payload = map[string]any{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev := Event{Seq: b.seq, Type: t, Payload: payload, TS: b.now()}
	if len(b.ring) >= b.bufSize {
		// Shift in place; keeps the backing array bounded.
		copy(b.ring, b.ring[1:])
		b.ring = b.ring[:len(b.ring)-1]
	}
	b.ring = append(b.ring, ev)
	// Fan out under the lock so ordering matches seq and a concurrent
	// Subscribe(Since) can neither miss nor duplicate the event.
	for s := range b.subs {
		if s.match(ev) {
			s.push(ev)
		}
	}
	return ev
}

// Subscribe registers a subscriber. If the bus is closed the returned
// subscription is already closed (its channel is closed).
func (b *Bus) Subscribe(opts ...SubOption) *Subscription {
	var cfg subConfig
	for _, o := range opts {
		o(&cfg)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	size := b.queueSize
	if cfg.hasQueue {
		size = cfg.queue
		if cfg.since != nil {
			size = max(size, b.bufSize)
		}
	}
	s := newSubscription(b, size, cfg)
	if b.closed {
		s.closeLocked()
		return s
	}
	if cfg.since != nil {
		since := min(*cfg.since, b.seq)
		if len(b.ring) > 0 {
			oldest := b.ring[0].Seq
			if since+1 < oldest {
				s.push(Event{Seq: b.seq, Type: Gap, TS: b.now(),
					Payload: map[string]any{"reason": GapReasonBuffer, "since": since}})
			}
			for _, ev := range b.ring {
				if ev.Seq > since && s.match(ev) {
					s.push(ev)
				}
			}
		}
	}
	b.subs[s] = struct{}{}
	return s
}

// LastSeq is the sequence number of the most recently published event.
func (b *Bus) LastSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Close closes all subscriptions; later Subscribe calls return closed ones.
func (b *Bus) Close() {
	b.mu.Lock()
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = make(map[*Subscription]struct{})
	b.mu.Unlock()
	for _, s := range subs {
		s.closeLocked()
	}
}

func (b *Bus) unregister(s *Subscription) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Subscription is a consumer handle. Read events from C(); the channel is
// closed when the subscription or bus is closed.
type Subscription struct {
	bus *Bus
	cfg subConfig
	ch  chan Event

	mu         sync.Mutex // guards closed, overflowed and sends/close on ch
	closed     bool
	overflowed bool
}

func newSubscription(b *Bus, size int, cfg subConfig) *Subscription {
	return &Subscription{bus: b, cfg: cfg, ch: make(chan Event, max(1, size))}
}

// C is the event channel.
func (s *Subscription) C() <-chan Event { return s.ch }

func (s *Subscription) match(ev Event) bool {
	if s.cfg.types != nil {
		if _, ok := s.cfg.types[ev.Type]; !ok {
			return false
		}
	}
	return s.cfg.filter == nil || s.cfg.filter(ev)
}

// push enqueues without ever blocking (callers hold the bus lock, so there
// is a single sender and a drain always frees room for one event).
func (s *Subscription) push(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.ch) == 0 {
		s.overflowed = false
	}
	select {
	case s.ch <- ev:
		return
	default:
	}
	if !s.overflowed {
		s.overflowed = true
		s.bus.log.Warn("event subscriber queue overflow; dropping queued events, subscriber must resync",
			"size", cap(s.ch))
	}
	for { // drop everything queued (consumer may race us; that is fine)
		select {
		case <-s.ch:
			continue
		default:
		}
		break
	}
	s.ch <- Event{Seq: ev.Seq, Type: Gap, TS: ev.TS,
		Payload: map[string]any{"reason": GapReasonOverflow}}
}

// Drain returns all queued events without blocking.
func (s *Subscription) Drain() []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-s.ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

// Closed reports whether the subscription is closed.
func (s *Subscription) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close unregisters from the bus and closes C(). Idempotent. Queued events
// are discarded, as in Python.
func (s *Subscription) Close() {
	s.closeLocked()
	s.bus.unregister(s)
}

func (s *Subscription) closeLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for { // discard queued events so receivers see closure immediately
		select {
		case <-s.ch:
			continue
		default:
		}
		break
	}
	close(s.ch)
}
