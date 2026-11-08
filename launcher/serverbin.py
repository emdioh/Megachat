"""How to spawn the headless server, ``megachatd`` (Go).

Stdlib only (see ``__init__.py``); shared by the launcher and by the TUI's
find-or-spawn (``client/attach.py``).

``megachatd serve <args> --project-dir <repo>``.  Binary lookup:
``$MEGACHATD_BIN``, else ``<repo>/megachatd/bin/megachatd``, else
``megachatd`` on ``PATH``.
"""

from __future__ import annotations

import os
import shutil
from pathlib import Path

REPO_DIR = Path(__file__).resolve().parent.parent
BIN_ENV = "MEGACHATD_BIN"
#: Where ``make megachatd`` puts the binary, relative to the repo.
REPO_BIN_REL = Path("megachatd") / "bin" / "megachatd"


class ServerBinaryNotFound(Exception):
    """megachatd cannot be found; the message is user facing (Italian)."""


def resolve_megachatd(repo: Path = REPO_DIR) -> str | None:
    """Path of the megachatd binary, or ``None``.

    ``$MEGACHATD_BIN`` wins and is returned as is when set (a broken path is
    the user's mistake, reported by the caller's spawn), else the repo build,
    else ``megachatd`` on ``PATH``.
    """
    explicit = os.environ.get(BIN_ENV, "").strip()
    if explicit:
        return str(Path(explicit).expanduser())
    built = repo / REPO_BIN_REL
    if built.is_file() and os.access(built, os.X_OK):
        return str(built)
    return shutil.which("megachatd")


def not_found_message(repo: Path = REPO_DIR) -> str:
    return (
        "megachatd non trovato. Compilalo con `make megachatd` (serve Go), "
        f"oppure imposta {BIN_ENV}=/percorso/megachatd."
    )


def serve_command(serve_args: list[str], *, repo: Path = REPO_DIR) -> list[str]:
    """Full argv of the headless server for *serve_args* (``megachat serve``
    flags, without the ``serve`` word).

    Raises :class:`ServerBinaryNotFound` when megachatd is missing.
    """
    binary = resolve_megachatd(repo)
    if binary is None:
        raise ServerBinaryNotFound(not_found_message(repo))
    project = [] if _has_project_dir(serve_args) else ["--project-dir", str(repo)]
    return [binary, "serve", *serve_args, *project]


def _has_project_dir(args: list[str]) -> bool:
    return any(a == "--project-dir" or a.startswith("--project-dir=") for a in args)
