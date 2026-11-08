#!/usr/bin/env bash
#
# start_on_server.sh — Avvia (o ferma) il server headless direttamente SUL SERVER.
#
# Utility lato server, pensata per il caso "disaster recovery": se la macchina
# locale è irraggiungibile, si può avviare tutto da qui (via console Hetzner o
# SSH) senza dipendere dagli script di handover che girano sul locale.
#
# Wrapper sottile di `launcher.py server start|stop|status`: il launcher avvia
# megachatd (Go), gestisce il lock di istanza (/tmp/signal-tui.lock) e lo stop
# pulito. Per l'uso interattivo, sul server:
#   .venv/bin/python megachat.py     # si collega al server in esecuzione
#
# Uso (dal server, dentro /root/signal-tui-client):
#   ./scripts/start_on_server.sh start     # server + Web UI su 0.0.0.0:4242
#   ./scripts/start_on_server.sh stop      # spegne il server in modo pulito
#   ./scripts/start_on_server.sh status    # stato attuale
#
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -x "$PROJECT_DIR/.venv/bin/python" ]; then
    PY="$PROJECT_DIR/.venv/bin/python"
else
    PY=python3   # il launcher usa solo la libreria standard
fi

die() { echo "❌ $*" >&2; exit 1; }

launcher() { "$PY" "$PROJECT_DIR/launcher.py" "$@"; }

case "${2:-}" in
    "") ;;
    *) die "Opzione sconosciuta: $2" ;;
esac

case "${1:-start}" in
    start)
        launcher server start --web-host 0.0.0.0 --web-port 4242
        ;;
    stop)
        launcher server stop
        ;;
    status) launcher server status ;;
    *) die "uso: $0 {start|stop|status}" ;;
esac
