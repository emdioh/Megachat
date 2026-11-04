package gmail

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

const listFetchItem = "(FLAGS BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE CONTENT-TYPE CONTENT-DISPOSITION)])"

func uidSet(uids []int64) string {
	parts := make([]string, len(uids))
	for i, u := range uids {
		parts[i] = strconv.FormatInt(u, 10)
	}
	return strings.Join(parts, ",")
}

// FetchUnreadStatus is fetch_unread_status: the unread count among the newest
// 200 messages plus UIDNEXT, from a read-only SELECT and one SEARCH UNSEEN.
func (s *Service) FetchUnreadStatus(ctx context.Context, folder *string) (UnreadStatus, error) {
	st, err := s.loadSettings()
	if err != nil {
		return UnreadStatus{}, err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return UnreadStatus{}, err
	}
	var out UnreadStatus
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, true); err != nil {
			return err
		}
		exists := c.exists
		if exists < 0 {
			return upstream("Stato cartella non disponibile")
		}
		if c.uidNext > 0 {
			v := c.uidNext
			out.UIDNext = &v
		}
		if exists == 0 {
			return nil
		}
		first := max(1, exists-listWindow+1)
		res, err := c.cmd("Stato cartella non disponibile", "SEARCH", raw("UNSEEN"), raw(strconv.Itoa(first)+":"+strconv.Itoa(exists)))
		if err != nil {
			return err
		}
		out.Unread = len(searchResults(res))
		return nil
	})
	return out, err
}

func summarySortLess(a, b listMsg) bool { // a sorts before b in descending order
	ak, bk := a.DateTS != nil, b.DateTS != nil
	if ak != bk {
		return ak
	}
	if ak && *a.DateTS != *b.DateTS {
		return *a.DateTS > *b.DateTS
	}
	return a.UID > b.UID
}

// FetchMessageList returns a date-descending page of message summaries.
func (s *Service) FetchMessageList(ctx context.Context, folder *string, offset, limit int, refresh bool) (api.GmailListOut, error) {
	st, err := s.loadSettings()
	if err != nil {
		return api.GmailListOut{}, err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return api.GmailListOut{}, err
	}
	pageSize, err := validateLimit(limit)
	if err != nil {
		return api.GmailListOut{}, err
	}
	start, err := validateOffset(offset)
	if err != nil {
		return api.GmailListOut{}, err
	}
	key := inboxKey{fv, accountFingerprint(st)}
	var epoch0 uint64
	if refresh {
		s.invalidateInbox(key)
		s.inboxMu.Lock()
		epoch0 = s.inboxEpoch
		s.inboxMu.Unlock()
	} else {
		s.inboxMu.Lock()
		if e := s.inboxGetLocked(key, s.now()); e != nil {
			out := listResponse(e, fv, start, pageSize)
			s.inboxMu.Unlock()
			return out, nil
		}
		epoch0 = s.inboxEpoch
		s.inboxMu.Unlock()
	}

	var uidvalidity int64
	var messages []listMsg
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, true); err != nil {
			return err
		}
		uidvalidity = c.uidValidity
		res, err := c.cmd("Ricerca messaggi fallita", "UID SEARCH", raw("ALL"))
		if err != nil {
			return err
		}
		set := map[int64]bool{}
		var uids []int64
		for _, u := range searchResults(res) {
			if !set[u] {
				set[u] = true
				uids = append(uids, u)
			}
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		if len(uids) > listWindow {
			uids = uids[:listWindow]
		}
		if len(uids) == 0 {
			return nil
		}
		res, err = c.cmd("Lettura messaggi fallita", "UID FETCH", raw(uidSet(uids)), raw(listFetchItem))
		if err != nil {
			return err
		}
		byUID := map[int64]fetchItem{}
		for _, it := range fetchItems(res) {
			if it.UID > 0 {
				byUID[it.UID] = it
			}
		}
		for _, uid := range uids {
			it, ok := byUID[uid]
			if !ok {
				continue
			}
			hdr, _ := it.firstBody()
			p := parseSummary(hdr)
			flags := canonicalFlags(it.Flags)
			seen := false
			for _, f := range flags {
				if strings.EqualFold(f, `\Seen`) {
					seen = true
				}
			}
			messages = append(messages, listMsg{
				UID: int(uid), From: p.From, To: p.To, Subject: p.Subject, DateTS: p.DateTS,
				HasAttachments: p.HasAttachments, Seen: seen, Flags: flags,
			})
		}
		sort.SliceStable(messages, func(i, j int) bool { return summarySortLess(messages[i], messages[j]) })
		return nil
	})
	if err != nil {
		return api.GmailListOut{}, err
	}
	entry := &inboxEntry{uidvalidity: uidvalidity, messages: messages, expires: s.now().Add(listCacheTTL)}
	s.inboxMu.Lock()
	s.inboxPutLocked(key, entry, epoch0)
	s.inboxMu.Unlock()
	return listResponse(entry, fv, start, pageSize), nil
}

func listResponse(e *inboxEntry, folder string, start, pageSize int) api.GmailListOut {
	total := len(e.messages)
	lo := min(start, total)
	hi := min(start+pageSize, total)
	out := make([]api.GmailSummary, 0, hi-lo)
	for _, m := range e.messages[lo:hi] {
		out = append(out, api.GmailSummary{
			UID: m.UID, From: m.From, To: m.To, Subject: m.Subject, DateTs: m.DateTS,
			Snippet: "", HasAttachments: m.HasAttachments, Seen: m.Seen,
			Flags: append([]string{}, m.Flags...),
		})
	}
	return api.GmailListOut{
		UIDValidity: int(e.uidvalidity), Folder: folder, Offset: start, Limit: pageSize,
		Window: listWindow, Total: total, HasMore: start+pageSize < total, Messages: out,
	}
}

func (s *Service) fetchFull(c *imapConn, uid int64) (fetchItem, []byte, error) {
	res, err := c.cmd("Lettura messaggio fallita", "UID FETCH", raw(strconv.FormatInt(uid, 10)), raw("(FLAGS BODY.PEEK[])"))
	if err != nil {
		return fetchItem{}, nil, err
	}
	for _, it := range fetchItems(res) {
		if it.UID == uid || it.UID == 0 {
			body, _ := it.Body["BODY[]"]
			return it, body, nil
		}
	}
	return fetchItem{}, nil, notFound("Messaggio non trovato")
}

// FetchMessage returns the full detail of one message.
func (s *Service) FetchMessage(ctx context.Context, uid int, folder *string) (api.GmailMessageDetail, error) {
	st, err := s.loadSettings()
	if err != nil {
		return api.GmailMessageDetail{}, err
	}
	u, err := validateUID(uid)
	if err != nil {
		return api.GmailMessageDetail{}, err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return api.GmailMessageDetail{}, err
	}
	var detail api.GmailMessageDetail
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, true); err != nil {
			return err
		}
		it, body, err := s.fetchFull(c, u)
		if err != nil {
			return err
		}
		pm := s.parseFull(body, fv, u)
		infos := make([]api.GmailAttachmentInfo, 0, len(pm.Attachments))
		for _, a := range pm.Attachments {
			if a.Inline {
				s.inlineCachePut(fv, u, a.Index, a.Data)
			}
			info := api.GmailAttachmentInfo{Index: a.Index, Filename: a.Filename, ContentType: a.ContentType,
				Size: a.Size, Inline: api.Ptr(a.Inline)}
			if a.ContentID != "" {
				info.ContentID = api.Ptr(a.ContentID)
			}
			infos = append(infos, info)
		}
		uidOut := int(it.UID)
		if uidOut == 0 {
			uidOut = uid
		}
		detail = api.GmailMessageDetail{
			UID: uidOut, From: pm.From, To: pm.To, Cc: pm.Cc, Subject: pm.Subject, DateTs: pm.DateTS,
			MessageID: pm.MessageID, InReplyTo: pm.InReplyTo, References: pm.References,
			Text: pm.Text, HTML: pm.HTML, Attachments: infos,
		}
		return nil
	})
	return detail, err
}

// FetchAttachment returns one attachment using the detail's MIME index.
func (s *Service) FetchAttachment(ctx context.Context, uid, index int, folder *string) (AttachmentDownload, error) {
	if index < 0 {
		return AttachmentDownload{}, validation("Indice allegato non valido")
	}
	st, err := s.loadSettings()
	if err != nil {
		return AttachmentDownload{}, err
	}
	u, err := validateUID(uid)
	if err != nil {
		return AttachmentDownload{}, err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return AttachmentDownload{}, err
	}
	var out AttachmentDownload
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, true); err != nil {
			return err
		}
		_, body, err := s.fetchFull(c, u)
		if err != nil {
			return err
		}
		atts := extractAttachments(parseMIME(body, 0))
		if index >= len(atts) {
			return notFound("Allegato non trovato")
		}
		a := atts[index]
		if len(a.Data) > maxDownloadBytes {
			return tooLarge("Allegato troppo grande")
		}
		out = AttachmentDownload{Filename: a.Filename, ContentType: a.ContentType, Data: a.Data}
		return nil
	})
	return out, err
}

func (s *Service) setFlag(ctx context.Context, uid int, folder *string, add bool) error {
	st, err := s.loadSettings()
	if err != nil {
		return err
	}
	u, err := validateUID(uid)
	if err != nil {
		return err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return err
	}
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, false); err != nil {
			return err
		}
		op := "-FLAGS"
		if add {
			op = "+FLAGS"
		}
		_, err := c.cmd("Aggiornamento flag fallito", "UID STORE", raw(strconv.FormatInt(u, 10)), raw(op), raw(`(\Seen)`))
		return err
	})
	if err != nil {
		return err
	}
	s.setSeenCached(inboxKey{fv, accountFingerprint(st)}, int(u), add)
	return nil
}

// MarkRead sets \Seen.
func (s *Service) MarkRead(ctx context.Context, uid int, folder *string) error {
	return s.setFlag(ctx, uid, folder, true)
}

// MarkUnread clears \Seen.
func (s *Service) MarkUnread(ctx context.Context, uid int, folder *string) error {
	return s.setFlag(ctx, uid, folder, false)
}

// Archive removes Gmail's \Inbox label (INBOX only).
func (s *Service) Archive(ctx context.Context, uid int, folder *string) error {
	st, err := s.loadSettings()
	if err != nil {
		return err
	}
	u, err := validateUID(uid)
	if err != nil {
		return err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return err
	}
	if strings.ToUpper(fv) != "INBOX" {
		return validation("Si possono archiviare solo i messaggi della Posta in arrivo")
	}
	defer s.invalidateInboxAll()
	return s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, false); err != nil {
			return err
		}
		us := strconv.FormatInt(u, 10)
		res, err := c.cmd("Lettura messaggio fallita", "UID FETCH", raw(us), raw("(FLAGS)"))
		if err != nil {
			return err
		}
		if len(fetchItems(res)) == 0 {
			return notFound("Messaggio non trovato")
		}
		_, err = c.cmd("Archiviazione fallita", "UID STORE", raw(us), raw("-X-GM-LABELS"), raw(`(\Inbox)`))
		return err
	})
}

// findSpecialMailbox returns the mailbox carrying a special-use attribute.
func findSpecialMailbox(c *imapConn, flag, fallback string) string {
	res, err := c.command("LIST", str(""), str("*"))
	if err != nil {
		return fallback
	}
	for _, e := range listEntries(res) {
		if e.Flags[flag] {
			return e.Name
		}
	}
	return fallback
}

// DeleteResult is delete_messages' result.
type DeleteResult struct {
	Trashed int
	Missing []int
}

// DeleteMessages moves several messages to Trash on one connection.
func (s *Service) DeleteMessages(ctx context.Context, uids []int64, folder *string) (DeleteResult, error) {
	st, err := s.loadSettings()
	if err != nil {
		return DeleteResult{}, err
	}
	fv, err := s.folderOr(folder, st)
	if err != nil {
		return DeleteResult{}, err
	}
	defer s.invalidateInboxAll()
	res := DeleteResult{Missing: []int{}}
	err = s.withConn(ctx, st, func(c *imapConn) error {
		if err := c.selectOrFail(fv, false); err != nil {
			return err
		}
		r, err := c.cmd("Lettura messaggi fallita", "UID FETCH", raw(uidSet(uids)), raw("(FLAGS)"))
		if err != nil {
			return err
		}
		existing := map[int64]bool{}
		for _, it := range fetchItems(r) {
			existing[it.UID] = true
		}
		var have []int64
		for _, u := range uids {
			if existing[u] {
				have = append(have, u)
			} else {
				res.Missing = append(res.Missing, int(u))
			}
		}
		if len(have) == 0 {
			return nil
		}
		seq := uidSet(have)
		trash := findSpecialMailbox(c, `\trash`, "[Gmail]/Trash")
		if _, err := c.cmd("Spostamento nel Cestino fallito", "UID COPY", raw(seq), str(trash)); err != nil {
			return err
		}
		if _, err := c.cmd("Aggiornamento flag fallito", "UID STORE", raw(seq), raw("+FLAGS"), raw(`(\Deleted)`)); err != nil {
			return err
		}
		if _, err := c.cmd("Espulsione messaggio fallita", "EXPUNGE"); err != nil {
			return err
		}
		res.Trashed = len(have)
		return nil
	})
	return res, err
}

// DeleteMessage trashes one message, not_found if it no longer exists.
func (s *Service) DeleteMessage(ctx context.Context, uid int, folder *string) error {
	u, err := validateUID(uid)
	if err != nil {
		return err
	}
	r, err := s.DeleteMessages(ctx, []int64{u}, folder)
	if err != nil {
		return err
	}
	if r.Trashed == 0 {
		return notFound("Messaggio non trovato")
	}
	return nil
}

// ---- inline image cache -----------------------------------------------------------------------

const (
	inlinePrefetchPartMax = 2 * 1024 * 1024
	inlineCacheMaxBytes   = 32 * 1024 * 1024
	inlineCacheTTL        = time.Hour
)

type inlineKey struct {
	folder string
	uid    int64
	index  int
}

type inlineEntry struct {
	at   time.Time
	data []byte
}

func (s *Service) inlineCachePut(folder string, uid int64, index int, data []byte) {
	if len(data) == 0 || len(data) > inlinePrefetchPartMax {
		return
	}
	k := inlineKey{folder, uid, index}
	s.inlineMu.Lock()
	defer s.inlineMu.Unlock()
	s.inlineDeleteLocked(k)
	s.inlineCache[k] = &inlineEntry{at: s.now(), data: data}
	s.inlineOrder = append(s.inlineOrder, k)
	s.inlineBytes += len(data)
	for s.inlineBytes > inlineCacheMaxBytes && len(s.inlineOrder) > 0 {
		s.inlineDeleteLocked(s.inlineOrder[0])
	}
}

func (s *Service) inlineDeleteLocked(k inlineKey) {
	if e, ok := s.inlineCache[k]; ok {
		s.inlineBytes -= len(e.data)
		delete(s.inlineCache, k)
	}
	for i, o := range s.inlineOrder {
		if o == k {
			s.inlineOrder = append(s.inlineOrder[:i], s.inlineOrder[i+1:]...)
			break
		}
	}
}

func (s *Service) cachedInlineImage(folder string, uid int64, index int) []byte {
	k := inlineKey{folder, uid, index}
	s.inlineMu.Lock()
	defer s.inlineMu.Unlock()
	e := s.inlineCache[k]
	if e == nil {
		return nil
	}
	if s.now().Sub(e.at) > inlineCacheTTL {
		s.inlineDeleteLocked(k)
		return nil
	}
	return e.data
}
