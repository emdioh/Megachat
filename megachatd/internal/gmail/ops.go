package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
)

var errImageNotFound = &notFoundError{Msg: "Immagine non trovata"}

const commError = "Errore di comunicazione con Gmail"

// mapErr is ops/gmail.py::_run's error mapping.
func (s *Service) mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var oe *ops.Error
	var v *validationError
	var nf *notFoundError
	var up *upstreamError
	var nc notConfiguredError
	var se *imapStatusError
	var ne net.Error
	switch {
	case errors.As(err, &oe):
		return err
	case errors.As(err, &nc):
		return ops.Errorf(api.CodeBackendDown, "Gmail non configurato")
	case errors.As(err, &v):
		code := api.CodeInvalidRequest
		if v.Status == 413 {
			code = api.CodeTooLarge
		}
		return ops.Errorf(code, "%s", v.Msg)
	case errors.As(err, &nf):
		return ops.Errorf(api.CodeNotFound, "Risorsa non trovata")
	case errors.As(err, &up), errors.As(err, &se), errors.As(err, &ne):
		s.log.Warn("Gmail upstream error", "op", op)
		return ops.Errorf(api.CodeBackendError, commError)
	}
	s.log.Warn("Gmail request failed", "op", op)
	return ops.Errorf(api.CodeBackendError, "Errore Gmail")
}

func (s *Service) poke() {
	if s.watcher != nil {
		s.watcher.Poke()
	}
}

// RegisterOps registers the gmail.* operations.
func RegisterOps(reg *ops.Registry, deps Deps) *Service {
	svc := deps.Service
	if svc == nil {
		svc = NewService(deps.Config, deps)
	}
	if svc.watcher == nil {
		svc.watcher = NewWatcher(svc, deps.Bus, durationS(deps.Config.GmailUnreadPollS()))
	}
	add := func(name, doc string, h ops.Handler) { reg.Register(ops.Op{Name: name, Doc: doc, Handler: h}) }

	add(api.OpGmailStatus, "Configuration status and limits; contacts no server.",
		ops.Typed(func(ctx context.Context, _ api.EmptyRequest) (api.GmailStatusOut, error) {
			configured, addr, folder := svc.StatusSnapshot()
			return api.GmailStatusOut{Configured: configured, Address: addr, Folder: folder,
				Limits: api.GmailLimits{MaxTextLength: maxTextLength, MaxAttachments: maxAttachments,
					MaxAttachmentBytes: maxAttachmentBytes, MaxTotalAttachmentBytes: maxTotalAttachmentBytes,
					MaxDownloadBytes: maxDownloadBytes, MaxMessagesLimit: maxLimit}}, nil
		}))

	add(api.OpGmailMessagesList, "One window of message summaries.",
		ops.Typed(func(ctx context.Context, r api.GmailMessagesListRequest) (api.GmailListOut, error) {
			out, err := svc.FetchMessageList(ctx, r.Folder, r.Offset, r.Limit, r.Refresh)
			return out, svc.mapErr("fetch_message_list", err)
		}))

	add(api.OpGmailMessagesGet, "The full message with sanitized HTML.",
		ops.Typed(func(ctx context.Context, r api.GmailMessageRef) (api.GmailMessageDetail, error) {
			d, err := svc.FetchMessage(ctx, r.UID, r.Folder)
			if err != nil {
				return d, svc.mapErr("fetch_message", err)
			}
			svc.scheduleRemotePrefetch(d.HTML)
			return d, nil
		}))

	add(api.OpGmailMessagesDelete, "Move one message to Trash.",
		ops.Typed(func(ctx context.Context, r api.GmailMessageRef) (api.GmailDeleteOut, error) {
			if err := svc.DeleteMessage(ctx, r.UID, r.Folder); err != nil {
				return api.GmailDeleteOut{}, svc.mapErr("delete_message", err)
			}
			svc.poke()
			return api.GmailDeleteOut{OK: true, Trashed: true}, nil
		}))

	add(api.OpGmailMessagesBulkDelete, "Move several messages to Trash.",
		ops.Typed(func(ctx context.Context, r api.GmailMessagesBulkDeleteRequest) (api.GmailBulkDeleteOut, error) {
			var folder *string
			if !api.RawIsNull(r.Folder) {
				f, ok := api.RawString(r.Folder)
				if !ok {
					return api.GmailBulkDeleteOut{}, ops.Errorf(api.CodeInvalidRequest, "Cartella non valida")
				}
				folder = &f
			}
			uids, err := validateUIDs(r.Uids)
			if err != nil {
				return api.GmailBulkDeleteOut{}, svc.mapErr("delete_messages", err)
			}
			res, err := svc.DeleteMessages(ctx, uids, folder)
			if err != nil {
				return api.GmailBulkDeleteOut{}, svc.mapErr("delete_messages", err)
			}
			svc.poke()
			return api.GmailBulkDeleteOut{OK: true, Trashed: res.Trashed, Missing: res.Missing}, nil
		}))

	flag := func(name, doc string, f func(context.Context, int, *string) error) {
		add(name, doc, ops.Typed(func(ctx context.Context, r api.GmailMessageRef) (api.OkOut, error) {
			if err := f(ctx, r.UID, r.Folder); err != nil {
				return api.OkOut{}, svc.mapErr(name, err)
			}
			svc.poke()
			return api.OkOut{OK: true}, nil
		}))
	}
	flag(api.OpGmailMessagesMarkRead, "Set the \\Seen flag.", svc.MarkRead)
	flag(api.OpGmailMessagesMarkUnread, "Clear the \\Seen flag.", svc.MarkUnread)
	flag(api.OpGmailMessagesArchive, "Archive an inbox message.", svc.Archive)

	add(api.OpGmailAddressBook, "Addresses for the compose autocomplete.",
		ops.Typed(func(ctx context.Context, _ api.EmptyRequest) (api.GmailAddressBookOut, error) {
			c, err := svc.FetchAddressBook(ctx, false)
			return api.GmailAddressBookOut{Contacts: c}, svc.mapErr("fetch_address_book", err)
		}))

	add(api.OpGmailUnread, "Unread count among the listed messages.",
		ops.Typed(func(ctx context.Context, _ api.EmptyRequest) (api.GmailUnreadOut, error) {
			if configured, _, _ := svc.StatusSnapshot(); !configured {
				return api.GmailUnreadOut{}, nil
			}
			if l, ok := svc.watcher.Latest(); ok {
				return api.GmailUnreadOut{Configured: true, Unread: l.Unread}, nil
			}
			st, err := svc.FetchUnreadStatus(ctx, nil)
			if err != nil {
				return api.GmailUnreadOut{}, svc.mapErr("fetch_unread_status", err)
			}
			return api.GmailUnreadOut{Configured: true, Unread: st.Unread}, nil
		}))

	add(api.OpGmailMessagesSendCheck, "Validate a send before its attachments are stored.",
		ops.Typed(func(ctx context.Context, r api.GmailMessagesSendCheckRequest) (api.OkOut, error) {
			if err := checkSlots(r.AttachmentCount, r.SlotsOK); err != nil {
				return api.OkOut{}, err
			}
			return api.OkOut{OK: true}, nil
		}))

	add(api.OpGmailMessagesSend, "Send a mail; returns its Message-ID.",
		ops.Typed(func(ctx context.Context, r api.GmailMessagesSendRequest) (api.GmailSendOut, error) {
			ok := true
			for _, a := range r.Attachments {
				if a.Path == "" && a.Rejected == nil {
					ok = false
				}
			}
			if err := checkSlots(len(r.Attachments), ok); err != nil {
				return api.GmailSendOut{}, err
			}
			files, err := readAttachments(r.Attachments)
			if err != nil {
				return api.GmailSendOut{}, err
			}
			p := SendParams{To: r.To, Cc: r.Cc, Bcc: r.Bcc, Subject: r.Subject, Text: r.Text,
				ReplyToUID: r.ReplyToUID, ReplyToFolder: r.ReplyToFolder,
				ForwardUID: r.ForwardUID, ForwardFolder: r.ForwardFolder, Attachments: files}
			id, err := svc.SendEmail(ctx, p)
			if err != nil {
				return api.GmailSendOut{}, svc.mapErr("send_email", err)
			}
			return api.GmailSendOut{OK: true, MessageID: id}, nil
		}))

	add(api.OpGmailAttachment, "One attachment as a temporary blob.",
		ops.Typed(func(ctx context.Context, r api.GmailAttachmentRequest) (api.GmailAttachmentOut, error) {
			dl, err := svc.FetchAttachment(ctx, r.UID, r.Index, r.Folder)
			if err != nil {
				return api.GmailAttachmentOut{}, svc.mapErr("fetch_attachment", err)
			}
			path, err := svc.writeBlob(dl.Data)
			if err != nil {
				return api.GmailAttachmentOut{}, svc.mapErr("write_blob", err)
			}
			return api.GmailAttachmentOut{BlobOut: blobFor(path, dl.ContentType), Filename: dl.Filename}, nil
		}))

	add(api.OpGmailImage, "An email image through the HMAC-signed proxy token.",
		ops.Typed(func(ctx context.Context, r api.GmailImageRequest) (api.BlobOut, error) {
			u := svc.verifyImageToken(r.Token)
			if u == "" {
				return api.BlobOut{}, ops.Errorf(api.CodeNotFound, "Immagine non trovata")
			}
			var data []byte
			var ct string
			if strings.HasPrefix(u, partImagePrefix) {
				var err error
				data, ct, err = svc.inlinePartImage(ctx, u)
				if err != nil {
					if errors.Is(err, errImageNotFound) {
						return api.BlobOut{}, ops.Errorf(api.CodeNotFound, "Immagine non trovata")
					}
					return api.BlobOut{}, svc.mapErr("fetch_attachment", err)
				}
			} else {
				data, ct, _ = svc.remoteImage(ctx, u)
			}
			if len(data) == 0 {
				return api.BlobOut{}, ops.Errorf(api.CodeBackendError, "Immagine non disponibile")
			}
			path, err := svc.writeBlob(data)
			if err != nil {
				return api.BlobOut{}, ops.Errorf(api.CodeBackendError, "Immagine non disponibile")
			}
			return blobFor(path, ct), nil
		}))
	return svc
}

func durationS(f float64) (d durationT) { return durationT(f * 1e9) }

func checkSlots(count int, slotsOK bool) error {
	if count > maxAttachments {
		return ops.Errorf(api.CodeInvalidRequest, "Troppi allegati")
	}
	if !slotsOK {
		return ops.Errorf(api.CodeInvalidRequest, "Richiesta non valida")
	}
	return nil
}

func readAttachments(atts []api.AttachmentIn) ([]SendFile, error) {
	total := int64(0)
	sizes := make([]int64, len(atts))
	for i, a := range atts {
		if a.Rejected != nil {
			return nil, ops.Errorf(api.CodeTooLarge, "Allegato troppo grande")
		}
		st, err := os.Stat(a.Path)
		if err != nil {
			return nil, ops.Errorf(api.CodeInvalidRequest, "Richiesta non valida")
		}
		if st.Size() > maxAttachmentBytes {
			return nil, ops.Errorf(api.CodeTooLarge, "Allegato troppo grande")
		}
		total += st.Size()
		if total > maxTotalAttachmentBytes {
			return nil, ops.Errorf(api.CodeTooLarge, "Allegati troppo grandi")
		}
		sizes[i] = st.Size()
	}
	files := make([]SendFile, 0, len(atts))
	for i, a := range atts {
		data, err := os.ReadFile(a.Path)
		if err != nil {
			return nil, ops.Errorf(api.CodeInvalidRequest, "Richiesta non valida")
		}
		if int64(len(data)) > sizes[i] {
			return nil, ops.Errorf(api.CodeTooLarge, "Allegato troppo grande")
		}
		name := "attachment"
		if a.Filename != nil && *a.Filename != "" {
			name = *a.Filename
		}
		files = append(files, SendFile{Filename: name, Data: data})
	}
	return files, nil
}

// validateUIDs validates a bulk UID list (list of 1..200 ints, no bools).
func validateUIDs(rawv json.RawMessage) ([]int64, error) {
	var items []json.RawMessage
	if api.RawIsNull(rawv) || json.Unmarshal(rawv, &items) != nil {
		return nil, validation("Lista UID non valida")
	}
	if len(items) == 0 {
		return nil, validation("Lista UID vuota")
	}
	if len(items) > maxBulkDelete {
		return nil, validation("Troppi UID (max 200)")
	}
	seen := map[int64]bool{}
	var out []int64
	for _, it := range items {
		if len(it) > 0 && it[0] == '"' {
			return nil, validation("UID non valido")
		}
		var n json.Number
		d := json.NewDecoder(strings.NewReader(string(it)))
		d.UseNumber()
		if err := d.Decode(&n); err != nil || strings.ContainsAny(n.String(), ".eE") {
			return nil, validation("UID non valido")
		}
		v, err := n.Int64()
		if err != nil || v <= 0 {
			return nil, validation("uid non valido")
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
