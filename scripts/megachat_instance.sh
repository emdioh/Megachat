#!/usr/bin/env bash
# =============================================================================
# megachat_instance.sh — create and run extra, fully isolated Megachat
# instances on the same machine (e.g. one for a friend).
#
# Each instance is a directory (MEGACHAT_HOME, see instance.py) holding its
# own config.json, .env, message DB, media, logs, lock and socket, plus its
# own WAHA container (own WhatsApp session, ports and data).  The main
# instance (no MEGACHAT_HOME) is never touched.
#
# Usage:
#   scripts/megachat_instance.sh create NAME [--offset N]
#   scripts/megachat_instance.sh up NAME          # start its WAHA container
#   scripts/megachat_instance.sh link NAME        # pair WhatsApp (QR code)
#   scripts/megachat_instance.sh start NAME       # server in background
#   scripts/megachat_instance.sh serve NAME [ARGS] # server in foreground
#   scripts/megachat_instance.sh stop NAME
#   scripts/megachat_instance.sh status [NAME]
#   scripts/megachat_instance.sh logs NAME [server|waha]
#   scripts/megachat_instance.sh down NAME        # stop its WAHA container
#   scripts/megachat_instance.sh env NAME         # eval "$(... env NAME)"
#   scripts/megachat_instance.sh systemd NAME     # print a systemd user unit
#   scripts/megachat_instance.sh list
#
# Instances live in $MEGACHAT_INSTANCES_DIR (default: <repo>/instances,
# git-ignored).  Ports are the defaults shifted by the offset N (default: the
# first free one, starting at 1):
#   WAHA 3005+10N, webhook 8088+10N, signal-cli 8080+10N,
#   web 4242+N, download 10042+N
# The server runs headless without the web UI (`serve --no-web`) by default:
# pass `--web-port`, or set "web": {"enabled": true} in the instance
# config.json, to have it.
# =============================================================================
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTANCES_DIR="${MEGACHAT_INSTANCES_DIR:-$REPO_DIR/instances}"

die() { echo "❌ $*" >&2; exit 1; }
info() { echo "ℹ️  $*"; }

python_bin() {
    if [ -n "${PYTHON:-}" ]; then
        echo "$PYTHON"
    elif [ -x "$REPO_DIR/.venv/bin/python" ]; then
        echo "$REPO_DIR/.venv/bin/python"
    else
        command -v python3 || die "python3 not found"
    fi
}

valid_name() {
    [[ "$1" =~ ^[a-z0-9][a-z0-9_-]{0,30}$ ]] \
        || die "invalid name '$1' (lowercase letters, digits, - and _)"
}

instance_dir() {
    [ -n "${1:-}" ] || die "missing instance NAME"
    valid_name "$1"
    echo "$INSTANCES_DIR/$1"
}

require_instance() {
    local dir
    dir="$(instance_dir "$1")"
    [ -f "$dir/.env" ] || die "instance '$1' not found ($dir). Run: $0 create $1"
    echo "$dir"
}

# Value of KEY in an .env file (last occurrence wins, quotes stripped).
env_value() {
    local file="$1" key="$2"
    sed -n "s/^[[:space:]]*\(export[[:space:]]\+\)\{0,1\}${key}=//p" "$file" \
        | tail -n 1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"
}

random_secret() {
    "$(python_bin)" -c 'import secrets; print(secrets.token_hex(24))'
}

used_offsets() {
    local env_file
    for env_file in "$INSTANCES_DIR"/*/.env; do
        [ -f "$env_file" ] || continue
        env_value "$env_file" MEGACHAT_PORT_OFFSET
    done
}

next_offset() {
    local used n=1
    used=" $(used_offsets | tr '\n' ' ') "
    while [[ "$used" == *" $n "* ]]; do n=$((n + 1)); done
    echo "$n"
}

waha_image() {
    local main_image=""
    if [ -f "$REPO_DIR/.env" ]; then
        main_image="$(env_value "$REPO_DIR/.env" WAHA_IMAGE)"
    fi
    if [ -n "$main_image" ]; then
        echo "$main_image"
    else
        case "$(uname -m)" in
            arm64|aarch64) echo "devlikeapro/waha:arm" ;;
            *) echo "devlikeapro/waha:latest" ;;
        esac
    fi
}

cmd_create() {
    local name="${1:-}" offset=""
    shift || true
    while [ $# -gt 0 ]; do
        case "$1" in
            --offset) offset="${2:-}"; shift 2 ;;
            --offset=*) offset="${1#--offset=}"; shift ;;
            *) die "unknown option: $1" ;;
        esac
    done
    local dir
    dir="$(instance_dir "$name")"
    [ -e "$dir/.env" ] && die "instance '$name' already exists ($dir)"
    if [ -z "$offset" ]; then
        offset="$(next_offset)"
    fi
    [[ "$offset" =~ ^[1-9][0-9]?$ ]] || die "--offset must be 1..99"
    if used_offsets | grep -qx "$offset"; then
        die "offset $offset is already used by another instance"
    fi

    mkdir -p "$dir/data" "$dir/logs" "$dir/run" "$dir/whatsapp-data"
    chmod 700 "$dir"

    local api_key dash_pw swagger_pw
    api_key="$(random_secret)"
    dash_pw="$(random_secret)"
    swagger_pw="$(random_secret)"

    umask 077
    cat > "$dir/.env" <<EOF
# Megachat instance "$name" — created by scripts/megachat_instance.sh.
# Read by the Python server (MEGACHAT_HOME=$dir) AND passed to its WAHA
# container (docker compose env_file), so keep it private (chmod 600).
MEGACHAT_PORT_OFFSET=$offset

# ── WhatsApp (WAHA container of this instance) ────────────────────────────
WAHA_IMAGE=$(waha_image)
WAHA_CONTAINER_NAME=megachat-$name-whatsapp
WAHA_DATA_PATH=$dir/whatsapp-data
MEGACHAT_ENV_FILE=$dir/.env
WAHA_API_KEY=$api_key
WAHA_DASHBOARD_USERNAME=admin
WAHA_DASHBOARD_PASSWORD=$dash_pw
WHATSAPP_SWAGGER_USERNAME=admin
WHATSAPP_SWAGGER_PASSWORD=$swagger_pw
WHATSAPP_API_PORT=$((3005 + 10 * offset))
WHATSAPP_API_URL=http://127.0.0.1:$((3005 + 10 * offset))
CLIENT_WEBHOOK_PORT=$((8088 + 10 * offset))

# ── Other ports (only used if the matching feature is enabled) ────────────
SIGNAL_DAEMON_PORT=$((8080 + 10 * offset))
MEGACHAT_DOWNLOAD_PORT=$((10042 + offset))

# ── Backends ──────────────────────────────────────────────────────────────
# WhatsApp only: no signal-cli, no Telegram.
SIGNAL_ENABLED=0

# ── Voice-note transcription ──────────────────────────────────────────────
# Fill in ONE provider key (see .env.example in the repo for all options).
AI_PROVIDER=openai
OPENAI_API_KEY=
# AI_PROVIDER=openrouter
# OPENROUTER_API_KEY=
# AI_PROVIDER=gemini
# GEMINI_API_KEY=
TRANSCRIPTION_LANGUAGE=it
EOF
    cat > "$dir/config.json" <<EOF
{
  "web": {
    "enabled": false,
    "port": $((4242 + offset))
  },
  "auto_transcribe": {
    "whatsapp": true
  }
}
EOF
    chmod 600 "$dir/.env" "$dir/config.json"

    echo "✅ Instance '$name' created in $dir (port offset $offset)"
    echo
    echo "Next steps:"
    echo "  1. Put the transcription API key in $dir/.env (OPENAI_API_KEY=...)"
    echo "  2. $0 up $name        # start its WhatsApp container"
    echo "  3. $0 link $name      # scan the QR with your friend's WhatsApp"
    echo "  4. $0 start $name     # start the server (headless, no web UI)"
}

compose() {
    local dir="$1"
    shift
    local files=(-f "$REPO_DIR/docker-compose.yml")
    local limits
    limits="$(env_value "$dir/.env" WAHA_DOCKER_LIMITS)"
    if [ "${limits:-1}" != "0" ] && [ -f "$REPO_DIR/docker-compose.resources.yml" ]; then
        files+=(-f "$REPO_DIR/docker-compose.resources.yml")
    fi
    (cd "$REPO_DIR" && docker compose -p "megachat-$(basename "$dir")" \
        --env-file "$dir/.env" "${files[@]}" "$@")
}

cmd_up() {
    local dir
    dir="$(require_instance "${1:-}")"
    compose "$dir" up -d
}

cmd_down() {
    local dir
    dir="$(require_instance "${1:-}")"
    compose "$dir" down
}

run_python() {
    local dir="$1"
    shift
    cd "$REPO_DIR"
    MEGACHAT_HOME="$dir" exec "$(python_bin)" "$@"
}

cmd_link() {
    local dir
    dir="$(require_instance "${1:-}")"
    run_python "$dir" link_whatsapp.py
}

web_enabled() {
    MEGACHAT_HOME="$1" "$(python_bin)" -c \
        'import sys; sys.path.insert(0, sys.argv[1]); from protocols.config import web_enabled; sys.exit(0 if web_enabled() else 1)' \
        "$REPO_DIR" 2>/dev/null
}

# `megachat serve` arguments: --no-web unless the web UI is enabled in the
# instance config.json or a --web-* option was given; then the caller's ARGS.
serve_args() {
    local dir="$1"
    shift
    if [[ " $* " != *" --web-"* ]] && ! web_enabled "$dir"; then
        echo "--no-web"
    fi
    local arg
    for arg in "$@"; do echo "$arg"; done
}

server_pid() {
    local dir="$1" pid=""
    if [ -f "$dir/run/signal-tui.lock" ]; then
        pid="$(tr -dc '0-9' < "$dir/run/signal-tui.lock")"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo "$pid"
            return 0
        fi
    fi
    return 1
}

cmd_serve() {
    local dir
    dir="$(require_instance "${1:-}")"
    shift
    local args=()
    mapfile -t args < <(serve_args "$dir" "$@")
    run_python "$dir" megachat.py serve "${args[@]}"
}

cmd_start() {
    local name="${1:-}" dir
    dir="$(require_instance "$name")"
    shift || true
    if pid="$(server_pid "$dir")"; then
        info "instance '$name' is already running (PID $pid)"
        return 0
    fi
    local args=()
    mapfile -t args < <(serve_args "$dir" "$@")
    (
        cd "$REPO_DIR"
        MEGACHAT_HOME="$dir" nohup "$(python_bin)" megachat.py serve "${args[@]}" \
            >> "$dir/logs/megachat-serve.log" 2>&1 < /dev/null &
    )
    local i
    for i in $(seq 1 20); do
        if pid="$(server_pid "$dir")"; then
            echo "✅ instance '$name' started (PID $pid) — logs: $0 logs $name"
            return 0
        fi
        sleep 0.5
    done
    die "server did not start; see $dir/logs/megachat-serve.log and $dir/logs/signal-tui.log"
}

cmd_stop() {
    local name="${1:-}" dir pid
    dir="$(require_instance "$name")"
    if ! pid="$(server_pid "$dir")"; then
        info "instance '$name' is not running"
        return 0
    fi
    kill -TERM "$pid"
    local i
    for i in $(seq 1 30); do
        kill -0 "$pid" 2>/dev/null || { echo "✅ instance '$name' stopped"; return 0; }
        sleep 0.5
    done
    die "PID $pid still alive after 15s (kill -KILL $pid to force)"
}

status_one() {
    local dir="$1" name pid server="stopped" waha="stopped" container
    name="$(basename "$dir")"
    if pid="$(server_pid "$dir")"; then server="running (PID $pid)"; fi
    container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
    if command -v docker >/dev/null 2>&1 \
        && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "$container"; then
        waha="running ($container, port $(env_value "$dir/.env" WHATSAPP_API_PORT))"
    fi
    printf '%-16s server: %-22s WAHA: %s\n' "$name" "$server" "$waha"
}

cmd_status() {
    if [ -n "${1:-}" ]; then
        status_one "$(require_instance "$1")"
        return 0
    fi
    local dir found=0
    for dir in "$INSTANCES_DIR"/*/; do
        [ -f "$dir/.env" ] || continue
        found=1
        status_one "${dir%/}"
    done
    [ "$found" = 1 ] || info "no instances in $INSTANCES_DIR"
}

cmd_list() {
    local dir
    for dir in "$INSTANCES_DIR"/*/; do
        [ -f "$dir/.env" ] && basename "$dir"
    done
    return 0
}

cmd_logs() {
    local dir
    dir="$(require_instance "${1:-}")"
    case "${2:-server}" in
        server) exec tail -n 100 -F "$dir/logs/signal-tui.log" ;;
        waha) compose "$dir" logs -f --tail 100 ;;
        *) die "logs: expected 'server' or 'waha'" ;;
    esac
}

cmd_env() {
    local dir
    dir="$(require_instance "${1:-}")"
    printf 'export MEGACHAT_HOME=%q\n' "$dir"
}

cmd_systemd() {
    local name="${1:-}" dir
    dir="$(require_instance "$name")"
    local args
    args="$(serve_args "$dir" | tr '\n' ' ')"
    cat <<EOF
# ~/.config/systemd/user/megachat-$name.service
#   systemctl --user daemon-reload
#   systemctl --user enable --now megachat-$name
#   loginctl enable-linger \$USER   # keep it running after logout
[Unit]
Description=Megachat instance $name
After=network-online.target docker.service

[Service]
Environment=MEGACHAT_HOME=$dir
WorkingDirectory=$REPO_DIR
ExecStartPre=-/usr/bin/env docker start $(env_value "$dir/.env" WAHA_CONTAINER_NAME)
ExecStart=$(python_bin) $REPO_DIR/megachat.py serve ${args% }
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
EOF
}

usage() {
    sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

main() {
    local cmd="${1:-}"
    shift || true
    case "$cmd" in
        create) cmd_create "$@" ;;
        up) cmd_up "$@" ;;
        down) cmd_down "$@" ;;
        link) cmd_link "$@" ;;
        serve) cmd_serve "$@" ;;
        start) cmd_start "$@" ;;
        stop) cmd_stop "$@" ;;
        status) cmd_status "$@" ;;
        list) cmd_list "$@" ;;
        logs) cmd_logs "$@" ;;
        env) cmd_env "$@" ;;
        systemd) cmd_systemd "$@" ;;
        -h|--help|help|"") usage 0 ;;
        *) echo "unknown command: $cmd" >&2; usage 1 ;;
    esac
}

main "$@"
