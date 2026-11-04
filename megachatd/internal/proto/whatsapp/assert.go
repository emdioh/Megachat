package whatsapp

import (
	"github.com/emdioh/megachat/megachatd/internal/config"
	"github.com/emdioh/megachat/megachatd/internal/core"
	"github.com/emdioh/megachat/megachatd/internal/linking"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/messages"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Compile-time capability checks.
var (
	_ proto.Backend               = (*Backend)(nil)
	_ proto.AttachmentSender      = (*Backend)(nil)
	_ proto.BatchAttachmentSender = (*Backend)(nil)
	_ proto.AttachmentResolver    = (*Backend)(nil)
	_ proto.AttachmentChunker     = (*Backend)(nil)
	_ proto.SentMirror            = (*Backend)(nil)
	_ proto.Editor                = (*Backend)(nil)
	_ proto.Reactor               = (*Backend)(nil)
	_ proto.AddressBook           = (*Backend)(nil)
	_ proto.ContactRefresher      = (*Backend)(nil)
	_ proto.ContactFinder         = (*Backend)(nil)
	_ proto.Pairer                = (*Backend)(nil)
	_ proto.Archiver              = (*Backend)(nil)
	_ proto.HistoryFetcher        = (*Backend)(nil)
	_ proto.HistoryResyncer       = (*Backend)(nil)
	_ proto.PresenceSubscriber    = (*Backend)(nil)
	_ proto.TypingSender          = (*Backend)(nil)

	// core's optional hooks.
	_ core.MessageIngester    = (*Backend)(nil)
	_ core.ReceiptProcessor   = (*Backend)(nil)
	_ core.ContactRegistrar   = (*Backend)(nil)
	_ core.UnreadReconciler   = (*Backend)(nil)
	_ core.LIDResolverStarter = (*Backend)(nil)
	_ core.NumberChecker      = (*Backend)(nil)
	_ core.PlaceholderNamer   = (*Backend)(nil)

	// media and messages capabilities.
	_ media.MediaDirProvider          = (*Backend)(nil)
	_ media.ProfilePictureURLProvider = (*Backend)(nil)
	_ messages.JIDPhoneResolver       = (*Backend)(nil)

	// linking: the WAHA-style QR flow, through the LinkerBackend view.
	_ linking.WhatsAppLinker = (*LinkerBackend)(nil)
	_ proto.Backend          = (*LinkerBackend)(nil)

	// The real config and store satisfy the backend's interfaces.
	_ Config        = (*config.Config)(nil)
	_ GuardSettings = (*config.Config)(nil)
	_ Store         = (*store.Store)(nil)
	_ SessionSource = (*Backend)(nil)
)
