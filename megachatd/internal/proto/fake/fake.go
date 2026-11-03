// Package fake provides an in-memory proto.Backend implementing every
// capability interface, with knobs for tests and for the skeleton server.
package fake

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Compile-time proof that the fake implements everything.
var (
	_ proto.Backend               = (*Backend)(nil)
	_ proto.AttachmentSender      = (*Backend)(nil)
	_ proto.BatchAttachmentSender = (*Backend)(nil)
	_ proto.AttachmentResolver    = (*Backend)(nil)
	_ proto.AttachmentChunker     = (*Backend)(nil)
	_ proto.SentMirror            = (*Backend)(nil)
	_ proto.Editor                = (*Backend)(nil)
	_ proto.Reactor               = (*Backend)(nil)
	_ proto.ReactionLister        = (*Backend)(nil)
	_ proto.AddressBook           = (*Backend)(nil)
	_ proto.ContactRefresher      = (*Backend)(nil)
	_ proto.ContactFinder         = (*Backend)(nil)
	_ proto.Pairer                = (*Backend)(nil)
	_ proto.TwoFactor             = (*Backend)(nil)
	_ proto.Archiver              = (*Backend)(nil)
	_ proto.HistoryFetcher        = (*Backend)(nil)
	_ proto.HistoryResyncer       = (*Backend)(nil)
	_ proto.PresenceSubscriber    = (*Backend)(nil)
	_ proto.TypingSender          = (*Backend)(nil)
)

// ErrInjected is the default error for the Fail* knobs.
var ErrInjected = errors.New("fake: injected failure")

// Call records one invocation (Method plus a free-form Args map).
type Call struct {
	Method string
	Args   map[string]any
}

// Backend is the in-memory fake. The zero value is not usable; use New.
// Exported fields are knobs: set them before sharing the backend (or via the
// Set* helpers, which lock).
type Backend struct {
	mu sync.Mutex

	protocol  string
	connected bool
	nextID    int64
	events    chan proto.Event

	contacts    []proto.Contact
	addressBook []proto.Contact
	messages    map[string][]proto.Message // by contactID
	attachments map[string]string          // id -> path
	chunks      map[string][]byte          // id -> data
	reactions   []string
	archived    map[string]bool
	calls       []Call

	// Knobs.
	connectErr    error
	connectDelay  time.Duration
	sendErr       error
	sendDelay     time.Duration
	failMethods   map[string]error
	needsPairing  bool
	pairingQR     string
	twoFAPassword string
	refreshErr    string
	refreshNew    []proto.Contact
	blockAddrBook chan struct{} // if non-nil, ListAddressBook waits on it
}

// New creates a fake for the given protocol id.
func New(protocol string) *Backend {
	return &Backend{
		protocol:    protocol,
		nextID:      time.Now().UnixMilli(),
		events:      make(chan proto.Event, 256),
		messages:    map[string][]proto.Message{},
		attachments: map[string]string{},
		chunks:      map[string][]byte{},
		archived:    map[string]bool{},
		failMethods: map[string]error{},
		reactions:   []string{"👍", "❤️", "😂", "😮", "😢", "🙏"},
	}
}

// ---- knobs -------------------------------------------------------------------

func (b *Backend) SetContacts(cs ...proto.Contact) {
	b.mu.Lock()
	b.contacts = append([]proto.Contact(nil), cs...)
	b.mu.Unlock()
}
func (b *Backend) SetAddressBook(cs ...proto.Contact) {
	b.mu.Lock()
	b.addressBook = append([]proto.Contact(nil), cs...)
	b.mu.Unlock()
}

// FailConnect makes Connect return err (nil clears).
func (b *Backend) FailConnect(err error) { b.mu.Lock(); b.connectErr = err; b.mu.Unlock() }

// DelayConnect makes Connect sleep (respecting ctx).
func (b *Backend) DelayConnect(d time.Duration) { b.mu.Lock(); b.connectDelay = d; b.mu.Unlock() }

// FailSend makes sends return err (nil clears). DelaySend adds latency.
func (b *Backend) FailSend(err error)        { b.mu.Lock(); b.sendErr = err; b.mu.Unlock() }
func (b *Backend) DelaySend(d time.Duration) { b.mu.Lock(); b.sendDelay = d; b.mu.Unlock() }

// FailMethod makes the named method (e.g. "ListContacts", "MarkRead",
// "EditMessage") return err (nil clears).
func (b *Backend) FailMethod(method string, err error) {
	b.mu.Lock()
	if err == nil {
		delete(b.failMethods, method)
	} else {
		b.failMethods[method] = err
	}
	b.mu.Unlock()
}

// SetPairing configures NeedsPairing/PairingQR.
func (b *Backend) SetPairing(needs bool, qr string) {
	b.mu.Lock()
	b.needsPairing, b.pairingQR = needs, qr
	b.mu.Unlock()
}

// SetTwoFactorPassword sets the password Complete2FA accepts ("" = any).
func (b *Backend) SetTwoFactorPassword(p string) { b.mu.Lock(); b.twoFAPassword = p; b.mu.Unlock() }

// SetRefresh configures RefreshContacts: errs non-empty simulates a remote
// failure; newContacts are reported as new.
func (b *Backend) SetRefresh(errs string, newContacts ...proto.Contact) {
	b.mu.Lock()
	b.refreshErr, b.refreshNew = errs, newContacts
	b.mu.Unlock()
}

// BlockAddressBook makes ListAddressBook block until the returned release
// func is called or ctx ends (to test manager timeouts).
func (b *Backend) BlockAddressBook() (release func()) {
	ch := make(chan struct{})
	b.mu.Lock()
	b.blockAddrBook = ch
	b.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// AddAttachment registers a resolvable attachment id (and optional bytes
// served by AttachmentChunk).
func (b *Backend) AddAttachment(id, path string, data []byte) {
	b.mu.Lock()
	b.attachments[id] = path
	if data != nil {
		b.chunks[id] = data
	}
	b.mu.Unlock()
}

// AddHistory seeds FetchHistory.
func (b *Backend) AddHistory(contactID string, msgs ...proto.Message) {
	b.mu.Lock()
	b.messages[contactID] = append(b.messages[contactID], msgs...)
	b.mu.Unlock()
}

// Emit injects an event into Receive streams (as if the remote sent it).
// It drops the event if the buffer is full rather than blocking.
func (b *Backend) Emit(ev proto.Event) bool {
	if ev.Protocol == "" {
		ev.Protocol = b.protocol
	}
	select {
	case b.events <- ev:
		return true
	default:
		return false
	}
}

// Calls returns a copy of the recorded calls; CallsTo filters by method.
func (b *Backend) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Call(nil), b.calls...)
}
func (b *Backend) CallsTo(method string) []Call {
	var out []Call
	for _, c := range b.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Archived reports the archive flag set via SetChatArchived.
func (b *Backend) Archived(contactID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.archived[contactID]
}

// ---- helpers -------------------------------------------------------------------

func (b *Backend) record(method string, args map[string]any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, Call{method, args})
	return b.failMethods[method]
}

func (b *Backend) newID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	return strconv.FormatInt(b.nextID, 10)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Backend) sendGate(ctx context.Context) error {
	b.mu.Lock()
	d, err := b.sendDelay, b.sendErr
	b.mu.Unlock()
	if e := sleep(ctx, d); e != nil {
		return e
	}
	return err
}

// ---- proto.Backend ---------------------------------------------------------------

func (b *Backend) Protocol() string { return b.protocol }

func (b *Backend) Connect(ctx context.Context) error {
	if err := b.record("Connect", nil); err != nil {
		return err
	}
	b.mu.Lock()
	d, err := b.connectDelay, b.connectErr
	b.mu.Unlock()
	if e := sleep(ctx, d); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.connected = true
	b.mu.Unlock()
	return nil
}

func (b *Backend) Disconnect(ctx context.Context) error {
	err := b.record("Disconnect", nil)
	b.mu.Lock()
	b.connected = false
	b.mu.Unlock()
	return err
}

func (b *Backend) IsConnected() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.connected }

func (b *Backend) ListContacts(ctx context.Context) ([]proto.Contact, error) {
	if err := b.record("ListContacts", nil); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]proto.Contact(nil), b.contacts...), nil
}

func (b *Backend) SendMessage(ctx context.Context, contactID, text string, q proto.Quote) (string, error) {
	if err := b.record("SendMessage", map[string]any{"contact": contactID, "text": text, "quote": q}); err != nil {
		return "", err
	}
	if err := b.sendGate(ctx); err != nil {
		return "", err
	}
	id := b.newID()
	ts, _ := strconv.ParseInt(id, 10, 64)
	b.mu.Lock()
	b.messages[contactID] = append(b.messages[contactID], proto.Message{
		ID: id, ContactID: contactID, Protocol: b.protocol, Text: text, IsMine: true,
		Sender: "me", Timestamp: ts, QuoteText: q.Message, MsgType: "text", Status: proto.StatusSent,
		ReplyToMessageID: q.ReplyToMessageID,
	})
	b.mu.Unlock()
	return id, nil
}

func (b *Backend) MarkRead(ctx context.Context, contactID string) error {
	return b.record("MarkRead", map[string]any{"contact": contactID})
}

// Receive streams injected events. The channel closes when ctx ends.
// Multiple concurrent Receive calls compete for events.
func (b *Backend) Receive(ctx context.Context) (<-chan proto.Event, error) {
	if err := b.record("Receive", nil); err != nil {
		return nil, err
	}
	out := make(chan proto.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-b.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// ---- attachments -------------------------------------------------------------------

func (b *Backend) SendAttachment(ctx context.Context, contactID string, a proto.Attachment, q proto.Quote) (string, error) {
	if err := b.record("SendAttachment", map[string]any{"contact": contactID, "attachment": a, "quote": q}); err != nil {
		return "", err
	}
	if err := b.sendGate(ctx); err != nil {
		return "", err
	}
	return b.newID(), nil
}

func (b *Backend) SendAttachments(ctx context.Context, contactID string, atts []proto.Attachment, batchID string, q proto.Quote) ([]string, error) {
	if err := b.record("SendAttachments", map[string]any{"contact": contactID, "attachments": atts, "batch": batchID, "quote": q}); err != nil {
		return nil, err
	}
	if err := b.sendGate(ctx); err != nil {
		return nil, err
	}
	ids := make([]string, len(atts))
	for i := range atts {
		ids[i] = b.newID()
	}
	return ids, nil
}

func (b *Backend) AttachmentPath(ctx context.Context, id string) (string, bool) {
	b.record("AttachmentPath", map[string]any{"id": id})
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.attachments[id]
	return p, ok
}

func (b *Backend) AttachmentChunk(ctx context.Context, id string, start int64, length int) ([]byte, error) {
	if err := b.record("AttachmentChunk", map[string]any{"id": id, "start": start, "length": length}); err != nil {
		return nil, err
	}
	b.mu.Lock()
	data, ok := b.chunks[id]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("fake: unknown attachment %q", id)
	}
	if start < 0 {
		start = 0
	}
	if start >= int64(len(data)) {
		return []byte{}, nil
	}
	end := min(int64(len(data)), start+int64(length))
	return append([]byte(nil), data[start:end]...), nil
}

func (b *Backend) EnqueueSentMessage(ctx context.Context, m proto.SentMessage) error {
	if err := b.record("EnqueueSentMessage", map[string]any{"message": m}); err != nil {
		return err
	}
	b.Emit(proto.Event{Type: proto.EventMessage, ContactID: m.ContactID, Payload: map[string]any{
		"id": m.MessageID, "text": m.Text, "is_mine": true, "attachment_path": m.AttachmentPath,
	}})
	return nil
}

func (b *Backend) EnqueueSentNotification(ctx context.Context, contactID, messageID string, ts int64, batchID string) error {
	if err := b.record("EnqueueSentNotification", map[string]any{"contact": contactID, "id": messageID, "ts": ts, "batch": batchID}); err != nil {
		return err
	}
	b.Emit(proto.Event{Type: proto.EventSentMirror, ContactID: contactID, Payload: map[string]any{
		"id": messageID, "timestamp": ts, "batch_id": batchID,
	}})
	return nil
}

// ---- edit / react ---------------------------------------------------------------------

func (b *Backend) EditMessage(ctx context.Context, contactID, messageID, newText string) (bool, error) {
	if err := b.record("EditMessage", map[string]any{"contact": contactID, "id": messageID, "text": newText}); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, m := range b.messages[contactID] {
		if m.ID == messageID && m.IsMine {
			b.messages[contactID][i].Text = newText
			return true, nil
		}
	}
	return false, nil
}

func (b *Backend) ApplyEdit(contactID, messageID, newText string, o proto.ApplyEditOptions) (*proto.EditResult, error) {
	if err := b.record("ApplyEdit", map[string]any{"contact": contactID, "id": messageID, "text": newText}); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, m := range b.messages[contactID] {
		if m.ID == messageID {
			if m.Text == newText {
				return nil, nil
			}
			b.messages[contactID][i].Text = newText
			mine := m.IsMine
			if o.IsMine != nil {
				mine = *o.IsMine
			}
			return &proto.EditResult{MessageID: m.ID, Timestamp: m.Timestamp, OldText: m.Text, Text: newText, IsMine: mine}, nil
		}
	}
	return nil, nil
}

func (b *Backend) SendReaction(ctx context.Context, contactID, messageID, emoji, targetAuthor string) (bool, error) {
	if err := b.record("SendReaction", map[string]any{"contact": contactID, "id": messageID, "emoji": emoji, "author": targetAuthor}); err != nil {
		return false, err
	}
	return true, nil
}

func (b *Backend) ApplyReaction(contactID string, payload map[string]any) (map[string]any, error) {
	if err := b.record("ApplyReaction", map[string]any{"contact": contactID, "payload": payload}); err != nil {
		return nil, err
	}
	id, _ := payload["target_message_id"].(string)
	if id == "" {
		return nil, nil
	}
	return map[string]any{"message_id": id, "reactions": []any{payload["emoji"]}}, nil
}

func (b *Backend) AvailableReactions(ctx context.Context) ([]string, error) {
	if err := b.record("AvailableReactions", nil); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.reactions...), nil
}

// ---- contacts -----------------------------------------------------------------------------

func (b *Backend) ListAddressBook(ctx context.Context, force bool) ([]proto.Contact, error) {
	if err := b.record("ListAddressBook", map[string]any{"force": force}); err != nil {
		return nil, err
	}
	b.mu.Lock()
	block := b.blockAddrBook
	b.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	src := b.addressBook
	if src == nil {
		src = b.contacts
	}
	out := make([]proto.Contact, len(src))
	for i, c := range src {
		out[i] = c.WithExtra("address_book", true)
	}
	return out, nil
}

func (b *Backend) RefreshContacts(ctx context.Context, force bool) proto.AddressBookRefreshResult {
	b.record("RefreshContacts", map[string]any{"force": force})
	b.mu.Lock()
	defer b.mu.Unlock()
	res := proto.AddressBookRefreshResult{Protocol: b.protocol}
	if b.refreshErr != "" {
		res.Errors = b.refreshErr
		return res
	}
	res.NewContacts = append([]proto.Contact(nil), b.refreshNew...)
	b.contacts = append(b.contacts, b.refreshNew...)
	return res
}

func (b *Backend) FindContact(contactID string) (proto.Contact, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.contacts {
		if c.ID == contactID {
			return c, true
		}
	}
	for _, c := range b.addressBook {
		if c.ID == contactID {
			return c, true
		}
	}
	return proto.Contact{}, false
}

// ---- pairing / misc ---------------------------------------------------------------------------

func (b *Backend) NeedsPairing() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.needsPairing }

func (b *Backend) PairingQR(ctx context.Context) (string, error) {
	if err := b.record("PairingQR", nil); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.needsPairing {
		return "", nil
	}
	return b.pairingQR, nil
}

func (b *Backend) Complete2FA(ctx context.Context, password string) (bool, error) {
	if err := b.record("Complete2FA", map[string]any{"password": password}); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ok := b.twoFAPassword == "" || b.twoFAPassword == password
	if ok {
		b.needsPairing = false
	}
	return ok, nil
}

func (b *Backend) SetChatArchived(ctx context.Context, contactID string, archived bool) (bool, error) {
	if err := b.record("SetChatArchived", map[string]any{"contact": contactID, "archived": archived}); err != nil {
		return false, err
	}
	b.mu.Lock()
	b.archived[contactID] = archived
	b.mu.Unlock()
	return true, nil
}

func (b *Backend) FetchHistory(ctx context.Context, contactID string, limit int) ([]proto.Message, error) {
	if err := b.record("FetchHistory", map[string]any{"contact": contactID, "limit": limit}); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	msgs := b.messages[contactID]
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return append([]proto.Message(nil), msgs...), nil
}

func (b *Backend) ResyncHistory(ctx context.Context, limit int) (int, error) {
	if err := b.record("ResyncHistory", map[string]any{"limit": limit}); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages), nil
}

func (b *Backend) StartPresenceSubscribe(ctx context.Context) error {
	return b.record("StartPresenceSubscribe", nil)
}

func (b *Backend) SendTyping(ctx context.Context, contactID string, started bool) error {
	return b.record("SendTyping", map[string]any{"contact": contactID, "started": started})
}
