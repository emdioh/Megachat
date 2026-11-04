package telegram

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gotd/td/tg"
)

// Telethon's default parse mode is markdown: outgoing text is parsed with
// telethon.extensions.markdown.parse. This is a port of the same delimiters
// (**bold**, __italic__, ~~strike~~, `code`, ```pre```, [text](url)); an
// unmatched delimiter stays literal and entities never nest.

var inlineURLRE = regexp.MustCompile(`^!?\[(.+?)\]\((.+?)\)`)

var mdDelims = []string{"```", "**", "__", "~~", "`"}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// parseMarkdown returns the text without delimiters and its entities (UTF-16
// offsets).
func parseMarkdown(message string) (string, []tg.MessageEntityClass) {
	if message == "" {
		return message, nil
	}
	var (
		out      strings.Builder
		entities []tg.MessageEntityClass
		off      int // utf16 offset in out
	)
	rest := message
	for len(rest) > 0 {
		if m := inlineURLRE.FindStringSubmatchIndex(rest); m != nil {
			label := rest[m[2]:m[3]]
			url := rest[m[4]:m[5]]
			length := utf16Len(label)
			if strings.HasPrefix(url, "tg://user?id=") {
				if id, ok := toInt(strings.TrimPrefix(url, "tg://user?id=")); ok {
					entities = append(entities, &tg.InputMessageEntityMentionName{Offset: off, Length: length, UserID: &tg.InputUser{UserID: id}})
				} else {
					entities = append(entities, &tg.MessageEntityTextURL{Offset: off, Length: length, URL: url})
				}
			} else {
				entities = append(entities, &tg.MessageEntityTextURL{Offset: off, Length: length, URL: url})
			}
			out.WriteString(label)
			off += length
			rest = rest[m[1]:]
			continue
		}
		matched := false
		for _, d := range mdDelims {
			if !strings.HasPrefix(rest, d) {
				continue
			}
			end := strings.Index(rest[len(d):], d)
			if end <= 0 { // unmatched or empty: literal
				continue
			}
			content := rest[len(d) : len(d)+end]
			length := utf16Len(content)
			switch d {
			case "**":
				entities = append(entities, &tg.MessageEntityBold{Offset: off, Length: length})
			case "__":
				entities = append(entities, &tg.MessageEntityItalic{Offset: off, Length: length})
			case "~~":
				entities = append(entities, &tg.MessageEntityStrike{Offset: off, Length: length})
			case "`":
				entities = append(entities, &tg.MessageEntityCode{Offset: off, Length: length})
			case "```":
				entities = append(entities, &tg.MessageEntityPre{Offset: off, Length: length})
			}
			out.WriteString(content)
			off += length
			rest = rest[len(d)+end+len(d):]
			matched = true
			break
		}
		if matched {
			continue
		}
		_, sz := utf8.DecodeRuneInString(rest)
		out.WriteString(rest[:sz])
		off += utf16Len(rest[:sz])
		rest = rest[sz:]
	}
	return out.String(), entities
}
