package signal

import (
	"context"

	"github.com/emdioh/megachat/megachatd/internal/linking"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Compile-time capability checks.
var (
	_ proto.Backend               = (*Backend)(nil)
	_ proto.AttachmentSender      = (*Backend)(nil)
	_ proto.BatchAttachmentSender = (*Backend)(nil)
	_ proto.AttachmentResolver    = (*Backend)(nil)
	_ proto.SentMirror            = (*Backend)(nil)
	_ proto.Editor                = (*Backend)(nil)
	_ proto.Reactor               = (*Backend)(nil)
	_ proto.AddressBook           = (*Backend)(nil)
	_ proto.ContactRefresher      = (*Backend)(nil)
	_ proto.ContactFinder         = (*Backend)(nil)
	_ linking.SignalAccount       = (*Backend)(nil)
	_ media.ProfilePhotoProvider  = (*Backend)(nil)

	// Shapes of core's optional hooks (core.MessageIngester, ReceiptProcessor,
	// ContactRegistrar, UserNumber); core.IngestResult must alias
	// proto.IngestResult.
	_ interface {
		IngestMessage(ctx context.Context, contactID string, msg map[string]any, ts int64) (proto.IngestResult, error)
	} = (*Backend)(nil)
	_ interface {
		ProcessReceipt(ctx context.Context, contactID string, payload map[string]any) ([]map[string]any, error)
	} = (*Backend)(nil)
	_ interface{ RegisterContact(c proto.Contact) bool } = (*Backend)(nil)
	_ interface{ UserNumber() string }                   = (*Backend)(nil)
)
