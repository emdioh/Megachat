package web

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

func writeJSON(w http.ResponseWriter, status int, v any, hdr ...string) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	for i := 0; i+1 < len(hdr); i += 2 {
		w.Header().Set(hdr[i], hdr[i+1])
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeDetail(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, api.HTTPError{Detail: detail})
}

func writeOpError(w http.ResponseWriter, err error) {
	oe := ops.AsError(err)
	status, ok := api.HTTPStatusFor[oe.Code]
	if !ok {
		status = 500
	}
	writeDetail(w, status, oe.Message)
}

// ---- auth ------------------------------------------------------------------

// isAuthorized validates an Authorization Bearer header in constant time.
func isAuthorized(authorization, token string) bool {
	if authorization == "" || token == "" {
		return false
	}
	scheme, supplied, found := strings.Cut(authorization, " ")
	return found && strings.EqualFold(scheme, "bearer") && supplied != "" &&
		subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) == 1
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if !s.authRequired {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !isAuthorized(r.Header.Get("Authorization"), s.token) {
			s.log.Warn("web auth 401", "path", r.URL.Path, "has_auth", r.Header.Get("Authorization") != "")
			writeJSON(w, 401, api.Unauthorized401, "WWW-Authenticate", "Bearer")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- operation calls -------------------------------------------------------

// callOp runs an operation. Operations outlive a disconnected caller, so the
// request context's cancellation is detached.
func (s *Server) callOp(r *http.Request, name string, params any) (any, error) {
	var raw json.RawMessage
	switch p := params.(type) {
	case nil:
		raw = json.RawMessage("{}")
	case json.RawMessage:
		raw = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return nil, ops.Errorf(api.CodeInternal, "%s", err.Error())
		}
		raw = b
	}
	return s.d.Call(context.WithoutCancel(r.Context()), name, raw)
}

// call is one request being translated: query/path/body parsing collects
// FastAPI-style validation errors (422) before the operation runs.
type call struct {
	r     *http.Request
	errs  []api.ValidationDetail
	abort *struct {
		status int
		detail string
	}
	body    map[string]json.RawMessage
	bodyOK  bool
	bodyTry bool
}

func (c *call) fail(status int, detail string) {
	if c.abort == nil {
		c.abort = &struct {
			status int
			detail string
		}{status, detail}
	}
}

func (c *call) addErr(typ, where, name, msg string) {
	c.errs = append(c.errs, api.ValidationDetail{Type: typ, Loc: []any{where, name}, Msg: msg, Input: json.RawMessage("null")})
}

func (c *call) lastQuery(name string) (string, bool) {
	vs, ok := c.r.URL.Query()[name]
	if !ok || len(vs) == 0 {
		return "", false
	}
	return vs[len(vs)-1], true
}

// qStr is a required string query parameter.
func (c *call) qStr(name string) string {
	v, ok := c.lastQuery(name)
	if !ok {
		c.addErr("missing", "query", name, "Field required")
	}
	return v
}

// qStrOpt is an optional string query parameter (nil when absent).
func (c *call) qStrOpt(name string) any {
	if v, ok := c.lastQuery(name); ok {
		return v
	}
	return nil
}

func parsePyInt(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	n, err := strconv.ParseInt(strings.ReplaceAll(v, "_", ""), 10, 64)
	if err != nil || strings.Contains(v, "__") || strings.HasPrefix(v, "_") || strings.HasSuffix(v, "_") {
		return 0, false
	}
	return n, true
}

func (c *call) intVal(where, name, v string) (int64, bool) {
	n, ok := parsePyInt(v)
	if !ok {
		c.addErr("int_parsing", where, name, "Input should be a valid integer, unable to parse string as an integer")
	}
	return n, ok
}

// qInt is an int query parameter with a default.
func (c *call) qInt(name string, def int64) int64 {
	v, ok := c.lastQuery(name)
	if !ok {
		return def
	}
	n, _ := c.intVal("query", name, v)
	return n
}

// qIntOpt is an optional int query parameter (nil when absent).
func (c *call) qIntOpt(name string) any {
	v, ok := c.lastQuery(name)
	if !ok {
		return nil
	}
	if n, ok := c.intVal("query", name, v); ok {
		return n
	}
	return nil
}

func (c *call) qBool(name string, def bool) bool {
	v, ok := c.lastQuery(name)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "on", "t", "yes", "y":
		return true
	case "false", "0", "off", "f", "no", "n":
		return false
	}
	c.addErr("bool_parsing", "query", name, "Input should be a valid boolean, unable to interpret input")
	return def
}

// pathInt parses an int path parameter.
func (c *call) pathInt(name, v string) int64 {
	n, _ := c.intVal("path", name, v)
	return n
}

// protoOf validates a Literal["signal","whatsapp","telegram"] value.
func (c *call) protoOf(where, name, v string) string {
	if !api.IsProtocol(v) {
		c.errs = append(c.errs, api.ValidationDetail{
			Type: "literal_error", Loc: []any{where, name},
			Msg:   "Input should be 'signal', 'whatsapp' or 'telegram'",
			Input: mustRaw(v),
			Ctx:   map[string]any{"expected": "'signal', 'whatsapp' or 'telegram'"},
		})
	}
	return v
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// obj reads the JSON body declared `dict[str, Any]`: 422 unless an object.
func (c *call) obj() map[string]json.RawMessage {
	if c.bodyTry {
		return c.body
	}
	c.bodyTry = true
	c.body = map[string]json.RawMessage{}
	raw, err := io.ReadAll(io.LimitReader(c.r.Body, 64<<20))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		c.errs = append(c.errs, api.ValidationDetail{Type: "missing", Loc: []any{"body"}, Msg: "Field required", Input: json.RawMessage("null")})
		return c.body
	}
	var m map[string]json.RawMessage
	var any0 any
	if json.Unmarshal(raw, &any0) != nil {
		c.errs = append(c.errs, api.ValidationDetail{Type: "json_invalid", Loc: []any{"body", 0}, Msg: "JSON decode error"})
		return c.body
	}
	if json.Unmarshal(raw, &m) != nil || m == nil {
		c.errs = append(c.errs, api.ValidationDetail{Type: "dict_type", Loc: []any{"body"}, Msg: "Input should be a valid dictionary", Input: raw})
		return c.body
	}
	c.body, c.bodyOK = m, true
	return m
}

// get is payload.get(key): the raw value, or null when absent.
func get(m map[string]json.RawMessage, key string) json.RawMessage {
	if v, ok := m[key]; ok {
		return v
	}
	return json.RawMessage("null")
}

// ---- static files ----------------------------------------------------------

func etagFor(mod time.Time, size int64) string {
	sum := md5.Sum([]byte(strconv.FormatFloat(float64(mod.UnixNano())/1e9, 'f', -1, 64) + "-" + strconv.FormatInt(size, 10)))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func contentTypeFor(name string) string {
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	if ct == "" {
		return "text/plain; charset=utf-8"
	}
	return ct
}

// serveStatic is the catch-all: unknown /api/* is 404 JSON, everything else
// is the SPA directory (StaticFiles html=True).
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || s.static == nil {
		writeDetail(w, 404, "Not Found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDetail(w, 405, "Method Not Allowed")
		return
	}
	p := path.Clean("/" + r.URL.Path)
	name := strings.TrimPrefix(p, "/")
	if name == "" {
		name = "."
	}
	st, err := fs.Stat(s.static, name)
	if err == nil && st.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusTemporaryRedirect)
			return
		}
		name = path.Join(name, "index.html")
		st, err = fs.Stat(s.static, name)
	}
	if err != nil || st.IsDir() {
		if st404, e := fs.Stat(s.static, "404.html"); e == nil && !st404.IsDir() {
			name, st = "404.html", st404
			s.sendFile(w, r, name, st, 404)
			return
		}
		writeDetail(w, 404, "Not Found")
		return
	}
	s.sendFile(w, r, name, st, 200)
}

func (s *Server) sendFile(w http.ResponseWriter, r *http.Request, name string, st fs.FileInfo, status int) {
	f, err := s.static.Open(name)
	if err != nil {
		writeDetail(w, 404, "Not Found")
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, _ := io.ReadAll(f)
		rs = bytes.NewReader(b)
	}
	w.Header().Set("Content-Type", contentTypeFor(name))
	w.Header().Set("ETag", etagFor(st.ModTime(), st.Size()))
	if status != 200 {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, rs)
		}
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
