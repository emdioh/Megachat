package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Port of server/send_registry.py: in-memory idempotency registry for text
// sends. No eviction by capacity: only expired entries are pruned.

const (
	TTLSent       = 600 * time.Second
	TTLInflight   = 120 * time.Second
	MaxEntries    = 100_000
	PruneInterval = 60 * time.Second
)

// SendOutcome of ClaimOrLookup.
type SendOutcome string

const (
	OutcomeSent     SendOutcome = "sent"
	OutcomeInflight SendOutcome = "inflight"
	OutcomeClaimed  SendOutcome = "claimed"
)

// SendKey is (protocol, contact_id, client_msg_id).
type SendKey struct{ Protocol, ContactID, ClientMsgID string }

// SendEntry is a registry entry (copy).
type SendEntry struct {
	Status      string
	Token       string
	MessageID   *string
	Timestamp   *int64
	Fingerprint string
	At          time.Time
}

// SendRegistry is safe for concurrent use.
type SendRegistry struct {
	mu                   sync.Mutex
	ttlSent, ttlInflight time.Duration
	maxEntries           int
	pruneInterval        time.Duration
	clock                func() time.Time
	entries              map[SendKey]*SendEntry
	lastPrune            time.Time
}

// SendRegistryOption configures NewSendRegistry.
type SendRegistryOption func(*SendRegistry)

// WithRegistryClock sets the (monotonic) clock.
func WithRegistryClock(f func() time.Time) SendRegistryOption {
	return func(r *SendRegistry) { r.clock = f }
}

// WithRegistryTTL overrides the TTLs.
func WithRegistryTTL(sent, inflight time.Duration) SendRegistryOption {
	return func(r *SendRegistry) { r.ttlSent, r.ttlInflight = sent, inflight }
}

// WithRegistryLimits overrides the soft capacity and prune interval.
func WithRegistryLimits(maxEntries int, interval time.Duration) SendRegistryOption {
	return func(r *SendRegistry) { r.maxEntries, r.pruneInterval = maxEntries, interval }
}

// NewSendRegistry creates a registry.
func NewSendRegistry(opts ...SendRegistryOption) *SendRegistry {
	r := &SendRegistry{
		ttlSent: TTLSent, ttlInflight: TTLInflight, maxEntries: MaxEntries,
		pruneInterval: PruneInterval, clock: time.Now, entries: map[SendKey]*SendEntry{},
	}
	for _, o := range opts {
		o(r)
	}
	r.lastPrune = r.clock()
	return r
}

// SendFingerprint is build_fingerprint: sha256 of the sorted-keys, compact,
// non-ASCII-preserving JSON of the normalised payload.
type SendFingerprint struct {
	Protocol          string
	ContactID         string
	Text              string
	QuoteTimestamp    *int64
	QuoteAuthor       *string
	QuoteMessage      *string
	ReplyToMessageID  *string
	QuoteContentType  *string
	QuoteAttachmentID *string
}

// Build returns the hex digest.
func (f SendFingerprint) Build() string {
	payload := map[string]any{
		"protocol":            f.Protocol,
		"contact_id":          f.ContactID,
		"text":                f.Text,
		"quote_timestamp":     f.QuoteTimestamp,
		"quote_author":        f.QuoteAuthor,
		"quote_message":       f.QuoteMessage,
		"reply_to_message_id": f.ReplyToMessageID,
		"quote_content_type":  f.QuoteContentType,
		"quote_attachment_id": f.QuoteAttachmentID,
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := marshalNoEscape(k)
		vb, _ := marshalNoEscape(payload[k])
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(vb)
	}
	sb.WriteByte('}')
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// marshalNoEscape is json.dumps(ensure_ascii=False): no HTML escaping.
func marshalNoEscape(v any) ([]byte, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(sb.String(), "\n")), nil
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ClaimOrLookup is atomic. "sent" returns the existing entry; "inflight"
// mutates nothing; "claimed" returns the ownership token.
func (r *SendRegistry) ClaimOrLookup(key SendKey, fingerprint string) (SendOutcome, SendEntry, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock()
	if e := r.entries[key]; e != nil {
		if e.Status == "sent" {
			if now.Sub(e.At) <= r.ttlSent {
				return OutcomeSent, *e, ""
			}
		} else if now.Sub(e.At) <= r.ttlInflight {
			return OutcomeInflight, SendEntry{}, ""
		}
	}
	if len(r.entries) >= r.maxEntries || now.Sub(r.lastPrune) >= r.pruneInterval {
		r.pruneLocked(now)
	}
	token := newToken()
	r.entries[key] = &SendEntry{Status: "inflight", Token: token, Fingerprint: fingerprint, At: now}
	return OutcomeClaimed, SendEntry{}, token
}

// MarkSent moves inflight -> sent if the token matches.
func (r *SendRegistry) MarkSent(key SendKey, token string, messageID *string, timestamp *int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key]
	if e == nil || e.Status != "inflight" || e.Token != token {
		return
	}
	e.Status, e.Token, e.MessageID, e.Timestamp, e.At = "sent", "", messageID, timestamp, r.clock()
}

// Release removes an inflight entry if the token matches.
func (r *SendRegistry) Release(key SendKey, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[key]; e != nil && e.Status == "inflight" && e.Token == token {
		delete(r.entries, key)
	}
}

// Prune removes expired entries.
func (r *SendRegistry) Prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.clock())
}

// Reset clears the registry.
func (r *SendRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = map[SendKey]*SendEntry{}
	r.lastPrune = r.clock()
}

// Snapshot returns a copy of an entry.
func (r *SendRegistry) Snapshot(key SendKey) (SendEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[key]; e != nil {
		return *e, true
	}
	return SendEntry{}, false
}

// Len is the number of entries.
func (r *SendRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *SendRegistry) pruneLocked(now time.Time) {
	for k, e := range r.entries {
		ttl := r.ttlSent
		if e.Status != "sent" {
			ttl = r.ttlInflight
		}
		if now.Sub(e.At) > ttl {
			delete(r.entries, k)
		}
	}
	r.lastPrune = now
}
