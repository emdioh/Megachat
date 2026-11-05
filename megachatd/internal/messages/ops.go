package messages

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// RegisterOps registers messages.page, messages.window and messages.snapshot.
// It panics on a duplicate name (the Registry's rule).
func RegisterOps(reg *ops.Registry, deps Deps) {
	s := &service{deps: deps, log: slog.Default()}
	reg.Register(ops.Op{
		Name: api.OpMessagesPage,
		Doc:  "Messages of a chat, oldest first (optionally refreshed from the backend).",
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			req, err := decodePage(params)
			if err != nil {
				return nil, err
			}
			return s.page(ctx, req)
		},
	})
	reg.Register(ops.Op{
		Name: api.OpMessagesWindow,
		Doc:  "One chat's canonical rows from the core's cache, optionally refreshed.",
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			req, err := decodeWindow(params)
			if err != nil {
				return nil, err
			}
			return s.window(ctx, req)
		},
	})
	reg.Register(ops.Op{
		Name: api.OpMessagesSnapshot,
		Doc:  "Every chat's canonical cache rows of a protocol, keyed by contact id.",
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			req, err := decodeSnapshot(params)
			if err != nil {
				return nil, err
			}
			return s.snapshot(ctx, req)
		},
	})
}

type service struct {
	deps Deps
	log  *slog.Logger
}

// resolveChat is _resolve_chat: the backend, the contact as the core knows it
// (a WhatsApp ghost @c.us resolves to the active @lid contact) and the
// canonical id.
func (s *service) resolveChat(ctx context.Context, protocol, contactID string) (proto.Backend, *proto.Contact, string) {
	backend, ok := s.deps.Manager().Get(protocol)
	if !ok {
		return nil, nil, contactID
	}
	if c, found := s.deps.FindContact(ctx, protocol, contactID); found {
		return backend, &c, c.ID
	}
	return backend, nil, contactID
}

// refreshHistory is _refresh_history: download the remote history of a chat
// into the backend (best effort). It reports whether the backend fetched it.
func (s *service) refreshHistory(ctx context.Context, backend proto.Backend, protocol, canonicalID string, limit int) bool {
	fetcher, ok := backend.(proto.HistoryFetcher)
	if !ok || canonicalID == "" {
		return false
	}
	msgs, err := fetcher.FetchHistory(ctx, canonicalID, limit)
	if err != nil {
		s.log.Debug("history refresh failed", "protocol", protocol, "contact", canonicalID, "err", err)
		return false
	}
	s.log.Debug("history refresh", "protocol", protocol, "contact", canonicalID, "n", len(msgs))
	return true
}

// loadRows is the SQLite read of _messages: read-union over the storage keys,
// cross-key dedup, intra-key twin merge, newest-limit trimming.
func (s *service) loadRows(ctx context.Context, protocol, contactID string, extraKeys []string, limit *int, since *int64) []msg {
	keys := []string{contactID}
	for _, k := range extraKeys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	q := store.MessageQuery{Protocol: protocol, Keys: keys, Since: since}
	if limit != nil {
		q.SQLLimit = *limit + store.MessagesLimitSlack
	}
	rows, err := s.deps.Store().ListMessages(ctx, q)
	if err != nil {
		s.log.Debug("messages query failed", "protocol", protocol, "contact", contactID, "err", err)
		return nil
	}
	if len(keys) > 1 {
		rows = crossKeyDedupRows(rows, contactID)
	}
	rows = dedupIntraKeyRows(rows)
	if limit != nil && len(rows) > *limit {
		rows = rows[len(rows)-*limit:]
	}
	return rows
}

// messageRows is _messages: the presentation rows of a chat, oldest first.
func (s *service) messageRows(ctx context.Context, protocol, contactID string, extraKeys []string, limit *int, since *int64) ([]api.MessageRow, error) {
	rows := s.loadRows(ctx, protocol, contactID, extraKeys, limit, since)
	cfg := s.deps.Config()
	b := &rowBuilder{
		st:        s.deps.Store(),
		cacheDir:  cfg.CacheDir(),
		lazyMedia: protocol == api.ProtocolWhatsApp && cfg.WALazyMedia(),
		protocol:  protocol,
	}
	out := b.build(ctx, rows)
	// Reactions stay single-key by design: the cross-key union covers the
	// messages, not the reaction aggregation.
	reactions, err := s.deps.Store().ReactionsForContact(ctx, protocol, contactID)
	if err != nil {
		return nil, ops.Errorf(api.CodeInternal, "Database error")
	}
	byRow := aggregateReactions(rows, reactions)
	for i := range rows {
		if r := byRow[rows[i].RowID]; len(r) > 0 {
			out[i].Reactions = r
		}
	}
	return out, nil
}

// page is messages_page (the messages.page operation).
func (s *service) page(ctx context.Context, req api.MessagesPageRequest) ([]api.MessageRow, error) {
	if req.Limit != nil && (*req.Limit < 1 || *req.Limit > maxMessagesLimit) {
		return nil, ops.Errorf(api.CodeInvalidRequest, "Invalid limit")
	}
	protocol, contactID := req.Protocol, req.ContactID
	backend, contact, canonicalID := s.resolveChat(ctx, protocol, contactID)
	if req.Refresh || protocol == api.ProtocolTelegram {
		s.refreshHistory(ctx, backend, protocol, canonicalID, 20)
	}
	var extraKeys []string
	if contact != nil {
		extraKeys = contactStorageKeys(*contact)[1:]
	}
	// Read from the canonical key: with a ghost id the extra keys are the ghost
	// itself, so reading by contact_id would skip the @lid history.
	result, err := s.messageRows(ctx, protocol, canonicalID, extraKeys, req.Limit, req.Since)
	if err != nil {
		return nil, err
	}

	cfg := s.deps.Config()
	atts := make([]*api.MessageAttachment, 0, len(result))
	for i := range result {
		if result[i].Attachment != nil {
			atts = append(atts, result[i].Attachment)
		}
	}
	media.AttachMediaVersions(s.deps.Manager(), cfg, protocol, atts)

	isGroup := strings.HasSuffix(contactID, "@g.us")
	if !isGroup {
		if contacts, ok := s.deps.Contacts(ctx, protocol); ok {
			for _, known := range contacts {
				if known.ID == contactID && known.Protocol == protocol {
					isGroup = truthy(known.Extras["is_group"])
					break
				}
			}
		}
	}
	if isGroup {
		resolve := groupSenderResolver(ctx, s.deps.Manager(), backend, protocol)
		st := s.deps.Store()
		for i := range result {
			m := &result[i]
			m.IsGroup = true
			m.Sender = resolve(m.Sender)
			qa := m.QuoteAuthor
			resolved := resolve(qa)
			if qa != "" && (strings.HasSuffix(resolved, "@lid") || strings.HasSuffix(resolved, "@c.us")) {
				// Unresolved quote author: the quoted message of the same chat
				// carries a sender (often already a name, e.g. a pushname).
				var qts int64
				if m.QuoteTimestamp != nil {
					qts = *m.QuoteTimestamp
				}
				if sender, _ := st.QuotedSender(ctx, protocol, contactID, qts); sender != "" {
					resolved = resolve(sender)
				}
			}
			m.QuoteAuthor = resolved
		}
	} else {
		// 1:1 chat: the quote author is always the contact (or us), which is
		// redundant, and the raw value may be an @lid.
		for i := range result {
			result[i].QuoteAuthor = ""
		}
	}
	return result, nil
}

// cacheRow is the canonical cache entry (protocols.db._load_cache) in the
// exact member order of the Python dict, nulls included.
type cacheRow struct {
	ID                  *string `json:"id"`
	Text                *string `json:"text"`
	IsMine              bool    `json:"is_mine"`
	Sender              *string `json:"sender"`
	Timestamp           int64   `json:"timestamp"`
	QuoteText           *string `json:"quote_text"`
	MsgType             *string `json:"msg_type"`
	AttachmentInfo      *string `json:"attachment_info"`
	AttachmentID        *string `json:"attachment_id"`
	ContentType         *string `json:"content_type"`
	QuoteAttachmentID   *string `json:"quote_attachment_id"`
	QuoteAttachmentPath *string `json:"quote_attachment_path"`
	QuoteContentType    *string `json:"quote_content_type"`
	QuoteTimestamp      *int64  `json:"quote_timestamp"`
	QuoteAuthor         *string `json:"quote_author"`
	ReplyToMessageID    *string `json:"reply_to_message_id"`
	Edited              bool    `json:"edited"`
	Read                bool    `json:"read"`
	Status              *string `json:"status"`
	Protocol            string  `json:"protocol"`
	BatchID             *string `json:"batch_id"`
	BatchIndex          *int64  `json:"batch_index"`
}

// isGroupCacheRow reports whether a cache row belongs to a WhatsApp group: the
// only chat kind where a raw "@lid" used to leak into sender/quote_author.
func isGroupCacheRow(m msg) bool {
	return m.Protocol == api.ProtocolWhatsApp && strings.HasSuffix(m.ContactNumber, "@g.us")
}

// unresolvedLIDPlaceholder is the display-time rescue of a group row stored
// before mc-zao.5.7: such a row still carries the raw "<n>@lid" sender or
// quote_author, and messages.window/messages.snapshot return cache rows
// verbatim (Python parity), so the TUI and the web showed the JID. Substituting
// the annotated placeholder here is the deliberate, narrow fix of mc-zao.5.8:
// it writes nothing to the shared Python-owned messages.db, it leaves the raw
// JID in the store so messages.page can still resolve a real name, and it
// diverges from Python only on the two fields already pinned by the parity
// allowlist (lidPlaceholderFields in parity_test.go). The value stays a
// placeholder (isPlaceholderDisplayName), so a later name still replaces it.
func unresolvedLIDPlaceholder(v *string) *string {
	if v == nil {
		return nil
	}
	if head, ok := strings.CutSuffix(*v, "@lid"); ok && head != "" {
		s := core.UnresolvedLIDName(head)
		return &s
	}
	return v
}

func toCacheRow(m msg) cacheRow {
	text := m.Text
	if strOf(m.MsgType) == api.MsgTypeImage {
		text = api.Ptr("")
	}
	sender, quoteAuthor := m.Sender, m.QuoteAuthor
	if isGroupCacheRow(m) {
		sender = unresolvedLIDPlaceholder(sender)
		quoteAuthor = unresolvedLIDPlaceholder(quoteAuthor)
	}
	return cacheRow{
		ID: m.MsgID, Text: text, IsMine: m.IsMine, Sender: sender, Timestamp: m.Timestamp,
		QuoteText: m.QuoteText, MsgType: m.MsgType, AttachmentInfo: m.AttachmentInfo,
		AttachmentID: m.AttachmentID, ContentType: m.ContentType,
		QuoteAttachmentID: m.QuoteAttachmentID, QuoteAttachmentPath: m.QuoteAttachmentPath,
		QuoteContentType: m.QuoteContentType, QuoteTimestamp: m.QuoteTimestamp,
		QuoteAuthor: quoteAuthor, ReplyToMessageID: m.ReplyToMessageID,
		Edited: m.Edited, Read: m.Read, Status: m.Status, Protocol: m.Protocol,
		BatchID: m.BatchID, BatchIndex: m.BatchIndex,
	}
}

func toCacheRows(msgs []msg) []cacheRow {
	out := make([]cacheRow, len(msgs))
	for i, m := range msgs {
		out[i] = toCacheRow(m)
	}
	return out
}

// windowOut is MessagesWindowOut with the exact cache-row shape.
type windowOut struct {
	Messages  []cacheRow `json:"messages"`
	Refreshed bool       `json:"refreshed"`
}

// unionCacheRows is _union_cache_rows: the rows of a contact across all its
// storage keys (canonical key first), collapsed with the cross-key dedup.
func unionCacheRows(byKey map[string][]msg, contact proto.Contact) []msg {
	var tagged []msg
	for _, key := range contactStorageKeys(contact) {
		tagged = append(tagged, byKey[key]...)
	}
	if len(tagged) == 0 {
		return nil
	}
	idx := dedupCrossKey(len(tagged), contact.ID,
		func(i int) string { return tagged[i].ContactNumber },
		func(i int) identity {
			r := &tagged[i]
			text := strOf(r.Text)
			if strOf(r.MsgType) == api.MsgTypeImage {
				text = ""
			}
			return identity{
				id: strOf(r.MsgID), isMine: r.IsMine, text: text, timestamp: r.Timestamp,
				msgType: strOf(r.MsgType), attachmentID: strOf(r.AttachmentID),
			}
		})
	out := make([]msg, len(idx))
	for i, j := range idx {
		out[i] = tagged[j]
	}
	return out
}

// window is messages_window (the messages.window operation).
func (s *service) window(ctx context.Context, req api.MessagesWindowRequest) (any, error) {
	protocol := req.Protocol
	backend, contact, canonicalID := s.resolveChat(ctx, protocol, req.ContactID)
	if backend == nil {
		return windowOut{Messages: []cacheRow{}}, nil
	}
	refreshed := false
	if req.Refresh {
		refreshed = s.refreshHistory(ctx, backend, protocol, canonicalID, req.RefreshLimit)
	}
	st := s.deps.Store()
	var rows []msg
	if contact == nil {
		var err error
		if rows, err = st.ListMessages(ctx, store.MessageQuery{Protocol: protocol, Keys: []string{canonicalID}}); err != nil {
			return nil, ops.Errorf(api.CodeInternal, "Database error")
		}
	} else {
		byKey := map[string][]msg{}
		for _, key := range contactStorageKeys(*contact) {
			part, err := st.ListMessages(ctx, store.MessageQuery{Protocol: protocol, Keys: []string{key}})
			if err != nil {
				return nil, ops.Errorf(api.CodeInternal, "Database error")
			}
			byKey[key] = part
		}
		rows = unionCacheRows(byKey, *contact)
	}
	return windowOut{Messages: toCacheRows(rows), Refreshed: refreshed}, nil
}

// orderedCache is a JSON object that keeps the Python dict insertion order.
type orderedCache struct {
	keys []string
	rows map[string][]cacheRow
}

func (o orderedCache) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, k := range o.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		rows := o.rows[k]
		if rows == nil {
			rows = []cacheRow{}
		}
		vb, err := json.Marshal(rows)
		if err != nil {
			return nil, err
		}
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, vb...)
	}
	return append(buf, '}'), nil
}

// snapshot is messages_snapshot (the messages.snapshot operation).
func (s *service) snapshot(ctx context.Context, req api.MessagesSnapshotRequest) (any, error) {
	protocol := req.Protocol
	out := orderedCache{rows: map[string][]cacheRow{}}
	if _, ok := s.deps.Manager().Get(protocol); !ok {
		return out, nil
	}
	cache, err := s.deps.Store().LoadCache(ctx, protocol)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, ops.Errorf(api.CodeInternal, "Database error")
	}
	// The Python cache dict is ordered by first appearance, i.e. by the
	// (timestamp, id) of the first row of each chat.
	cids := make([]string, 0, len(cache))
	for cid, rows := range cache {
		if len(rows) > 0 {
			cids = append(cids, cid)
		}
	}
	sort.Slice(cids, func(i, j int) bool {
		a, b := cache[cids[i]][0], cache[cids[j]][0]
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		return a.RowID < b.RowID
	})

	add := func(key string, rows []msg) {
		if _, dup := out.rows[key]; !dup {
			out.keys = append(out.keys, key)
		}
		out.rows[key] = toCacheRows(rows)
	}
	if protocol != api.ProtocolWhatsApp {
		for _, cid := range cids {
			add(cid, cache[cid])
		}
		return out, nil
	}
	contacts, _ := s.deps.Contacts(ctx, protocol)
	keyOwner := map[string]proto.Contact{}
	for _, c := range contacts {
		for _, key := range contactStorageKeys(c) {
			if _, taken := keyOwner[key]; !taken {
				keyOwner[key] = c
			}
		}
	}
	processed := map[string]bool{}
	for _, cid := range cids {
		owner, owned := keyOwner[cid]
		switch {
		case !owned:
			add(cid, cache[cid])
		case !processed[owner.ID]:
			processed[owner.ID] = true
			add(owner.ID, unionCacheRows(cache, owner))
		}
	}
	return out, nil
}
