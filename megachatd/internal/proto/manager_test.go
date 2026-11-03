package proto_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/fake"
)

func ctx5(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// minimal implements only the core Backend interface.
type minimal struct{ proto string }

func (m minimal) Protocol() string                 { return m.proto }
func (m minimal) Connect(context.Context) error    { return nil }
func (m minimal) Disconnect(context.Context) error { return nil }
func (m minimal) IsConnected() bool                { return true }
func (m minimal) ListContacts(context.Context) ([]proto.Contact, error) {
	return []proto.Contact{{ID: "m1", DisplayName: "M", Protocol: m.proto}}, nil
}
func (m minimal) SendMessage(context.Context, string, string, proto.Quote) (string, error) {
	return "1", nil
}
func (m minimal) MarkRead(context.Context, string) error { return nil }
func (m minimal) Receive(ctx context.Context) (<-chan proto.Event, error) {
	ch := make(chan proto.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func TestRegistry(t *testing.T) {
	m := proto.New()
	if err := m.Register(minimal{}); err == nil {
		t.Fatal("empty protocol must be rejected")
	}
	a, b := fake.New("signal"), fake.New("telegram")
	m.Register(a)
	m.Register(b)
	m.Register(fake.New("signal")) // replaces, keeps order
	if got := m.Protocols(); !slices.Equal(got, []string{"signal", "telegram"}) {
		t.Fatal(got)
	}
	if g, _ := m.Get("signal"); g == proto.Backend(a) {
		t.Fatal("expected replacement")
	}
	if _, ok := m.Get("nope"); ok || len(m.All()) != 2 {
		t.Fatal("get/all")
	}
	_, err := m.SendMessage(ctx5(t), "nope", "c", "hi", proto.Quote{})
	if !errors.Is(err, proto.ErrNoBackend) {
		t.Fatal(err)
	}
}

func TestSendRoutesAndMirrors(t *testing.T) {
	m := proto.New()
	sig, tg := fake.New("signal"), fake.New("telegram")
	m.Register(sig)
	m.Register(tg)
	id, err := m.SendMessage(ctx5(t), "telegram", "42", "hello", proto.Quote{Timestamp: 5, Author: "a", Message: "q"})
	if err != nil || id == "" {
		t.Fatal(err)
	}
	if len(tg.CallsTo("SendMessage")) != 1 || len(sig.CallsTo("SendMessage")) != 0 {
		t.Fatal("routing")
	}
	mirrors := tg.CallsTo("EnqueueSentMessage")
	if len(mirrors) != 1 || mirrors[0].Args["message"].(proto.SentMessage).MessageID != id {
		t.Fatalf("mirror: %+v", mirrors)
	}
	// Quote-media fallback is Signal only.
	m.SendMessage(ctx5(t), "signal", "1", "x", proto.Quote{Attachments: []string{"image/png|a.png"}})
	sm := sig.CallsTo("EnqueueSentMessage")[0].Args["message"].(proto.SentMessage)
	if sm.QuoteContentType != "image/png" || sm.QuoteFilename != "a.png" {
		t.Fatalf("%+v", sm)
	}
	m.SendMessage(ctx5(t), "telegram", "1", "x", proto.Quote{Attachments: []string{"image/png|a.png"}})
	sm = tg.CallsTo("EnqueueSentMessage")[1].Args["message"].(proto.SentMessage)
	if sm.QuoteContentType != "" {
		t.Fatalf("telegram must not get quote media: %+v", sm)
	}
	// send failure: no mirror
	tg.FailSend(errors.New("boom"))
	if _, err := m.SendMessage(ctx5(t), "telegram", "1", "x", proto.Quote{}); err == nil {
		t.Fatal("expected error")
	}
	if len(tg.CallsTo("EnqueueSentMessage")) != 2 {
		t.Fatal("must not mirror failed send")
	}
}

func TestMirrorFailureDoesNotFailSend(t *testing.T) {
	m := proto.New()
	f := fake.New("whatsapp")
	m.Register(f)
	f.FailMethod("EnqueueSentMessage", errors.New("mirror down"))
	if _, err := m.SendMessage(ctx5(t), "whatsapp", "1", "x", proto.Quote{}); err != nil {
		t.Fatal(err)
	}
}

func TestSendAttachmentsBatchRules(t *testing.T) {
	m := proto.New()
	sig, wa := fake.New("signal"), fake.New("whatsapp")
	m.Register(sig)
	m.Register(wa)
	atts := []proto.Attachment{{Path: "/a", Caption: "c1", MimeType: "image/png"}, {Path: "/b", MimeType: "image/jpeg"}}
	ids, err := m.SendAttachments(ctx5(t), "signal", "1", atts, "B1", proto.Quote{})
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if len(sig.CallsTo("EnqueueSentNotification")) != 1 || len(sig.CallsTo("EnqueueSentMessage")) != 0 {
		t.Fatal("signal: single notification only")
	}
	ids, err = m.SendAttachments(ctx5(t), "whatsapp", "1", atts, "B2", proto.Quote{Message: "q"})
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	ms := wa.CallsTo("EnqueueSentMessage")
	if len(ms) != 2 {
		t.Fatal("one mirror per message")
	}
	m0 := ms[0].Args["message"].(proto.SentMessage)
	m1 := ms[1].Args["message"].(proto.SentMessage)
	if m0.BatchIndex == nil || *m0.BatchIndex != 0 || *m1.BatchIndex != 1 || m0.Quote.Message != "q" || m1.Quote.Message != "" {
		t.Fatalf("%+v %+v", m0, m1)
	}
	if got, err := m.SendAttachments(ctx5(t), "whatsapp", "1", nil, "", proto.Quote{}); err != nil || len(got) != 0 {
		t.Fatal("empty batch")
	}
	// atomic: failure aborts
	wa.FailMethod("SendAttachments", errors.New("x"))
	if _, err := m.SendAttachments(ctx5(t), "whatsapp", "1", atts, "", proto.Quote{}); err == nil {
		t.Fatal("expected failure")
	}
}

func TestCapabilityRouting(t *testing.T) {
	m := proto.New()
	f := fake.New("telegram")
	f.AddAttachment("a1", "/tmp/a1", []byte("0123456789"))
	f.AddHistory("c", proto.Message{ID: api.Ptr("7"), IsMine: api.Ptr(true), Text: api.Ptr("old")})
	f.SetPairing(true, "tg://login?token=x")
	f.SetTwoFactorPassword("pw")
	m.Register(f)
	m.Register(minimal{proto: "bare"})
	c := ctx5(t)

	if p, ok := m.AttachmentPath(c, "telegram", "a1"); !ok || p != "/tmp/a1" {
		t.Fatal(p, ok)
	}
	if _, ok := m.AttachmentPath(c, "bare", "a1"); ok {
		t.Fatal("bare backend has no attachments")
	}
	if b, err := m.AttachmentChunk(c, "telegram", "a1", 2, 3); err != nil || string(b) != "234" {
		t.Fatal(string(b), err)
	}
	if _, err := m.AttachmentChunk(c, "bare", "a1", 0, 1); !errors.Is(err, proto.ErrUnsupported) {
		t.Fatal(err)
	}
	if ok, err := m.EditMessage(c, "telegram", "c", "7", "new"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := m.EditMessage(c, "bare", "c", "7", "new"); !errors.Is(err, proto.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := m.EditMessage(c, "zzz", "c", "7", "new"); !errors.Is(err, proto.ErrNoBackend) {
		t.Fatal(err)
	}
	if ok, _ := m.SendReaction(c, "telegram", "c", "7", "👍", ""); !ok {
		t.Fatal("reaction")
	}
	if q, _ := m.PairingQR(c, "telegram"); q != "tg://login?token=x" {
		t.Fatal(q)
	}
	if ok, _ := m.Complete2FA(c, "telegram", "bad"); ok {
		t.Fatal("bad password")
	}
	if ok, _ := m.Complete2FA(c, "telegram", "pw"); !ok {
		t.Fatal("good password")
	}
	if ok, _ := m.SetChatArchived(c, "telegram", "c", true); !ok || !f.Archived("c") {
		t.Fatal("archive")
	}
	if h, err := m.FetchHistory(c, "telegram", "c", 10); err != nil || len(h) != 1 {
		t.Fatal(h, err)
	}
	if _, ok := m.FindContact("bare", "x"); ok {
		t.Fatal("bare find")
	}
	st := m.Status()
	if len(st) != 2 || !slices.Contains(st[0].Capabilities, "reactions") || slices.Contains(st[1].Capabilities, "reactions") {
		t.Fatalf("%+v", st)
	}
	if st[0].NeedsPairing {
		t.Fatal("pairing completed by 2FA")
	}
}

func TestLifecycle(t *testing.T) {
	var mu sync.Mutex
	var changes []proto.StateChange
	m := proto.New(proto.WithOnState(func(c proto.StateChange) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	}))
	ok, bad := fake.New("signal"), fake.New("telegram")
	bad.FailConnect(errors.New("no creds"))
	m.Register(ok)
	m.Register(bad)
	err := m.Start(ctx5(t))
	if err == nil || !errors.Is(err, errors.Unwrap(err)) {
		t.Log("joined err:", err)
	}
	if err == nil {
		t.Fatal("expected joined error")
	}
	st := m.Status()
	if st[0].State != proto.StateConnected || !st[0].Connected || st[1].State != proto.StateFailed || st[1].Error != "no creds" {
		t.Fatalf("%+v", st)
	}
	// events flow from the healthy backend
	ok.Emit(proto.Event{Type: proto.EventTyping, ContactID: "c", Payload: map[string]any{"action": "STARTED"}})
	select {
	case ev := <-m.Events():
		if ev.Type != proto.EventTyping || ev.Protocol != "signal" {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	// retry the failed one
	bad.FailConnect(nil)
	if err := m.StartBackend(ctx5(t), "telegram"); err != nil {
		t.Fatal(err)
	}
	if m.Status()[1].State != proto.StateConnected {
		t.Fatal("restart")
	}
	// Start again is a no-op for connected backends.
	m.Start(ctx5(t))
	if n := len(ok.CallsTo("Connect")); n != 1 {
		t.Fatalf("connect called %d times", n)
	}
	if err := m.Stop(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Status() {
		if s.State != proto.StateStopped || s.Connected {
			t.Fatalf("%+v", s)
		}
	}
	// restart after stop works
	if err := m.Start(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	m.Stop(ctx5(t))
	mu.Lock()
	defer mu.Unlock()
	if len(changes) < 6 {
		t.Fatalf("observer saw %d changes", len(changes))
	}
	if err := m.StartBackend(ctx5(t), "zzz"); !errors.Is(err, proto.ErrNoBackend) {
		t.Fatal(err)
	}
}

func TestStopDisconnectErrorsAreIsolated(t *testing.T) {
	m := proto.New()
	a, b := fake.New("signal"), fake.New("whatsapp")
	a.FailMethod("Disconnect", errors.New("hang"))
	m.Register(a)
	m.Register(b)
	m.Start(ctx5(t))
	if err := m.Stop(ctx5(t)); err == nil {
		t.Fatal("expected error")
	}
	if b.IsConnected() {
		t.Fatal("other backend must still be disconnected")
	}
}

func TestListContactsAndAddressBook(t *testing.T) {
	m := proto.New(proto.WithTimeouts(100*time.Millisecond, 100*time.Millisecond))
	sig, wa, slow := fake.New("signal"), fake.New("whatsapp"), fake.New("telegram")
	sig.SetContacts(proto.Contact{ID: "1", Protocol: "signal", DisplayName: "A"})
	wa.SetContacts(proto.Contact{ID: "2", Protocol: "whatsapp", DisplayName: "B"})
	wa.SetAddressBook(proto.Contact{ID: "2", Protocol: "whatsapp"}, proto.Contact{ID: "3", Protocol: "whatsapp"})
	release := slow.BlockAddressBook()
	defer release()
	m.Register(sig)
	m.Register(wa)
	m.Register(slow)
	c := ctx5(t)

	all, err := m.ListContacts(c)
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
	slow.FailMethod("ListContacts", errors.New("down"))
	if all, err = m.ListContacts(c); err == nil || len(all) != 2 {
		t.Fatalf("partial result expected: %v %v", all, err)
	}

	start := time.Now()
	book, failed := m.ListAddressBook(c, nil, false)
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not enforced")
	}
	if len(book) != 3 { // signal default (1) + whatsapp book (2)
		t.Fatalf("%+v", book)
	}
	if !errors.Is(failed["telegram"], context.DeadlineExceeded) || len(failed) != 1 {
		t.Fatalf("%+v", failed)
	}
	for _, ct := range book {
		if ct.Extras["address_book"] != true {
			t.Fatalf("not flagged: %+v", ct)
		}
	}
	book, failed = m.ListAddressBook(c, []string{"signal"}, false)
	if len(book) != 1 || len(failed) != 0 {
		t.Fatal(book, failed)
	}
}

func TestRefreshContacts(t *testing.T) {
	m := proto.New(proto.WithTimeouts(time.Second, time.Second))
	a, b := fake.New("whatsapp"), fake.New("telegram")
	a.SetRefresh("", proto.Contact{ID: "n", Protocol: "whatsapp"})
	b.SetRefresh("remote 500")
	m.Register(a)
	m.Register(b)
	m.Register(minimal{proto: "bare"})
	res := m.RefreshContacts(ctx5(t), nil, true)
	if len(res["whatsapp"].NewContacts) != 1 || res["telegram"].Errors != "remote 500" {
		t.Fatalf("%+v", res)
	}
	if r, ok := res["bare"]; !ok || r.Errors != "" || len(r.NewContacts) != 0 {
		t.Fatalf("bare must be a no-op: %+v", r)
	}
	if got := m.RefreshContacts(ctx5(t), []string{"whatsapp"}, true); len(got) != 1 {
		t.Fatal("filter")
	}
}

func TestConcurrentUse(t *testing.T) {
	m := proto.New()
	f := fake.New("signal")
	m.Register(f)
	if err := m.Start(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				m.SendMessage(context.Background(), "signal", "c", "x", proto.Quote{})
				m.Status()
				m.ListAddressBook(context.Background(), nil, false)
				f.Emit(proto.Event{Type: proto.EventMessage})
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-m.Events():
			case <-done:
				return
			}
		}
	}()
	wg.Wait()
	close(done)
	if err := m.Stop(ctx5(t)); err != nil {
		t.Fatal(err)
	}
}

func TestContactHelpers(t *testing.T) {
	c := proto.Contact{ID: "9", Protocol: "signal", Extras: map[string]any{"phone": "39", "last_message_ts": int64(5)}}
	if c.CacheKey() != "signal:9" || c.Phone() != "39" || c.LastMessageTS() != 5 {
		t.Fatal("helpers")
	}
	c2 := c.WithExtra("x", 1)
	if _, ok := c.Extras["x"]; ok || c2.Extras["x"] != 1 {
		t.Fatal("WithExtra must copy")
	}
	if ct, fn := proto.ParseQuoteAttachmentDescriptor("a/b|f.x"); ct != "a/b" || fn != "f.x" {
		t.Fatal(ct, fn)
	}
}
