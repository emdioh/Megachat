package whatsapp

import (
	"context"
	"sort"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// ApplyReaction implements proto.Reactor: it applies a WhatsApp reaction delta
// (one author adds, changes or removes an emoji) and aggregates the reactions
// of the target message. nil = unknown target / nothing changed.
func (b *Backend) ApplyReaction(contactID string, payload map[string]any) (map[string]any, error) {
	if getStr(payload, "mode") != "delta" || b.st == nil {
		return nil, nil
	}
	target := getStr(payload, "target_message_id")
	authorKey := getStr(payload, "author_key")
	if target == "" || authorKey == "" {
		return nil, nil
	}
	ctx := context.Background()
	requested := target

	var (
		resolvedID     = requested
		targetTS       int64
		targetResolved bool
	)
	// 1. in-memory cache: exact id, then canonical id.
	b.ingestMu.Lock()
	var cached *entry
	for _, m := range b.cache[contactID] {
		if m.ID == requested {
			cached = m
			break
		}
	}
	if cached == nil {
		if rc := CanonicalMsgID(requested); rc != "" {
			for _, m := range b.cache[contactID] {
				if CanonicalMsgID(m.ID) == rc {
					cached = m
					break
				}
			}
		}
	}
	if cached != nil {
		if cached.ID != "" {
			resolvedID = cached.ID
		}
		targetTS, targetResolved = cached.Timestamp, true
	}
	b.ingestMu.Unlock()

	payloadTS, _ := toInt64(payload["target_timestamp"])
	// 2. the stored row (exact or "..._<id>" suffix).
	lookID := resolvedID
	row, err := b.st.ResolveReactionTargetRow(ctx, proto.ProtocolWhatsApp, contactID, &lookID, ptrInt(payloadTS))
	if err != nil {
		return nil, err
	}
	// 3. persisted rows by canonical id.
	if row == nil && !targetResolved {
		if rc := CanonicalMsgID(requested); rc != "" {
			if persisted, err := b.st.LoadCache(ctx, proto.ProtocolWhatsApp); err == nil {
				for _, m := range persisted[contactID] {
					if id := store.Deref(m.MsgID); id != "" && CanonicalMsgID(id) == rc {
						pid := id
						row, err = b.st.ResolveReactionTargetRow(ctx, proto.ProtocolWhatsApp, contactID, &pid, ptrInt(payloadTS))
						if err != nil {
							return nil, err
						}
						break
					}
				}
			}
		}
	}
	if row != nil {
		targetResolved = true
		targetTS = row.Timestamp
		if row.MsgID != nil && *row.MsgID != "" {
			resolvedID = *row.MsgID
		}
	}
	if !targetResolved {
		targetTS = payloadTS
	}
	ts, _ := toInt64(payload["timestamp"])
	rid := resolvedID
	changed, err := b.st.ApplyReactionDelta(ctx, store.ReactionDelta{
		Protocol: proto.ProtocolWhatsApp, Contact: contactID, TargetMsgID: &rid, TargetTimestamp: ptrInt(targetTS),
		Emoji: getStr(payload, "emoji"), AuthorKey: authorKey, Author: getStr(payload, "author"),
		IsMine: truthy(payload["is_mine"]), IsRemove: truthy(payload["is_remove"]), Timestamp: ts,
	})
	if err != nil {
		return nil, err
	}
	if !changed || !targetResolved {
		return nil, nil
	}

	rows, err := b.st.ReactionsForContact(ctx, proto.ProtocolWhatsApp, contactID)
	if err != nil {
		return nil, err
	}
	type agg struct {
		emoji   string
		count   int64
		isMine  bool
		authors map[string]bool
		ts      int64
	}
	grouped := map[string]*agg{}
	for _, r := range rows {
		matchID := r.TargetMsgID != nil && *r.TargetMsgID == resolvedID
		matchTS := r.TargetTimestamp != nil && targetTS != 0 && *r.TargetTimestamp == targetTS
		if !matchID && !matchTS {
			continue
		}
		a := grouped[r.Emoji]
		if a == nil {
			a = &agg{emoji: r.Emoji, ts: r.Timestamp, authors: map[string]bool{}}
			grouped[r.Emoji] = a
		}
		a.count += r.Count
		a.isMine = a.isMine || r.IsMine
		if au := strings.TrimSpace(r.Author); au != "" {
			a.authors[au] = true
		}
		if r.Timestamp < a.ts {
			a.ts = r.Timestamp
		}
	}
	list := make([]*agg, 0, len(grouped))
	for _, a := range grouped {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		if list[i].ts != list[j].ts {
			return list[i].ts < list[j].ts
		}
		return list[i].emoji < list[j].emoji
	})
	reactions := make([]any, 0, len(list))
	for _, a := range list {
		names := make([]string, 0, len(a.authors))
		for n := range a.authors {
			names = append(names, n)
		}
		sort.Strings(names)
		authors := make([]any, len(names))
		for i, n := range names {
			authors[i] = n
		}
		reactions = append(reactions, map[string]any{
			"emoji": a.emoji, "count": a.count, "is_mine": a.isMine, "authors": authors,
		})
	}
	return map[string]any{"message_id": resolvedID, "timestamp": targetTS, "reactions": reactions}, nil
}

func ptrInt(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}
