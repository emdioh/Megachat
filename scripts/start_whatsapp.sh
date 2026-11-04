#!/usr/bin/env bash
#
# start_whatsapp.sh — WhatsApp è ora nativo (megachatd), non serve più WAHA.
#
# Il collegamento avviene con un QR, senza Docker:
#   python link_whatsapp.py
#
# Questo script serve solo a chi usa ancora il vecchio server Python
# (MEGACHAT_SERVER=python) con WAHA (WhatsApp HTTP API via Docker Compose) e
# funziona solo con --waha.
#
# Uso:
#   ./scripts/start_whatsapp.sh                          # spiega e termina (nessun WAHA)
#   ./scripts/start_whatsapp.sh --waha                   # avvia WAHA + attende (cap CPU/RAM attivi)
#   ./scripts/start_whatsapp.sh --waha --no-wait         # avvia senza attendere
#   ./scripts/start_whatsapp.sh --waha --no-docker-limits # avvia senza cap CPU/RAM
#   ./scripts/start_whatsapp.sh --waha --stop            # ferma il container
#
# WAHA_DOCKER_LIMITS=0 nell'ambiente equivale a --no-docker-limits.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Port defaults to 3005, configurable via WHATSAPP_API_PORT. An explicit
# WHATSAPP_API_URL override wins.
WA_PORT="${WHATSAPP_API_PORT:-3005}"
API_URL="${WHATSAPP_API_URL:-http://127.0.0.1:${WA_PORT}}"
WAIT=1
DOCKER_LIMITS="${WAHA_DOCKER_LIMITS:-1}"

die() { echo "❌ $*" >&2; exit 1; }

compose_args() {
    local args=(-f "$PROJECT_DIR/docker-compose.yml")
    if [ "$DOCKER_LIMITS" -eq 1 ]; then
        args+=(-f "$PROJECT_DIR/docker-compose.resources.yml")
    fi
    printf '%s\n' "${args[@]}"
}

USE_WAHA=0
STOP=0
for arg in "$@"; do
    case "$arg" in
        --waha)             USE_WAHA=1;;
        --no-wait)          WAIT=0;;
        --no-docker-limits) DOCKER_LIMITS=0;;
        --stop)             STOP=1;;
        *)                  die "Opzione sconosciuta: $arg";;
    esac
done

if [ "$USE_WAHA" -ne 1 ]; then
    echo "ℹ️  WhatsApp è ora nativo (megachatd): WAHA non serve più."
    echo "   Per collegare l'account con un QR: python link_whatsapp.py"
    echo "   Solo con il vecchio server Python (MEGACHAT_SERVER=python) WAHA è ancora"
    echo "   necessario: in quel caso rilancia con --waha."
    exit 0
fi

if [ "$STOP" -eq 1 ]; then
    mapfile -t stop_args < <(compose_args)
    docker compose "${stop_args[@]}" down
    echo "WAHA fermato."
    exit 0
fi

command -v docker >/dev/null 2>&1 || { echo "❌ docker non trovato. Installa Docker e riprova."; exit 1; }

mapfile -t COMPOSE_ARGS < <(compose_args)
if [ "$DOCKER_LIMITS" -eq 1 ]; then
    echo "🟢 Avvio WhatsApp HTTP API (WAHA) via Docker Compose (cap CPU/RAM attivi)..."
else
    echo "🟢 Avvio WhatsApp HTTP API (WAHA) via Docker Compose (--no-docker-limits: nessun cap)..."
fi
docker compose "${COMPOSE_ARGS[@]}" up -d

if [ "$WAIT" -eq 1 ]; then
    # Carica la chiave API dal .env (stessa che docker-compose passa al container)
    # così l'healthcheck autentica correttamente e non ottiene 401.  Usiamo
    # /api/version (esiste su tutte le build "core"); /api/server non è più
    # esposto su quelle recenti (404).
    WA_API_KEY="${WAHA_API_KEY:-}"
    if [ -f "$PROJECT_DIR/.env" ]; then
        WA_API_KEY="$(grep -E '^WAHA_API_KEY=' "$PROJECT_DIR/.env" | head -1 | cut -d= -f2- | tr -d '[:space:]')"
    fi

    echo "⏳ Attendo che l'API risponda su $API_URL ..."
    for i in $(seq 1 60); do
        if [ -n "$WA_API_KEY" ]; then
            ok=$(curl -fsS -H "X-Api-Key: $WA_API_KEY" "$API_URL/api/version" >/dev/null 2>&1 && echo 1 || echo 0)
        else
            ok=$(curl -fsS "$API_URL/api/version" >/dev/null 2>&1 && echo 1 || echo 0)
        fi
        if [ "$ok" -eq 1 ]; then
            echo "✅ WhatsApp HTTP API pronta: $API_URL"
            exit 0
        fi
        sleep 2
    done
    echo "❌ Timeout: l'API non risponde su $API_URL dopo ~120s." >&2
    echo "   Controlla: docker compose -f \"$PROJECT_DIR/docker-compose.yml\" logs whatsapp"
    exit 1
fi
echo "▶️  Comando di avvio inviato (--no-wait)."
