package gmail

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A small IMAP4rev1 client: just what server/gmail.py uses from imaplib
// (LOGIN, SELECT/EXAMINE, UID SEARCH/FETCH/STORE/COPY, SEARCH, FETCH, LIST,
// EXPUNGE, NOOP, LOGOUT). See doc.go for why go-imap's imapclient is not used.

const maxLiteral = 256 << 20

var errIMAPNo = errors.New("imap: NO")

// imapStatusError is a tagged NO/BAD completion.
type imapStatusError struct {
	Status string
	Text   string
}

func (e *imapStatusError) Error() string { return "imap " + e.Status }

// token is one parsed IMAP value: string (atom/quoted), []byte (literal),
// []any (parenthesised list) or nil (NIL).
type token = any

type untagged struct {
	// Raw is the response line with literals replaced by \x00<n>\x00.
	Raw      []byte
	Literals [][]byte
}

type imapResult struct {
	Untagged []untagged
	Status   string
	Text     string
}

type imapConn struct {
	conn    net.Conn
	r       *bufio.Reader
	tag     int
	timeout time.Duration

	// Last values seen in untagged responses (updated by every command).
	exists      int
	uidValidity int64
	uidNext     int64
	lastUsed    time.Time
	broken      bool
	gen         int
}

// dialIMAP connects with implicit TLS and reads the greeting.
func dialIMAP(host string, port int, timeout time.Duration, cfg *tls.Config) (*imapConn, error) {
	d := net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	raw, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	var tc *tls.Config
	if cfg != nil {
		tc = cfg.Clone()
	} else {
		tc = &tls.Config{}
	}
	if tc.ServerName == "" && !tc.InsecureSkipVerify {
		tc.ServerName = host
	}
	_ = raw.SetDeadline(time.Now().Add(timeout))
	tlsConn := tls.Client(raw, tc)
	if err := tlsConn.Handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	c := &imapConn{conn: tlsConn, r: bufio.NewReaderSize(tlsConn, 64<<10), timeout: timeout, lastUsed: time.Now()}
	line, err := c.readLine()
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if !bytes.HasPrefix(line, []byte("* OK")) && !bytes.HasPrefix(line, []byte("* PREAUTH")) {
		_ = tlsConn.Close()
		return nil, errors.New("imap: unexpected greeting")
	}
	return c, nil
}

func (c *imapConn) close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = c.conn.Write([]byte("zz LOGOUT\r\n"))
	_ = c.conn.Close()
	c.conn = nil
}

func (c *imapConn) readLine() ([]byte, error) {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	line = bytes.TrimRight(line, "\r\n")
	return line, nil
}

// literalSize parses a trailing "{n}" / "{n+}".
func literalSize(line []byte) (int, bool) {
	if len(line) < 3 || line[len(line)-1] != '}' {
		return 0, false
	}
	open := bytes.LastIndexByte(line, '{')
	if open < 0 {
		return 0, false
	}
	num := line[open+1 : len(line)-1]
	num = bytes.TrimSuffix(num, []byte("+"))
	n, err := strconv.Atoi(string(num))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// readResponse reads one full response (line + literals + continuation lines).
func (c *imapConn) readResponse() (untagged, error) {
	var out untagged
	for {
		line, err := c.readLine()
		if err != nil {
			return out, err
		}
		n, ok := literalSize(line)
		if !ok {
			out.Raw = append(out.Raw, line...)
			return out, nil
		}
		if n > maxLiteral {
			return out, errors.New("imap: literal too large")
		}
		open := bytes.LastIndexByte(line, '{')
		out.Raw = append(out.Raw, line[:open]...)
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return out, err
		}
		out.Raw = append(out.Raw, 0)
		out.Raw = append(out.Raw, strconv.Itoa(len(out.Literals))...)
		out.Raw = append(out.Raw, 0)
		out.Literals = append(out.Literals, buf)
	}
}

// cmdArg is one command argument. Raw args are sent verbatim (atoms, lists,
// sequence sets); others are quoted or sent as synchronising literals.
type cmdArg struct {
	Raw string
	Str string
	Lit bool
}

func raw(s string) cmdArg { return cmdArg{Raw: s} }
func str(s string) cmdArg { return cmdArg{Str: s, Lit: false} }
func needsLiteral(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' || s[i] == '\n' || s[i] == 0 || s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// command runs one tagged command and collects its untagged responses.
func (c *imapConn) command(name string, args ...cmdArg) (*imapResult, error) {
	if c.conn == nil {
		return nil, errors.New("imap: closed")
	}
	c.tag++
	tag := fmt.Sprintf("G%d", c.tag)
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	var buf bytes.Buffer
	buf.WriteString(tag + " " + name)
	flush := func() error {
		_, err := c.conn.Write(buf.Bytes())
		buf.Reset()
		return err
	}
	for _, a := range args {
		buf.WriteByte(' ')
		switch {
		case a.Raw != "":
			buf.WriteString(a.Raw)
		case needsLiteral(a.Str):
			fmt.Fprintf(&buf, "{%d}\r\n", len(a.Str))
			if err := flush(); err != nil {
				c.broken = true
				return nil, err
			}
			for {
				line, err := c.readLine()
				if err != nil {
					c.broken = true
					return nil, err
				}
				if bytes.HasPrefix(line, []byte("+")) {
					break
				}
				if bytes.HasPrefix(line, []byte(tag+" ")) {
					return nil, &imapStatusError{Status: "BAD", Text: string(line)}
				}
			}
			buf.WriteString(a.Str)
		default:
			buf.WriteString(quote(a.Str))
		}
	}
	buf.WriteString("\r\n")
	if err := flush(); err != nil {
		c.broken = true
		return nil, err
	}
	res := &imapResult{}
	for {
		u, err := c.readResponse()
		if err != nil {
			c.broken = true
			return nil, err
		}
		if bytes.HasPrefix(u.Raw, []byte(tag+" ")) {
			rest := string(u.Raw[len(tag)+1:])
			status, text, _ := strings.Cut(rest, " ")
			res.Status = strings.ToUpper(status)
			res.Text = text
			break
		}
		c.observe(u)
		res.Untagged = append(res.Untagged, u)
	}
	c.lastUsed = time.Now()
	if res.Status != "OK" {
		return res, &imapStatusError{Status: res.Status, Text: res.Text}
	}
	return res, nil
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// observe tracks EXISTS / UIDVALIDITY / UIDNEXT.
func (c *imapConn) observe(u untagged) {
	line := string(u.Raw)
	if !strings.HasPrefix(line, "* ") {
		return
	}
	rest := line[2:]
	if n, kw, ok := strings.Cut(rest, " "); ok && strings.EqualFold(strings.TrimSpace(kw), "EXISTS") {
		if v, err := strconv.Atoi(n); err == nil {
			c.exists = v
		}
		return
	}
	for _, key := range []string{"UIDVALIDITY", "UIDNEXT"} {
		if i := strings.Index(strings.ToUpper(rest), "["+key+" "); i >= 0 {
			tail := rest[i+len(key)+2:]
			if j := strings.IndexByte(tail, ']'); j >= 0 {
				if v, err := strconv.ParseInt(strings.TrimSpace(tail[:j]), 10, 64); err == nil {
					if key == "UIDVALIDITY" {
						c.uidValidity = v
					} else {
						c.uidNext = v
					}
				}
			}
		}
	}
}

func (c *imapConn) login(user, pass string) error {
	_, err := c.command("LOGIN", str(user), str(pass))
	return err
}

// selectFolder selects (or examines) a mailbox and resets the counters.
func (c *imapConn) selectFolder(name string, readonly bool) error {
	c.exists, c.uidValidity, c.uidNext = -1, 0, 0
	cmd := "SELECT"
	if readonly {
		cmd = "EXAMINE"
	}
	_, err := c.command(cmd, str(name))
	return err
}

func (c *imapConn) noop() error {
	_, err := c.command("NOOP")
	return err
}

// ---- response parsing -------------------------------------------------------

type tokenizer struct {
	b    []byte
	pos  int
	lits [][]byte
}

func newTokenizer(u untagged, from int) *tokenizer {
	return &tokenizer{b: u.Raw, pos: from, lits: u.Literals}
}

func (t *tokenizer) skipSpace() {
	for t.pos < len(t.b) && (t.b[t.pos] == ' ' || t.b[t.pos] == '\t') {
		t.pos++
	}
}

func (t *tokenizer) done() bool {
	t.skipSpace()
	return t.pos >= len(t.b)
}

// next parses one value.
func (t *tokenizer) next() (token, error) {
	t.skipSpace()
	if t.pos >= len(t.b) {
		return nil, io.EOF
	}
	switch ch := t.b[t.pos]; {
	case ch == '(':
		t.pos++
		list := []any{}
		for {
			t.skipSpace()
			if t.pos >= len(t.b) {
				return nil, errors.New("imap: unterminated list")
			}
			if t.b[t.pos] == ')' {
				t.pos++
				return list, nil
			}
			v, err := t.next()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case ch == '"':
		t.pos++
		var out []byte
		for t.pos < len(t.b) {
			c := t.b[t.pos]
			if c == '\\' && t.pos+1 < len(t.b) {
				out = append(out, t.b[t.pos+1])
				t.pos += 2
				continue
			}
			if c == '"' {
				t.pos++
				return string(out), nil
			}
			out = append(out, c)
			t.pos++
		}
		return nil, errors.New("imap: unterminated string")
	case ch == 0:
		end := bytes.IndexByte(t.b[t.pos+1:], 0)
		if end < 0 {
			return nil, errors.New("imap: bad literal marker")
		}
		idx, err := strconv.Atoi(string(t.b[t.pos+1 : t.pos+1+end]))
		if err != nil || idx < 0 || idx >= len(t.lits) {
			return nil, errors.New("imap: bad literal index")
		}
		t.pos += end + 2
		return t.lits[idx], nil
	default:
		start := t.pos
		depth := 0
		for t.pos < len(t.b) {
			c := t.b[t.pos]
			if c == '[' {
				depth++
			} else if c == ']' {
				if depth > 0 {
					depth--
				}
			} else if depth == 0 && (c == ' ' || c == '(' || c == ')' || c == 0 || c == '"') {
				break
			}
			t.pos++
		}
		atom := string(t.b[start:t.pos])
		if strings.EqualFold(atom, "NIL") {
			return nil, nil
		}
		return atom, nil
	}
}

// firstBody returns the first BODY[...] section of a fetch item.
func (f fetchItem) firstBody() ([]byte, bool) {
	keys := make([]string, 0, len(f.Body))
	for k := range f.Body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, false
	}
	return f.Body[keys[0]], true
}

// fetchItem is one "* n FETCH (...)" response.
type fetchItem struct {
	Seq   int
	UID   int64
	Flags []string
	// Body maps an uppercased "BODY[...]" key (without .PEEK) to its data.
	Body map[string][]byte
}

// parseFetch decodes a FETCH response; ok=false for any other response.
func parseFetch(u untagged) (fetchItem, bool) {
	line := u.Raw
	if !bytes.HasPrefix(line, []byte("* ")) {
		return fetchItem{}, false
	}
	rest := line[2:]
	sp := bytes.IndexByte(rest, ' ')
	if sp < 0 {
		return fetchItem{}, false
	}
	seq, err := strconv.Atoi(string(rest[:sp]))
	if err != nil {
		return fetchItem{}, false
	}
	after := rest[sp+1:]
	if len(after) < 5 || !strings.EqualFold(string(after[:5]), "FETCH") {
		return fetchItem{}, false
	}
	t := &tokenizer{b: line, pos: len(line) - len(after) + 5, lits: u.Literals}
	v, err := t.next()
	list, ok := v.([]any)
	if err != nil || !ok {
		return fetchItem{}, false
	}
	item := fetchItem{Seq: seq, Body: map[string][]byte{}}
	for i := 0; i+1 < len(list); i += 2 {
		key, _ := list[i].(string)
		up := strings.ToUpper(key)
		switch {
		case up == "UID":
			if s, ok := list[i+1].(string); ok {
				item.UID, _ = strconv.ParseInt(s, 10, 64)
			}
		case up == "FLAGS":
			if fl, ok := list[i+1].([]any); ok {
				for _, f := range fl {
					if s, ok := f.(string); ok && s != "" {
						item.Flags = append(item.Flags, s)
					}
				}
			}
		case strings.HasPrefix(up, "BODY["):
			up = strings.Replace(up, "BODY.PEEK[", "BODY[", 1)
			switch d := list[i+1].(type) {
			case []byte:
				item.Body[up] = d
			case string:
				item.Body[up] = []byte(d)
			}
		}
	}
	return item, true
}

// fetchItems collects every FETCH response of a result.
func fetchItems(res *imapResult) []fetchItem {
	var out []fetchItem
	for _, u := range res.Untagged {
		if it, ok := parseFetch(u); ok {
			out = append(out, it)
		}
	}
	return out
}

// searchResults parses "* SEARCH n n n" lines.
func searchResults(res *imapResult) []int64 {
	var out []int64
	for _, u := range res.Untagged {
		line := string(u.Raw)
		if !strings.HasPrefix(strings.ToUpper(line), "* SEARCH") {
			continue
		}
		for _, f := range strings.Fields(line[len("* SEARCH"):]) {
			if v, err := strconv.ParseInt(f, 10, 64); err == nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// listEntry is a parsed "* LIST" response.
type listEntry struct {
	Flags map[string]bool // lowercased
	Name  string
}

func listEntries(res *imapResult) []listEntry {
	var out []listEntry
	for _, u := range res.Untagged {
		line := string(u.Raw)
		if len(line) < 6 || !strings.EqualFold(line[:6], "* LIST") {
			continue
		}
		t := newTokenizer(u, 6)
		fl, err := t.next()
		flags, ok := fl.([]any)
		if err != nil || !ok {
			continue
		}
		if _, err := t.next(); err != nil { // delimiter
			continue
		}
		nm, err := t.next()
		if err != nil {
			continue
		}
		var name string
		switch v := nm.(type) {
		case string:
			name = v
		case []byte:
			name = string(v)
		}
		if name == "" {
			continue
		}
		e := listEntry{Flags: map[string]bool{}, Name: name}
		for _, f := range flags {
			if s, ok := f.(string); ok {
				e.Flags[strings.ToLower(s)] = true
			}
		}
		out = append(out, e)
	}
	return out
}
