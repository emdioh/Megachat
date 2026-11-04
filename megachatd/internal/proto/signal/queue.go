package signal

import (
	"context"
	"sync"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// eventQueue is an unbounded FIFO of backend events (Python's queue.Queue).
type eventQueue struct {
	mu     sync.Mutex
	items  []proto.Event
	notify chan struct{}
}

func newEventQueue() *eventQueue { return &eventQueue{notify: make(chan struct{}, 1)} }

func (q *eventQueue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *eventQueue) put(ev proto.Event) {
	q.mu.Lock()
	q.items = append(q.items, ev)
	q.mu.Unlock()
	q.wake()
}

func (q *eventQueue) putFront(ev proto.Event) {
	q.mu.Lock()
	q.items = append([]proto.Event{ev}, q.items...)
	q.mu.Unlock()
	q.wake()
}

func (q *eventQueue) pop() (proto.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return proto.Event{}, false
	}
	ev := q.items[0]
	q.items = q.items[1:]
	return ev, true
}

// get blocks until an event is available; false when ctx or closed fires.
func (q *eventQueue) get(ctx context.Context, closed <-chan struct{}) (proto.Event, bool) {
	for {
		if ctx.Err() != nil {
			return proto.Event{}, false
		}
		select {
		case <-closed:
			return proto.Event{}, false
		default:
		}
		if ev, ok := q.pop(); ok {
			if q.len() > 0 {
				q.wake()
			}
			return ev, true
		}
		select {
		case <-q.notify:
		case <-ctx.Done():
			return proto.Event{}, false
		case <-closed:
			return proto.Event{}, false
		}
	}
}

func (q *eventQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *eventQueue) drain() []proto.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items
	q.items = nil
	return out
}
