#!/usr/bin/env python3
"""Megachat entry point.

``megachat serve [...]`` runs the headless server (core + socket + web
adapter, no TUI).  Anything else starts the TUI, which attaches to a running
server or spawns ``megachat serve`` (``--connect PATH`` attaches to a given
socket, ``--in-process`` runs the core inside the TUI; design §5).  The TUI is
imported lazily so the serve path never loads Textual.
"""

import os
import sys
from pathlib import Path

PROJECT_DIR = Path(__file__).resolve().parent
REEXEC_ENV = "MEGACHAT_VENV_REEXEC"


def _missing_dependencies(argv: list[str]) -> bool:
    """Whether this interpreter lacks the packages the selected mode imports."""
    import importlib.util

    wanted = ("pydantic",) if argv[:1] == ["serve"] else ("pydantic", "textual")
    for name in wanted:
        try:
            if importlib.util.find_spec(name) is None:
                return True
        except (ImportError, ValueError):
            return True
    return False


def _ensure_venv(argv: list[str]) -> None:
    """Re-exec under the project ``.venv`` when this Python lacks the deps.

    Service units and manual ``python3 megachat.py`` runs must not die with
    ``ModuleNotFoundError`` when the project has a ``.venv``; same policy as
    ``link_whatsapp.py``.  The guard variable prevents an exec loop when the
    venv itself is broken.
    """
    venv_python = PROJECT_DIR / ".venv" / "bin" / "python"
    if os.environ.get(REEXEC_ENV) == "1" or not venv_python.is_file():
        return
    try:
        same = os.path.samefile(sys.executable, venv_python)
    except OSError:
        same = False
    if same or not _missing_dependencies(argv):
        return
    os.environ[REEXEC_ENV] = "1"
    print(
        f"🔁 dipendenze mancanti nel Python corrente; riavvio con: {venv_python}",
        file=sys.stderr,
    )
    try:
        os.execv(str(venv_python), [str(venv_python), *sys.argv])
    except OSError as exc:
        print(f"⚠️  impossibile riavviare {venv_python}: {exc}", file=sys.stderr)


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] == "serve":
        from server.main import serve

        return serve(args[1:])
    from signal_tui import main as tui_main

    return tui_main(args)


if __name__ == "__main__":
    _ensure_venv(sys.argv[1:])
    sys.exit(main())
