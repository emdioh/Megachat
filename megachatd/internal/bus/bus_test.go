package bus

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func recv(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-s.C():
		if !ok {
			t.Fatal("channel closed")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	return Event{}
}

func TestPublishSubscribe(t *testing.T) {
	b := New()
	s := b.Subscribe()
	e := b.Publish(Message, map[string]any{"a": 1})
	if e.Seq != 1 || b.LastSeq() != 1 {
		t.Fatalf("seq=%d", e.Seq)
	}
	got := recv(t, s)
	if got.Type != Message || got.Payload["a"] != 1 {
		t.Fatalf("got %+v", got)
	}
	if e2 := b.Publish(Typing, nil); e2.Payload == nil || e2.Seq != 2 {
		t.Fatalf("nil payload not normalised: %+v", e2)
	}
}

func TestFilters(t *testing.T) {
	b := New()
	typed := b.Subscribe(Types(Typing))
	fn := b.Subscribe(WithFilter(func(e Event) bool { return e.Payload["p"] == "signal" }))
	b.Publish(Message, map[string]any{"p": "signal"})
	b.Publish(Typing, map[string]any{"p": "telegram"})
	if got := typed.Drain(); len(got) != 1 || got[0].Type != Typing {
		t.Fatalf("typed: %+v", got)
	}
	if got := fn.Drain(); len(got) != 1 || got[0].Type != Message {
		t.Fatalf("fn: %+v", got)
	}
}

func TestOverflowGap(t *testing.T) {
	b := New(WithBufferSize(2), WithSubscriberQueueSize(2))
	slow := b.Subscribe(QueueSize(2))
	fast := b.Subscribe()
	for i := 0; i < 5; i++ { // must never block
		b.Publish(Message, map[string]any{"i": i})
	}
	evs := slow.Drain()
	if len(evs) == 0 || evs[0].Type != Gap || evs[0].Payload["reason"] != GapReasonOverflow {
		t.Fatalf("expected leading overflow gap, got %+v", evs)
	}
	if last := evs[len(evs)-1]; last.Seq != 5 {
		t.Fatalf("last seq %d", last.Seq)
	}
	_ = fast
	// Subscriber recovers afterwards.
	b.Publish(Message, nil)
	if got := slow.Drain(); len(got) != 1 || got[0].Type != Message {
		t.Fatalf("after recovery: %+v", got)
	}
}

func TestReplaySince(t *testing.T) {
	b := New(WithBufferSize(3))
	for i := 0; i < 5; i++ {
		b.Publish(Message, map[string]any{"i": i})
	}
	// Ring holds seq 3..5. since=1 is too old: gap then 3,4,5.
	evs := b.Subscribe(Since(1)).Drain()
	if len(evs) != 4 || evs[0].Type != Gap || evs[0].Payload["reason"] != GapReasonBuffer {
		t.Fatalf("%+v", evs)
	}
	if evs[1].Seq != 3 || evs[3].Seq != 5 {
		t.Fatalf("%+v", evs)
	}
	// since=2 is exactly adjacent: no gap.
	evs = b.Subscribe(Since(2)).Drain()
	if len(evs) != 3 || evs[0].Seq != 3 {
		t.Fatalf("%+v", evs)
	}
	// since beyond last is clamped: nothing replayed.
	if evs = b.Subscribe(Since(99)).Drain(); len(evs) != 0 {
		t.Fatalf("%+v", evs)
	}
}

func TestCloseSemantics(t *testing.T) {
	b := New()
	s := b.Subscribe()
	b.Publish(Message, nil)
	s.Close()
	s.Close() // idempotent
	if _, ok := <-s.C(); ok {
		t.Fatal("expected closed channel with queue discarded")
	}
	b.Publish(Message, nil) // no panic after close
	s2 := b.Subscribe()
	b.Close()
	if _, ok := <-s2.C(); ok || !s2.Closed() {
		t.Fatal("bus close should close subs")
	}
	if s3 := b.Subscribe(); !s3.Closed() {
		t.Fatal("subscribe after close should be closed")
	}
}

func TestConcurrentRace(t *testing.T) {
	b := New(WithBufferSize(50), WithSubscriberQueueSize(10))
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Publish(Message, map[string]any{"k": fmt.Sprint(p, i)})
			}
		}(p)
	}
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s := b.Subscribe(Since(0))
				for j := 0; j < 5; j++ {
					select {
					case <-s.C():
					default:
					}
				}
				s.Drain()
				s.Close()
			}
		}()
	}
	wg.Add(1)
	go func() { // slow consumer
		defer wg.Done()
		s := b.Subscribe()
		defer s.Close()
		deadline := time.After(200 * time.Millisecond)
		for {
			select {
			case <-s.C():
				time.Sleep(time.Microsecond * 50)
			case <-deadline:
				return
			}
		}
	}()
	wg.Wait()
	if b.LastSeq() != 2000 {
		t.Fatalf("last seq %d", b.LastSeq())
	}
	b.Close()
}

func TestSeqOrderPerSubscriber(t *testing.T) {
	b := New()
	s := b.Subscribe(QueueSize(5000))
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Publish(Message, nil)
			}
		}()
	}
	wg.Wait()
	var prev uint64
	for _, ev := range s.Drain() {
		if ev.Seq != prev+1 {
			t.Fatalf("out of order: %d after %d", ev.Seq, prev)
		}
		prev = ev.Seq
	}
	if prev != 2000 {
		t.Fatalf("got %d events", prev)
	}
}
