package telegram

import (
	"sort"
	"unicode"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

// Telethon's default parse mode is markdown. parseMarkdown and unparseMarkdown
// are line-by-line ports of telethon.extensions.markdown.parse / unparse
// (Telethon 1.45, DEFAULT_DELIMITERS ** __ ~~ ` ``` and DEFAULT_URL_RE),
// working on UTF-16 code units exactly like Telethon's add_surrogate text.
// markdown_parity_test.go diffs both against the real library.

// mdDelims is Telethon's delimiter regex order: longest first, then dict order.
var mdDelims = []string{"```", "**", "__", "~~", "`"}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

type mdEnt struct {
	kind   string // bold italic strike code pre url
	offset int
	length int
	url    []uint16
}

func u16(s string) []uint16 { return utf16.Encode([]rune(s)) }

// del_surrogate: decode UTF-16 code units back to a string.
func fromU16(u []uint16) string { return string(utf16.Decode(u)) }

func hasPrefixAt(m []uint16, i int, d []uint16) bool {
	if i+len(d) > len(m) {
		return false
	}
	for k := range d {
		if m[i+k] != d[k] {
			return false
		}
	}
	return true
}

// find is str.find(sub, start).
func find(m []uint16, d []uint16, start int) int {
	for i := max(start, 0); i+len(d) <= len(m); i++ {
		if hasPrefixAt(m, i, d) {
			return i
		}
	}
	return -1
}

func concat(parts ...[]uint16) []uint16 {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]uint16, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// matchURL is DEFAULT_URL_RE = r'\[([^]]*?)\]\(([\s\S]*?)\)' matched at pos i.
// Returns end (exclusive) and the two groups.
func matchURL(m []uint16, i int) (end int, text, url []uint16, ok bool) {
	if i >= len(m) || m[i] != '[' {
		return 0, nil, nil, false
	}
	j := i + 1
	for j < len(m) && m[j] != ']' {
		j++
	}
	if j+1 >= len(m) || m[j] != ']' || m[j+1] != '(' {
		return 0, nil, nil, false
	}
	k := j + 2
	for k < len(m) && m[k] != ')' {
		k++
	}
	if k >= len(m) {
		return 0, nil, nil, false
	}
	return k + 1, m[i+1 : j], m[j+2 : k], true
}

// pyIsSpace is str.isspace for one code unit.
func pyIsSpace(c uint16) bool {
	if c >= 0x1c && c <= 0x1f {
		return true
	}
	if c >= 0xd800 && c <= 0xdfff {
		return false
	}
	return unicode.IsSpace(rune(c))
}

// stripText is telethon.helpers.strip_text.
func stripText(text []uint16, ents []*mdEnt) ([]uint16, []*mdEnt) {
	lstrip := func(t []uint16) []uint16 {
		for len(t) > 0 && pyIsSpace(t[0]) {
			t = t[1:]
		}
		return t
	}
	rstrip := func(t []uint16) []uint16 {
		for len(t) > 0 && pyIsSpace(t[len(t)-1]) {
			t = t[:len(t)-1]
		}
		return t
	}
	if len(ents) == 0 {
		return rstrip(lstrip(text)), ents
	}
	lenOri := len(text)
	text = lstrip(text)
	left := lenOri - len(text)
	text = rstrip(text)
	lenFinal := len(text)
	for i := len(ents) - 1; i >= 0; i-- {
		e := ents[i]
		del := func() { ents = append(ents[:i], ents[i+1:]...) }
		if e.length == 0 {
			del()
			continue
		}
		if e.offset+e.length > left {
			if e.offset >= left {
				e.offset -= left
			} else {
				e.length = e.offset + e.length - left
				e.offset = 0
			}
		} else {
			del()
			continue
		}
		if e.offset+e.length <= lenFinal {
			continue
		}
		if e.offset >= lenFinal {
			del()
		} else {
			e.length = lenFinal - e.offset
		}
	}
	return text, ents
}

// parseMarkdown returns the text without delimiters and its entities (UTF-16
// offsets), exactly as telethon.extensions.markdown.parse.
func parseMarkdown(message string) (string, []tg.MessageEntityClass) {
	if message == "" {
		return message, nil
	}
	m := u16(message)
	var result []*mdEnt
	i := 0
	for i < len(m) {
		var delim []uint16
		var kind string
		for _, d := range mdDelims {
			if hasPrefixAt(m, i, u16(d)) {
				delim = u16(d)
				kind = map[string]string{"```": "pre", "**": "bold", "__": "italic", "~~": "strike", "`": "code"}[d]
				break
			}
		}
		if delim != nil {
			end := find(m, delim, i+len(delim)+1)
			if end != -1 {
				m = concat(m[:i], m[i+len(delim):end], m[end+len(delim):])
				for _, ent := range result {
					if ent.offset+ent.length > i {
						if ent.offset <= i && ent.offset+ent.length >= end+len(delim) {
							ent.length -= len(delim) * 2
						} else {
							ent.length -= len(delim)
						}
					}
				}
				result = append(result, &mdEnt{kind: kind, offset: i, length: end - i - len(delim)})
				if kind == "code" || kind == "pre" {
					i = end - len(delim)
				}
				continue
			}
		} else if urlEnd, text, url, ok := matchURL(m, i); ok {
			start := i
			m = concat(m[:start], text, m[urlEnd:])
			delimSize := urlEnd - start - len(text)
			for _, ent := range result {
				if ent.offset+ent.length > start {
					ent.length -= delimSize
				}
			}
			result = append(result, &mdEnt{kind: "url", offset: start, length: len(text), url: append([]uint16(nil), url...)})
			i += len(text)
			continue
		}
		i++
	}
	m, result = stripText(m, result)
	ents := make([]tg.MessageEntityClass, 0, len(result))
	for _, e := range result {
		switch e.kind {
		case "bold":
			ents = append(ents, &tg.MessageEntityBold{Offset: e.offset, Length: e.length})
		case "italic":
			ents = append(ents, &tg.MessageEntityItalic{Offset: e.offset, Length: e.length})
		case "strike":
			ents = append(ents, &tg.MessageEntityStrike{Offset: e.offset, Length: e.length})
		case "code":
			ents = append(ents, &tg.MessageEntityCode{Offset: e.offset, Length: e.length})
		case "pre":
			ents = append(ents, &tg.MessageEntityPre{Offset: e.offset, Length: e.length, Language: ""})
		case "url":
			ents = append(ents, &tg.MessageEntityTextURL{Offset: e.offset, Length: e.length, URL: fromU16(e.url)})
		}
	}
	return fromU16(m), ents
}

// unparseMarkdown is telethon.extensions.markdown.unparse, which is what
// Message.text returns (offsets are UTF-16 code units). Entity types without
// a delimiter or URL (spoiler, blockquote, ...) are dropped, as in Telethon.
func unparseMarkdown(text string, entities []tg.MessageEntityClass) string {
	if text == "" || len(entities) == 0 {
		return text
	}
	type ins struct {
		at, idx int
		what    string
	}
	var insertAt []ins
	for i, e := range entities {
		var delim, url string
		var off, length int
		switch t := e.(type) {
		case *tg.MessageEntityBold:
			delim, off, length = "**", t.Offset, t.Length
		case *tg.MessageEntityItalic:
			delim, off, length = "__", t.Offset, t.Length
		case *tg.MessageEntityStrike:
			delim, off, length = "~~", t.Offset, t.Length
		case *tg.MessageEntityCode:
			delim, off, length = "`", t.Offset, t.Length
		case *tg.MessageEntityPre:
			delim, off, length = "```", t.Offset, t.Length
		case *tg.MessageEntityTextURL:
			url, off, length = t.URL, t.Offset, t.Length
		case *tg.MessageEntityMentionName:
			url, off, length = "tg://user?id="+itoa64(t.UserID), t.Offset, t.Length
		default:
			continue
		}
		s, en := off, off+length
		if delim != "" {
			insertAt = append(insertAt, ins{s, i, delim}, ins{en, -i, delim})
		} else {
			insertAt = append(insertAt, ins{s, i, "["}, ins{en, -i, "](" + url + ")"})
		}
	}
	sort.SliceStable(insertAt, func(a, b int) bool {
		if insertAt[a].at != insertAt[b].at {
			return insertAt[a].at < insertAt[b].at
		}
		return insertAt[a].idx < insertAt[b].idx
	})
	u := u16(text)
	within := func(t []uint16, idx int) bool {
		return 1 < idx && idx < len(t) &&
			t[idx-1] >= 0xd800 && t[idx-1] <= 0xdbff &&
			t[idx] >= 0xd800 && t[idx] <= 0xdfff
	}
	for k := len(insertAt) - 1; k >= 0; k-- {
		at := insertAt[k].at
		for within(u, at) {
			at++
		}
		// Python slicing clamps out-of-range indices.
		if at < 0 {
			at = max(len(u)+at, 0)
		}
		if at > len(u) {
			at = len(u)
		}
		u = concat(u[:at], u16(insertAt[k].what), u[at:])
	}
	return fromU16(u)
}

func itoa64(n int64) string {
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
