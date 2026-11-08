#!/usr/bin/env python3
"""Standalone CLI utility to link WhatsApp (QR code) through the Megachat server.

Talks to the running server (``megachatd``, started on demand like the TUI does)
with the ``link.*`` operations, the same ones the TUI's device-link screen uses:
``link.status`` -> ``link.start`` (QR) -> ``link.poll`` until the phone has
scanned it.  No WAHA / Docker involved.  WhatsApp must be enabled
(``WHATSAPP_ENABLED=1`` or ``"whatsapp": {"enabled": true}`` in config.json).

Mirrors ``link_account.py`` (the Signal linking flow).

Usage:
    python3 link_whatsapp.py
"""

from __future__ import annotations

import logging
import os
import sys


# ─── Auto-run inside the project virtualenv ───────────────────────────────────
# `qrcode`, `websocket-client` etc. are installed in the project's `.venv`
# (created by install.sh).  If this script is launched with the system python
# (e.g. `python3 link_whatsapp.py` without activating the venv), re-execute
# ourselves under the venv interpreter so it "just works".
def _ensure_venv() -> None:
    try:
        import qrcode  # noqa: F401

        return
    except ImportError:
        pass

    project_dir = os.path.dirname(os.path.abspath(__file__))
    venv_python = os.path.join(project_dir, ".venv", "bin", "python")
    if os.path.exists(venv_python):
        print(f"🔁 Dipendenze non nel Python corrente; riavvio con: {venv_python}")
        os.execv(venv_python, [venv_python] + sys.argv)


_ensure_venv()

import base64
import time

logger = logging.getLogger(__name__)

from qr_utils import print_qr_code, qr_png_to_ascii
from client.attach import AttachError, attach_or_spawn
from client.client import ApiError

#: Seconds to wait for the phone to scan before giving up.
LINK_TIMEOUT_S = 300
POLL_INTERVAL_S = 2.0
CALL_TIMEOUT_S = {"link.start": 60.0, "link.poll": 20.0}


def _call(client, op: str, data: dict | None = None):
    return client.call(op, data, timeout=CALL_TIMEOUT_S.get(op, 10.0))


def _print_qr(qr: dict) -> str:
    """Show a ``link.start``/``link.poll`` QR payload; return its kind."""
    kind, data = qr.get("kind"), str(qr.get("data") or "")
    if kind == "url":
        print()
        print("📸 SCANSIONA QUESTO QR CON WHATSAPP (Dispositivi collegati):")
        print()
        print_qr_code(data)
        print()
    elif kind == "png":
        print()
        print("📸 SCANSIONA QUESTO QR CON WHATSAPP (Dispositivi collegati):")
        print()
        print(qr_png_to_ascii(base64.b64decode(data)))
        print()
    elif kind == "error":
        print(f"❌ {data}", file=sys.stderr)
    else:  # info / text
        print()
        print(data)
        print()
    return str(kind)


def main() -> None:
    print("=" * 60)
    print("  🔗 Collega WhatsApp")
    print("  📱 Scansiona il QR con WhatsApp sul telefono")
    print("=" * 60)

    try:
        attached = attach_or_spawn(
            notify=lambda msg: print(f"⏳ {msg}", file=sys.stderr, flush=True)
        )
    except AttachError as exc:
        print(f"❌ {exc}", file=sys.stderr)
        sys.exit(1)
    client = attached.client
    if attached.spawned:
        print(
            "ℹ️  Server avviato in background (fermalo con: python3 launcher.py server stop)."
        )

    try:
        status = _call(client, "link.status").get("protocols", {}).get("whatsapp", {})
        if not status.get("available"):
            print("❌ Il backend WhatsApp non è attivo nel server.", file=sys.stderr)
            print(
                "   Abilitalo con WHATSAPP_ENABLED=1 oppure "
                '"whatsapp": { "enabled": true } in config.json,',
                file=sys.stderr,
            )
            print(
                "   poi riavvia il server: python3 launcher.py server start",
                file=sys.stderr,
            )
            sys.exit(1)
        # Non abbattere una sessione già funzionante: un rilancio del link
        # scollegherebbe il dispositivo appena associato.
        if status.get("linked") is True:
            print("✅ Sessione WhatsApp già collegata: nessun reset necessario.")
            print(
                "   Per ri-associare un dispositivo, esegui prima un logout esplicito."
            )
            return

        print("⏳ Richiedo un QR valido...")
        qr = _call(client, "link.start", {"protocol": "whatsapp"})["qr"]
        kind = _print_qr(qr)
        if kind == "error":
            sys.exit(1)
        if kind == "info":
            return  # e.g. "already linked": nothing to scan

        print("⏳ In attesa della scansione dal telefono...")
        print("   (Ctrl+C per annullare — il QR viene rigenerato quando scade)")
        print("=" * 60)
        deadline = time.time() + LINK_TIMEOUT_S
        try:
            while time.time() < deadline:
                try:
                    poll = _call(client, "link.poll", {"protocol": "whatsapp"})
                except ApiError as exc:
                    logger.warning("link.poll failed: %s", exc)
                    poll = {"state": "waiting", "qr": None}
                if poll.get("state") == "done":
                    print()
                    print("✅ WhatsApp collegato e connesso!")
                    return
                if poll.get("qr"):  # the server renewed the expired QR
                    print("⟳ Il QR è scaduto: ecco quello nuovo.")
                    _print_qr(poll["qr"])
                time.sleep(POLL_INTERVAL_S)
        except KeyboardInterrupt:
            try:
                _call(client, "link.cancel", {"protocol": "whatsapp"})
            except Exception:
                logger.debug("link.cancel failed", exc_info=True)
            raise
        print()
        print("❌ Timeout: il telefono non ha scansionato il QR.", file=sys.stderr)
        sys.exit(1)
    except ApiError as exc:
        print(f"❌ Il server ha rifiutato la richiesta: {exc}", file=sys.stderr)
        sys.exit(1)
    finally:
        try:
            client.close()
        except Exception:
            logger.debug("client.close failed", exc_info=True)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print()
        print("⏹ Operazione annullata dall'utente.")
        sys.exit(0)
