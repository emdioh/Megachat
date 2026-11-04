#!/usr/bin/env bash
#
# restart_backend.sh — Riavvia il daemon signal-cli (e, solo con il vecchio
# server Python, MEGACHAT_SERVER=python, anche WAHA).
#
# Wrapper sottile di `launcher.py backend-restart`: con megachatd WhatsApp è
# nativo e WAHA non viene toccata.
#
# Uso: ./scripts/restart_backend.sh [--no-wait] [--no-docker-limits]
#
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -x "$PROJECT_DIR/.venv/bin/python" ]; then
    PY="$PROJECT_DIR/.venv/bin/python"
else
    PY=python3
fi

for arg in "$@"; do
    case "$arg" in
        --no-wait|--no-docker-limits) ;;
        --help|-h) echo "Uso: $0 [--no-wait] [--no-docker-limits]"; exit 0 ;;
        *) echo "❌ Opzione sconosciuta: $arg" >&2; exit 1 ;;
    esac
done

exec "$PY" "$PROJECT_DIR/launcher.py" backend-restart "$@"
