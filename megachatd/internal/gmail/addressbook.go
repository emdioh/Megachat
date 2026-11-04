package gmail

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

const (
	addressBookWindow = 500
	addressBookTTL    = 15 * time.Minute
	addressBookMax    = 2000
	addressBookFetch  = "(BODY.PEEK[HEADER.FIELDS (FROM TO CC DATE)])"
)

var noreplyRE = regexp.MustCompile(`(?i)(^|[._+-])(no-?reply|do-?not-?reply|mailer-daemon|bounces?)([._+-]|@)`)

type addrBookCache struct {
	at      time.Time
	fp      string
	entries []api.GmailAddressEntry
}

// ClearAddressBookCache forgets the cached address book.
func (s *Service) ClearAddressBookCache() {
	s.abMu.Lock()
	s.abCache = nil
	s.abMu.Unlock()
}

type abEntry struct {
	email, name string
	score       float64
	lastTS      int64
}

type weightedHeader struct {
	name   string
	weight float64
}

func collectAddresses(entries map[string]*abEntry, order *[]string, headers []byte, weights []weightedHeader, own string) {
	root := parseMIME(headers, maxMIMEDepth)
	var dateTS int64
	if d := parseDateTS(root.Header.Get("Date")); d != nil {
		dateTS = *d
	}
	for _, w := range weights {
		vals := root.Header.Values(w.name)
		for _, p := range getAddresses(vals) {
			email := strings.ToLower(strings.TrimSpace(p.Email))
			if email == "" || !strings.Contains(email, "@") || email == own ||
				noreplyRE.MatchString(email) || strings.ContainsAny(email, ` <>,;"`) {
				continue
			}
			name := strings.TrimSpace(strings.Trim(strings.TrimSpace(decodeMIMEHeader(p.Name)), `"`))
			e := entries[email]
			if e == nil {
				e = &abEntry{email: email}
				entries[email] = e
				*order = append(*order, email)
			}
			e.score += w.weight
			if name != "" && strings.ToLower(name) != email && dateTS >= e.lastTS {
				e.name = name
			}
			if dateTS > e.lastTS {
				e.lastTS = dateTS
			}
		}
	}
}

// FetchAddressBook returns addresses from the latest Sent and Inbox headers,
// best first (cached per account for 15 minutes).
func (s *Service) FetchAddressBook(ctx context.Context, refresh bool) ([]api.GmailAddressEntry, error) {
	st, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	fp := accountFingerprint(st)
	s.abMu.Lock()
	if c := s.abCache; !refresh && c != nil && c.fp == fp && s.now().Sub(c.at) < addressBookTTL {
		out := append([]api.GmailAddressEntry(nil), c.entries...)
		s.abMu.Unlock()
		return out, nil
	}
	s.abMu.Unlock()
	own := strings.ToLower(strings.TrimSpace(st.Address))
	entries := map[string]*abEntry{}
	var order []string
	inbox, err := s.validateFolder(&st.Folder)
	if err != nil {
		return nil, err
	}
	err = s.withConn(ctx, st, func(c *imapConn) error {
		sent := findSpecialMailbox(c, `\sent`, "[Gmail]/Sent Mail")
		sources := []struct {
			folder  string
			weights []weightedHeader
		}{
			{sent, []weightedHeader{{"To", 3}, {"Cc", 2}}},
			{inbox, []weightedHeader{{"From", 1}, {"To", 0.25}, {"Cc", 0.25}}},
		}
		for _, src := range sources {
			if err := c.selectFolder(src.folder, true); err != nil {
				if _, ok := err.(*imapStatusError); ok {
					continue
				}
				return err
			}
			if c.exists <= 0 {
				continue
			}
			first := max(1, c.exists-addressBookWindow+1)
			res, err := c.command("FETCH", raw(strconv.Itoa(first)+":"+strconv.Itoa(c.exists)), raw(addressBookFetch))
			if err != nil {
				if _, ok := err.(*imapStatusError); ok {
					continue
				}
				return err
			}
			for _, it := range fetchItems(res) {
				if b, ok := it.firstBody(); ok {
					collectAddresses(entries, &order, b, src.weights, own)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	list := make([]*abEntry, 0, len(order))
	for _, k := range order {
		list = append(list, entries[k])
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].lastTS > list[j].lastTS
	})
	if len(list) > addressBookMax {
		list = list[:addressBookMax]
	}
	out := make([]api.GmailAddressEntry, 0, len(list))
	for _, e := range list {
		out = append(out, api.GmailAddressEntry{
			Email: e.email, Name: e.name, Score: math.Round(e.score*100) / 100, LastTs: float64(e.lastTS),
		})
	}
	s.abMu.Lock()
	s.abCache = &addrBookCache{at: s.now(), fp: fp, entries: out}
	s.abMu.Unlock()
	return append([]api.GmailAddressEntry(nil), out...), nil
}
