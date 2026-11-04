package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

func TestRouteToOpTable(t *testing.T) {
	cases := []struct {
		method, url, body, op, params string
	}{
		{"GET", "/api/emoji", "", "emoji.list", `{}`},
		{"GET", "/api/reactions?protocol=bogus", "", "reactions.available", `{"protocol":"bogus"}`},
		{"GET", "/api/status", "", "server.status", `{}`},
		{"GET", "/api/contacts", "", "contacts.list", `{"q":null}`},
		{"GET", "/api/contacts?q=ali", "", "contacts.list", `{"q":"ali"}`},
		{"GET", "/api/contacts/book?q=al&force=yes", "", "contacts.book", `{"q":"al","force":true}`},
		{"GET", "/api/link-preview?url=http://x", "", "links.preview", `{"url":"http://x"}`},
		{"POST", "/api/log", `{"message":"hi","x":1}`, "server.client_log", `{"message":"hi","source":"web-client"}`},
		{"POST", "/api/transcribe", `{"protocol":"signal","attachment_id":"a/b","z":1}`, "transcribe.submit", `{"protocol":"signal","attachment_id":"a/b"}`},
		{"POST", "/api/config/openai-key", `{"api_key":"sk"}`, "config.set_openai_key", `{"api_key":"sk"}`},
		{"GET", "/api/logs", "", "server.logs", `{"after":0,"limit":500,"level":""}`},
		{"GET", "/api/logs?after=3&limit=10&level=INFO", "", "server.logs", `{"after":3,"limit":10,"level":"INFO"}`},
		{"DELETE", "/api/logs", "", "server.logs_clear", `{}`},
		{"GET", "/api/transcribe/diagnostics", "", "transcribe.diagnostics", `{}`},
		{"POST", "/api/transcribe/check", "", "transcribe.check", `{}`},
		{"GET", "/api/transcribe/config", "", "transcribe.config", `{}`},
		{"GET", "/api/transcribe/settings", "", "transcribe.settings", `{}`},
		{"PUT", "/api/transcribe/settings", `{"model":"m","evil":1,"timeout":3}`, "transcribe.set_settings", `{"model":"m","timeout":3}`},
		{"PUT", "/api/transcribe/api-key", `{"provider":"openai","api_key":"k","x":1}`, "transcribe.set_api_key", `{"provider":"openai","api_key":"k"}`},
		{"GET", "/api/autotranscribe", "", "autotranscribe.config", `{}`},
		{"PUT", "/api/autotranscribe/global/signal", `{"enabled":true}`, "autotranscribe.set_global", `{"protocol":"signal","enabled":true}`},
		{"GET", "/api/autotranscribe/chat?proto=whatsapp&contact_id=a@c.us", "", "autotranscribe.chat", `{"protocol":"whatsapp","contact_id":"a@c.us"}`},
		{"PUT", "/api/autotranscribe/chat?proto=signal&contact_id=%2B1", `{"enabled":null}`, "autotranscribe.set_chat", `{"protocol":"signal","contact_id":"+1","enabled":null}`},
		{"GET", "/api/transcribe/telegram/tgref:1:2", "", "transcribe.status", `{"protocol":"telegram","attachment_id":"tgref:1:2"}`},
		{"GET", "/api/transcribe/signal/a/b/c", "", "transcribe.status", `{"protocol":"signal","attachment_id":"a/b/c"}`},
		{"GET", "/api/messages?proto=signal&contact_id=%2B1", "", "messages.page", `{"protocol":"signal","contact_id":"+1","refresh":false,"limit":null,"since":null}`},
		{"GET", "/api/messages?proto=signal&contact_id=x&refresh=1&limit=5&since=9", "", "messages.page", `{"protocol":"signal","contact_id":"x","refresh":true,"limit":5,"since":9}`},
		{"POST", "/api/messages/read", `{"protocol":"signal","contact_id":"+1"}`, "chats.mark_read", `{"protocol":"signal","contact_id":"+1"}`},
		{"POST", "/api/chats/archive", `{"protocol":"signal","contact_id":"+1","archived":true}`, "chats.archive", `{"protocol":"signal","contact_id":"+1","archived":true}`},
		{"POST", "/api/messages/edit", `{"a":1}`, "messages.edit", `{"a":1}`},
		{"POST", "/api/messages/reaction", `{"a":1}`, "reactions.set", `{"a":1}`},
		{"GET", "/api/link/status", "", "link.status", `{}`},
		{"GET", "/api/link/required", "", "link.required", `{}`},
		{"POST", "/api/link/start", `{"protocol":"signal","phone":"+1","device_name":"d","x":1}`, "link.start", `{"protocol":"signal","phone":"+1","device_name":"d"}`},
		{"POST", "/api/link/poll", `{"protocol":"signal"}`, "link.poll", `{"protocol":"signal"}`},
		{"POST", "/api/link/finalize", `{"phone":null}`, "link.finalize", `{"protocol":"signal","phone":""}`},
		{"POST", "/api/link/finalize", `{"phone":"+39"}`, "link.finalize", `{"protocol":"signal","phone":"+39"}`},
		{"POST", "/api/link/cancel", `{"protocol":"whatsapp"}`, "link.cancel", `{"protocol":"whatsapp"}`},
		{"POST", "/api/link/submit_2fa", `{"password":"pw","x":1}`, "link.submit_2fa", `{"password":"pw"}`},
		{"POST", "/api/link/reconnect", `{"protocols":["signal"]}`, "link.reconnect", `{"protocols":["signal"]}`},
		{"GET", "/api/gmail/status", "", "gmail.status", `{}`},
		{"GET", "/api/gmail/unread", "", "gmail.unread", `{}`},
		{"GET", "/api/gmail/address-book", "", "gmail.address_book", `{}`},
		{"GET", "/api/gmail/messages", "", "gmail.messages.list", `{"folder":null,"offset":0,"limit":50,"refresh":false}`},
		{"GET", "/api/gmail/messages?folder=INBOX&offset=5&limit=7&refresh=true", "", "gmail.messages.list", `{"folder":"INBOX","offset":5,"limit":7,"refresh":true}`},
		{"POST", "/api/gmail/messages/bulk-delete", `{"uids":[1,2],"folder":"X","y":1}`, "gmail.messages.bulk_delete", `{"uids":[1,2],"folder":"X"}`},
		{"DELETE", "/api/gmail/messages/12?folder=F", "", "gmail.messages.delete", `{"uid":12,"folder":"F"}`},
		{"GET", "/api/gmail/messages/12", "", "gmail.messages.get", `{"uid":12,"folder":null}`},
		{"POST", "/api/gmail/messages/12/read", "", "gmail.messages.mark_read", `{"uid":12,"folder":null}`},
		{"POST", "/api/gmail/messages/12/unread", "", "gmail.messages.mark_unread", `{"uid":12,"folder":null}`},
		{"POST", "/api/gmail/messages/12/archive", "", "gmail.messages.archive", `{"uid":12,"folder":null}`},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.url, func(t *testing.T) {
			e := newEnv(t)
			resp, body := e.do(t, c.method, c.url, c.body)
			if resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if got := e.d.names(); len(got) != 1 || got[0] != c.op {
				t.Fatalf("ops = %v, want [%s]", got, c.op)
			}
			jsonEq(t, e.d.last().Params, c.params)
		})
	}
}

// Every route of api.Routes is registered, and mapped to its documented op.
func TestAllDocumentedRoutesExist(t *testing.T) {
	e := newEnv(t)
	for _, rt := range api.Routes {
		if rt.Path == api.RouteHealth {
			continue
		}
		u := rt.Path
		u = strings.NewReplacer("{proto}", "signal", "{attachment_id:path}", "a", "{message_row_id}", "5",
			"{uid}", "3", "{index}", "0").Replace(u)
		if rt.Path == api.RouteGmailImage {
			continue
		}
		resp, _ := e.do(t, rt.Method, u, `{}`, "Authorization", "")
		if resp.StatusCode != 401 {
			t.Errorf("%s %s without token: %d, want 401", rt.Method, rt.Path, resp.StatusCode)
		}
		resp, _ = e.do(t, rt.Method, u, `{}`)
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			t.Errorf("%s %s not routed (%d)", rt.Method, rt.Path, resp.StatusCode)
		}
	}
}

func TestOpErrorsMapToStatusAndDetail(t *testing.T) {
	for code, want := range api.HTTPStatusFor {
		e := newEnv(t)
		e.d.errs["server.status"] = &ops.Error{Code: code, Message: "boom " + code}
		resp, body := e.do(t, "GET", "/api/status", "")
		if resp.StatusCode != want || body != `{"detail":"boom `+code+`"}` {
			t.Errorf("%s: %d %s", code, resp.StatusCode, body)
		}
	}
	e := newEnv(t)
	e.d.errs["server.status"] = io_err("x")
	if resp, body := e.do(t, "GET", "/api/status", ""); resp.StatusCode != 500 || body != `{"detail":"x"}` {
		t.Errorf("plain error: %d %s", resp.StatusCode, body)
	}
}

type io_err string

func (e io_err) Error() string { return string(e) }

func TestValidation422(t *testing.T) {
	cases := []struct{ method, url, body string }{
		{"GET", "/api/contacts/book", ""},
		{"GET", "/api/reactions", ""},
		{"GET", "/api/messages?proto=irc&contact_id=x", ""},
		{"GET", "/api/messages?proto=signal", ""},
		{"GET", "/api/messages?proto=signal&contact_id=x&limit=abc", ""},
		{"GET", "/api/messages?proto=signal&contact_id=x&refresh=maybe", ""},
		{"GET", "/api/logs?after=x", ""},
		{"GET", "/api/gmail/messages/abc", ""},
		{"GET", "/api/media/irc/x", ""},
		{"GET", "/api/media/signal/x?w=big", ""},
		{"GET", "/api/quote-media/signal/abc", ""},
		{"PUT", "/api/autotranscribe/global/irc", `{"enabled":true}`},
		{"POST", "/api/log", `[1]`},
		{"POST", "/api/log", `nope`},
		{"POST", "/api/messages/read", ``},
		{"GET", "/gmail-image", ""},
	}
	for _, c := range cases {
		e := newEnv(t)
		hdr := []string{}
		if c.url == "/gmail-image" {
			hdr = []string{"Authorization", ""}
		}
		resp, body := e.do(t, c.method, c.url, c.body, hdr...)
		var v api.HTTPValidationError
		if resp.StatusCode != 422 || json.Unmarshal([]byte(body), &v) != nil || len(v.Detail) == 0 {
			t.Errorf("%s %s: %d %s", c.method, c.url, resp.StatusCode, body)
		}
		if len(e.d.names()) != 0 {
			t.Errorf("%s: op called despite 422", c.url)
		}
	}
	e := newEnv(t)
	resp, body := e.do(t, "PUT", "/api/autotranscribe/chat?proto=signal&contact_id=x", `{}`)
	if resp.StatusCode != 422 || body != `{"detail":"enabled richiesto"}` {
		t.Errorf("enabled richiesto: %d %s", resp.StatusCode, body)
	}
}

func TestEmojiAndLinkPreviewHeaders(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "GET", "/api/emoji", "")
	if resp.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Error(resp.Header)
	}
	resp, _ = e.do(t, "GET", "/api/link-preview?url=u", "")
	if resp.Header.Get("Cache-Control") != "private, max-age=3600" {
		t.Error(resp.Header)
	}
}

type fakeQR struct{}

func (fakeQR) PNG(data string) ([]byte, error) { return []byte("PNG:" + data), nil }

func TestLinkQRConvertedToPNG(t *testing.T) {
	e := newEnv(t, WithQRRenderer(fakeQR{}))
	e.d.results["link.start"] = map[string]any{"qr": map[string]any{"kind": "url", "data": "sgnl://x"}}
	e.d.results["link.poll"] = map[string]any{"state": "waiting", "needs_2fa": false, "qr": map[string]any{"kind": "info", "data": "hi"}}
	_, body := e.do(t, "POST", "/api/link/start", `{"protocol":"signal"}`)
	// base64("PNG:sgnl://x")
	if !strings.Contains(body, `"kind":"png"`) || !strings.Contains(body, "UE5HOnNnbmw6Ly94") {
		t.Errorf("start: %s", body)
	}
	_, body = e.do(t, "POST", "/api/link/poll", `{"protocol":"signal"}`)
	if !strings.Contains(body, `"kind":"info"`) {
		t.Errorf("poll: %s", body)
	}
	e.d.results["link.poll"] = map[string]any{"state": "done", "needs_2fa": false, "qr": nil}
	_, body = e.do(t, "POST", "/api/link/poll", `{"protocol":"signal"}`)
	if !strings.Contains(body, `"qr":null`) {
		t.Errorf("poll null: %s", body)
	}
}
