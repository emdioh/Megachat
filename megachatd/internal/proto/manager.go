package proto

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is a backend's lifecycle state as tracked by the Manager.
type State string

const (
	StateIdle       State = "idle"
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateFailed     State = "failed"
	StateStopped    State = "stopped"
)

// Defaults from protocols/manager.py.
const (
	DefaultFanOutWorkers     = 3
	DefaultAddressBookTimout = 25 * time.Second
	DefaultRefreshTimeout    = 30 * time.Second
	eventBuffer              = 256
)

// StateChange is delivered to the OnState observer (the Python core
// published backend_connecting / backend_ready / backend_done on its bus).
type StateChange struct {
	Protocol string
	State    State
	Err      error
}

// BackendStatus is a snapshot for status endpoints.
type BackendStatus struct {
	Protocol     string
	State        State
	Connected    bool // Backend.IsConnected()
	NeedsPairing bool
	Error        string
	Capabilities []string
}

type slot struct {
	b     Backend
	state State
	err   error
}

// Manager owns the registered backends and routes operations to them.
// It is safe for concurrent use.
type Manager struct {
	mu    sync.RWMutex
	order []string
	slots map[string]*slot

	events  chan Event
	log     *slog.Logger
	onState func(StateChange)

	workers        int
	addressBookTTO time.Duration
	refreshTTO     time.Duration

	lifeMu sync.Mutex // serialises Start/Stop
	runCtx context.Context
	cancel context.CancelFunc
	pumps  sync.WaitGroup
}

// Option configures a Manager.
type Option func(*Manager)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(m *Manager) { m.log = l } }

// WithOnState registers a lifecycle observer; it is called synchronously
// from lifecycle goroutines and must not block.
func WithOnState(f func(StateChange)) Option { return func(m *Manager) { m.onState = f } }

// WithTimeouts overrides the per-backend address-book (25s) and refresh (30s)
// timeouts.
func WithTimeouts(addressBook, refresh time.Duration) Option {
	return func(m *Manager) { m.addressBookTTO, m.refreshTTO = addressBook, refresh }
}

// New creates an empty Manager.
func New(opts ...Option) *Manager {
	m := &Manager{
		slots:          map[string]*slot{},
		events:         make(chan Event, eventBuffer),
		log:            slog.Default(),
		workers:        DefaultFanOutWorkers,
		addressBookTTO: DefaultAddressBookTimout,
		refreshTTO:     DefaultRefreshTimeout,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// ---- registry --------------------------------------------------------------

// Register adds a backend keyed by its Protocol (replacing any previous one,
// like Python's dict assignment).
func (m *Manager) Register(b Backend) error {
	if b == nil || b.Protocol() == "" {
		return errors.New("proto: backend must define a non-empty protocol")
	}
	p := b.Protocol()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.slots[p]; !ok {
		m.order = append(m.order, p)
	}
	m.slots[p] = &slot{b: b, state: StateIdle}
	return nil
}

// Get returns the backend for protocol.
func (m *Manager) Get(protocol string) (Backend, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.slots[protocol]
	if !ok {
		return nil, false
	}
	return s.b, true
}

// All returns the backends in registration order.
func (m *Manager) All() []Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Backend, 0, len(m.order))
	for _, p := range m.order {
		out = append(out, m.slots[p].b)
	}
	return out
}

// Protocols returns the registered protocol ids in registration order.
func (m *Manager) Protocols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.order...)
}

func (m *Manager) must(protocol string) (Backend, error) {
	if b, ok := m.Get(protocol); ok {
		return b, nil
	}
	return nil, fmt.Errorf("%w %q", ErrNoBackend, protocol)
}

// As returns the backend for protocol as capability T. It fails with
// ErrNoBackend or ErrUnsupported.
func As[T any](m *Manager, protocol string) (T, error) {
	var zero T
	b, err := m.must(protocol)
	if err != nil {
		return zero, err
	}
	c, ok := b.(T)
	if !ok {
		return zero, fmt.Errorf("%w: %s", ErrUnsupported, protocol)
	}
	return c, nil
}

// CapabilitiesOf lists the optional capabilities b implements.
func CapabilitiesOf(b Backend) []string {
	var caps []string
	add := func(ok bool, name string) {
		if ok {
			caps = append(caps, name)
		}
	}
	_, ok := b.(AttachmentSender)
	add(ok, "attachments")
	_, ok = b.(BatchAttachmentSender)
	add(ok, "attachment_batch")
	_, ok = b.(AttachmentResolver)
	add(ok, "attachment_path")
	_, ok = b.(AttachmentChunker)
	add(ok, "attachment_chunk")
	_, ok = b.(SentMirror)
	add(ok, "sent_mirror")
	_, ok = b.(Editor)
	add(ok, "edit")
	_, ok = b.(Reactor)
	add(ok, "reactions")
	_, ok = b.(ReactionLister)
	add(ok, "reaction_list")
	_, ok = b.(AddressBook)
	add(ok, "address_book")
	_, ok = b.(ContactRefresher)
	add(ok, "contact_refresh")
	_, ok = b.(ContactFinder)
	add(ok, "contact_find")
	_, ok = b.(Pairer)
	add(ok, "pairing")
	_, ok = b.(TwoFactor)
	add(ok, "two_factor")
	_, ok = b.(Archiver)
	add(ok, "archive")
	_, ok = b.(HistoryFetcher)
	add(ok, "history")
	_, ok = b.(HistoryResyncer)
	add(ok, "history_resync")
	_, ok = b.(PresenceSubscriber)
	add(ok, "presence")
	_, ok = b.(TypingSender)
	add(ok, "typing")
	return caps
}

// ---- lifecycle -------------------------------------------------------------

// Events is the merged stream of all running backends' events. It is never
// closed; consume it until your own context ends.
func (m *Manager) Events() <-chan Event { return m.events }

func (m *Manager) setState(p string, st State, err error) {
	m.mu.Lock()
	if s, ok := m.slots[p]; ok {
		s.state, s.err = st, err
	}
	m.mu.Unlock()
	if m.onState != nil {
		m.onState(StateChange{Protocol: p, State: st, Err: err})
	}
}

func (m *Manager) ensureRun() context.Context {
	if m.runCtx == nil || m.runCtx.Err() != nil {
		m.runCtx, m.cancel = context.WithCancel(context.Background())
	}
	return m.runCtx
}

// Start connects every backend that is not already connected/connecting, in
// parallel, and starts pumping their events into Events(). ctx bounds only
// the connect calls. A failing backend does not affect the others; the
// returned error joins all failures (each wrapped with its protocol).
func (m *Manager) Start(ctx context.Context) error {
	return m.start(ctx, m.Protocols())
}

// StartBackend (re)connects a single backend.
func (m *Manager) StartBackend(ctx context.Context, protocol string) error {
	if _, err := m.must(protocol); err != nil {
		return err
	}
	return m.start(ctx, []string{protocol})
}

func (m *Manager) start(ctx context.Context, protocols []string) error {
	m.lifeMu.Lock()
	run := m.ensureRun()
	m.lifeMu.Unlock()

	var (
		wg   sync.WaitGroup
		emu  sync.Mutex
		errs []error
	)
	for _, p := range protocols {
		m.mu.Lock()
		s := m.slots[p]
		skip := s == nil || s.state == StateConnected || s.state == StateConnecting
		var b Backend
		if !skip {
			b = s.b
			s.state, s.err = StateConnecting, nil
		}
		m.mu.Unlock()
		if skip {
			continue
		}
		if m.onState != nil {
			m.onState(StateChange{Protocol: p, State: StateConnecting})
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Connect(ctx); err != nil {
				m.log.Warn("backend connect failed", "protocol", p, "err", err)
				m.setState(p, StateFailed, err)
				emu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", p, err))
				emu.Unlock()
				return
			}
			m.setState(p, StateConnected, nil)
			m.startPump(run, p, b)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (m *Manager) startPump(ctx context.Context, p string, b Backend) {
	ch, err := b.Receive(ctx)
	if err != nil {
		m.log.Warn("backend receive failed", "protocol", p, "err", err)
		m.setState(p, StateFailed, err)
		return
	}
	m.pumps.Add(1)
	go func() {
		defer m.pumps.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				select {
				case m.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// Stop cancels the event pumps and disconnects every backend (best effort:
// failures are logged and joined into the returned error, never abort the
// others).
func (m *Manager) Stop(ctx context.Context) error {
	m.lifeMu.Lock()
	defer m.lifeMu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.pumps.Wait()
	var errs []error
	for _, p := range m.Protocols() {
		b, _ := m.Get(p)
		if err := b.Disconnect(ctx); err != nil {
			m.log.Debug("backend disconnect failed", "protocol", p, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
		m.setState(p, StateStopped, nil)
	}
	return errors.Join(errs...)
}

// Status snapshots every backend.
func (m *Manager) Status() []BackendStatus {
	m.mu.RLock()
	type snap struct {
		b   Backend
		st  State
		err error
	}
	snaps := make([]snap, 0, len(m.order))
	for _, p := range m.order {
		s := m.slots[p]
		snaps = append(snaps, snap{s.b, s.state, s.err})
	}
	m.mu.RUnlock()
	out := make([]BackendStatus, 0, len(snaps))
	for _, s := range snaps {
		bs := BackendStatus{
			Protocol:     s.b.Protocol(),
			State:        s.st,
			Connected:    s.b.IsConnected(),
			Capabilities: CapabilitiesOf(s.b),
		}
		if pr, ok := s.b.(Pairer); ok {
			bs.NeedsPairing = pr.NeedsPairing()
		}
		if s.err != nil {
			bs.Error = s.err.Error()
		}
		out = append(out, bs)
	}
	return out
}

// ---- contacts ----------------------------------------------------------------

// ListContacts merges the contacts of all backends. A failing backend is
// skipped; its error is joined into the returned error alongside the
// (valid) partial result.
func (m *Manager) ListContacts(ctx context.Context) ([]Contact, error) {
	var out []Contact
	var errs []error
	for _, b := range m.All() {
		cs, err := b.ListContacts(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.Protocol(), err))
			continue
		}
		out = append(out, cs...)
	}
	return out, errors.Join(errs...)
}

func (m *Manager) selectBackends(protocols []string) []Backend {
	var out []Backend
	for _, b := range m.All() {
		if len(protocols) == 0 || contains(protocols, b.Protocol()) {
			out = append(out, b)
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// fanOut runs fn for each backend with at most m.workers in parallel and a
// per-backend timeout. A backend that exceeds it is abandoned (its goroutine
// finishes on its own) and reported as a context.DeadlineExceeded error.
// Results are returned in backend order.
func fanOut[T any](m *Manager, ctx context.Context, bs []Backend, tto time.Duration,
	fn func(context.Context, Backend) (T, error)) ([]T, []error) {
	vals := make([]T, len(bs))
	errs := make([]error, len(bs))
	sem := make(chan struct{}, m.workers)
	var wg sync.WaitGroup
	for i, b := range bs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, tto)
			defer cancel()
			type res struct {
				v   T
				err error
			}
			done := make(chan res, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						done <- res{err: fmt.Errorf("panic: %v", r)}
					}
				}()
				v, err := fn(cctx, b)
				done <- res{v, err}
			}()
			select {
			case r := <-done:
				vals[i], errs[i] = r.v, r.err
			case <-cctx.Done():
				errs[i] = cctx.Err()
			}
		}()
	}
	wg.Wait()
	return vals, errs
}

// ListAddressBook returns the aggregated address book of the selected
// protocols (nil/empty = all), fanned out with 3 workers and a 25s
// per-backend timeout. Failing backends are reported in the returned map
// (protocol -> error) and skipped.
func (m *Manager) ListAddressBook(ctx context.Context, protocols []string, force bool) ([]Contact, map[string]error) {
	bs := m.selectBackends(protocols)
	vals, errs := fanOut(m, ctx, bs, m.addressBookTTO, func(c context.Context, b Backend) ([]Contact, error) {
		if ab, ok := b.(AddressBook); ok {
			return ab.ListAddressBook(c, force)
		}
		// base default: loaded contacts flagged as address book
		cs, err := b.ListContacts(c)
		for i := range cs {
			cs[i] = cs[i].WithExtra("address_book", true)
		}
		return cs, err
	})
	var out []Contact
	failed := map[string]error{}
	for i, b := range bs {
		if errs[i] != nil {
			failed[b.Protocol()] = errs[i]
			m.log.Warn("address book failed", "protocol", b.Protocol(), "err", errs[i])
			continue
		}
		out = append(out, vals[i]...)
	}
	return out, failed
}

// RefreshContacts runs the dynamic contact refresh on the selected backends
// (30s timeout each). It never fails as a whole: errors are isolated per
// backend in the result's Errors field. Backends without ContactRefresher
// return an empty result.
func (m *Manager) RefreshContacts(ctx context.Context, protocols []string, force bool) map[string]AddressBookRefreshResult {
	bs := m.selectBackends(protocols)
	vals, errs := fanOut(m, ctx, bs, m.refreshTTO, func(c context.Context, b Backend) (AddressBookRefreshResult, error) {
		if r, ok := b.(ContactRefresher); ok {
			return r.RefreshContacts(c, force), nil
		}
		return AddressBookRefreshResult{Protocol: b.Protocol()}, nil
	})
	out := make(map[string]AddressBookRefreshResult, len(bs))
	for i, b := range bs {
		if errs[i] != nil {
			out[b.Protocol()] = AddressBookRefreshResult{Protocol: b.Protocol(), Errors: errs[i].Error()}
			m.log.Warn("dynamic refresh failed", "protocol", b.Protocol(), "err", errs[i])
			continue
		}
		out[b.Protocol()] = vals[i]
	}
	return out
}

// FindContact looks a contact up in memory via the backend's ContactFinder
// (zero network). ok=false when the backend is absent, incapable or unaware.
func (m *Manager) FindContact(protocol, contactID string) (Contact, bool) {
	cf, err := As[ContactFinder](m, protocol)
	if err != nil {
		return Contact{}, false
	}
	return cf.FindContact(contactID)
}

// ---- sending -----------------------------------------------------------------

// SendMessage routes a text send to protocol's backend and mirrors it into
// the receive queue when the backend supports SentMirror.
func (m *Manager) SendMessage(ctx context.Context, protocol, contactID, text string, quote Quote) (string, error) {
	b, err := m.must(protocol)
	if err != nil {
		return "", err
	}
	id, err := b.SendMessage(ctx, contactID, text, quote)
	if err != nil {
		return "", err
	}
	qct, qfn := ParseQuoteAttachmentDescriptor(firstOf(quote.Attachments))
	m.mirror(ctx, b, SentMessage{
		ContactID: contactID, MessageID: id, Text: text, Quote: quote,
		QuoteContentType: qct, QuoteFilename: qfn,
	})
	return id, nil
}

// SendAttachment routes a single-attachment send.
func (m *Manager) SendAttachment(ctx context.Context, protocol, contactID string, att Attachment, quote Quote) (string, error) {
	b, err := m.must(protocol)
	if err != nil {
		return "", err
	}
	as, ok := b.(AttachmentSender)
	if !ok {
		return "", fmt.Errorf("%w: %s attachments", ErrUnsupported, protocol)
	}
	id, err := as.SendAttachment(ctx, contactID, att, quote)
	if err != nil {
		return "", err
	}
	qct, qfn := ParseQuoteAttachmentDescriptor(firstOf(quote.Attachments))
	m.mirror(ctx, b, SentMessage{
		ContactID: contactID, MessageID: id, Text: att.Caption, Quote: quote,
		AttachmentPath: att.Path, MimeType: att.MimeType, MediaKind: att.MediaKind,
		Filename: att.Filename, QuoteContentType: qct, QuoteFilename: qfn,
	})
	return id, nil
}

// SendAttachments sends N attachments atomically (any failure aborts and is
// returned). Uses the backend's native batch send when available, else
// loops SendAttachment with the quote on the first file only. Mirroring
// follows the Python rules: Signal gets a single sent-mirror notification
// (it mirrors its own rows); other protocols get one mirror per message.
func (m *Manager) SendAttachments(ctx context.Context, protocol, contactID string, atts []Attachment, batchID string, quote Quote) ([]string, error) {
	b, err := m.must(protocol)
	if err != nil {
		return nil, err
	}
	var ids []string
	if bs, ok := b.(BatchAttachmentSender); ok {
		ids, err = bs.SendAttachments(ctx, contactID, atts, batchID, quote)
	} else if as, ok := b.(AttachmentSender); ok {
		for i, a := range atts {
			q := Quote{}
			if i == 0 {
				q = quote
			}
			id, serr := as.SendAttachment(ctx, contactID, a, q)
			if serr != nil {
				return nil, serr
			}
			ids = append(ids, id)
		}
	} else {
		return nil, fmt.Errorf("%w: %s attachments", ErrUnsupported, protocol)
	}
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return ids, nil
	}
	if protocol == ProtocolSignal {
		ts, perr := strconv.ParseInt(ids[0], 10, 64)
		if perr != nil {
			ts = time.Now().UnixMilli()
		}
		if sm, ok := b.(SentMirror); ok {
			if nerr := sm.EnqueueSentNotification(ctx, contactID, ids[0], ts, batchID); nerr != nil {
				m.log.Error("failed to enqueue sent-mirror notification", "protocol", protocol, "err", nerr)
			}
		}
		return ids, nil
	}
	qct, qfn := ParseQuoteAttachmentDescriptor(firstOf(quote.Attachments))
	for i, id := range ids {
		if i >= len(atts) {
			break
		}
		idx := i
		sm := SentMessage{
			ContactID: contactID, MessageID: id, Text: atts[i].Caption, BatchID: batchID,
			AttachmentPath: atts[i].Path, MimeType: atts[i].MimeType,
			MediaKind: atts[i].MediaKind, Filename: atts[i].Filename,
		}
		if batchID != "" {
			sm.BatchIndex = &idx
		}
		if i == 0 {
			sm.Quote, sm.QuoteContentType, sm.QuoteFilename = quote, qct, qfn
		}
		m.mirror(ctx, b, sm)
	}
	return ids, nil
}

// mirror publishes a sent message via SentMirror. Never fails the send: an
// fs error retries without the attachment; others are logged.
func (m *Manager) mirror(ctx context.Context, b Backend, sm SentMessage) {
	mi, ok := b.(SentMirror)
	if !ok {
		return
	}
	if b.Protocol() != ProtocolSignal { // quote-media fallback is Signal-only
		sm.QuoteContentType, sm.QuoteFilename = "", ""
	}
	err := mi.EnqueueSentMessage(ctx, sm)
	if err == nil {
		return
	}
	var pe *fs.PathError
	if errors.As(err, &pe) || errors.Is(err, fs.ErrNotExist) {
		m.log.Warn("unable to copy sent attachment while mirroring", "protocol", b.Protocol(), "err", err)
		sm.AttachmentPath = ""
		if err = mi.EnqueueSentMessage(ctx, sm); err == nil {
			return
		}
	}
	m.log.Error("unable to mirror sent message", "protocol", b.Protocol(), "contact", sm.ContactID, "err", err)
}

func firstOf(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

// ParseQuoteAttachmentDescriptor splits a Signal quote-attachment descriptor
// "content_type[:filename[:path]]" (models.parse_quote_attachment_descriptor).
// Everything after the second ':' is the path and ignored; parts are trimmed
// and empty parts are "".
func ParseQuoteAttachmentDescriptor(d string) (contentType, filename string) {
	if d == "" {
		return "", ""
	}
	ct, rest, found := strings.Cut(d, ":")
	contentType = strings.TrimSpace(ct)
	if !found {
		return contentType, ""
	}
	fn, _, _ := strings.Cut(rest, ":")
	return contentType, strings.TrimSpace(fn)
}

// ---- other routed operations ---------------------------------------------------

// MarkRead routes mark-read.
func (m *Manager) MarkRead(ctx context.Context, protocol, contactID string) error {
	b, err := m.must(protocol)
	if err != nil {
		return err
	}
	return b.MarkRead(ctx, contactID)
}

// AttachmentPath resolves an attachment id; ok=false if the backend is
// absent, unsupported or does not know it (Python returned None).
func (m *Manager) AttachmentPath(ctx context.Context, protocol, attachmentID string) (string, bool) {
	r, err := As[AttachmentResolver](m, protocol)
	if err != nil {
		return "", false
	}
	return r.AttachmentPath(ctx, attachmentID)
}

// AttachmentChunk fetches a byte range; ErrUnsupported when the backend
// cannot (Python returned None).
func (m *Manager) AttachmentChunk(ctx context.Context, protocol, attachmentID string, start int64, length int) ([]byte, error) {
	c, err := As[AttachmentChunker](m, protocol)
	if err != nil {
		return nil, err
	}
	return c.AttachmentChunk(ctx, attachmentID, start, length)
}

// EditMessage routes an edit. Unlike Python (which returned False) an absent
// or incapable backend is reported as ErrNoBackend / ErrUnsupported.
func (m *Manager) EditMessage(ctx context.Context, protocol, contactID, messageID, newText string) (bool, error) {
	e, err := As[Editor](m, protocol)
	if err != nil {
		return false, err
	}
	return e.EditMessage(ctx, contactID, messageID, newText)
}

// SendReaction routes a reaction.
func (m *Manager) SendReaction(ctx context.Context, protocol, contactID, messageID, emoji, targetAuthor string) (bool, error) {
	r, err := As[Reactor](m, protocol)
	if err != nil {
		return false, err
	}
	return r.SendReaction(ctx, contactID, messageID, emoji, targetAuthor)
}

// PairingQR returns the current pairing QR ("" when not pairing or when the
// backend has no pairing).
func (m *Manager) PairingQR(ctx context.Context, protocol string) (string, error) {
	p, err := As[Pairer](m, protocol)
	if err != nil {
		return "", err
	}
	return p.PairingQR(ctx)
}

// Complete2FA routes a 2FA password.
func (m *Manager) Complete2FA(ctx context.Context, protocol, password string) (bool, error) {
	t, err := As[TwoFactor](m, protocol)
	if err != nil {
		return false, err
	}
	return t.Complete2FA(ctx, password)
}

// SetChatArchived routes archive/unarchive.
func (m *Manager) SetChatArchived(ctx context.Context, protocol, contactID string, archived bool) (bool, error) {
	a, err := As[Archiver](m, protocol)
	if err != nil {
		return false, err
	}
	return a.SetChatArchived(ctx, contactID, archived)
}

// FetchHistory routes a remote history fetch.
func (m *Manager) FetchHistory(ctx context.Context, protocol, contactID string, limit int) ([]Message, error) {
	h, err := As[HistoryFetcher](m, protocol)
	if err != nil {
		return nil, err
	}
	return h.FetchHistory(ctx, contactID, limit)
}
