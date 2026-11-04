package signal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Constants from protocols/rpc.py.
const (
	DefaultDaemonPort = 8080
	// SubprocessTimeout bounds one-shot signal-cli runs.
	SubprocessTimeout = 120 * time.Second
	// RPCTimeout is the per-call HTTP timeout of the JSON-RPC client.
	RPCTimeout = 30 * time.Second
	// SSEReadTimeout: with no data (not even a keep-alive) for this long the
	// event stream is considered dead and re-opened.
	SSEReadTimeout = 30 * time.Second
)

// NotConfiguredMsg is the canonical "no phone number" error text.
const NotConfiguredMsg = "Signal phone number not configured.\n" +
	"Set the SIGNAL_USER_NUMBER environment variable or create a config.json file:\n" +
	`  echo '{"user_number": "+1234567890"}' > config.json`

// ErrNotConfigured is returned when no Signal number is configured.
var ErrNotConfigured = errors.New(NotConfiguredMsg)

// ErrCLINotFound mirrors find_signal_cli()'s FileNotFoundError.
var ErrCLINotFound = errors.New("signal-cli not found in ./bin/")

// FindSignalCLI returns <binDir>/signal-cli-*/bin/signal-cli (executable).
func FindSignalCLI(binDir string) (string, error) {
	entries, err := os.ReadDir(binDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "signal-cli-") {
				continue
			}
			exe := filepath.Join(binDir, e.Name(), "bin", "signal-cli")
			if st, err := os.Stat(exe); err == nil && st.Mode()&0o111 != 0 {
				return exe, nil
			}
		}
	}
	return "", ErrCLINotFound
}

// Client is the JSON-RPC-over-HTTP client of the signal-cli daemon
// (SignalRPCClient). It is safe for concurrent use.
type Client struct {
	URL   string
	HTTP  *http.Client
	reqID atomic.Int64
}

// NewClient builds a client for url (e.g. http://127.0.0.1:8080/api/v1/rpc).
func NewClient(url string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{URL: url, HTTP: hc}
}

// Response is a decoded JSON-RPC answer. Python's _call returned a dict that
// carries an "error" key on a transport failure and on a JSON-RPC error alike.
type Response struct {
	// Result is the "result" member (nil when absent).
	Result any
	// HasResult reports that the "result" member was present.
	HasResult bool
	// Err is non-empty when the call failed (transport error, HTTP status,
	// invalid JSON, or a JSON-RPC "error" member).
	Err string
}

// OK reports a successful call.
func (r Response) OK() bool { return r.Err == "" }

// Call executes one JSON-RPC call. It never returns a Go error: failures are
// reported in Response.Err, like the Python client's {"error": str(e)}.
func (c *Client) Call(ctx context.Context, method string, params map[string]any) Response {
	if params == nil {
		params = map[string]any{}
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.reqID.Add(1),
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{Err: err.Error()}
	}
	cctx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return Response{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{Err: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Response{Err: fmt.Sprintf("HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{Err: err.Error()}
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return Response{Err: err.Error()}
	}
	if e, ok := obj["error"]; ok {
		return Response{Err: errString(e)}
	}
	res, ok := obj["result"]
	return Response{Result: res, HasResult: ok}
}

func errString(e any) string {
	switch t := e.(type) {
	case string:
		return t
	case map[string]any:
		if m, ok := t["message"].(string); ok && m != "" {
			return m
		}
	}
	b, _ := json.Marshal(e)
	return string(b)
}

// ListContacts fetches the contact list (empty on any failure).
func (c *Client) ListContacts(ctx context.Context) []map[string]any {
	r := c.Call(ctx, "listContacts", nil)
	if !r.OK() {
		return nil
	}
	return asMapList(r.Result)
}

func asMapList(v any) []map[string]any {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(l))
	for _, it := range l {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// SendParams are the arguments of the "send" call. Pointer / nil members are
// omitted from the wire like Python's None.
type SendParams struct {
	Message          string
	Recipient        string
	Timestamp        *int64
	QuoteTimestamp   *int64
	QuoteAuthor      *string
	QuoteMessage     *string
	EditTimestamp    *int64
	QuoteAttachments []string // nil = omitted
	Attachments      []string // nil = omitted
}

func (p SendParams) wire() map[string]any {
	m := map[string]any{"message": p.Message, "recipient": []string{p.Recipient}}
	if p.Timestamp != nil {
		m["timestamp"] = *p.Timestamp
	}
	if p.QuoteTimestamp != nil {
		m["quoteTimestamp"] = *p.QuoteTimestamp
	}
	if p.QuoteAuthor != nil {
		m["quoteAuthor"] = *p.QuoteAuthor
	}
	if p.QuoteMessage != nil {
		m["quoteMessage"] = *p.QuoteMessage
	}
	if p.EditTimestamp != nil {
		m["editTimestamp"] = *p.EditTimestamp
	}
	if p.QuoteAttachments != nil {
		m["quoteAttachments"] = p.QuoteAttachments
	}
	if p.Attachments != nil {
		m["attachments"] = p.Attachments
	}
	return m
}

// Send issues the "send" call.
func (c *Client) Send(ctx context.Context, p SendParams) Response {
	return c.Call(ctx, "send", p.wire())
}

// SendReaction sends a reaction and reports whether signal-cli accepted it
// (some results[].type == "SUCCESS").
func (c *Client) SendReaction(ctx context.Context, recipient, emoji, targetAuthor string, targetTimestamp int64) bool {
	r := c.Call(ctx, "sendReaction", map[string]any{
		"recipient":       []string{recipient},
		"emoji":           emoji,
		"targetAuthor":    targetAuthor,
		"targetTimestamp": targetTimestamp,
	})
	if !r.OK() {
		return false
	}
	res, _ := r.Result.(map[string]any)
	results, _ := res["results"].([]any)
	for _, it := range results {
		if m, ok := it.(map[string]any); ok && m["type"] == "SUCCESS" {
			return true
		}
	}
	return false
}

// ListenEvents opens the SSE endpoint and calls onEnvelope for every event
// whose JSON data is an object with an "envelope" key. It returns when the
// stream ends, errors, ctx is done, or nothing (not even a keep-alive) arrived
// for readTimeout. The caller reconnects.
func ListenEvents(ctx context.Context, hc *http.Client, sseURL, account string, readTimeout time.Duration, onEnvelope func(map[string]any)) {
	if hc == nil {
		hc = &http.Client{}
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The account is appended unencoded, exactly like the Python f-string.
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, sseURL+"?account="+account, nil)
	if err != nil {
		return
	}
	watchdog := time.AfterFunc(readTimeout, cancel)
	defer watchdog.Stop()
	resp, err := hc.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return
	}
	rd := bufio.NewReaderSize(resp.Body, 64*1024)
	cur := map[string]string{}
	for {
		raw, err := rd.ReadString('\n')
		if raw == "" && err != nil {
			return
		}
		watchdog.Reset(readTimeout)
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		switch {
		case line == "":
			// Blank line: end of event.
			if d, ok := cur["data"]; ok {
				dec := json.NewDecoder(strings.NewReader(d))
				dec.UseNumber()
				var obj map[string]any
				if dec.Decode(&obj) == nil {
					if _, ok := obj["envelope"]; ok {
						onEnvelope(obj)
					}
				}
			}
			cur = map[string]string{}
		case strings.HasPrefix(line, ":"):
			// SSE comment / keep-alive.
		default:
			if i := strings.Index(line, ":"); i >= 0 {
				field, value := line[:i], strings.TrimPrefix(line[i+1:], " ")
				if old, ok := cur[field]; ok {
					cur[field] = old + "\n" + value
				} else {
					cur[field] = value
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// ---- subprocess fallback ------------------------------------------------------

// runSubprocess runs `signal-cli -u <number> <args...>` and returns stdout.
// The number is checked before the binary, like the Python helper.
func (b *Backend) runSubprocess(ctx context.Context, args ...string) (string, error) {
	num := b.UserNumber()
	if num == "" {
		num = strings.TrimSpace(b.cfg.SignalUserNumber())
	}
	if num == "" {
		return "", ErrNotConfigured
	}
	cli, err := b.cliPath()
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, SubprocessTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, cli, append([]string{"-u", num}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("signal-cli error (code %d): %s", ee.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		if cctx.Err() != nil {
			return "", fmt.Errorf("signal-cli timed out after %s", SubprocessTimeout)
		}
		return "", err
	}
	return stdout.String(), nil
}

// sendSubprocess is _send_subprocess.
func (b *Backend) sendSubprocess(ctx context.Context, p SendParams) (string, error) {
	args := []string{"send", "-m", p.Message, p.Recipient}
	if p.QuoteTimestamp != nil {
		args = append(args, "--quote-timestamp", strconv.FormatInt(*p.QuoteTimestamp, 10))
	}
	if p.QuoteAuthor != nil {
		args = append(args, "--quote-author", *p.QuoteAuthor)
	}
	if p.QuoteMessage != nil {
		args = append(args, "--quote-message", *p.QuoteMessage)
	}
	if p.EditTimestamp != nil {
		args = append(args, "--edit-timestamp", strconv.FormatInt(*p.EditTimestamp, 10))
	}
	for _, qa := range p.QuoteAttachments {
		args = append(args, "--quote-attachment", qa)
	}
	for _, a := range p.Attachments {
		args = append(args, "--attachment", a)
	}
	return b.runSubprocess(ctx, args...)
}
