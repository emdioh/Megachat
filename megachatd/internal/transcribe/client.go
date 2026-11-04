package transcribe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxAudioSize = 25 * 1024 * 1024

// Formats accepted by the OpenAI API (Signal voice notes are .oga/opus).
var supportedExtensions = map[string]bool{
	".mp3": true, ".mp4": true, ".mpeg": true, ".mpga": true, ".m4a": true,
	".wav": true, ".webm": true, ".ogg": true, ".flac": true,
}

// Ogg/Opus voice notes: the API accepts Ogg, it only needs the ".ogg" name.
var oggAliases = map[string]bool{".oga": true, ".opus": true}

// Error is a transcription failure; its text is stored in the record's error
// column and shown to the user (Python TranscriptionError).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

func isFormatError(err error) bool {
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "unsupported file format") ||
		(strings.Contains(t, "format") && (strings.Contains(t, "http 400") || strings.Contains(t, "http 415")))
}

// Client transcribes one audio file.
type Client interface {
	Transcribe(ctx context.Context, audioPath string) (string, error)
	// Kind is the Python class name (diagnostics "service_client").
	Kind() string
	Model() string
	BaseURL() string
}

// ffmpegBin is the ffmpeg executable (a variable so tests can point it away).
var ffmpegBin = "ffmpeg"

// FFmpegAvailable reports whether `ffmpeg -version` runs.
func FFmpegAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, ffmpegBin, "-version").Run() == nil
}

// TranscodeToMP3 converts path to a temporary mono 64k mp3 with ffmpeg. The
// caller removes the returned file.
func TranscodeToMP3(path string) (string, error) {
	if !FFmpegAvailable() {
		return "", errf("Formato audio non supportato dal provider e ffmpeg non disponibile per la transcodifica")
	}
	slog.Info("Trascrizione: transcodifica in mp3 con ffmpeg", "file", filepath.Base(path))
	f, err := os.CreateTemp("", "*.mp3")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegBin, "-y", "-loglevel", "error", "-i", path,
		"-ac", "1", "-b:a", "64k", tmp)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errf("Transcodifica ffmpeg in timeout")
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", errf("Transcodifica ffmpeg fallita: %s", truncRunes(strings.ToValidUTF8(stderr.String(), "�"), 200))
		}
		return "", err
	}
	return tmp, nil
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func errorBody(text string) string {
	return truncRunes(strings.ReplaceAll(strings.TrimSpace(text), "\n", " "), 200)
}

// netErrKind mimics the Python exception class name used in messages.
func netErrKind(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "TimeoutError"
	}
	return "ClientConnectionError"
}

func httpClient(timeoutS int) *http.Client {
	return &http.Client{Timeout: time.Duration(timeoutS) * time.Second}
}

// ---- OpenAI-compatible (OpenAI, OpenRouter) ------------------------------------

// CloudClient is the OpenAI-compatible multipart client
// (POST {base}/audio/transcriptions). With Provider "openrouter" it always
// sends "language" and adds the attribution headers.
type CloudClient struct {
	APIKey       string
	BaseURLValue string
	ModelValue   string
	Language     string
	TimeoutS     int
	ExtraHeaders map[string]string
	OpenRouter   bool
	HTTP         *http.Client // optional override (tests)
}

// NewCloudClient builds the OpenAI client.
func NewCloudClient(apiKey, baseURL, model, language string, timeoutS int) *CloudClient {
	return &CloudClient{APIKey: apiKey, BaseURLValue: strings.TrimRight(baseURL, "/"),
		ModelValue: model, Language: language, TimeoutS: timeoutS}
}

// NewOpenRouterClient builds the OpenRouter client.
func NewOpenRouterClient(apiKey, baseURL, model, language string, timeoutS int, headers map[string]string) *CloudClient {
	c := NewCloudClient(apiKey, baseURL, model, language, timeoutS)
	c.OpenRouter = true
	c.ExtraHeaders = headers
	return c
}

func (c *CloudClient) Kind() string {
	if c.OpenRouter {
		return "OpenRouterTranscriptionClient"
	}
	return "CloudTranscriptionClient"
}
func (c *CloudClient) Model() string   { return c.ModelValue }
func (c *CloudClient) BaseURL() string { return c.BaseURLValue }

func (c *CloudClient) languageField() string {
	if c.OpenRouter || c.ModelValue == "whisper-1" {
		return "language"
	}
	return "languages"
}

// Transcribe uploads the file, transcoding to mp3 when the format is not
// accepted (or the provider rejects it).
func (c *CloudClient) Transcribe(ctx context.Context, audioPath string) (string, error) {
	st, err := os.Stat(audioPath)
	if err != nil {
		return "", err
	}
	if st.Size() > maxAudioSize {
		return "", errf("File audio troppo grande (max 25MB)")
	}
	ext := strings.ToLower(filepath.Ext(audioPath))
	upload, cleanup := audioPath, false
	defer func() {
		if cleanup {
			os.Remove(upload)
		}
	}()
	var text string
	if oggAliases[ext] {
		stem := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
		text, err = c.upload(ctx, audioPath, stem+".ogg")
	} else {
		if !supportedExtensions[ext] {
			if upload, err = TranscodeToMP3(audioPath); err != nil {
				return "", err
			}
			cleanup = true
		}
		text, err = c.upload(ctx, upload, "")
	}
	if err == nil {
		return text, nil
	}
	var te *Error
	if cleanup || !errors.As(err, &te) || !isFormatError(err) {
		return "", err
	}
	if !FFmpegAvailable() {
		return "", errf("%s (il provider rifiuta il formato e ffmpeg non è installato per convertirlo in mp3)", err)
	}
	if upload, err = TranscodeToMP3(audioPath); err != nil {
		return "", err
	}
	cleanup = true
	return c.upload(ctx, upload, "")
}

func (c *CloudClient) upload(ctx context.Context, path, filename string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if filename == "" {
		filename = filepath.Base(path)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", c.ModelValue)
	if c.Language != "" {
		_ = mw.WriteField(c.languageField(), c.Language)
	}
	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", ctype)
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(data)
	_ = mw.Close()

	url := c.BaseURLValue + "/audio/transcriptions"
	lang := c.Language
	if lang == "" {
		lang = "auto"
	}
	slog.Info("Trascrizione: POST", "url", url, "model", c.ModelValue, "file", filename,
		"bytes", len(data), "lingua", lang)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", errf("Errore di rete: %s: %v", "InvalidURL", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = httpClient(c.TimeoutS)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", errf("Errore di rete: %s: %v", netErrKind(err), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errf("Errore di rete: %s: %v", netErrKind(err), err)
	}
	if resp.StatusCode != http.StatusOK {
		b := strings.ReplaceAll(errorBody(string(raw)), c.APIKey, "***")
		slog.Warn("Trascrizione: risposta HTTP non 200", "status", resp.StatusCode, "body", b)
		return "", errf("Errore trascrizione HTTP %d: %s", resp.StatusCode, b)
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", errf("Risposta non JSON: %s", errorBody(string(raw)))
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return "", errf("Risposta non valida: %s", truncRunes(fmt.Sprint(parsed), 200))
	}
	text, ok := obj["text"].(string)
	if !ok {
		return "", errf("Risposta senza campo 'text': %s", errorBody(string(raw)))
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	slog.Info("Trascrizione: risposta HTTP 200", "campi", keys, "caratteri", len([]rune(text)))
	return strings.TrimSpace(text), nil
}

// ---- Gemini ---------------------------------------------------------------------

// GeminiPrompt is the transcription prompt ({language} is filled in).
const GeminiPrompt = "Transcribe the audio verbatim in its original language%s. " +
	"Output only the transcription text: no comments, no timestamps, no quotes, " +
	"no speaker labels. If there is no speech, output nothing."

// Gemini inline request limit (~20MB total); base64 grows the payload by 4/3.
const geminiMaxAudio = 15 * 1024 * 1024

var geminiFormats = map[string]string{
	".wav": "audio/wav", ".mp3": "audio/mp3", ".aiff": "audio/aiff", ".aif": "audio/aiff",
	".aac": "audio/aac", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
	".flac": "audio/flac",
}

// GeminiClient calls models/{model}:generateContent with inline audio.
type GeminiClient struct {
	APIKey       string
	BaseURLValue string
	ModelValue   string
	Language     string
	TimeoutS     int
	HTTP         *http.Client
}

// NewGeminiClient builds the Gemini client.
func NewGeminiClient(apiKey, baseURL, model, language string, timeoutS int) *GeminiClient {
	return &GeminiClient{APIKey: apiKey, BaseURLValue: strings.TrimRight(baseURL, "/"),
		ModelValue: model, Language: language, TimeoutS: timeoutS}
}

func (c *GeminiClient) Kind() string    { return "GeminiTranscriptionClient" }
func (c *GeminiClient) Model() string   { return c.ModelValue }
func (c *GeminiClient) BaseURL() string { return c.BaseURLValue }

func (c *GeminiClient) prompt() string {
	lang := ""
	if c.Language != "" {
		lang = fmt.Sprintf(" (in language code '%s')", c.Language)
	}
	return fmt.Sprintf(GeminiPrompt, lang)
}

const geminiSizeMessage = "File audio troppo grande per Gemini (max 15MB)"

func (c *GeminiClient) Transcribe(ctx context.Context, audioPath string) (string, error) {
	st, err := os.Stat(audioPath)
	if err != nil {
		return "", err
	}
	if st.Size() > geminiMaxAudio {
		return "", errf("%s", geminiSizeMessage)
	}
	upload := audioPath
	format, ok := geminiFormats[strings.ToLower(filepath.Ext(audioPath))]
	if !ok {
		if upload, err = TranscodeToMP3(audioPath); err != nil {
			return "", err
		}
		defer os.Remove(upload)
		format = geminiFormats[".mp3"]
	}
	st, err = os.Stat(upload)
	if err != nil {
		return "", err
	}
	if st.Size() > geminiMaxAudio {
		return "", errf("%s", geminiSizeMessage)
	}
	raw, err := os.ReadFile(upload)
	if err != nil {
		return "", err
	}
	return c.request(ctx, base64.StdEncoding.EncodeToString(raw), format)
}

func (c *GeminiClient) request(ctx context.Context, data, format string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"contents": []any{map[string]any{"parts": []any{
			map[string]any{"text": c.prompt()},
			map[string]any{"inline_data": map[string]any{"mime_type": format, "data": data}},
		}}},
		"generationConfig": map[string]any{"temperature": 0},
	})
	url := fmt.Sprintf("%s/models/%s:generateContent", c.BaseURLValue, c.ModelValue)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", errf("Errore di rete con Gemini: %s", "InvalidURL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	hc := c.HTTP
	if hc == nil {
		hc = httpClient(c.TimeoutS)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", errf("Errore di rete con Gemini: %s", netErrKind(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errf("Errore di rete con Gemini: %s", netErrKind(err))
	}
	if resp.StatusCode != http.StatusOK {
		return "", errf("%s", strings.ReplaceAll(
			fmt.Sprintf("Errore trascrizione Gemini HTTP %d: %s", resp.StatusCode, errorBody(string(raw))),
			c.APIKey, "***"))
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return "", errf("Risposta Gemini non valida")
	}
	return parseGemini(result)
}

func parseGemini(result map[string]any) (string, error) {
	cands, _ := result["candidates"].([]any)
	if len(cands) == 0 {
		if fb, ok := result["promptFeedback"].(map[string]any); ok {
			if reason, _ := fb["blockReason"].(string); reason != "" {
				return "", errf("Richiesta bloccata da Gemini (%s)", reason)
			}
		}
		return "", nil
	}
	cand, _ := cands[0].(map[string]any)
	var sb strings.Builder
	if content, ok := cand["content"].(map[string]any); ok {
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			if pm, ok := p.(map[string]any); ok {
				if t, ok := pm["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
	}
	text := strings.TrimSpace(sb.String())
	reason, _ := cand["finishReason"].(string)
	switch reason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		if text == "" {
			return "", errf("Risposta bloccata da Gemini (%s)", reason)
		}
	}
	return text, nil
}

// BuildClient returns the client of provider (openai | openrouter | gemini).
func BuildClient(provider, apiKey, baseURL, model, language string, timeoutS int, headers map[string]string) (Client, error) {
	switch provider {
	case "openai":
		return NewCloudClient(apiKey, baseURL, model, language, timeoutS), nil
	case "openrouter":
		return NewOpenRouterClient(apiKey, baseURL, model, language, timeoutS, headers), nil
	case "gemini":
		return NewGeminiClient(apiKey, baseURL, model, language, timeoutS), nil
	}
	return nil, fmt.Errorf("unsupported transcription provider: %q", provider)
}
