"""Fixtures of the black-box contract suite (``make contract-test``).

The suite drives the Go ``megachatd`` binary exclusively through its wire
interfaces (Unix socket via the TUI's ``SocketClient``, HTTP, WebSocket).

Binary: ``$MEGACHATD_BIN`` if set (it must have been built with
``-tags contracttest``: the fake-backend hook of the send tests is compiled in
only for that tag), else ``go build -tags contracttest`` into a temp dir.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest
from harness import REPO, TOKEN, Server, short_tmp, start_server


@pytest.fixture(scope="session")
def go_binary() -> str:
    env = os.environ.get("MEGACHATD_BIN")
    if env:
        assert Path(env).is_file(), f"MEGACHATD_BIN={env} not found"
        return env
    go = shutil.which("go")
    if not go:
        pytest.skip("no Go toolchain and no $MEGACHATD_BIN")
    out = short_tmp("mcbin") / "megachatd"
    r = subprocess.run(
        [go, "build", "-tags", "contracttest", "-o", str(out), "./cmd/megachatd"],
        cwd=REPO / "megachatd", capture_output=True, text=True,
    )
    if r.returncode != 0:
        pytest.fail(f"go build failed:\n{r.stdout}\n{r.stderr}")
    return str(out)


@pytest.fixture(scope="session")
def seed_home():
    """A HOME holding the seeded SQLite DB (real Python schema) + media files."""
    root = short_tmp("mcseed")
    home = root / "home"
    home.mkdir()
    env = {**os.environ, "HOME": str(home)}
    r = subprocess.run(
        [sys.executable, "-I", str(Path(__file__).with_name("seed_db.py")), str(REPO), str(home)],
        env=env, capture_output=True, text=True, cwd=str(root),
    )
    if r.returncode != 0:
        pytest.fail(f"seeding failed:\n{r.stdout}\n{r.stderr}")
    yield home
    shutil.rmtree(root, ignore_errors=True)


@pytest.fixture(scope="session")
def go_server(go_binary) -> Server:
    """Go server, empty HOME, no backend; read-only tests share it."""
    srv = start_server("go", binary=go_binary)
    yield srv
    srv.stop()


@pytest.fixture
def go_fresh(go_binary) -> Server:
    """A private Go server per test (mutating ops, shutdown)."""
    srv = start_server("go", binary=go_binary)
    yield srv
    srv.stop()


@pytest.fixture
def go_signal(go_binary) -> Server:
    """A private Go server with SIGNAL_ENABLED=1 and no Signal account.

    The Signal backend is registered (ops and the startup event ring see it)
    but its connect worker stops at ErrNotConfigured: no daemon is spawned.
    """
    srv = start_server("go", binary=go_binary, extra_env={"SIGNAL_ENABLED": "1"})
    yield srv
    srv.stop()


@pytest.fixture(scope="session")
def go_seeded(go_binary, seed_home) -> Server:
    # SIGNAL_ENABLED=1 registers the Signal backend, which is what window /
    # snapshot read the seeded Signal rows through. No SIGNAL_USER_NUMBER and no
    # signal-cli on the fixture, so the connect worker stops at ErrNotConfigured
    # and no daemon is ever started.
    srv = start_server("go", binary=go_binary, seed_home=seed_home,
                       extra_env={"SIGNAL_ENABLED": "1"})
    yield srv
    srv.stop()


@pytest.fixture(scope="session")
def token() -> str:
    return TOKEN
