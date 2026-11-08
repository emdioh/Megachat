"""Full installer — signal-cli download, venv, Python deps, megachatd build,
web config.

Ported from ``install.sh``.
"""

from __future__ import annotations

import json
import os
import re
import secrets
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

from . import serverbin
from .aliases import install_aliases
from .common import (
    C_BOLD,
    C_GREEN,
    C_RESET,
    PROJECT_DIR,
    command_exists,
    die,
    http_get,
    info,
    ok,
    warn,
)

BIN_DIR = PROJECT_DIR / "bin"
MEGACHATD_DIR = PROJECT_DIR / "megachatd"
REPO = "AsamK/signal-cli"
REQUIRED_PYTHON = (3, 12)
REQUIRED_JAVA_MAJOR = 25


def ensure_web_config() -> None:
    config_file = PROJECT_DIR / "config.json"
    existed = config_file.exists()
    if existed:
        try:
            config = json.loads(config_file.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            die(f"config.json non valido: {exc}")
            return
        if not isinstance(config, dict):
            die("config.json deve contenere un oggetto JSON")
            return
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

    fd, tmp_name = tempfile.mkstemp(prefix=".config.json.", dir=config_file.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as tmp:
            json.dump(config, tmp, ensure_ascii=False, indent=2)
            tmp.write("\n")
        os.chmod(tmp_name, stat.S_IRUSR | stat.S_IWUSR)
        os.replace(tmp_name, config_file)
    finally:
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass

    if existed:
        ok(f"Configurazione Web verificata in {config_file}")
    else:
        ok(f"Configurazione Web creata in {config_file} (permessi 0600)")


def get_latest_version() -> str:
    url = f"https://api.github.com/repos/{REPO}/releases/latest"
    result = http_get(
        url, headers={"User-Agent": "signal-tui-client-launcher"}, timeout=15
    )
    if result is None or result[0] != 200:
        die("Impossibile determinare l'ultima versione di signal-cli.")
        return ""
    try:
        data = json.loads(result[1])
    except json.JSONDecodeError:
        die("Impossibile determinare l'ultima versione di signal-cli.")
        return ""
    tag = str(data.get("tag_name") or "")
    return tag.removeprefix("v")


def get_installed_version() -> str:
    if BIN_DIR.is_dir():
        for d in sorted(BIN_DIR.glob("signal-cli-*")):
            if d.is_dir():
                return d.name.removeprefix("signal-cli-")
    return ""


def check_python() -> None:
    major, minor = sys.version_info[:2]
    if (major, minor) < REQUIRED_PYTHON:
        die(
            f"Python {REQUIRED_PYTHON[0]}.{REQUIRED_PYTHON[1]}+ richiesto, "
            f"trovato {major}.{minor}. Installa Python "
            f"{REQUIRED_PYTHON[0]}.{REQUIRED_PYTHON[1]} o superiore e riprova "
            "(macOS/Homebrew: 'brew install python@"
            f"{REQUIRED_PYTHON[0]}.{REQUIRED_PYTHON[1]}' | Debian/Ubuntu: "
            f"'sudo apt install python{REQUIRED_PYTHON[0]}.{REQUIRED_PYTHON[1]}')."
        )
        return
    ok(f"Python {major}.{minor} trovato.")


def check_java() -> bool:
    if not command_exists("java"):
        warn(
            "Java non trovato. La build JVM di signal-cli richiede "
            f"Java {REQUIRED_JAVA_MAJOR}+."
        )
        warn(
            f"Installa Java {REQUIRED_JAVA_MAJOR} "
            f"(es. su Debian/Ubuntu: 'sudo apt install openjdk-{REQUIRED_JAVA_MAJOR}-jre')."
        )
        return False
    try:
        result = subprocess.run(
            ["java", "-version"],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
        first_line = (result.stdout or result.stderr).splitlines()[0]
    except (subprocess.SubprocessError, OSError, IndexError):
        warn("Impossibile determinare la versione di Java.")
        return False
    import re

    m = re.search(r'version "(\d+)', first_line)
    if not m:
        warn("Impossibile determinare la versione di Java.")
        return False
    ver = int(m.group(1))
    if ver < REQUIRED_JAVA_MAJOR:
        warn(f"Java {ver} trovato, ma signal-cli richiede Java {REQUIRED_JAVA_MAJOR}+.")
        warn(
            f"Aggiorna Java (es. su Debian/Ubuntu: "
            f"'sudo apt install openjdk-{REQUIRED_JAVA_MAJOR}-jre')."
        )
        return False
    ok(f"Java {ver} trovato.")
    return True


def download_signal_cli(version: str) -> None:
    url = f"https://github.com/{REPO}/releases/download/v{version}/signal-cli-{version}.tar.gz"
    tarball = Path(tempfile.gettempdir()) / f"signal-cli-{version}.tar.gz"

    info(f"Scaricamento signal-cli v{version} (build JVM completa)...")
    info(f"  {url}")
    try:
        urllib.request.urlretrieve(url, tarball)
    except (urllib.error.URLError, OSError) as exc:
        die(f"Download fallito. Verifica la versione '{version}'. ({exc})")
        return

    info(f"Estrazione in {BIN_DIR} ...")
    BIN_DIR.mkdir(parents=True, exist_ok=True)
    try:
        with tarfile.open(tarball) as tar:
            tar.extractall(BIN_DIR, filter="data")  # type: ignore[call-arg]
    except TypeError:
        with tarfile.open(tarball) as tar:
            tar.extractall(BIN_DIR)
    tarball.unlink(missing_ok=True)

    exe = BIN_DIR / f"signal-cli-{version}" / "bin" / "signal-cli"
    if not exe.is_file():
        die(
            f"Struttura inattesa: '{exe}' non trovato. "
            "La build scaricata non è quella JVM completa."
        )
        return
    exe.chmod(exe.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    ok(f"signal-cli v{version} installato in {BIN_DIR / f'signal-cli-{version}'}/")


def remove_old_versions(keep: str) -> None:
    if not BIN_DIR.is_dir():
        return
    import shutil

    for d in BIN_DIR.glob("signal-cli-*"):
        if d.is_dir() and d.name != f"signal-cli-{keep}":
            info(f"Rimozione vecchia versione: {d.name}")
            shutil.rmtree(d, ignore_errors=True)


def install_python_deps(do_venv: bool) -> None:
    if do_venv:
        venv_dir = PROJECT_DIR / ".venv"
        if not venv_dir.is_dir():
            info(f"Creazione virtualenv in {venv_dir} ...")
            result = subprocess.run(
                [sys.executable, "-m", "venv", str(venv_dir)], check=False
            )
            if result.returncode != 0:
                die("Creazione del virtualenv fallita.")
                return
        else:
            info(f"Virtualenv già presente in {venv_dir}")
        python_cmd = str(venv_dir / "bin" / "python")
    else:
        python_cmd = sys.executable

    info("Installazione delle dipendenze Python (requirements.txt) ...")
    subprocess.run(
        [python_cmd, "-m", "pip", "install", "--upgrade", "pip"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    result = subprocess.run(
        [
            python_cmd,
            "-m",
            "pip",
            "install",
            "-r",
            str(PROJECT_DIR / "requirements.txt"),
        ],
        check=False,
    )
    if result.returncode != 0:
        die("Installazione delle dipendenze Python fallita.")
        return

    if do_venv:
        ok("Dipendenze installate nel virtualenv. Attivalo con:")
        print(f"    source {PROJECT_DIR}/.venv/bin/activate")
    else:
        ok("Dipendenze installate nel Python di sistema.")


def _version_tuple(text: str) -> tuple[int, ...]:
    return tuple(int(p) for p in re.findall(r"\d+", text)[:3])


def go_required_version() -> str:
    """Minimum Go version, the ``go X.Y[.Z]`` line of ``megachatd/go.mod``."""
    try:
        text = (MEGACHATD_DIR / "go.mod").read_text(encoding="utf-8")
    except OSError:
        return ""
    match = re.search(r"^go\s+([0-9][0-9.]*)", text, re.MULTILINE)
    return match.group(1) if match else ""


def installed_go_version() -> str:
    try:
        out = subprocess.run(
            ["go", "version"], capture_output=True, text=True, timeout=30, check=False
        ).stdout
    except (subprocess.SubprocessError, OSError):
        return ""
    match = re.search(r"go([0-9][0-9.]*)", out)
    return match.group(1) if match else ""


def build_megachatd() -> bool:
    """Build ``megachatd/bin/megachatd`` when Go is available; an existing
    release binary is accepted when Go is not.  ``True`` when megachatd is
    available.  A missing Go *and* binary, or a failed build, is fatal."""
    want = go_required_version()
    if not (MEGACHATD_DIR / "go.mod").is_file():
        if serverbin.resolve_megachatd(PROJECT_DIR):
            info("Sorgenti di megachatd non trovati: uso il binario già presente.")
            return True
        die(
            "Sorgenti di megachatd non trovati e nessun binario megachatd: "
            "serve il repository completo o un binario in "
            f"{serverbin.BIN_ENV}/PATH."
        )
        return False
    if not command_exists("go"):
        binary = serverbin.resolve_megachatd(PROJECT_DIR)
        if binary:
            info(f"Go non trovato: uso il binario megachatd già presente ({binary}).")
            return True
        die(
            f"Go non trovato: megachatd non può essere compilato (serve Go "
            f"{want or '1.21'}+) e nessun binario megachatd è presente. Installa "
            "Go da https://go.dev/dl/ oppure imposta "
            f"{serverbin.BIN_ENV}=/percorso/megachatd."
        )
        return False
    have = installed_go_version()
    if want and have and _version_tuple(have) < _version_tuple(want):
        if _version_tuple(have) >= (1, 21) and os.environ.get("GOTOOLCHAIN") != "local":
            info(
                f"Go {have} trovato, megachatd richiede Go {want}: Go scaricherà la toolchain necessaria."
            )
        else:
            die(
                f"Go {have} trovato, ma megachatd richiede Go {want}+: aggiornalo da https://go.dev/dl/."
            )
            return False
    info("Compilazione di megachatd (go build) ...")
    result = subprocess.run(
        ["go", "build", "-o", "bin/megachatd", "./cmd/megachatd"],
        cwd=MEGACHATD_DIR,
        check=False,
    )
    if result.returncode != 0:
        die("Compilazione di megachatd fallita.")
        return False
    ok(f"megachatd compilato in {MEGACHATD_DIR / 'bin' / 'megachatd'}")
    return True


def run_aliases_only() -> int:
    info("Aggiunta alias web…")
    ensure_web_config()
    return 0 if install_aliases() else 1


def run(args) -> int:
    do_venv = not args.no_venv
    do_signal_cli = not args.skip_signal_cli
    specific_version = args.version

    if args.aliases:
        return run_aliases_only()

    print()
    print(f"{C_BOLD}=== Signal TUI Client — Installazione ==={C_RESET}")
    print()

    if not command_exists("tar"):
        die("Comando 'tar' non trovato. Installalo e riprova.")
        return 1

    if args.update:
        info("Modalità aggiornamento signal-cli ...")
        latest = get_latest_version()
        if not latest:
            die("Impossibile determinare l'ultima versione di signal-cli.")
            return 1
        installed = get_installed_version()
        if installed and installed == latest:
            ok(f"signal-cli è già all'ultima versione ({latest}).")
        else:
            info(f"Versione installata: {installed or 'nessuna'} → ultima: {latest}")
            download_signal_cli(latest)
            remove_old_versions(latest)
            ok(f"signal-cli aggiornato alla versione {latest}.")
        print()
        print(f"{C_GREEN}{C_BOLD}=== Aggiornamento completato ==={C_RESET}")
        return 0

    check_python()
    check_java()  # warning-only, never fatal (mirrors `check_java || true`)

    if do_signal_cli:
        version = specific_version
        if not version:
            version = get_latest_version()
            if not version:
                die("Impossibile determinare l'ultima versione di signal-cli.")
                return 1
            info(f"Ultima versione di signal-cli rilevata: {version}")
        installed = get_installed_version()
        if installed and installed == version:
            ok(f"signal-cli v{version} già installato. (usa --update per aggiornare)")
        else:
            download_signal_cli(version)
            remove_old_versions(version)
    else:
        info("Download di signal-cli saltato (--skip-signal-cli).")
        installed = get_installed_version()
        if not installed:
            warn(
                f"Nessuna versione di signal-cli trovata in {BIN_DIR}. Il client non funzionerà."
            )
        else:
            ok(f"signal-cli v{installed} trovato in {BIN_DIR}.")

    install_python_deps(do_venv)

    build_megachatd()

    ensure_web_config()

    if not install_aliases():
        warn("Installazione degli alias shell non riuscita; l'installazione continua.")

    print()
    print(f"{C_GREEN}{C_BOLD}=== Installazione completata! ==={C_RESET}")
    print()
    print("Prossimi passi:")
    print("  1. Abilita Signal (megachatd) e configura il tuo numero di telefono:")
    print('       export SIGNAL_ENABLED=1 SIGNAL_USER_NUMBER="+1234567890"')
    print("     oppure aggiungi al config.json (un oggetto per dominio):")
    print("""       { "signal": { "enabled": true, "user_number": "+1234567890" } }""")
    print("  2. Collega il tuo account Signal (QR code):")
    if do_venv:
        print("       source .venv/bin/activate")
    print("       python3 link_account.py")
    print("  3. (Opzionale) Abilita e collega WhatsApp (QR code, nessun Docker):")
    print("       export WHATSAPP_ENABLED=1")
    print('       # oppure config.json: "whatsapp": { "enabled": true }')
    print("       python3 link_whatsapp.py")
    print("  4. (Opzionale) Abilita Telegram:")
    print(
        "       export TELEGRAM_ENABLED=1 TELEGRAM_API_ID=123456 TELEGRAM_API_HASH=..."
    )
    print(
        '       # oppure config.json: "telegram": { "enabled": true, "api_id": 123456, "api_hash": "..." }'
    )
    print("       # poi Ctrl+L nella TUI")
    print("  5. Avvia il client:")
    if do_venv:
        print("       source .venv/bin/activate")
    print("       python3 signal_tui.py")
    print("       Web UI: http://127.0.0.1:4242")
    print("       Token:  config.json → web.token")
    print(
        "       Gmail (opzionale): python3 scripts/gmail_setup.py "
        "(scrive gmail.enabled in config.json; a mano: GMAIL_ENABLED=1)"
    )
    print()
    return 0
