"""Entry-point dispatch: ``megachat serve`` (megachatd) vs the TUI."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

import megachat

ROOT = Path(__file__).resolve().parent.parent


def test_serve_execs_megachatd_by_default(monkeypatch):
    monkeypatch.setenv("MEGACHATD_BIN", "/opt/megachatd")
    with (
        patch("os.execv", side_effect=SystemExit(0)) as execv,
        pytest.raises(SystemExit),
    ):
        megachat.main(["serve", "--no-web"])
    execv.assert_called_once_with(
        "/opt/megachatd",
        ["/opt/megachatd", "serve", "--no-web", "--project-dir", str(ROOT)],
    )


def test_serve_without_megachatd_reports_and_fails(monkeypatch, capsys):
    monkeypatch.delenv("MEGACHATD_BIN", raising=False)
    monkeypatch.setattr("launcher.serverbin.shutil.which", lambda name: None)
    monkeypatch.setattr("launcher.serverbin.REPO_BIN_REL", Path("nope/megachatd"))
    with patch("os.execv") as execv:
        assert megachat.main(["serve"]) == 1
    execv.assert_not_called()
    assert "megachatd non trovato" in capsys.readouterr().err


def test_other_args_go_to_the_tui():
    with (
        patch("signal_tui.main", return_value=3) as tui,
        patch("os.execv") as execv,
    ):
        rc = megachat.main(["--web", "--web-port", "4242"])
    assert rc == 3
    tui.assert_called_once_with(["--web", "--web-port", "4242"])
    execv.assert_not_called()


def test_no_args_start_the_tui_with_sys_argv(monkeypatch):
    monkeypatch.setattr(sys, "argv", ["megachat", "--debug"])
    with patch("signal_tui.main", return_value=0) as tui:
        assert megachat.main() == 0
    tui.assert_called_once_with(["--debug"])


def test_serve_is_only_the_first_argument():
    with patch("signal_tui.main", return_value=0) as tui:
        megachat.main(["--debug", "serve"])
    tui.assert_called_once_with(["--debug", "serve"])


def megachat_bin() -> str | None:
    from launcher import serverbin

    return serverbin.resolve_megachatd()


def _run(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, *args],
        env=dict(os.environ),
        cwd=ROOT,
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )


def test_megachat_script_serve_help():
    """Hands over to ``megachatd serve --help`` (Go's flag package prints the
    usage on stderr and exits 0)."""
    if megachat_bin() is None:
        pytest.skip("megachatd not built (make megachatd)")
    result = _run("megachat.py", "serve", "--help")
    assert result.returncode == 0
    assert "-web-no-auth" in result.stdout + result.stderr


def test_signal_tui_script_and_module_help_still_work():
    for args in (("signal_tui.py", "--help"), ("-m", "signal_tui", "--help")):
        result = _run(*args)
        assert result.returncode == 0, result.stderr
        assert "--web-no-auth" in result.stdout
