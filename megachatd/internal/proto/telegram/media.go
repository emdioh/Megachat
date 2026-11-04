package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// EnsureMediaDir implements media.MediaDirProvider: the persistent Telegram
// media directory (<DataDir>/telegram-media), created on demand.
func (b *Backend) EnsureMediaDir() (string, error) {
	dir := b.mediaDirPath()
	if dir == "" {
		return "", errors.New("telegram: media directory not configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// photoLocation picks the largest size of a photo (Telethon _get_thumb).
func photoLocation(p *tg.Photo) tg.InputFileLocationClass {
	best, bestSize := "", -1
	for _, s := range p.Sizes {
		var typ string
		var size int
		switch t := s.(type) {
		case *tg.PhotoSize:
			typ, size = t.Type, t.Size
		case *tg.PhotoSizeProgressive:
			typ = t.Type
			for _, v := range t.Sizes {
				if v > size {
					size = v
				}
			}
		case *tg.PhotoCachedSize:
			typ, size = t.Type, len(t.Bytes)
		default:
			continue
		}
		if size > bestSize {
			best, bestSize = typ, size
		}
	}
	return &tg.InputPhotoFileLocation{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference, ThumbSize: best}
}

func documentLocation(d *tg.Document) tg.InputFileLocationClass {
	return &tg.InputDocumentFileLocation{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}
}

func (mi mediaInfo) location() tg.InputFileLocationClass {
	if mi.Photo != nil {
		return photoLocation(mi.Photo)
	}
	if mi.Doc != nil {
		return documentLocation(mi.Doc)
	}
	return nil
}

// liveFilename mirrors Telethon's download_media(file=<dir>) naming
// (_get_proper_filename): the document file name, "performer - title" for
// audio, else "<kind>_YYYY-MM-DD_HH-MM-SS<ext>" (UTC); an existing file is
// never overwritten ("name (1).ext").
func liveFilename(dir string, mi mediaInfo) string {
	var kind, extension string
	var date int
	var possible []string
	switch {
	case mi.Photo != nil:
		kind, extension, date = "photo", ".jpg", mi.Photo.Date
	case mi.Doc != nil:
		kind, extension, date = "document", guessExtension(mi.Doc.MimeType), mi.Doc.Date
		for _, attr := range mi.Doc.Attributes {
			switch t := attr.(type) {
			case *tg.DocumentAttributeFilename:
				possible = append([]string{t.FileName}, possible...)
			case *tg.DocumentAttributeAudio:
				kind = "audio"
				switch {
				case t.Performer != "" && t.Title != "":
					possible = append(possible, t.Performer+" - "+t.Title)
				case t.Performer != "":
					possible = append(possible, t.Performer)
				case t.Title != "":
					possible = append(possible, t.Title)
				case t.Voice:
					kind = "voice"
				}
			}
		}
	default:
		return ""
	}
	name := ""
	for _, p := range possible {
		if p != "" {
			name = filepath.Base(p)
			break
		}
	}
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		t := time.Unix(int64(date), 0).UTC()
		name = fmt.Sprintf("%s_%d-%02d-%02d_%02d-%02d-%02d", kind, t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	}
	stem, ext := splitExt(name)
	if ext == "" {
		ext = extension
	}
	result := filepath.Join(dir, stem+ext)
	if !isFile(result) {
		return result
	}
	for i := 1; ; i++ {
		result = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if !isFile(result) {
			return result
		}
	}
}

// downloadToPath downloads a location into path atomically (via .part).
func downloadToPath(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	if _, err := downloader.NewDownloader().Download(api, loc).ToPath(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// downloadMedia is the live msg.download_media(file=<media dir>): it returns
// the stored path ("" when the message has no downloadable media).
func (b *Backend) downloadMedia(ctx context.Context, api *tg.Client, msg *tg.Message) (string, error) {
	mi := messageMedia(msg)
	if !mi.any() {
		return "", nil
	}
	dir, err := b.EnsureMediaDir()
	if err != nil {
		return "", err
	}
	path := liveFilename(dir, mi)
	if err := downloadToPath(ctx, api, mi.location(), path); err != nil {
		return "", err
	}
	return path, nil
}

// ---- message lookup ------------------------------------------------------------------

// fetchMessage is client.get_messages(entity, ids=msgID): nil when gone.
func (b *Backend) fetchMessage(ctx context.Context, api *tg.Client, chatID int64, msgID int) (*tg.Message, entityMaps, error) {
	peer, err := b.resolveInputPeer(chatID)
	if err != nil {
		return nil, entityMaps{}, err
	}
	var res tg.MessagesMessagesClass
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: ids,
		})
	} else {
		res, err = api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, entityMaps{}, err
	}
	mod, ok := res.AsModified()
	if !ok {
		return nil, entityMaps{}, nil
	}
	b.peers.learnLists(mod.GetUsers(), mod.GetChats())
	ents := newEntityMaps(mod.GetUsers(), mod.GetChats())
	for _, m := range mod.GetMessages() {
		if msg, ok := m.(*tg.Message); ok && msg.ID == msgID {
			return msg, ents, nil
		}
	}
	return nil, ents, nil
}

func parseTgref(id string) (chat int64, msg int, ok bool) {
	if !strings.HasPrefix(id, tgrefPrefix) {
		return 0, 0, false
	}
	rest := id[len(tgrefPrefix):]
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return 0, 0, false
	}
	c, err1 := strconv.ParseInt(rest[:i], 10, 64)
	m, err2 := strconv.Atoi(rest[i+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return c, m, true
}

// sentMirror finds the "<chat>-<msg>-sent*" copy of an outgoing attachment.
func (b *Backend) sentMirror(chat int64, msg int) string {
	pattern := filepath.Join(b.mediaDirPath(), fmt.Sprintf("%d-%d-sent*", chat, msg))
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		if isFile(m) {
			return m
		}
	}
	return ""
}

// AttachmentPath implements proto.AttachmentResolver (get_attachment_path).
func (b *Backend) AttachmentPath(ctx context.Context, attachmentID string) (string, bool) {
	if attachmentID == "" {
		return "", false
	}
	if isFile(attachmentID) {
		return attachmentID, true
	}
	if !strings.HasPrefix(attachmentID, tgrefPrefix) {
		// Legacy absolute path from another machine: look the basename up
		// in the media dir (no traversal).
		name := filepath.Base(attachmentID)
		if name != "" && name != "." && name != ".." && !strings.Contains(name, "..") &&
			!strings.Contains(attachmentID, string(filepath.Separator)+".."+string(filepath.Separator)) &&
			!strings.HasSuffix(attachmentID, string(filepath.Separator)+"..") {
			cand := filepath.Join(b.mediaDirPath(), name)
			if isFile(cand) {
				return cand, true
			}
		}
		return "", false
	}
	chat, msg, ok := parseTgref(attachmentID)
	if !ok {
		return "", false
	}
	if m := b.sentMirror(chat, msg); m != "" {
		return m, true
	}
	p, err := b.downloadMediaByRef(ctx, chat, msg)
	if err != nil || p == "" {
		if err != nil {
			b.log.Error("Telegram: lazy media download failed", "err", err)
		}
		return "", false
	}
	return p, true
}

// downloadMediaByRef is _download_media_by_ref (30 s budget).
func (b *Backend) downloadMediaByRef(ctx context.Context, chat int64, msgID int) (string, error) {
	api, err := b.getAPI()
	if err != nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, _, err := b.fetchMessage(ctx, api, chat, msgID)
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", nil
	}
	mi := messageMedia(msg)
	if !mi.any() {
		return "", nil
	}
	name, ext := "", ".jpg"
	if mi.Doc != nil {
		name = readDocAttrs(mi.Doc).Filename
		if e := guessExtension(mi.Doc.MimeType); e != "" {
			ext = e
		}
	}
	if name != "" {
		name = filepath.Base(name)
	} else {
		name = "photo" + ext
	}
	dir, err := b.EnsureMediaDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, fmt.Sprintf("%d-%d-%s", chat, msgID, name))
	if isFile(target) {
		return target, nil
	}
	if err := downloadToPath(ctx, api, mi.location(), target); err != nil {
		return "", err
	}
	return target, nil
}

func readRange(path string, start int64, length int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := start
	if start < 0 {
		off = st.Size() - int64(length)
	}
	if off < 0 {
		off = 0
	}
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, off)
	if err != nil && n == 0 && off < st.Size() {
		return nil, err
	}
	return buf[:n], nil
}

// AttachmentChunk implements proto.AttachmentChunker (get_attachment_chunk):
// a bounded byte range; start < 0 reads the last length bytes. nil data means
// "unavailable".
func (b *Backend) AttachmentChunk(ctx context.Context, attachmentID string, start int64, length int) ([]byte, error) {
	if attachmentID == "" || length <= 0 {
		return nil, nil
	}
	if isFile(attachmentID) {
		data, err := readRange(attachmentID, start, length)
		if err != nil {
			return nil, nil
		}
		return data, nil
	}
	chat, msgID, ok := parseTgref(attachmentID)
	if !ok {
		return nil, nil
	}
	if m := b.sentMirror(chat, msgID); m != "" {
		data, err := readRange(m, start, length)
		if err != nil {
			return nil, nil
		}
		return data, nil
	}
	api, err := b.getAPI()
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, _, err := b.fetchMessage(ctx, api, chat, msgID)
	if err != nil || msg == nil {
		return nil, nil
	}
	mi := messageMedia(msg)
	if mi.Doc == nil || mi.Doc.Size <= 0 {
		return nil, nil
	}
	size := mi.Doc.Size
	offset := int64(0)
	if start < 0 {
		offset = size - int64(length)
		if offset < 0 {
			offset = 0
		}
	} else {
		offset = start
	}
	amount := int64(length)
	if rem := size - offset; amount > rem {
		amount = rem
	}
	if amount <= 0 {
		return []byte{}, nil
	}
	data, err := b.fetchRange(ctx, api, documentLocation(mi.Doc), offset, amount)
	if err != nil {
		b.log.Debug("Telegram: partial media download failed", "err", err)
		return nil, nil
	}
	return data, nil
}

// fetchRange downloads [offset, offset+amount) honouring MTProto's 4 KiB
// alignment of upload.getFile.
func (b *Backend) fetchRange(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, offset, amount int64) ([]byte, error) {
	const align = 4096
	const maxReq = 512 * 1024
	begin := offset - offset%align
	end := offset + amount
	var out []byte
	for pos := begin; pos < end; {
		limit := ((end - pos + align - 1) / align) * align
		if limit > maxReq {
			limit = maxReq
		}
		res, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: loc, Offset: pos, Limit: int(limit), Precise: true})
		if err != nil {
			return nil, err
		}
		f, ok := res.(*tg.UploadFile)
		if !ok {
			return nil, errors.New("telegram: unexpected upload.getFile result")
		}
		out = append(out, f.Bytes...)
		if len(f.Bytes) == 0 || int64(len(f.Bytes)) < limit {
			break
		}
		pos += int64(len(f.Bytes))
	}
	skip := offset - begin
	if skip > int64(len(out)) {
		return []byte{}, nil
	}
	out = out[skip:]
	if int64(len(out)) > amount {
		out = out[:amount]
	}
	return out, nil
}

// sniffImageContentType is media_utils.sniff_image_content_type.
func sniffImageContentType(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return "image/jpeg"
}

// ProfilePhoto implements media.ProfilePhotoProvider (get_profile_photo_bytes):
// the big profile photo of a user/group/channel. Best effort: any failure
// yields no picture.
func (b *Backend) ProfilePhoto(ctx context.Context, contactID string) ([]byte, string, error) {
	eid, ok := toInt(contactID)
	if !ok {
		return nil, "", nil
	}
	api, err := b.getAPI()
	if err != nil {
		return nil, "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := b.profilePhotoBytes(ctx, api, eid)
	if err != nil {
		b.log.Debug("Telegram: profile photo unavailable", "id", eid, "err", err)
		return nil, "", nil
	}
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return nil, "", nil
	}
	return data, sniffImageContentType(data), nil
}

func (b *Backend) profilePhotoBytes(ctx context.Context, api *tg.Client, eid int64) ([]byte, error) {
	peer, err := b.resolveInputPeer(eid)
	if err != nil {
		return nil, err
	}
	kind, raw := splitMarked(eid)
	var photoID int64
	switch kind {
	case PeerUser:
		var iu tg.InputUserClass = &tg.InputUserSelf{}
		if p, ok := peer.(*tg.InputPeerUser); ok {
			iu = &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}
		}
		users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{iu})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if uu, ok := u.(*tg.User); ok && uu.ID == raw {
				if ph, ok := uu.Photo.(*tg.UserProfilePhoto); ok {
					photoID = ph.PhotoID
				}
			}
		}
	case PeerChat:
		res, err := api.MessagesGetChats(ctx, []int64{raw})
		if err != nil {
			return nil, err
		}
		for _, c := range res.GetChats() {
			if cc, ok := c.(*tg.Chat); ok {
				if ph, ok := cc.Photo.(*tg.ChatPhoto); ok {
					photoID = ph.PhotoID
				}
			}
		}
	case PeerChannel:
		ip, ok := peer.(*tg.InputPeerChannel)
		if !ok {
			return nil, errors.New("telegram: no channel peer")
		}
		res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: ip.ChannelID, AccessHash: ip.AccessHash}})
		if err != nil {
			return nil, err
		}
		for _, c := range res.GetChats() {
			if cc, ok := c.(*tg.Channel); ok {
				if ph, ok := cc.Photo.(*tg.ChatPhoto); ok {
					photoID = ph.PhotoID
				}
			}
		}
	}
	if photoID == 0 {
		return nil, nil
	}
	loc := &tg.InputPeerPhotoFileLocation{Big: true, Peer: peer, PhotoID: photoID}
	var buf bytes.Buffer
	if _, err := downloader.NewDownloader().Download(api, loc).Stream(ctx, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
