# megachatd

Go port of the Megachat headless server (`megachat serve`). The Python TUI and the web SPA are
unchanged clients: the wire contract is in `CONTRACTS.md`, the Go twin in `internal/api`.

## Build

```bash
cd megachatd
go build -o megachatd ./cmd/megachatd     # Go 1.26
go vet ./... && go test -race ./...
```

## Run

```bash
./megachatd serve [flags]
./megachatd version
```

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
API credentials are configured; WhatsApp (native, whatsmeow, no WAHA) when `WHATSAPP_ENABLED=1` /
`"whatsapp_enabled": true`, when `<data>/whatsmeow.db` exists, or when a legacy `WHATSAPP_API_URL`
is set.

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
