package signal

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

var bg = context.Background()

func TestConnectWithRunningDaemon(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	if !h.b.IsConnected() {
		t.Fatal("not connected")
	}
	cs, _ := h.b.ListContacts(bg)
	if len(cs) != 2 || cs[0].ID != "+391234567890" || cs[0].DisplayName != "Mario" || cs[1].DisplayName != "Luigi" {
		t.Fatalf("%+v", cs)
	}
	if cs[0].Extras["aci"] != "uuid-123" || cs[0].Extras["number"] != "+391234567890" {
		t.Fatalf("%+v", cs[0].Extras)
	}
	if len(h.d.callsOf("sendSyncRequest")) != 1 {
		t.Fatal("sendSyncRequest not issued")
	}

	ch, err := h.b.Receive(bg)
	if err != nil {
		t.Fatal(err)
	}
	h.d.pushEnvelope(t, map[string]any{
		"source": "+391234567890", "sourceNumber": "+391234567890", "sourceName": "Mario", "timestamp": 1700000000000,
		"dataMessage": map[string]any{"message": "ciao", "timestamp": 1700000000000},
	})
	ev := recv(t, ch)
	if ev.Type != proto.EventMessage || ev.ContactID != "+391234567890" || ev.Payload["text"] != "ciao" ||
		ev.Payload["sender"] != "Mario" || ev.Payload["is_mine"] != false {
		t.Fatalf("%+v", ev)
	}
	if c, ok := ev.Payload["contact"].(proto.Contact); !ok || c.ID != "+391234567890" {
		t.Fatalf("contact payload: %#v", ev.Payload["contact"])
	}
	h.d.mu.Lock()
	q := h.d.sseQuery[0]
	h.d.mu.Unlock()
	if q != "account="+testNumber {
		t.Fatal(q)
	}

	// Disconnect closes the stream.
	if err := h.b.Disconnect(bg); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed")
	}
	if h.b.IsConnected() {
		t.Fatal("still connected")
	}
}

func TestConnectStartsDaemonThenSubprocessFallback(t *testing.T) {
	log := t.TempDir() + "/cli.log"
	t.Setenv("FAKE_SIGNAL_LOG", log)
	h := newHarness(t, func(o *Options) {
		o.CLIPath = fakeCLI(t)
		o.DaemonURL = "http://127.0.0.1:1/api/v1/rpc"
		o.SSEURL = "http://127.0.0.1:1/api/v1/events"
		o.DaemonPort = 18080
	})
	h.connect(t)
	if h.b.IsConnected() {
		t.Fatal("daemon never came up: must not be connected")
	}
	// contacts came from the subprocess fallback
	cs, _ := h.b.ListContacts(bg)
	if len(cs) != 2 || cs[0].DisplayName != "Mario Rossi" || cs[0].Extras["aci"] != "uuid-123" ||
		cs[1].DisplayName != "Anna Maria Bianchi" || cs[1].Extras["aci"] != "" {
		t.Fatalf("%+v", cs)
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	wantDaemon := "-u|" + testNumber + "|daemon|--http|127.0.0.1:18080|--receive-mode|on-connection|--no-receive-stdout"
	found := false
	for _, l := range lines {
		if l == wantDaemon {
			found = true
		}
	}
	if !found {
		t.Fatalf("daemon not started with %q: %q", wantDaemon, lines)
	}
	if lines[len(lines)-1] != "-u|"+testNumber+"|listContacts" {
		t.Fatalf("fallback list: %q", lines)
	}
}

// TestConnectFailsWhenSpawnedDaemonExits: an unlinked account makes
// signal-cli exit right away. Connect must fail with the exit status and
// stderr instead of silently falling back to subprocess mode (mc-tih).
func TestConnectFailsWhenSpawnedDaemonExits(t *testing.T) {
	t.Setenv("FAKE_SIGNAL_DAEMON_FAIL", "-390000000000 is not registered")
	t.Setenv("FAKE_SIGNAL_DAEMON_EXIT", "3")
	h := newHarness(t, func(o *Options) {
		o.CLIPath = fakeCLI(t)
		o.DaemonURL = "http://127.0.0.1:1/api/v1/rpc"
		o.SSEURL = "http://127.0.0.1:1/api/v1/events"
		o.DaemonPort = 18081
		o.ProbeAttempts = 200
		o.ProbeInterval = 5 * time.Millisecond
	})
	err := h.b.Connect(bg)
	if err == nil {
		t.Fatal("Connect reported success although the daemon exited")
	}
	for _, want := range []string{"signal-cli daemon exited", "exit status 3", "is not registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if h.b.IsConnected() {
		t.Fatal("half-connected backend after a dead daemon")
	}
}

func TestConnectErrors(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.DaemonURL = "http://127.0.0.1:1/x" })
	if err := h.b.Connect(bg); err != ErrCLINotFound {
		t.Fatalf("want ErrCLINotFound, got %v", err)
	}
	empty := New(Options{Config: noNumberCfg{testCfg{t.TempDir()}}, Logger: quietLogger()})
	if err := empty.Connect(bg); err != ErrNotConfigured {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

type noNumberCfg struct{ testCfg }

func (noNumberCfg) SignalUserNumber() string { return "" }

func TestSetContactsFiltersAndLastTimestamp(t *testing.T) {
	h := newHarness(t)
	h.b.cache["+1"] = []*entry{{Timestamp: 5}, {Timestamp: 9}, {Timestamp: 7}}
	h.b.setContacts([]proto.Contact{contact("+1", "A", ""), contact("status@broadcast", "S", ""), {ID: "", Protocol: "signal"}})
	cs, _ := h.b.ListContacts(bg)
	if len(cs) != 1 || cs[0].LastMessageTS() != 9 {
		t.Fatalf("%+v", cs)
	}
}

func TestParseContactDict(t *testing.T) {
	cases := []struct {
		in   map[string]any
		name string
		aci  string
		id   string
	}{
		{map[string]any{"number": "+1", "name": "N", "uuid": "u"}, "N", "u", "+1"},
		{map[string]any{"number": "+1", "givenName": "G"}, "G", "", "+1"},
		{map[string]any{"number": "+1", "profile": map[string]any{"givenName": "P"}, "aci": "a"}, "P", "a", "+1"},
		{map[string]any{"uuid": "only-uuid"}, "only-uuid", "only-uuid", "only-uuid"},
		{map[string]any{"number": "+1"}, "+1", "", "+1"},
	}
	for i, c := range cases {
		got := parseContactDict(c.in)
		if got.ID != c.id || got.DisplayName != c.name || got.Extras["aci"] != c.aci {
			t.Errorf("%d: %+v", i, got)
		}
	}
}

func TestSendMessageDaemon(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.d.handle("send", func(p map[string]any) any { return map[string]any{"timestamp": 1700000000123} })
	id, err := h.b.SendMessage(bg, "+391234567890", "hi", proto.Quote{Timestamp: 1234, Author: "+391111111111", Message: ""})
	if err != nil || id != "1700000000123" {
		t.Fatal(id, err)
	}
	p := h.d.callsOf("send")[0].Params
	if p["message"] != "hi" || p["quoteMessage"] != "" || p["quoteAuthor"] != "+391111111111" {
		t.Fatalf("%v", p)
	}
	if _, ok := p["timestamp"]; !ok {
		t.Fatal("client timestamp is sent (ignored by signal-cli)")
	}
	if _, ok := p["quoteAttachments"]; ok {
		t.Fatal("quoteAttachments must be omitted")
	}

	// no real timestamp -> optimistic one
	h.d.handle("send", func(map[string]any) any { return map[string]any{} })
	h.b.clock = func() time.Time { return time.UnixMilli(1700000009999) }
	id, err = h.b.SendMessage(bg, "+391234567890", "hi", proto.Quote{})
	if err != nil || id != "1700000009999" {
		t.Fatal(id, err)
	}
	p = h.d.callsOf("send")[1].Params
	for _, k := range []string{"quoteTimestamp", "quoteAuthor", "quoteMessage"} {
		if _, ok := p[k]; ok {
			t.Fatalf("%s must be omitted without a quote", k)
		}
	}

	// RPC error -> error
	h.d.handle("send", func(map[string]any) any { return rpcFail("rate limited") })
	if _, err := h.b.SendMessage(bg, "+391234567890", "hi", proto.Quote{}); err == nil || err.Error() != "rate limited" {
		t.Fatal(err)
	}

	// quote attachments travel verbatim
	h.d.handle("send", func(map[string]any) any { return map[string]any{"timestamp": 5} })
	_, _ = h.b.SendMessage(bg, "+391234567890", "hi", proto.Quote{Timestamp: 1, Attachments: []string{"image/png:photo.png:/tmp/photo.png"}})
	p = h.d.callsOf("send")[3].Params
	if qa := p["quoteAttachments"].([]any); qa[0] != "image/png:photo.png:/tmp/photo.png" {
		t.Fatalf("%v", p)
	}
}

func TestSendMessageSubprocess(t *testing.T) {
	log := t.TempDir() + "/cli.log"
	t.Setenv("FAKE_SIGNAL_LOG", log)
	h := newHarness(t, func(o *Options) { o.CLIPath = fakeCLI(t) })
	// not connected: subprocess path
	id, err := h.b.SendMessage(bg, "+391234567890", "hello world", proto.Quote{Timestamp: 99, Author: "+39", Message: "orig", Attachments: []string{"image/png"}})
	if err != nil || id != "1700000000123" {
		t.Fatal(id, err)
	}
	raw, _ := os.ReadFile(log)
	want := "-u|" + testNumber + "|send|-m|hello world|+391234567890|--quote-timestamp|99|--quote-author|+39|--quote-message|orig|--quote-attachment|image/png"
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("argv:\n got %q\nwant %q", strings.TrimSpace(string(raw)), want)
	}
	t.Setenv("FAKE_SIGNAL_SEND_OUT", "not a number")
	h.b.clock = func() time.Time { return time.UnixMilli(1700000004242) }
	if id, _ := h.b.SendMessage(bg, "+39", "x", proto.Quote{}); id != "1700000004242" {
		t.Fatal("dirty stdout must fall back to the optimistic ts:", id)
	}
	// non-zero exit
	_, err = h.b.runSubprocess(bg, "fail")
	if err == nil || err.Error() != "signal-cli error (code 3): boom on stderr" {
		t.Fatal(err)
	}
}

func TestEditMessage(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ok, err := h.b.EditMessage(bg, "+39", "1700000000123", "new")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	p := h.d.callsOf("send")[0].Params
	if p["editTimestamp"].(interface{ String() string }).String() != "1700000000123" || p["message"] != "new" {
		t.Fatalf("%v", p)
	}
	if ok, err := h.b.EditMessage(bg, "+39", "not-a-ts", "x"); ok || err != nil {
		t.Fatal("non numeric id -> false,nil")
	}
	h.d.handle("send", func(map[string]any) any { return rpcFail("denied") })
	if _, err := h.b.EditMessage(bg, "+39", "5", "x"); err == nil {
		t.Fatal("rpc error must surface")
	}

	// subprocess
	log := t.TempDir() + "/cli.log"
	t.Setenv("FAKE_SIGNAL_LOG", log)
	h2 := newHarness(t, func(o *Options) { o.CLIPath = fakeCLI(t) })
	if ok, err := h2.b.EditMessage(bg, "+39", "77", "txt"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "send|-m|txt|+39|--edit-timestamp|77") {
		t.Fatal(string(raw))
	}
}

func TestSendReaction(t *testing.T) {
	h := newHarness(t)
	h.offline(contact("+391234567890", "M", ""))
	h.d.handle("sendReaction", func(map[string]any) any {
		return map[string]any{"results": []any{map[string]any{"type": "SUCCESS"}}}
	})
	h.b.cache["+391234567890"] = []*entry{
		{ID: "100", Timestamp: 100, IsMine: true},
		{ID: "200", Timestamp: 200, IsMine: false},
	}
	ok, err := h.b.SendReaction(bg, "+391234567890", "100", "👍", "")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if a := h.d.callsOf("sendReaction")[0].Params["targetAuthor"]; a != testNumber {
		t.Fatalf("own message author must be the account number, got %v", a)
	}
	_, _ = h.b.SendReaction(bg, "+391234567890", "200", "👍", "")
	if a := h.d.callsOf("sendReaction")[1].Params["targetAuthor"]; a != "+391234567890" {
		t.Fatal(a)
	}
	_, _ = h.b.SendReaction(bg, "+391234567890", "300", "👍", "+39explicit")
	if a := h.d.callsOf("sendReaction")[2].Params["targetAuthor"]; a != "+39explicit" {
		t.Fatal(a)
	}
	if ok, _ := h.b.SendReaction(bg, "+39", "abc", "👍", ""); ok {
		t.Fatal("non numeric id -> false")
	}
	h.b.useDaemon.Store(false)
	if ok, _ := h.b.SendReaction(bg, "+39", "100", "👍", ""); ok {
		t.Fatal("no daemon -> false")
	}
}

func TestAddressBookAndRefresh(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ab, _ := h.b.ListAddressBook(bg, false)
	if len(ab) != 2 || ab[0].Extras["phone"] != "391234567890" || ab[0].Extras["address_book"] != true || ab[0].Extras["is_chat_active"] != false {
		t.Fatalf("%+v", ab)
	}
	// cached within TTL
	h.b.cache["+391234567890"] = []*entry{{Timestamp: 5}}
	h.b.setContacts([]proto.Contact{contact("+391234567890", "Mario", "uuid-123")})
	ab, _ = h.b.ListAddressBook(bg, false)
	if len(ab) != 2 {
		t.Fatal("TTL cache must be served")
	}
	ab, _ = h.b.ListAddressBook(bg, true)
	if len(ab) != 1 || ab[0].Extras["is_chat_active"] != true {
		t.Fatalf("%+v", ab)
	}

	// refresh: a new contact, a rename and a ghost survive
	h.b.RegisterContact(proto.Contact{ID: "+ghost", DisplayName: "Ghost", Protocol: "signal", Extras: map[string]any{"ghost": true}})
	if h.b.RegisterContact(contact("+ghost", "Ghost", "")) {
		t.Fatal("register must be idempotent by cache key")
	}
	h.d.handle("listContacts", func(map[string]any) any {
		return []any{
			map[string]any{"number": "+391234567890", "name": "Mario R.", "uuid": "uuid-123"},
			map[string]any{"number": "+392", "name": "New", "uuid": "u2"},
		}
	})
	res := h.b.RefreshContacts(bg, true)
	if res.Errors != "" || len(res.NewContacts) != 1 || res.NewContacts[0].ID != "+392" ||
		len(res.RenamedContacts) != 1 || res.RenamedContacts[0].DisplayName != "Mario R." {
		t.Fatalf("%+v", res)
	}
	cs, _ := h.b.ListContacts(bg)
	ids := []string{}
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "+391234567890,+ghost,+392" {
		t.Fatal(ids)
	}
	if cs[0].LastMessageTS() != 5 {
		t.Fatal("last_message_ts must be preserved on merge")
	}
	evs := h.b.PollOnce()
	if len(evs) != 2 || evs[0].Type != proto.EventContactUpdate || evs[0].ContactID != "+392" || evs[1].ContactID != "+391234567890" {
		t.Fatalf("%+v", evs)
	}
	// failure: nothing committed
	h.d.handle("listContacts", func(map[string]any) any { return rpcFail("down") })
	res = h.b.RefreshContacts(bg, true)
	if res.Errors != "down" {
		t.Fatalf("%+v", res)
	}
	h.d.handle("listContacts", func(map[string]any) any { return map[string]any{} })
	if res = h.b.RefreshContacts(bg, true); res.Errors != "unexpected RPC result" {
		t.Fatalf("%+v", res)
	}
	h.b.useDaemon.Store(false)
	if res = h.b.RefreshContacts(bg, true); res.Errors != "daemon not running" {
		t.Fatalf("%+v", res)
	}
	if c, ok := h.b.FindContact("+ghost"); !ok || c.DisplayName != "Ghost" {
		t.Fatal("FindContact")
	}
}

func TestParseContactsOutput(t *testing.T) {
	cs := parseContactsOutput("Number:+391234567890 Name:Mario Rossi ACI:uuid-123\nNumber:+391111111111 Name:Anna Maria Bianchi\n\nnonsense\n" +
		"Number:+3922 Name:Zed ACI:u9 Profile name: Z Z\n")
	if len(cs) != 3 || cs[0].DisplayName != "Mario Rossi" || cs[0].Extras["aci"] != "uuid-123" ||
		cs[1].DisplayName != "Anna Maria Bianchi" || cs[2].DisplayName != "Zed" || cs[2].Extras["aci"] != "u9" {
		t.Fatalf("%+v", cs)
	}
}

func TestUserNumberAccount(t *testing.T) {
	h := newHarness(t)
	if h.b.UserNumber() != testNumber {
		t.Fatal(h.b.UserNumber())
	}
	h.b.SetUserNumber("+39999")
	if h.b.UserNumber() != "+39999" {
		t.Fatal("SetUserNumber")
	}
}

func TestMarkRead(t *testing.T) {
	h := newHarness(t)
	h.offline(contact("+1", "A", ""))
	ev := map[string]any{"text": "x", "is_mine": false, "sender": "A", "msg_type": "text"}
	if _, err := h.b.IngestMessage(bg, "+1", ev, 1000); err != nil {
		t.Fatal(err)
	}
	if err := h.b.MarkRead(bg, "+1"); err != nil {
		t.Fatal(err)
	}
	counts, _ := h.st.CountUnread(bg)
	if counts["+1"] != 0 {
		t.Fatal(counts)
	}
}
