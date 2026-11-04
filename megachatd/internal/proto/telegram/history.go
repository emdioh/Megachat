package telegram

import (
	"context"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/gotd/td/tg"
)

// getHistory is client.get_messages(entity, limit=limit): newest first.
func (b *Backend) getHistory(ctx context.Context, api *tg.Client, eid int64, limit int) ([]*tg.Message, entityMaps, error) {
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		return nil, entityMaps{}, err
	}
	var out []*tg.Message
	var ents entityMaps
	offsetID := 0
	for len(out) < limit {
		n := limit - len(out)
		if n > 100 {
			n = 100
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offsetID, Limit: n})
		if err != nil {
			return nil, entityMaps{}, err
		}
		mod, ok := res.AsModified()
		if !ok {
			break
		}
		b.peers.learnLists(mod.GetUsers(), mod.GetChats())
		e := newEntityMaps(mod.GetUsers(), mod.GetChats())
		if ents.users == nil {
			ents = e
		} else {
			for k, v := range e.users {
				ents.users[k] = v
			}
			for k, v := range e.chats {
				ents.chats[k] = v
			}
			for k, v := range e.channels {
				ents.channels[k] = v
			}
		}
		msgs := mod.GetMessages()
		if len(msgs) == 0 {
			break
		}
		lowest := 0
		for _, m := range msgs {
			if msg, ok := m.(*tg.Message); ok {
				out = append(out, msg)
			}
			if _, id, _ := messageDateID(m); lowest == 0 || id < lowest {
				lowest = id
			}
		}
		if len(msgs) < n || lowest == 0 {
			break
		}
		offsetID = lowest
	}
	if ents.users == nil {
		ents = newEntityMaps(nil, nil)
	}
	return out, ents, nil
}

// fetchAndIngest fetches the history of one chat and ingests it, returning
// the normalised payloads (newest first).
func (b *Backend) fetchAndIngest(ctx context.Context, api *tg.Client, contactID string, limit int) ([]map[string]any, error) {
	eid, err := parseContactID(contactID)
	if err != nil {
		return nil, err
	}
	msgs, ents, err := b.getHistory(ctx, api, eid, limit)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, msg := range msgs {
		if msg.Date == 0 {
			continue
		}
		ev := b.messageToEvent(msg, ents, "")
		if ev == nil {
			continue
		}
		ts, _ := toInt64(ev.Payload["timestamp"])
		if _, err := b.IngestMessage(ctx, contactID, ev.Payload, ts); err != nil {
			b.log.Error("Telegram: ingest failed", "err", err)
		}
		out = append(out, ev.Payload)
	}
	return out, nil
}

func payloadToMessage(p map[string]any) proto.Message {
	str := func(k string) *string {
		if s := getStr(p, k); s != "" {
			return &s
		}
		return nil
	}
	var m proto.Message
	m.ID = str("id")
	text := getStr(p, "text")
	m.Text = &text
	mine := truthy(p["is_mine"])
	m.IsMine = &mine
	sender := getStr(p, "sender")
	m.Sender = &sender
	if ts, ok := toInt64(p["timestamp"]); ok {
		m.Timestamp = &ts
	}
	m.QuoteText = str("quote_text")
	m.MsgType = str("msg_type")
	m.AttachmentInfo = str("attachment_info")
	m.AttachmentID = str("attachment_id")
	m.ContentType = str("content_type")
	m.ReplyToMessageID = str("reply_to_message_id")
	m.MediaKind = str("media_kind")
	m.QuoteAttachmentID = str("quote_attachment_id")
	m.QuoteContentType = str("quote_content_type")
	proto := "telegram"
	m.Protocol = &proto
	return m
}

// FetchHistory implements proto.HistoryFetcher (fetch_history): it fetches
// and ingests the recent messages of one chat (120 s budget).
func (b *Backend) FetchHistory(ctx context.Context, contactID string, limit int) ([]proto.Message, error) {
	api, err := b.getAPI()
	if err != nil {
		return nil, nil
	}
	if _, ok := toInt(contactID); !ok {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	payloads, err := b.fetchAndIngest(ctx, api, contactID, limit)
	if err != nil {
		b.log.Error("Telegram fetch_history failed", "contact", contactID, "err", err)
		return nil, nil
	}
	out := make([]proto.Message, 0, len(payloads))
	for _, p := range payloads {
		out = append(out, payloadToMessage(p))
	}
	return out, nil
}

// FetchRecentHistory is fetch_recent_history: the recent messages of every
// known contact, ingested into cache + store. It returns the message count.
func (b *Backend) FetchRecentHistory(ctx context.Context, limit int) int {
	api, err := b.getAPI()
	if err != nil {
		return 0
	}
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	b.cmu.RLock()
	ids := make([]string, 0, len(b.contacts))
	for _, c := range b.contacts {
		ids = append(ids, c.ID)
	}
	b.cmu.RUnlock()
	total := 0
	for _, id := range ids {
		if _, ok := toInt(id); !ok {
			continue
		}
		p, err := b.fetchAndIngest(ctx, api, id, limit)
		if err != nil {
			b.log.Error("Telegram fetch_history failed", "contact", id, "err", err)
			continue
		}
		total += len(p)
	}
	return total
}

// ResyncHistory implements proto.HistoryResyncer: refetch the recent history
// of every contact; it returns the number of chats synced.
func (b *Backend) ResyncHistory(ctx context.Context, limit int) (int, error) {
	if _, err := b.getAPI(); err != nil {
		return 0, err
	}
	n := b.FetchRecentHistory(ctx, limit)
	_ = n
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	return len(b.contacts), nil
}
