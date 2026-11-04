package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

func TestParsePreviewPrefersOpenGraphAndResolvesImage(t *testing.T) {
	doc := `<html><head><title>Plain</title>
	<meta property="og:title" content="  OG   Title ">
	<meta property="og:description" content="Desc">
	<meta property="og:site_name" content="Site">
	<meta property="og:image" content="/img/a.png">
	<meta property="og:image" content="/img/second.png">
	</head><body><meta property="og:title" content="late"></body></html>`
	p := ParsePreview(doc, "https://example.com/page")
	if p == nil || p.Title != "OG Title" || p.Description != "Desc" || p.SiteName != "Site" || p.ImageURL != "https://example.com/img/a.png" {
		t.Fatalf("%+v", p)
	}
}

func TestParsePreviewFallbacks(t *testing.T) {
	p := ParsePreview(`<head><title> Hello
	 world </title><meta name="description" content="D"></head>`, "https://www.example.com/x")
	if p == nil || p.Title != "Hello world" || p.Description != "D" || p.SiteName != "example.com" || p.ImageURL != "" {
		t.Fatalf("%+v", p)
	}
	if ParsePreview(`<head></head><body>nothing</body>`, "https://example.com/") != nil {
		t.Fatal("no title -> no preview")
	}
	// private image candidates are dropped
	p = ParsePreview(`<head><title>t</title><meta property="og:image" content="http://127.0.0.1/a.png"></head>`, "https://example.com/")
	if p == nil || p.ImageURL != "" {
		t.Fatalf("%+v", p)
	}
	long := strings.Repeat("é", 400)
	p = ParsePreview(`<head><title>`+long+`</title></head>`, "https://example.com/")
	if n := len([]rune(p.Title)); n != 200 || !strings.HasSuffix(p.Title, "…") {
		t.Fatalf("title len %d", n)
	}
}

func TestValidateURL(t *testing.T) {
	for _, u := range []string{
		"", "ftp://example.com/", "file:///etc/passwd", "http://localhost/", "http://foo.local/", "http://a.internal/",
		"http://user:pw@example.com/", "http://example.com:8080/", "http://127.0.0.1/", "http://10.0.0.1/", "http://[::1]/",
		"http://169.254.169.254/latest", "http://192.168.1.1/", "http://100.64.0.1/", "http://[::ffff:127.0.0.1]/",
		"http://0.0.0.0/", "http:///nohost", "http://224.0.0.1/", strings.Repeat("a", 2100),
	} {
		if _, err := ValidateURL(u); !IsBlockedURL(err) {
			t.Errorf("%q must be blocked (%v)", u, err)
		}
	}
	for _, u := range []string{"https://example.com/a?b=1", "http://example.com:80/", "https://example.com:443/", "http://8.8.8.8/", "https://[2606:4700:4700::1111]/"} {
		if _, err := ValidateURL(u); err != nil {
			t.Errorf("%q must be allowed: %v", u, err)
		}
	}
}

func newPreviewer(t *testing.T) *LinkPreviewer {
	e := newEnv(t)
	lp := NewLinkPreviewer(e.cfg)
	lp.allowLocal = true // httptest servers listen on 127.0.0.1
	t.Cleanup(lp.Close)
	return lp
}

func pngBytes() []byte { return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...) }

func TestFetchFollowsRedirectsAndServesImage(t *testing.T) {
	var htmlHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/page", http.StatusFound) })
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		htmlHits.Add(1)
		if !strings.Contains(r.UserAgent(), "MegachatLinkPreview") {
			t.Errorf("user agent %q", r.UserAgent())
		}
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		fmt.Fprint(w, "<html><head><title>Caff\xe8</title><meta property=\"og:image\" content=\"/i.png\"></head>")
	})
	mux.HandleFunc("/i.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml") // header lies; bytes decide
		w.Write(pngBytes())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lp := newPreviewer(t)

	p, err := lp.GetPreview(context.Background(), srv.URL+"/start")
	if err != nil || p == nil || p.Title != "Caffè" || p.URL != srv.URL+"/start" || p.ImageURL != srv.URL+"/i.png" {
		t.Fatalf("%+v %v", p, err)
	}
	// cached
	if _, err := lp.GetPreview(context.Background(), srv.URL+"/start"); err != nil || htmlHits.Load() != 1 {
		t.Fatalf("hits %d", htmlHits.Load())
	}
	data, ct, err := lp.GetPreviewImage(context.Background(), srv.URL+"/start")
	if err != nil || ct != "image/png" || len(data) != 48 {
		t.Fatalf("%d %q %v", len(data), ct, err)
	}
	path := lp.MaterializeImage(data, ct)
	if filepath.Ext(path) != ".png" || filepath.Dir(path) != lp.cfg.LinkPreviewImagesDir() {
		t.Fatalf("%s", path)
	}
	if lp.MaterializeImage(data, ct) != path {
		t.Fatal("content addressed")
	}
}

func TestPreviewImageRejectsSVGAndOversize(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<head><title>t</title><meta property="og:image" content="/s.svg"></head>`)
	})
	mux.HandleFunc("/s.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, "<svg/>")
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write(append(pngBytes(), make([]byte, maxImageBytes)...))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lp := newPreviewer(t)
	if data, _, err := lp.GetPreviewImage(context.Background(), srv.URL+"/p"); err != nil || data != nil {
		t.Fatalf("svg served: %v %v", data, err)
	}
	if data, _ := lp.FetchExternalImage(context.Background(), srv.URL+"/big", 0, 0); data != nil {
		t.Fatal("oversize image served")
	}
}

func TestRedirectToBlockedHostGivesNoPreview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
	}))
	defer srv.Close()
	lp := newPreviewer(t)
	if p, err := lp.GetPreview(context.Background(), srv.URL); err != nil || p != nil {
		t.Fatalf("%+v %v", p, err)
	}
	// redirect loop
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, r.URL.Path+"x", 302) }))
	defer loop.Close()
	if p, _ := lp.GetPreview(context.Background(), loop.URL+"/a"); p != nil {
		t.Fatal("redirect loop produced a preview")
	}
}

func TestNonHTMLNon200AndTooLargeGiveNoPreview(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}")
	})
	mux.HandleFunc("/404", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		fmt.Fprint(w, "<title>x</title>")
	})
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<head><title>x</title></head>"+strings.Repeat("a", maxHTMLBytes))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	lp := newPreviewer(t)
	for _, p := range []string{"/json", "/404", "/huge"} {
		if got, err := lp.GetPreview(context.Background(), srv.URL+p); err != nil || got != nil {
			t.Errorf("%s: %+v %v", p, got, err)
		}
	}
}

func TestFailureIsCachedShorterAndExpires(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	lp := newPreviewer(t)
	now := time.Now()
	lp.now = func() time.Time { return now }
	lp.GetPreview(context.Background(), srv.URL)
	lp.GetPreview(context.Background(), srv.URL)
	if hits.Load() != 1 {
		t.Fatalf("failure not cached: %d", hits.Load())
	}
	now = now.Add(PreviewFailTTL + time.Second)
	lp.GetPreview(context.Background(), srv.URL)
	if hits.Load() != 2 {
		t.Fatalf("failure cache must expire: %d", hits.Load())
	}
}

func TestConcurrentPreviewsShareOneFetch(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<head><title>t</title></head>")
	}))
	defer srv.Close()
	lp := newPreviewer(t)
	res := make(chan *Preview, 5)
	for i := 0; i < 5; i++ {
		go func() { p, _ := lp.GetPreview(context.Background(), srv.URL); res <- p }()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	for i := 0; i < 5; i++ {
		if p := <-res; p == nil || p.Title != "t" {
			t.Fatalf("%+v", p)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func TestLocationLinkUsesStoredThumbnailWithoutNetwork(t *testing.T) {
	e := newEnv(t)
	lp := NewLinkPreviewer(e.cfg)
	url := "https://maps.google.com/?q=45.464200,9.190000"
	if p, err := lp.GetPreview(context.Background(), url); p != nil || err != nil {
		t.Fatalf("no thumbnail yet: %+v %v", p, err)
	}
	writeFile(t, filepath.Join(e.cfg.LocationThumbsDir(), "45.464200_9.190000.jpg"), jpegBytes)
	p, err := lp.GetPreview(context.Background(), url)
	if err != nil || p == nil || p.Title != "Apri in Google Maps" || p.SiteName != "Google Maps" || !p.LocalImage {
		t.Fatalf("%+v %v", p, err)
	}
	data, ct, _ := lp.GetPreviewImage(context.Background(), url)
	if ct != "image/jpeg" || string(data) != string(jpegBytes) {
		t.Fatalf("%q %q", data, ct)
	}
	if _, ok := lpCoords("https://maps.google.com/?q=95.000000,9.190000"); ok {
		t.Fatal("latitude out of range accepted")
	}
}

func lpCoords(u string) (float64, bool) { la, _, ok := ParseMapsURL(u); return la, ok }

func TestLinksOps(t *testing.T) {
	e := newEnv(t)
	_, err := e.call("links.preview", map[string]any{"url": "http://127.0.0.1/"})
	wantCode(t, err, api.CodeInvalidRequest, "URL not allowed")
	_, err = e.call("links.preview_image", map[string]any{"url": "ftp://x/"})
	wantCode(t, err, api.CodeInvalidRequest, "URL not allowed")
	_, err = e.call("links.preview", map[string]any{})
	wantCode(t, err, api.CodeInvalidRequest, "Invalid request")
	_, err = e.call("links.preview", map[string]any{"url": "https://maps.google.com/?q=45.464200,9.190000"})
	wantCode(t, err, api.CodeNotFound, "No preview")

	// success path through the registry with a permissive previewer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/i.png" {
			w.Write(pngBytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<head><title>Card</title><meta property="og:image" content="/i.png"></head>`)
	}))
	defer srv.Close()
	lp := NewLinkPreviewer(e.cfg)
	lp.allowLocal = true
	d := e.deps
	d.Previewer = lp
	reg := newRegistry(d)
	out := callRaw(t, reg, "links.preview", map[string]any{"url": srv.URL})
	if out["title"] != "Card" || out["has_image"] != true || out["url"] != srv.URL || out["site_name"] == "" {
		t.Fatalf("%v", out)
	}
	out = callRaw(t, reg, "links.preview_image", map[string]any{"url": srv.URL})
	if out["content_type"] != "image/png" || out["immutable"] != false {
		t.Fatalf("%v", out)
	}
	if _, err := os.Stat(out["path"].(string)); err != nil {
		t.Fatal(err)
	}
}
