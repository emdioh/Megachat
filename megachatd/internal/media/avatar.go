package media

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // cache key
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

// Avatar cache TTLs (avatar_cache.py).
const (
	AvatarTTL         = 7 * 24 * time.Hour
	TelegramAvatarTTL = 24 * time.Hour
	NoPhotoTTL        = time.Hour
	avatarDownloadTO  = 15 * time.Second
	avatarBlobKeep    = 256
	maxAvatarDownload = 16 * 1024 * 1024
)

// AvatarCache is the on-disk cache of contact profile pictures.
type AvatarCache struct {
	cfg    Config
	client *http.Client
	// Now is the clock (tests).
	Now func() time.Time
}

// NewAvatarCache builds an AvatarCache over cfg.
func NewAvatarCache(cfg Config) *AvatarCache {
	return &AvatarCache{cfg: cfg, client: &http.Client{Timeout: avatarDownloadTO}, Now: time.Now}
}

func (a *AvatarCache) avatarPath(name, contactID string) (string, error) {
	h := sha1.Sum([]byte(contactID)) //nolint:gosec
	dir := a.cfg.AvatarDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, hex.EncodeToString(h[:])+".jpg"), nil
}

func noPhotoMarker(p string) string { return p + ".nophoto" }

func (a *AvatarCache) fresh(p string, ttl time.Duration, now time.Time) bool {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return now.Sub(st.ModTime()) <= ttl
}

func (a *AvatarCache) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAvatarDownload))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ResolveWhatsApp returns the cached avatar path, fetching it through WAHA on
// a miss; "" when there is no picture.
func (a *AvatarCache) ResolveWhatsApp(ctx context.Context, rest ProfilePictureURLProvider, contactID string) string {
	if rest == nil || contactID == "" {
		return ""
	}
	now := a.Now()
	path, err := a.avatarPath("whatsapp-avatars", contactID)
	if err != nil {
		return ""
	}
	marker := noPhotoMarker(path)
	if a.fresh(marker, NoPhotoTTL, now) {
		return ""
	}
	if a.fresh(path, AvatarTTL, now) {
		return path
	}
	l := pathLock(path)
	l.Lock()
	defer l.Unlock()
	if a.fresh(marker, NoPhotoTTL, now) {
		return ""
	}
	if a.fresh(path, AvatarTTL, now) {
		return path
	}
	url, noPhoto, err := rest.ProfilePictureURL(ctx, contactID)
	if err != nil {
		slog.Debug("WhatsApp avatar URL lookup failed", "err", err)
		return ""
	}
	if url != "" {
		data, err := a.download(ctx, url)
		if err == nil {
			err = writeAtomic(path, data)
		}
		if err != nil {
			slog.Debug("WhatsApp avatar download failed", "err", err)
			return ""
		}
		_ = os.Remove(marker)
		return path
	}
	if noPhoto {
		_ = os.WriteFile(marker, nil, 0o644)
	}
	return ""
}

// SniffImageContentType is sniff_image_content_type (default image/jpeg).
func SniffImageContentType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return "image/jpeg"
}

// ResolveTelegram returns the cached Telegram avatar bytes and type.
func (a *AvatarCache) ResolveTelegram(ctx context.Context, be ProfilePhotoProvider, contactID string) ([]byte, string, bool) {
	if be == nil || contactID == "" {
		return nil, "", false
	}
	now := a.Now()
	path, err := a.avatarPath("telegram-avatars", contactID)
	if err != nil {
		return nil, "", false
	}
	marker := noPhotoMarker(path)
	read := func() ([]byte, string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", false
		}
		return data, SniffImageContentType(data), true
	}
	if a.fresh(marker, NoPhotoTTL, now) {
		return nil, "", false
	}
	if a.fresh(path, TelegramAvatarTTL, now) {
		return read()
	}
	l := pathLock(path)
	l.Lock()
	defer l.Unlock()
	if a.fresh(marker, NoPhotoTTL, now) {
		return nil, "", false
	}
	if a.fresh(path, TelegramAvatarTTL, now) {
		return read()
	}
	data, _, err := be.ProfilePhoto(ctx, contactID)
	if err != nil {
		slog.Debug("Telegram avatar fetch failed", "err", err)
		return nil, "", false
	}
	if len(data) == 0 {
		_ = os.WriteFile(marker, nil, 0o644)
		return nil, "", false
	}
	if err := writeAtomic(path, data); err != nil {
		return nil, "", false
	}
	_ = os.Remove(marker)
	return data, SniffImageContentType(data), true
}

func (a *AvatarCache) materialize(proto string, data []byte) string {
	h := sha1.Sum(data) //nolint:gosec
	dir := a.cfg.AvatarBlobsDir(proto)
	path := filepath.Join(dir, hex.EncodeToString(h[:])+".img")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	l := pathLock(path)
	l.Lock()
	if isRegularFile(path) {
		touch(path)
		l.Unlock()
		return path
	}
	err := writeAtomic(path, data)
	l.Unlock()
	if err != nil {
		return ""
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "*.img"))
	type ent struct {
		p string
		t time.Time
	}
	var files []ent
	for _, p := range entries {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			files = append(files, ent{p, st.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].t.After(files[j].t) })
	for i := avatarBlobKeep; i < len(files); i++ {
		_ = os.Remove(files[i].p)
	}
	return path
}

// Resolve returns (data, contentType) of a contact avatar (resolve_avatar).
func (a *AvatarCache) resolve(ctx context.Context, b Backends, protocol, contactID string) ([]byte, string, bool) {
	if contactID == "" || b == nil {
		return nil, "", false
	}
	be, ok := b.Get(protocol)
	if !ok || be == nil {
		return nil, "", false
	}
	switch protocol {
	case api.ProtocolWhatsApp:
		rest, _ := be.(ProfilePictureURLProvider)
		p := a.ResolveWhatsApp(ctx, rest, contactID)
		if p == "" {
			return nil, "", false
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, "", false
		}
		return data, "image/jpeg", true
	case api.ProtocolSignal:
		pp, _ := be.(ProfilePhotoProvider)
		if pp == nil {
			return nil, "", false
		}
		data, ct, err := pp.ProfilePhoto(ctx, contactID)
		if err != nil || len(data) == 0 {
			return nil, "", false
		}
		if ct == "" {
			ct = SniffImageContentType(data)
		}
		return data, ct, true
	case api.ProtocolTelegram:
		pp, _ := be.(ProfilePhotoProvider)
		return a.ResolveTelegram(ctx, pp, contactID)
	}
	return nil, "", false
}

// ResolveFile is resolve_avatar_file: the picture as (local file, type).
func (a *AvatarCache) ResolveFile(ctx context.Context, b Backends, protocol, contactID string) (string, string, bool) {
	if contactID == "" || b == nil {
		return "", "", false
	}
	if protocol == api.ProtocolWhatsApp {
		be, ok := b.Get(protocol)
		if !ok || be == nil {
			return "", "", false
		}
		rest, _ := be.(ProfilePictureURLProvider)
		p := a.ResolveWhatsApp(ctx, rest, contactID)
		if p == "" || !isRegularFile(p) {
			return "", "", false
		}
		return p, "image/jpeg", true
	}
	data, ct, ok := a.resolve(ctx, b, protocol, contactID)
	if !ok {
		return "", "", false
	}
	if protocol == api.ProtocolTelegram {
		if p, err := a.avatarPath("telegram-avatars", contactID); err == nil && isRegularFile(p) {
			return p, ct, true
		}
	}
	p := a.materialize(protocol, data)
	if p == "" {
		return "", "", false
	}
	return p, ct, true
}

// AvatarBlob is the contact's profile picture as a blob (not_found: none).
func (d *Deps) AvatarBlob(ctx context.Context, protocol, contactID string) (api.BlobOut, error) {
	p, ct, ok := d.Avatars.ResolveFile(ctx, d.Backends, protocol, contactID)
	if !ok {
		return api.BlobOut{}, ops.Errorf(api.CodeNotFound, "No profile picture")
	}
	if strings.TrimSpace(ct) == "" {
		return BlobFor(p, nil, false), nil
	}
	return BlobFor(p, &ct, false), nil
}
