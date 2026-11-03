"""scripts/megachat_instance.sh: create → destroy → recreate (no docker, no systemd)."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SCRIPT = PROJECT_ROOT / "scripts" / "megachat_instance.sh"

pytestmark = pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")


@pytest.fixture
def run(tmp_path: Path):
    fakebin = tmp_path / "bin"
    fakebin.mkdir()
    log = tmp_path / "calls.log"
    for tool in ("docker", "systemctl"):
        stub = fakebin / tool
        stub.write_text(f'#!/bin/sh\necho "{tool} $*" >> "{log}"\nexit 0\n')
        stub.chmod(0o755)
    env = {
        **os.environ,
        "PATH": f"{fakebin}:{os.environ['PATH']}",
        "MEGACHAT_INSTANCES_DIR": str(tmp_path / "instances"),
        "XDG_CONFIG_HOME": str(tmp_path / "xdg"),
    }
    env.pop("MEGACHAT_HOME", None)

    def _run(*args: str, stdin: str = "", check: bool = True):
        result = subprocess.run(
            ["bash", str(SCRIPT), *args],
            env=env,
            input=stdin,
            capture_output=True,
            text=True,
            timeout=60,
            check=False,
        )
        if check:
            assert result.returncode == 0, result.stdout + result.stderr
        return result

    return _run, tmp_path / "instances", log


def test_create_assigns_offsets_and_status_page(run) -> None:
    _run, root, _ = run
    _run("create", "amico1")
    _run("create", "amico2")
    env2 = (root / "amico2" / ".env").read_text()
    assert "MEGACHAT_PORT_OFFSET=2" in env2
    assert "WHATSAPP_API_PORT=3025" in env2
    assert "CLIENT_WEBHOOK_PORT=8108" in env2
    config = json.loads((root / "amico2" / "config.json").read_text())
    assert config["auto_transcribe"] == {"whatsapp": True}
    assert config["status_page"]["port"] == 4302
    assert config["status_page"]["enabled"] is True
    url = _run("url", "amico2").stdout.strip()
    assert url.endswith(f":4302/{config['status_page']['token']}/")
    assert _run("create", "amico2", check=False).returncode != 0
    assert _run("create", "Bad", check=False).returncode != 0


def test_destroy_keeps_config_and_recreate_reuses_it(run) -> None:
    _run, root, log = run
    _run("create", "amico")
    home = root / "amico"
    env_before = (home / ".env").read_text()
    config_before = (home / "config.json").read_text()
    (home / "data" / "messages.db").write_text("x")
    (home / "whatsapp-data" / "session").write_text("x")

    assert _run("destroy", "amico", stdin="wrong\n", check=False).returncode != 0
    assert (home / "data" / "messages.db").exists()

    _run("destroy", "amico", stdin="amico\n")
    assert sorted(p.name for p in home.iterdir()) == [
        ".destroyed",
        ".env",
        "config.json",
    ]
    assert "docker compose -p megachat-amico" in log.read_text()
    assert "destroyed" in _run("status").stdout
    assert _run("start", "amico", check=False).returncode != 0

    # A new instance does not take the destroyed one's ports.
    _run("create", "other")
    assert "MEGACHAT_PORT_OFFSET=2" in (root / "other" / ".env").read_text()

    assert _run("create", "amico", "--offset", "5", check=False).returncode != 0
    out = _run("create", "amico").stdout
    assert "recreated" in out
    assert (home / ".env").read_text() == env_before
    assert (home / "config.json").read_text() == config_before
    assert not (home / ".destroyed").exists()
    assert (home / "data").is_dir() and not (home / "data" / "messages.db").exists()


def test_destroy_purge(run) -> None:
    _run, root, _ = run
    _run("create", "amico")
    _run("destroy", "amico", "--purge", "--yes")
    assert not (root / "amico").exists()


def test_systemd_install_and_uninstall(run, tmp_path: Path) -> None:
    _run, _, log = run
    _run("create", "amico")
    unit = tmp_path / "xdg" / "systemd" / "user" / "megachat-amico.service"
    printed = _run("systemd", "amico", "print").stdout
    assert "Environment=MEGACHAT_HOME=" in printed and "serve --no-web" in printed
    _run("systemd", "amico", "install")
    assert unit.read_text() == printed
    calls = log.read_text()
    assert "systemctl --user enable megachat-amico.service" in calls
    _run("systemd", "amico", "uninstall")
    assert not unit.exists()
    assert "systemctl --user disable --now megachat-amico.service" in log.read_text()


def test_help(run) -> None:
    _run, root, _ = run
    overview = _run("help").stdout
    assert "AVVIO RAPIDO" in overview and "destroy NOME" in overview
    assert "@S@" not in overview
    full = _run("help", "all").stdout
    for topic in (
        "create NOME",
        "destroy NOME",
        "systemd NOME",
        "url NOME",
        "RISOLUZIONE",
    ):
        assert topic in full
    assert "Spegne e cancella" in _run("help", "destroy").stdout
    _run("create", "amico")
    # `--help` anywhere shows the command guide and never runs the command.
    assert "Spegne e cancella" in _run("destroy", "amico", "--help").stdout
    assert (root / "amico" / "data").is_dir()
    assert _run("help", "nope", check=False).returncode == 1
    assert _run("nope", check=False).returncode == 1


def _compose_calls(log: Path) -> list[str]:
    return [line for line in log.read_text().splitlines() if "docker compose" in line]


def test_docker_limits_and_extra_compose_files(run, tmp_path: Path) -> None:
    _run, root, log = run
    extra = tmp_path / "nolimits.yml"
    extra.write_text("services:\n  whatsapp: {}\n")

    _run("create", "a1")
    _run("up", "a1")
    assert "-f docker-compose.resources.yml" in _compose_calls(log)[-1].replace(
        str(PROJECT_ROOT) + "/", ""
    )

    _run("up", "a1", "--no-docker-limits", "--compose-file", str(extra))
    last = _compose_calls(log)[-1]
    assert "resources.yml" not in last and last.endswith(f"-f {extra} up -d")

    _run("create", "a2", "--no-docker-limits", "--compose-file", str(extra))
    env = (root / "a2" / ".env").read_text()
    assert "\nWAHA_DOCKER_LIMITS=0\n" in env
    assert f"\nWAHA_COMPOSE_FILES={extra}\n" in env
    _run("up", "a2")
    last = _compose_calls(log)[-1]
    assert "resources.yml" not in last and f"-f {extra}" in last

    # A missing extra file stops before docker is called.
    calls = len(_compose_calls(log))
    assert _run("up", "a1", "--compose-file", "/nope.yml", check=False).returncode != 0
    (root / "a2" / ".env").write_text(env.replace(str(extra), "missing.yml"))
    assert _run("up", "a2", check=False).returncode != 0
    assert len(_compose_calls(log)) == calls

    # A recreated instance keeps its .env: docker options are refused.
    _run("destroy", "a1", "--yes")
    assert _run("create", "a1", "--no-docker-limits", check=False).returncode != 0


def test_top_level_install_alias(run, tmp_path: Path) -> None:
    _run, _, log = run
    _run("create", "amico")
    _run("install", "amico")
    unit = tmp_path / "xdg" / "systemd" / "user" / "megachat-amico.service"
    assert unit.exists()
    assert "systemctl --user enable megachat-amico.service" in log.read_text()
    _run("uninstall", "amico")
    assert not unit.exists()
    overview = _run("help").stdout
    assert "install NOME" in overview and "--no-docker-limits" in overview
    assert "WAHA_COMPOSE_FILES" in _run("help", "docker").stdout
