"""Per-instance layout (``MEGACHAT_HOME``, see instance.py)."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

import instance
from server.paths import default_socket_path

PROJECT_ROOT = Path(__file__).resolve().parent.parent


@pytest.fixture
def no_home(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MEGACHAT_HOME", raising=False)
    monkeypatch.delenv("MEGACHAT_LOCK_FILE", raising=False)
    monkeypatch.delenv("MEGACHAT_SOCKET", raising=False)


def test_default_layout_is_unchanged(no_home: None) -> None:
    assert instance.home() is None
    assert instance.config_file() == instance.PROJECT_DIR / "config.json"
    assert instance.dotenv_file() == instance.PROJECT_DIR / ".env"
    assert instance.data_dir() == Path.home() / ".local/share/signal-tui-client"
    assert instance.signal_cli_dir() == Path.home() / ".local/share/signal-cli"
    assert instance.signal_cli_config_args() == []
    assert instance.cache_dir() is None
    assert instance.socket_file() is None
    assert instance.lock_file() == "/tmp/signal-tui.lock"
    assert instance.log_file("x.log") == "/tmp/x.log"


def test_instance_layout(no_home: None, monkeypatch, tmp_path: Path) -> None:
    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    assert instance.home() == tmp_path.resolve()
    assert instance.config_file() == tmp_path / "config.json"
    assert instance.dotenv_file() == tmp_path / ".env"
    assert instance.data_dir() == tmp_path / "data"
    assert instance.signal_cli_config_args() == [
        "--config",
        str(tmp_path / "signal-cli"),
    ]
    assert instance.cache_dir() == tmp_path / "cache"
    assert instance.lock_file() == str(tmp_path / "run" / "signal-tui.lock")
    assert instance.log_file("x.log") == str(tmp_path / "logs" / "x.log")
    assert (tmp_path / "logs").is_dir()
    assert default_socket_path() == tmp_path / "run" / "server.sock"


def test_explicit_lock_and_socket_win(monkeypatch, tmp_path: Path) -> None:
    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    monkeypatch.setenv("MEGACHAT_LOCK_FILE", "/x/lock")
    monkeypatch.setenv("MEGACHAT_SOCKET", "/x/s.sock")
    assert instance.lock_file() == "/x/lock"
    assert default_socket_path() == Path("/x/s.sock")


def test_load_instance_env_never_overrides(monkeypatch, tmp_path: Path) -> None:
    (tmp_path / ".env").write_text(
        "# comment\nMC_T_A=1\nexport MC_T_B='two'\nMC_T_C=\"3\"\nMC_T_SET=file\nbad line\n"
    )
    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    monkeypatch.setenv("MC_T_SET", "env")
    for key in ("MC_T_A", "MC_T_B", "MC_T_C"):
        monkeypatch.delenv(key, raising=False)
    instance.load_instance_env()
    try:
        assert os.environ["MC_T_A"] == "1"
        assert os.environ["MC_T_B"] == "two"
        assert os.environ["MC_T_C"] == "3"
        assert os.environ["MC_T_SET"] == "env"
    finally:
        for key in ("MC_T_A", "MC_T_B", "MC_T_C"):
            os.environ.pop(key, None)


def test_config_reads_instance_files(monkeypatch, tmp_path: Path) -> None:
    from protocols import config

    (tmp_path / "config.json").write_text('{"whatsapp_session_name": "friend"}')
    (tmp_path / ".env").write_text("WAHA_API_KEY=instance-key\n")
    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    monkeypatch.delenv("WHATSAPP_SESSION_NAME", raising=False)
    monkeypatch.delenv("WHATSAPP_API_KEY", raising=False)
    assert config.get_whatsapp_session_name() == "friend"
    assert config.get_whatsapp_api_key() == "instance-key"
    assert config.get_telegram_session_path() == tmp_path / "data" / "telegram.session"


def test_auto_transcribe_writes_instance_config(monkeypatch, tmp_path: Path) -> None:
    from transcription import config as tconfig

    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    tconfig.set_auto_transcribe_global("whatsapp", True)
    assert '"whatsapp": true' in (tmp_path / "config.json").read_text()
    assert tconfig.get_auto_transcribe_global("whatsapp") is True


def test_import_time_constants_follow_instance(tmp_path: Path) -> None:
    """A fresh interpreter with MEGACHAT_HOME picks the instance paths/ports."""
    (tmp_path / ".env").write_text(
        "CLIENT_WEBHOOK_PORT=8098\nSIGNAL_DAEMON_PORT=8090\nMEGACHAT_DOWNLOAD_PORT=10043\n"
    )
    code = (
        "import protocols.db as d, protocols.rpc as r, protocols.webhook as w,"
        " protocols.download as dl, server.lock as l;"
        "print(d.DB_FILE); print(r.DAEMON_HTTP_PORT); print(w.WEBHOOK_PORT);"
        "print(dl.DOWNLOAD_PORT); print(l.LOCK_FILE); print(r.SIGNAL_CLI_ATTACHMENTS_DIR)"
    )
    env = {
        k: v
        for k, v in os.environ.items()
        if k
        not in (
            "CLIENT_WEBHOOK_PORT",
            "SIGNAL_DAEMON_PORT",
            "MEGACHAT_DOWNLOAD_PORT",
            "MEGACHAT_LOCK_FILE",
        )
    }
    env["MEGACHAT_HOME"] = str(tmp_path)
    out = subprocess.run(
        [sys.executable, "-c", code],
        cwd=str(PROJECT_ROOT),
        env=env,
        capture_output=True,
        text=True,
        timeout=60,
        check=True,
    ).stdout.splitlines()
    home = tmp_path.resolve()
    assert out == [
        str(home / "data" / "messages.db"),
        "8090",
        "8098",
        "10043",
        str(home / "run" / "signal-tui.lock"),
        str(home / "signal-cli" / "attachments"),
    ]
