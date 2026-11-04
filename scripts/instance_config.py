#!/usr/bin/env python3
"""``config.json`` helper for scripts/megachat_instance.sh (stdlib only).

    instance_config.py status-page CONFIG_JSON OFFSET
    instance_config.py show CONFIG_JSON
    instance_config.py probe URL [API_KEY]

Makes sure CONFIG_JSON has an enabled ``status_page`` section (instances
created before the status page existed lack it): missing keys get their
defaults (``host`` 0.0.0.0, ``port`` 4300+OFFSET, a random ``token``), present
ones are kept.  Prints ``PORT TOKEN``; exit code 0 = unchanged, 10 = the file
was updated (the server must be restarted to serve the page).

``show`` only reads: prints ``ENABLED PORT TOKEN`` of the status page
(``1``/``0``, ``-`` for a missing port or token).

``probe`` GETs URL directly (no proxy, 5 s timeout, ``X-Api-Key`` when given)
and prints ``CODE SUMMARY``: the HTTP status (``0`` = no answer) and a short
summary of the body or of the error.  Used by ``megachat_instance.sh doctor``.
"""

from __future__ import annotations

import json
import os
import secrets
import sys
from pathlib import Path

CHANGED = 10


def ensure_status_page(path: Path, offset: int) -> tuple[int, str, bool]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        data = {}
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: not a JSON object")
    section = data.get("status_page")
    if not isinstance(section, dict):
        section = {}
    before = dict(section)
    section.setdefault("host", "0.0.0.0")
    section.setdefault("port", 4300 + offset)
    if not section.get("token"):
        section["token"] = secrets.token_hex(24)
    section["enabled"] = True
    changed = section != before
    if changed:
        data["status_page"] = section
        tmp = path.with_name(path.name + ".tmp")
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as out:
            json.dump(data, out, indent=2)
            out.write("\n")
        os.replace(tmp, path)
    return int(section["port"]), str(section["token"]), changed


def show_status_page(path: Path) -> str:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        data = {}
    section = data.get("status_page") if isinstance(data, dict) else None
    if not isinstance(section, dict):
        section = {}
    enabled = "1" if section.get("enabled") is True and section.get("token") else "0"
    port = section.get("port")
    port = str(port) if isinstance(port, int) and 1 <= port <= 65535 else "-"
    token = str(section.get("token") or "-")
    return f"{enabled} {port} {token}"


def probe(url: str, api_key: str = "") -> str:
    import urllib.error
    import urllib.request

    request = urllib.request.Request(
        url, headers={"X-Api-Key": api_key} if api_key else {}
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=5) as resp:
            code, raw = resp.status, resp.read(4096)
    except urllib.error.HTTPError as err:
        code, raw = err.code, err.read(4096)
    except (urllib.error.URLError, OSError) as exc:
        reason = getattr(exc, "reason", exc)
        return f"0 {reason}"
    text = raw.decode("utf-8", "replace")
    try:
        data = json.loads(text)
    except ValueError:
        return f"{code} {' '.join(text.split())[:150]}"
    if isinstance(data, dict):
        if "whatsapp" in data and isinstance(data["whatsapp"], dict):  # status page
            wa = data["whatsapp"]
            detail = f" ({wa['detail']})" if wa.get("detail") else ""
            return f"{code} WhatsApp {wa.get('state')}{detail}"
        if "status" in data:  # WAHA session
            me = data.get("me") if isinstance(data.get("me"), dict) else {}
            who = f", account {me.get('id')}" if me else ""
            return f"{code} status {data.get('status')}{who}"
        message = data.get("message") or data.get("error")
        if message:
            return f"{code} {' '.join(str(message).split())[:150]}"
    return f"{code} {' '.join(text.split())[:150]}"


def main(argv: list[str]) -> int:
    if len(argv) in (2, 3) and argv[0] == "probe":
        print(probe(argv[1], argv[2] if len(argv) == 3 else ""))
        return 0
    if len(argv) == 2 and argv[0] == "show":
        print(show_status_page(Path(argv[1])))
        return 0
    if len(argv) != 3 or argv[0] != "status-page":
        print(__doc__, file=sys.stderr)
        return 2
    port, token, changed = ensure_status_page(Path(argv[1]), int(argv[2]))
    print(port, token)
    return CHANGED if changed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
