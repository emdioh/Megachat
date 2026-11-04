package gmail

import (
	"bytes"
	"context"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"regexp"
	"sync"
	"time"
)

const (
	remotePrefetchMax     = 30
	remotePrefetchConc    = 4
	remoteImageTTL        = 15 * time.Minute
	remoteImageCacheBytes = 48 * 1024 * 1024
)

var dataSrcRE = regexp.MustCompile(`data-src="(gmail-image\?token=[^"]+)"`)

type remoteEntry struct {
	at   time.Time
	data []byte
	ct   string
}

type flight struct {
	done chan struct{}
	data []byte
	ct   string
	ok   bool
}

type remoteImages struct {
	mu       sync.Mutex
	cache    map[string]*remoteEntry
	order    []string
	bytes    int
	inflight map[string]*flight
}

func (r *remoteImages) init() {
	r.cache = map[string]*remoteEntry{}
	r.inflight = map[string]*flight{}
}

func (r *remoteImages) dropLocked(u string) {
	if e, ok := r.cache[u]; ok {
		r.bytes -= len(e.data)
		delete(r.cache, u)
	}
	for i, o := range r.order {
		if o == u {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// ClearRemoteImageCache forgets cached and in-flight remote images.
func (s *Service) ClearRemoteImageCache() {
	r := &s.remote
	r.mu.Lock()
	r.init()
	r.order, r.bytes = nil, 0
	r.mu.Unlock()
}

// remoteImage returns an image from the cache, a shared in-flight fetch or a
// new fetch through the SSRF-safe fetcher.
func (s *Service) remoteImage(ctx context.Context, u string) ([]byte, string, bool) {
	r := &s.remote
	r.mu.Lock()
	if e := r.cache[u]; e != nil {
		if s.now().Sub(e.at) <= remoteImageTTL {
			d, ct := e.data, e.ct
			r.mu.Unlock()
			return d, ct, true
		}
		r.dropLocked(u)
	}
	f := r.inflight[u]
	if f == nil {
		f = &flight{done: make(chan struct{})}
		r.inflight[u] = f
		go func() {
			fctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if s.images != nil {
				f.data, f.ct = s.images.FetchExternalImage(fctx, u, imageProxyMaxBytes, imageProxyMaxPixels)
				f.ok = f.data != nil
			}
			r.mu.Lock()
			if f.ok {
				r.dropLocked(u)
				r.cache[u] = &remoteEntry{at: s.now(), data: f.data, ct: f.ct}
				r.order = append(r.order, u)
				r.bytes += len(f.data)
				for r.bytes > remoteImageCacheBytes && len(r.order) > 0 {
					r.dropLocked(r.order[0])
				}
			}
			delete(r.inflight, u)
			r.mu.Unlock()
			close(f.done)
		}()
	}
	r.mu.Unlock()
	select {
	case <-f.done:
		return f.data, f.ct, f.ok
	case <-ctx.Done():
		return nil, "", false
	}
}

// remoteImageURLs lists the distinct remote URLs (not inline parts) of the
// proxied images of a sanitised document.
func (s *Service) remoteImageURLs(doc string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, m := range dataSrcRE.FindAllStringSubmatch(doc, -1) {
		u, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			continue
		}
		v := s.verifyImageToken(u.Query().Get("token"))
		if v == "" || len(v) >= len(partImagePrefix) && v[:len(partImagePrefix)] == partImagePrefix || seen[v] {
			continue
		}
		seen[v] = true
		urls = append(urls, v)
		if len(urls) >= remotePrefetchMax {
			break
		}
	}
	return urls
}

func (s *Service) scheduleRemotePrefetch(doc string) {
	if !s.cfg.GmailImagePrefetch() || doc == "" || s.images == nil {
		return
	}
	urls := s.remoteImageURLs(doc)
	if len(urls) == 0 {
		return
	}
	go func() {
		sem := make(chan struct{}, remotePrefetchConc)
		var wg sync.WaitGroup
		for _, u := range urls {
			wg.Add(1)
			sem <- struct{}{}
			go func(u string) {
				defer wg.Done()
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				s.remoteImage(ctx, u)
			}(u)
		}
		wg.Wait()
	}()
}

// sniffRaster identifies jpeg/png/gif/webp by magic bytes.
func sniffRaster(d []byte) string {
	switch {
	case bytes.HasPrefix(d, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(d, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(d, []byte("GIF87a")), bytes.HasPrefix(d, []byte("GIF89a")):
		return "image/gif"
	case len(d) >= 12 && string(d[:4]) == "RIFF" && string(d[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// inlinePartImage returns the bytes of a cid: image part if it is a raster
// image within the proxy caps.
func (s *Service) inlinePartImage(ctx context.Context, payload string) ([]byte, string, error) {
	folder, uid, index, ok := s.parsePartImage(payload, s.now())
	if !ok {
		return nil, "", errImageNotFound
	}
	data := s.cachedInlineImage(folder, uid, index)
	if data == nil {
		dl, err := s.FetchAttachment(ctx, int(uid), index, &folder)
		if err != nil {
			return nil, "", err
		}
		data = dl.Data
	}
	if len(data) == 0 || len(data) > imageProxyMaxBytes {
		return nil, "", nil
	}
	ct := sniffRaster(data)
	if ct == "" {
		return nil, "", nil
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && int64(cfg.Width)*int64(cfg.Height) > imageProxyMaxPixels {
		return nil, "", nil
	}
	return data, ct, nil
}
