package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// aggregateReactions ports messages._aggregate_reactions for one message row:
// the persisted reactions grouped per emoji, ordered by (-count, first ts).
func aggregateReactions(row store.Message, reactions []store.Reaction) []api.Reaction {
	ids := map[string]bool{itoa(row.Timestamp): true}
	if row.MsgID != nil {
		ids[*row.MsgID] = true
	}
	type agg struct {
		api.Reaction
		first   int64
		authors map[string]bool
	}
	grouped := map[string]*agg{}
	var order []string
	for _, r := range reactions {
		matchesID := r.TargetMsgID != nil && ids[*r.TargetMsgID]
		matchesTS := r.TargetTimestamp != nil && *r.TargetTimestamp == row.Timestamp
		if !matchesID && !matchesTS {
			continue
		}
		a, ok := grouped[r.Emoji]
		if !ok {
			a = &agg{Reaction: api.Reaction{Emoji: r.Emoji}, first: r.Timestamp, authors: map[string]bool{}}
			grouped[r.Emoji] = a
			order = append(order, r.Emoji)
		}
		a.Count += int(r.Count)
		a.IsMine = a.IsMine || r.IsMine
		if au := strings.TrimSpace(r.Author); au != "" {
			a.authors[au] = true
		}
		a.first = min(a.first, r.Timestamp)
	}
	list := make([]*agg, 0, len(order))
	for _, e := range order {
		list = append(list, grouped[e])
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].first < list[j].first
	})
	out := make([]api.Reaction, 0, len(list))
	for _, a := range list {
		authors := make([]string, 0, len(a.authors))
		for au := range a.authors {
			authors = append(authors, au)
		}
		sort.Strings(authors)
		r := a.Reaction
		r.Authors = authors
		out = append(out, r)
	}
	return out
}

// telegramReactionSnapshot ports messages._telegram_reaction_snapshot: the
// message's reactions minus our own, plus our new emoji.
func (c *Core) telegramReactionSnapshot(ctx context.Context, contactID, messageID, emoji string) []map[string]any {
	var current []api.Reaction
	rows, err := c.st.ListMessages(ctx, store.MessageQuery{Protocol: api.ProtocolTelegram, Keys: []string{contactID}})
	if err == nil {
		reactions, _ := c.st.ReactionsForContact(ctx, api.ProtocolTelegram, contactID)
		for _, row := range rows {
			id := store.Deref(row.MsgID)
			if id == "" {
				id = itoa(row.RowID)
			}
			if id == messageID {
				current = aggregateReactions(row, reactions)
				break
			}
		}
	}
	type item struct {
		emoji   string
		count   int
		isMine  bool
		authors []string
	}
	var snap []*item
	for _, r := range current {
		count := r.Count
		var authors []string
		for _, a := range r.Authors {
			if a != "You" && a != "Tu" {
				authors = append(authors, a)
			}
		}
		if r.IsMine {
			count--
		}
		if count > 0 {
			snap = append(snap, &item{r.Emoji, count, false, authors})
		}
	}
	var sel *item
	for _, it := range snap {
		if it.emoji == emoji {
			sel = it
			break
		}
	}
	if sel == nil {
		sel = &item{emoji: emoji}
		snap = append(snap, sel)
	}
	sel.count++
	sel.isMine = true
	sel.authors = append(sel.authors, "You")
	out := make([]map[string]any, len(snap))
	for i, it := range snap {
		authors := make([]any, len(it.authors))
		for j, a := range it.authors {
			authors[j] = a
		}
		out[i] = map[string]any{"emoji": it.emoji, "count": it.count, "is_mine": it.isMine, "authors": authors}
	}
	return out
}

// opReactionsSet is reactions.set.
func (c *Core) opReactionsSet(ctx context.Context, req api.ReactionsSetRequest) (api.ReactionsSetOut, error) {
	ctx = context.WithoutCancel(ctx)
	protocol, emojiStr := req.Protocol, req.Emoji
	if !api.IsProtocol(protocol) || strings.TrimSpace(req.ContactID) == "" ||
		strings.TrimSpace(req.MessageID) == "" || emojiStr == "" || runeLen(emojiStr) > 8 {
		return api.ReactionsSetOut{}, invalidRequest()
	}
	if _, ok := allEmoji[emojiStr]; !ok {
		return api.ReactionsSetOut{}, invalidRequest()
	}
	contactID := strings.TrimSpace(req.ContactID)
	messageID := strings.TrimSpace(req.MessageID)
	backend, ok := c.mgr.Get(protocol)
	if !ok {
		return api.ReactionsSetOut{}, &ops.Error{Code: api.CodeNotFound, Message: "Backend not found"}
	}
	row, err := c.st.MessageRowForReaction(ctx, protocol, contactID, messageID)
	if err != nil {
		return api.ReactionsSetOut{}, &ops.Error{Code: api.CodeInternal, Message: "Database error"}
	}
	if row == nil {
		return api.ReactionsSetOut{}, &ops.Error{Code: api.CodeNotFound, Message: "Message not found"}
	}
	targetTS := row.Timestamp
	targetAuthor := ""
	backendMsgID := messageID
	if protocol == api.ProtocolSignal {
		backendMsgID = itoa(targetTS)
		if row.IsMine {
			if un, ok := backend.(UserNumber); ok {
				targetAuthor = un.UserNumber()
			}
		} else {
			targetAuthor = contactID
		}
	}
	sent, err := c.mgr.SendReaction(ctx, protocol, contactID, backendMsgID, emojiStr, targetAuthor)
	if err != nil || !sent {
		return api.ReactionsSetOut{}, &ops.Error{Code: api.CodeBackendError, Message: "Reaction send failed"}
	}

	rowMsgID := store.Deref(row.MsgID)
	if rowMsgID == "" {
		rowMsgID = backendMsgID
	}
	payload := map[string]any{
		"mode":              "delta",
		"target_message_id": rowMsgID,
		"target_timestamp":  targetTS,
		"emoji":             emojiStr,
		"is_mine":           true,
		"author_key":        "me",
		"author":            "You",
		"is_remove":         false,
		"timestamp":         time.Now().UnixMilli(),
	}
	if protocol == api.ProtocolTelegram {
		payload["mode"] = "snapshot"
		payload["snapshot"] = c.telegramReactionSnapshot(ctx, contactID, messageID, emojiStr)
	}
	var aggregate map[string]any
	if rc, ok := backend.(proto.Reactor); ok {
		aggregate, err = rc.ApplyReaction(contactID, payload)
		if err != nil {
			c.log.Debug("apply_reaction failed", "err", err)
			aggregate = nil
		}
	}
	reactions := []api.Reaction{}
	if aggregate != nil {
		if b, err := json.Marshal(jsonable(aggregate["reactions"])); err == nil {
			var rs []api.Reaction
			if json.Unmarshal(b, &rs) == nil && rs != nil {
				reactions = rs
			}
		}
	}
	resolvedID := rowMsgID
	if aggregate != nil && aggregate["message_id"] != nil {
		if s, ok := idString(aggregate["message_id"]); ok {
			resolvedID = s
		}
	}
	resolvedTS := targetTS
	if aggregate != nil {
		if n, ok := asInt64(aggregate["timestamp"]); ok {
			resolvedTS = n
		}
	}
	c.PublishWebPush("reaction_update", map[string]any{
		"protocol": protocol, "contact_id": contactID, "message_id": resolvedID,
		"timestamp": resolvedTS, "reactions": reactions,
	})
	return api.ReactionsSetOut{OK: true, Reactions: reactions}, nil
}
