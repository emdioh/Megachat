#!/usr/bin/env bash
#
# start_on_server.sh — Avvia (o ferma) il server headless direttamente SUL SERVER.
#
# Utility lato server, pensata per il caso "disaster recovery": se la macchina
# locale è irraggiungibile, si può avviare tutto da qui (via console Hetzner o
# SSH) senza dipendere dagli script di handover che girano sul locale.
#
# Wrapper sottile di `launcher.py server start|stop|status`: il launcher sceglie
# il motore (megachatd in Go di default; con MEGACHAT_SERVER=python, da ambiente
# o da .env, il vecchio server Python + WAHA), gestisce il lock di istanza
# (/tmp/signal-tui.lock) e lo stop pulito. Per l'uso interattivo, sul server:
#   .venv/bin/python megachat.py     # si collega al server in esecuzione
#
# Uso (dal server, dentro /root/signal-tui-client):
#   ./scripts/start_on_server.sh start                    # server + Web UI su 0.0.0.0:4242
#   ./scripts/start_on_server.sh start --no-docker-limits # solo server Python: WAHA senza cap CPU/RAM
#   ./scripts/start_on_server.sh stop                     # spegne il server in modo pulito
#   ./scripts/start_on_server.sh status                   # stato attuale
#
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -x "$PROJECT_DIR/.venv/bin/python" ]; then
    PY="$PROJECT_DIR/.venv/bin/python"
else
    PY=python3   # il launcher usa solo la libreria standard
fi

die() { echo "❌ $*" >&2; exit 1; }

# MEGACHAT_SERVER=python (ambiente, poi .env): server Python, che usa WAHA.
use_python() {
    local engine="${MEGACHAT_SERVER:-}"
    [ -n "$engine" ] || engine="$(sed -n 's/^MEGACHAT_SERVER=//p' "$PROJECT_DIR/.env" 2>/dev/null | tail -1 | tr -d "\"'[:space:]")"
    [ "$(printf '%s' "$engine" | tr '[:upper:]' '[:lower:]')" = python ]
}

launcher() { "$PY" "$PROJECT_DIR/launcher.py" "$@"; }

extra=()
case "${2:-}" in
    --no-docker-limits) extra+=(--no-docker-limits) ;;
    "") ;;
    *) die "Opzione sconosciuta: $2" ;;
esac

case "${1:-start}" in
    start)
        waha=()
        use_python && waha+=(--waha)
        launcher server start --web-host 0.0.0.0 --web-port 4242 ${waha[@]+"${waha[@]}"} ${extra[@]+"${extra[@]}"}
        ;;
    stop)
        waha=()
        use_python && waha+=(--waha)
        launcher server stop ${waha[@]+"${waha[@]}"}
        ;;
    status) launcher server status ;;
    *) die "uso: $0 {start|stop|status} [--no-docker-limits]" ;;
esac
