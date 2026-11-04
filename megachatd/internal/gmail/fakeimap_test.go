package gmail

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func testCert(t testing.TB) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// ---- fake IMAP: go-imap's imapserver + imapmemserver behind a TLS listener ----

type fakeBox struct {
	name  string
	attrs []imap.MailboxAttr
}

type fakeIMAP struct {
	t     testing.TB
	ln    net.Listener
	user  *imapmemserver.User
	name  string
	pass  string
	boxes []fakeBox

	mu   sync.Mutex
	cmds []string
	conn int // connections accepted
}

// fakeSession adds what Gmail does and imapmemserver does not: special-use
// attributes in a plain LIST.
type fakeSession struct {
	imapserver.SessionIMAP4rev2
	f *fakeIMAP
}

func (s *fakeSession) List(w *imapserver.ListWriter, ref string, patterns []string, o *imap.ListOptions) error {
	for _, b := range s.f.boxes {
		match := false
		for _, p := range patterns {
			if imapserver.MatchList(b.name, '/', ref, p) {
				match = true
			}
		}
		if !match {
			continue
		}
		if err := w.WriteList(&imap.ListData{Mailbox: b.name, Delim: '/', Attrs: b.attrs}); err != nil {
			return err
		}
	}
	return nil
}

// loggingConn records the commands a client sends. Gmail's
// "-X-GM-LABELS.SILENT (\Inbox)" is logged as is, then rewritten (same
// length) to a STORE the in-memory server understands.
type loggingConn struct {
	net.Conn
	f   *fakeIMAP
	buf []byte
}

var (
	gmLabelsWire = []byte(`-X-GM-LABELS.SILENT (\Inbox)`)
	gmLabelsFake = []byte(`-FLAGS.SILENT (\InboxLabels)`)
)

func (c *loggingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.buf = append(c.buf, b[:n]...)
		for {
			i := bytes.Index(c.buf, []byte("\r\n"))
			if i < 0 {
				break
			}
			line := string(c.buf[:i])
			c.buf = c.buf[i+2:]
			if _, rest, ok := strings.Cut(line, " "); ok {
				c.f.mu.Lock()
				c.f.cmds = append(c.f.cmds, rest)
				c.f.mu.Unlock()
			}
		}
		if bytes.Contains(b[:n], gmLabelsWire) {
			copy(b[:n], bytes.Replace(b[:n], gmLabelsWire, gmLabelsFake, 1))
		}
	}
	return n, err
}

type memLiteral struct{ *bytes.Reader }

func (m memLiteral) Size() int64 { return int64(m.Reader.Size()) }

func newFakeIMAP(t testing.TB, user, pass string) *fakeIMAP {
	f := &fakeIMAP{t: t, name: user, pass: pass, boxes: []fakeBox{
		{"INBOX", nil},
		{"[Gmail]/Trash", []imap.MailboxAttr{imap.MailboxAttrTrash}},
		{"[Gmail]/Sent Mail", []imap.MailboxAttr{imap.MailboxAttrSent}},
	}}
	mem := imapmemserver.New()
	f.user = imapmemserver.NewUser(user, pass)
	for _, b := range f.boxes {
		if err := f.user.Create(b.name, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(f.user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &fakeSession{SessionIMAP4rev2: mem.NewSession().(imapserver.SessionIMAP4rev2), f: f}, nil, nil
		},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(t)}})
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go srv.Serve(&loggingListener{Listener: ln, f: f})
	t.Cleanup(func() { srv.Close() })
	return f
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

type loggingListener struct {
	net.Listener
	f *fakeIMAP
}

func (l *loggingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.f.mu.Lock()
	l.f.conn++
	l.f.mu.Unlock()
	return &loggingConn{Conn: c, f: l.f}, nil
}

func (f *fakeIMAP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

// add appends a message (LF line ends are converted to CRLF) and returns its UID.
func (f *fakeIMAP) add(box string, flags []string, raw string) int {
	f.t.Helper()
	var fl []imap.Flag
	for _, x := range flags {
		fl = append(fl, imap.Flag(x))
	}
	data, err := f.user.Append(box, memLiteral{bytes.NewReader([]byte(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", "\r\n")))},
		&imap.AppendOptions{Flags: fl})
	if err != nil {
		f.t.Fatal(err)
	}
	return int(data.UID)
}

// commands is the log of every command line the server received.
func (f *fakeIMAP) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeIMAP) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn
}

// uids lists a mailbox's UIDs through a throwaway client session.
func (f *fakeIMAP) uids(box string) []int {
	f.t.Helper()
	c, err := imapclient.DialTLS(f.ln.Addr().String(), &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}})
	if err != nil {
		f.t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(f.name, f.pass).Wait(); err != nil {
		f.t.Fatal(err)
	}
	if _, err := c.Select(box, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		f.t.Fatal(err)
	}
	d, err := c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		f.t.Fatal(err)
	}
	var out []int
	for _, u := range d.AllUIDs() {
		out = append(out, int(u))
	}
	return out
}

var _ = bufio.NewReader
var _ = io.EOF

// ---- fake SMTP ----

type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	from string
	to   []string
	data string
	auth string
}

func newFakeSMTP(t testing.TB) *fakeSMTP {
	f := &fakeSMTP{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	cert := testCert(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c, cert)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn, cert tls.Certificate) {
	defer func() { c.Close() }()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s)) }
	w("220 fake ESMTP\r\n")
	tlsOn := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			if tlsOn {
				w("250-localhost\r\n250-AUTH PLAIN LOGIN\r\n250 8BITMIME\r\n")
			} else {
				w("250-localhost\r\n250 STARTTLS\r\n")
			}
		case up == "STARTTLS":
			w("220 go ahead\r\n")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			r = bufio.NewReader(tc)
			w = func(s string) { _, _ = tc.Write([]byte(s)) }
			tlsOn = true
		case strings.HasPrefix(up, "AUTH"):
			f.mu.Lock()
			f.auth = line
			f.mu.Unlock()
			w("235 ok\r\n")
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(strings.SplitN(line[10:], ">", 2)[0], "< ")
			f.to = nil
			f.mu.Unlock()
			w("250 ok\r\n")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.mu.Lock()
			f.to = append(f.to, strings.Trim(line[8:], "<> "))
			f.mu.Unlock()
			w("250 ok\r\n")
		case up == "DATA":
			w("354 go\r\n")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued\r\n")
		case up == "QUIT":
			w("221 bye\r\n")
			return
		default:
			w("250 ok\r\n")
		}
	}
}

// ---- config stub + helpers ----

type testCfg struct {
	addr, pass, imapHost, smtpHost string
	imapPort, smtpPort             int
	folder                         string
	cacheDir                       string
	noStyles, noInline             bool
	token                          string
}

func (c *testCfg) GmailEnabled() bool        { return c.addr != "" && c.pass != "" }
func (c *testCfg) GmailAddress() string      { return c.addr }
func (c *testCfg) GmailAppPassword() string  { return c.pass }
func (c *testCfg) GmailIMAPHost() string     { return "127.0.0.1" }
func (c *testCfg) GmailIMAPPort() int        { return c.imapPort }
func (c *testCfg) GmailSMTPHost() string     { return "127.0.0.1" }
func (c *testCfg) GmailSMTPPort() int        { return c.smtpPort }
func (c *testCfg) GmailFolder() string       { return "INBOX" }
func (c *testCfg) GmailTimeoutS() int        { return 5 }
func (c *testCfg) GmailHTMLStyles() bool     { return !c.noStyles }
func (c *testCfg) GmailHTMLInlineCSS() bool  { return !c.noInline }
func (c *testCfg) GmailImagePrefetch() bool  { return false }
func (c *testCfg) GmailUnreadPollS() float64 { return 0 }
func (c *testCfg) CacheDir() string          { return c.cacheDir }
func (c *testCfg) WebToken() string          { return "tok" }

func newTestService(t testing.TB, imap *fakeIMAP, smtpSrv *fakeSMTP) (*Service, *testCfg) {
	t.Helper()
	cfg := &testCfg{addr: "me@example.com", pass: "secret", cacheDir: t.TempDir()}
	if imap != nil {
		cfg.imapPort = imap.port()
	}
	if smtpSrv != nil {
		cfg.smtpPort = smtpSrv.port()
	}
	s := NewService(cfg, Deps{Config: cfg, TLS: &tls.Config{InsecureSkipVerify: true}})
	t.Cleanup(s.Close)
	return s, cfg
}

func rfc(subject, from, date, body string) string {
	return "From: " + from + "\nTo: me@example.com\nSubject: " + subject + "\nDate: " + date + "\nMessage-ID: <" + subject + "@x>\nContent-Type: text/plain; charset=utf-8\n\n" + body + "\n"
}
