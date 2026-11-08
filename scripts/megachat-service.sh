#!/usr/bin/env bash
#
# megachat-service.sh — install/uninstall megachatd as a system service.
#
# Supports systemd (unit in /etc/systemd/system) and OpenRC (init script in
# /etc/init.d, for Alpine).  The service runs as the owning user so the data
# dirs (signal-cli, whatsmeow, Telethon session) and HOME are reused.
#
# Usage:
#   scripts/megachat-service.sh [--dry-run] [--root DIR] install ...
#   scripts/megachat-service.sh [--root DIR] uninstall ...
#   scripts/megachat-service.sh [--root DIR] status ...
#
# See "scripts/megachat-service.sh --help" for the subcommand options.
#
set -euo pipefail

PROG=${0##*/}

CMD=""
DRY_RUN=0
ROOT=""
INIT=""
NAME="megachat"
SERVICE_USER=""
PROJECT_DIR=""
BIN_PATH=""
PURGE_LOGS=0
EXTRA_ARGS=()

UNIT_PATH="/etc/systemd/system/${NAME}.service"
INIT_PATH="/etc/init.d/${NAME}"
LOG_PATH="/var/log/${NAME}.log"
HOME_DIR=""
GROUP=""
SOCKET=""
LOG_FILE_PATH=""

# ─── Helpers ──────────────────────────────────────────────────────────────────

say()  { printf '%s\n' "$*"; }
err()  { printf '%s: error: %s\n' "$PROG" "$*" >&2; }
die()  { err "$*"; exit 1; }

usage() {
    cat <<'EOF'
Install/uninstall megachatd as a system service (systemd or OpenRC).

Usage:
  megachat-service.sh install   [--user USER] [--project-dir DIR] [--bin PATH]
                                [--name NAME] [--init systemd|openrc]
                                [--extra ARG]... [-- EXTRA ARG...]
  megachat-service.sh uninstall [--name NAME] [--init systemd|openrc]
                                [--purge-logs]
  megachat-service.sh status    [--name NAME] [--init systemd|openrc]

Global options:
  --dry-run        Print what would be written and run; change nothing.
  --root DIR       Prefix every written path with DIR (for tests) and never
                   call systemctl / rc-service / rc-update.
  --init INIT      Force the init system: "systemd" or "openrc".  Otherwise
                   detected from /run/systemd/system or openrc-run.
  --name NAME      Service name (default: megachat).
  -h, --help       Show this help.

Install options:
  --user USER      User to run the service as (default: $SUDO_USER under
                   sudo, else the invoking user, else the owner of the project
                   dir).  root is refused.
  --project-dir DIR
                   Directory with config.json (default: the repo holding this
                   script).
  --bin PATH       megachatd binary (default: <project>/megachatd/bin/megachatd,
                   else megachatd from PATH).
  --extra ARG      Extra argument appended to `megachatd serve` (repeatable).
                   `--` passes every remaining argument through as-is.

Examples:
  sudo ./scripts/megachat-service.sh install
  sudo ./scripts/megachat-service.sh install --user alice --extra --web-port \
       --extra 8080
  sudo ./scripts/megachat-service.sh uninstall --purge-logs
  ./scripts/megachat-service.sh status
EOF
}

# Prefix a path with --root (empty root = the real path).
prefixed() {
    if [ -n "$ROOT" ]; then
        printf '%s%s' "$ROOT" "$1"
    else
        printf '%s' "$1"
    fi
}

require_root() {
    if [ "$DRY_RUN" -eq 1 ] || [ -n "$ROOT" ]; then
        return 0
    fi
    if [ "$(id -u)" -ne 0 ]; then
        die "must be run as root (use sudo), or pass --dry-run/--root"
    fi
}

resolve_init() {
    if [ -n "$INIT" ]; then
        case "$INIT" in
            systemd|openrc) ;;
            *) die "--init must be 'systemd' or 'openrc' (got '$INIT')" ;;
        esac
        printf '%s\n' "$INIT"
        return 0
    fi
    if [ -d /run/systemd/system ]; then
        printf 'systemd\n'
        return 0
    fi
    if command -v openrc-run >/dev/null 2>&1 || [ -x /sbin/openrc-run ]; then
        printf 'openrc\n'
        return 0
    fi
    die "cannot detect the init system: no /run/systemd/system and no openrc-run; pass --init systemd|openrc"
}

user_home() {
    local user=$1 line=""
    if command -v getent >/dev/null 2>&1; then
        line=$(getent passwd "$user" 2>/dev/null || true)
    fi
    if [ -z "$line" ] && [ -r /etc/passwd ]; then
        line=$(awk -F: -v u="$user" '$1 == u { print; exit }' /etc/passwd)
    fi
    [ -n "$line" ] || return 1
    printf '%s\n' "$line" | cut -d: -f6
}

user_group() {
    local user=$1 group
    group=$(id -gn "$user" 2>/dev/null || true)
    printf '%s\n' "${group:-$user}"
}

resolve_service_user() {
    # Default: the user who ran sudo; without sudo, whoever runs the script
    # (a stale $SUDO_USER in a non-root shell is ignored); as plain root, the
    # owner of the project dir.
    if [ -z "$SERVICE_USER" ] && [ "$(id -u)" -eq 0 ]; then
        SERVICE_USER=${SUDO_USER:-}
    fi
    if [ -z "$SERVICE_USER" ] && [ "$(id -u)" -ne 0 ]; then
        SERVICE_USER=$(id -un)
    fi
    if [ -z "$SERVICE_USER" ]; then
        SERVICE_USER=$(stat -c '%U' "$PROJECT_DIR" 2>/dev/null || true)
    fi
    if [ -z "$SERVICE_USER" ]; then
        SERVICE_USER=$(stat -f '%Su' "$PROJECT_DIR" 2>/dev/null || true)
    fi
    [ -n "$SERVICE_USER" ] || die "cannot determine the service user; pass --user USER"
    if [ "$SERVICE_USER" = root ]; then
        die "refusing to run the service as root; pass --user USER"
    fi
    local uid
    uid=$(id -u "$SERVICE_USER" 2>/dev/null || true)
    [ -n "$uid" ] || die "unknown user '$SERVICE_USER'"
    if [ "$uid" -eq 0 ]; then
        die "refusing to run the service as root; pass --user USER"
    fi
}

resolve_bin() {
    if [ -z "$BIN_PATH" ]; then
        if [ -x "$PROJECT_DIR/megachatd/bin/megachatd" ]; then
            BIN_PATH="$PROJECT_DIR/megachatd/bin/megachatd"
        else
            BIN_PATH=$(command -v megachatd 2>/dev/null || true)
        fi
    fi
    if [ -z "$BIN_PATH" ]; then
        die "megachatd not found (looked for $PROJECT_DIR/megachatd/bin/megachatd and in PATH); run 'make megachatd' or pass --bin PATH"
    fi
    [ -x "$BIN_PATH" ] || die "megachatd binary is not executable: $BIN_PATH (run 'make megachatd')"
    case "$BIN_PATH" in
        /*) ;;
        *) BIN_PATH="$(cd "$(dirname "$BIN_PATH")" && pwd)/$(basename "$BIN_PATH")" ;;
    esac
}

# Is the unit/init script already running (so install can restart it instead of
# refusing)?  Never true under --root/dry-run: those never touch the init system.
service_active() {
    [ -n "$ROOT" ] && return 1
    [ "$DRY_RUN" -eq 1 ] && return 1
    case "$INIT" in
        systemd) systemctl is-active --quiet "$NAME" 2>/dev/null ;;
        openrc)  rc-service "$NAME" status >/dev/null 2>&1 ;;
        *)       return 1 ;;
    esac
}

refuse_running_server() {
    die "a megachatd server is already running (lock: ${1}); stop it first with 'python3 launcher.py server stop' and retry"
}

# Refuse to install over an unrelated running server.  The lock is the same
# single-instance lock the TUI/server use; never kill it here.
check_no_running_server() {
    local managed_active=$1
    [ "$managed_active" -eq 1 ] && return 0

    local lock="${MEGACHAT_LOCK_FILE:-/tmp/signal-tui.lock}" pid=""
    if [ -f "$lock" ]; then
        pid=$(cat "$lock" 2>/dev/null || true)
        case "$pid" in
            ''|*[!0-9]*) ;;
            *)
                if [ -d "/proc/$pid" ] || kill -0 "$pid" 2>/dev/null; then
                    refuse_running_server "$lock"
                fi
                ;;
        esac
    fi

    # The launcher reports the same lock, but it also knows about legacy tmux
    # sessions.  Skip it under --root (tests must stay hermetic) and dry-run.
    if [ -z "$ROOT" ] && [ "$DRY_RUN" -eq 0 ] && [ -f "$PROJECT_DIR/launcher.py" ] \
        && command -v python3 >/dev/null 2>&1; then
        # No bytecode: as root it would leave root-owned __pycache__ in the repo.
        if ( cd "$PROJECT_DIR" && PYTHONDONTWRITEBYTECODE=1 python3 launcher.py server status 2>/dev/null ) \
            | grep -q 'Server: ATTIVO'; then
            refuse_running_server "$lock"
        fi
    fi
}

# ─── Command rendering ────────────────────────────────────────────────────────

# Quote one argument for systemd's ExecStart= (only when needed).
systemd_quote() {
    local v=$1
    case "$v" in
        *[[:space:]]*)
            v=${v//\\/\\\\}
            v=${v//\"/\\\"}
            printf '"%s"' "$v"
            ;;
        *) printf '%s' "$v" ;;
    esac
}

# Quote one word for an OpenRC assignment.
openrc_quote() {
    local v=$1
    case "$v" in
        *[[:space:]]*|*\'*)
            printf "'%s'" "${v//\'/\'\\\'\'}"
            ;;
        *) printf '%s' "$v" ;;
    esac
}

# The arguments of `megachatd serve` (without the executable name).
serve_args_only() {
    printf '%s\n' serve --project-dir "$PROJECT_DIR" --socket "$SOCKET"
    if [ "${#EXTRA_ARGS[@]}" -gt 0 ]; then
        printf '%s\n' "${EXTRA_ARGS[@]}"
    fi
}

serve_argv() {
    printf '%s\n' "$BIN_PATH"
    serve_args_only
}

build_exec_start() {
    local arg out=""
    while IFS= read -r arg; do
        if [ -z "$out" ]; then
            out=$(systemd_quote "$arg")
        else
            out="$out $(systemd_quote "$arg")"
        fi
    done < <(serve_argv)
    printf '%s' "$out"
}

build_openrc_args() {
    local arg out=""
    while IFS= read -r arg; do
        if [ -z "$out" ]; then
            out=$(openrc_quote "$arg")
        else
            out="$out $(openrc_quote "$arg")"
        fi
    done < <(serve_args_only)
    printf '%s' "$out"
}

render_systemd_unit() {
    local exec_start=$1
    cat <<EOF
[Unit]
Description=Megachat server (megachatd)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$GROUP
WorkingDirectory=$(systemd_quote "$PROJECT_DIR")
Environment=$(systemd_quote "HOME=$HOME_DIR")
ExecStart=$exec_start
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
}

render_openrc_init() {
    local args=$1
    cat <<EOF
#!/sbin/openrc-run
# Generated by $PROG; re-run it instead of editing.

description="Megachat server (megachatd)"
command=$(openrc_quote "$BIN_PATH")
command_args="$args"
command_user="$SERVICE_USER:$GROUP"
directory=$(openrc_quote "$PROJECT_DIR")
supervisor="supervise-daemon"
respawn_delay=2
respawn_max=0
output_log=$(openrc_quote "$LOG_FILE_PATH")
error_log=$(openrc_quote "$LOG_FILE_PATH")
export HOME=$(openrc_quote "$HOME_DIR")

depend() {
    need net
    after net
}
EOF
}

# ─── Write / run primitives ──────────────────────────────────────────────────

write_file() {
    local path=$1 content=$2
    if [ "$DRY_RUN" -eq 1 ]; then
        say "# would write $path"
        printf '%s\n' "$content"
        return 0
    fi
    mkdir -p "$(dirname "$path")"
    printf '%s\n' "$content" > "$path"
}

remove_file() {
    local path=$1
    if [ "$DRY_RUN" -eq 1 ]; then
        if [ -e "$path" ]; then
            say "# would remove $path"
        else
            say "# would remove $path (not present)"
        fi
        return 0
    fi
    rm -f "$path"
}

run_cmd() {
    if [ "$DRY_RUN" -eq 1 ]; then
        say "# would run: $*"
        return 0
    fi
    if [ -n "$ROOT" ]; then
        say "# skipped (--root): $*"
        return 0
    fi
    "$@"
}

run_cmd_soft() {
    if [ "$DRY_RUN" -eq 1 ]; then
        say "# would run: $*"
        return 0
    fi
    if [ -n "$ROOT" ]; then
        say "# skipped (--root): $*"
        return 0
    fi
    "$@" || true
}

create_log_file() {
    local path=$1
    if [ "$DRY_RUN" -eq 1 ]; then
        say "# would create $path (owned by $SERVICE_USER:$GROUP)"
        return 0
    fi
    mkdir -p "$(dirname "$path")"
    [ -e "$path" ] || : > "$path"
    if [ -z "$ROOT" ]; then
        chown "$SERVICE_USER:$GROUP" "$path"
    fi
    chmod 0640 "$path"
}

# ─── install ─────────────────────────────────────────────────────────────────

install_systemd() {
    local active=$1
    local unit_path exec_start content
    unit_path=$(prefixed "$UNIT_PATH")
    exec_start=$(build_exec_start)
    content=$(render_systemd_unit "$exec_start")

    write_file "$unit_path" "$content"
    run_cmd systemctl daemon-reload
    if [ "$active" -eq 1 ]; then
        run_cmd systemctl enable "$NAME"
        run_cmd systemctl restart "$NAME"
    else
        run_cmd systemctl enable --now "$NAME"
    fi

    say "installed systemd service '$NAME'"
    say "  unit:   $unit_path"
    say "  status: systemctl status $NAME"
    say "  logs:   journalctl -u $NAME"
}

install_openrc() {
    local active=$1
    local init_path content args
    init_path=$(prefixed "$INIT_PATH")
    args=$(build_openrc_args)
    content=$(render_openrc_init "$args")

    write_file "$init_path" "$content"
    if [ "$DRY_RUN" -eq 1 ]; then
        say "# would chmod 0755 $init_path"
    else
        chmod 0755 "$init_path"
    fi
    create_log_file "$LOG_FILE_PATH"

    run_cmd rc-update add "$NAME" default
    if [ "$active" -eq 1 ]; then
        run_cmd rc-service "$NAME" restart
    else
        run_cmd rc-service "$NAME" start
    fi

    say "installed OpenRC service '$NAME'"
    say "  init:   $init_path"
    say "  status: rc-service $NAME status"
    say "  logs:   $LOG_FILE_PATH"
}

cmd_install() {
    require_root
    INIT=$(resolve_init)

    [ -d "$PROJECT_DIR" ] || die "project dir does not exist: $PROJECT_DIR"
    PROJECT_DIR=$(cd "$PROJECT_DIR" && pwd)

    resolve_service_user
    resolve_bin
    HOME_DIR=$(user_home "$SERVICE_USER" || true)
    [ -n "$HOME_DIR" ] || die "cannot determine the home dir of user '$SERVICE_USER'"
    GROUP=$(user_group "$SERVICE_USER")
    SOCKET="$HOME_DIR/.local/state/megachat/server.sock"
    LOG_FILE_PATH=$(prefixed "$LOG_PATH")

    local active=0
    if service_active; then
        active=1
    fi
    check_no_running_server "$active"

    case "$INIT" in
        systemd) install_systemd "$active" ;;
        openrc)  install_openrc "$active" ;;
    esac
}

# ─── uninstall ───────────────────────────────────────────────────────────────

cmd_uninstall() {
    require_root
    INIT=$(resolve_init)
    LOG_FILE_PATH=$(prefixed "$LOG_PATH")

    case "$INIT" in
        systemd)
            run_cmd_soft systemctl disable --now "$NAME"
            remove_file "$(prefixed "$UNIT_PATH")"
            run_cmd_soft systemctl daemon-reload
            say "removed systemd service '$NAME'"
            ;;
        openrc)
            run_cmd_soft rc-service "$NAME" stop
            run_cmd_soft rc-update del "$NAME" default
            remove_file "$(prefixed "$INIT_PATH")"
            say "removed OpenRC service '$NAME'"
            ;;
    esac

    if [ "$PURGE_LOGS" -eq 1 ]; then
        remove_file "$LOG_FILE_PATH"
    fi
    say "user data was not touched (config.json, ~/.local/share/signal-tui-client)"
}

# ─── status ──────────────────────────────────────────────────────────────────

cmd_status() {
    INIT=$(resolve_init)

    case "$INIT" in
        systemd)
            say "init: systemd"
            say "unit: $(prefixed "$UNIT_PATH")"
            if [ -n "$ROOT" ]; then
                say "enabled: unknown (--root)"
                say "active: unknown (--root)"
            else
                local enabled active
                enabled=$(systemctl is-enabled "$NAME" 2>/dev/null || true)
                active=$(systemctl is-active "$NAME" 2>/dev/null || true)
                say "enabled: ${enabled:-unknown}"
                say "active: ${active:-unknown}"
            fi
            ;;
        openrc)
            say "init: openrc"
            say "unit: $(prefixed "$INIT_PATH")"
            if [ -n "$ROOT" ]; then
                say "enabled: unknown (--root)"
                say "active: unknown (--root)"
            else
                local enabled active
                if rc-update show default 2>/dev/null | awk '{print $1}' \
                    | grep -qx "$NAME"; then
                    enabled=yes
                else
                    enabled=no
                fi
                if rc-service "$NAME" status >/dev/null 2>&1; then
                    active=started
                else
                    active=stopped
                fi
                say "enabled: $enabled"
                say "active: $active"
            fi
            ;;
    esac
}

# ─── Argument parsing ────────────────────────────────────────────────────────

need_value() {
    [ "$#" -ge 2 ] || die "option $1 requires a value"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        install|uninstall|status)
            [ -z "$CMD" ] || die "only one subcommand may be given"
            CMD=$1
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --root)
            need_value "$@"
            ROOT=$2
            shift 2
            ;;
        --root=*)
            ROOT=${1#*=}
            shift
            ;;
        --init)
            need_value "$@"
            INIT=$2
            shift 2
            ;;
        --init=*)
            INIT=${1#*=}
            shift
            ;;
        --name)
            need_value "$@"
            NAME=$2
            shift 2
            ;;
        --name=*)
            NAME=${1#*=}
            shift
            ;;
        --user)
            need_value "$@"
            SERVICE_USER=$2
            shift 2
            ;;
        --user=*)
            SERVICE_USER=${1#*=}
            shift
            ;;
        --project-dir)
            need_value "$@"
            PROJECT_DIR=$2
            shift 2
            ;;
        --project-dir=*)
            PROJECT_DIR=${1#*=}
            shift
            ;;
        --bin)
            need_value "$@"
            BIN_PATH=$2
            shift 2
            ;;
        --bin=*)
            BIN_PATH=${1#*=}
            shift
            ;;
        --extra)
            need_value "$@"
            EXTRA_ARGS+=("$2")
            shift 2
            ;;
        --extra=*)
            EXTRA_ARGS+=("${1#*=}")
            shift
            ;;
        --purge-logs)
            PURGE_LOGS=1
            shift
            ;;
        --)
            shift
            while [ "$#" -gt 0 ]; do
                EXTRA_ARGS+=("$1")
                shift
            done
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1 (see --help)"
            ;;
    esac
done

[ -n "$CMD" ] || { usage >&2; exit 2; }

case "$NAME" in
    ''|*[!A-Za-z0-9_.-]*) die "invalid service name: '$NAME' (use letters, digits, '_', '.', '-')" ;;
esac
UNIT_PATH="/etc/systemd/system/${NAME}.service"
INIT_PATH="/etc/init.d/${NAME}"
LOG_PATH="/var/log/${NAME}.log"

if [ -n "$ROOT" ]; then
    ROOT=${ROOT%/}
fi

if [ -z "$PROJECT_DIR" ]; then
    SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
    PROJECT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
fi

case "$CMD" in
    install)   cmd_install ;;
    uninstall) cmd_uninstall ;;
    status)    cmd_status ;;
    *)         die "unknown subcommand: $CMD" ;;
esac
