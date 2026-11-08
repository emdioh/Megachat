#!/usr/bin/env bash
#
# install.sh — Installazione automatica di Signal TUI Client
#
# Scarica la build JVM completa di signal-cli (quella giusta, con il comando
# `daemon` per il JSON-RPC), crea un virtualenv (opzionale), installa le
# dipendenze Python e compila il server megachatd (Go). Serve Go, oppure un
# binario megachatd già presente (make megachatd / release).
# WhatsApp è nativo in megachatd: niente Docker/WAHA.
#
# Uso:
#   ./install.sh                     # installazione completa
#   ./install.sh --no-venv           # senza creare il virtualenv
#   ./install.sh --version 0.14.7    # scarica una versione specifica di signal-cli
#   ./install.sh --skip-signal-cli   # non scaricare signal-cli (se già presente)
#   ./install.sh --update            # aggiorna signal-cli all'ultima versione
#   ./install.sh --aliases           # installa solo gli alias shell della Web UI
#   ./install.sh --help              # mostra questo aiuto
#
set -euo pipefail

# ─── Colori ───────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
    C_RED=$'\033[31m'
    C_GREEN=$'\033[32m'
    C_YELLOW=$'\033[33m'
    C_BLUE=$'\033[34m'
    C_BOLD=$'\033[1m'
    C_RESET=$'\033[0m'
else
    C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""; C_BOLD=""; C_RESET=""
fi

info()  { echo "${C_BLUE}${C_BOLD}[INFO]${C_RESET} $*"; }
ok()    { echo "${C_GREEN}${C_BOLD}[OK]${C_RESET} $*"; }
warn()  { echo "${C_YELLOW}${C_BOLD}[WARN]${C_RESET} $*"; }
err()   { echo "${C_RED}${C_BOLD}[ERROR]${C_RESET} $*" >&2; }
die()   { err "$*"; exit 1; }

# ─── Configurazione ───────────────────────────────────────────────────────────
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$PROJECT_DIR/bin"
REPO="AsamK/signal-cli"
REQUIRED_PYTHON_MAJOR=3
REQUIRED_PYTHON_MINOR=12
REQUIRED_JAVA_MAJOR=25

# Flag di default
DO_VENV=1
DO_SIGNAL_CLI=1
DO_UPDATE=0
DO_ALIASES_ONLY=0
DO_GMAIL=0
SPECIFIC_VERSION=""

# ─── Parsing argomenti ────────────────────────────────────────────────────────
usage() {
    cat <<EOF
${C_BOLD}install.sh — Installazione automatica di Signal TUI Client${C_RESET}

Uso:
  ./install.sh [opzioni]

Opzioni:
  --no-venv            Non creare il virtualenv Python (usa il Python di sistema)
  --version X.Y.Z      Scarica una versione specifica di signal-cli (es. 0.14.7)
  --skip-signal-cli    Non scaricare signal-cli (se già presente in ./bin/)
  --update             Aggiorna signal-cli all'ultima versione disponibile
  --aliases            Installa solo gli alias shell della Web UI
  --gmail              Configura solo Gmail (App Password, IMAP/SMTP, config.json)
  --help               Mostra questo aiuto

Esempi:
  ./install.sh                          # installazione completa
  ./install.sh --no-venv                # senza virtualenv
  ./install.sh --update                 # aggiorna signal-cli
  ./install.sh --aliases                # installa solo gli alias della Web UI
  ./install.sh --gmail                  # configura Gmail (dopo un git pull, senza reinstallare)
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --no-venv)          DO_VENV=0; shift ;;
        --skip-signal-cli)  DO_SIGNAL_CLI=0; shift ;;
        --update)           DO_UPDATE=1; shift ;;
        --aliases)          DO_ALIASES_ONLY=1; shift ;;
        --gmail)            DO_GMAIL=1; shift ;;
        --version)
            [ $# -lt 2 ] && die "--version richiede un argomento (es. 0.14.7)"
            SPECIFIC_VERSION="$2"; shift 2 ;;
        --help|-h)          usage; exit 0 ;;
        *)                  die "Opzione sconosciuta: $1 (usa --help)" ;;
    esac
done

# ─── Funzioni di supporto ─────────────────────────────────────────────────────

# Verifica che un comando esista
require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        die "Comando '$cmd' non trovato. Installalo e riprova."
    fi
}

ensure_web_config() {
    local config_file="$PROJECT_DIR/config.json"
    local existed=0
    [ -f "$config_file" ] && existed=1

    if ! python3 - "$config_file" <<'PY'
import json
import os
import secrets
import stat
import sys
import tempfile
from pathlib import Path

config_file = Path(sys.argv[1])
if config_file.exists():
    try:
        config = json.loads(config_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SystemExit(f"config.json non valido: {exc}") from exc
    if not isinstance(config, dict):
        raise SystemExit("config.json deve contenere un oggetto JSON")
else:
    config = {}

web = config.get("web")
if not isinstance(web, dict):
    web = {}
web.setdefault("enabled", True)
web.setdefault("host", "127.0.0.1")
web.setdefault("port", 4242)
if not str(web.get("token") or "").strip():
    web["token"] = secrets.token_urlsafe(32)
config["web"] = web

fd, temporary_name = tempfile.mkstemp(prefix=".config.json.", dir=config_file.parent)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as temporary:
        json.dump(config, temporary, ensure_ascii=False, indent=2)
        temporary.write("\n")
    os.chmod(temporary_name, stat.S_IRUSR | stat.S_IWUSR)
    os.replace(temporary_name, config_file)
finally:
    try:
        os.unlink(temporary_name)
    except FileNotFoundError:
        pass
PY
    then
        err "Impossibile preparare la configurazione Web in $config_file"
        return 1
    fi

    if [ "$existed" -eq 1 ]; then
        ok "Configurazione Web verificata in $config_file"
    else
        ok "Configurazione Web creata in $config_file (permessi 0600)"
    fi
}

install_aliases() {
    local shell_name="${SHELL:-}"
    shell_name="${shell_name##*/}"
    local rc_file
    local begin_marker="# ── BEGIN signal-tui aliases ──"
    local end_marker="# ── END signal-tui aliases ──"
    local ALIASES_BLOCK

    case "$shell_name" in
        *zsh*)  rc_file="$HOME/.zshrc" ;;
        *bash*|"") rc_file="$HOME/.bashrc" ;;
        *ash*)  rc_file="$HOME/.ashrc" ;;
        *)
            warn "Shell '$shell_name' non supportata: gli alias richiedono bash/zsh/ash; fish richiederebbe funzioni (vedi docs/ALIASES.md)."
            return 0
            ;;
    esac

    if [ "$shell_name" != "${shell_name#*ash}" ] && [ "${ENV:-}" != "$rc_file" ]; then
        warn "ash legge gli alias da \$ENV, non automaticamente da $rc_file."
        warn "Assicurati che \$ENV punti a $rc_file (tipicamente esportato da /etc/profile su Alpine)."
    fi

    if [[ "$PROJECT_DIR" == *[[:space:]]* ]]; then
        warn "Il path del progetto contiene spazi; verifica il quoting degli alias dopo l'installazione: $PROJECT_DIR"
    fi

    read -r -d '' ALIASES_BLOCK <<'EOF' || true
# ─── Signal TUI Client: web reader + background via megachat serve ───────
# Web su 0.0.0.0:4242. Token Bearer: config.json (web.token) o WEB_TOKEN.
# web-signal-tui-bg stampa ed esporta il token (fast cycle: curl + login Web UI).
# Gmail TPM: la shell/TUI deve avere il gruppo tss (dopo usermod serve re-login).
SIGNAL_TUI_DIR="__PROJECT_DIR__"
alias web-signal-tui='( cd "$SIGNAL_TUI_DIR" && .venv/bin/python -m signal_tui --web --web-port 4242 --web-host 0.0.0.0 )'
_signal_tui_web_bg() {
    if [ -x "$SIGNAL_TUI_DIR/.venv/bin/python" ] && [ -f "$SIGNAL_TUI_DIR/scripts/gmail_warm_cache.py" ]; then
        "$SIGNAL_TUI_DIR/.venv/bin/python" "$SIGNAL_TUI_DIR/scripts/gmail_warm_cache.py" || true
    fi
    local pid="" lock="${MEGACHAT_LOCK_FILE:-/tmp/signal-tui.lock}"
    [ -f "$lock" ] && pid="$(cat "$lock")"
    if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
        "$SIGNAL_TUI_DIR/.venv/bin/python" "$SIGNAL_TUI_DIR/launcher.py" server start --no-token || return 1
    fi
    WEB_TOKEN="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["web"]["token"])' "$SIGNAL_TUI_DIR/config.json")" || return 1
    export WEB_TOKEN
    echo "Server bg attivo — token: $WEB_TOKEN"
}
_signal_tui_web_stop() {
    local lock="${MEGACHAT_LOCK_FILE:-/tmp/signal-tui.lock}" pid=""
    [ -f "$lock" ] && pid="$(cat "$lock" 2>/dev/null)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill -INT "$pid" 2>/dev/null || true
        for i in $(seq 1 60); do
            kill -0 "$pid" 2>/dev/null || break
            sleep 0.5
        done
        if kill -0 "$pid" 2>/dev/null; then
            echo "Server (pid $pid) ancora attivo dopo 30s: lock lasciato." >&2
            return 1
        fi
    fi
    tmux kill-session -t tui 2>/dev/null || true
    # Rimuove il lock solo se punta ancora al processo appena fermato (o è orfano).
    if [ -f "$lock" ] && [ "$(cat "$lock" 2>/dev/null)" = "$pid" ]; then
        rm -f "$lock"
    fi
    echo "Server bg fermato."
}
_signal_tui_web_status() { "$SIGNAL_TUI_DIR/.venv/bin/python" "$SIGNAL_TUI_DIR/launcher.py" server status; }
alias web-signal-tui-bg='_signal_tui_web_bg'
alias web-signal-tui-stop='_signal_tui_web_stop'
alias web-signal-tui-status='_signal_tui_web_status'
alias signal-tui-bg='_signal_tui_web_bg'
alias signal-tui-stop='_signal_tui_web_stop'
EOF
    ALIASES_BLOCK="${ALIASES_BLOCK//__PROJECT_DIR__/$PROJECT_DIR}"

    if [ ! -e "$rc_file" ]; then
        : > "$rc_file" || return 1
    fi

    local tmp_file
    tmp_file="$(mktemp "${rc_file}.tmp.XXXXXX")" || return 1
    local line in_block=0 replaced=0

    while IFS= read -r line || [ -n "$line" ]; do
        if [ "$line" = "$begin_marker" ]; then
            if [ "$replaced" -eq 0 ]; then
                printf '%s\n%s\n%s\n' "$begin_marker" "$ALIASES_BLOCK" "$end_marker" >> "$tmp_file"
                replaced=1
            fi
            in_block=1
        elif [ "$line" = "$end_marker" ] && [ "$in_block" -eq 1 ]; then
            in_block=0
        elif [ "$in_block" -eq 0 ]; then
            printf '%s\n' "$line" >> "$tmp_file"
        fi
    done < "$rc_file"

    if [ "$in_block" -eq 1 ]; then
        rm -f "$tmp_file"
        warn "Marcatore END mancante in $rc_file; file lasciato invariato."
        return 1
    fi

    if [ "$replaced" -eq 0 ]; then
        printf '\n%s\n%s\n%s\n' "$begin_marker" "$ALIASES_BLOCK" "$end_marker" >> "$tmp_file"
    fi

    if ! command cat "$tmp_file" > "$rc_file"; then
        rm -f "$tmp_file"
        return 1
    fi
    rm -f "$tmp_file"
    ok "Alias shell della Web UI installati in $rc_file"
}

ensure_env_permissions() {
    local env_file="$PROJECT_DIR/.env"
    if [ -f "$env_file" ]; then
        if chmod 600 "$env_file" 2>/dev/null; then
            ok ".env: permessi 600 verificati"
        else
            warn "Impossibile impostare .env a 600 (owner/permessi da verificare)."
        fi
    fi
}

check_optional_secrets() {
    local config_file="$PROJECT_DIR/config.json"
    [ -f "$config_file" ] || return 0
    local flags
    if ! flags="$(python3 - "$config_file" <<'PY'
import json
import sys
from pathlib import Path

try:
    config = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError):
    raise SystemExit(0)
gmail = config.get("gmail") if isinstance(config, dict) else None
gmail = gmail if isinstance(gmail, dict) else {}
out = []
if str(gmail.get("app_password") or "").strip():
    out.append("plaintext")
command = str(gmail.get("app_password_cmd") or "")
if "systemd-creds" in command:
    out.append("systemd-creds")
if "gpg" in command:
    out.append("gpg")
print(" ".join(out))
PY
    )"; then
        return 0
    fi
    case " $flags " in
        *" plaintext "*)
            warn "Gmail: App Password in chiaro in config.json (gmail.app_password); usa gmail.app_password_file o gmail.app_password_cmd."
            ;;
    esac
    case " $flags " in
        *" systemd-creds "*)
            if ! id -nG 2>/dev/null | tr ' ' '\n' | grep -qx tss; then
                warn "Gmail TPM: utente non nel gruppo tss; esegui 'sudo usermod -aG tss $USER' e rifai login."
            fi
            ;;
    esac
    case " $flags " in
        *" gpg "*)
            if ! command -v gpg >/dev/null 2>&1; then
                warn "Gmail GPG: comando configurato in config.json ma gpg non è installato."
            fi
            ;;
    esac
}

# Ottiene l'ultima versione di signal-cli dalla GitHub API
get_latest_version() {
    local url="https://api.github.com/repos/$REPO/releases/latest"
    if command -v curl >/dev/null 2>&1; then
        curl -s "$url" | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p' | head -1
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$url" | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p' | head -1
    else
        die "Serve curl o wget per determinare l'ultima versione di signal-cli."
    fi
}

# Ottiene la versione di signal-cli attualmente installata in ./bin/
get_installed_version() {
    if [ -d "$BIN_DIR" ]; then
        for d in "$BIN_DIR"/signal-cli-*; do
            if [ -d "$d" ]; then
                basename "$d" | sed 's/^signal-cli-//'
                return 0
            fi
        done
    fi
    echo ""
}

# Verifica la versione di Python
check_python() {
    if ! command -v python3 >/dev/null 2>&1; then
        die "Python 3 non trovato. Installa Python ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+ e riprova."
    fi
    local ver
    ver="$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
    local major="${ver%%.*}"
    local minor="${ver#*.}"; minor="${minor%%.*}"
    if [ "$major" -lt "$REQUIRED_PYTHON_MAJOR" ] || \
       { [ "$major" -eq "$REQUIRED_PYTHON_MAJOR" ] && [ "$minor" -lt "$REQUIRED_PYTHON_MINOR" ]; }; then
        die "Python ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+ richiesto, trovato $ver. Installa Python ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR} o superiore e riprova (macOS/Homebrew: 'brew install python@${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}' | Debian/Ubuntu: 'sudo apt install python${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}')."
    fi
    ok "Python $ver trovato."
}

# Verifica la versione di Java (richiesta dalla build JVM di signal-cli)
check_java() {
    if ! command -v java >/dev/null 2>&1; then
        warn "Java non trovato. La build JVM di signal-cli richiede Java ${REQUIRED_JAVA_MAJOR}+."
        warn "Installa Java ${REQUIRED_JAVA_MAJOR} (es. su Debian/Ubuntu: 'sudo apt install openjdk-${REQUIRED_JAVA_MAJOR}-jre')."
        return 1
    fi
    local ver
    ver="$(java -version 2>&1 | head -1 | sed 's/.*version "\([0-9]*\).*/\1/')"
    if [ -z "$ver" ]; then
        warn "Impossibile determinare la versione di Java."
        return 1
    fi
    if [ "$ver" -lt "$REQUIRED_JAVA_MAJOR" ]; then
        warn "Java $ver trovato, ma signal-cli richiede Java ${REQUIRED_JAVA_MAJOR}+."
        warn "Aggiorna Java (es. su Debian/Ubuntu: 'sudo apt install openjdk-${REQUIRED_JAVA_MAJOR}-jre')."
        return 1
    fi
    ok "Java $ver trovato."
    return 0
}

# Scarica ed estrae la build JVM completa di signal-cli in ./bin/
download_signal_cli() {
    local version="$1"
    local url="https://github.com/$REPO/releases/download/v$version/signal-cli-$version.tar.gz"
    local tarball="/tmp/signal-cli-$version.tar.gz"

    info "Scaricamento signal-cli v$version (build JVM completa)..."
    info "  $url"

    if command -v curl >/dev/null 2>&1; then
        curl -fSL -o "$tarball" "$url" || die "Download fallito (curl). Verifica la versione '$version'."
    elif command -v wget >/dev/null 2>&1; then
        wget -O "$tarball" "$url" || die "Download fallito (wget). Verifica la versione '$version'."
    else
        die "Serve curl o wget per scaricare signal-cli."
    fi

    info "Estrazione in $BIN_DIR ..."
    mkdir -p "$BIN_DIR"
    tar xzf "$tarball" -C "$BIN_DIR"
    rm -f "$tarball"

    # Verifica la struttura attesa: bin/signal-cli-*/bin/signal-cli
    local exe="$BIN_DIR/signal-cli-$version/bin/signal-cli"
    if [ ! -f "$exe" ]; then
        die "Struttura inattesa: '$exe' non trovato. La build scaricata non è quella JVM completa."
    fi
    chmod +x "$exe"
    ok "signal-cli v$version installato in $BIN_DIR/signal-cli-$version/"
}

# Rimuove le versioni di signal-cli diverse da quella specificata
remove_old_versions() {
    local keep="$1"
    if [ ! -d "$BIN_DIR" ]; then
        return 0
    fi
    for d in "$BIN_DIR"/signal-cli-*; do
        if [ -d "$d" ] && [ "$(basename "$d")" != "signal-cli-$keep" ]; then
            info "Rimozione vecchia versione: $(basename "$d")"
            rm -rf "$d"
        fi
    done
}

# Installa le dipendenze Python (con o senza venv)
install_python_deps() {
    local pip_cmd="pip3"
    local python_cmd="python3"

    if [ "$DO_VENV" -eq 1 ]; then
        if [ ! -d "$PROJECT_DIR/.venv" ]; then
            info "Creazione virtualenv in $PROJECT_DIR/.venv ..."
            python3 -m venv "$PROJECT_DIR/.venv" || die "Creazione del virtualenv fallita."
        else
            info "Virtualenv già presente in $PROJECT_DIR/.venv"
        fi
        python_cmd="$PROJECT_DIR/.venv/bin/python"
        pip_cmd="$PROJECT_DIR/.venv/bin/pip"
    fi

    info "Installazione delle dipendenze Python (requirements.txt) ..."
    "$python_cmd" -m pip install --upgrade pip >/dev/null 2>&1 || true
    "$pip_cmd" install -r "$PROJECT_DIR/requirements.txt" || die "Installazione delle dipendenze Python fallita."

    if [ "$DO_VENV" -eq 1 ]; then
        ok "Dipendenze installate nel virtualenv. Attivalo con:"
        echo "    source $PROJECT_DIR/.venv/bin/activate"
    else
        ok "Dipendenze installate nel Python di sistema."
    fi
}

# ── megachatd (server Go) ──────────────────────────────────────────────────────

MEGACHATD_DIR="$PROJECT_DIR/megachatd"

# Versione minima di Go (riga "go X.Y[.Z]" di megachatd/go.mod)
go_required_version() {
    sed -n 's/^go[[:space:]][[:space:]]*\([0-9][0-9.]*\).*/\1/p' "$MEGACHATD_DIR/go.mod" | head -1
}

# version_ge A B: vero se A >= B (ordinamento per versione)
version_ge() {
    [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$2" ]
}

# Un binario megachatd già presente (build locale, release o $MEGACHATD_BIN).
megachatd_binary() {
    if [ -n "${MEGACHATD_BIN:-}" ]; then
        printf '%s\n' "$MEGACHATD_BIN"
        return 0
    fi
    if [ -x "$MEGACHATD_DIR/bin/megachatd" ]; then
        printf '%s\n' "$MEGACHATD_DIR/bin/megachatd"
        return 0
    fi
    command -v megachatd 2>/dev/null || return 1
}

build_megachatd() {
    local want have binary
    if [ ! -f "$MEGACHATD_DIR/go.mod" ]; then
        if binary="$(megachatd_binary)"; then
            info "Sorgenti di megachatd non trovati: uso il binario già presente ($binary)."
            return 0
        fi
        die "Sorgenti di megachatd non trovati e nessun binario megachatd: serve il repository completo o un binario in MEGACHATD_BIN/PATH."
    fi
    want="$(go_required_version)"
    if ! command -v go >/dev/null 2>&1; then
        if binary="$(megachatd_binary)"; then
            info "Go non trovato: uso il binario megachatd già presente ($binary)."
            return 0
        fi
        die "Go non trovato: megachatd non può essere compilato (serve Go ${want:-1.21}+) e nessun binario megachatd è presente. Installa Go da https://go.dev/dl/ oppure imposta MEGACHATD_BIN=/percorso/megachatd."
    fi
    have="$(go version | sed -n 's/.*go\([0-9][0-9.]*\).*/\1/p' | head -1)"
    if [ -n "$want" ] && [ -n "$have" ] && ! version_ge "$have" "$want"; then
        if version_ge "$have" "1.21" && [ "${GOTOOLCHAIN:-auto}" != "local" ]; then
            info "Go $have trovato, megachatd richiede Go $want: Go scaricherà la toolchain necessaria."
        else
            die "Go $have trovato, ma megachatd richiede Go $want+: aggiornalo da https://go.dev/dl/."
        fi
    fi
    info "Compilazione di megachatd (go build) ..."
    if (cd "$MEGACHATD_DIR" && go build -o bin/megachatd ./cmd/megachatd); then
        ok "megachatd compilato in $MEGACHATD_DIR/bin/megachatd"
    else
        die "Compilazione di megachatd fallita."
    fi
}

setup_gmail() {
    local py="$PROJECT_DIR/.venv/bin/python"
    [ -x "$py" ] || py="python3"
    "$py" "$PROJECT_DIR/scripts/gmail_setup.py"
}

if [ "$DO_GMAIL" -eq 1 ]; then
    info "Configurazione Gmail…"
    ensure_web_config || die "Configurazione Web non riuscita."
    ensure_env_permissions
    chmod +x "$PROJECT_DIR"/scripts/gmail_*.sh "$PROJECT_DIR"/scripts/gmail_setup.py 2>/dev/null || true
    setup_gmail
    exit $?
fi

if [ "$DO_ALIASES_ONLY" -eq 1 ]; then
    info "Aggiunta alias web…"
    ensure_web_config || die "Configurazione Web non riuscita."
    ensure_env_permissions
    check_optional_secrets
    install_aliases
    exit $?
fi

# ─── Esecuzione principale ────────────────────────────────────────────────────
echo
echo "${C_BOLD}=== Signal TUI Client — Installazione ===${C_RESET}"
echo

# 1. Verifica prerequisiti di base
require_cmd tar
require_cmd python3

# 2. Aggiornamento signal-cli
if [ "$DO_UPDATE" -eq 1 ]; then
    info "Modalità aggiornamento signal-cli ..."
    latest="$(get_latest_version)"
    [ -z "$latest" ] && die "Impossibile determinare l'ultima versione di signal-cli."
    installed="$(get_installed_version)"

    if [ -n "$installed" ] && [ "$installed" = "$latest" ]; then
        ok "signal-cli è già all'ultima versione ($latest)."
    else
        info "Versione installata: ${installed:-nessuna} → ultima: $latest"
        download_signal_cli "$latest"
        remove_old_versions "$latest"
        ok "signal-cli aggiornato alla versione $latest."
    fi
    echo
    echo "${C_GREEN}${C_BOLD}=== Aggiornamento completato ===${C_RESET}"
    exit 0
fi

# 3. Verifica Python e Java
check_python
check_java || true   # Java è un warning, non blocca l'installazione (ma signal-cli non funzionerà senza)

# 4. Download signal-cli (se richiesto)
if [ "$DO_SIGNAL_CLI" -eq 1 ]; then
    version="$SPECIFIC_VERSION"
    if [ -z "$version" ]; then
        version="$(get_latest_version)"
        [ -z "$version" ] && die "Impossibile determinare l'ultima versione di signal-cli."
        info "Ultima versione di signal-cli rilevata: $version"
    fi

    installed="$(get_installed_version)"
    if [ -n "$installed" ] && [ "$installed" = "$version" ]; then
        ok "signal-cli v$version già installato. (usa --update per aggiornare)"
    else
        download_signal_cli "$version"
        remove_old_versions "$version"
    fi
else
    info "Download di signal-cli saltato (--skip-signal-cli)."
    installed="$(get_installed_version)"
    if [ -z "$installed" ]; then
        warn "Nessuna versione di signal-cli trovata in $BIN_DIR. Il client non funzionerà."
    else
        ok "signal-cli v$installed trovato in $BIN_DIR."
    fi
fi

# 5. Installazione dipendenze Python
install_python_deps

# 5.05 Server megachatd (Go): compila, oppure usa un binario già presente
build_megachatd

# 5.1 Configurazione Web — abilita il server al normale avvio della TUI
ensure_web_config || die "Configurazione Web non riuscita."

# 5.6 Alias shell della Web UI
install_aliases || warn "Installazione degli alias shell non riuscita; l'installazione continua."

# 5.7 Permessi .env e segreti Gmail in config.json
ensure_env_permissions
check_optional_secrets
chmod +x "$PROJECT_DIR"/scripts/gmail_*.sh "$PROJECT_DIR"/scripts/gmail_setup.py 2>/dev/null || true

# 6. Riepilogo finale
echo
echo "${C_GREEN}${C_BOLD}=== Installazione completata! ===${C_RESET}"
echo
echo "Prossimi passi:"
echo "  1. Abilita Signal (megachatd) e configura il tuo numero di telefono:"
echo "       export SIGNAL_ENABLED=1 SIGNAL_USER_NUMBER=\"+1234567890\""
echo "     oppure aggiungi al config.json (un oggetto per dominio):"
echo "       { \"signal\": { \"enabled\": true, \"user_number\": \"+1234567890\" } }"
echo "  2. Collega il tuo account Signal (QR code):"
if [ "$DO_VENV" -eq 1 ]; then
    echo "       source .venv/bin/activate"
    echo "       python3 link_account.py"
else
    echo "       python3 link_account.py"
fi
echo "  3. (Opzionale) Abilita e collega WhatsApp (QR code, nessun Docker):"
echo "       export WHATSAPP_ENABLED=1"
echo "       # oppure config.json: \"whatsapp\": { \"enabled\": true }"
echo "       python3 link_whatsapp.py"
echo "  4. (Opzionale) Abilita Telegram:"
echo "       export TELEGRAM_ENABLED=1 TELEGRAM_API_ID=123456 TELEGRAM_API_HASH=..."
echo "       # oppure config.json: \"telegram\": { \"enabled\": true, \"api_id\": 123456, \"api_hash\": \"...\" }"
echo "       # poi Ctrl+L nella TUI"
echo "  5. Avvia il client:"
if [ "$DO_VENV" -eq 1 ]; then
    echo "       source .venv/bin/activate"
fi
echo "       python3 signal_tui.py"
echo "       Web UI: http://127.0.0.1:4242"
echo "       Token:  config.json → web.token"
echo "       Gmail (opzionale): ./install.sh --gmail  (scrive gmail.enabled in config.json; a mano: GMAIL_ENABLED=1)"
echo
