package telegram

import (
	"testing"

	_ "github.com/gotd/td/session"
	_ "github.com/gotd/td/telegram"
	_ "github.com/gotd/td/telegram/auth"
	_ "github.com/gotd/td/telegram/auth/qrlogin"
	_ "github.com/gotd/td/telegram/downloader"
	_ "github.com/gotd/td/telegram/message"
	_ "github.com/gotd/td/telegram/uploader"
	_ "github.com/gotd/td/telegram/updates"
	_ "github.com/gotd/td/tg"
	_ "github.com/gotd/td/tgmock"
	_ "modernc.org/sqlite"
)

func TestScratch(t *testing.T) {}
