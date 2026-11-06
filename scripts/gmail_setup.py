#!/usr/bin/env python3
"""Guided Gmail setup for the optional Web UI.

Prompts for the Gmail address and Google App Password, verifies IMAP/SMTP,
stores the secret in a ``600`` file under ``~/.config/signal-tui`` and writes
the ``gmail`` section of ``config.json`` (address, ``app_password_file`` and
``enabled: true`` unless an explicit false is already configured).  The secret
never goes through the browser.  If the current interpreter lacks the project
dependencies (Pillow), the script re-executes itself with the project
virtualenv interpreter.

Run it from the project directory:

    python3 scripts/gmail_setup.py

or:

    ./install.sh --gmail
"""

from __future__ import annotations

import argparse
import contextlib
import getpass
import imaplib
import json
import os
import smtplib
import ssl
import sys
import tempfile
from pathlib import Path

PROJECT_DIR = Path(__file__).resolve().parent.parent
if str(PROJECT_DIR) not in sys.path:
    sys.path.insert(0, str(PROJECT_DIR))


def _pil_available() -> bool:
    try:
        import PIL  # noqa: F401
    except ImportError:
        return False
    return True


def _venv_python() -> Path | None:
    """Path del python del venv di progetto, se esiste ed è un altro interprete."""
    candidate = PROJECT_DIR / ".venv" / "bin" / "python"
    if not candidate.is_file() or not os.access(candidate, os.X_OK):
        return None
    with contextlib.suppress(OSError):
        if Path(sys.executable).resolve() == candidate.resolve():
            return None
    return candidate


def _ensure_venv() -> None:
    if _pil_available():
        return
    candidate = _venv_python()
    if candidate is not None:
        print(
            f"🔁 Dipendenze non nel Python corrente; riavvio con: {candidate}",
            flush=True,
        )
        os.execv(str(candidate), [str(candidate), *sys.argv])
    print(
        "Dipendenze mancanti: usa .venv/bin/python oppure crea il venv con "
        "`python3 -m venv .venv` e `pip install -r requirements.txt`.",
        file=sys.stderr,
    )
    raise SystemExit(1)


if __name__ == "__main__":
    _ensure_venv()

CONFIG_ROOT = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
SECRET_DIR = CONFIG_ROOT / "signal-tui"
SECRET_FILE = SECRET_DIR / "gmail-app-password"
CONFIG_FILE = PROJECT_DIR / "config.json"

# Nested-schema resolution: config.json gmail.<setting>, overridden by env
# GMAIL_<SETTING> when non-blank (same rule as megachatd).
_FALSE = ("0", "false", "no", "off")


def normalize_password(value: str) -> str:
    """App Passwords are displayed in groups of four; spaces are cosmetic."""
    return "".join(value.split())


def load_config(path: Path) -> dict:
    """Return the config.json object, or ``{}`` when missing/invalid."""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def gmail_section(config: dict) -> dict:
    """Return the ``gmail`` domain object (empty when absent/malformed)."""
    section = config.get("gmail")
    return section if isinstance(section, dict) else {}


def gmail_setting(section: dict, name: str) -> str:
    """Env ``GMAIL_<NAME>`` when non-blank, else gmail.<name>, else ``""``."""
    env = os.environ.get(f"GMAIL_{name.upper()}", "").strip()
    if env:
        return env
    value = section.get(name)
    return "" if value is None else str(value).strip()


def gmail_int(section: dict, name: str, default: int) -> int:
    try:
        return int(gmail_setting(section, name))
    except ValueError:
        return default


def _is_explicit_false(value) -> bool:
    if isinstance(value, bool):
        return value is False
    return str(value).strip().lower() in _FALSE


def write_config(path: Path, config: dict) -> None:
    """Write config.json atomically with 0600 (it holds secrets)."""
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary_name = tempfile.mkstemp(prefix=".config.json.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as temporary:
            json.dump(config, temporary, ensure_ascii=False, indent=2)
            temporary.write("\n")
        os.chmod(temporary_name, 0o600)
        os.replace(temporary_name, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(temporary_name)


def update_config(config_path: Path, address: str, secret_path: Path) -> None:
    """Write address/secret and enable Gmail unless it is explicitly off.

    Every other key (including the other gmail settings) is preserved.  A
    plaintext ``app_password`` is removed: the 0600 file is the source now.
    ``GMAIL_ENABLED`` or an existing ``gmail.enabled: false`` wins over the
    implicit enable.
    """
    config = load_config(config_path)
    section = dict(gmail_section(config))
    explicit_false = _is_explicit_false(section.get("enabled")) or _is_explicit_false(
        os.environ.get("GMAIL_ENABLED", "")
    )
    section["address"] = address
    section["app_password_file"] = str(secret_path)
    section.pop("app_password", None)
    if not explicit_false:
        section["enabled"] = True
    config["gmail"] = section
    write_config(config_path, config)


def write_secret(path: Path, password: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)
    # Intentional fallback: the file is created with 600 permissions, never
    # committed, and can be upgraded to GPG/TPM with the helper scripts.
    # codeql[py/clear-text-storage-sensitive-data]
    path.write_text(password + "\n", encoding="utf-8")
    path.chmod(0o600)


def test_credentials(address: str, password: str) -> bool:
    """Verify IMAP and SMTP login without sending any message."""
    section = gmail_section(load_config(CONFIG_FILE))
    timeout = max(1, gmail_int(section, "timeout_s", 30))
    imap_host = gmail_setting(section, "imap_host") or "imap.gmail.com"
    imap_port = gmail_int(section, "imap_port", 993)
    smtp_host = gmail_setting(section, "smtp_host") or "smtp.gmail.com"
    smtp_port = gmail_int(section, "smtp_port", 587)
    ok = True

    imap = None
    try:
        imap = imaplib.IMAP4_SSL(
            imap_host,
            1 <= imap_port <= 65535 and imap_port or 993,
            timeout=timeout,
        )
        imap.login(address, password)
        imap.select(gmail_setting(section, "folder") or "INBOX", readonly=True)
        print("IMAP: login OK")
    except Exception as exc:  # noqa: BLE001 - never print the secret
        ok = False
        print(f"IMAP: errore ({type(exc).__name__})", file=sys.stderr)
    finally:
        if imap is not None:
            with contextlib.suppress(Exception):
                imap.logout()

    smtp = None
    try:
        smtp = smtplib.SMTP(
            smtp_host,
            1 <= smtp_port <= 65535 and smtp_port or 587,
            timeout=timeout,
        )
        smtp.ehlo()
        smtp.starttls(context=ssl.create_default_context())
        smtp.ehlo()
        smtp.login(address, password)
        print("SMTP: login OK")
    except Exception as exc:  # noqa: BLE001 - never print the secret
        ok = False
        print(f"SMTP: errore ({type(exc).__name__})", file=sys.stderr)
    finally:
        if smtp is not None:
            with contextlib.suppress(Exception):
                smtp.quit()
            with contextlib.suppress(Exception):
                smtp.close()

    return ok


def _platform() -> str:
    return sys.platform


def encryption_hint() -> list[str]:
    """Righe del messaggio finale sulla cifratura a riposo."""
    lines = [
        "Cifratura a riposo consigliata (ora la App Password è in chiaro in un file 600):",
        "  bash scripts/gmail_gpg_init.sh    # GPG AES-256 + cache gpg-agent (7 giorni)",
    ]
    if _platform().startswith("linux"):
        lines.append("  bash scripts/gmail_tpm_init.sh    # TPM2 via systemd-creds")
    else:
        lines.append(
            "  TPM2/systemd-creds non disponibili su questa piattaforma: usa GPG."
        )
    lines.extend(
        [
            "  Guida: README.md#gmail-optional · docs/DESIGN_GMAIL_POC.md",
            (
                "Esegui gmail_gpg_init.sh subito dopo il setup per non lasciare "
                "la password in chiaro."
            ),
        ]
    )
    return lines


def _existing_configuration() -> bool:
    section = gmail_section(load_config(CONFIG_FILE))
    return bool(gmail_setting(section, "address")) or any(
        gmail_setting(section, name)
        for name in ("app_password", "app_password_file", "app_password_cmd")
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Configura Gmail per la Web UI (memorizza l'App Password in un file 600).",
    )
    parser.add_argument("--address", help="indirizzo Gmail (altrimenti viene chiesto)")
    parser.add_argument(
        "--no-test",
        action="store_true",
        help="salva senza verificare il login IMAP/SMTP",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="sovrascrive una configurazione Gmail esistente senza chiedere",
    )
    args = parser.parse_args(argv)

    if _existing_configuration() and not args.force:
        print(
            "Attenzione: la configurazione Gmail esistente (inclusi eventuali "
            "GPG/TPM) verrà sostituita da un file 600."
        )
        answer = input("Sovrascrivere? [y/N] ").strip()
        if answer.lower() not in ("y", "yes", "s", "si", "sì"):
            print("Nessuna modifica.")
            return 1

    default_address = gmail_setting(gmail_section(load_config(CONFIG_FILE)), "address")
    address = (args.address or "").strip()
    if not address:
        prompt = (
            f"Indirizzo Gmail [{default_address}]: "
            if default_address
            else "Indirizzo Gmail: "
        )
        address = input(prompt).strip() or default_address
    if "@" not in address:
        print("Indirizzo Gmail non valido.", file=sys.stderr)
        return 1

    try:
        password = normalize_password(
            getpass.getpass("App Password Google (16 caratteri, input nascosto): ")
        )
    except (EOFError, KeyboardInterrupt):
        print("\nAnnullato.", file=sys.stderr)
        return 1
    if not password:
        print("App Password vuota.", file=sys.stderr)
        return 1
    if len(password) != 16:
        print(
            "Attenzione: l'App Password Google è di solito di 16 caratteri; proseguo comunque.",
            file=sys.stderr,
        )

    if not args.no_test and not test_credentials(address, password):
        print(
            "Login IMAP/SMTP fallito: configurazione non salvata. "
            "Controlla l'App Password o usa --no-test per salvarla comunque.",
            file=sys.stderr,
        )
        return 1

    write_secret(SECRET_FILE, password)
    update_config(CONFIG_FILE, address, SECRET_FILE)

    print()
    print("Gmail configurato.")
    print("  Segreto salvato in un file con permessi 600.")
    print(f"  Config:  {CONFIG_FILE} (600)")
    print(
        "  gmail.enabled = true scritto in config.json (se non era esplicitamente false)."
    )
    print()
    print(
        "Ricarica la tab Gmail nella Web UI: il server rilegge config.json a ogni richiesta."
    )
    print(
        "Con megachatd riavvia il server, perché i flag sono letti all'avvio: "
        "python3 launcher.py server stop && python3 launcher.py server start"
    )
    for line in encryption_hint():
        print(line)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())


# Nota manutentore: il messaggio finale di ``main()`` passa per
# ``encryption_hint()`` e rimanda a ``scripts/gmail_gpg_init.sh`` (guida:
# README.md#gmail-optional) per cifrare la App Password a riposo; la riga
# ``gmail_tpm_init.sh`` compare solo su Linux (TPM2/systemd-creds). Aggiorna
# entrambi se cambi il flusso.
