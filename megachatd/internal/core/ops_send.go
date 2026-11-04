package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Port of server/ops/send.py: messages.send and messages.send.check.

// prepared is a fully validated send request.
type prepared struct {
	protocol, contactID, text, clientMsgID string

	quoteTimestamp                                             *int64
	quoteAuthor, quoteMessage, replyTo, quoteCType, quoteAttID *string
	quoteAttachments                                           []string
	hasQuoteAttachments                                        bool

	backend proto.Backend
	contact proto.Contact
}

func newUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// rawOptString decodes an Any field that must be str|None: ok=false for any
// other JSON type. null/absent -> nil.
func rawOptString(raw []byte) (*string, bool) {
	if api.RawIsNull(raw) {
		return nil, true
	}
	s, ok := api.RawString(raw)
	if !ok {
		return nil, false
	}
	return &s, true
}

func stripToNil(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	if s == "" {
		return nil
	}
	return &s
}

// prepare is send._prepare: cheap validation in the legacy precedence order,
// no side effects.
func (c *Core) prepare(ctx context.Context, req api.MessagesSendRequest, attachmentCount int, slotsOK bool) (*prepared, error) {
	optimistic := req.Optimistic != nil
	if req.Protocol == nil || !api.IsProtocol(*req.Protocol) {
		return nil, invalidRequest()
	}
	protocol := *req.Protocol
	if req.ContactID == nil || strings.TrimSpace(*req.ContactID) == "" {
		return nil, invalidRequest()
	}
	contactID := *req.ContactID
	if req.Text == nil || runeLen(*req.Text) > api.MaxTextLength {
		return nil, invalidRequest()
	}
	text := *req.Text
	if attachmentCount == 0 && strings.TrimSpace(text) == "" {
		return nil, invalidRequest()
	}
	if attachmentCount > api.MaxAttachments {
		return nil, &ops.Error{Code: api.CodeInvalidRequest, Message: "Too many attachments"}
	}
	if !slotsOK {
		return nil, invalidRequest()
	}
	if optimistic && attachmentCount > 0 {
		return nil, invalidRequest()
	}

	clientMsgID := newUUID4()
	if s, ok := api.RawString(req.ClientMsgID); ok && strings.TrimSpace(s) != "" && runeLen(s) <= 128 {
		clientMsgID = strings.TrimSpace(s)
	}

	p := &prepared{protocol: protocol, contactID: contactID, text: text, clientMsgID: clientMsgID}

	var ok bool
	if p.quoteAuthor, ok = rawOptString(req.QuoteAuthor); !ok {
		return nil, invalidRequest()
	}
	if p.quoteMessage, ok = rawOptString(req.QuoteMessage); !ok {
		return nil, invalidRequest()
	}
	if p.replyTo, ok = rawOptString(req.ReplyToMessageID); !ok {
		return nil, invalidRequest()
	}
	ct, ok1 := rawOptString(req.QuoteContentType)
	ai, ok2 := rawOptString(req.QuoteAttachmentID)
	if !ok1 || !ok2 {
		return nil, invalidRequest()
	}
	if protocol == api.ProtocolSignal {
		p.quoteCType, p.quoteAttID = ct, ai
	}
	p.quoteAuthor = stripToNil(p.quoteAuthor)
	p.quoteCType = stripToNil(p.quoteCType)
	p.quoteAttID = stripToNil(p.quoteAttID)
	if p.quoteMessage != nil && !optimistic {
		stripped := strings.TrimSpace(*p.quoteMessage)
		if stripped != "" || p.quoteCType != nil {
			p.quoteMessage = &stripped
		} else {
			p.quoteMessage = nil
		}
	}
	p.replyTo = stripToNil(p.replyTo)
	if !api.RawIsNull(req.QuoteTimestamp) {
		n, ok := api.RawInt(req.QuoteTimestamp)
		if !ok {
			return nil, invalidRequest()
		}
		p.quoteTimestamp = &n
	}
	isReply := p.quoteTimestamp != nil || p.quoteAuthor != nil || p.quoteMessage != nil ||
		p.replyTo != nil || p.quoteCType != nil || p.quoteAttID != nil
	if protocol == api.ProtocolTelegram && isReply {
		if p.replyTo == nil {
			return nil, invalidRequest()
		}
		if n, err := strconv.Atoi(*p.replyTo); err != nil || n <= 0 {
			return nil, invalidRequest()
		}
	}
	if protocol == api.ProtocolWhatsApp && isReply && p.replyTo == nil {
		return nil, invalidRequest()
	}

	backend, found := c.mgr.Get(protocol)
	if !found {
		return nil, &ops.Error{Code: api.CodeNotFound, Message: "Not Found"}
	}
	p.backend = backend
	contact, found := c.FindContact(ctx, protocol, contactID)
	if !found {
		return nil, &ops.Error{Code: api.CodeNotFound, Message: "Not Found"}
	}
	p.contact = contact

	if protocol == api.ProtocolSignal && p.quoteAttID != nil {
		rows, err := c.st.ListMessages(ctx, store.MessageQuery{Protocol: protocol, Keys: []string{contactID}})
		if err != nil {
			return nil, &ops.Error{Code: api.CodeInternal, Message: "Database error"}
		}
		known := false
		for _, m := range rows {
			if m.AttachmentID != nil && *m.AttachmentID == *p.quoteAttID {
				known = true
				break
			}
		}
		if !known {
			return nil, invalidRequest()
		}
	}
	if protocol == api.ProtocolSignal && p.quoteCType != nil {
		if p.quoteAttID == nil {
			if !optimistic {
				return nil, invalidRequest()
			}
			// A reply to media whose file id is unknown keeps its type only.
			p.quoteAttachments, p.hasQuoteAttachments = []string{*p.quoteCType}, true
		} else {
			if path, ok := c.mgr.AttachmentPath(ctx, protocol, *p.quoteAttID); ok && path != "" {
				p.quoteAttachments = []string{fmt.Sprintf("%s:%s:%s", *p.quoteCType, filepath.Base(path), path)}
			} else {
				p.quoteAttachments = []string{*p.quoteCType}
			}
			p.hasQuoteAttachments = true
		}
	}
	return p, nil
}

// quote builds the proto.Quote (Python kwargs).
func (p *prepared) quote(optimistic bool) proto.Quote {
	q := proto.Quote{}
	if p.quoteTimestamp != nil {
		q.Timestamp = *p.quoteTimestamp
	}
	if p.quoteAuthor != nil {
		q.Author = *p.quoteAuthor
	}
	if p.quoteMessage != nil {
		q.Message = *p.quoteMessage
	}
	if p.protocol == api.ProtocolSignal && p.hasQuoteAttachments {
		q.Attachments = p.quoteAttachments
	}
	if (p.protocol == api.ProtocolWhatsApp || p.protocol == api.ProtocolTelegram || optimistic) && p.replyTo != nil {
		q.ReplyToMessageID = *p.replyTo
	}
	return q
}

func (c *Core) opSendCheck(ctx context.Context, req api.MessagesSendCheckRequest) (api.OkOut, error) {
	if _, err := c.prepare(ctx, req.MessagesSendRequest, req.AttachmentCount, req.SlotsOK); err != nil {
		return api.OkOut{}, err
	}
	return api.OkOut{OK: true}, nil
}

func (c *Core) validateAttachments(atts []api.AttachmentIn) ([]Upload, error) {
	var out []Upload
	for _, a := range atts {
		if a.Rejected != nil {
			return nil, &ops.Error{Code: api.CodeTooLarge, Message: "Upload too large"}
		}
		var fn, ct string
		if a.Filename != nil {
			fn = *a.Filename
		}
		if a.ContentType != nil {
			ct = *a.ContentType
		}
		u, err := c.validator(a.Path, fn, ct)
		if err != nil {
			var ue *UploadError
			if errors.As(err, &ue) {
				if ue.Status == 413 {
					return nil, &ops.Error{Code: api.CodeTooLarge, Message: "Upload too large"}
				}
				return nil, &ops.Error{Code: api.CodeInvalidRequest, Message: "Unsupported media type"}
			}
			return nil, invalidRequest()
		}
		out = append(out, u)
	}
	return out, nil
}

// opSend is messages.send. It returns MessagesSendOut, MessagesSendDuplicateOut
// or MessagesSendOptimisticOut. It is never cancelled with the caller.
func (c *Core) opSend(ctx context.Context, req api.MessagesSendRequest) (any, error) {
	ctx = context.WithoutCancel(ctx)
	atts := req.Attachments
	opt := req.Optimistic
	slotsOK := true
	for _, a := range atts {
		if a.Path == "" && a.Rejected == nil {
			slotsOK = false
		}
	}
	p, err := c.prepare(ctx, req, len(atts), slotsOK)
	if err != nil {
		return nil, err
	}
	protocol, contactID, text := p.protocol, p.contactID, p.text
	contact := p.contact

	// A book-only contact is registered before the send so later events
	// resolve it; a ghost already registered re-tries its alias.
	if !c.KnownContact(ctx, protocol, contact.ID) {
		contact = contact.WithExtra("ghost", true)
		c.RegisterContact(contact)
	} else if asBool(contact.Extras["ghost"]) {
		c.RegisterContact(contact)
	}

	// Idempotency (text branch only).
	var (
		regKey   SendKey
		regToken string
	)
	if len(atts) == 0 {
		regKey = SendKey{protocol, contactID, p.clientMsgID}
		fp := SendFingerprint{
			Protocol: protocol, ContactID: contactID, Text: text, QuoteTimestamp: p.quoteTimestamp,
			QuoteAuthor: p.quoteAuthor, QuoteMessage: p.quoteMessage, ReplyToMessageID: p.replyTo,
			QuoteContentType: p.quoteCType, QuoteAttachmentID: p.quoteAttID,
		}.Build()
		outcome, entry, token := c.sendReg.ClaimOrLookup(regKey, fp)
		switch outcome {
		case OutcomeSent:
			if entry.Fingerprint != fp {
				return nil, &ops.Error{Code: api.CodeUnprocessable, Message: "Client message id conflict"}
			}
			return api.MessagesSendDuplicateOut{OK: true, Duplicate: true}, nil
		case OutcomeInflight:
			return nil, &ops.Error{Code: api.CodeConflict, Message: "Send in progress"}
		}
		regToken = token
	}

	var (
		succeeded  bool
		messageID  string
		failure    *ops.Error
		lastError  *string
		rowPending bool
	)
	setLast := func(err error) {
		s := SafeErrorText(err, 200)
		lastError = &s
	}
	quote := p.quote(opt != nil)

	func() {
		if opt != nil {
			rowPending = !opt.Resend
			if e := c.beginOptimistic(ctx, p, opt); e != nil {
				failure = e
				return
			}
			rowPending = true
		}
		if len(atts) > 0 {
			uploads, e := c.validateAttachments(atts)
			if e != nil {
				failure = ops.AsError(e)
				return
			}
			batchID := ""
			if s, ok := api.RawString(req.BatchID); ok && strings.TrimSpace(s) != "" {
				batchID = s
			}
			pa := make([]proto.Attachment, len(uploads))
			for i, u := range uploads {
				pa[i] = proto.Attachment{Path: u.Path, MimeType: u.MimeType, MediaKind: u.MediaKind, Filename: u.Filename}
			}
			pa[0].Caption = text
			if _, err := c.mgr.SendAttachments(ctx, protocol, contactID, pa, batchID, quote); err != nil {
				setLast(err)
				if errors.Is(err, proto.ErrUnsupported) {
					failure = &ops.Error{Code: api.CodeNotSupported, Message: "Attachment send not supported"}
				} else {
					c.log.Error("Web send failed", "protocol", protocol, "contact", contactID, "err", err)
					failure = &ops.Error{Code: api.CodeBackendError, Message: "Message send failed"}
				}
				return
			}
			succeeded = true
			return
		}
		maxAttempts := 1
		if req.Retry {
			maxAttempts = SendRetryMaxAttempts
		}
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			started := time.Now()
			var id string
			var err error
			if opt == nil {
				id, err = c.mgr.SendMessage(ctx, protocol, contactID, text, quote)
			} else {
				// Straight to the backend: no sent_mirror, the pending row
				// already is the local copy.
				id, err = p.backend.SendMessage(ctx, contactID, text, quote)
			}
			if err == nil {
				c.logSendDuration(protocol, contactID, started)
				messageID = id
				succeeded = true
				return
			}
			setLast(err)
			if ClassifySendError(protocol, err) == Terminal {
				c.log.Error("Terminal send error, no retry", "protocol", protocol, "contact", contactID, "err", err)
				return
			}
			if attempt == maxAttempts {
				c.log.Error("Send failed after attempts", "attempts", maxAttempts, "protocol", protocol, "contact", contactID, "err", err)
				return
			}
			delay := ComputeDelay(attempt, c.rng)
			c.log.Info("Send retry", "attempt", attempt+1, "max", SendRetryMaxAttempts, "delay", delay.String(), "protocol", protocol, "contact", contactID, "err", err)
			c.PublishWebPush("send_retry", map[string]any{
				"client_msg_id": p.clientMsgID, "protocol": protocol, "contact_id": contactID,
				"attempt": attempt + 1, "max_attempts": SendRetryMaxAttempts, "error": SafeErrorText(err, 200),
			})
			if err := c.sleep(ctx, delay); err != nil {
				return
			}
		}
	}()

	// mark_sent only on success; release in every other case, token-bound.
	if regToken != "" {
		if succeeded && failure == nil {
			var mid *string
			if messageID != "" {
				mid = &messageID
			}
			now := time.Now().Unix()
			c.sendReg.MarkSent(regKey, regToken, mid, &now)
		} else {
			c.sendReg.Release(regKey, regToken)
		}
	}
	if failure == nil && !succeeded {
		failure = &ops.Error{Code: api.CodeBackendError, Message: "Message send failed"}
	}
	if failure != nil {
		if opt != nil && rowPending {
			applied := c.transitionOutgoingStatus(ctx, protocol, contactID, opt.Timestamp, text, api.StatusFailed, []string{api.StatusPending})
			failure.Details = map[string]any{"status_applied": applied, "error": ptrOrNil(lastError)}
		}
		return nil, failure
	}
	if opt != nil {
		return c.finishOptimistic(ctx, p, opt, messageID), nil
	}
	c.PublishWebPush("message", map[string]any{"protocol": protocol, "contact_id": contactID, "is_mine": true})
	return api.MessagesSendOut{OK: true}, nil
}

func ptrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func (c *Core) logSendDuration(protocol, contactID string, started time.Time) {
	if d := time.Since(started); d >= time.Second {
		c.log.Warn("send_message slow", "protocol", protocol, "contact", contactID, "ms", d.Milliseconds())
	} else {
		c.log.Debug("send_message took", "ms", d.Milliseconds(), "protocol", protocol)
	}
}

// transitionOutgoingStatus ports server/outgoing.py: advance one optimistic
// outgoing row (by timestamp+text, falling back to the latest row with that
// text, because the echo may already have replaced the timestamp).
func (c *Core) transitionOutgoingStatus(ctx context.Context, protocol, contactID string, ts int64, text, status string, expected []string) bool {
	ok, err := c.st.UpdateMessageStatus(ctx, ts, status, protocol, contactID, &text, expected)
	if err == nil && !ok {
		ok, err = c.st.UpdateMessageStatusByText(ctx, text, status, protocol, contactID, expected)
	}
	if err != nil || !ok {
		c.log.Warn("Outgoing status transition failed", "protocol", protocol, "contact", contactID, "ts", ts, "status", status, "err", err)
		return false
	}
	return true
}

// beginOptimistic seeds + persists the pending row (or re-arms a failed one)
// before the network send.
func (c *Core) beginOptimistic(ctx context.Context, p *prepared, opt *api.OptimisticSend) *ops.Error {
	ts := opt.Timestamp
	c.bumpRecency(p.contact.CacheKey(), ts)
	if opt.Resend {
		if !c.transitionOutgoingStatus(ctx, p.protocol, p.contactID, ts, p.text, api.StatusPending, []string{api.StatusFailed}) {
			return &ops.Error{Code: api.CodeConflict, Message: "Message is not in a failed state",
				Details: map[string]any{"status_applied": false, "error": nil}}
		}
		return nil
	}
	nm := store.NewMessage{
		Protocol: p.protocol, ContactNumber: p.contactID, Text: p.text, IsMine: true, Sender: "You",
		Timestamp: ts, MsgType: api.MsgTypeText, Status: api.StatusPending,
		QuoteText: opt.QuoteText, AttachmentID: opt.AttachmentID, ContentType: opt.ContentType,
		QuoteTimestamp: p.quoteTimestamp, QuoteAuthor: p.quoteAuthor, ReplyToMessageID: p.replyTo,
		QuoteAttachmentID: opt.QuoteAttachmentID, QuoteAttachmentPath: opt.QuoteAttachmentPath,
		QuoteContentType: opt.QuoteContentType,
	}
	// An id-less outgoing row with the same text inside the send-dedup window
	// is the same message: nothing is added or persisted (the contact is
	// promoted anyway).
	if c.cachedOptimisticTwin(ctx, p, ts) {
		c.log.Debug("optimistic ingest: already cached", "protocol", p.protocol, "contact", p.contactID, "ts", ts)
		return nil
	}
	// SQLite write BEFORE the network send, so the echo always finds the row.
	_, added, err := c.st.AddMessage(ctx, nm)
	if err != nil {
		c.log.Error("optimistic persist failed", "err", err)
		return &ops.Error{Code: api.CodeBackendError, Message: "Message send failed"}
	}
	c.log.Debug("optimistic ingest", "added", added, "protocol", p.protocol, "contact", p.contactID, "ts", ts)
	if added {
		c.PublishWebPush("message", map[string]any{
			"id": nil, "protocol": p.protocol, "contact_id": p.contactID, "timestamp": ts, "is_mine": true,
		})
	}
	return nil
}

// finishOptimistic moves pending -> sent and attaches the server id.
func (c *Core) finishOptimistic(ctx context.Context, p *prepared, opt *api.OptimisticSend, messageID string) api.MessagesSendOptimisticOut {
	applied := c.transitionOutgoingStatus(ctx, p.protocol, p.contactID, opt.Timestamp, p.text, api.StatusSent, []string{api.StatusPending})
	var attached *string
	if messageID != "" {
		// Attach the real id to the optimistic row so the echo matches by id
		// and never rewrites the optimistic timestamp.
		if _, err := c.attachMessageID(ctx, p.protocol, p.contactID, p.text, opt.Timestamp, messageID); err != nil {
			c.log.Error("Attaching the server id failed", "protocol", p.protocol, "contact", p.contactID, "err", err)
		} else {
			attached = &messageID
		}
	}
	return api.MessagesSendOptimisticOut{OK: true, Status: api.StatusSent, StatusApplied: applied, MessageID: attached}
}

// SendDedupWindowMs is _SEND_DEDUP_WINDOW_MS.
const SendDedupWindowMs int64 = 5000

func (c *Core) cachedOptimisticTwin(ctx context.Context, p *prepared, ts int64) bool {
	since := ts - SendDedupWindowMs
	rows, err := c.st.ListMessages(ctx, store.MessageQuery{Protocol: p.protocol, Keys: []string{p.contactID}, Since: &since})
	if err != nil {
		return false
	}
	for _, m := range rows {
		d := m.Timestamp - ts
		if d < 0 {
			d = -d
		}
		if m.IsMine && store.Deref(m.Text) == p.text && store.Deref(m.MsgID) == "" && d <= SendDedupWindowMs {
			return true
		}
	}
	return false
}
