"""Entry-point dispatch: ``megachat serve`` vs the TUI, ``python -m server``."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

import megachat

ROOT = Path(__file__).resolve().parent.parent


def test_serve_dispatches_to_server_main(monkeypatch):
    monkeypatch.setenv("MEGACHAT_SERVER", "python")
    with patch("server.main.serve", return_value=7) as serve:
        rc = megachat.main(["serve", "--no-web", "--debug"])
    assert rc == 7
    serve.assert_called_once_with(["--no-web", "--debug"])


def test_serve_execs_megachatd_by_default(monkeypatch):
    monkeypatch.setenv("MEGACHATD_BIN", "/opt/megachatd")
    with (
        patch("os.execv", side_effect=SystemExit(0)) as execv,
        patch("server.main.serve") as serve,
        pytest.raises(SystemExit),
    ):
        megachat.main(["serve", "--no-web"])
    execv.assert_called_once_with(
        "/opt/megachatd",
        ["/opt/megachatd", "serve", "--no-web", "--project-dir", str(ROOT)],
    )
    serve.assert_not_called()


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
        patch("server.main.serve") as serve,
    ):
        rc = megachat.main(["--web", "--web-port", "4242"])
    assert rc == 3
    tui.assert_called_once_with(["--web", "--web-port", "4242"])
    serve.assert_not_called()


def test_no_args_start_the_tui_with_sys_argv(monkeypatch):
    monkeypatch.setattr(sys, "argv", ["megachat", "--debug"])
    with patch("signal_tui.main", return_value=0) as tui:
        assert megachat.main() == 0
    tui.assert_called_once_with(["--debug"])


def test_serve_is_only_the_first_argument():
    with patch("signal_tui.main", return_value=0) as tui:
        megachat.main(["--debug", "serve"])
    tui.assert_called_once_with(["--debug", "serve"])


def test_in_process_tui_refuses_when_lock_is_held(capsys):
    import signal_tui

    with (
        patch("signal_tui.logging.basicConfig"),
        patch("signal_tui._acquire_lock", return_value=False),
    ):
        assert signal_tui.main(["--in-process"]) == 1
    assert "already running" in capsys.readouterr().err


def _run(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, *args],
        env={**os.environ, "MEGACHAT_SERVER": "python"},
        cwd=ROOT,
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )


def test_python_m_server_dispatches_to_serve():
    result = _run("-m", "server", "--help")
    assert result.returncode == 0
    assert "--no-web" in result.stdout


def test_python_m_server_rejects_bad_port():
    result = _run("-m", "server", "--web-port", "70000")
    assert result.returncode == 2


def test_megachat_script_serve_help():
    result = _run("megachat.py", "serve", "--help")
    assert result.returncode == 0
    assert "--web-no-auth" in result.stdout


def test_signal_tui_script_and_module_help_still_work():
    for args in (("signal_tui.py", "--help"), ("-m", "signal_tui", "--help")):
        result = _run(*args)
        assert result.returncode == 0, result.stderr
        assert "--web-no-auth" in result.stdout
