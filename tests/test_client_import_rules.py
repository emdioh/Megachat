"""Import rules of the Python client side (the TUI talks to megachatd only).

* ``client/`` (socket client, attach, wire types) never imports Textual or the
  UI modules: the launcher and scripts use it without a terminal.
* Nothing imports the deleted Python server packages (``server``,
  ``protocols``, ``transcription``, ``web`` Python modules).
"""

from __future__ import annotations

import ast
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DELETED = {"server", "protocols", "transcription", "web"}
UI = {"textual", "tui", "signal_tui", "ui_components", "contact_picker"}


def _python_files() -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files", "*.py"], cwd=ROOT, capture_output=True, text=True, check=True
    ).stdout.split()
    return [ROOT / f for f in out if not f.startswith("megachatd/")]


def _imports(path: Path) -> set[str]:
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    found: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            found.update(alias.name.split(".")[0] for alias in node.names)
        elif isinstance(node, ast.ImportFrom) and node.module and node.level == 0:
            found.add(node.module.split(".")[0])
    return found


def test_nothing_imports_the_deleted_python_server():
    offenders = {
        str(p.relative_to(ROOT)): sorted(_imports(p) & DELETED)
        for p in _python_files()
        if _imports(p) & DELETED
    }
    assert offenders == {}


def test_client_package_never_imports_ui_modules():
    offenders = {
        str(p.relative_to(ROOT)): sorted(_imports(p) & UI)
        for p in sorted((ROOT / "client").glob("*.py"))
        if _imports(p) & UI
    }
    assert offenders == {}


def test_client_package_imports_without_textual_loaded():
    code = (
        "import sys, client.attach, client.socket_client, client.config, "
        "client.domain, client.events_api;"
        "bad = sorted(m for m in sys.modules if m.split('.')[0] in "
        f"{sorted(UI)!r});"
        "print(bad); sys.exit(1 if bad else 0)"
    )
    result = subprocess.run(
        [sys.executable, "-c", code], cwd=ROOT, capture_output=True, text=True, check=False
    )
    assert result.returncode == 0, result.stdout + result.stderr
