package telegram

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/gotd/td/tg"
)

// TestMarkdownParityTelethon compares parseMarkdown/unparseMarkdown with
// telethon.extensions.markdown (what Python sent and showed). Skips when
// python3 or telethon is unavailable.
func TestMarkdownParityTelethon(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command("python3", "-c", "import telethon").Run(); err != nil {
		t.Skip("telethon not installed")
	}
	parseCases := []string{
		"plain", "**bold**", "__it__ and ~~st~~", "`code` x", "```pre block```",
		"```go\nfmt.Println()\n```", "**nested __it__ bold**", "[link](https://e.x/a?b=c)",
		"**unclosed", "a**b**c__d__", "emoji 😀 **b😀ld** ok", "`no **bold** in code`",
		"||spoiler||", "****", "**a** **b**", "[](empty)", "x ```a``` y `b`",
	}
	type ent = [4]string
	unparseCases := []struct {
		Text string
		Ents []ent
	}{
		{"hello world", []ent{{"bold", "0", "5", ""}}},
		{"hello world", []ent{{"bold", "0", "11", ""}, {"italic", "6", "5", ""}}},
		{"a😀b", []ent{{"code", "1", "2", ""}}},
		{"a😀b", []ent{{"bold", "2", "1", ""}}},
		{"x y", []ent{{"url", "0", "1", "https://e.x"}, {"mention", "2", "1", "42"}}},
		{"code", []ent{{"pre", "0", "4", "go"}}},
		{"secret", []ent{{"spoiler", "0", "6", ""}}},
		{"same", []ent{{"bold", "0", "4", ""}, {"italic", "0", "4", ""}}},
		{"abc", []ent{{"bold", "0", "1", ""}, {"strike", "1", "2", ""}}},
	}

	// Python input.
	pyUnparse := make([][]any, len(unparseCases))
	for i, c := range unparseCases {
		var es [][]any
		for _, e := range c.Ents {
			o, _ := strconv.Atoi(e[1])
			l, _ := strconv.Atoi(e[2])
			es = append(es, []any{e[0], o, l, e[3]})
		}
		pyUnparse[i] = []any{c.Text, es}
	}
	in, _ := json.Marshal(map[string]any{"parse": parseCases, "unparse": pyUnparse})
	cmd := exec.Command("python3", "testdata/telethon_md.py")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, stderr.String())
	}
	var py struct {
		Parse   [][2]json.RawMessage `json:"parse"`
		Unparse []string             `json:"unparse"`
	}
	if err := json.Unmarshal(raw, &py); err != nil {
		t.Fatal(err)
	}

	for i, s := range parseCases {
		text, ents := parseMarkdown(s)
		var pyText string
		var pyEnts [][4]any
		_ = json.Unmarshal(py.Parse[i][0], &pyText)
		_ = json.Unmarshal(py.Parse[i][1], &pyEnts)
		gotEnts := entsOut(ents)
		pj, _ := json.Marshal(pyEnts)
		gj, _ := json.Marshal(gotEnts)
		if text != pyText || string(pj) != string(gj) {
			t.Errorf("parse(%q)\n go: %q %s\n py: %q %s", s, text, gj, pyText, pj)
		}
	}
	for i, c := range unparseCases {
		var es []tg.MessageEntityClass
		for _, e := range c.Ents {
			o, _ := strconv.Atoi(e[1])
			l, _ := strconv.Atoi(e[2])
			switch e[0] {
			case "bold":
				es = append(es, &tg.MessageEntityBold{Offset: o, Length: l})
			case "italic":
				es = append(es, &tg.MessageEntityItalic{Offset: o, Length: l})
			case "strike":
				es = append(es, &tg.MessageEntityStrike{Offset: o, Length: l})
			case "code":
				es = append(es, &tg.MessageEntityCode{Offset: o, Length: l})
			case "pre":
				es = append(es, &tg.MessageEntityPre{Offset: o, Length: l, Language: e[3]})
			case "url":
				es = append(es, &tg.MessageEntityTextURL{Offset: o, Length: l, URL: e[3]})
			case "mention":
				id, _ := strconv.ParseInt(e[3], 10, 64)
				es = append(es, &tg.MessageEntityMentionName{Offset: o, Length: l, UserID: id})
			case "spoiler":
				es = append(es, &tg.MessageEntitySpoiler{Offset: o, Length: l})
			}
		}
		if got := unparseMarkdown(c.Text, es); got != py.Unparse[i] {
			t.Errorf("unparse(%q, %v)\n go: %q\n py: %q", c.Text, c.Ents, got, py.Unparse[i])
		}
	}
}

func entsOut(ents []tg.MessageEntityClass) [][4]any {
	out := [][4]any{}
	for _, e := range ents {
		switch t := e.(type) {
		case *tg.MessageEntityBold:
			out = append(out, [4]any{"bold", t.Offset, t.Length, ""})
		case *tg.MessageEntityItalic:
			out = append(out, [4]any{"italic", t.Offset, t.Length, ""})
		case *tg.MessageEntityStrike:
			out = append(out, [4]any{"strike", t.Offset, t.Length, ""})
		case *tg.MessageEntityCode:
			out = append(out, [4]any{"code", t.Offset, t.Length, ""})
		case *tg.MessageEntityPre:
			out = append(out, [4]any{"pre", t.Offset, t.Length, t.Language})
		case *tg.MessageEntityTextURL:
			out = append(out, [4]any{"url", t.Offset, t.Length, t.URL})
		case *tg.MessageEntityMentionName:
			out = append(out, [4]any{"mention", t.Offset, t.Length, strconv.FormatInt(t.UserID, 10)})
		default:
			out = append(out, [4]any{"other", 0, 0, ""})
		}
	}
	return out
}
