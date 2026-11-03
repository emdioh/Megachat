package config

import (
	"path/filepath"
	"strings"
)

// Environment variable names for paths.
const (
	SocketEnv = "MEGACHAT_SOCKET"
	LockEnv   = "MEGACHAT_LOCK_FILE"
)

// DefaultLockFile and DefaultLogFile are fixed paths in the Python server.
const (
	DefaultLockFile = "/tmp/signal-tui.lock"
	DefaultLogFile  = "/tmp/signal-tui.log"
)

// SocketPath is where the server listens (server/paths.py): $MEGACHAT_SOCKET
// (with ~ expansion), else $XDG_RUNTIME_DIR/megachat/server.sock when that is
// absolute, else ~/.local/state/megachat/server.sock.
func (c *Config) SocketPath() string {
	if v := c.Env(SocketEnv); v != "" {
		return expandHome(v, c.Home)
	}
	if rt := c.Env("XDG_RUNTIME_DIR"); rt != "" && filepath.IsAbs(rt) {
		return filepath.Join(rt, "megachat", "server.sock")
	}
	return filepath.Join(c.Home, ".local", "state", "megachat", "server.sock")
}

// RuntimeFilePath is the runtime file next to a socket (server.sock -> server.json).
func RuntimeFilePath(socket string) string {
	return strings.TrimSuffix(socket, filepath.Ext(socket)) + ".json"
}

// LockFile is $MEGACHAT_LOCK_FILE or /tmp/signal-tui.lock.
func (c *Config) LockFile() string {
	if v := c.Env(LockEnv); v != "" {
		return v
	}
	return DefaultLockFile
}

// DataDir is the persistent data/cache root (Python CACHE_DIR):
// ~/.local/share/signal-tui-client. It deliberately ignores XDG_DATA_HOME,
// exactly like protocols/db.py.
func (c *Config) DataDir() string {
	return filepath.Join(c.Home, ".local", "share", "signal-tui-client")
}

// CacheDir is an alias of DataDir (Python calls it CACHE_DIR).
func (c *Config) CacheDir() string { return c.DataDir() }

// DBFile is the SQLite message store, <DataDir>/messages.db.
func (c *Config) DBFile() string { return filepath.Join(c.DataDir(), "messages.db") }

// LegacyCacheFile is <DataDir>/messages.json (pre-SQLite cache).
func (c *Config) LegacyCacheFile() string { return filepath.Join(c.DataDir(), "messages.json") }

// MediaDir is the WhatsApp media directory: WHATSAPP_MEDIA_DIR /
// whatsapp_media_dir, else <DataDir>/whatsapp-media.
func (c *Config) MediaDir() string {
	if d := c.WhatsAppMediaDirConfigured(); d != "" {
		return d
	}
	return filepath.Join(c.DataDir(), "whatsapp-media")
}

// TelegramMediaDir is <DataDir>/telegram-media.
func (c *Config) TelegramMediaDir() string { return filepath.Join(c.DataDir(), "telegram-media") }

// DownloadsDir is <DataDir>/downloads (temporary download area).
func (c *Config) DownloadsDir() string { return filepath.Join(c.DataDir(), "downloads") }

// QuoteThumbsDir is <DataDir>/quote-thumbs.
func (c *Config) QuoteThumbsDir() string { return filepath.Join(c.DataDir(), "quote-thumbs") }

// LocationThumbsDir is <DataDir>/location-thumbs.
func (c *Config) LocationThumbsDir() string { return filepath.Join(c.DataDir(), "location-thumbs") }

// LinkPreviewImagesDir is <DataDir>/link-preview-images.
func (c *Config) LinkPreviewImagesDir() string {
	return filepath.Join(c.DataDir(), "link-preview-images")
}

// AvatarDir is <DataDir>/<name> ("whatsapp-avatars", ...).
func (c *Config) AvatarDir(name string) string { return filepath.Join(c.DataDir(), name) }

// AvatarBlobsDir is <DataDir>/avatar-blobs/<protocol>.
func (c *Config) AvatarBlobsDir(protocol string) string {
	return filepath.Join(c.DataDir(), "avatar-blobs", protocol)
}

// WALidMapFile is <DataDir>/wa_lid_map.json.
func (c *Config) WALidMapFile() string { return filepath.Join(c.DataDir(), "wa_lid_map.json") }

// TelegramSessionPath is $XDG_DATA_HOME (default ~/.local/share) /
// signal-tui-client/telegram.session. Note it honours XDG_DATA_HOME, unlike
// DataDir.
func (c *Config) TelegramSessionPath() string {
	base := c.Env("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(c.Home, ".local", "share")
	}
	return filepath.Join(base, "signal-tui-client", "telegram.session")
}

// WebThumbDir is $XDG_CACHE_HOME (default ~/.cache) /
// signal-tui-client/web-thumbs/<protocol>.
func (c *Config) WebThumbDir(protocol string) string {
	base := c.Env("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(c.Home, ".cache")
	} else {
		base = expandHome(base, c.Home)
	}
	return filepath.Join(base, "signal-tui-client", "web-thumbs", protocol)
}
