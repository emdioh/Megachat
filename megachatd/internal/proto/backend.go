package proto

import (
	"context"
	"errors"
)

// Errors returned by the Manager.
var (
	// ErrNoBackend: no backend registered for the protocol (Python KeyError).
	ErrNoBackend = errors.New("proto: no backend registered for protocol")
	// ErrUnsupported: the backend does not implement the capability
	// (Python base-class defaults returning False/None).
	ErrUnsupported = errors.New("proto: operation not supported by backend")
)

// Backend is the core contract every protocol backend must implement
// (ChatBackend's abstract methods plus is_connected). Everything else is an
// optional capability interface below, discovered with a type assertion.
//
// All methods must be safe for concurrent use. Blocking operations take a
// context and must return promptly when it is cancelled; the Python *_sync
// variants (worker-thread only) and their async twins are merged into one
// context-aware method.
type Backend interface {
	// Protocol is the stable identifier (ProtocolSignal, ...). Non-empty.
	Protocol() string

	// Connect starts the backend (daemon, websocket, ...) and loads initial
	// data. Disconnect stops it and releases resources.
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error

	// IsConnected reports whether the backend has an active session now.
	IsConnected() bool

	// ListContacts returns the known (active-chat) contacts.
	ListContacts(ctx context.Context) ([]Contact, error)

	// SendMessage sends text and returns the message id/timestamp.
	SendMessage(ctx context.Context, contactID, text string, quote Quote) (string, error)

	// MarkRead marks all messages of the contact as read.
	MarkRead(ctx context.Context, contactID string) error

	// Receive streams normalised events until ctx is cancelled or the
	// backend disconnects; the channel is then closed.
	Receive(ctx context.Context) (<-chan Event, error)
}

// --- optional capabilities ---------------------------------------------------
//
// Support matrix in the Python code (S=signal, W=whatsapp, T=telegram):
//   AttachmentSender, AttachmentResolver, SentMirror   S W T
//   Editor, Reactor                                    S W T
//   AttachmentChunker                                  W T
//   Pairer                                             W T (Signal links via server/link.py)
//   TwoFactor, ReactionLister, HistoryFetcher          T (HistoryFetcher also W)
//   Archiver, HistoryResyncer, PresenceSubscriber      W
//   AddressBook / ContactRefresher / ContactFinder     W T (Signal uses base defaults)

// AttachmentSender sends one file (send_attachment_sync).
type AttachmentSender interface {
	SendAttachment(ctx context.Context, contactID string, att Attachment, quote Quote) (string, error)
}

// BatchAttachmentSender has a native multi-file send (Signal attachment
// lists, Telegram albums). Without it the Manager loops SendAttachment,
// applying the quote to the first file only.
type BatchAttachmentSender interface {
	SendAttachments(ctx context.Context, contactID string, atts []Attachment, batchID string, quote Quote) ([]string, error)
}

// AttachmentResolver maps an attachment id to a local file path.
type AttachmentResolver interface {
	AttachmentPath(ctx context.Context, attachmentID string) (path string, ok bool)
}

// AttachmentChunker fetches a bounded byte range of an attachment
// (get_attachment_chunk). start < 0 means "unspecified".
type AttachmentChunker interface {
	AttachmentChunk(ctx context.Context, attachmentID string, start int64, length int) ([]byte, error)
}

// SentMirror publishes a successful facade send through the normal receive
// queue (enqueue_sent_message / enqueue_sent_notification).
type SentMirror interface {
	EnqueueSentMessage(ctx context.Context, m SentMessage) error
	// EnqueueSentNotification is the lightweight "sent-mirror" event used by
	// Signal after a batch send already mirrored its own rows.
	EnqueueSentNotification(ctx context.Context, contactID, messageID string, timestamp int64, batchID string) error
}

// Editor edits own messages. messageID semantics: Signal = original
// timestamp (ms) as string, Telegram = server id, WhatsApp = Baileys id.
type Editor interface {
	// EditMessage returns true if the backend accepted the edit.
	EditMessage(ctx context.Context, contactID, messageID, newText string) (bool, error)
	// ApplyEdit applies a received/echoed edit to cache+store. Returns nil
	// when nothing changed (idempotent).
	ApplyEdit(contactID, messageID, newText string, opts ApplyEditOptions) (*EditResult, error)
}

// Reactor sends and applies reactions.
type Reactor interface {
	SendReaction(ctx context.Context, contactID, messageID, emoji, targetAuthor string) (bool, error)
	// ApplyReaction applies a received reaction/snapshot payload and returns
	// the update to push, or nil for unknown target / no-op.
	ApplyReaction(contactID string, payload map[string]any) (map[string]any, error)
}

// ReactionLister lists the reactions the account may use (Telegram).
type ReactionLister interface {
	AvailableReactions(ctx context.Context) ([]string, error)
}

// AddressBook returns the complete address book (not only active chats).
// Backends without it fall back to ListContacts in the Manager.
type AddressBook interface {
	ListAddressBook(ctx context.Context, force bool) ([]Contact, error)
}

// ContactRefresher re-fetches contacts and merges names in place. It never
// returns an error for remote failures: those go in result.Errors with no
// mutation committed.
type ContactRefresher interface {
	RefreshContacts(ctx context.Context, force bool) AddressBookRefreshResult
}

// ContactFinder looks a contact up in memory only (zero network).
type ContactFinder interface {
	FindContact(contactID string) (Contact, bool)
}

// Pairer is implemented by backends needing interactive QR/device pairing.
type Pairer interface {
	NeedsPairing() bool
	// PairingQR returns the current QR link/data, "" when not pairing.
	PairingQR(ctx context.Context) (string, error)
}

// TwoFactor completes a 2FA login (Telegram).
type TwoFactor interface {
	Complete2FA(ctx context.Context, password string) (bool, error)
}

// Archiver archives/unarchives a chat (WhatsApp).
type Archiver interface {
	SetChatArchived(ctx context.Context, contactID string, archived bool) (bool, error)
}

// HistoryFetcher fetches recent history for a chat from the remote.
type HistoryFetcher interface {
	FetchHistory(ctx context.Context, contactID string, limit int) ([]Message, error)
}

// HistoryResyncer bulk-resyncs history (WhatsApp resync_history). limit <= 0
// means the backend default. Returns the number of chats synced.
type HistoryResyncer interface {
	ResyncHistory(ctx context.Context, limit int) (int, error)
}

// PresenceSubscriber starts presence/typing subscriptions (WhatsApp
// start_presence_subscribe).
type PresenceSubscriber interface {
	StartPresenceSubscribe(ctx context.Context) error
}

// TypingSender sends an outgoing typing indicator. NOTE: the Python code has
// no outbound typing (typing is receive-only); declared for the Go server's
// future use and implemented by the fake only.
type TypingSender interface {
	SendTyping(ctx context.Context, contactID string, started bool) error
}
