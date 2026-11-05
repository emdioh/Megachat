package core

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func TestFingerprintMatchesPython(t *testing.T) {
	ts := int64(1700000000000)
	q, ref := "q", "abc"
	got := SendFingerprint{Protocol: "whatsapp", ContactID: "39123@c.us", Text: "Ciao è 😀 <b>",
		QuoteTimestamp: &ts, QuoteMessage: &q, ReplyToMessageID: &ref}.Build()
	// Value produced by server.send_registry.build_fingerprint.
	if want := "11ece535f84a8c98170c8a91b9d0516bce57d98c0445f26a12de17885150c090"; got != want {
		t.Fatalf("fingerprint %s != %s", got, want)
	}
}

func TestSendRegistry(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewSendRegistry(WithRegistryClock(func() time.Time { return now }))
	k := SendKey{"signal", "+1", "m1"}

	o, _, tok := r.ClaimOrLookup(k, "fp")
	if o != OutcomeClaimed || tok == "" {
		t.Fatalf("first claim: %v", o)
	}
	if o, _, _ := r.ClaimOrLookup(k, "fp"); o != OutcomeInflight {
		t.Fatalf("second claim while inflight: %v", o)
	}
	// A foreign token neither marks nor releases.
	r.MarkSent(k, "other", nil, nil)
	r.Release(k, "other")
	if e, _ := r.Snapshot(k); e.Status != "inflight" {
		t.Fatalf("status %q", e.Status)
	}
	id := "42"
	r.MarkSent(k, tok, &id, nil)
	o, entry, _ := r.ClaimOrLookup(k, "fp")
	if o != OutcomeSent || entry.Fingerprint != "fp" || *entry.MessageID != "42" {
		t.Fatalf("sent lookup: %v %+v", o, entry)
	}
	// Sent entries expire after 600 s; a hit does not extend the TTL.
	now = now.Add(601 * time.Second)
	if o, _, _ := r.ClaimOrLookup(k, "fp"); o != OutcomeClaimed {
		t.Fatalf("expired sent entry should be claimable, got %v", o)
	}
	// Inflight entries expire after 120 s.
	now = now.Add(121 * time.Second)
	if o, _, _ := r.ClaimOrLookup(k, "fp"); o != OutcomeClaimed {
		t.Fatalf("expired inflight entry should be claimable, got %v", o)
	}
	// Release removes an inflight entry.
	o, _, tok = r.ClaimOrLookup(SendKey{"signal", "+1", "m2"}, "x")
	r.Release(SendKey{"signal", "+1", "m2"}, tok)
	if _, ok := r.Snapshot(SendKey{"signal", "+1", "m2"}); ok {
		t.Fatal("released entry still present")
	}
}

func TestSendRegistryNoCapacityEviction(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewSendRegistry(WithRegistryClock(func() time.Time { return now }), WithRegistryLimits(2, time.Hour))
	for i := 0; i < 5; i++ {
		k := SendKey{"signal", "c", fmt.Sprint(i)}
		_, _, tok := r.ClaimOrLookup(k, "f")
		r.MarkSent(k, tok, nil, nil)
	}
	if r.Len() != 5 {
		t.Fatalf("young sent entries must never be evicted, len=%d", r.Len())
	}
}

func TestClassifySendError(t *testing.T) {
	refused := fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	cases := []struct {
		proto string
		err   error
		want  string
	}{
		{"signal", refused, Retryable},
		{"signal", errors.New("Connection refused by daemon"), Retryable},
		{"signal", errors.New("signal-cli not configured"), Terminal},
		{"signal", os.ErrNotExist, Terminal},
		{"signal", errors.New("boom"), Terminal},
		{"whatsapp", &HTTPStatusError{Code: 503}, Retryable},
		{"whatsapp", &HTTPStatusError{Code: 429}, Retryable},
		{"whatsapp", &HTTPStatusError{Code: 404}, Terminal},
		{"whatsapp", errors.New("request failed status=502"), Retryable},
		{"whatsapp", errors.New("request failed status=400"), Terminal},
		{"whatsapp", errors.New("status=0 connection refused"), Retryable},
		{"telegram", &FloodWaitError{Seconds: 2}, Retryable},
		{"telegram", &FloodWaitError{Seconds: 30}, Terminal},
		{"telegram", refused, Retryable},
		{"telegram", errors.New("anything"), Terminal},
		{"other", refused, Terminal},
	}
	for _, c := range cases {
		if got := ClassifySendError(c.proto, c.err); got != c.want {
			t.Errorf("%s %v: got %s want %s", c.proto, c.err, got, c.want)
		}
	}
}

func TestComputeDelayBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for attempt := 1; attempt <= 3; attempt++ {
		base := time.Duration(1<<(attempt-1)) * time.Second
		for i := 0; i < 50; i++ {
			d := ComputeDelay(attempt, rng)
			if d < base || d > base+time.Second {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, d, base, base+time.Second)
			}
		}
	}
	if got := SafeErrorText(errors.New(strings.Repeat("é", 300)), 200); len([]rune(got)) != 200 {
		t.Fatalf("SafeErrorText length %d", len([]rune(got)))
	}
}

func TestLogBufferRedactionAndFilter(t *testing.T) {
	b := NewLogBuffer()
	now := time.Now()
	b.Add(now, "INFO", "x", "key sk-abcdef123456 and AIzaSyA1234567890abc and Bearer token: sekret and Bearer abcdefgh12345")
	b.Add(now, "DEBUG", "x", "dbg")
	b.Add(now, "ERROR", "x", "bad")
	recs := b.Snapshot(0, 500, 0)
	if len(recs) != 3 || recs[0].Message != "key sk-*** and AIza*** and Bearer token: *** and Bearer ***" {
		t.Fatalf("redaction: %+v", recs)
	}
	if got := b.Snapshot(0, 500, 20); len(got) != 2 {
		t.Fatalf("min level: %+v", got)
	}
	if got := b.Snapshot(1, 1, 0); len(got) != 1 || got[0].Seq != 3 {
		t.Fatalf("after/limit keep the last: %+v", got)
	}
	b.Clear()
	if len(b.Snapshot(0, 500, 0)) != 0 {
		t.Fatal("clear")
	}
	for i := 0; i < LogBufferCapacity+10; i++ {
		b.Add(now, "INFO", "x", "m")
	}
	if got := b.Snapshot(0, 5000, 0); len(got) != LogBufferCapacity {
		t.Fatalf("ring size %d", len(got))
	}
}

func TestLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	l := NewLock(p)
	if !l.Acquire() {
		t.Fatal("acquire")
	}
	if pid, ok := l.Holder(); !ok || pid != os.Getpid() {
		t.Fatalf("holder %d %v", pid, ok)
	}
	l.Release()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("lock file should be removed")
	}
	// A dead pid does not block the lock.
	_ = os.WriteFile(p, []byte("999999999"), 0o644)
	if !l.Acquire() {
		t.Fatal("stale lock should be taken over")
	}
	// A live foreign pid (init) blocks it.
	_ = os.WriteFile(p, []byte("1"), 0o644)
	if l.Acquire() {
		t.Fatal("lock held by pid 1 must not be acquired")
	}
}

func TestContactStorageKeys(t *testing.T) {
	wa := func(id string, ex map[string]any) proto.Contact { return contact("whatsapp", id, "n", ex) }
	cases := []struct {
		c    proto.Contact
		want []string
	}{
		{contact("signal", "+1", "n", nil), []string{"+1"}},
		{wa("123@lid", map[string]any{"phone": "39"}), []string{"123@lid", "39@c.us"}},
		{wa("39@c.us", map[string]any{"phone": "39", "lid": "123@lid"}), []string{"39@c.us", "123@lid"}},
		{wa("g@g.us", map[string]any{"phone": "39"}), []string{"g@g.us"}},
		{wa("39@s.whatsapp.net", map[string]any{"phone": "39"}), []string{"39@s.whatsapp.net", "39@c.us"}},
	}
	for _, c := range cases {
		got := ContactStorageKeys(c.c)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: %v want %v", c.c.ID, got, c.want)
		}
	}
}

func TestSearchAndUnresolved(t *testing.T) {
	cs := []proto.Contact{
		contact("signal", "+391", "Alice", map[string]any{"phone": "391"}),
		contact("signal", "+392", "Bob", nil),
	}
	if got := SearchContacts(cs, " ALI ", 50); len(got) != 1 || got[0].ID != "+391" {
		t.Fatalf("search by name: %v", got)
	}
	if got := SearchContacts(cs, "392", 50); len(got) != 1 {
		t.Fatalf("search by id: %v", got)
	}
	if got := SearchContacts(cs, "", 1); len(got) != 1 {
		t.Fatalf("empty query caps: %v", got)
	}
	if !LooksUnresolved(contact("signal", "+39", "+39", nil)) || !LooksUnresolved(contact("signal", "a", "123", nil)) ||
		LooksUnresolved(contact("signal", "a", "Alice", nil)) {
		t.Fatal("LooksUnresolved (signal)")
	}
	if !LooksUnresolved(contact("whatsapp", "39@c.us", "+39", map[string]any{"phone": "39"})) ||
		LooksUnresolved(contact("whatsapp", "39@c.us", "Mario", map[string]any{"phone": "39"})) ||
		!LooksUnresolved(contact("whatsapp", "205086@lid", UnresolvedLIDName("205086"), map[string]any{"lid": "205086@lid"})) ||
		!LooksUnresolved(contact("whatsapp", "205086@lid", UnresolvedLIDName("205086"), map[string]any{"lid": "205086@lid", "phone": "393331112222"})) ||
		LooksUnresolved(contact("whatsapp", "205086@lid", UnresolvedLIDName("205087"), map[string]any{"lid": "205086@lid"})) {
		t.Fatal("LooksUnresolved (whatsapp)")
	}
}
