#!/usr/bin/env bash
#
# restart_backend.sh — Riavvia il daemon signal-cli dell'account configurato.
#
# Wrapper sottile di `launcher.py backend-restart`.
#
# Uso: ./scripts/restart_backend.sh [--no-wait]
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
        --no-wait) ;;
        --help|-h) echo "Uso: $0 [--no-wait]"; exit 0 ;;
        *) echo "❌ Opzione sconosciuta: $arg" >&2; exit 1 ;;
    esac
done

exec "$PY" "$PROJECT_DIR/launcher.py" backend-restart "$@"
