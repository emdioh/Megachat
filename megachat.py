#!/usr/bin/env python3
"""Megachat entry point.

``megachat serve [...]`` hands over to ``megachatd serve`` (Go; build with
``make megachatd``), the headless server.  Anything else starts the TUI, which
attaches to a running server or spawns one (``--connect PATH`` attaches to a
given socket).  The TUI is imported lazily so the serve path never loads
Textual.
"""

import os
import sys


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] == "serve":
        from launcher import serverbin

        try:
            argv = serverbin.serve_command(args[1:])
        except serverbin.ServerBinaryNotFound as exc:
            print(f"❌ {exc}", file=sys.stderr)
            return 1
        os.execv(argv[0], argv)  # replaces this process: same pid, same lock
    from signal_tui import main as tui_main

    return tui_main(args)


if __name__ == "__main__":
    sys.exit(main())
