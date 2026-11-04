package telegram

import (
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/linking"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/proto"
)

// Compile-time interface checks.
var (
	_ proto.Backend               = (*Backend)(nil)
	_ proto.AttachmentSender      = (*Backend)(nil)
	_ proto.BatchAttachmentSender = (*Backend)(nil)
	_ proto.AttachmentResolver    = (*Backend)(nil)
	_ proto.AttachmentChunker     = (*Backend)(nil)
	_ proto.SentMirror            = (*Backend)(nil)
	_ proto.Editor                = (*Backend)(nil)
	_ proto.Reactor               = (*Backend)(nil)
	_ proto.ReactionLister        = (*Backend)(nil)
	_ proto.AddressBook           = (*Backend)(nil)
	_ proto.ContactRefresher      = (*Backend)(nil)
	_ proto.ContactFinder         = (*Backend)(nil)
	_ proto.Pairer                = (*Backend)(nil)
	_ proto.TwoFactor             = (*Backend)(nil)
	_ proto.Archiver              = (*Backend)(nil)
	_ proto.HistoryFetcher        = (*Backend)(nil)
	_ proto.HistoryResyncer       = (*Backend)(nil)

	_ core.MessageIngester  = (*Backend)(nil)
	_ core.ReceiptProcessor = (*Backend)(nil)
	_ core.ContactRegistrar = (*Backend)(nil)
	_ core.PlaceholderNamer = (*Backend)(nil)

	_ media.ProfilePhotoProvider = (*Backend)(nil)
	_ media.MediaDirProvider     = (*Backend)(nil)

	_ linking.TelegramLogin = (*Backend)(nil)
)
