#!/usr/bin/env bash
#
# tui_handover.sh — Passa il server headless da una macchina all'altra.
#
# Gli account Signal/WhatsApp/Telegram sono condivisi: il server (e, con il
# server Python, la sessione WAHA e il daemon signal-cli) NON devono essere
# attivi su due macchine contemporaneamente. Questo script è un wrapper sottile
# di `launcher.py handover`, che ferma/avvia il server con il motore giusto
# (megachatd di default; MEGACHAT_SERVER=python per il vecchio server Python)
# e centralizza il passaggio pulito.
#
# Uso:
#   ./scripts/tui_handover.sh to-server [--no-docker-limits]  # spegni locale → accendi il server Hetzner
#   ./scripts/tui_handover.sh to-local  [--no-docker-limits]  # spegni il server → accendi il locale
#   ./scripts/tui_handover.sh status                          # stato server/daemon su entrambe
#
# Configurazione (letta da launcher/handover.py):
#   HZ_HOST — IP del server Hetzner (default 167.233.140.207)
#   HZ_USER — utente SSH sul server (default root)
#   ~/.config/signal-tui-handover.conf con HZ_HOST=<nuovo-ip> se l'IP cambia.
#   Richiede chiave SSH già installata sul server (ssh-copy-id); la password
#   non viene mai usata.
#
# Dopo l'handover, sulla macchina di destinazione la TUI si apre con
# `.venv/bin/python megachat.py` (si collega al server in esecuzione).
#
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -x "$PROJECT_DIR/.venv/bin/python" ]; then
    PY="$PROJECT_DIR/.venv/bin/python"
else
    PY=python3
fi

die() { echo "❌ $*" >&2; exit 1; }

case "${2:-}" in
    --no-docker-limits|"") ;;
    *) die "Opzione sconosciuta: $2" ;;
esac

case "${1:-}" in
    to-server|to-local|status) exec "$PY" "$PROJECT_DIR/launcher.py" handover "$@" ;;
    *) die "uso: $0 {to-server|to-local|status} [--no-docker-limits]" ;;
esac
