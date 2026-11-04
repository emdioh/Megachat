package media

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Link preview limits (server/link_preview.py).
const (
	PreviewTTL       = 6 * time.Hour
	PreviewFailTTL   = 30 * time.Minute
	previewCacheSize = 512
	previewTimeout   = 6 * time.Second
	maxRedirects     = 4
	maxHTMLBytes     = 512 * 1024
	maxImageBytes    = 3 * 1024 * 1024
	maxURLLength     = 2048
	titleLimit       = 200
	descLimit        = 300
	userAgent        = "Mozilla/5.0 (compatible; MegachatLinkPreview/1.0)"
	imageKeep        = 256
)

// BlockedURLError: the URL (or a redirect hop) points somewhere previews may
// not go.
type BlockedURLError struct{ Reason string }

func (e *BlockedURLError) Error() string { return e.Reason }

func blocked(format string, a ...any) error {
	return &BlockedURLError{Reason: fmt.Sprintf(format, a...)}
}

// IsBlockedURL reports whether err is a *BlockedURLError.
func IsBlockedURL(err error) bool {
	var b *BlockedURLError
	return errors.As(err, &b)
}

// Preview is the sanitized Open Graph card of a page.
type Preview struct {
	URL         string
	Title       string
	Description string
	SiteName    string
	ImageURL    string
	LocalImage  bool // location card served from the map thumbnail store
}

var nonGlobalV4 = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)
var nonGlobalV6 = mustPrefixes(
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32",
	"2001:10::/28", "fc00::/7", "fe80::/10", "ff00::/8",
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// isPublicIP is ip.is_global and not multicast (IPv4-mapped unwrapped).
func isPublicIP(value string) bool {
	value, _, _ = strings.Cut(value, "%")
	a, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	a = a.Unmap()
	table := nonGlobalV6
	if a.Is4() {
		table = nonGlobalV4
	}
	for _, p := range table {
		if p.Contains(a) {
			// 2001:1::1, 2001:1::2 and 2001:3::/32, 2001:4:112::/48, 2001:20::/28 are
			// globally reachable carve-outs of 2001::/23; treated as non-public
			// here (conservative).
			return false
		}
	}
	return true
}

// ValidateURL returns u if previews may fetch it, else a *BlockedURLError.
func ValidateURL(raw string) (string, error) {
	return validateURL(raw, false)
}

func validateURL(raw string, allowLocal bool) (string, error) {
	if raw == "" || len(raw) > maxURLLength {
		return "", blocked("invalid url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", blocked("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", blocked("scheme not allowed")
	}
	if allowLocal {
		return raw, nil
	}
	host := strings.ToLower(strings.TrimRight(u.Hostname(), "."))
	if host == "" || u.User != nil {
		return "", blocked("invalid host")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return "", blocked("port not allowed")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", blocked("host not allowed")
	}
	if _, err := netip.ParseAddr(strings.SplitN(host, "%", 2)[0]); err != nil {
		return raw, nil
	}
	if !isPublicIP(host) {
		return "", blocked("address not allowed")
	}
	return raw, nil
}

// ---- HTML ----------------------------------------------------------------

func clean(value string, limit int) string {
	text := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	r := []rune(text)
	return strings.TrimRight(string(r[:limit-1]), " \t\n\r") + "…"
}

type metaScan struct {
	meta  map[string]string
	title string
}

func scanMeta(doc string) metaScan {
	ms := metaScan{meta: map[string]string{}}
	z := html.NewTokenizer(strings.NewReader(doc))
	inTitle := false
loop:
	for {
		switch z.Next() {
		case html.ErrorToken:
			break loop
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "body":
				break loop
			case "title":
				inTitle = true
			case "meta":
				values := map[string]string{}
				for _, a := range t.Attr {
					values[strings.ToLower(a.Key)] = a.Val
				}
				key := values["property"]
				if key == "" {
					key = values["name"]
				}
				key = strings.ToLower(key)
				content := strings.TrimSpace(values["content"])
				if _, dup := ms.meta[key]; key != "" && content != "" && !dup {
					ms.meta[key] = content
				}
			}
		case html.EndTagToken:
			t := z.Token()
			if t.Data == "title" {
				inTitle = false
			} else if t.Data == "head" {
				break loop
			}
		case html.TextToken:
			if inTitle && len(ms.title) < 1000 {
				ms.title += z.Token().Data
			}
		}
	}
	return ms
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

// ParsePreview extracts a preview from html, or nil when there is no title.
func ParsePreview(doc, pageURL string) *Preview { return parsePreview(doc, pageURL, false) }

func parsePreview(doc, pageURL string, allowLocal bool) *Preview {
	ms := scanMeta(doc)
	title := clean(firstNonEmpty(ms.meta, "og:title", "twitter:title"), titleLimit)
	if title == "" {
		title = clean(ms.title, titleLimit)
	}
	if title == "" {
		return nil
	}
	description := clean(firstNonEmpty(ms.meta, "og:description", "twitter:description", "description"), descLimit)
	site := clean(ms.meta["og:site_name"], 80)
	if site == "" {
		if u, err := url.Parse(pageURL); err == nil {
			site = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		}
	}
	p := &Preview{URL: pageURL, Title: title, Description: description, SiteName: site}
	if raw := firstNonEmpty(ms.meta, "og:image:secure_url", "og:image", "og:image:url", "twitter:image", "twitter:image:src"); raw != "" {
		if base, err := url.Parse(pageURL); err == nil {
			if ref, err := url.Parse(strings.TrimSpace(raw)); err == nil {
				cand := base.ResolveReference(ref).String()
				if v, err := validateURL(cand, allowLocal); err == nil {
					p.ImageURL = v
				}
			}
		}
	}
	return p
}

// ---- fetcher ---------------------------------------------------------------

type cacheEntry struct {
	key     string
	expires time.Time
	value   *Preview
}

type inflight struct {
	done  chan struct{}
	value *Preview
}

// LinkPreviewer fetches and caches previews. Safe for concurrent use.
type LinkPreviewer struct {
	cfg    Config
	client *http.Client
	now    func() time.Time

	// allowLocal disables the SSRF guard (tests with httptest servers only).
	allowLocal bool

	mu      sync.Mutex
	lru     *list.List
	items   map[string]*list.Element
	pending map[string]*inflight
}

// NewLinkPreviewer builds a previewer whose connections only ever go to
// public addresses: the address that is validated is the one dialled.
func NewLinkPreviewer(cfg Config) *LinkPreviewer {
	lp := &LinkPreviewer{cfg: cfg, now: time.Now, lru: list.New(), items: map[string]*list.Element{}, pending: map[string]*inflight{}}
	dialer := &net.Dialer{Timeout: previewTimeout}
	tr := &http.Transport{
		Proxy:               nil, // trust_env=False
		MaxIdleConnsPerHost: 8,
		MaxConnsPerHost:     8,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if lp.allowLocal {
				return dialer.DialContext(ctx, network, addr)
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, blocked("non-public address for %s", host)
			}
			for _, ip := range ips {
				if !isPublicIP(ip.IP.String()) {
					return nil, blocked("non-public address for %s", host)
				}
			}
			var last error
			for _, ip := range ips {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
	lp.client = &http.Client{
		Transport:     tr,
		Timeout:       previewTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return lp
}

// Close releases idle connections.
func (lp *LinkPreviewer) Close() { lp.client.CloseIdleConnections() }

type fetched struct {
	finalURL    string
	contentType string
	charset     string
	body        []byte
}

// get follows validated redirects and returns the body (status 200 only).
func (lp *LinkPreviewer) get(ctx context.Context, raw string, maxBytes int64) (*fetched, error) {
	current, err := validateURL(raw, lp.allowLocal)
	if err != nil {
		return nil, err
	}
	for i := 0; i <= maxRedirects; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return nil, blocked("invalid url")
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept-Language", "it,en;q=0.8")
		resp, err := lp.client.Do(req)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case 301, 302, 303, 307, 308:
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return nil, blocked("redirect without location")
			}
			base, _ := url.Parse(current)
			ref, err := url.Parse(loc)
			if err != nil {
				return nil, blocked("invalid url")
			}
			if current, err = validateURL(base.ResolveReference(ref).String(), lp.allowLocal); err != nil {
				return nil, err
			}
			continue
		case 200:
		default:
			resp.Body.Close()
			return nil, blocked("status %d", resp.StatusCode)
		}
		if resp.ContentLength > maxBytes {
			resp.Body.Close()
			return nil, blocked("response too large")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > maxBytes {
			return nil, blocked("response too large")
		}
		ct, cs := "", ""
		if mt, params, err := parseMedia(resp.Header.Get("Content-Type")); err == nil {
			ct, cs = mt, params["charset"]
		}
		return &fetched{finalURL: current, contentType: ct, charset: cs, body: body}, nil
	}
	return nil, blocked("too many redirects")
}

func parseMedia(v string) (string, map[string]string, error) {
	if v == "" {
		return "", nil, nil
	}
	mt, params, err := mimeParse(v)
	return mt, params, err
}

var metaCharsetRE = regexp.MustCompile(`(?i)<meta[^>]+charset=["']?([\w-]+)`)

func (lp *LinkPreviewer) fetchPreviewUncached(ctx context.Context, raw string) (*Preview, error) {
	f, err := lp.get(ctx, raw, maxHTMLBytes)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(f.contentType, "html") {
		return nil, nil
	}
	if u, err := url.Parse(f.finalURL); err == nil && strings.HasPrefix(strings.ToLower(u.Hostname()), "consent.") {
		return nil, nil
	}
	label := f.charset
	if label == "" {
		head := f.body
		if len(head) > 4096 {
			head = head[:4096]
		}
		if m := metaCharsetRE.FindSubmatch(head); m != nil {
			label = string(m[1])
		} else {
			label = "utf-8"
		}
	}
	text := decodeBody(f.body, label)
	p := parsePreview(text, f.finalURL, lp.allowLocal)
	if p != nil {
		p.URL = raw
	}
	return p, nil
}

func decodeBody(body []byte, label string) string {
	r, err := charset.NewReaderLabel(label, bytes.NewReader(body))
	if err != nil {
		r = bytes.NewReader(body)
	}
	b, _ := io.ReadAll(r)
	return strings.ToValidUTF8(string(b), "�")
}

func (lp *LinkPreviewer) cacheGet(key string) (bool, *Preview) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	el, ok := lp.items[key]
	if !ok {
		return false, nil
	}
	e := el.Value.(*cacheEntry)
	if e.expires.Before(lp.now()) {
		lp.lru.Remove(el)
		delete(lp.items, key)
		return false, nil
	}
	lp.lru.MoveToBack(el)
	return true, e.value
}

func (lp *LinkPreviewer) cachePut(key string, v *Preview) {
	ttl := PreviewTTL
	if v == nil {
		ttl = PreviewFailTTL
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	if el, ok := lp.items[key]; ok {
		lp.lru.Remove(el)
	}
	lp.items[key] = lp.lru.PushBack(&cacheEntry{key, lp.now().Add(ttl), v})
	for lp.lru.Len() > previewCacheSize {
		front := lp.lru.Front()
		lp.lru.Remove(front)
		delete(lp.items, front.Value.(*cacheEntry).key)
	}
}

var mapsURLRE = regexp.MustCompile(`^https://maps\.google\.com/\?q=(-?\d{1,3}\.\d{6}),(-?\d{1,3}\.\d{6})$`)

// ParseMapsURL returns (lat, lng) for a link built by maps_url.
func ParseMapsURL(u string) (lat, lng float64, ok bool) {
	m := mapsURLRE.FindStringSubmatch(u)
	if m == nil {
		return 0, 0, false
	}
	lat, _ = strconv.ParseFloat(m[1], 64)
	lng, _ = strconv.ParseFloat(m[2], 64)
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

func (lp *LinkPreviewer) locationThumbPath(lat, lng float64) string {
	return filepath.Join(lp.cfg.LocationThumbsDir(), fmt.Sprintf("%.6f_%.6f.jpg", lat, lng))
}

// GetPreview returns the (cached) preview of rawURL, nil if there is none, or
// a *BlockedURLError for a URL that may not be fetched at all.
func (lp *LinkPreviewer) GetPreview(ctx context.Context, rawURL string) (*Preview, error) {
	if lat, lng, ok := ParseMapsURL(rawURL); ok {
		if !isRegularFile(lp.locationThumbPath(lat, lng)) {
			return nil, nil
		}
		return &Preview{URL: rawURL, Title: "Apri in Google Maps", SiteName: "Google Maps", LocalImage: true}, nil
	}
	if _, err := validateURL(rawURL, lp.allowLocal); err != nil {
		return nil, err
	}
	if hit, v := lp.cacheGet(rawURL); hit {
		return v, nil
	}
	lp.mu.Lock()
	if p, ok := lp.pending[rawURL]; ok {
		lp.mu.Unlock()
		select {
		case <-p.done:
			return p.value, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p := &inflight{done: make(chan struct{})}
	lp.pending[rawURL] = p
	lp.mu.Unlock()

	// The fetch outlives a cancelled caller (shielded in Python).
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*previewTimeout)
	defer cancel()
	value, err := lp.fetchPreviewUncached(fctx, rawURL)
	if err != nil {
		slog.Debug("link preview failed", "url", rawURL, "err", err)
		value = nil
	}
	lp.cachePut(rawURL, value)
	lp.mu.Lock()
	delete(lp.pending, rawURL)
	lp.mu.Unlock()
	p.value = value
	close(p.done)
	return value, nil
}

// GetPreviewImage returns the bytes and sniffed type of the preview image
// advertised by the page's own preview (never a generic fetch proxy).
func (lp *LinkPreviewer) GetPreviewImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	p, err := lp.GetPreview(ctx, rawURL)
	if err != nil {
		return nil, "", err
	}
	if p != nil && p.LocalImage {
		if lat, lng, ok := ParseMapsURL(rawURL); ok {
			if data, err := os.ReadFile(lp.locationThumbPath(lat, lng)); err == nil && len(data) > 0 {
				return data, "image/jpeg", nil
			}
		}
		return nil, "", nil
	}
	if p == nil || p.ImageURL == "" {
		return nil, "", nil
	}
	f, err := lp.get(ctx, p.ImageURL, maxImageBytes)
	if err != nil {
		slog.Debug("link preview image failed", "url", p.ImageURL, "err", err)
		return nil, "", nil
	}
	sniffed := sniffRaster(f.body)
	if sniffed == "" {
		return nil, "", nil
	}
	return f.body, sniffed, nil
}

var imageExt = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}

// MaterializeImage stores data content-addressed (sha256) under the link
// preview image directory and keeps the newest 256 files. "" on failure.
func (lp *LinkPreviewer) MaterializeImage(data []byte, contentType string) string {
	sum := sha256.Sum256(data)
	dir := lp.cfg.LinkPreviewImagesDir()
	ext, ok := imageExt[contentType]
	if !ok {
		ext = ".img"
	}
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+ext)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	if isRegularFile(path) {
		touch(path)
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return ""
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
		return ""
	}
	_ = os.Chmod(path, 0o644)
	entries, _ := os.ReadDir(dir)
	type ent struct {
		p string
		t time.Time
	}
	var files []ent
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			files = append(files, ent{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].t.After(files[j].t) })
	for i := imageKeep; i < len(files); i++ {
		_ = os.Remove(files[i].p)
	}
	return path
}

// FetchExternalImage fetches a public raster image through the SSRF-safe
// session (Gmail image proxy). maxPixels 0 = unlimited; unparseable headers
// fail open. nil body when rejected.
func (lp *LinkPreviewer) FetchExternalImage(ctx context.Context, rawURL string, maxBytes int64, maxPixels int64) ([]byte, string) {
	if maxBytes <= 0 {
		maxBytes = maxImageBytes
	}
	f, err := lp.get(ctx, rawURL, maxBytes)
	if err != nil {
		return nil, ""
	}
	sniffed := sniffRaster(f.body)
	if sniffed == "" {
		return nil, ""
	}
	if maxPixels > 0 {
		if w, h, ok := rasterDimensions(f.body, sniffed); ok && int64(w)*int64(h) > maxPixels {
			return nil, ""
		}
	}
	return f.body, sniffed
}

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

func jpegDimensions(d []byte) (int, int, bool) {
	i, size := 2, len(d)
	for i+1 < size {
		if d[i] != 0xFF {
			return 0, 0, false
		}
		for i < size && d[i] == 0xFF {
			i++
		}
		if i >= size {
			return 0, 0, false
		}
		marker := d[i]
		i++
		if marker == 0x00 {
			continue
		}
		if marker == 0xD9 {
			return 0, 0, false
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			continue
		}
		if marker == 0xDA || i+2 > size {
			return 0, 0, false
		}
		sl := int(binary.BigEndian.Uint16(d[i : i+2]))
		if sl < 2 || i+sl > size {
			return 0, 0, false
		}
		if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if sl < 7 {
				return 0, 0, false
			}
			h := int(binary.BigEndian.Uint16(d[i+3 : i+5]))
			w := int(binary.BigEndian.Uint16(d[i+5 : i+7]))
			return w, h, true
		}
		i += sl
	}
	return 0, 0, false
}

func le24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

func webpDimensions(d []byte) (int, int, bool) {
	if len(d) < 20 || string(d[:4]) != "RIFF" || string(d[8:12]) != "WEBP" {
		return 0, 0, false
	}
	kind := string(d[12:16])
	cs := int(binary.LittleEndian.Uint32(d[16:20]))
	if 20+cs > len(d) {
		return 0, 0, false
	}
	switch kind {
	case "VP8X":
		if cs < 10 || len(d) < 30 {
			return 0, 0, false
		}
		return le24(d[24:27]) + 1, le24(d[27:30]) + 1, true
	case "VP8 ":
		if cs < 10 || len(d) < 30 || string(d[23:26]) != "\x9d\x01\x2a" {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(d[26:28]) & 0x3FFF), int(binary.LittleEndian.Uint16(d[28:30]) & 0x3FFF), true
	case "VP8L":
		if cs < 5 || len(d) < 25 || d[20] != 0x2F {
			return 0, 0, false
		}
		packed := binary.LittleEndian.Uint32(d[21:25])
		return int(packed&0x3FFF) + 1, int((packed>>14)&0x3FFF) + 1, true
	}
	return 0, 0, false
}

func rasterDimensions(d []byte, ct string) (int, int, bool) {
	switch ct {
	case "image/png":
		if len(d) < 24 || string(d[12:16]) != "IHDR" {
			return 0, 0, false
		}
		return int(binary.BigEndian.Uint32(d[16:20])), int(binary.BigEndian.Uint32(d[20:24])), true
	case "image/jpeg":
		return jpegDimensions(d)
	case "image/gif":
		if len(d) < 10 {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(d[6:8])), int(binary.LittleEndian.Uint16(d[8:10])), true
	case "image/webp":
		return webpDimensions(d)
	}
	return 0, 0, false
}

func mimeParse(v string) (string, map[string]string, error) { return mime.ParseMediaType(v) }
