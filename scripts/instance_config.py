#!/usr/bin/env python3
"""``config.json`` helper for scripts/megachat_instance.sh (stdlib only).

    instance_config.py status-page CONFIG_JSON OFFSET

Makes sure CONFIG_JSON has an enabled ``status_page`` section (instances
created before the status page existed lack it): missing keys get their
defaults (``host`` 0.0.0.0, ``port`` 4300+OFFSET, a random ``token``), present
ones are kept.  Prints ``PORT TOKEN``; exit code 0 = unchanged, 10 = the file
was updated (the server must be restarted to serve the page).
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


def main(argv: list[str]) -> int:
    if len(argv) != 3 or argv[0] != "status-page":
        print(__doc__, file=sys.stderr)
        return 2
    port, token, changed = ensure_status_page(Path(argv[1]), int(argv[2]))
    print(port, token)
    return CHANGED if changed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
