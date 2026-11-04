package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// shutdownDelay is the pause between the server.shutdown acknowledgement and
// the hook (api.SHUTDOWN_DELAY_S).
const shutdownDelay = 200 * time.Millisecond

func invalidRequest() *ops.Error {
	return &ops.Error{Code: api.CodeInvalidRequest, Message: "Invalid request"}
}

func (c *Core) opEmojiList(ctx context.Context, _ api.EmptyRequest) ([]api.EmojiCategoryOut, error) {
	out := make([]api.EmojiCategoryOut, 0, len(emojiCategories))
	for _, cat := range emojiCategories {
		aliases := make(map[string]string, len(cat.Emojis))
		for _, e := range cat.Emojis {
			aliases[e] = emojiAliases[e]
		}
		out = append(out, api.EmojiCategoryOut{
			Category: cat.Name, Icon: cat.Icon, Emojis: append([]string(nil), cat.Emojis...), Aliases: aliases,
		})
	}
	return out, nil
}

func (c *Core) opReactionsAvailable(ctx context.Context, req api.ReactionsAvailableRequest) (api.ReactionsAvailableOut, error) {
	if req.Protocol != api.ProtocolTelegram {
		return api.ReactionsAvailableOut{}, nil
	}
	rl, err := proto.As[proto.ReactionLister](c.mgr, req.Protocol)
	if err != nil {
		return api.ReactionsAvailableOut{}, nil
	}
	emojis, err := rl.AvailableReactions(ctx)
	if err != nil {
		c.log.Debug("Telegram available reactions request failed", "err", err)
		return api.ReactionsAvailableOut{}, nil
	}
	return api.ReactionsAvailableOut{Emojis: emojis}, nil
}

func (c *Core) opServerStatus(ctx context.Context, _ api.EmptyRequest) (api.ServerStatus, error) {
	out := api.ServerStatus{}
	for _, p := range api.Protocols {
		b, ok := c.mgr.Get(p)
		out[p] = ok && b.IsConnected()
	}
	return out, nil
}

func (c *Core) opServerConfig(ctx context.Context, _ api.EmptyRequest) (api.ServerConfigOut, error) {
	return api.ServerConfigOut{
		ThumbnailMaxLines:      c.cfg.ThumbnailMaxLines(),
		ThumbnailMaxCols:       c.cfg.ThumbnailMaxCols(),
		PickerMaxResults:       c.cfg.PickerMaxResults(),
		PickerPreferredBackend: c.cfg.PickerPreferredBackend(),
		WaLazyMedia:            c.cfg.WALazyMedia(),
	}, nil
}

// pyStrOfRaw approximates str(message or "") for an arbitrary JSON value.
func pyStrOfRaw(raw json.RawMessage) string {
	if api.RawIsNull(raw) {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return ""
	case float64:
		if t == 0 {
			return ""
		}
	case []any:
		if len(t) == 0 {
			return ""
		}
	case map[string]any:
		if len(t) == 0 {
			return ""
		}
	}
	return strings.TrimSpace(string(raw))
}

func (c *Core) opClientLog(ctx context.Context, req api.ClientLogRequest) (api.StatusOut, error) {
	msg := pyStrOfRaw(req.Message)
	if r := []rune(msg); len(r) > 500 {
		msg = string(r[:500])
	}
	c.log.Info("[" + req.Source + "] " + msg)
	return api.StatusOut{Status: "ok"}, nil
}

func (c *Core) opServerLogs(ctx context.Context, req api.ServerLogsRequest) (api.ServerLogsOut, error) {
	level := 0
	if req.Level != "" {
		level = levelNumbers[strings.ToUpper(req.Level)]
	}
	limit := req.Limit
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	after := max(req.After, 0)
	records := c.logbuf.Snapshot(after, limit, level)
	last := after
	if n := len(records); n > 0 {
		last = records[n-1].Seq
	}
	since := float64(c.logbuf.InstalledAt().UnixNano()) / 1e9
	return api.ServerLogsOut{Records: records, LastSeq: last, Since: &since}, nil
}

func (c *Core) opServerLogsClear(ctx context.Context, _ api.EmptyRequest) (api.StatusOut, error) {
	c.logbuf.Clear()
	return api.StatusOut{Status: "ok"}, nil
}

func (c *Core) opServerClients(reg *ops.Registry) func(context.Context, api.EmptyRequest) (api.ClientsOut, error) {
	return func(ctx context.Context, _ api.EmptyRequest) (api.ClientsOut, error) {
		counts := reg.ClientCounts()
		_, hasSocket := counts["socket"]
		return api.ClientsOut{
			Connections: max(counts["socket"], 0), SelfIncluded: hasSocket, Web: max(counts["web"], 0),
		}, nil
	}
}

func (c *Core) opServerShutdown(reg *ops.Registry) func(context.Context, api.EmptyRequest) (api.StatusOut, error) {
	return func(ctx context.Context, _ api.EmptyRequest) (api.StatusOut, error) {
		hook := reg.ShutdownHook
		if hook == nil {
			return api.StatusOut{}, &ops.Error{Code: api.CodeNotSupported, Message: "This server cannot be shut down remotely"}
		}
		go func() {
			time.Sleep(shutdownDelay)
			hook()
		}()
		c.log.Info("Shutdown requested through server.shutdown")
		return api.StatusOut{Status: "ok"}, nil
	}
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }
