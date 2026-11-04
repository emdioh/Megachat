package core

import (
	"context"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/bus"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Result is one event to publish.
type Result struct {
	Type    bus.EventType
	Payload map[string]any
}

// Process is the domain step for ONE raw backend event (ChatCore.process): it
// mutates the store and the contact overlays, and returns the bus events in
// exact wire form. It publishes nothing.
func (c *Core) Process(ctx context.Context, ev proto.Event) ([]Result, error) {
	switch ev.Type {
	case proto.EventMessage:
		return c.processMessage(ctx, ev)
	case proto.EventMessageEdit:
		return c.processEdit(ctx, ev)
	case proto.EventReactionUpdate:
		return c.processReaction(ctx, ev)
	case proto.EventReceipt:
		return c.processReceiptEvent(ctx, ev)
	case proto.EventTyping:
		return c.processTyping(ev), nil
	case proto.EventContactUpdate:
		return c.passthrough(bus.ContactUpdate, ev), nil
	case proto.EventLidWarmupDone:
		return c.passthrough(bus.LidWarmupDone, ev), nil
	case proto.EventSentMirror:
		return c.processSentMirror(ctx, ev)
	}
	return nil, nil
}

func (c *Core) passthrough(t bus.EventType, ev proto.Event) []Result {
	p := jsonable(ev.Payload).(map[string]any)
	if ev.Payload == nil {
		p = map[string]any{}
	}
	p["protocol"] = ev.Protocol
	p["contact_id"] = ev.ContactID
	return []Result{{t, p}}
}

// payloadContact extracts a contact object a backend put in the payload.
func payloadContact(v any) (proto.Contact, bool) {
	switch t := v.(type) {
	case proto.Contact:
		return t, true
	case *proto.Contact:
		if t != nil {
			return *t, true
		}
	case api.ContactWire:
		return proto.Contact{ID: t.ID, DisplayName: t.DisplayName, Protocol: t.Protocol, Extras: t.Extras}, true
	case map[string]any:
		id, ok1 := t["id"].(string)
		name, _ := t["display_name"].(string)
		p, ok2 := t["protocol"].(string)
		if ok1 && ok2 {
			extras, _ := t["extras"].(map[string]any)
			return proto.Contact{ID: id, DisplayName: name, Protocol: p, Extras: extras}, true
		}
	}
	return proto.Contact{}, false
}

// resolveContact: payload contact -> backend lookup -> placeholder.
func (c *Core) resolveContact(ctx context.Context, ev proto.Event) (proto.Contact, bool) {
	if ct, ok := payloadContact(ev.Payload["contact"]); ok {
		return ct, false
	}
	if ct, ok := c.FindContact(ctx, ev.Protocol, ev.ContactID); ok {
		return ct, false
	}
	name := ev.ContactID
	if b, ok := c.mgr.Get(ev.Protocol); ok {
		if pn, ok := b.(PlaceholderNamer); ok {
			if n := pn.PlaceholderName(ev.ContactID); n != "" {
				name = n
			}
		}
	}
	return proto.Contact{ID: ev.ContactID, DisplayName: name, Protocol: ev.Protocol}, true
}

// ensureRegistered makes ct known when the backend does not list it.
func (c *Core) ensureRegistered(ctx context.Context, ct proto.Contact) {
	if c.KnownContact(ctx, ct.Protocol, ct.ID) {
		return
	}
	c.RegisterContact(ct)
}

func (c *Core) processSentMirror(ctx context.Context, ev proto.Event) ([]Result, error) {
	if _, ok := c.mgr.Get(ev.Protocol); !ok {
		return nil, nil
	}
	ct, ok := c.FindContact(ctx, ev.Protocol, ev.ContactID)
	if !ok {
		ct, _ = c.resolveContact(ctx, ev)
	}
	c.ensureRegistered(ctx, ct)
	c.bumpRecency(proto.ContactCacheKey(ev.Protocol, ev.ContactID), ev.Payload["timestamp"])
	p := jsonable(ev.Payload).(map[string]any)
	if ev.Payload == nil {
		p = map[string]any{}
	}
	if id, ok := idString(ev.Payload["id"]); ok {
		p["id"] = id
	} else {
		p["id"] = nil
	}
	if s, ok := ev.Payload["batch_id"].(string); !ok || s == "" {
		p["batch_id"] = nil
	}
	if _, ok := p["timestamp"]; !ok {
		p["timestamp"] = nil
	}
	p["protocol"] = ev.Protocol
	p["contact_id"] = ev.ContactID
	p["contact"] = jsonable(c.applyRecency(ct))
	return []Result{{bus.SentMirror, p}}, nil
}

func (c *Core) processMessage(ctx context.Context, ev proto.Event) ([]Result, error) {
	if _, ok := c.mgr.Get(ev.Protocol); !ok {
		return nil, nil
	}
	ct, placeholder := c.resolveContact(ctx, ev)
	c.ensureRegistered(ctx, ct)
	if placeholder || LooksUnresolved(ct) {
		c.ab.schedule(ev.Protocol)
	}
	var tsAny any = int64(0)
	if v, ok := ev.Payload["timestamp"]; ok {
		tsAny = v
	}
	ts, _ := asInt64(tsAny)
	res, err := c.ingest(ctx, ev.Protocol, ct.ID, ev.Payload, ts)
	if err != nil {
		c.log.Warn("message ingest failed", "protocol", ev.Protocol, "err", err)
	}
	c.bumpRecency(ct.CacheKey(), tsAny)
	c.log.Debug("core: message", "added", res == IngestAdded, "changed", res == IngestChanged, "protocol", ev.Protocol, "contact", ct.ID)
	return []Result{{bus.Message, map[string]any{
		"protocol":        ev.Protocol,
		"contact_id":      ct.ID,
		"contact":         jsonable(c.applyRecency(ct)),
		"was_placeholder": placeholder,
		"message":         jsonable(ev.Payload),
		"timestamp":       jsonable(tsAny),
		"added":           res == IngestAdded,
		"changed":         res == IngestChanged,
	}}}, nil
}

func (c *Core) processEdit(ctx context.Context, ev proto.Event) ([]Result, error) {
	b, ok := c.mgr.Get(ev.Protocol)
	if !ok {
		return nil, nil
	}
	editor, ok := b.(proto.Editor)
	if !ok {
		return nil, nil
	}
	newText, _ := ev.Payload["text"].(string)
	editID, hasID := idString(ev.Payload["edit_message_id"])
	if !hasID || newText == "" {
		return nil, nil
	}
	ct, _ := c.resolveContact(ctx, ev)
	opts := proto.ApplyEditOptions{MarkEdited: true}
	if v, ok := ev.Payload["is_mine"]; ok && v != nil {
		m := asBool(v)
		opts.IsMine = &m
	}
	if n, ok := asInt64(ev.Payload["edit_timestamp"]); ok {
		opts.EditTimestamp = n
	}
	info, err := editor.ApplyEdit(ev.ContactID, editID, newText, opts)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	return []Result{{bus.MessageEdit, map[string]any{
		"protocol":   ev.Protocol,
		"contact_id": ev.ContactID,
		"contact":    jsonable(c.applyRecency(ct)),
		"info": toMap(api.EditInfo{MessageID: info.MessageID, Timestamp: info.Timestamp,
			OldText: info.OldText, Text: info.Text, IsMine: info.IsMine}),
		"text": newText,
	}}}, nil
}

func (c *Core) processReaction(ctx context.Context, ev proto.Event) ([]Result, error) {
	b, ok := c.mgr.Get(ev.Protocol)
	if !ok {
		return nil, nil
	}
	reactor, ok := b.(proto.Reactor)
	if !ok {
		return nil, nil
	}
	info, err := reactor.ApplyReaction(ev.ContactID, ev.Payload)
	if err != nil {
		return nil, err
	}
	if len(info) == 0 {
		return nil, nil
	}
	return []Result{{bus.ReactionUpdate, map[string]any{
		"protocol": ev.Protocol, "contact_id": ev.ContactID, "info": jsonable(info),
	}}}, nil
}

func (c *Core) processReceiptEvent(ctx context.Context, ev proto.Event) ([]Result, error) {
	if _, ok := c.mgr.Get(ev.Protocol); !ok {
		return nil, nil
	}
	updated, err := c.processReceipt(ctx, ev.Protocol, ev.ContactID, ev.Payload)
	if err != nil {
		return nil, err
	}
	if len(updated) == 0 {
		return nil, nil
	}
	list := make([]any, len(updated))
	for i, u := range updated {
		list[i] = jsonable(u)
	}
	return []Result{{bus.Receipt, map[string]any{
		"protocol": ev.Protocol, "contact_id": ev.ContactID, "updated": list,
	}}}, nil
}

func (c *Core) processTyping(ev proto.Event) []Result {
	action, _ := ev.Payload["action"].(string)
	if action != api.TypingStarted && action != api.TypingStopped {
		return nil
	}
	return []Result{{bus.Typing, toMap(api.TypingPayload{Protocol: ev.Protocol, ContactID: ev.ContactID, Action: action})}}
}

var _ = strings.TrimSpace
