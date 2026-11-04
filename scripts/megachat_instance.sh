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
# Full guide (in Italian): scripts/megachat_instance.sh help [COMMAND|all]
# The help texts live in help_text() below.
# =============================================================================
if [ -z "${BASH_VERSION:-}" ]; then
    echo "❌ megachat_instance.sh needs bash (Alpine: apk add bash)" >&2
    exit 1
fi
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
    [ ! -e "$dir/.destroyed" ] \
        || die "instance '$1' was destroyed (config kept). Run: $0 create $1"
    echo "$dir"
}

# Value of KEY in an .env file (last occurrence wins, quotes stripped).
env_value() {
    local file="$1" key="$2"
    sed -n "s/^[[:space:]]*\(export[[:space:]][[:space:]]*\)\{0,1\}${key}=//p" "$file" \
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
    local name="${1:-}" offset="" limits="" files=()
    shift || true
    while [ $# -gt 0 ]; do
        case "$1" in
            --offset) offset="${2:-}"; shift 2 ;;
            --offset=*) offset="${1#--offset=}"; shift ;;
            --no-docker-limits) limits=0; shift ;;
            --docker-limits) limits=1; shift ;;
            --compose-file)
                [ -n "${2:-}" ] || die "--compose-file needs a file"
                files+=("$(abs_compose_file "$2")"); shift 2 ;;
            --compose-file=*)
                files+=("$(abs_compose_file "${1#--compose-file=}")"); shift ;;
            *) die "unknown option: $1" ;;
        esac
    done
    local compose_list=""
    if [ "${#files[@]}" -gt 0 ]; then
        compose_list="$(IFS=:; echo "${files[*]}")"
    fi
    local dir
    dir="$(instance_dir "$name")"
    if [ -e "$dir/.env" ]; then
        [ -e "$dir/.destroyed" ] \
            || die "instance '$name' already exists ($dir); 'destroy $name' first to rebuild it"
        [ -z "$limits" ] && [ -z "$compose_list" ] \
            || die "instance '$name' keeps its .env: set WAHA_DOCKER_LIMITS / WAHA_COMPOSE_FILES there"
        recreate_instance "$name" "$dir" "$offset"
        return 0
    fi
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

    # Docker settings: options first, else inherited from the main .env (so a
    # host that needs WAHA_DOCKER_LIMITS=0, e.g. an LXC container, keeps it).
    local main_env="$REPO_DIR/.env" inherited=""
    if [ -z "$limits" ] && [ -f "$main_env" ]; then
        if is_off "$(env_value "$main_env" WAHA_DOCKER_LIMITS)"; then
            limits=0
            inherited="WAHA_DOCKER_LIMITS=0"
        fi
    fi
    if [ "${#files[@]}" -eq 0 ] && [ -f "$main_env" ]; then
        local main_files entry path resolved=()
        main_files="$(env_value "$main_env" WAHA_COMPOSE_FILES)"
        local IFS=:
        for entry in $main_files; do
            [ -n "$entry" ] || continue
            path="${entry/#\~/$HOME}"
            [[ "$path" = /* ]] || path="$REPO_DIR/$path"
            resolved+=("$path")
        done
        unset IFS
        if [ "${#resolved[@]}" -gt 0 ]; then
            compose_list="$(IFS=:; echo "${resolved[*]}")"
            inherited="${inherited:+$inherited, }WAHA_COMPOSE_FILES"
        fi
    fi
    local limits_line="# WAHA_DOCKER_LIMITS=0" files_line="# WAHA_COMPOSE_FILES=docker-compose.local.yml"
    [ "$limits" = 0 ] && limits_line="WAHA_DOCKER_LIMITS=0"
    [ -n "$compose_list" ] && files_line="WAHA_COMPOSE_FILES=$compose_list"

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

# ── Docker compose files of its WAHA container ───────────────────────────
# WAHA_DOCKER_LIMITS=0 skips the CPU/RAM caps of docker-compose.resources.yml
# (needed e.g. in an LXC container without the memory cgroup).
# WAHA_COMPOSE_FILES: extra compose files layered last (their values win),
# ":"-separated, relative to the repository. Applied by up/down/destroy.
$limits_line
$files_line

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
    write_default_config "$dir" "$offset"
    chmod 600 "$dir/.env" "$dir/config.json"

    echo "✅ Instance '$name' created in $dir (port offset $offset)"
    [ -z "$inherited" ] || info "from the main .env: $inherited"
    local desc
    desc="$(describe_compose "$dir")" || desc="(see WAHA_COMPOSE_FILES in $dir/.env)"
    info "WAHA compose files: $desc"
    next_steps "$name" "$dir"
}

write_default_config() {
    local dir="$1" offset="$2"
    (
        umask 077
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
    )
    "$(python_bin)" "$REPO_DIR/scripts/instance_config.py" status-page \
        "$dir/config.json" "$offset" > /dev/null || [ $? -eq 10 ]
}

# Rebuild a destroyed instance from the .env (and config.json) it kept.
recreate_instance() {
    local name="$1" dir="$2" wanted="$3" offset
    offset="$(env_value "$dir/.env" MEGACHAT_PORT_OFFSET)"
    [[ "$offset" =~ ^[1-9][0-9]?$ ]] || die "$dir/.env has no valid MEGACHAT_PORT_OFFSET"
    if [ -n "$wanted" ] && [ "$wanted" != "$offset" ]; then
        die "instance '$name' keeps offset $offset from its .env (edit it there to change the ports)"
    fi
    mkdir -p "$dir/data" "$dir/logs" "$dir/run" "$dir/whatsapp-data"
    chmod 700 "$dir"
    if [ -f "$dir/config.json" ]; then
        "$(python_bin)" "$REPO_DIR/scripts/instance_config.py" status-page \
            "$dir/config.json" "$offset" > /dev/null || [ $? -eq 10 ]
    else
        write_default_config "$dir" "$offset"
    fi
    chmod 600 "$dir/.env" "$dir/config.json"
    rm -f "$dir/.destroyed"
    echo "✅ Instance '$name' recreated in $dir (port offset $offset, existing .env and config.json kept)"
    next_steps "$name" "$dir"
}

next_steps() {
    local name="$1" dir="$2"
    echo
    echo "Next steps:"
    echo "  1. Put the transcription API key in $dir/.env (OPENAI_API_KEY=...)"
    echo "  2. $0 up $name        # start its WhatsApp container"
    echo "  3. $0 link $name      # scan the QR with your friend's WhatsApp"
    echo "  4. $0 start $name     # start the server (headless, no web UI)"
    echo "     or: $0 install $name   # start it at boot with systemd"
    echo "  5. $0 url $name       # status/QR page link to send to your friend"
}

# ── docker compose files ─────────────────────────────────────────────────
# Same rules as launcher.py: docker-compose.yml, then the CPU/RAM overlay
# docker-compose.resources.yml (unless WAHA_DOCKER_LIMITS=0 or
# --no-docker-limits), then the extra files of WAHA_COMPOSE_FILES
# (":"-separated, relative to the repository) and of --compose-file.

# One-run overrides, set by parse_docker_opts.
RUN_NO_LIMITS=0
RUN_COMPOSE_FILES=()

is_off() {
    case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')" in
        0|false|no|off) return 0 ;;
        *) return 1 ;;
    esac
}

# Absolute path of a compose file given on the command line (must exist).
abs_compose_file() {
    local file="$1"
    [ -f "$file" ] || die "compose file not found: $file"
    (cd "$(dirname "$file")" && echo "$(pwd)/$(basename "$file")")
}

# Parse --no-docker-limits / --compose-file F out of "$@"; the remaining
# arguments are left in REST_ARGS.
parse_docker_opts() {
    REST_ARGS=()
    while [ $# -gt 0 ]; do
        case "$1" in
            --no-docker-limits) RUN_NO_LIMITS=1 ;;
            --compose-file)
                [ -n "${2:-}" ] || die "--compose-file needs a file"
                RUN_COMPOSE_FILES+=("$(abs_compose_file "$2")")
                shift
                ;;
            --compose-file=*) RUN_COMPOSE_FILES+=("$(abs_compose_file "${1#--compose-file=}")") ;;
            *) REST_ARGS+=("$1") ;;
        esac
        shift
    done
}

# The "-f FILE" list for an instance, one argument per line.
compose_files() {
    local dir="$1" entry path
    echo "-f"
    echo "$REPO_DIR/docker-compose.yml"
    if [ "$RUN_NO_LIMITS" != 1 ] && ! is_off "$(env_value "$dir/.env" WAHA_DOCKER_LIMITS)" \
        && [ -f "$REPO_DIR/docker-compose.resources.yml" ]; then
        echo "-f"
        echo "$REPO_DIR/docker-compose.resources.yml"
    fi
    local IFS=:
    for entry in $(env_value "$dir/.env" WAHA_COMPOSE_FILES); do
        [ -n "$entry" ] || continue
        path="${entry/#\~/$HOME}"
        [[ "$path" = /* ]] || path="$REPO_DIR/$path"
        [ -f "$path" ] || die "WAHA_COMPOSE_FILES: file not found: $path (see $dir/.env)"
        echo "-f"
        echo "$path"
    done
    unset IFS
    for path in "${RUN_COMPOSE_FILES[@]}"; do
        echo "-f"
        echo "$path"
    done
}

compose() {
    local dir="$1"
    shift
    local files=() list
    list="$(compose_files "$dir")" || exit 1
    mapfile -t files <<< "$list"
    (cd "$REPO_DIR" && docker compose -p "megachat-$(basename "$dir")" \
        --env-file "$dir/.env" "${files[@]}" "$@")
}

# Human-readable list of the compose files in use.
describe_compose() {
    local dir="$1" line out=() list
    list="$(compose_files "$dir")" || exit 1
    while IFS= read -r line; do
        [ "$line" = "-f" ] || out+=("${line#"$REPO_DIR/"}")
    done <<< "$list"
    local joined="" item
    for item in "${out[@]}"; do joined="${joined:+$joined + }$item"; done
    echo "$joined"
}

cmd_up() {
    local dir
    parse_docker_opts "$@"
    set -- "${REST_ARGS[@]+"${REST_ARGS[@]}"}"
    dir="$(require_instance "${1:-}")"
    local desc
    desc="$(describe_compose "$dir")" || exit 1
    info "compose files: $desc"
    compose "$dir" up -d
}

cmd_down() {
    local dir
    parse_docker_opts "$@"
    set -- "${REST_ARGS[@]+"${REST_ARGS[@]}"}"
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
        print_status_page "$name" "$dir"
        return 0
    fi
    if service_installed "$name"; then
        [ $# -eq 0 ] || die "instance '$name' runs as a service: no extra ARGS (re-run 'install $name' after changing config.json)"
        service_start "$name"
        echo "✅ instance '$name' started via $(service_kind "$name") ($(service_name "$name"))"
        print_status_page "$name" "$dir"
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
            print_status_page "$name" "$dir"
            return 0
        fi
        sleep 0.5
    done
    die "server did not start; see $dir/logs/megachat-serve.log and $dir/logs/signal-tui.log"
}

cmd_stop() {
    local name="${1:-}" dir pid
    dir="$(require_instance "$name")"
    if service_installed "$name" && service_active "$name"; then
        service_stop "$name"
        echo "✅ instance '$name' stopped via $(service_kind "$name")"
        return 0
    fi
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
    if [ -e "$dir/.destroyed" ]; then
        printf '%-16s destroyed (.env and config.json kept: "create %s" rebuilds it)\n' \
            "$name" "$name"
        return 0
    fi
    if pid="$(server_pid "$dir")"; then server="running (PID $pid)"; fi
    container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
    if command -v docker >/dev/null 2>&1 \
        && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "$container"; then
        waha="running ($container, port $(env_value "$dir/.env" WHATSAPP_API_PORT))"
    fi
    local enabled port token qr="off"
    read -r enabled port token <<< "$(status_page_info "$dir")"
    [ "$enabled" = 1 ] && qr="port $port"
    printf '%-16s server: %-22s WAHA: %-44s QR page: %-10s service: %s\n' \
        "$name" "$server" "$waha" "$qr" "$(service_state "$name")"
}

cmd_status() {
    if [ -n "${1:-}" ]; then
        local one
        one="$(instance_dir "$1")"
        [ -f "$one/.env" ] || die "instance '$1' not found ($one)"
        status_one "$one"
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
    local dir name enabled port token qr
    for dir in "$INSTANCES_DIR"/*/; do
        [ -f "$dir/.env" ] || continue
        dir="${dir%/}"
        name="$(basename "$dir")"
        read -r enabled port token <<< "$(status_page_info "$dir")"
        if [ -e "$dir/.destroyed" ]; then
            qr="destroyed"
        elif [ "$enabled" = 1 ]; then
            qr="status/QR page port $port"
        else
            qr="status/QR page off"
        fi
        printf '%-16s offset %-3s WAHA %-6s %s\n' "$name" \
            "$(env_value "$dir/.env" MEGACHAT_PORT_OFFSET)" \
            "$(env_value "$dir/.env" WHATSAPP_API_PORT)" "$qr"
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

# ── status page ──────────────────────────────────────────────────────────

public_host() {
    if [ -n "${MEGACHAT_PUBLIC_HOST:-}" ]; then
        echo "$MEGACHAT_PUBLIC_HOST"
        return
    fi
    local ip
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')" || ip=""
    if [ -z "$ip" ] && command -v ip >/dev/null 2>&1; then
        ip="$(ip -4 -o addr show scope global 2>/dev/null \
            | awk '{sub(/\/.*/, "", $4); print $4; exit}')" || ip=""
    fi
    echo "${ip:-$(hostname)}"
}

# "ENABLED PORT TOKEN" of the instance status page (read only).
status_page_info() {
    "$(python_bin)" "$REPO_DIR/scripts/instance_config.py" show "$1/config.json" \
        2>/dev/null || echo "0 - -"
}

# One line about the status page, printed by start.
print_status_page() {
    local name="$1" dir="$2" enabled port token
    read -r enabled port token <<< "$(status_page_info "$dir")"
    if [ "$enabled" = 1 ]; then
        echo "📊 status/QR page on port $port: http://$(public_host):$port/$token/"
    else
        info "status/QR page disabled (enable it with: $0 url $name, then restart)"
    fi
}

cmd_url() {
    local name="${1:-}" dir offset out rc=0 port token
    dir="$(require_instance "$name")"
    offset="$(env_value "$dir/.env" MEGACHAT_PORT_OFFSET)"
    out="$("$(python_bin)" "$REPO_DIR/scripts/instance_config.py" status-page \
        "$dir/config.json" "${offset:-1}")" || rc=$?
    [ "$rc" -eq 0 ] || [ "$rc" -eq 10 ] || die "could not update $dir/config.json"
    read -r port token <<< "$out"
    echo "http://$(public_host):$port/$token/"
    if [ "$rc" -eq 10 ] && server_pid "$dir" >/dev/null; then
        info "status page just enabled: restart the server to serve it ($0 stop $name && $0 start $name)"
    fi
}

# ── services: systemd (user units) or OpenRC (Alpine) ────────────────────
# `install NAME` picks the init system of the machine: systemd when it is
# running (/run/systemd/system), OpenRC when rc-service/openrc-run exist
# (Alpine), else none.  MEGACHAT_INIT=systemd|openrc|none forces it.

OPENRC_DIR="${MEGACHAT_OPENRC_DIR:-/etc/init.d}"
OPENRC_RUNLEVELS="${MEGACHAT_OPENRC_RUNLEVELS:-/etc/runlevels}"

init_system() {
    case "${MEGACHAT_INIT:-}" in
        systemd|openrc|none) echo "$MEGACHAT_INIT"; return 0 ;;
        "") ;;
        *) die "MEGACHAT_INIT must be systemd, openrc or none" ;;
    esac
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
        echo systemd
    elif command -v rc-service >/dev/null 2>&1 \
        && { command -v openrc-run >/dev/null 2>&1 || [ -x /sbin/openrc-run ]; }; then
        echo openrc
    else
        echo none
    fi
}

service_name() { echo "megachat-$1"; }

# Which kind of service is installed for NAME ("systemd", "openrc" or "").
service_kind() {
    if [ -f "$(unit_path "$1")" ] && command -v systemctl >/dev/null 2>&1; then
        echo systemd
    elif [ -f "$OPENRC_DIR/$(service_name "$1")" ]; then
        echo openrc
    fi
}

service_installed() { [ -n "$(service_kind "$1")" ]; }

service_active() {
    case "$(service_kind "$1")" in
        systemd) systemctl --user is-active --quiet "$(unit_name "$1")" ;;
        openrc) rc-service "$(service_name "$1")" status >/dev/null 2>&1 ;;
        *) return 1 ;;
    esac
}

service_start() {
    case "$(service_kind "$1")" in
        systemd) systemctl --user start "$(unit_name "$1")" ;;
        openrc) as_root rc-service "$(service_name "$1")" start ;;
    esac
}

service_stop() {
    case "$(service_kind "$1")" in
        systemd) systemctl --user stop "$(unit_name "$1")" ;;
        openrc) as_root rc-service "$(service_name "$1")" stop ;;
    esac
}

# One word for `status`: "systemd:enabled", "openrc:default", ... or "-".
service_state() {
    local kind state
    kind="$(service_kind "$1")"
    case "$kind" in
        systemd)
            state="$(systemctl --user is-enabled "$(unit_name "$1")" 2>/dev/null || true)"
            echo "systemd:${state:-installed}"
            ;;
        openrc)
            if [ -e "$OPENRC_RUNLEVELS/default/$(service_name "$1")" ]; then
                echo "openrc:enabled"
            else
                echo "openrc:installed"
            fi
            ;;
        *) echo "-" ;;
    esac
}

# Run a command as root (directly, or through doas / sudo).
as_root() {
    if [ "$(id -u)" = 0 ]; then
        "$@"
    elif command -v doas >/dev/null 2>&1; then
        doas "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        die "root needed for: $* (run this script as root, or install doas/sudo)"
    fi
}

# The user that owns the instance (not root when called through sudo/doas).
service_user() {
    if [ "$(id -u)" = 0 ] && [ -n "${SUDO_USER:-${DOAS_USER:-}}" ]; then
        echo "${SUDO_USER:-$DOAS_USER}"
    else
        id -un
    fi
}

# Stop a server started by hand before a service takes it over.
stop_manual_server() {
    local name="$1" dir="$2" pid
    if pid="$(server_pid "$dir")" && ! service_active "$name"; then
        info "stopping the copy started by hand (PID $pid) before handing it to the service"
        kill -TERM "$pid"
        local i
        for i in $(seq 1 30); do
            kill -0 "$pid" 2>/dev/null || break
            sleep 0.5
        done
    fi
}

# ── systemd ──

unit_name() { echo "megachat-$1.service"; }

unit_path() {
    echo "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$(unit_name "$1")"
}

render_unit() {
    local name="$1" dir="$2" args docker_bin container
    args="$(serve_args "$dir" | tr '\n' ' ')"
    docker_bin="$(command -v docker || echo /usr/bin/docker)"
    container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
    cat <<UNIT
# Generated by scripts/megachat_instance.sh for instance "$name".
# Regenerate with: $REPO_DIR/scripts/megachat_instance.sh install $name
[Unit]
Description=Megachat instance $name (WhatsApp + transcription)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=MEGACHAT_HOME=$dir
Environment=PYTHONUNBUFFERED=1
WorkingDirectory=$REPO_DIR
# Its WAHA container (restart: unless-stopped): make sure it is up.
Environment=MEGACHAT_INSTANCES_DIR=$INSTANCES_DIR
# Its WAHA container: create it if missing, start it (no-op when running).
ExecStartPre=-$(command -v bash || echo /bin/bash) $REPO_DIR/scripts/megachat_instance.sh up $name
ExecStart=$(python_bin) $REPO_DIR/megachat.py serve ${args% }
Restart=on-failure
RestartSec=10
TimeoutStopSec=30
KillSignal=SIGTERM

[Install]
WantedBy=default.target
UNIT
}

systemd_install() {
    local name="$1" dir="$2" path unit
    command -v systemctl >/dev/null 2>&1 || die "systemctl not found (on Alpine use: $0 openrc $name install)"
    path="$(unit_path "$name")"
    unit="$(unit_name "$name")"
    stop_manual_server "$name" "$dir"
    mkdir -p "$(dirname "$path")"
    render_unit "$name" "$dir" > "$path"
    systemctl --user daemon-reload
    systemctl --user enable "$unit"
    systemctl --user restart "$unit"
    echo "✅ $unit installed and started ($path)"
    echo "   Check everything with: $0 doctor $name"
    if command -v loginctl >/dev/null 2>&1 \
        && [ "$(loginctl show-user "${USER:-$(id -un)}" -p Linger --value 2>/dev/null)" != "yes" ]; then
        echo "⚠️  To start it at boot and keep it running after logout, run once:"
        echo "     sudo loginctl enable-linger ${USER:-$(id -un)}"
    fi
}

systemd_uninstall() {
    local name="$1" path unit
    path="$(unit_path "$name")"
    unit="$(unit_name "$name")"
    if [ ! -f "$path" ]; then
        info "$unit is not installed"
        return 0
    fi
    systemctl --user disable --now "$unit" || true
    rm -f "$path"
    systemctl --user daemon-reload
    echo "✅ $unit removed (the instance data is untouched)"
}

cmd_systemd() {
    local name="${1:-}" action="${2:-print}" dir
    dir="$(require_instance "$name")"
    case "$action" in
        install) systemd_install "$name" "$dir" ;;
        uninstall|remove) systemd_uninstall "$name" ;;
        status) systemctl --user status --no-pager "$(unit_name "$name")" ;;
        print) render_unit "$name" "$dir" ;;
        *) die "systemd: expected install, uninstall, status or print" ;;
    esac
}

# ── OpenRC (Alpine) ──

render_openrc() {
    local name="$1" dir="$2" args user group user_home container docker_bin
    args="$(serve_args "$dir" | tr '\n' ' ')"
    user="$(service_user)"
    group="$(id -gn "$user" 2>/dev/null)" || group="$user"
    user_home="$(getent passwd "$user" 2>/dev/null | cut -d: -f6)" || user_home=""
    user_home="${user_home:-$HOME}"
    container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
    docker_bin="$(command -v docker || echo /usr/bin/docker)"
    cat <<RC
#!/sbin/openrc-run
# Generated by scripts/megachat_instance.sh for instance "$name".
# Regenerate with: $REPO_DIR/scripts/megachat_instance.sh install $name

name="megachat-$name"
description="Megachat instance $name (WhatsApp + transcription)"

# supervise-daemon restarts the server if it dies.
supervisor=supervise-daemon
respawn_delay=10
respawn_max=0
retry="SIGTERM/30/SIGKILL/5"

command="/usr/bin/env"
command_args="MEGACHAT_HOME='$dir' HOME='$user_home' PYTHONUNBUFFERED=1 '$(python_bin)' '$REPO_DIR/megachat.py' serve ${args% }"
command_user="$user:$group"
directory="$REPO_DIR"
output_log="$dir/logs/service.log"
error_log="$dir/logs/service.log"

depend() {
    need net
    after docker
}

start_pre() {
    # Its WAHA container: create it if missing, start it (no-op when running).
    if [ -x "$docker_bin" ]; then
        MEGACHAT_INSTANCES_DIR='$INSTANCES_DIR' "$(command -v bash || echo /bin/bash)" \\
            '$REPO_DIR/scripts/megachat_instance.sh' up '$name' >/dev/null 2>&1 \\
            || ewarn "could not start the WAHA container $container (see: megachat_instance.sh doctor $name)"
    fi
    checkpath --directory --owner "$user:$group" "$dir/logs"
    return 0
}
RC
}

openrc_install() {
    local name="$1" dir="$2" svc tmp
    command -v rc-service >/dev/null 2>&1 || die "rc-service not found: is this an OpenRC system?"
    svc="$(service_name "$name")"
    stop_manual_server "$name" "$dir"
    tmp="$dir/run/$svc.openrc"
    render_openrc "$name" "$dir" > "$tmp"
    as_root install -m 0755 "$tmp" "$OPENRC_DIR/$svc"
    rm -f "$tmp"
    as_root rc-update add "$svc" default
    as_root rc-service "$svc" restart
    echo "✅ $svc installed in $OPENRC_DIR, enabled at boot (runlevel default) and started"
    echo "   Check everything with: $0 doctor $name"
    echo "   Output of the service: $dir/logs/service.log"
}

openrc_uninstall() {
    local name="$1" svc
    svc="$(service_name "$name")"
    if [ ! -f "$OPENRC_DIR/$svc" ]; then
        info "$svc is not installed"
        return 0
    fi
    as_root rc-service "$svc" stop || true
    as_root rc-update del "$svc" default || true
    as_root rm -f "$OPENRC_DIR/$svc"
    echo "✅ $svc removed (the instance data is untouched)"
}

cmd_openrc() {
    local name="${1:-}" action="${2:-print}" dir
    dir="$(require_instance "$name")"
    case "$action" in
        install) openrc_install "$name" "$dir" ;;
        uninstall|remove) openrc_uninstall "$name" ;;
        status) rc-service "$(service_name "$name")" status ;;
        print) render_openrc "$name" "$dir" ;;
        *) die "openrc: expected install, uninstall, status or print" ;;
    esac
}

# ── install / uninstall (whatever the machine uses) ──

cmd_install() {
    local name="${1:-}" dir kind
    dir="$(require_instance "$name")"
    kind="$(init_system)"
    case "$kind" in
        systemd) systemd_install "$name" "$dir" ;;
        openrc) openrc_install "$name" "$dir" ;;
        *)
            die "no systemd or OpenRC on this machine: start it with '$0 start $name'" \
                "(or force one with MEGACHAT_INIT=systemd|openrc)"
            ;;
    esac
}

cmd_uninstall() {
    local name="${1:-}" dir
    dir="$(require_instance "$name")"
    case "$(service_kind "$name")" in
        systemd) systemd_uninstall "$name" ;;
        openrc) openrc_uninstall "$name" ;;
        *) info "no service installed for '$name'" ;;
    esac
}

# Remove whatever service NAME has (used by destroy).
service_remove() {
    case "$(service_kind "$1")" in
        systemd) systemd_uninstall "$1" ;;
        openrc) openrc_uninstall "$1" ;;
    esac
}

# ── doctor ───────────────────────────────────────────────────────────────

DOCTOR_FAILS=0
ok() { echo "  ✅ $*"; }
bad() { echo "  ❌ $*"; DOCTOR_FAILS=$((DOCTOR_FAILS + 1)); }
warn() { echo "  ⚠️  $*"; }

cmd_doctor() {
    local name="${1:-}" dir
    dir="$(require_instance "$name")"
    local container port url key session webhook
    container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
    port="$(env_value "$dir/.env" WHATSAPP_API_PORT)"
    url="$(env_value "$dir/.env" WHATSAPP_API_URL)"
    url="${url:-http://127.0.0.1:$port}"
    key="$(env_value "$dir/.env" WAHA_API_KEY)"
    session="$(env_value "$dir/.env" WHATSAPP_SESSION_NAME)"
    session="${session:-default}"
    webhook="$(env_value "$dir/.env" CLIENT_WEBHOOK_PORT)"
    local probe=("$(python_bin)" "$REPO_DIR/scripts/instance_config.py" probe)
    echo "Instance '$name' ($dir)"

    echo "Docker / WAHA container ($container)"
    local wstate=""
    if ! command -v docker >/dev/null 2>&1; then
        bad "docker not found (Alpine: apk add docker docker-cli-compose)"
    elif ! docker info >/dev/null 2>&1; then
        bad "docker daemon not reachable by $(id -un) (running? in the docker group?)"
    else
        ok "docker daemon reachable"
        docker compose version >/dev/null 2>&1 \
            || bad "'docker compose' missing (Alpine: apk add docker-cli-compose)"
        wstate="$(docker inspect -f '{{.State.Status}}' "$container" 2>/dev/null)" || wstate=""
        case "$wstate" in
            running) ok "container running" ;;
            "") bad "container does not exist → $0 up $name" ;;
            *) bad "container is '$wstate' → $0 up $name, then: $0 logs $name waha" ;;
        esac
        if [ -n "$wstate" ]; then
            local mapping
            mapping="$(docker port "$container" 3000/tcp 2>/dev/null | head -n 1)" || mapping=""
            if [ -z "$mapping" ]; then
                bad "port 3000 of the container is not published"
            elif [ "${mapping##*:}" != "$port" ]; then
                bad "container publishes $mapping but WHATSAPP_API_PORT=$port: recreate it with $0 up $name"
            else
                ok "published on $mapping"
            fi
            local cenv ckey cport
            cenv="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$container" 2>/dev/null)" \
                || cenv=""
            ckey="$(printf '%s\n' "$cenv" | sed -n 's/^WAHA_API_KEY=//p' | head -n 1)"
            cport="$(printf '%s\n' "$cenv" | sed -n 's/^WHATSAPP_API_PORT=//p' | tail -n 1)"
            if [ -n "$cport" ] && [ "$cport" != 3000 ]; then
                bad "WAHA listens on port $cport inside the container but Docker forwards to 3000: git pull, then $0 up $name"
            fi
            if [ -n "$ckey" ] && [ "$ckey" != "$key" ]; then
                bad "the container has a different WAHA_API_KEY than $dir/.env: recreate it with $0 up $name"
            fi
            if [ "$wstate" = running ]; then
                local errors
                errors="$(docker logs --tail 200 "$container" 2>&1 | grep -iE 'error|fatal' | tail -n 3)" || errors=""
                [ -z "$errors" ] || { warn "recent errors in the WAHA log:"; echo "$errors" | sed 's/^/       /'; }
            fi
        fi
    fi

    echo "WAHA API ($url)"
    local proxy="${HTTP_PROXY:-${http_proxy:-}}"
    if [ -n "$proxy" ]; then
        case ",${NO_PROXY:-${no_proxy:-}}," in
            *,127.0.0.1,*|*,localhost,*) ;;
            *) warn "http_proxy is set ($proxy) without 127.0.0.1 in no_proxy: local requests may go to the proxy" ;;
        esac
    fi
    local answer code body linked=0
    answer="$("${probe[@]}" "$url/api/sessions/$session" "$key")" || answer="0 probe failed"
    code="${answer%% *}"
    body="${answer#* }"
    case "$code" in
        200)
            ok "session '$session': $body"
            case "$body" in *WORKING*) linked=1 ;; esac
            ;;
        404) warn "session '$session' does not exist yet → $0 link $name (or the Avvia button on the page)" ;;
        401|403) bad "WAHA rejects the API key (HTTP $code): recreate the container with $0 up $name" ;;
        0)
            case "$body" in
                *"reset by peer"*|*"Connection reset"*)
                    bad "no answer: $body — nothing listens on port 3000 inside the container (WAHA still starting? see $0 logs $name waha)" ;;
                *) bad "no answer: $body" ;;
            esac
            ;;
        *) bad "HTTP $code: $body" ;;
    esac

    echo "Megachat server"
    local pid
    if pid="$(server_pid "$dir")"; then
        ok "running (PID $pid), service: $(service_state "$name")"
    else
        bad "not running → $0 start $name (service: $(service_state "$name"))"
    fi
    if [ -n "$webhook" ]; then
        answer="$("${probe[@]}" "http://127.0.0.1:$webhook/webhook" "")" || answer="0 probe failed"
        if [ "${answer%% *}" != 0 ]; then
            ok "webhook listening on port $webhook"
        elif [ "$linked" = 1 ] && pid="$(server_pid "$dir")"; then
            bad "webhook port $webhook not listening although WhatsApp is linked: messages will not arrive (the server reconnects within ~1 min; else restart it)"
        else
            warn "webhook port $webhook not listening yet (normal until WhatsApp is linked)"
        fi
    fi
    local enabled sport token
    read -r enabled sport token <<< "$(status_page_info "$dir")"
    if [ "$enabled" = 1 ]; then
        answer="$("${probe[@]}" "http://127.0.0.1:$sport/$token/status.json" "")" || answer="0 probe failed"
        if [ "${answer%% *}" = 200 ]; then
            ok "status page answers on port $sport: ${answer#* }"
        else
            bad "status page on port $sport: ${answer#* } (restart the server; see logs)"
        fi
    else
        warn "status page disabled ($0 url $name enables it)"
    fi
    local log="$dir/logs/signal-tui.log"
    if [ -f "$log" ]; then
        local recent
        recent="$(grep -E ' (ERROR|CRITICAL) ' "$log" | tail -n 3)" || recent=""
        [ -z "$recent" ] || { warn "last errors in $log:"; echo "$recent" | sed 's/^/       /'; }
    fi

    echo
    if [ "$DOCTOR_FAILS" = 0 ]; then
        echo "✅ no problems found"
    else
        echo "❌ $DOCTOR_FAILS problem(s) found"
        return 1
    fi
}

# ── destroy ──────────────────────────────────────────────────────────────

# rm -rf that also handles files the WAHA container created as root.
remove_tree() {
    local target="$1" image
    [ -e "$target" ] || return 0
    rm -rf "$target" 2>/dev/null && return 0
    image="$(env_value "$(dirname "$target")/.env" WAHA_IMAGE 2>/dev/null || true)"
    if command -v docker >/dev/null 2>&1 && [ -n "$image" ]; then
        docker run --rm -v "$target:/purge" --entrypoint sh "$image" \
            -c 'rm -rf /purge/* /purge/.[!.]* /purge/..?*' >/dev/null 2>&1 || true
    fi
    rm -rf "$target" 2>/dev/null || die "cannot delete $target (try: sudo rm -rf '$target')"
}

cmd_destroy() {
    parse_docker_opts "$@"
    set -- "${REST_ARGS[@]+"${REST_ARGS[@]}"}"
    local name="${1:-}" purge=0 yes=0 dir
    shift || true
    while [ $# -gt 0 ]; do
        case "$1" in
            --purge) purge=1 ;;
            --yes|-y) yes=1 ;;
            *) die "unknown option: $1" ;;
        esac
        shift
    done
    dir="$(instance_dir "$name")"
    [ -f "$dir/.env" ] || die "instance '$name' not found ($dir)"

    if [ "$yes" != 1 ]; then
        if [ "$purge" = 1 ]; then
            echo "This deletes EVERYTHING of '$name' ($dir), .env included."
        else
            echo "This stops '$name', removes its WAHA container and systemd unit and"
            echo "deletes its messages, media, WhatsApp session and logs."
            echo "Kept: $dir/.env and config.json (\"create $name\" rebuilds it)."
        fi
        local answer
        read -r -p "Type the instance name to confirm: " answer
        [ "$answer" = "$name" ] || die "aborted"
    fi

    if [ ! -e "$dir/.destroyed" ]; then
        service_remove "$name"
        local pid
        if pid="$(server_pid "$dir")"; then
            kill -TERM "$pid"
            local i
            for i in $(seq 1 30); do
                kill -0 "$pid" 2>/dev/null || break
                sleep 0.5
            done
            kill -0 "$pid" 2>/dev/null && kill -KILL "$pid"
            echo "✅ server stopped"
        fi
        if command -v docker >/dev/null 2>&1; then
            compose "$dir" down --remove-orphans \
                && echo "✅ WAHA container removed"
            local container
            container="$(env_value "$dir/.env" WAHA_CONTAINER_NAME)"
            docker rm -f "$container" >/dev/null 2>&1 || true
        else
            info "docker not found: no WAHA container to remove"
        fi
    fi

    if [ "$purge" = 1 ]; then
        remove_tree "$dir/whatsapp-data"
        remove_tree "$dir"
        echo "✅ instance '$name' purged"
        return 0
    fi
    local entry
    for entry in "$dir"/* "$dir"/.[!.]* "$dir"/..?*; do
        [ -e "$entry" ] || continue
        case "$(basename "$entry")" in
            .env|config.json) ;;
            *) remove_tree "$entry" ;;
        esac
    done
    date -u +%Y-%m-%dT%H:%M:%SZ > "$dir/.destroyed"
    echo "✅ instance '$name' destroyed; kept $dir/.env and config.json"
    echo "   Rebuild it with: $0 create $name"
}

# ── help ─────────────────────────────────────────────────────────────────
# Help texts: quoted heredocs (nothing is expanded), @S@ = this script.

help_text() {
    case "$1" in
        overview)
            cat <<'TXT'
megachat_instance.sh — istanze Megachat aggiuntive e isolate sulla stessa
macchina (per esempio una per ogni amico che vuole la trascrizione automatica
delle note vocali WhatsApp, visibile direttamente nell'app sul telefono).

Ogni istanza ha la sua cartella (instances/NOME/) con config.json, .env,
database, media, log, lock e socket, più il suo container WhatsApp (WAHA) con
porte e sessione proprie. La tua istanza principale (avviata senza
MEGACHAT_HOME, con launcher.py, alias o `megachat serve`) non viene toccata.

USO
  @S@ COMANDO [NOME] [OPZIONI]
  @S@ help COMANDO        guida dettagliata di un comando
  @S@ help all            guida completa (tutti i comandi + risoluzione problemi)
  @S@ COMANDO --help      come `help COMANDO`

COMANDI
  Creazione e rimozione
    create NOME [--offset N] [--no-docker-limits] [--compose-file FILE ...]
                                      crea (o ricrea dopo destroy) un'istanza
    destroy NOME [--purge] [--yes]    spegne e cancella, tenendo .env e config.json
    list                              elenca le istanze con le porte (WAHA, pagina QR)

  Avvio automatico (servizio: systemd, oppure OpenRC su Alpine)
    install NOME                      installa e avvia il servizio: parte al boot
                                      e riparte da solo (sceglie systemd/OpenRC)
    uninstall NOME                    rimuove il servizio (i dati restano)
    openrc NOME [install|uninstall|status|print]    solo OpenRC (Alpine)
    systemd NOME [install|uninstall|status|print]   solo systemd

  WhatsApp (container WAHA dell'istanza)
    up NOME [--no-docker-limits] [--compose-file FILE ...]
                                      avvia il container
    down NOME                         ferma e rimuove il container (sessione conservata)
    link NOME                         collega WhatsApp con il QR code nel terminale

  Server Megachat dell'istanza
    start NOME                        avvia il server in background (o via servizio)
    stop NOME                         ferma il server
    serve NOME [ARGS]                 avvia il server in primo piano (debug)
    status [NOME]                     stato di server, WAHA e servizio
    doctor NOME                       diagnosi passo passo (container, porte, API
                                      WAHA, chiave, webhook, pagina di stato, log)
    logs NOME [server|waha]           segue i log

  Accesso
    url NOME                          link della pagina di stato/QR da mandare all'amico
    env NOME                          stampa `export MEGACHAT_HOME=...`

AVVIO RAPIDO (un amico)
  1. Crea l'istanza (cartella instances/amico/, porte libere, chiavi casuali):
       @S@ create amico
  2. Inserisci la chiave del servizio di trascrizione (OpenAI, OpenRouter o
     Gemini) in instances/amico/.env, es. OPENAI_API_KEY=sk-...
  3. Avvia il suo WhatsApp (container WAHA):
       @S@ up amico
  4. Avvia il server, sempre attivo anche dopo un reboot:
       @S@ install amico
     (oppure, senza systemd: @S@ start amico)
  5. Manda all'amico il link della sua pagina di stato: lì vede se è
     collegato e, se serve, il QR (o un codice) per collegare WhatsApp:
       @S@ url amico
  Spegnere tutto: @S@ destroy amico (tiene .env e config.json;
  `create amico` la ricrea uguale).

Più amici: ripeti con nomi diversi (amico1, amico2, ...): ogni istanza riceve
automaticamente porte libere.
TXT
            ;;
        create)
            cat <<'TXT'
create NOME [--offset N] [--no-docker-limits] [--compose-file FILE ...]

  Crea instances/NOME/ con:
    .env          porte, chiave API di WAHA (casuale), provider di trascrizione
                  (da completare: OPENAI_API_KEY=..., o OPENROUTER_/GEMINI_),
                  SIGNAL_ENABLED=0 (solo WhatsApp), lingua it
    config.json   trascrizione automatica attiva per tutte le chat WhatsApp,
                  web UI spenta, pagina di stato attiva con token casuale
    data/ logs/ run/ whatsapp-data/

  NOME: lettere minuscole, cifre, - e _ (max 31 caratteri), es. amico1.

  Porte: valori di default spostati dell'offset N (di default il primo libero
  da 1 in su; --offset N per sceglierlo, 1..99):
    WAHA (API WhatsApp)   3005 + 10·N      es. N=1 → 3015
    webhook               8088 + 10·N      es. N=1 → 8098
    pagina di stato       4300 + N         es. N=1 → 4301
    web UI (spenta)       4242 + N
    signal-cli (spento)   8080 + 10·N
    download              10042 + N
  La tua istanza principale usa N=0 (3005, 8088, 4242, ...), quindi non
  collide. Lo script non verifica se altri programmi usano già quelle porte:
  se `up` o `start` falliscono per "address already in use", distruggi e
  ricrea con un altro --offset.

  Ricreare: se NOME è stato distrutto con `destroy` (senza --purge), `create
  NOME` lo ricostruisce riusando .env e config.json esistenti: stesse porte,
  chiavi, impostazioni e link della pagina di stato. --offset non è ammesso in
  questo caso (le porte si cambiano modificando il .env).

  Container WAHA (vedi `help docker`):
    --no-docker-limits    scrive WAHA_DOCKER_LIMITS=0 nel .env: niente limiti
                          CPU/RAM di docker-compose.resources.yml
    --docker-limits       forza i limiti anche se il .env principale li toglie
    --compose-file FILE   file compose aggiuntivo (ripetibile), salvato in
                          WAHA_COMPOSE_FILES con percorso assoluto
  Senza queste opzioni create copia WAHA_DOCKER_LIMITS=0 e WAHA_COMPOSE_FILES
  dal .env principale del progetto, se ci sono: un host che ne ha bisogno
  (es. LXC) funziona subito anche per le istanze degli amici.

  Esempi:
    @S@ create amico
    @S@ create mario --offset 5
    @S@ create luca --no-docker-limits
    @S@ create anna --compose-file docker-compose.local.yml
TXT
            ;;
        destroy)
            cat <<'TXT'
destroy NOME [--purge] [--yes]

  Spegne e cancella un'istanza:
    1. rimuove il servizio systemd (se installato)
    2. ferma il server
    3. ferma e cancella il container WAHA (docker compose down)
    4. cancella dati, media, sessione WhatsApp, log, socket
  CONSERVA instances/NOME/.env e config.json: `create NOME` la ricrea con la
  stessa configurazione (WhatsApp va però ricollegato con il QR).
  Finché non è ricreata, l'istanza compare in `status` come "destroyed" e i
  suoi numeri di porta restano riservati.

  Opzioni:
    --purge   cancella TUTTA la cartella, .env compreso (non si può ricreare)
    --yes     non chiede conferma (di default bisogna digitare il nome)

  Se docker non è raggiungibile, il container non viene rimosso: lo script
  te lo segnala e puoi farlo dopo con `docker rm -f megachat-NOME-whatsapp`.
  I file creati come root dal container vengono cancellati tramite docker;
  se anche questo fallisce lo script suggerisce il `sudo rm -rf` da usare.

  Esempi:
    @S@ destroy amico               # poi: @S@ create amico
    @S@ destroy amico --purge --yes
TXT
            ;;
        list)
            cat <<'TXT'
list

  Stampa le istanze (anche quelle distrutte con config conservata) con
  offset, porta WAHA e porta della pagina di stato/QR, es.:
    amico1           offset 1   WAHA 3015   status/QR page port 4301
  Le istanze stanno in $MEGACHAT_INSTANCES_DIR (default: <repo>/instances,
  esclusa da git).
TXT
            ;;
        up)
            cat <<'TXT'
up NOME [--no-docker-limits] [--compose-file FILE ...]

  Avvia (o aggiorna, se cambi i file compose) il container WAHA
  dell'istanza, chiamato megachat-NOME-whatsapp:
    docker compose -p megachat-NOME --env-file instances/NOME/.env \
      -f docker-compose.yml [-f docker-compose.resources.yml] [-f ...] up -d
  Stampa i file compose usati. Il container ha `restart: unless-stopped`,
  quindi riparte da solo con docker. La sessione WhatsApp sta in
  instances/NOME/whatsapp-data/.

  Le opzioni valgono solo per questa esecuzione (per renderle permanenti
  usa il .env dell'istanza, vedi `help docker`):
    --no-docker-limits    senza docker-compose.resources.yml
    --compose-file FILE   aggiunge FILE in fondo (ripetibile)
TXT
            ;;
        docker)
            cat <<'TXT'
FILE DOCKER COMPOSE E LIMITI CPU/RAM (container WAHA)

  Ogni comando che usa docker compose (up, down, destroy) combina, in ordine:
    1. docker-compose.yml               il servizio WAHA
    2. docker-compose.resources.yml     limiti 1.5 CPU / 4 GB RAM, saltato se
                                        WAHA_DOCKER_LIMITS=0 o --no-docker-limits
    3. WAHA_COMPOSE_FILES               file tuoi, separati da ":", relativi
                                        alla cartella del progetto o assoluti
    4. --compose-file FILE              file aggiunti solo per questa volta
  I file successivi vincono sui precedenti (stesse regole di launcher.py).

  Impostazioni permanenti, in instances/NOME/.env:
    WAHA_DOCKER_LIMITS=0
    WAHA_COMPOSE_FILES=docker-compose.local.yml:/etc/megachat/waha.yml
  Dopo averle cambiate: @S@ up NOME (ricrea il container con la nuova
  configurazione; la sessione WhatsApp resta).

  Quando togliere i limiti: se il container non parte con
    "error setting cgroup config ... memory.max: no such file or directory"
  (Proxmox/LXC senza nesting): WAHA_DOCKER_LIMITS=0.

  Limiti diversi invece di nessuno: un file con i tuoi valori, es.
    # docker-compose.local.yml
    services:
      whatsapp:
        deploy:
          resources:
            limits:
              cpus: '1'
              memory: 1024M
  e WAHA_COMPOSE_FILES=docker-compose.local.yml (oppure --compose-file).

  Se il .env principale del progetto ha WAHA_DOCKER_LIMITS=0 o
  WAHA_COMPOSE_FILES, `create` li copia nella nuova istanza.
TXT
            ;;
        down)
            cat <<'TXT'
down NOME

  Ferma e rimuove il container WAHA (docker compose down). La sessione
  WhatsApp in instances/NOME/whatsapp-data/ resta: con `up` riparte già
  collegata. Per cancellare anche la sessione usa `destroy`.
TXT
            ;;
        link)
            cat <<'TXT'
link NOME

  Collega WhatsApp dal terminale: mostra il QR code (esegue link_whatsapp.py
  con MEGACHAT_HOME=instances/NOME). Sul telefono dell'amico: WhatsApp →
  Impostazioni → Dispositivi collegati → Collega un dispositivo.
  Richiede il container avviato (`up NOME`).

  Se l'amico non è lì con te, non serve: manda il link di `url NOME`; dalla
  pagina di stato può inquadrare il QR, oppure chiedere un codice da inserire
  con "Collega con il numero di telefono" (utile se apre la pagina dal
  telefono stesso).
TXT
            ;;
        start)
            cat <<'TXT'
start NOME [ARGS]

  Avvia il server dell'istanza in background (`megachat.py serve --no-web`),
  log in instances/NOME/logs/, e stampa porta e link della pagina di
  stato/QR da mandare all'amico. Se è installato il servizio (`install NOME`)
  lo avvia tramite systemd o OpenRC (in quel caso niente ARGS: cambia
  config.json e rilancia `install NOME`).

  ARGS sono passati a `megachat serve`, es. --debug o --web-port 4250 (con
  --web-* la web UI completa viene avviata).
TXT
            ;;
        stop)
            cat <<'TXT'
stop NOME

  Ferma il server (SIGTERM, attende fino a 15 s). Tramite systemd/OpenRC se
  è installato il servizio. Il container WAHA resta acceso: per fermarlo
  anche lui usa `down NOME`.
TXT
            ;;
        serve)
            cat <<'TXT'
serve NOME [ARGS]

  Come start ma in primo piano (Ctrl+C per fermare): utile per vedere subito
  gli errori. ARGS come per start (es. --debug).
TXT
            ;;
        status)
            cat <<'TXT'
status [NOME]

  Per ogni istanza (o solo NOME): server avviato o no (PID), container WAHA
  e porta, stato del servizio systemd. Per lo stato di WhatsApp (collegato,
  in attesa di QR, ...) apri la pagina di `url NOME`.
TXT
            ;;
        doctor)
            cat <<'TXT'
doctor NOME

  Controlla l'istanza passo passo e dice cosa fare per ogni problema:
    - docker installato e raggiungibile, `docker compose` presente
    - container WAHA esistente e acceso, porta pubblicata uguale a
      WHATSAPP_API_PORT, stessa WAHA_API_KEY del .env, errori recenti nel log
    - API WAHA: risponde? la chiave è accettata? stato della sessione
    - http_proxy che potrebbe deviare le richieste locali
    - server Megachat avviato, porta webhook in ascolto, pagina di stato
    - ultimi errori in logs/signal-tui.log
  Esce con codice 1 se trova problemi. Usalo quando la pagina di stato dice
  "WAHA non raggiungibile" o le note vocali non vengono trascritte.
TXT
            ;;
        logs)
            cat <<'TXT'
logs NOME [server|waha]

  server (default)  tail -F di instances/NOME/logs/signal-tui.log
  waha              log del container WAHA (docker compose logs -f)
  Altri file in instances/NOME/logs/: megachat-serve.log (output del
  processo, errori di avvio).
TXT
            ;;
        url)
            cat <<'TXT'
url NOME

  Stampa il link della pagina di stato dell'istanza:
    http://HOST:PORTA/TOKEN/
  La pagina (servita dal server dell'istanza, senza web UI) mostra se WhatsApp
  è collegato e con quale account, il QR code quando serve collegarlo, un
  modulo per ottenere un codice di collegamento con il numero di telefono, un
  pulsante per avviare/riavviare la sessione, e lo stato della trascrizione.
  Si aggiorna da sola.

  HOST è il primo IP della macchina; imposta MEGACHAT_PUBLIC_HOST per usare un
  nome diverso (es. il nome Tailscale o il dominio del reverse proxy):
    MEGACHAT_PUBLIC_HOST=server.example.org @S@ url amico

  Il TOKEN nel percorso è l'unica protezione: chiunque abbia il link vede la
  pagina. Esponila su VPN/tunnel o dietro un reverse proxy HTTPS, non in chiaro
  su internet. Per cambiarlo modifica "status_page" → "token" in config.json e
  riavvia il server. Per le istanze create prima di questa funzione, `url`
  aggiunge la sezione a config.json: poi riavvia il server (stop + start).
TXT
            ;;
        install|uninstall|systemd|openrc)
            cat <<'TXT'
install NOME
uninstall NOME
openrc NOME [install|uninstall|status|print]
systemd NOME [install|uninstall|status|print]

  `install NOME` fa partire il server dell'istanza al boot e lo riavvia se si
  blocca, con il sistema di init della macchina:
    systemd   se è attivo (/run/systemd/system): servizio *utente*
              ~/.config/systemd/user/megachat-NOME.service
    OpenRC    su Alpine (rc-service / openrc-run): servizio di sistema
              /etc/init.d/megachat-NOME, nel runlevel default, gestito da
              supervise-daemon; il server gira comunque con il TUO utente
              (command_user), non come root
  Se il server era stato avviato a mano viene fermato e preso in carico dal
  servizio. Il servizio crea/avvia il container WAHA (`up`) e poi il server.
  Da quel momento start/stop/status passano dal servizio.
  `uninstall NOME` lo ferma, lo disabilita e lo cancella (dati intatti).
  MEGACHAT_INIT=systemd|openrc forza la scelta.

  openrc / systemd NOME AZIONE agiscono su quel sistema soltanto:
    install | uninstall | status | print (default: stampa il file del servizio)

  OpenRC (Alpine):
    - serve root per scrivere in /etc/init.d e usare rc-update/rc-service:
      lancia lo script come root, oppure con doas/sudo installati lo script
      li usa da solo
    - prerequisiti: apk add bash python3 docker docker-cli-compose
                    rc-update add docker default
    - output del servizio: instances/NOME/logs/service.log
    - comandi diretti: rc-service megachat-NOME start|stop|restart|status
                       rc-update show default

  systemd:
    - perché parta al boot senza che tu faccia login, una volta sola:
        sudo loginctl enable-linger $USER
    - il tuo utente deve poter usare docker (gruppo docker)
    - log anche in: journalctl --user -u megachat-NOME

  Dopo aver cambiato config.json (es. web UI) rilancia `install NOME`.
TXT
            ;;
        env)
            cat <<'TXT'
env NOME

  Stampa `export MEGACHAT_HOME=...`: per usare gli altri strumenti del
  progetto (TUI, link_whatsapp.py, script) sull'istanza invece che sulla tua:
    eval "$(@S@ env amico)"
    python3 megachat.py          # TUI collegata al server di "amico"
TXT
            ;;
        troubleshooting)
            cat <<'TXT'
RISOLUZIONE PROBLEMI

  La pagina di stato dice "WAHA non raggiungibile"
    → @S@ doctor NOME (dice cosa non va), poi di solito @S@ up NOME;
      log del container: @S@ logs NOME waha
  Il server non parte
    → instances/NOME/logs/megachat-serve.log e signal-tui.log;
      @S@ serve NOME --debug per vederlo in primo piano
  "address already in use"
    → un altro programma usa una porta dell'istanza: @S@ destroy NOME --purge
      e @S@ create NOME --offset N con un N diverso (oppure cambia le porte
      nel .env e in config.json)
  Le note vocali non vengono trascritte
    → la pagina di stato deve dire "Chiave API: configurata" e "Automatica:
      attiva"; nella chat scrivi !transcription per vedere lo stato; dopo
      aver modificato .env riavvia il server (stop + start)
  Il QR non si collega / è scaduto
    → la pagina lo rinnova da sola; se la sessione è "in errore" usa il
      pulsante Riavvia; in ultima istanza: destroy + create + up
  L'amico vuole spegnere la trascrizione in una chat
    → in quella chat, da WhatsApp: !transcription off (on per riattivarla);
      !transcription global off per tutte le chat; !t in risposta a una nota
      vocale per trascriverla al volo

FILE DI UN'ISTANZA (instances/NOME/)
  .env            porte, chiavi (WAHA, provider AI) — letto da server e WAHA
  config.json     impostazioni (trascrizione, pagina di stato, web UI)
  data/           messages.db, media WhatsApp
  whatsapp-data/  sessione WAHA (il collegamento WhatsApp)
  logs/           log del server
  run/            lock e socket del server

VARIABILI D'AMBIENTE DELLO SCRIPT
  MEGACHAT_INSTANCES_DIR   dove stanno le istanze (default <repo>/instances)
  MEGACHAT_PUBLIC_HOST     host mostrato da `url`
  PYTHON                   interprete da usare (default .venv/bin/python o python3)
TXT
            ;;
        *) return 1 ;;
    esac
}

HELP_TOPICS="create destroy list install up down docker link start stop serve status doctor logs url env"

usage() {
    local topic="${1:-overview}" code="${2:-0}" t
    local self="$0"
    if [ "$topic" = "all" ]; then
        {
            help_text overview
            for t in $HELP_TOPICS troubleshooting; do
                echo
                echo "────────────────────────────────────────────────────────────────────"
                help_text "$t"
            done
        } | sed "s|@S@|$self|g"
    elif help_text "$topic" > /dev/null 2>&1; then
        help_text "$topic" | sed "s|@S@|$self|g"
    else
        echo "Nessuna guida per '$topic'. Argomenti: $HELP_TOPICS troubleshooting all" >&2
        code=1
    fi
    exit "$code"
}

main() {
    local cmd="${1:-}"
    shift || true
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help) usage "$cmd" 0 ;;
        esac
    done
    case "$cmd" in
        create) cmd_create "$@" ;;
        up) cmd_up "$@" ;;
        down) cmd_down "$@" ;;
        destroy) cmd_destroy "$@" ;;
        link) cmd_link "$@" ;;
        serve) cmd_serve "$@" ;;
        start) cmd_start "$@" ;;
        stop) cmd_stop "$@" ;;
        status) cmd_status "$@" ;;
        list) cmd_list "$@" ;;
        logs) cmd_logs "$@" ;;
        env) cmd_env "$@" ;;
        url) cmd_url "$@" ;;
        doctor) cmd_doctor "$@" ;;
        systemd) cmd_systemd "$@" ;;
        openrc) cmd_openrc "$@" ;;
        install) cmd_install "$@" ;;
        uninstall) cmd_uninstall "$@" ;;
        help) usage "${1:-overview}" 0 ;;
        -h|--help|"") usage overview 0 ;;
        *) echo "❌ comando sconosciuto: $cmd" >&2; echo >&2; usage overview 1 ;;
    esac
}

main "$@"
