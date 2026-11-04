# megachatd

Go port of the Megachat headless server (`megachat serve`). The Python TUI and the web SPA are
unchanged clients: the wire contract is in `CONTRACTS.md`, the Go twin in `internal/api`.

## Build

```bash
make megachatd                            # from the repo root: -> megachatd/bin/megachatd (git-ignored)
# = cd megachatd && go build -o bin/megachatd ./cmd/megachatd     (Go 1.26, see go.mod)
cd megachatd && go vet ./... && go test -race ./...
make contract-test                        # 583 black-box tests against the real binary
```

`./install.sh` runs the build when Go is available (and otherwise falls back to the Python server,
see below).

### Alpine / musl

megachatd has no cgo dependencies (SQLite is `modernc.org/sqlite`, WhatsApp is whatsmeow, Telegram is
gotd), so a `CGO_ENABLED=0` build is a fully static binary that runs on Alpine unchanged:

```bash
make megachatd-static                     # -> megachatd/bin/megachatd-linux-<arch>
make megachatd-static GOARCH=arm64        # cross-compile, e.g. for a Raspberry Pi on Alpine
```

Or build on Alpine itself: `apk add go make && make megachatd-static` (Go is fetched per `go.mod`'s
toolchain line if the packaged one is older). Point the launcher at the result with
`MEGACHATD_BIN=/path/to/megachatd-linux-amd64`.

Runtime tools are separate packages on Alpine:

- `apk add ffmpeg` for voice-note conversion and video thumbnails.
- `signal-cli` needs Java (`apk add openjdk21-jre`) and its `libsignal_jni` native library, which
  upstream ships for glibc only; on Alpine use `gcompat` or a musl build of libsignal-client.
- The Python TUI still needs its venv (`python3`, `py3-pip`).

## Run

```bash
megachatd/bin/megachatd serve [flags]
megachatd/bin/megachatd version
```

You normally don't run it by hand: `python3 launcher.py server start|stop|status`, `python3
megachat.py serve` and the TUI's find-or-spawn all start `megachatd serve <flags> --project-dir
<repo>`. Binary lookup: `$MEGACHATD_BIN`, else `<repo>/megachatd/bin/megachatd`, else `megachatd` on
`PATH` (`launcher/serverbin.py`). Escape hatch: `MEGACHAT_SERVER=python` (environment or the repo's
`.env`, where `install.sh` writes it when Go is missing) runs the old Python server instead.

`serve` takes the flags of `megachat serve`, plus `--project-dir`:

| flag | meaning |
|---|---|
| `--debug` | debug logging |
| `--web-port N` | web UI port (default `web.port` of config.json, else 4242) |
| `--web-host H[,H...]` | bind host(s) (default `web.host`, else 127.0.0.1) |
| `--web-no-auth` | disable the Bearer token (INSECURE) |
| `--no-web` | no web adapter |
| `--no-socket` / `--socket PATH` | no Unix socket / socket path (mutually exclusive) |
| `--project-dir DIR` | directory with `config.json` and `.env` |

Project directory: `--project-dir`, else `$MEGACHAT_PROJECT_DIR`, else the nearest ancestor of the
executable holding `megachat.py` or `pyproject.toml`, else the working directory.

Environment (same as Python): `MEGACHAT_SOCKET`, `MEGACHAT_LOCK_FILE` (default
`/tmp/signal-tui.lock`), `SIGNAL_TUI_WEB_TOKEN`, `SIGNAL_ENABLED`, `TELEGRAM_API_ID/HASH`, ... The
log goes to `/tmp/signal-tui.log` (Go-only override: `MEGACHAT_LOG_FILE`). Exit code 1 when another
instance holds the lock or an adapter cannot start. SIGINT/SIGTERM and the `server.shutdown`
operation stop the server gracefully.

Backends: Signal is always registered (it auto-connects only when `signal_enabled`); Telegram when
API credentials are configured (the existing Telethon session file is reused as is); WhatsApp
(native, whatsmeow, no WAHA) when `WHATSAPP_ENABLED=1` / `"whatsapp_enabled": true`, when
`<data>/whatsmeow.db` exists, or when a legacy `WHATSAPP_API_URL` is set.

Link WhatsApp with a QR: `python3 link_whatsapp.py` or the TUI's `Ctrl+L` (both use the `link.*`
operations). **Migration from WAHA:** a WAHA session cannot be carried over. The first start with
WhatsApp enabled and no `<data>/whatsmeow.db` prints one Italian notice ("ℹ️ WhatsApp: la sessione
WAHA non è migrabile ...", also logged; marker file `<data>/.whatsapp-relink-notice`) and
`launcher.py server start` shows it.

The WhatsApp client registers as a recent desktop Chrome (phone: "Chrome (Linux)", QR client type
Chrome) instead of whatsmeow's default "whatsmeow" identity. config.json overrides it
(env equivalents in parentheses): `whatsapp_browser` (`WHATSAPP_BROWSER`, one of Chrome, Firefox,
Edge, Opera, Safari, IE, Desktop, Android; unknown names warn and keep Chrome),
`whatsapp_browser_version` (`WHATSAPP_BROWSER_VERSION`, default tracks Chrome stable) and
`whatsapp_browser_os` (`WHATSAPP_BROWSER_OS`, default `Linux`). The identity is fixed server-side at
registration, so it applies to the next pairing; restart the server after editing config.json.

Files shared with the Python server, so the launcher's `status`/`stop`/`start` and the TUI's attach
work unchanged: the instance lock (`$MEGACHAT_LOCK_FILE`, default `/tmp/signal-tui.lock`, holds the
pid; SIGINT = clean stop), the Unix socket and the runtime file `server.json` next to it (pid), and the
log `/tmp/signal-tui.log`.

## Smoke tests

Automated (fake backend, temp HOME, Unix socket + HTTP):

```bash
go test -race -count=1 ./cmd/megachatd
```

Real binary with no backends, driven by Python's socket client (from the repo root):

```bash
T=$(mktemp -d); mkdir -p $T/home $T/proj $T/run
(cd megachatd && go build -o $T/megachatd ./cmd/megachatd)
HOME=$T/home XDG_RUNTIME_DIR=$T/run MEGACHAT_LOCK_FILE=$T/lock MEGACHAT_LOG_FILE=$T/server.log \
  MEGACHAT_PROJECT_DIR=$T/proj SIGNAL_TUI_WEB_TOKEN=tok SIGNAL_ENABLED=0 \
  $T/megachatd serve --web-port 47321 &
sleep 1.5
HOME=$T/home XDG_RUNTIME_DIR=$T/run python3 -I -c "
import sys; sys.path.insert(0, '.')
from server.socket_client import SocketClient
c = SocketClient.connect(None, timeout=5, reconnect=False)
print(c.call('server.status'))   # {'signal': False, 'telegram': False, 'whatsapp': False}
c.call('server.shutdown')
"
curl -s localhost:47321/health   # while it runs
```

## Telegram: raw vs marked contact ids (mc-zao.4.3, offline part)

The Python backend listed Telegram groups/channels with the **raw positive id**
(`str(entity.id)` in `protocols/telegram.py::_entity_to_contact`), while messages,
reactions, receipts and `tgref:` media refs used Telethon's **marked id**
(`str(msg.chat_id)`: `-id` for groups, `-100<id>` for channels). megachatd uses
marked ids for both. Where a contact id is persisted outside `messages`:

| Store | What Python wrote for groups/channels | Action |
|---|---|---|
| `messages`, `reactions` (`contact_number`, `author_key`), `transcriptions` (`tgref:<chat>:<msg>`), media file names | marked ids (all built from `msg.chat_id` / `-peer.chat_id` / `-1000000000000 - channel_id`) | none |
| Unread/read state | only `messages.read` in SQLite, keyed by marked id | none |
| Telegram address book | in memory only (`_address_book`), never persisted | none |
| Shell aliases (`docs/ALIASES.md`, `launcher/aliases.py`) | shell aliases, no contact ids; the TUI has no contact-alias storage | none |
| Pinned / archived lists | not in `config.json`; archive exists only for WhatsApp | none |
| `config.json` `auto_transcribe_chats` (`telegram:<id>`) | **raw positive id** when set from the web UI (`PUT /api/autotranscribe/chat` with `contact.id` from the contact list); marked when set by the `/trascrizione` voice command | **migrated** |
| Web SPA `localStorage` `signal-tui-web-drafts` (`telegram:<contact.id>`) | **raw positive id** | migrated client-side |
| Other SPA keys (token, protocol, skin, sizes...) | no contact ids | none |

**Migration** (`internal/proto/telegram/migrate.go`, run from `app.Build` when Telegram is
configured, before the backend starts): rewrites `telegram:<raw>` in `auto_transcribe_chats`
to the marked key. A raw id is rewritten only if exactly one of its group/channel forms is
known (Telethon session `entities`, read-only, plus the telegram `contact_number`s in the
store) and the id is not also a known user; unknown/ambiguous keys are left alone. If the
marked key already exists it wins and the raw one is dropped. `config.json` is copied to
`config.json.pre-telegram-marked-ids.bak` before the first change (never overwritten).
It is idempotent: a second run changes nothing. Tests: `migrate_test.go`.

The SPA moves old drafts in `loadContacts()` (`migrateTelegramDraftKeys` in `web/static/app.js`),
only when the raw id is not also a listed user.

Not verifiable offline: that real accounts' `entities` tables contain the groups/channels that
have overrides (an override for a chat unknown to both session and messages is kept as-is).
