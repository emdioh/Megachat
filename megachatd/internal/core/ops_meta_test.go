package core

import (
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
)

func TestServerStatusAndConfig(t *testing.T) {
	e := newEnv(t, "signal", "whatsapp")
	wantJSON(t, e.mustCall("server.status", `{}`), `{"signal":false,"whatsapp":false,"telegram":false}`)
	if err := e.mgr.StartBackend(bg, "signal"); err != nil {
		t.Fatal(err)
	}
	// Unregistered protocols (telegram) report false.
	wantJSON(t, e.mustCall("server.status", `{}`), `{"signal":true,"whatsapp":false,"telegram":false}`)
	wantJSON(t, e.mustCall("server.config", `{}`), `{"thumbnail_max_lines":12,"thumbnail_max_cols":60,
	  "picker_max_results":50,"picker_preferred_backend":"","wa_lazy_media":true}`)
}

func TestServerClientLogAndLogs(t *testing.T) {
	e := newEnv(t)
	e.core.log = slog.New(e.core.logbuf.Handler(nil))
	wantJSON(t, e.mustCall("server.client_log", `{"message":"hello sk-abcdef123456","source":"web-client"}`), `{"status":"ok"}`)
	wantJSON(t, e.mustCall("server.client_log", `{}`), `{"status":"ok"}`)

	var out struct {
		Records []map[string]any `json:"records"`
		LastSeq int64            `json:"last_seq"`
		Since   *float64         `json:"since"`
	}
	if err := json.Unmarshal([]byte(e.mustCall("server.logs", `{}`)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 2 || out.Records[0]["message"] != "[web-client] hello sk-***" ||
		out.Records[0]["level"] != "INFO" || out.Records[1]["message"] != "[client] " ||
		out.LastSeq != 2 || out.Since == nil {
		t.Fatalf("logs: %+v", out)
	}
	// after / limit / level
	_ = json.Unmarshal([]byte(e.mustCall("server.logs", `{"after":1,"limit":5000,"level":"warning"}`)), &out)
	if len(out.Records) != 0 || out.LastSeq != 1 {
		t.Fatalf("level filter + empty result keeps after: %+v", out)
	}
	_ = json.Unmarshal([]byte(e.mustCall("server.logs", `{"after":-5,"level":"nonsense"}`)), &out)
	if len(out.Records) != 2 {
		t.Fatalf("negative after clamps, unknown level = all: %+v", out)
	}
	wantJSON(t, e.mustCall("server.logs_clear", `{}`), `{"status":"ok"}`)
	got := e.mustCall("server.logs", `{}`)
	var cleared map[string]any
	_ = json.Unmarshal([]byte(got), &cleared)
	if recs, ok := cleared["records"].([]any); !ok || len(recs) != 0 {
		t.Fatalf("records must be [] after clear: %s", got)
	}
}

func TestServerClients(t *testing.T) {
	e := newEnv(t)
	wantJSON(t, e.mustCall("server.clients", `{}`), `{"connections":0,"self_included":false,"web":0}`)
	e.reg.SetClientCounter("socket", func() int { return 3 })
	e.reg.SetClientCounter("web", func() int { return 2 })
	wantJSON(t, e.mustCall("server.clients", `{}`), `{"connections":3,"self_included":true,"web":2}`)
}

func TestServerShutdown(t *testing.T) {
	e := newEnv(t)
	if oe := e.callErr("server.shutdown", `{}`); oe.Code != "not_supported" || oe.Message != "This server cannot be shut down remotely" {
		t.Fatalf("no hook: %+v", oe)
	}
	called := make(chan struct{})
	e.reg.ShutdownHook = func() { close(called) }
	wantJSON(t, e.mustCall("server.shutdown", `{}`), `{"status":"ok"}`)
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("hook was not called")
	}
}

func TestEmojiListAndReactionsAvailable(t *testing.T) {
	e := newEnv(t)
	var cats []struct {
		Category string            `json:"category"`
		Icon     string            `json:"icon"`
		Emojis   []string          `json:"emojis"`
		Aliases  map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(e.mustCall("emoji.list", `{}`)), &cats); err != nil {
		t.Fatal(err)
	}
	if len(cats) == 0 || cats[0].Category != "Smileys" || cats[0].Icon != "😀" || cats[0].Emojis[0] != "😀" {
		t.Fatalf("categories: %+v", cats[0])
	}
	if cats[0].Aliases["😀"] != "grinning_face" {
		t.Fatalf("alias: %q", cats[0].Aliases["😀"])
	}
	uniq := map[string]bool{}
	for _, em := range cats[0].Emojis {
		uniq[em] = true
		if _, ok := cats[0].Aliases[em]; !ok {
			t.Fatalf("emoji %q has no alias entry (empty string if none)", em)
		}
	}
	if len(cats[0].Aliases) != len(uniq) {
		t.Fatal("aliases must have exactly one entry per distinct emoji")
	}

	wantJSON(t, e.mustCall("reactions.available", `{"protocol":"telegram"}`), `{"emojis":["👍","❤️","😂","😮","😢","🙏"]}`)
	wantJSON(t, e.mustCall("reactions.available", `{"protocol":"signal"}`), `{"emojis":[]}`)
	e.fake("telegram").FailMethod("AvailableReactions", errors.New("down"))
	wantJSON(t, e.mustCall("reactions.available", `{"protocol":"telegram"}`), `{"emojis":[]}`)
}

var _ = proto.ProtocolSignal
