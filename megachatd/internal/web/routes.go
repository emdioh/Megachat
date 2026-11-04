package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

type respCfg struct {
	headers []string
	post    func(s *Server, c *call, res any) (any, error)
}

type respOpt func(*respCfg)

func withHeaders(kv ...string) respOpt {
	return func(c *respCfg) { c.headers = append(c.headers, kv...) }
}
func withPost(f func(s *Server, c *call, res any) (any, error)) respOpt {
	return func(c *respCfg) { c.post = f }
}

func (s *Server) finish(w http.ResponseWriter, c *call) bool {
	if c.abort != nil {
		writeDetail(w, c.abort.status, c.abort.detail)
		return false
	}
	if len(c.errs) > 0 {
		writeJSON(w, 422, api.HTTPValidationError{Detail: c.errs})
		return false
	}
	return true
}

// jsonRoute builds the handler of a plain JSON route: params parses the
// request (and reports validation errors on c), then op runs.
func (s *Server) jsonRoute(op string, params func(c *call) any, opts ...respOpt) http.HandlerFunc {
	var cfg respCfg
	for _, o := range opts {
		o(&cfg)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		c := &call{r: r}
		var p any
		if params != nil {
			p = params(c)
		}
		if !s.finish(w, c) {
			return
		}
		res, err := s.callOp(r, op, p)
		if err == nil && cfg.post != nil {
			res, err = cfg.post(s, c, res)
		}
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, 200, res, cfg.headers...)
	}
}

type obj = map[string]any

func protoParam(c *call) string { return c.protoOf("path", "proto", chi.URLParam(c.r, "proto")) }

func (s *Server) routes(r chi.Router) {
	r.Get("/api/emoji", s.jsonRoute(api.OpEmojiList, nil, withHeaders("Cache-Control", "public, max-age=3600")))
	r.Get("/api/reactions", s.jsonRoute(api.OpReactionsAvailable, func(c *call) any {
		return obj{"protocol": c.qStr("protocol")}
	}))
	r.Post("/api/send", s.handleSend)
	r.Get("/api/status", s.jsonRoute(api.OpServerStatus, nil))
	r.Get("/api/contacts", s.jsonRoute(api.OpContactsList, func(c *call) any {
		return obj{"q": c.qStrOpt("q")}
	}))
	r.Get("/api/contacts/book", s.jsonRoute(api.OpContactsBook, func(c *call) any {
		return obj{"q": c.qStr("q"), "force": c.qBool("force", false)}
	}))
	r.Get("/api/contact-avatar", s.handleAvatar)
	r.Get("/api/link-preview", s.jsonRoute(api.OpLinksPreview, func(c *call) any {
		return obj{"url": c.qStr("url")}
	}, withHeaders("Cache-Control", "private, max-age=3600")))
	r.Get("/api/link-preview/image", s.handleLinkPreviewImage)
	r.Post("/api/log", s.jsonRoute(api.OpServerClientLog, func(c *call) any {
		b := c.obj()
		return obj{"message": get(b, "message"), "source": "web-client"}
	}))
	r.Post("/api/transcribe", s.jsonRoute(api.OpTranscribeSubmit, func(c *call) any {
		b := c.obj()
		return obj{"protocol": get(b, "protocol"), "attachment_id": get(b, "attachment_id")}
	}))
	r.Post("/api/config/openai-key", s.jsonRoute(api.OpConfigSetOpenaiKey, func(c *call) any {
		return obj{"api_key": get(c.obj(), "api_key")}
	}))
	r.Get("/api/logs", s.jsonRoute(api.OpServerLogs, func(c *call) any {
		after, limit := c.qInt("after", 0), c.qInt("limit", 500)
		level := ""
		if v, ok := c.lastQuery("level"); ok {
			level = v
		}
		return obj{"after": after, "limit": limit, "level": level}
	}))
	r.Delete("/api/logs", s.jsonRoute(api.OpServerLogsClear, nil))
	r.Get("/api/transcribe/diagnostics", s.jsonRoute(api.OpTranscribeDiagnostics, nil))
	r.Post("/api/transcribe/check", s.jsonRoute(api.OpTranscribeCheck, nil))
	r.Get("/api/transcribe/config", s.jsonRoute(api.OpTranscribeConfig, nil))
	r.Get("/api/transcribe/settings", s.jsonRoute(api.OpTranscribeSettings, nil))
	r.Put("/api/transcribe/settings", s.jsonRoute(api.OpTranscribeSetSettings, func(c *call) any {
		b := c.obj()
		out := obj{}
		for _, k := range []string{"provider", "providers", "model", "language", "base_url", "timeout"} {
			if v, ok := b[k]; ok {
				out[k] = v
			}
		}
		return out
	}))
	r.Put("/api/transcribe/api-key", s.jsonRoute(api.OpTranscribeSetApiKey, func(c *call) any {
		b := c.obj()
		return obj{"provider": get(b, "provider"), "api_key": get(b, "api_key")}
	}))
	r.Get("/api/autotranscribe", s.jsonRoute(api.OpAutotranscribeConfig, nil))
	r.Put("/api/autotranscribe/global/{proto}", s.jsonRoute(api.OpAutotranscribeSetGlobal, func(c *call) any {
		proto := protoParam(c)
		return obj{"protocol": proto, "enabled": get(c.obj(), "enabled")}
	}))
	r.Get("/api/autotranscribe/chat", s.jsonRoute(api.OpAutotranscribeChat, func(c *call) any {
		proto, _ := c.lastQuery("proto")
		if _, ok := c.lastQuery("proto"); !ok {
			c.addErr("missing", "query", "proto", "Field required")
		} else {
			c.protoOf("query", "proto", proto)
		}
		return obj{"protocol": proto, "contact_id": c.qStr("contact_id")}
	}))
	r.Put("/api/autotranscribe/chat", s.jsonRoute(api.OpAutotranscribeSetChat, func(c *call) any {
		proto, ok := c.lastQuery("proto")
		if !ok {
			c.addErr("missing", "query", "proto", "Field required")
		} else {
			c.protoOf("query", "proto", proto)
		}
		cid := c.qStr("contact_id")
		b := c.obj()
		if len(c.errs) == 0 {
			if _, has := b["enabled"]; !has {
				c.fail(422, "enabled richiesto")
			}
		}
		return obj{"protocol": proto, "contact_id": cid, "enabled": get(b, "enabled")}
	}))
	r.Get("/api/transcribe/{proto}/*", s.jsonRoute(api.OpTranscribeStatus, func(c *call) any {
		proto := protoParam(c)
		return obj{"protocol": proto, "attachment_id": chi.URLParam(c.r, "*")}
	}))
	r.Get("/api/messages", s.jsonRoute(api.OpMessagesPage, func(c *call) any {
		proto, ok := c.lastQuery("proto")
		if !ok {
			c.addErr("missing", "query", "proto", "Field required")
		} else {
			c.protoOf("query", "proto", proto)
		}
		cid := c.qStr("contact_id")
		return obj{
			"protocol": proto, "contact_id": cid, "refresh": c.qBool("refresh", false),
			"limit": c.qIntOpt("limit"), "since": c.qIntOpt("since"),
		}
	}))
	passBody := func(op string) http.HandlerFunc {
		return s.jsonRoute(op, func(c *call) any { return c.obj() })
	}
	r.Post("/api/messages/read", passBody(api.OpChatsMarkRead))
	r.Post("/api/chats/archive", passBody(api.OpChatsArchive))
	r.Post("/api/messages/edit", passBody(api.OpMessagesEdit))
	r.Post("/api/messages/reaction", passBody(api.OpReactionsSet))

	// Device linking.
	r.Get("/api/link/status", s.jsonRoute(api.OpLinkStatus, nil))
	r.Get("/api/link/required", s.jsonRoute(api.OpLinkRequired, nil))
	r.Post("/api/link/start", s.jsonRoute(api.OpLinkStart, func(c *call) any {
		b := c.obj()
		out := obj{}
		for _, k := range []string{"protocol", "phone", "device_name"} {
			if v, ok := b[k]; ok {
				out[k] = v
			}
		}
		return out
	}, withPost((*Server).withPNGQR)))
	r.Post("/api/link/poll", s.jsonRoute(api.OpLinkPoll, func(c *call) any {
		return obj{"protocol": get(c.obj(), "protocol")}
	}, withPost((*Server).withPNGQR)))
	r.Post("/api/link/finalize", s.jsonRoute(api.OpLinkFinalize, func(c *call) any {
		phone := get(c.obj(), "phone")
		if !pyTruthyRaw(phone) {
			phone = json.RawMessage(`""`)
		}
		return obj{"protocol": "signal", "phone": phone}
	}))
	r.Post("/api/link/cancel", s.jsonRoute(api.OpLinkCancel, func(c *call) any {
		return obj{"protocol": get(c.obj(), "protocol")}
	}))
	r.Post("/api/link/submit_2fa", s.jsonRoute(api.OpLinkSubmit2fa, func(c *call) any {
		b := c.obj()
		out := obj{}
		if v, ok := b["password"]; ok {
			out["password"] = v
		}
		return out
	}))
	r.Post("/api/link/reconnect", s.jsonRoute(api.OpLinkReconnect, func(c *call) any {
		return obj{"protocols": get(c.obj(), "protocols")}
	}))

	r.Get("/api/media/{proto}/*", s.handleMedia)
	r.Get("/api/quote-media/{proto}/{message_row_id}", s.handleQuoteMedia)

	s.gmailRoutes(r)
}

func pyTruthyRaw(raw json.RawMessage) bool {
	var v any
	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return false
	}
	return truthy(v)
}

// withPNGQR converts a url/text QR of a link result into a PNG (base64).
func (s *Server) withPNGQR(c *call, res any) (any, error) {
	if s.qr == nil {
		return res, nil // TODO(mc-zao.2.3): needs a QRRenderer
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return res, nil
	}
	var qr struct {
		Kind string `json:"kind"`
		Data string `json:"data"`
	}
	if raw, ok := m["qr"]; !ok || json.Unmarshal(raw, &qr) != nil || (qr.Kind != api.QRKindURL && qr.Kind != api.QRKindText) {
		return res, nil
	}
	png, err := s.qr.PNG(qr.Data)
	if err != nil {
		return nil, err
	}
	m["qr"] = mustRaw(api.LinkQrOut{Kind: api.QRKindPNG, Data: base64.StdEncoding.EncodeToString(png)})
	return m, nil
}
