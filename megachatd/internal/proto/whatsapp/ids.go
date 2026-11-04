// Package whatsapp is the Go port of protocols/whatsapp.py (and its REST,
// event and webhook helpers): the WhatsApp backend, built on whatsmeow instead
// of the WAHA Docker container. It speaks to WhatsApp directly (multi-device
// web protocol) and keeps WAHA's identifier formats so that the existing
// message cache, reactions, quotes and aliases line up:
//
//   - chat/contact ids are WAHA ids: "<num>@c.us", "<id>@g.us", "<num>@lid"
//     (whatsmeow spells the first one "<num>@s.whatsapp.net");
//   - message ids are WAHA serialized ids "true_<chat>_<ID>" (sent by me) or
//     "false_<chat>_<ID>" (received), with a "_<participant>" suffix in groups;
//     the receipt/ack path compares them through CanonicalMsgID (the hex part).
//
// The WAHA webhooks are replaced by whatsmeow events (events.go); the WAHA
// session guard (server/wa_session_guard.py) is ported in guard.go.
package whatsapp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"go.mau.fi/whatsmeow/types"
)

// WAHA id server parts.
const (
	serverWAHAUser  = "c.us"
	serverWAHAGroup = "g.us"
)

// WAHAFromJID converts a whatsmeow JID to the WAHA id Python stored as the
// contact id / cache key. Device suffixes ("123:5@s.whatsapp.net") are
// dropped, the "s.whatsapp.net" server becomes "c.us", "lid", "g.us",
// "broadcast" and "newsletter" are unchanged. The empty JID yields "".
func WAHAFromJID(j types.JID) string {
	if j.IsEmpty() {
		return ""
	}
	j = j.ToNonAD()
	switch j.Server {
	case types.DefaultUserServer, types.LegacyUserServer:
		return j.User + "@" + serverWAHAUser
	}
	return j.User + "@" + j.Server
}

// JIDFromWAHA is the inverse of WAHAFromJID: "<num>@c.us" becomes
// "<num>@s.whatsapp.net". It also accepts already-native JIDs.
func JIDFromWAHA(id string) (types.JID, error) {
	id = strings.TrimSpace(id)
	user, server, ok := strings.Cut(id, "@")
	if !ok || user == "" || server == "" {
		return types.EmptyJID, fmt.Errorf("whatsapp: invalid chat id %q", id)
	}
	switch server {
	case serverWAHAUser, types.DefaultUserServer:
		server = types.DefaultUserServer
	case serverWAHAGroup, types.HiddenUserServer, types.BroadcastServer, types.NewsletterServer,
		types.BotServer, types.HostedServer, types.HostedLIDServer:
	default:
		return types.EmptyJID, fmt.Errorf("whatsapp: unsupported chat id %q", id)
	}
	j := types.NewJID(user, server)
	return j, nil
}

// normalizeWAHAID turns any spelling of a JID string into the WAHA id
// ("x@s.whatsapp.net" -> "x@c.us"); unknown shapes are returned as is.
func normalizeWAHAID(s string) string {
	if j, err := types.ParseJID(strings.TrimSpace(s)); err == nil && !j.IsEmpty() {
		return WAHAFromJID(j)
	}
	if user, server, ok := strings.Cut(s, "@"); ok && server == types.DefaultUserServer {
		return user + "@" + serverWAHAUser
	}
	return s
}

// jidDigits is _jid_digits: only the digits of a JID ("" when none).
func jidDigits(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var fetchableJIDRe = regexp.MustCompile(`^[0-9][0-9-]*@(c\.us|lid|g\.us)$`)

// isFetchableJID is _is_fetchable_jid.
func isFetchableJID(jid string) bool { return fetchableJIDRe.MatchString(jid) }

// isBroadcastJID is _is_broadcast_jid: status/broadcast/newsletter ids.
func isBroadcastJID(jid string) bool {
	return strings.Contains(jid, "@broadcast") || strings.Contains(jid, "@newsletter")
}

// ---- message ids ----------------------------------------------------------

var hexIDRe = regexp.MustCompile(`^[0-9A-Fa-f]+$`)

// CanonicalMsgID is canonical_msg_id: the comparable token of a message id.
//
//   - "true_<chat>_<HEX>" / "false_<chat>_<HEX>[_<participant>]" -> "<HEX>"
//     (upper case; the only "@"-free hex segment);
//   - a plain hex id -> upper case;
//   - anything else is returned unchanged ("" for empty input).
func CanonicalMsgID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "true_") || strings.HasPrefix(raw, "false_") {
		_, body, _ := strings.Cut(raw, "_")
		var hexSegs []string
		for _, seg := range strings.Split(body, "_") {
			if seg != "" && !strings.Contains(seg, "@") && hexIDRe.MatchString(seg) {
				hexSegs = append(hexSegs, seg)
			}
		}
		if len(hexSegs) == 1 {
			return strings.ToUpper(hexSegs[0])
		}
		return raw
	}
	if hexIDRe.MatchString(raw) {
		return strings.ToUpper(raw)
	}
	return raw
}

// sameMsgID is _same_msg_id.
func sameMsgID(a, b string) bool {
	return a != "" && b != "" && CanonicalMsgID(a) == CanonicalMsgID(b)
}

// MsgID is a parsed WAHA message id.
type MsgID struct {
	FromMe      bool
	Chat        string // WAHA chat id; "" for a bare id
	ID          string // the WhatsApp message id (hex)
	Participant string // group sender (WAHA id), "" in direct chats
	Serialized  bool   // true when the "<bool>_<chat>_<id>" form was parsed
}

var serializedMsgIDRe = regexp.MustCompile(
	`^(true|false)_([0-9][0-9-]*@(?:c\.us|lid|g\.us|s\.whatsapp\.net|broadcast|newsletter))_([0-9A-Za-z]+)(?:_([0-9][0-9:.-]*@(?:c\.us|lid|s\.whatsapp\.net)))?$`)

// ParseMsgID parses "true_<chat>_<ID>[_<participant>]". Any other string is
// treated as a bare WhatsApp id.
func ParseMsgID(s string) MsgID {
	s = strings.TrimSpace(s)
	if m := serializedMsgIDRe.FindStringSubmatch(s); m != nil {
		p := m[4]
		if p != "" {
			p = normalizeWAHAID(p)
		}
		return MsgID{FromMe: m[1] == "true", Chat: normalizeWAHAID(m[2]), ID: m[3], Participant: p, Serialized: true}
	}
	return MsgID{ID: s}
}

// BuildMsgID is the inverse of ParseMsgID. participant is only appended for
// group chats.
func BuildMsgID(fromMe bool, chat, id, participant string) string {
	prefix := "false"
	if fromMe {
		prefix = "true"
	}
	out := prefix + "_" + chat + "_" + id
	if participant != "" && strings.HasSuffix(chat, "@"+serverWAHAGroup) {
		out += "_" + participant
	}
	return out
}

// resolveMediaChatID is _resolve_wa_media_chat_id: the chat id and the
// extension-less media name of an attachment id (a file name, or a WAHA URL).
func resolveMediaChatID(attachmentID string) (chat, mediaName string) {
	name := attachmentID
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		if i := strings.Index(name, "?"); i >= 0 {
			name = name[:i]
		}
		name = pyBase(name)
	}
	name = urlUnescape(name)
	if ext := pySuffix(name); ext != "" && !chatSuffixRe.MatchString(name) {
		name = name[:len(name)-len(ext)]
	}
	m := mediaChatRe.FindStringSubmatch(name)
	if m == nil {
		return "", name
	}
	return m[1], name
}

var (
	chatSuffixRe = regexp.MustCompile(`(?i)@(c|g)\.us$`)
	mediaChatRe  = regexp.MustCompile(`(?i)^(?:true|false)_([0-9][0-9-]*@(?:lid|c\.us|g\.us)(?:_.+)?)$`)
)

// errBadID reports an id that cannot be mapped to a WhatsApp target.
var errBadID = errors.New("whatsapp: invalid message id")
