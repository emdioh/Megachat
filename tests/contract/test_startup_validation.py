"""``megachatd serve`` refuses to start when an enabled integration lacks its
settings (CONTRACTS.md 9.1): exit code 1, the whole list on stderr, and no
adapter ever listens."""

from __future__ import annotations

import os
import shutil
import subprocess

from harness import _SCRUB, short_tmp


def _serve_env(root):
    env = {k: v for k, v in os.environ.items() if k not in _SCRUB}
    run = root / "run"
    run.mkdir(exist_ok=True)
    home = root / "home"
    home.mkdir(exist_ok=True)
    proj = root / "proj"
    proj.mkdir(exist_ok=True)
    env.update(
        HOME=str(home),
        XDG_RUNTIME_DIR=str(run),
        MEGACHAT_LOCK_FILE=str(root / "lock"),
        MEGACHAT_LOG_FILE=str(root / "server.log"),
        MEGACHAT_PROJECT_DIR=str(proj),
        SIGNAL_ENABLED="0",
        WHATSAPP_ENABLED="0",
        TELEGRAM_ENABLED="0",
        GMAIL_ENABLED="0",
    )
    return env, proj


def test_serve_exits_nonzero_naming_every_missing_setting(go_binary):
    root = short_tmp("mcval")
    try:
        env, proj = _serve_env(root)
        env.update(
            TELEGRAM_ENABLED="1",
            TELEGRAM_API_ID="",
            TELEGRAM_API_HASH="",
            GMAIL_ENABLED="1",
            GMAIL_ADDRESS="me@example.com",
            GMAIL_APP_PASSWORD="",
        )
        sock = root / "s.sock"
        result = subprocess.run(
            [go_binary, "serve", "--project-dir", str(proj),
             "--no-web", "--socket", str(sock)],
            env=env, cwd=str(root), capture_output=True, text=True, timeout=30,
        )

        assert result.returncode != 0
        assert (
            "telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID), "
            "telegram.api_hash (TELEGRAM_API_HASH)"
        ) in result.stderr
        assert (
            "gmail is enabled but missing: one of gmail.app_password / "
            "gmail.app_password_file / gmail.app_password_cmd"
        ) in result.stderr
        assert "telegram.enabled=false / gmail.enabled=false" in result.stderr
        # Validation runs before any adapter exists and before the lock is held.
        assert not sock.exists()
        assert not (root / "lock").exists()
    finally:
        shutil.rmtree(root, ignore_errors=True)
