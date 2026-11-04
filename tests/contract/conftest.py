"""Fixtures of the black-box contract suite (``make contract-test``).

The suite drives the Go ``megachatd`` binary exclusively through its wire
interfaces (Unix socket via ``SocketClient``, HTTP, WebSocket).  Where the
Python dependencies are present it also runs the Python server on the same
seeded database, so the same calls can be diffed (parity).

Binary: ``$MEGACHATD_BIN`` if set, else ``go build`` into a temp dir.
"""

from __future__ import annotations

import importlib.util
import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest
from harness import REPO, TOKEN, Server, copy_python_project, short_tmp, start_server


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
        [go, "build", "-o", str(out), "./cmd/megachatd"],
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


@pytest.fixture(scope="session")
def go_seeded(go_binary, seed_home) -> Server:
    srv = start_server("go", binary=go_binary, seed_home=seed_home)
    yield srv
    srv.stop()


def _py_missing() -> str | None:
    for mod in ("fastapi", "uvicorn", "pydantic", "multipart", "websockets"):
        if importlib.util.find_spec(mod) is None:
            return mod
    return None


@pytest.fixture(scope="session")
def py_seeded(seed_home) -> Server:
    missing = _py_missing()
    if missing:
        pytest.skip(f"Python server dependency missing: {missing}")
    proj_root = short_tmp("mcpyproj")
    py_root = copy_python_project(proj_root / "megachat")
    try:
        # SIGNAL_ENABLED=1 only makes the Python Signal backend load its message cache
        # from the DB (what window/snapshot read); signal-cli does not exist, so no
        # connection is ever made.
        srv = start_server(
            "py", seed_home=seed_home, py_root=py_root,
            extra_env={"SIGNAL_ENABLED": "1", "SIGNAL_USER_NUMBER": "+15550100002"},
        )
    except RuntimeError as exc:
        pytest.skip(f"Python server could not start: {exc}")
    yield srv
    srv.stop()
    shutil.rmtree(proj_root, ignore_errors=True)


@pytest.fixture(scope="session")
def token() -> str:
    return TOKEN
