package gmail

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emersion/go-imap/v2"
)

// listHeaderSection is the header-only list fetch (_LIST_FETCH_ITEM).
var listHeaderSection = headerFieldsSection("FROM", "TO", "SUBJECT", "DATE", "CONTENT-TYPE", "CONTENT-DISPOSITION")

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
			v := int64(c.uidNext)
			out.UIDNext = &v
		}
		if exists == 0 {
			return nil
		}
		first := max(1, exists-listWindow+1)
		n, err := c.countUnseen("Stato cartella non disponibile", first, exists)
		out.Unread = n
		return err
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
		uidvalidity = int64(c.uidValidity)
		all, err := c.uidSearchAll("Ricerca messaggi fallita")
		if err != nil {
			return err
		}
		set := map[int64]bool{}
		var uids []int64
		for _, u := range all {
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
		items, err := c.fetch("Lettura messaggi fallita", uidNumSet(uids), &imap.FetchOptions{
			UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{listHeaderSection},
		})
		if err != nil {
			return err
		}
		byUID := map[int64]fetched{}
		for _, it := range items {
			if it.UID > 0 {
				byUID[it.UID] = it
			}
		}
		for _, uid := range uids {
			it, ok := byUID[uid]
			if !ok {
				continue
			}
			p := parseSummary(it.section(listHeaderSection))
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

// fetchFull is _fetch_full_on: the whole message in one UID FETCH.
func (s *Service) fetchFull(c *imapConn, uid int64) (fetched, []byte, error) {
	items, err := c.fetch("Lettura messaggio fallita", uidOne(uid), &imap.FetchOptions{
		UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{fullSection()},
	})
	if err != nil {
		return fetched{}, nil, err
	}
	for _, it := range items {
		if it.UID == uid || it.UID == 0 {
			return it, it.section(fullSection()), nil
		}
	}
	return fetched{}, nil, notFound("Messaggio non trovato")
}

func publicAttachments(atts []attachment) []api.GmailAttachmentInfo {
	infos := make([]api.GmailAttachmentInfo, 0, len(atts))
	for _, a := range atts {
		info := api.GmailAttachmentInfo{Index: a.Index, Filename: a.Filename, ContentType: a.ContentType,
			Size: a.Size, Inline: api.Ptr(a.Inline)}
		if a.ContentID != "" {
			info.ContentID = api.Ptr(a.ContentID)
		}
		infos = append(infos, info)
	}
	return infos
}

func detailOf(uid int, pm *parsedMessage) api.GmailMessageDetail {
	return api.GmailMessageDetail{
		UID: uid, From: pm.From, To: pm.To, Cc: pm.Cc, Subject: pm.Subject, DateTs: pm.DateTS,
		MessageID: pm.MessageID, InReplyTo: pm.InReplyTo, References: pm.References,
		Text: pm.Text, HTML: pm.HTML, Attachments: publicAttachments(pm.Attachments),
	}
}

// FetchMessage returns the full detail of one message: through BODYSTRUCTURE
// and a partial fetch of the text parts when possible, else the whole message.
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
		var pm *partMap
		if rec, err := s.fetchStructure(c, u); err != nil {
			return err
		} else if rec != nil {
			pm = rec.index
			cands := inlineImageCandidates(pm)
			payloads, err := s.fetchRenderParts(c, u, pm, cands)
			if errors.Is(err, errPartFetch) && len(cands) > 0 {
				// The extra image parts must never cost the fast path.
				payloads, err = s.fetchRenderParts(c, u, pm, nil)
			}
			if err != nil && !errors.Is(err, errPartFetch) {
				return err
			}
			if err == nil {
				parsed, aerr := s.assembleDetail(u, rec, payloads, fv)
				if aerr == nil {
					s.cacheInlineImages(fv, u, pm, parsed.Attachments, payloads)
					detail = detailOf(int(u), parsed)
					return nil
				}
			}
		}
		it, body, err := s.fetchFull(c, u)
		if err != nil {
			return err
		}
		parsed := s.parseFull(body, fv, u)
		for _, a := range parsed.Attachments {
			if a.Inline {
				s.inlineCachePut(fv, u, a.Index, a.Data)
			}
		}
		uidOut := int(it.UID)
		if uidOut == 0 {
			uidOut = uid
		}
		detail = detailOf(uidOut, parsed)
		return nil
	})
	return detail, err
}

// FetchAttachment returns one attachment using the detail's MIME index, with a
// single-part fetch when the BODYSTRUCTURE allows it.
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
		if rec, err := s.fetchStructure(c, u); err != nil {
			return err
		} else if rec != nil {
			if index >= len(rec.index.attachments) {
				return notFound("Allegato non trovato")
			}
			part := rec.index.attachments[index]
			switch part.encoding {
			case "7bit", "8bit", "binary":
				if part.octets > maxDownloadBytes {
					return tooLarge("Allegato troppo grande")
				}
			}
			payloads, perr := s.fetchPartPayloads(c, u, []*partInfo{part})
			if perr != nil && !errors.Is(perr, errPartFetch) {
				return perr
			}
			if perr == nil {
				if payload, ok := payloads[pathKey(part.path)]; ok {
					if data, derr := decodeFetchedBytes(part, payload); derr == nil {
						if len(data) > maxDownloadBytes {
							return tooLarge("Allegato troppo grande")
						}
						out = AttachmentDownload{Filename: safePartFilename(part, index), ContentType: part.contentType(), Data: data}
						return nil
					}
				}
			}
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
		return c.storeFlags("Aggiornamento flag fallito", uidOne(u), add, imap.FlagSeen)
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
		items, err := c.fetch("Lettura messaggio fallita", uidOne(u), &imap.FetchOptions{UID: true, Flags: true})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return notFound("Messaggio non trovato")
		}
		return c.removeInboxLabel("Archiviazione fallita", u)
	})
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
		items, err := c.fetch("Lettura messaggi fallita", uidNumSet(uids), &imap.FetchOptions{UID: true, Flags: true})
		if err != nil {
			return err
		}
		existing := map[int64]bool{}
		for _, it := range items {
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
		seq := uidNumSet(have)
		trash := findSpecialMailbox(c, `\Trash`, "[Gmail]/Trash")
		if err := c.copyTo("Spostamento nel Cestino fallito", seq, trash); err != nil {
			return err
		}
		if err := c.storeFlags("Aggiornamento flag fallito", seq, true, imap.FlagDeleted); err != nil {
			return err
		}
		if err := c.expunge("Espulsione messaggio fallita"); err != nil {
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
