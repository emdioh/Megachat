// Package signal is the Go port of protocols/signal.py and protocols/rpc.py:
// the Signal backend, driven through a signal-cli daemon (JSON-RPC over HTTP
// plus an SSE event stream) with a one-shot subprocess fallback.
//
// Besides proto.Backend it implements the capability interfaces Python's
// SignalBackend has (AttachmentSender, BatchAttachmentSender,
// AttachmentResolver, SentMirror, Editor, Reactor, AddressBook,
// ContactRefresher, ContactFinder), the core hooks (core.MessageIngester,
// core.ReceiptProcessor, core.ContactRegistrar, core.UserNumber), the
// linking.SignalAccount interface and media.ProfilePhotoProvider.
package signal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Config is the configuration the backend needs. *config.Config implements it.
type Config interface {
	SignalUserNumber() string
	SignalCLIBinDir() string
	SignalCLIAttachmentsDir() string
	SignalCLIAvatarsDir() string
	QuoteThumbsDir() string
	AddressBookTTLS() int
}

// Store is the slice of internal/store the backend persists through.
// *store.Store implements it.
type Store interface {
	LoadCache(ctx context.Context, protocol string) (map[string][]store.Message, error)
	ListMessages(ctx context.Context, q store.MessageQuery) ([]store.Message, error)
	AddMessage(ctx context.Context, m store.NewMessage) (id int64, inserted bool, err error)
	UpdateMessageID(ctx context.Context, protocol, contact, text string, isMine bool, timestamp int64, msgID string) (bool, error)
	UpdateMessageAttachmentID(ctx context.Context, protocol, contact, msgID string, timestamp int64, attachmentID string, expected *string) (bool, error)
	UpdateMessageAttachmentInfo(ctx context.Context, protocol, contact, msgID string, timestamp int64, info string) (bool, error)
	FillMessageQuoteFields(ctx context.Context, protocol, contact, msgID string, timestamp int64, q store.QuoteFill) (bool, error)
	MarkAsRead(ctx context.Context, protocol, contact string, extraKeys ...string) error
	UpdateMessageStatus(ctx context.Context, timestamp int64, status, protocol, contact string, text *string, expected []string) (bool, error)
	UpdateMessageText(ctx context.Context, u store.TextUpdate) (bool, error)
	ApplyReactionDelta(ctx context.Context, d store.ReactionDelta) (bool, error)
	ReactionsForContact(ctx context.Context, protocol, contact string) ([]store.Reaction, error)
	ResolveReactionTargetRow(ctx context.Context, protocol, contact string, targetMsgID *string, targetTS *int64) (*store.ReactionTarget, error)
}

// BatchSlotWriter is an OPTIONAL store extension for the multi-attachment
// send barrier (Python ran raw SQL here). Without it the batch-slot repair
// and the mirror rollback are skipped with a warning.
type BatchSlotWriter interface {
	// SetBatchSlot stamps batch_id/batch_index on the row with messages.id rowID.
	SetBatchSlot(ctx context.Context, rowID int64, batchID *string, batchIndex int64) error
	// DeleteMirrorRows deletes the rows of (protocol, contact, msgID) whose
	// attachment_id is in attachmentIDs.
	DeleteMirrorRows(ctx context.Context, protocol, contact, msgID string, attachmentIDs []string) error
}

// Options configures New.
type Options struct {
	Config Config // required
	Store  Store  // optional: without it nothing is persisted
	Logger *slog.Logger

	// UserNumber overrides Config.SignalUserNumber().
	UserNumber string

	// Daemon endpoints (default: 127.0.0.1:8080).
	DaemonPort int
	DaemonURL  string
	SSEURL     string
	HTTP       *http.Client

	// CLIPath overrides the signal-cli lookup under Config.SignalCLIBinDir().
	CLIPath string

	// Tunables (defaults: 90 probes, 1 s, 30 s, 1 s).
	ProbeAttempts  int
	ProbeInterval  time.Duration
	SSEReadTimeout time.Duration
	SSERetryDelay  time.Duration
}

// Backend is the Signal backend.
type Backend struct {
	cfg   Config
	st    Store
	log   *slog.Logger
	rpc   *Client
	hc    *http.Client
	opts  Options
	sseU  string
	port  int
	clock func() time.Time

	userMu sync.RWMutex
	number string

	useDaemon atomic.Bool

	lifeMu    sync.Mutex
	runCtx    context.Context
	cancel    context.CancelFunc
	sseCancel context.CancelFunc
	sseDone   chan struct{}
	closed    chan struct{} // closed by Disconnect; replaced on Connect

	queue *eventQueue

	// Contacts (cmu); the message cache and ingest state (ingestMu).
	cmu          sync.RWMutex
	contacts     []*proto.Contact
	byKey        map[string]*proto.Contact
	addressBook  []proto.Contact
	addressBookT time.Time
	addressBookS bool

	ingestMu sync.Mutex
	cache    map[string][]*entry

	sentMu    sync.Mutex
	sentPaths map[string]string // resolved source path -> persistent copy
	sentOrder []string
}

// New builds a Signal backend.
func New(o Options) *Backend {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.DaemonPort == 0 {
		o.DaemonPort = DefaultDaemonPort
	}
	if o.DaemonURL == "" {
		o.DaemonURL = "http://127.0.0.1:" + strconv.Itoa(o.DaemonPort) + "/api/v1/rpc"
	}
	if o.SSEURL == "" {
		o.SSEURL = "http://127.0.0.1:" + strconv.Itoa(o.DaemonPort) + "/api/v1/events"
	}
	if o.ProbeAttempts == 0 {
		o.ProbeAttempts = 90
	}
	if o.ProbeInterval == 0 {
		o.ProbeInterval = time.Second
	}
	if o.SSEReadTimeout == 0 {
		o.SSEReadTimeout = SSEReadTimeout
	}
	if o.SSERetryDelay == 0 {
		o.SSERetryDelay = time.Second
	}
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	closed := make(chan struct{})
	close(closed)
	number := o.UserNumber
	if number == "" && o.Config != nil {
		number = o.Config.SignalUserNumber()
	}
	return &Backend{
		cfg: o.Config, st: o.Store, log: o.Logger, hc: hc, opts: o, port: o.DaemonPort,
		rpc: NewClient(o.DaemonURL, hc), sseU: o.SSEURL, clock: time.Now,
		number: number, closed: closed, queue: newEventQueue(),
		byKey: map[string]*proto.Contact{}, cache: map[string][]*entry{},
		sentPaths: map[string]string{},
	}
}

// Protocol implements proto.Backend.
func (b *Backend) Protocol() string { return proto.ProtocolSignal }

// UserNumber implements linking.SignalAccount / core.UserNumber.
func (b *Backend) UserNumber() string {
	b.userMu.RLock()
	defer b.userMu.RUnlock()
	return b.number
}

// SetUserNumber implements linking.SignalAccount (the link flow stores the
// freshly linked account here, then calls RestartSSE).
func (b *Backend) SetUserNumber(n string) {
	b.userMu.Lock()
	b.number = n
	b.userMu.Unlock()
}

// IsConnected reports whether Connect reached a working daemon RPC session.
func (b *Backend) IsConnected() bool { return b.useDaemon.Load() }

func (b *Backend) cliPath() (string, error) {
	if b.opts.CLIPath != "" {
		return b.opts.CLIPath, nil
	}
	return FindSignalCLI(b.cfg.SignalCLIBinDir())
}

func (b *Backend) isDaemonRunning(ctx context.Context) bool {
	return b.rpc.Call(ctx, "listContacts", nil).HasResult
}

// ---- lifecycle -----------------------------------------------------------------------

// Connect loads the cached history, makes sure a signal-cli daemon answers
// (starting one when needed), loads the contacts and starts the SSE listener.
func (b *Backend) Connect(ctx context.Context) error {
	if b.UserNumber() == "" {
		n := strings.TrimSpace(b.cfg.SignalUserNumber())
		if n == "" {
			return ErrNotConfigured
		}
		b.SetUserNumber(n)
	}
	b.lifeMu.Lock()
	if b.runCtx == nil || b.runCtx.Err() != nil {
		b.runCtx, b.cancel = context.WithCancel(context.Background())
		b.closed = make(chan struct{})
	}
	b.lifeMu.Unlock()

	b.loadProtocolCache(ctx)

	if b.isDaemonRunning(ctx) {
		b.useDaemon.Store(true)
		b.loadContactsRPC(ctx)
	} else {
		cli, err := b.cliPath()
		if err != nil {
			return err
		}
		proc, err := b.startDaemon(cli)
		if err != nil {
			return err
		}
		// The SSE listener retries by itself, so start it right away to
		// catch the messages a fresh daemon downloads in its first seconds.
		b.startSSE()
		up := false
		for i := 0; i < b.opts.ProbeAttempts; i++ {
			if b.rpc.Call(ctx, "listContacts", nil).HasResult {
				up = true
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-proc.done:
				// The daemon is gone (typically the account is not linked
				// yet). Reporting success here would leave the core with a
				// half-connected backend and no warning, so fail instead;
				// link.reconnect restarts it once the account exists.
				b.useDaemon.Store(false)
				b.stopSSE()
				err := proc.exitError()
				b.log.Warn("signal-cli daemon exited", "err", err)
				return err
			case <-time.After(b.opts.ProbeInterval):
			}
		}
		if up {
			b.useDaemon.Store(true)
			b.loadContactsRPC(ctx)
		} else {
			b.log.Warn("signal-cli daemon did not answer; using the subprocess fallback")
			b.useDaemon.Store(false)
			b.loadContactsSubprocess(ctx)
		}
	}

	if b.useDaemon.Load() {
		b.startSSE()
		// Ask the server to re-send pending messages; best effort.
		r := b.rpc.Call(ctx, "sendSyncRequest", nil)
		if r.OK() {
			b.log.Info("SYNC-REQUEST: ok")
		} else {
			b.log.Info("SYNC-REQUEST failed", "err", r.Err)
		}
	}
	return nil
}

func (b *Backend) startDaemon(cli string) (*daemonProc, error) {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	cmd := exec.Command(cli, "-u", b.UserNumber(), "daemon",
		"--http", "127.0.0.1:"+strconv.Itoa(b.port),
		"--receive-mode", "on-connection", "--no-receive-stdout")
	proc := &daemonProc{done: make(chan struct{})}
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { // reap; the daemon outlives Disconnect by design
		proc.err = cmd.Wait()
		close(proc.done)
	}()
	return proc, nil
}

// daemonStderrCap bounds the daemon stderr kept for the connect error.
const daemonStderrCap = 4096

// daemonProc is one spawned signal-cli daemon. done is closed once Wait
// returned; err and stderr are only read after that.
type daemonProc struct {
	done   chan struct{}
	err    error
	stderr tailBuffer
}

// exitError describes why the daemon went away: exit status plus the tail of
// its stderr (signal-cli prints the account error there).
func (p *daemonProc) exitError() error {
	status := "exit status 0"
	if p.err != nil {
		status = p.err.Error()
	}
	msg := "signal-cli daemon exited (" + status + ")"
	if s := p.stderr.String(); s != "" {
		msg += ": " + s
	}
	return errors.New(msg)
}

// tailBuffer is an io.Writer keeping the last daemonStderrCap bytes.
type tailBuffer struct{ buf []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > daemonStderrCap {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-daemonStderrCap:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.buf)) }

// Disconnect stops the SSE listener. The daemon itself is left running, by
// design (like the Python backend).
func (b *Backend) Disconnect(ctx context.Context) error {
	b.lifeMu.Lock()
	cancel, done := b.cancel, b.sseDone
	b.cancel, b.sseCancel, b.sseDone = nil, nil, nil
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	b.lifeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
	}
	b.useDaemon.Store(false)
	return nil
}

// RestartSSE restarts the listener (called after device linking).
func (b *Backend) RestartSSE() {
	b.lifeMu.Lock()
	if b.runCtx == nil || b.runCtx.Err() != nil {
		b.runCtx, b.cancel = context.WithCancel(context.Background())
		b.closed = make(chan struct{})
	}
	b.lifeMu.Unlock()
	b.stopSSE()
	b.startSSE()
}

// stopSSE stops the listener, waiting (bounded) for its goroutine to return.
func (b *Backend) stopSSE() {
	b.lifeMu.Lock()
	cancel, done := b.sseCancel, b.sseDone
	b.sseCancel, b.sseDone = nil, nil
	b.lifeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// ---- SSE ------------------------------------------------------------------------------

func (b *Backend) startSSE() {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	if b.sseDone != nil {
		select {
		case <-b.sseDone:
		default:
			return // already running
		}
	}
	if b.runCtx == nil || b.runCtx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(b.runCtx)
	done := make(chan struct{})
	b.sseCancel, b.sseDone = cancel, done
	go func() {
		defer close(done)
		b.sseLoop(ctx)
	}()
}

func (b *Backend) sseLoop(ctx context.Context) {
	for ctx.Err() == nil {
		received := false
		ListenEvents(ctx, b.hc, b.sseU, b.UserNumber(), b.opts.SSEReadTimeout, func(obj map[string]any) {
			received = true
			env, _ := obj["envelope"].(map[string]any)
			evs := b.EnvelopeToEvents(env)
			for _, ev := range evs {
				b.queue.put(ev)
			}
			if len(evs) > 0 {
				b.log.Debug("SSE: received events", "n", len(evs))
			}
		})
		if !received && ctx.Err() == nil {
			b.log.Debug("SSE: connection lost, retrying")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.opts.SSERetryDelay):
		}
	}
}

// Receive streams normalised events until ctx is cancelled or the backend is
// disconnected; the channel is then closed.
func (b *Backend) Receive(ctx context.Context) (<-chan proto.Event, error) {
	b.lifeMu.Lock()
	closed := b.closed
	b.lifeMu.Unlock()
	out := make(chan proto.Event)
	go func() {
		defer close(out)
		for {
			ev, ok := b.queue.get(ctx, closed)
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				b.queue.putFront(ev)
				return
			case <-closed:
				b.queue.putFront(ev)
				return
			}
		}
	}()
	return out, nil
}

// PollOnce drains the pending events without blocking (poll_once).
func (b *Backend) PollOnce() []proto.Event { return b.queue.drain() }

// ---- contacts --------------------------------------------------------------------------------

var contactLineRE = regexp.MustCompile(`^Number:(?P<number>\S+)\s+Name:(?P<name>.+?)(?:\s+ACI:(?P<aci>\S+))?(?:\s+Profile name:.*)?$`)

func cloneContact(c proto.Contact) proto.Contact {
	m := make(map[string]any, len(c.Extras))
	for k, v := range c.Extras {
		m[k] = v
	}
	c.Extras = m
	return c
}

func newContact(number, name, aci string) proto.Contact {
	if name == "" {
		name = number
	}
	return proto.Contact{ID: number, DisplayName: name, Protocol: proto.ProtocolSignal,
		Extras: map[string]any{"aci": aci, "number": number}}
}

func (b *Backend) loadContactsRPC(ctx context.Context) {
	data := b.rpc.ListContacts(ctx)
	if len(data) > 0 {
		cs := make([]proto.Contact, 0, len(data))
		for _, d := range data {
			cs = append(cs, parseContactDict(d))
		}
		b.setContacts(cs)
		return
	}
	b.loadContactsSubprocess(ctx)
}

func (b *Backend) loadContactsSubprocess(ctx context.Context) {
	out, err := b.runSubprocess(ctx, "listContacts")
	if err != nil {
		b.log.Debug("contact subprocess load failed", "err", err)
		return
	}
	b.setContacts(parseContactsOutput(out))
}

// parseContactsOutput parses `signal-cli listContacts` text output.
func parseContactsOutput(out string) []proto.Contact {
	var cs []proto.Contact
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := contactLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cs = append(cs, newContact(m[1], strings.TrimSpace(m[2]), m[3]))
	}
	return cs
}

func parseContactDict(c map[string]any) proto.Contact {
	number := anyString(firstTruthy(c["number"], c["uuid"], ""))
	name := anyString(firstTruthy(c["name"], c["givenName"], getMap(c, "profile")["givenName"], ""))
	if name == "" {
		name = number
	}
	aci := anyString(firstTruthy(c["uuid"], c["aci"], ""))
	return newContact(number, name, aci)
}

func (b *Backend) lastTimestampLocked(contactID string) int64 {
	var ts int64
	for _, m := range b.cache[contactID] {
		if m.Timestamp > ts {
			ts = m.Timestamp
		}
	}
	return ts
}

func (b *Backend) lastTimestamp(contactID string) int64 {
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	return b.lastTimestampLocked(contactID)
}

// setContacts replaces the contact list, dropping system contacts and
// restoring last_message_ts from the cache.
func (b *Backend) setContacts(cs []proto.Contact) {
	list := make([]*proto.Contact, 0, len(cs))
	for i := range cs {
		c := cs[i]
		if c.ID == "" || strings.Contains(c.ID, "@broadcast") {
			continue
		}
		c = cloneContact(c)
		c.Extras["last_message_ts"] = b.lastTimestamp(c.ID)
		list = append(list, &c)
	}
	b.cmu.Lock()
	b.contacts = list
	b.byKey = make(map[string]*proto.Contact, len(list))
	for _, c := range list {
		b.byKey[c.CacheKey()] = c
	}
	b.cmu.Unlock()
}

// ListContacts returns the known contacts.
func (b *Backend) ListContacts(ctx context.Context) ([]proto.Contact, error) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	out := make([]proto.Contact, len(b.contacts))
	for i, c := range b.contacts {
		out[i] = cloneContact(*c)
	}
	return out, nil
}

// FindContact looks a contact up in memory (find_contact; zero network).
func (b *Backend) FindContact(contactID string) (proto.Contact, bool) {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	for _, c := range b.contacts {
		if c.ID == contactID {
			return cloneContact(*c), true
		}
	}
	return proto.Contact{}, false
}

// RegisterContact makes a contact (open-or-create ghost) known; idempotent by
// cache key. It reports whether the contact was added.
func (b *Backend) RegisterContact(c proto.Contact) bool {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	for _, e := range b.contacts {
		if e.CacheKey() == c.CacheKey() {
			return false
		}
	}
	cc := cloneContact(c)
	b.contacts = append(b.contacts, &cc)
	b.byKey[cc.CacheKey()] = &cc
	return true
}

// ListAddressBook returns the Signal address book: the loaded contacts
// enriched with phone / address_book / is_chat_active, cached for the
// configured TTL. It never fails.
func (b *Backend) ListAddressBook(ctx context.Context, force bool) ([]proto.Contact, error) {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	ttl := time.Duration(b.cfg.AddressBookTTLS()) * time.Second
	now := b.clock()
	if !force && b.addressBookS && now.Sub(b.addressBookT) < ttl {
		return cloneContacts(b.addressBook), nil
	}
	res := make([]proto.Contact, 0, len(b.contacts))
	for _, c := range b.contacts {
		cc := cloneContact(*c)
		var phone strings.Builder
		for _, r := range c.ID {
			if unicode.IsDigit(r) {
				phone.WriteRune(r)
			}
		}
		cc.Extras["phone"] = phone.String()
		cc.Extras["address_book"] = true
		cc.Extras["is_chat_active"] = c.LastMessageTS() > 0
		res = append(res, cc)
	}
	b.addressBook, b.addressBookT, b.addressBookS = res, now, true
	return cloneContacts(res), nil
}

func cloneContacts(in []proto.Contact) []proto.Contact {
	out := make([]proto.Contact, len(in))
	for i, c := range in {
		out[i] = cloneContact(c)
	}
	return out
}

// RefreshContacts re-fetches listContacts over RPC (never the subprocess) and
// merges the result in place. Remote failures go to Errors with nothing
// committed; new/renamed contacts also emit a contact_update event.
func (b *Backend) RefreshContacts(ctx context.Context, force bool) proto.AddressBookRefreshResult {
	res := proto.AddressBookRefreshResult{Protocol: proto.ProtocolSignal}
	if !b.useDaemon.Load() {
		res.Errors = "daemon not running"
		return res
	}
	raw := b.rpc.Call(ctx, "listContacts", nil)
	if !raw.OK() {
		res.Errors = raw.Err
		return res
	}
	if _, ok := raw.Result.([]any); !ok {
		res.Errors = "unexpected RPC result"
		return res
	}
	var fresh []proto.Contact
	for _, d := range asMapList(raw.Result) {
		fresh = append(fresh, parseContactDict(d))
	}
	added, renamed := b.mergeContacts(fresh)
	b.cmu.Lock()
	b.addressBookS = false
	b.cmu.Unlock()
	for _, c := range append(append([]proto.Contact{}, added...), renamed...) {
		b.queue.put(contactUpdateEvent(c))
	}
	res.NewContacts, res.RenamedContacts = added, renamed
	return res
}

func contactUpdateEvent(c proto.Contact) proto.Event {
	var phone any
	if p, ok := c.Extras["phone"]; ok {
		phone = p
	}
	return proto.Event{Type: proto.EventContactUpdate, Protocol: proto.ProtocolSignal, ContactID: c.ID,
		Payload: map[string]any{"display_name": c.DisplayName, "phone": phone, "contact": c}}
}

// mergeContacts merges fresh into the list preserving the existing entries
// (ghosts survive, contacts gone from the book are dropped).
func (b *Backend) mergeContacts(fresh []proto.Contact) (added, renamed []proto.Contact) {
	freshByKey := map[string]proto.Contact{}
	for _, f := range fresh {
		freshByKey[f.CacheKey()] = f
	}
	b.cmu.Lock()
	defer b.cmu.Unlock()
	keep := map[string]bool{}
	for _, c := range b.contacts {
		f, ok := freshByKey[c.CacheKey()]
		if !ok {
			if truthy(c.Extras["ghost"]) {
				keep[c.CacheKey()] = true
			}
			continue
		}
		delete(freshByKey, c.CacheKey())
		if f.DisplayName != c.DisplayName {
			c.DisplayName = f.DisplayName
			renamed = append(renamed, cloneContact(*c))
		}
		if c.Extras == nil {
			c.Extras = map[string]any{}
		}
		if v, ok := f.Extras["aci"]; ok {
			c.Extras["aci"] = v
		} else {
			c.Extras["aci"] = ""
		}
		if v, ok := f.Extras["number"]; ok {
			c.Extras["number"] = v
		} else {
			c.Extras["number"] = c.ID
		}
		keep[c.CacheKey()] = true
	}
	var newPtrs []*proto.Contact
	for _, f := range fresh {
		if keep[f.CacheKey()] {
			continue
		}
		fc := cloneContact(f)
		fc.Extras["last_message_ts"] = b.lastTimestamp(fc.ID)
		newPtrs = append(newPtrs, &fc)
		added = append(added, cloneContact(fc))
	}
	var list []*proto.Contact
	for _, c := range b.contacts {
		if keep[c.CacheKey()] {
			list = append(list, c)
		}
	}
	list = append(list, newPtrs...)
	b.contacts = list
	b.byKey = make(map[string]*proto.Contact, len(list))
	for _, c := range list {
		b.byKey[c.CacheKey()] = c
	}
	return added, renamed
}

// ---- cache -------------------------------------------------------------------------------------

func (b *Backend) loadProtocolCache(ctx context.Context) {
	if b.st == nil {
		return
	}
	loaded, err := b.st.LoadCache(ctx, proto.ProtocolSignal)
	if err != nil {
		b.log.Warn("signal cache load failed", "err", err)
		return
	}
	b.ingestMu.Lock()
	defer b.ingestMu.Unlock()
	for contactID, msgs := range loaded {
		type ident struct {
			ts   int64
			text string
			mine bool
		}
		have := map[ident]bool{}
		for _, m := range b.cache[contactID] {
			have[ident{m.Timestamp, m.Text, m.IsMine}] = true
		}
		for _, m := range msgs {
			e := entryFromStore(m)
			id := ident{e.Timestamp, e.Text, e.IsMine}
			if !have[id] {
				b.cache[contactID] = append(b.cache[contactID], e)
				have[id] = true
			}
		}
	}
}

// ---- misc ----------------------------------------------------------------------------------------

// MarkRead marks every stored message of the contact as read.
func (b *Backend) MarkRead(ctx context.Context, contactID string) error {
	if b.st == nil {
		return nil
	}
	return b.st.MarkAsRead(ctx, proto.ProtocolSignal, contactID)
}
