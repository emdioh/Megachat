package transcribe

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Store is the slice of internal/store used here. *store.Store implements it.
type Store interface {
	GetTranscription(ctx context.Context, protocol, attachmentID string) (*store.Transcription, error)
	SetTranscription(ctx context.Context, t store.Transcription) error
	RecentTranscriptions(ctx context.Context, limit int) ([]store.Transcription, error)
}

// Config is the slice of *config.Config used here.
type Config interface {
	TranscriptionProvider() string
	APIKey(provider string) (string, error)
	ProviderModel(provider string) (string, error)
	ProviderBaseURL(provider string) (string, error)
	OpenRouterHeaders() map[string]string
	TranscriptionLanguage() string
	TranscriptionTimeoutS() int
	TranscriptionEnvOverrides() []string
	SetAPIKey(provider, apiKey string) error
	SetTranscriptionSettings(values map[string]any) error

	AutoTranscribeGlobal(protocol string) bool
	AutoTranscribeGlobals() map[string]bool
	AutoTranscribeChat(protocol, contactID string) *bool
	AutoTranscribeEffective(protocol, contactID string) bool
	SetAutoTranscribeGlobal(protocol string, enabled bool) error
	SetAutoTranscribeChat(protocol, contactID string, enabled *bool) error
}

// Bus is what the auto-transcriber needs: *bus.Bus implements it. Optional
// (nil disables automatic transcription and the "!" commands).
type Bus interface {
	Subscribe(opts ...bus.SubOption) *bus.Subscription
}

// Media resolves an attachment to a local file (confined to the protocol's
// media root, may download lazily). *media.Deps implements it.
type Media interface {
	AttachmentBlob(ctx context.Context, protocol, attachmentID string) (api.BlobOut, error)
}

// QuotedMessage is the stored row a "!t" reply quotes.
type QuotedMessage struct {
	MsgID        string
	Timestamp    int64
	MediaKind    string
	AttachmentID string
	IsMine       bool
}

// QuotedFinder looks up the message a command replies to (Python
// _find_quoted: msg_id match or "%_<id>" suffix match for replyToMessageID,
// else timestamp match, over the chat's storage keys, preferring voice/audio
// rows). Optional: without it "!t" answers with the usage hint.
type QuotedFinder interface {
	FindQuoted(ctx context.Context, protocol, contactID, replyToMessageID string, quoteTimestamp int64) (*QuotedMessage, error)
}

// Deps are the collaborators of New / RegisterOps.
type Deps struct {
	Store  Store
	Config Config
	Bus    Bus   // optional
	Media  Media // optional only for tests that never submit
	// Dispatcher runs messages.send for the auto-transcriber. Optional:
	// RegisterOps defaults it to the registry it registers into.
	Dispatcher ops.Dispatcher
	Quoted     QuotedFinder // optional
	Logger     *slog.Logger // optional

	// Service is the instance the ops operate on. Create it with New and pass
	// it here; RegisterOps builds an unstarted one when nil.
	Service *Service

	// Now overrides the clock (tests).
	Now func() time.Time
	// Inline runs auto-transcriber jobs on the calling goroutine (tests).
	Inline bool
}

func (d Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// DoneFunc is called by the worker after a record turned "ok" or "failed".
type DoneFunc func(protocol, attachmentID string, row store.Transcription)

type job struct{ protocol, attachmentID, path string }

// worker is Python's TranscriptionService: one goroutine draining a queue.
type worker struct {
	client Client
	svc    *Service

	mu      sync.Mutex // guards queue, stopped, submit sequence
	cond    *sync.Cond
	queue   []job
	stopped bool
	done    chan struct{}
}

func newWorker(svc *Service, client Client) *worker {
	w := &worker{client: client, svc: svc, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

func (w *worker) submit(ctx context.Context, protocol, attachmentID, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cur, err := w.svc.deps.Store.GetTranscription(ctx, protocol, attachmentID)
	if err != nil {
		return err
	}
	if cur != nil && (cur.Status == "ok" || cur.Status == "pending") {
		return nil
	}
	if err := w.svc.deps.Store.SetTranscription(ctx, store.Transcription{
		Protocol: protocol, AttachmentID: attachmentID, Status: "pending"}); err != nil {
		return err
	}
	w.queue = append(w.queue, job{protocol, attachmentID, path})
	w.cond.Signal()
	return nil
}

func (w *worker) stop() {
	w.mu.Lock()
	w.stopped = true
	w.cond.Broadcast()
	w.mu.Unlock()
}

func (w *worker) queueSize() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queue)
}

func (w *worker) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.stopped {
			w.cond.Wait()
		}
		if w.stopped {
			w.mu.Unlock()
			return
		}
		j := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		w.process(j)
	}
}

func (w *worker) process(j job) {
	log := w.svc.deps.log()
	ctx := context.Background()
	log.Info("Trascrizione avviata", "protocol", j.protocol, "attachment_id", j.attachmentID,
		"client", w.client.Kind(), "model", w.client.Model(), "file", j.path)
	text, err := w.client.Transcribe(ctx, j.path)
	st := w.svc.deps.Store
	if err != nil {
		msg := err.Error()
		log.Error("Trascrizione fallita", "protocol", j.protocol, "attachment_id", j.attachmentID, "error", msg)
		if serr := st.SetTranscription(ctx, store.Transcription{
			Protocol: j.protocol, AttachmentID: j.attachmentID, Status: "failed", Error: &msg}); serr != nil {
			log.Error("Trascrizione: salvataggio fallito", "error", serr)
		}
	} else {
		model := w.client.Model()
		if serr := st.SetTranscription(ctx, store.Transcription{
			Protocol: j.protocol, AttachmentID: j.attachmentID, Status: "ok", Text: &text, Model: &model}); serr != nil {
			// Python: a store failure inside the try block marks the record failed.
			msg := serr.Error()
			_ = st.SetTranscription(ctx, store.Transcription{
				Protocol: j.protocol, AttachmentID: j.attachmentID, Status: "failed", Error: &msg})
		} else {
			log.Info("Trascrizione completata", "protocol", j.protocol,
				"attachment_id", j.attachmentID, "caratteri", len([]rune(text)))
		}
	}
	w.svc.notifyDone(j.protocol, j.attachmentID)
}

// Service is the transcription service host (Python TranscriptionHost): it
// owns the current queue worker (absent while the selected provider has no API
// key) and the auto-transcriber, and rebuilds the worker when settings change.
type Service struct {
	deps Deps

	mu     sync.Mutex
	w      *worker
	auto   *AutoTranscriber
	disp   ops.Dispatcher
	onDone atomic.Pointer[DoneFunc]
}

// SetDispatcher sets the dispatcher used for messages.send when Deps.Dispatcher
// was nil (RegisterOps passes its registry).
func (s *Service) SetDispatcher(d ops.Dispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disp == nil {
		s.disp = d
	}
}

func (s *Service) dispatcher() ops.Dispatcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disp
}

// New builds a Service. Call Start to create the worker and listen to the bus.
func New(deps Deps) *Service {
	return &Service{deps: deps, disp: deps.Dispatcher}
}

// Start builds the worker from the current configuration and, when a Bus is
// set, starts the auto-transcriber. Idempotent.
func (s *Service) Start() error {
	err := s.Reinit()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deps.Bus != nil && s.auto == nil {
		s.auto = newAutoTranscriber(s)
		s.auto.start()
	}
	return err
}

// Stop stops the auto-transcriber and the worker (in-flight work finishes in
// the background). Safe to call repeatedly.
func (s *Service) Stop() {
	s.mu.Lock()
	auto, w := s.auto, s.w
	s.auto, s.w = nil, nil
	s.mu.Unlock()
	if auto != nil {
		auto.stop()
	}
	if w != nil {
		w.stop()
	}
}

// Wait blocks until the stopped worker (if any) has exited, up to d.
func (s *Service) wait(w *worker, d time.Duration) {
	select {
	case <-w.done:
	case <-time.After(d):
	}
}

// Reinit stops the current worker and rebuilds it from the configuration
// (a changed key or setting replaces the client). The done hook survives.
func (s *Service) Reinit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		s.w.stop()
		s.w = nil
	}
	cfg := s.deps.Config
	provider := cfg.TranscriptionProvider()
	key, err := cfg.APIKey(provider)
	if err != nil {
		return err
	}
	if key == "" {
		s.deps.log().Info("Trascrizione cloud disattivata (provider senza chiave)", "provider", provider)
		return nil
	}
	model, err := cfg.ProviderModel(provider)
	if err != nil {
		return err
	}
	baseURL, err := cfg.ProviderBaseURL(provider)
	if err != nil {
		return err
	}
	var headers map[string]string
	if provider == "openrouter" {
		headers = cfg.OpenRouterHeaders()
	}
	client, err := BuildClient(provider, key, baseURL, model, cfg.TranscriptionLanguage(),
		cfg.TranscriptionTimeoutS(), headers)
	if err != nil {
		return err
	}
	s.w = newWorker(s, client)
	s.deps.log().Info("Trascrizione cloud attiva", "provider", provider)
	return nil
}

func (s *Service) worker() *worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w
}

// Active reports whether a worker exists (the selected provider has a key).
func (s *Service) Active() bool { return s.worker() != nil }

// SetOnDone installs (nil removes) the completion hook.
func (s *Service) SetOnDone(f DoneFunc) {
	if f == nil {
		s.onDone.Store(nil)
		return
	}
	s.onDone.Store(&f)
}

func (s *Service) hasOnDone() bool { return s.onDone.Load() != nil }

func (s *Service) notifyDone(protocol, attachmentID string) {
	f := s.onDone.Load()
	if f == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.deps.log().Error("Callback di trascrizione fallita", "protocol", protocol,
				"attachment_id", attachmentID, "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	row, err := s.deps.Store.GetTranscription(ctx, protocol, attachmentID)
	if err != nil || row == nil {
		return
	}
	(*f)(protocol, attachmentID, *row)
}

// Status returns the stored record, nil when unknown.
func (s *Service) Status(ctx context.Context, protocol, attachmentID string) (*store.Transcription, error) {
	return s.deps.Store.GetTranscription(ctx, protocol, attachmentID)
}

// Submit queues the file unless the record is already ok or pending.
func (s *Service) Submit(ctx context.Context, protocol, attachmentID, path string) error {
	w := s.worker()
	if w == nil {
		return errNotConfigured
	}
	return w.submit(ctx, protocol, attachmentID, path)
}
