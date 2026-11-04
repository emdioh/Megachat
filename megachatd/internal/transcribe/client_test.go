package transcribe

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ctx() context.Context { return context.Background() }

func TestRejectsAudioLargerThan25MB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.mp3")
	f, _ := os.Create(p)
	_ = f.Truncate(maxAudioSize + 1)
	f.Close()
	_, err := NewCloudClient("secret", "http://x", "m", "", 5).Transcribe(ctx(), p)
	if err == nil || !strings.Contains(err.Error(), "max 25MB") {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadSendsMultipartAuthAndParsesText(t *testing.T) {
	srv := newFakeAPI(t)
	audio := writeFile(t, t.TempDir(), "voice.mp3", "audio")
	c := NewCloudClient("secret", srv.URL+"/v1/", "gpt-transcribe", "", 120)
	got, err := c.Transcribe(ctx(), audio)
	if err != nil || got != "testo trascritto" {
		t.Fatalf("got %q, %v", got, err)
	}
	u := srv.all()[0]
	if u.Path != "/v1/audio/transcriptions" || u.Auth != "Bearer secret" {
		t.Fatalf("%+v", u)
	}
	if u.Fields["model"] != "gpt-transcribe" || u.Filename != "voice.mp3" || u.Size != 5 {
		t.Fatalf("%+v", u)
	}
	if _, ok := u.Fields["language"]; ok {
		t.Fatal("unexpected language")
	}
	if len(u.Header.Values("HTTP-Referer")) != 0 {
		t.Fatal("openai must not send extra headers")
	}
}

func TestHTTPErrorRaisesTranscriptionError(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("rate limited for secret"))
	}
	audio := writeFile(t, t.TempDir(), "voice.mp3", "audio")
	_, err := NewCloudClient("secret", srv.URL, "m", "", 5).Transcribe(ctx(), audio)
	if err == nil || err.Error() != "Errore trascrizione HTTP 429: rate limited for ***" {
		t.Fatalf("err = %v", err)
	}
}

func TestLanguageFieldDependsOnModel(t *testing.T) {
	for model, want := range map[string]string{"gpt-transcribe": "languages", "whisper-1": "language"} {
		srv := newFakeAPI(t)
		audio := writeFile(t, t.TempDir(), "voice.wav", "audio")
		if _, err := NewCloudClient("k", srv.URL, model, "it", 5).Transcribe(ctx(), audio); err != nil {
			t.Fatal(err)
		}
		f := srv.all()[0].Fields
		other := "language"
		if want == "language" {
			other = "languages"
		}
		if f[want] != "it" {
			t.Fatalf("%s: fields %v", model, f)
		}
		if _, ok := f[other]; ok {
			t.Fatalf("%s: both fields sent", model)
		}
	}
}

func TestOggAliasesAreSentAsOggWithoutFFmpeg(t *testing.T) {
	old := ffmpegBin
	ffmpegBin = "/nonexistent/ffmpeg"
	defer func() { ffmpegBin = old }()
	for _, ext := range []string{".oga", ".opus"} {
		srv := newFakeAPI(t)
		audio := writeFile(t, t.TempDir(), "voice"+ext, "OggS")
		got, err := NewCloudClient("k", srv.URL, "m", "", 5).Transcribe(ctx(), audio)
		if err != nil || got != "testo trascritto" {
			t.Fatalf("%s: %q %v", ext, got, err)
		}
		if fn := srv.all()[0].Filename; fn != "voice.ogg" {
			t.Fatalf("filename %q", fn)
		}
	}
}

func TestUnsupportedFormatWithoutFFmpegFails(t *testing.T) {
	old := ffmpegBin
	ffmpegBin = "/nonexistent/ffmpeg"
	defer func() { ffmpegBin = old }()
	audio := writeFile(t, t.TempDir(), "voice.amr", "x")
	_, err := NewCloudClient("k", "http://127.0.0.1:1", "m", "", 5).Transcribe(ctx(), audio)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg non disponibile") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsupportedExtensionIsTranscodedBeforeUpload(t *testing.T) {
	srv := newFakeAPI(t)
	audio := makeAudio(t, t.TempDir(), "voice.aiff")
	got, err := NewCloudClient("k", srv.URL, "m", "", 30).Transcribe(ctx(), audio)
	if err != nil || got != "testo trascritto" {
		t.Fatalf("%q %v", got, err)
	}
	u := srv.all()[0]
	if !strings.HasSuffix(u.Filename, ".mp3") || u.Size == 0 {
		t.Fatalf("%+v", u)
	}
}

func TestFormatHTTPErrorRetriesAfterTranscoding(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(n int, _ upload, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("Unsupported file format"))
			return
		}
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}
	audio := makeAudio(t, t.TempDir(), "voice.wav")
	got, err := NewCloudClient("k", srv.URL, "m", "", 30).Transcribe(ctx(), audio)
	if err != nil || got != "ok" {
		t.Fatalf("%q %v", got, err)
	}
	ups := srv.all()
	if len(ups) != 2 || ups[0].Filename != "voice.wav" || !strings.HasSuffix(ups[1].Filename, ".mp3") {
		t.Fatalf("%+v", ups)
	}
}

func TestNonFormatErrorDoesNotRetry(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) { w.WriteHeader(500) }
	audio := writeFile(t, t.TempDir(), "voice.wav", "x")
	if _, err := NewCloudClient("k", srv.URL, "m", "", 5).Transcribe(ctx(), audio); err == nil {
		t.Fatal("want error")
	}
	if len(srv.all()) != 1 {
		t.Fatal("retried")
	}
}

func TestBadResponses(t *testing.T) {
	cases := map[string]string{
		"not json":  "Risposta non JSON: <html>",
		`[1]`:       "Risposta non valida: [1]",
		`{"a":"b"}`: `Risposta senza campo 'text': {"a":"b"}`,
	}
	for body, want := range cases {
		srv := newFakeAPI(t)
		srv.handler = func(_ int, _ upload, w http.ResponseWriter) {
			if body == "not json" {
				body = "<html>"
			}
			_, _ = w.Write([]byte(body))
		}
		audio := writeFile(t, t.TempDir(), "v.mp3", "x")
		_, err := NewCloudClient("k", srv.URL, "m", "", 5).Transcribe(ctx(), audio)
		if err == nil || err.Error() != want {
			t.Fatalf("%q: err = %v, want %q", body, err, want)
		}
	}
}

func TestNetworkError(t *testing.T) {
	audio := writeFile(t, t.TempDir(), "v.mp3", "x")
	_, err := NewCloudClient("k", "http://127.0.0.1:1", "m", "", 5).Transcribe(ctx(), audio)
	if err == nil || !strings.HasPrefix(err.Error(), "Errore di rete: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRouterMultipartAndHeaders(t *testing.T) {
	srv := newFakeAPI(t)
	audio := writeFile(t, t.TempDir(), "v.mp3", "x")
	c := NewOpenRouterClient("sk-or-k", srv.URL, "openai/whisper-large-v3", "it", 5,
		map[string]string{"HTTP-Referer": "https://app.test", "X-OpenRouter-Title": "Megachat"})
	if _, err := c.Transcribe(ctx(), audio); err != nil {
		t.Fatal(err)
	}
	u := srv.all()[0]
	if u.Fields["language"] != "it" || u.Fields["languages"] != "" {
		t.Fatalf("%v", u.Fields)
	}
	if u.Header.Get("HTTP-Referer") != "https://app.test" || u.Header.Get("X-OpenRouter-Title") != "Megachat" {
		t.Fatalf("%v", u.Header)
	}
	if c.Kind() != "OpenRouterTranscriptionClient" {
		t.Fatal(c.Kind())
	}
}

func TestGeminiRequestShape(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":" ciao "},{"text":"mondo "}]}}]}`))
	}
	audio := writeFile(t, t.TempDir(), "v.oga", "OggS")
	c := NewGeminiClient("AIzaKEY", srv.URL+"/v1beta/", "gemini-flash-latest", "it", 5)
	got, err := c.Transcribe(ctx(), audio)
	if err != nil || got != "ciao mondo" {
		t.Fatalf("%q %v", got, err)
	}
	u := srv.all()[0]
	if u.Path != "/v1beta/models/gemini-flash-latest:generateContent" || u.Header.Get("x-goog-api-key") != "AIzaKEY" {
		t.Fatalf("%+v", u)
	}
	body := u.Fields["body"]
	for _, want := range []string{`"mime_type":"audio/ogg"`, `"temperature":0`, "language code 'it'", `"data":"T2dnUw=="`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %s: %s", want, body)
		}
	}
}

func TestGeminiUnsupportedFormatIsTranscoded(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`))
	}
	audio := makeAudio(t, t.TempDir(), "voice.m4a")
	if _, err := NewGeminiClient("AIzaKEY", srv.URL, "m", "", 30).Transcribe(ctx(), audio); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(srv.all()[0].Fields["body"], `"mime_type":"audio/mp3"`) {
		t.Fatal("not sent as mp3")
	}
}

func TestGeminiRejectsLargeAudio(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.ogg")
	f, _ := os.Create(p)
	_ = f.Truncate(geminiMaxAudio + 1)
	f.Close()
	_, err := NewGeminiClient("k", "http://x", "m", "", 5).Transcribe(ctx(), p)
	if err == nil || !strings.Contains(err.Error(), "max 15MB") {
		t.Fatalf("%v", err)
	}
}

func TestGeminiEmptyAndBlocked(t *testing.T) {
	cases := []struct {
		payload, text, err string
	}{
		{`{}`, "", ""},
		{`{"candidates":[]}`, "", ""},
		{`{"candidates":[{"content":{"parts":[]}}]}`, "", ""},
		{`{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"SAFETY"}]}`, "hi", ""},
		{`{"promptFeedback":{"blockReason":"SAFETY"}}`, "", "Richiesta bloccata da Gemini (SAFETY)"},
		{`{"candidates":[{"finishReason":"PROHIBITED_CONTENT"}]}`, "", "Risposta bloccata da Gemini (PROHIBITED_CONTENT)"},
	}
	for _, c := range cases {
		srv := newFakeAPI(t)
		srv.handler = func(_ int, _ upload, w http.ResponseWriter) { _, _ = w.Write([]byte(c.payload)) }
		audio := writeFile(t, t.TempDir(), "v.ogg", "x")
		got, err := NewGeminiClient("k", srv.URL, "m", "", 5).Transcribe(ctx(), audio)
		if c.err == "" && (err != nil || got != c.text) {
			t.Fatalf("%s: %q %v", c.payload, got, err)
		}
		if c.err != "" && (err == nil || err.Error() != c.err) {
			t.Fatalf("%s: %v", c.payload, err)
		}
	}
}

func TestGeminiHTTPErrorNeverLeaksKey(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("bad key AIzaSECRETKEY123"))
	}
	audio := writeFile(t, t.TempDir(), "v.ogg", "x")
	_, err := NewGeminiClient("AIzaSECRETKEY123", srv.URL, "m", "", 5).Transcribe(ctx(), audio)
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "Gemini HTTP 403") {
		t.Fatalf("%v", err)
	}
}

func TestBuildClientSelectsClass(t *testing.T) {
	for p, kind := range map[string]string{"openai": "CloudTranscriptionClient",
		"openrouter": "OpenRouterTranscriptionClient", "gemini": "GeminiTranscriptionClient"} {
		c, err := BuildClient(p, "k", "http://b/", "m", "", 5, nil)
		if err != nil || c.Kind() != kind || c.BaseURL() != "http://b" {
			t.Fatalf("%s: %v %v", p, c, err)
		}
	}
	if _, err := BuildClient("deepseek", "k", "", "", "", 5, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestTranscodeToMP3(t *testing.T) {
	src := makeAudio(t, t.TempDir(), "a.wav")
	out, err := TranscodeToMP3(src)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out)
	if st, err := os.Stat(out); err != nil || st.Size() == 0 || filepath.Ext(out) != ".mp3" {
		t.Fatalf("%v %v", st, err)
	}
	bad := writeFile(t, t.TempDir(), "bad.wav", "not audio")
	if _, err := TranscodeToMP3(bad); err == nil || !strings.HasPrefix(err.Error(), "Transcodifica ffmpeg fallita: ") {
		t.Fatalf("%v", err)
	}
}
