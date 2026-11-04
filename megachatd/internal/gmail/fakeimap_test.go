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
	"fmt"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

type fakeMsg struct {
	uid   int
	flags map[string]bool
	raw   []byte
}

type fakeIMAP struct {
	t     testing.TB
	ln    net.Listener
	mu    sync.Mutex
	boxes map[string][]*fakeMsg
	next  map[string]int
	user  string
	pass  string
	cmds  []string
}

func newFakeIMAP(t testing.TB, user, pass string) *fakeIMAP {
	f := &fakeIMAP{t: t, boxes: map[string][]*fakeMsg{"INBOX": nil, "[Gmail]/Trash": nil, "[Gmail]/Sent Mail": nil},
		next: map[string]int{}, user: user, pass: pass}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testCert(t)}})
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeIMAP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeIMAP) add(box string, flags []string, raw string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next[box]++
	m := &fakeMsg{uid: f.next[box], flags: map[string]bool{}, raw: []byte(strings.ReplaceAll(raw, "\n", "\r\n"))}
	for _, fl := range flags {
		m.flags[fl] = true
	}
	f.boxes[box] = append(f.boxes[box], m)
	return m.uid
}

func (f *fakeIMAP) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeIMAP) uids(box string) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, m := range f.boxes[box] {
		out = append(out, m.uid)
	}
	return out
}

func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	depth, inQ := 0, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQ:
			if ch == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else if ch == '"' {
				inQ = false
			} else {
				cur.WriteByte(ch)
			}
		case ch == '"':
			inQ = true
			if cur.Len() == 0 {
				out = append(out, "")
				cur.Reset()
				// mark: quoted token started; collect until closing quote
				j := i + 1
				var q strings.Builder
				for j < len(s) && s[j] != '"' {
					if s[j] == '\\' && j+1 < len(s) {
						j++
					}
					q.WriteByte(s[j])
					j++
				}
				out[len(out)-1] = q.String()
				i = j
				inQ = false
			}
		case ch == '(' || ch == '[':
			depth++
			cur.WriteByte(ch)
		case ch == ')' || ch == ']':
			depth--
			cur.WriteByte(ch)
		case ch == ' ' && depth == 0:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func parseSet(set string, max int, byUID bool, msgs []*fakeMsg) map[int]bool {
	sel := map[int]bool{}
	for _, part := range strings.Split(set, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		conv := func(x string) int {
			if x == "*" {
				if byUID && len(msgs) > 0 {
					return msgs[len(msgs)-1].uid
				}
				return max
			}
			n, _ := strconv.Atoi(x)
			return n
		}
		a := conv(lo)
		b := a
		if isRange {
			b = conv(hi)
		}
		if a > b {
			a, b = b, a
		}
		for i, m := range msgs {
			key := i + 1
			if byUID {
				key = m.uid
			}
			if key >= a && key <= b {
				sel[i] = true
			}
		}
	}
	return sel
}

func headerFields(raw []byte, fields []string) []byte {
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	head := raw
	if idx >= 0 {
		head = raw[:idx+2]
	}
	var out bytes.Buffer
	keep := false
	for _, ln := range strings.SplitAfter(string(head), "\r\n") {
		if ln == "" {
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' {
			if keep {
				out.WriteString(ln)
			}
			continue
		}
		name, _, _ := strings.Cut(ln, ":")
		keep = false
		for _, f := range fields {
			if strings.EqualFold(f, name) {
				keep = true
			}
		}
		if keep {
			out.WriteString(ln)
		}
	}
	out.WriteString("\r\n")
	return out.Bytes()
}

func (f *fakeIMAP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s)) }
	w("* OK fake IMAP ready\r\n")
	selected := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, rest, _ := strings.Cut(line, " ")
		f.mu.Lock()
		f.cmds = append(f.cmds, rest)
		f.mu.Unlock()
		args := splitArgs(rest)
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToUpper(args[0])
		args = args[1:]
		byUID := false
		if cmd == "UID" && len(args) > 0 {
			byUID = true
			cmd = strings.ToUpper(args[0])
			args = args[1:]
		}
		f.mu.Lock()
		switch cmd {
		case "LOGIN":
			if len(args) == 2 && args[0] == f.user && args[1] == f.pass {
				w(tag + " OK logged in\r\n")
			} else {
				w(tag + " NO [AUTHENTICATIONFAILED] bad credentials\r\n")
			}
		case "NOOP":
			w(tag + " OK\r\n")
		case "LOGOUT":
			w("* BYE\r\n" + tag + " OK\r\n")
			f.mu.Unlock()
			return
		case "SELECT", "EXAMINE":
			box := args[0]
			if _, ok := f.boxes[box]; !ok {
				w(tag + " NO no such mailbox\r\n")
				break
			}
			selected = box
			w(fmt.Sprintf("* %d EXISTS\r\n* OK [UIDVALIDITY 7] ok\r\n* OK [UIDNEXT %d] ok\r\n", len(f.boxes[box]), f.next[box]+1))
			w(tag + " OK [READ-WRITE] done\r\n")
		case "LIST":
			w("* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n")
			w("* LIST (\\HasNoChildren \\Trash) \"/\" \"[Gmail]/Trash\"\r\n")
			w("* LIST (\\HasNoChildren \\Sent) \"/\" \"[Gmail]/Sent Mail\"\r\n")
			w(tag + " OK\r\n")
		case "SEARCH":
			msgs := f.boxes[selected]
			var res []string
			if byUID {
				for _, m := range msgs {
					res = append(res, strconv.Itoa(m.uid))
				}
			} else {
				sel := parseSet(args[len(args)-1], len(msgs), false, msgs)
				for i, m := range msgs {
					if sel[i] && !m.flags["\\Seen"] {
						res = append(res, strconv.Itoa(i+1))
					}
				}
			}
			w("* SEARCH " + strings.Join(res, " ") + "\r\n" + tag + " OK\r\n")
		case "FETCH":
			msgs := f.boxes[selected]
			sel := parseSet(args[0], len(msgs), byUID, msgs)
			items := strings.ToUpper(args[1])
			var idxs []int
			for i := range sel {
				idxs = append(idxs, i)
			}
			sort.Ints(idxs)
			for _, i := range idxs {
				m := msgs[i]
				var fl []string
				for k := range m.flags {
					fl = append(fl, k)
				}
				sort.Strings(fl)
				var buf bytes.Buffer
				fmt.Fprintf(&buf, "* %d FETCH (UID %d FLAGS (%s)", i+1, m.uid, strings.Join(fl, " "))
				switch {
				case strings.Contains(items, "HEADER.FIELDS"):
					a := strings.Index(items, "(FROM")
					if a < 0 {
						a = strings.Index(items, "HEADER.FIELDS (") + len("HEADER.FIELDS ")
					}
					b := strings.Index(items[a:], ")") + a
					fields := strings.Fields(items[a+1 : b])
					data := headerFields(m.raw, fields)
					fmt.Fprintf(&buf, " BODY[HEADER.FIELDS (%s)] {%d}\r\n", strings.Join(fields, " "), len(data))
					buf.Write(data)
				case strings.Contains(items, "BODY.PEEK[]"):
					fmt.Fprintf(&buf, " BODY[] {%d}\r\n", len(m.raw))
					buf.Write(m.raw)
				}
				buf.WriteString(")\r\n")
				_, _ = c.Write(buf.Bytes())
			}
			w(tag + " OK fetched\r\n")
		case "STORE":
			msgs := f.boxes[selected]
			sel := parseSet(args[0], len(msgs), byUID, msgs)
			op := strings.ToUpper(args[1])
			flags := strings.Fields(strings.Trim(args[2], "()"))
			if strings.Contains(op, "X-GM-LABELS") {
				w(tag + " OK\r\n")
				break
			}
			for i := range sel {
				for _, fl := range flags {
					if strings.HasPrefix(op, "+") {
						msgs[i].flags[fl] = true
					} else {
						delete(msgs[i].flags, fl)
					}
				}
			}
			w(tag + " OK stored\r\n")
		case "COPY":
			msgs := f.boxes[selected]
			sel := parseSet(args[0], len(msgs), byUID, msgs)
			dest := args[1]
			if _, ok := f.boxes[dest]; !ok {
				w(tag + " NO [TRYCREATE] no such mailbox\r\n")
				break
			}
			for i, m := range msgs {
				if sel[i] {
					f.next[dest]++
					f.boxes[dest] = append(f.boxes[dest], &fakeMsg{uid: f.next[dest], flags: map[string]bool{}, raw: m.raw})
				}
			}
			w(tag + " OK copied\r\n")
		case "EXPUNGE":
			var keep []*fakeMsg
			n := 0
			for _, m := range f.boxes[selected] {
				if m.flags["\\Deleted"] {
					n++
					w(fmt.Sprintf("* %d EXPUNGE\r\n", len(keep)+1))
					continue
				}
				keep = append(keep, m)
			}
			f.boxes[selected] = keep
			w(tag + " OK expunged\r\n")
		default:
			w(tag + " BAD unknown command\r\n")
		}
		f.mu.Unlock()
	}
}

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
