package messages

import (
	"context"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// JIDPhoneResolver is the optional capability of a WhatsApp backend that maps
// an "@lid" JID to a phone number from its in-memory cache (Python
// backend._jid_to_phone). "" means unknown.
type JIDPhoneResolver interface {
	JIDToPhone(jid string) string
}

// addressBooker lists the aggregated address book (Manager.ListAddressBook).
type addressBooker interface {
	ListAddressBook(ctx context.Context, protocols []string, force bool) ([]proto.Contact, map[string]error)
}

// groupSenderResolver is _group_sender_resolver: it resolves a sender JID or
// number to the address-book name, as the TUI does. The fallback is always a
// readable value, never a raw "@lid"/"@c.us" JID: an unresolved "@lid" gets
// the annotated placeholder a nameless @lid chat gets (core.UnresolvedLIDName,
// mc-zao.5.7), because a LID's bare digits read like a phone number it is not;
// a "@c.us" sender keeps Python's bare number (wire parity). The backend now
// stores that placeholder itself, so the resolver only sees a raw "@lid" on
// rows written before the fix.
func groupSenderResolver(ctx context.Context, book addressBooker, backend proto.Backend, protocol string) func(string) string {
	phoneToName := map[string]string{}
	idToName := map[string]string{}
	lidToName := map[string]string{}
	if book != nil {
		contacts, _ := book.ListAddressBook(ctx, []string{protocol}, false)
		for _, c := range contacts {
			name := c.DisplayName
			if name == "" {
				continue
			}
			if c.ID != "" {
				idToName[c.ID] = name
				// A merged active chat keeps its @lid as id: that IS the alias
				// a group participant shows up with.
				if strings.HasSuffix(c.ID, "@lid") {
					lidToName[c.ID] = name
				}
			}
			if phone := pyStr(c.Extras["phone"]); phone != "" {
				phoneToName[phone] = name
			}
			if lid := pyStr(c.Extras["lid"]); lid != "" {
				lidToName[lid] = name
			}
		}
	}
	jidPhone, _ := backend.(JIDPhoneResolver)

	return func(sender string) string {
		if sender == "" {
			return sender
		}
		if n, ok := idToName[sender]; ok {
			return n
		}
		if n, ok := lidToName[sender]; ok {
			return n
		}
		local, _, _ := strings.Cut(sender, "@")

		if strings.HasSuffix(sender, "@lid") {
			phone := ""
			if jidPhone != nil {
				phone = jidPhone.JIDToPhone(sender)
			}
			if n, ok := phoneToName[phone]; phone != "" && ok {
				return n
			}
			if n, ok := phoneToName[local]; ok {
				return n
			}
			if n, ok := idToName[local+"@c.us"]; ok {
				return n
			}
			if phone != "" {
				return phone
			}
		}
		if strings.HasSuffix(sender, "@c.us") {
			if n, ok := phoneToName[local]; ok {
				return n
			}
		}
		if n, ok := phoneToName[sender]; ok {
			return n
		}
		if strings.HasSuffix(sender, "@lid") || strings.HasSuffix(sender, "@c.us") {
			// Python returns the bare local part for both: for an unresolved
			// @lid those digits look like a phone number the LID is not, so
			// the placeholder replaces them (deliberate divergence, mc-zao.5.7).
			// A "<user>:<device>@lid" head names the same user as "<user>@lid":
			// the placeholder names the user, never a device (mc-zao.5.8).
			if strings.HasSuffix(sender, "@lid") && local != "" {
				return core.UnresolvedLIDName(lidUser(local))
			}
			return local
		}
		return sender
	}
}

// lidUser is the user part of a LID local part: "<user>:<device>" and
// "<user>" name the same LID user, and core.UnresolvedLIDName callers spell the
// bare user part. An empty user falls back to the local part unchanged.
func lidUser(local string) string {
	user, _, _ := strings.Cut(local, ":")
	if user == "" {
		return local
	}
	return user
}
