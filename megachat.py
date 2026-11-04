#!/usr/bin/env python3
"""Megachat entry point.

``megachat serve [...]`` runs the headless server (core + socket + web
adapter, no TUI): by default it hands over to ``megachatd serve`` (Go; build
with ``make megachatd``), with ``MEGACHAT_SERVER=python`` it runs the old
Python server in this process.  Anything else starts the TUI, which attaches to a running
server or spawns ``megachat serve`` (``--connect PATH`` attaches to a given
socket, ``--in-process`` runs the core inside the TUI; design §5).  The TUI is
imported lazily so the serve path never loads Textual.
"""

import os
import sys


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] == "serve":
        from launcher import serverbin

        if not serverbin.use_python():
            try:
                argv = serverbin.serve_command(args[1:])
            except serverbin.ServerBinaryNotFound as exc:
                print(f"❌ {exc}", file=sys.stderr)
                return 1
            os.execv(argv[0], argv)  # replaces this process: same pid, same lock
        from server.main import serve

        return serve(args[1:])
    from signal_tui import main as tui_main

    return tui_main(args)


if __name__ == "__main__":
    sys.exit(main())
