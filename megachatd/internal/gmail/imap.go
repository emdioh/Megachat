package gmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// imapConn is one pooled IMAP session (the Python _PooledConn): a go-imap
// client plus the bookkeeping the pool needs. Every command runs under a
// watchdog (the equivalent of imaplib's socket timeout): when it fires the
// connection is closed and marked broken. A connection is used by one
// operation at a time, never concurrently.
type imapConn struct {
	cl       *imapclient.Client
	timeout  time.Duration
	lastUsed time.Time
	gen      int
	broken   atomic.Bool

	// Values of the last SELECT/EXAMINE.
	exists      int // -1 until a mailbox is selected
	uidValidity uint32
	uidNext     uint32
}

// archiveFlag is a sentinel flag: imapclient's STORE can only speak FLAGS,
// so Gmail's "UID STORE ... -X-GM-LABELS (\Inbox)" is written as
// "-FLAGS.SILENT (\MegachatArchive)" and rewritten on the wire.
const archiveFlag imap.Flag = `\MegachatArchive`

var (
	archiveWire    = []byte("-FLAGS.SILENT (" + string(archiveFlag) + ")")
	archiveWireNew = []byte(`-X-GM-LABELS.SILENT (\Inbox)`)
)

// rewriteConn rewrites the archive sentinel into the Gmail label command.
type rewriteConn struct{ net.Conn }

func (c rewriteConn) Write(b []byte) (int, error) {
	if !bytes.Contains(b, archiveWire) {
		return c.Conn.Write(b)
	}
	nb := bytes.Replace(b, archiveWire, archiveWireNew, 1)
	if _, err := c.Conn.Write(nb); err != nil {
		return 0, err
	}
	return len(b), nil
}

// dialIMAP connects with implicit TLS, waits for the greeting and logs in.
func dialIMAP(ctx context.Context, host string, port int, timeout time.Duration, cfg *tls.Config) (*imapConn, error) {
	var tc *tls.Config
	if cfg != nil {
		tc = cfg.Clone()
	} else {
		tc = &tls.Config{}
	}
	if tc.ServerName == "" && !tc.InsecureSkipVerify {
		tc.ServerName = host
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: tc}
	raw, err := d.DialContext(dctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	c := &imapConn{
		cl:      imapclient.New(rewriteConn{raw}, nil),
		timeout: timeout, lastUsed: time.Now(), exists: -1,
	}
	if err := c.do(c.cl.WaitGreeting); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// abort closes the socket, failing any command in flight.
func (c *imapConn) abort() {
	c.broken.Store(true)
	_ = c.cl.Close()
}

// close logs out politely (short timeout) and closes the socket.
func (c *imapConn) close() {
	if c == nil || c.cl == nil {
		return
	}
	if !c.broken.Load() {
		t := time.AfterFunc(500*time.Millisecond, func() { _ = c.cl.Close() })
		_ = c.cl.Logout().Wait()
		t.Stop()
	}
	c.broken.Store(true)
	_ = c.cl.Close()
}

// alive reports whether the socket is still open.
func (c *imapConn) alive() bool {
	if c.broken.Load() {
		return false
	}
	select {
	case <-c.cl.Closed():
		return false
	default:
		return true
	}
}

// isStatus reports whether err is a tagged NO/BAD completion (the connection
// stays usable).
func isStatus(err error) bool {
	var se *imap.Error
	return errors.As(err, &se)
}

// do runs fn under the watchdog and marks the connection broken on any
// error that is not a tagged NO/BAD.
func (c *imapConn) do(fn func() error) error {
	if c.broken.Load() {
		return errors.New("imap: closed")
	}
	t := time.AfterFunc(c.timeout, c.abort)
	err := fn()
	t.Stop()
	if err != nil && !isStatus(err) {
		c.broken.Store(true)
	}
	if err == nil {
		c.lastUsed = time.Now()
	}
	return err
}

// call is do with a NO/BAD completion mapped to upstream(failMsg).
func (c *imapConn) call(failMsg string, fn func() error) error {
	err := c.do(fn)
	if err != nil && isStatus(err) {
		return upstream(failMsg)
	}
	return err
}

func (c *imapConn) login(user, pass string) error {
	return c.do(func() error { return c.cl.Login(user, pass).Wait() })
}

func (c *imapConn) noop() error {
	return c.do(func() error { return c.cl.Noop().Wait() })
}

// selectFolder selects (or examines) a mailbox and records its counters.
func (c *imapConn) selectFolder(name string, readonly bool) error {
	c.exists, c.uidValidity, c.uidNext = -1, 0, 0
	return c.do(func() error {
		d, err := c.cl.Select(name, &imap.SelectOptions{ReadOnly: readonly}).Wait()
		if err != nil {
			return err
		}
		c.exists, c.uidValidity, c.uidNext = int(d.NumMessages), d.UIDValidity, uint32(d.UIDNext)
		return nil
	})
}

func (c *imapConn) selectOrFail(folder string, readonly bool) error {
	err := c.selectFolder(folder, readonly)
	if err != nil && isStatus(err) {
		return upstream("Cartella non disponibile")
	}
	return err
}

func uidNumSet(uids []int64) imap.UIDSet {
	var s imap.UIDSet
	for _, u := range uids {
		s.AddNum(imap.UID(u))
	}
	return s
}

func uidOne(uid int64) imap.UIDSet { return uidNumSet([]int64{uid}) }

// uidSearchAll is UID SEARCH ALL.
func (c *imapConn) uidSearchAll(failMsg string) ([]int64, error) {
	var out []int64
	err := c.call(failMsg, func() error {
		d, err := c.cl.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return err
		}
		for _, u := range d.AllUIDs() {
			out = append(out, int64(u))
		}
		return nil
	})
	return out, err
}

// countUnseen is SEARCH UNSEEN first:last (sequence numbers).
func (c *imapConn) countUnseen(failMsg string, first, last int) (int, error) {
	var set imap.SeqSet
	set.AddRange(uint32(first), uint32(last))
	n := 0
	err := c.call(failMsg, func() error {
		d, err := c.cl.Search(&imap.SearchCriteria{
			SeqNum: []imap.SeqSet{set}, NotFlag: []imap.Flag{imap.FlagSeen},
		}, nil).Wait()
		if err != nil {
			return err
		}
		n = len(d.AllSeqNums())
		return nil
	})
	return n, err
}

// fetched is one FETCH response with the sections this package asks for.
type fetched struct {
	Seq   int
	UID   int64
	Flags []string
	buf   *imapclient.FetchMessageBuffer
}

func (f fetched) section(s *imap.FetchItemBodySection) []byte {
	b, _ := f.lookup(s)
	return b
}

// lookup is section plus whether the server returned it at all.
func (f fetched) lookup(s *imap.FetchItemBodySection) ([]byte, bool) {
	if f.buf == nil {
		return nil, false
	}
	for _, sb := range f.buf.BodySection {
		if sb.Section.Specifier == s.Specifier && intsEqual(sb.Section.Part, s.Part) &&
			stringsEqualFold(sb.Section.HeaderFields, s.HeaderFields) {
			return sb.Bytes, true
		}
	}
	return nil, false
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringsEqualFold(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func headerFieldsSection(fields ...string) *imap.FetchItemBodySection {
	return &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, HeaderFields: fields, Peek: true}
}

func fullSection() *imap.FetchItemBodySection { return &imap.FetchItemBodySection{Peek: true} }

func partSection(path []int) *imap.FetchItemBodySection {
	return &imap.FetchItemBodySection{Part: path, Peek: true}
}

func collectFetched(msgs []*imapclient.FetchMessageBuffer) []fetched {
	out := make([]fetched, 0, len(msgs))
	for _, m := range msgs {
		f := fetched{Seq: int(m.SeqNum), UID: int64(m.UID), buf: m}
		for _, fl := range m.Flags {
			if s := string(fl); s != "" {
				f.Flags = append(f.Flags, s)
			}
		}
		out = append(out, f)
	}
	return out
}

// fetch runs FETCH / UID FETCH and collects every response.
func (c *imapConn) fetch(failMsg string, set imap.NumSet, o *imap.FetchOptions) ([]fetched, error) {
	var out []fetched
	err := c.call(failMsg, func() error {
		msgs, err := c.cl.Fetch(set, o).Collect()
		if err != nil {
			return err
		}
		out = collectFetched(msgs)
		return nil
	})
	return out, err
}

// storeFlags is UID STORE +FLAGS / -FLAGS.
func (c *imapConn) storeFlags(failMsg string, uids imap.UIDSet, add bool, flags ...imap.Flag) error {
	op := imap.StoreFlagsDel
	if add {
		op = imap.StoreFlagsAdd
	}
	return c.call(failMsg, func() error {
		return c.cl.Store(uids, &imap.StoreFlags{Op: op, Silent: true, Flags: flags}, nil).Close()
	})
}

// removeInboxLabel is UID STORE -X-GM-LABELS (\Inbox) (see archiveFlag).
func (c *imapConn) removeInboxLabel(failMsg string, uid int64) error {
	return c.storeFlags(failMsg, uidOne(uid), false, archiveFlag)
}

func (c *imapConn) copyTo(failMsg string, uids imap.UIDSet, mailbox string) error {
	return c.call(failMsg, func() error {
		_, err := c.cl.Copy(uids, mailbox).Wait()
		return err
	})
}

func (c *imapConn) expunge(failMsg string) error {
	return c.call(failMsg, func() error { return c.cl.Expunge().Close() })
}

// findSpecialMailbox returns the mailbox carrying a special-use attribute
// (e.g. `\Trash`), or fallback.
func findSpecialMailbox(c *imapConn, attr, fallback string) string {
	name := ""
	err := c.do(func() error {
		list, err := c.cl.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		for _, e := range list {
			for _, a := range e.Attrs {
				if strings.EqualFold(string(a), attr) {
					name = e.Mailbox
					return nil
				}
			}
		}
		return nil
	})
	if err != nil || name == "" {
		return fallback
	}
	return name
}
