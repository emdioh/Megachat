package main

import (
	"context"
	"errors"

	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/gmail"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/proto/whatsapp"
	"github.com/emdioh/megachat/megachatd/internal/store"
	"github.com/emdioh/megachat/megachatd/internal/transcribe"
)

// uploadValidator adapts media.ValidateUpload (the full Python policy) to
// core.UploadValidator.
func uploadValidator(path, filename, contentType string) (core.Upload, error) {
	v, err := media.ValidateUpload(path, filename, contentType)
	if err != nil {
		var ue *media.UploadValidationError
		if errors.As(err, &ue) {
			return core.Upload{}, &core.UploadError{Status: ue.Status}
		}
		return core.Upload{}, err
	}
	return core.Upload{Path: v.Path, Filename: v.Filename, MimeType: v.MimeType, MediaKind: v.MediaKind}, nil
}

// uploadGuard adapts media.UploadGuard to web.UploadGuard.
type uploadGuard struct{}

func (uploadGuard) NewGuard(filename, contentType string, maxBytes int64, mediaLimits bool) func([]byte) error {
	g := &media.UploadGuard{DeclaredType: contentType, Filename: filename, NoMediaLimits: !mediaLimits}
	zeroCap := maxBytes == 0
	if maxBytes > 0 {
		g.MaxBytes = maxBytes
	}
	return func(chunk []byte) error {
		if zeroCap && len(chunk) > 0 {
			return &media.UploadValidationError{Status: 413}
		}
		return g.Feed(chunk)
	}
}

// linkBackends is linking.Backends: the manager, except that WhatsApp is seen
// through its WAHA-shaped linker view (the PairingQR signatures differ).
type linkBackends struct {
	mgr *proto.Manager
	wa  *whatsapp.Backend
}

func (l linkBackends) Get(protocol string) (proto.Backend, bool) {
	b, ok := l.mgr.Get(protocol)
	if !ok {
		return nil, false
	}
	if wb, isWA := b.(*whatsapp.Backend); isWA && wb == l.wa && l.wa != nil {
		return l.wa.AsLinker(), true
	}
	return b, true
}

// quotedFinder is transcribe.QuotedFinder over store.FindQuotedMessage, with
// the chat's storage keys of Python's _storage_keys.
type quotedFinder struct {
	core *core.Core
	st   *store.Store
}

func (q quotedFinder) FindQuoted(ctx context.Context, protocol, contactID, replyToMessageID string, quoteTimestamp int64) (*transcribe.QuotedMessage, error) {
	keys := []string{contactID}
	if c, ok := q.core.FindContact(ctx, protocol, contactID); ok {
		keys = nil
		for _, k := range core.ContactStorageKeys(c) {
			if k != "" {
				keys = append(keys, k)
			}
		}
		found := false
		for _, k := range keys {
			found = found || k == contactID
		}
		if !found {
			keys = append(keys, contactID)
		}
	}
	row, err := q.st.FindQuotedMessage(ctx, protocol, keys, replyToMessageID, quoteTimestamp)
	if err != nil || row == nil {
		return nil, err
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return &transcribe.QuotedMessage{MsgID: str(row.MsgID), Timestamp: row.Timestamp,
		MediaKind: str(row.MediaKind), AttachmentID: str(row.AttachmentID), IsMine: row.IsMine}, nil
}

// unreadWatcher shares the core's gmail_unread watcher with the gmail
// operations (gmail.UnreadWatcher), so a single poll loop runs.
type unreadWatcher struct{ w *core.GmailUnreadWatcher }

func (u unreadWatcher) Poke() { u.w.Poke() }
func (u unreadWatcher) Stop() {} // the core owns the loop
func (u unreadWatcher) Latest() (gmail.UnreadStatus, bool) {
	s, ok := u.w.Snapshot()
	return gmail.UnreadStatus{Unread: s.Unread, UIDNext: s.UIDNext}, ok
}
