package core

import (
	"strings"
	"unicode"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Port of server/domain.py and models.contact_storage_keys.

// ContactStorageKeys lists every storage key (messages.contact_number) of a
// contact, canonical key first. Non-WhatsApp contacts have one key; a WhatsApp
// contact unions its @c.us / @lid twins.
func ContactStorageKeys(c proto.Contact) []string {
	if c.Protocol != api.ProtocolWhatsApp {
		return []string{c.ID}
	}
	cid := c.ID
	if cid == "" || !strings.Contains(cid, "@") || strings.HasSuffix(cid, "@g.us") {
		return []string{cid}
	}
	keys := []string{cid}
	lid := extraString(c.Extras, "lid")
	phone := extraString(c.Extras, "phone")
	if strings.HasSuffix(cid, "@lid") {
		if phone != "" {
			keys = append(keys, phone+"@c.us")
		}
	} else {
		if lid != "" && strings.HasSuffix(lid, "@lid") {
			keys = append(keys, lid)
		}
		if phone != "" && !contains(keys, phone+"@c.us") && !strings.HasSuffix(cid, "@c.us") {
			keys = append(keys, phone+"@c.us")
		}
	}
	var out []string
	for _, k := range keys {
		if !contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// extraString is str(extras.get(key) or "").
func extraString(extras map[string]any, key string) string {
	switch v := extras[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return ""
	case float64:
		if v == 0 {
			return ""
		}
	case int, int64:
		if asBool(v) {
			return strings.TrimSpace(formatAny(v))
		}
		return ""
	}
	return formatAny(extras[key])
}

func formatAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return itoa(int64(t))
	case int64:
		return itoa(t)
	case float64:
		if t == float64(int64(t)) {
			return itoa(int64(t))
		}
	}
	return ""
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func jidDigits(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func looksLikePhone(name, phone string) bool {
	if name == "" || phone == "" {
		return false
	}
	nd, pd := jidDigits(name), jidDigits(phone)
	return nd != "" && pd != "" && nd == pd
}

// UnresolvedLIDName is the deliberate, clearly marked display name of a
// WhatsApp "@lid" contact nobody can name yet. A LID is not a phone number, so
// the bare digits must never be shown: they look exactly like the contact's
// number. The text is still a placeholder (isPlaceholderDisplayName), so a
// device/address-book name replaces it as soon as one is known.
func UnresolvedLIDName(head string) string {
	return "Contatto WhatsApp (ID " + head + ")"
}

func isPlaceholderDisplayName(name, contactID, phone string) bool {
	if name == "" || name == contactID {
		return true
	}
	if strings.HasSuffix(contactID, "@lid") {
		if head, _, _ := strings.Cut(contactID, "@"); name == head || name == UnresolvedLIDName(head) {
			return true
		}
	}
	if phone == "" {
		phone = jidDigits(contactID)
	}
	return looksLikePhone(name, phone)
}

// LooksUnresolved: the contact still shows a placeholder instead of a name.
func LooksUnresolved(c proto.Contact) bool {
	if c.Protocol == api.ProtocolWhatsApp {
		return isPlaceholderDisplayName(c.DisplayName, c.ID, c.Phone())
	}
	if c.DisplayName == "" || c.DisplayName == c.ID {
		return true
	}
	for _, r := range c.DisplayName {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// SearchContacts filters contacts by a case-insensitive substring over name,
// id and phone (original order, capped at max).
func SearchContacts(contacts []proto.Contact, query string, max int) []proto.Contact {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if len(contacts) > max {
			return contacts[:max]
		}
		return contacts
	}
	var out []proto.Contact
	for _, c := range contacts {
		if strings.Contains(strings.ToLower(c.DisplayName), q) ||
			strings.Contains(strings.ToLower(c.ID), q) ||
			strings.Contains(strings.ToLower(c.Phone()), q) {
			out = append(out, c)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}
