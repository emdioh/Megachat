package whatsapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	gproto "google.golang.org/protobuf/proto"
)

// lazyWaitS is _LAZY_MEDIA_WAIT_S: the longest a lazy media request waits for
// the in-flight download of the same media.
const lazyWait = 120 * time.Second

// lazyMediaTokenPrefix / sep are models.LAZY_MEDIA_TOKEN_PREFIX / _SEP.
const (
	lazyTokenPrefix = "lazy-wa-"
	lazyTokenSep    = "~"
)

var lazyIDRe = regexp.MustCompile(`^[A-Za-z0-9@._:+-]{1,256}$`)

func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func unb64url(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	d, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", false
	}
	return string(d), true
}

// lazyMediaToken is models.lazy_media_token; ok=false is None.
func lazyMediaToken(contactID, msgID string) (string, bool) {
	for _, v := range []string{contactID, msgID} {
		if !lazyIDRe.MatchString(v) || strings.Contains(v, "..") {
			return "", false
		}
	}
	return lazyTokenPrefix + b64url(contactID) + lazyTokenSep + b64url(msgID), true
}

// parseLazyMediaToken is models.parse_lazy_media_token.
func parseLazyMediaToken(token string) (contactID, msgID string, ok bool) {
	if !strings.HasPrefix(token, lazyTokenPrefix) {
		return "", "", false
	}
	parts := strings.Split(token[len(lazyTokenPrefix):], lazyTokenSep)
	if len(parts) != 2 {
		return "", "", false
	}
	c, ok1 := unb64url(parts[0])
	m, ok2 := unb64url(parts[1])
	if !ok1 || !ok2 {
		return "", "", false
	}
	if t, ok := lazyMediaToken(c, m); !ok || t != token {
		return "", "", false
	}
	return c, m, true
}

// ---- media info (what is needed to download a message's media later) -------------------

// mediaInfoStore keeps, per message, the media message proto (URL, keys,
// hashes) so a download can happen long after the event: lazily, on demand.
// The proto is persisted in the device store file when it is open.
type mediaInfoStore struct {
	mu  sync.Mutex
	mem map[string][]byte // canonical id -> marshalled waE2E.Message
}

func newMediaInfoStore() mediaInfoStore { return mediaInfoStore{mem: map[string][]byte{}} }

const mediaTableDDL = `CREATE TABLE IF NOT EXISTS megachat_media (
	msg_id TEXT PRIMARY KEY, chat TEXT NOT NULL, msg BLOB NOT NULL, ts INTEGER NOT NULL)`

// wrapMedia wraps a downloadable media message into a waE2E.Message.
func wrapMedia(d whatsmeow.DownloadableMessage) *waE2E.Message {
	switch x := d.(type) {
	case *waE2E.ImageMessage:
		return &waE2E.Message{ImageMessage: x}
	case *waE2E.VideoMessage:
		return &waE2E.Message{VideoMessage: x}
	case *waE2E.AudioMessage:
		return &waE2E.Message{AudioMessage: x}
	case *waE2E.DocumentMessage:
		return &waE2E.Message{DocumentMessage: x}
	case *waE2E.StickerMessage:
		return &waE2E.Message{StickerMessage: x}
	}
	return nil
}

func (b *Backend) mediaDB() *sql.DB {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	return b.db
}

// rememberMedia records the media message of an event.
func (b *Backend) rememberMedia(chat, msgID string, info types.MessageInfo, e *events.Message, mp *mediaPart, attName string) {
	w := wrapMedia(mp.Msg)
	if w == nil {
		return
	}
	data, err := gproto.Marshal(w)
	if err != nil {
		return
	}
	key := CanonicalMsgID(msgID)
	b.mediaInfo.mu.Lock()
	b.mediaInfo.mem[key] = data
	b.mediaInfo.mu.Unlock()
	if db := b.mediaDB(); db != nil {
		if _, err := db.Exec(mediaTableDDL); err == nil {
			_, _ = db.Exec(`INSERT INTO megachat_media (msg_id, chat, msg, ts) VALUES (?, ?, ?, ?)
				ON CONFLICT(msg_id) DO UPDATE SET msg = excluded.msg`, key, chat, data, info.Timestamp.UnixMilli())
		}
	}
}

// lookupMedia returns the stored media message of a message id.
func (b *Backend) lookupMedia(msgID string) (*mediaPart, bool) {
	key := CanonicalMsgID(msgID)
	b.mediaInfo.mu.Lock()
	data, ok := b.mediaInfo.mem[key]
	b.mediaInfo.mu.Unlock()
	if !ok {
		db := b.mediaDB()
		if db == nil {
			return nil, false
		}
		if err := db.QueryRow(`SELECT msg FROM megachat_media WHERE msg_id = ?`, key).Scan(&data); err != nil {
			return nil, false
		}
	}
	var m waE2E.Message
	if gproto.Unmarshal(data, &m) != nil {
		return nil, false
	}
	c := extractContent(&m)
	return c.Media, c.Media != nil
}

// ---- downloads ----------------------------------------------------------------------------

// lazyCall is one in-flight media download shared by every waiter.
type lazyCall struct {
	done chan struct{}
	once sync.Once
	path string
}

func (c *lazyCall) finish(path string) {
	c.once.Do(func() {
		c.path = path
		close(c.done)
	})
}

// downloadTo downloads the media of msgID into <mediaDir>/<name> (atomically).
func (b *Backend) downloadTo(ctx context.Context, msgID, name string) (string, error) {
	cli := b.currentClient()
	if cli == nil || !b.connected.Load() {
		return "", ErrNotConnected
	}
	mp, ok := b.lookupMedia(msgID)
	if !ok {
		return "", errors.New("whatsapp: no media info for message")
	}
	dir, err := b.EnsureMediaDir()
	if err != nil {
		return "", err
	}
	dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	data, err := cli.Download(dctx, mp.Msg)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(dir, "."+name+"."+hex.EncodeToString(rnd[:])+".part")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return target, nil
}

// fetchMedia is single-flight + negative-cached download of one media file
// (the lazy media gate/coalescing/negative TTL of Python).
func (b *Backend) fetchMedia(ctx context.Context, key, msgID, name string) string {
	b.mediaMu.Lock()
	if exp, ok := b.lazyFailed[key]; ok {
		if exp.After(nowFunc()) {
			b.mediaMu.Unlock()
			return ""
		}
		delete(b.lazyFailed, key)
	}
	if c, ok := b.lazyFlight[key]; ok {
		b.mediaMu.Unlock()
		select {
		case <-c.done:
			return c.path
		case <-time.After(lazyWait):
			return ""
		case <-ctx.Done():
			return ""
		}
	}
	c := &lazyCall{done: make(chan struct{})}
	b.lazyFlight[key] = c
	b.mediaMu.Unlock()

	path, err := b.downloadTo(ctx, msgID, name)
	if err != nil {
		b.log.Debug("WhatsApp media download failed", "id", msgID, "err", err)
		path = ""
	}
	b.mediaMu.Lock()
	delete(b.lazyFlight, key)
	if path == "" && b.lazyNegTTL > 0 {
		b.lazyFailed[key] = nowFunc().Add(b.lazyNegTTL)
	}
	b.mediaMu.Unlock()
	c.finish(path)
	return path
}

// scheduleMediaPrefetch downloads a freshly received media in the background
// (WAHA did this with downloadMedia=true before the webhook fired).
func (b *Backend) scheduleMediaPrefetch(chat, msgID, name string) {
	if b.stopped.Load() || name == "" {
		return
	}
	dir := b.mediaDir
	if dir != "" && isFile(filepath.Join(dir, name)) {
		return
	}
	go func() {
		select {
		case b.prefetch <- struct{}{}:
			defer func() { <-b.prefetch }()
		case <-b.closed:
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		b.fetchMedia(ctx, "name:"+name, msgID, name)
	}()
}

// AttachmentPath implements proto.AttachmentResolver (get_attachment_path):
// the local file of an attachment id, downloading it on demand. Lazy tokens
// ("lazy-wa-...") resolve one message's media.
func (b *Backend) AttachmentPath(ctx context.Context, attachmentID string) (string, bool) {
	if attachmentID == "" {
		return "", false
	}
	if strings.HasPrefix(attachmentID, lazyTokenPrefix) {
		contact, msgID, ok := parseLazyMediaToken(attachmentID)
		if !ok || !isFetchableJID(contact) {
			return "", false
		}
		p := b.lazyResolveMedia(ctx, contact, msgID)
		return p, p != ""
	}
	dir, err := b.EnsureMediaDir()
	if err != nil {
		return "", false
	}
	bare, _, _ := strings.Cut(attachmentID, "?")
	name := pyBase(bare)
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	candidate := filepath.Join(dir, name)
	if isFile(candidate) {
		return candidate, true
	}
	// Lazy download of a message whose media was recorded.
	_, media := resolveMediaChatID(name)
	if media == "" {
		return "", false
	}
	if p := b.fetchMedia(ctx, "name:"+name, media, name); p != "" {
		return p, true
	}
	return "", false
}

// lazyMediaRow is _lazy_media_row: the stored row of a message across the
// chat's storage keys (@lid / @c.us twins).
func (b *Backend) lazyMediaRow(ctx context.Context, contactID, msgID string) (string, *store.MediaRow) {
	if b.st == nil {
		return contactID, nil
	}
	keys := []string{contactID}
	if c, ok := b.FindContact(contactID); ok {
		for _, k := range contactStorageKeys(c) {
			if !containsStr(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	for _, k := range keys {
		if row, err := b.st.MessageMediaRow(ctx, proto.ProtocolWhatsApp, k, msgID); err == nil && row != nil {
			return k, row
		}
	}
	return contactID, nil
}

// lazyResolveMedia is _lazy_resolve_media: resolve ONE pending media (a lazy
// token) to a local file and persist its attachment id.
func (b *Backend) lazyResolveMedia(ctx context.Context, contactID, msgID string) string {
	key, row := b.lazyMediaRow(ctx, contactID, msgID)
	if row != nil && row.AttachmentID != nil && *row.AttachmentID != "" {
		if dir, err := b.EnsureMediaDir(); err == nil {
			name := pyBase(*row.AttachmentID)
			if name != "" && isFile(filepath.Join(dir, name)) {
				return filepath.Join(dir, name)
			}
		}
	}
	if b.stopped.Load() {
		return ""
	}
	mp, ok := b.lookupMedia(msgID)
	if !ok {
		return ""
	}
	name := ""
	if row != nil && row.AttachmentID != nil && *row.AttachmentID != "" {
		name = pyBase(*row.AttachmentID)
	}
	if name == "" {
		name = sanitizeFileName(msgID) + mimeExtension(mp.Mime, mp.FileName)
	}
	path := b.fetchMedia(ctx, "lazy:"+key+"|"+msgID, msgID, name)
	if path == "" {
		return ""
	}
	if row != nil && (row.AttachmentID == nil || *row.AttachmentID == "") && b.st != nil {
		kind := mp.Kind
		var ts int64
		if row.Timestamp != 0 {
			ts = row.Timestamp
		}
		mt := msgTypeForMediaKind(kind)
		if _, err := b.st.UpdateMessageMediaIdentity(ctx, proto.ProtocolWhatsApp, key, msgID, ts, name, mt, &kind); err != nil {
			b.log.Debug("WhatsApp media identity update failed", "err", err)
		}
	}
	return path
}

// AttachmentChunk implements proto.AttachmentChunker (get_attachment_chunk): a
// byte range of the local file (downloaded first when missing: the media is
// end-to-end encrypted, so there is no remote range). start < 0 reads the tail.
func (b *Backend) AttachmentChunk(ctx context.Context, attachmentID string, start int64, length int) ([]byte, error) {
	if attachmentID == "" || length <= 0 {
		return nil, nil
	}
	path, ok := b.AttachmentPath(ctx, attachmentID)
	if !ok {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	if start < 0 {
		st, err := f.Stat()
		if err != nil {
			return nil, nil
		}
		start = st.Size() - int64(length)
		if start < 0 {
			start = 0
		}
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, nil
	}
	buf := make([]byte, length)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, nil
	}
	return buf[:n], nil
}

// ProfilePictureURL implements media.ProfilePictureURLProvider
// (rest.get_profile_picture_url). noPhoto is true when the account answered
// but has no (or hides its) picture, which earns a negative marker.
func (b *Backend) ProfilePictureURL(ctx context.Context, contactID string) (string, bool, error) {
	cli, err := b.requireClient()
	if err != nil {
		return "", false, err
	}
	j, err := JIDFromWAHA(contactID)
	if err != nil {
		return "", false, err
	}
	info, err := cli.GetProfilePictureInfo(ctx, j, &whatsmeow.GetProfilePictureParams{})
	switch {
	case errors.Is(err, whatsmeow.ErrProfilePictureNotSet), errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized):
		return "", true, nil
	case err != nil:
		return "", false, err
	case info == nil || info.URL == "":
		return "", true, nil
	}
	return info.URL, false, nil
}
