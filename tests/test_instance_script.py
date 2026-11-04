"""scripts/megachat_instance.sh: create → destroy → recreate (no docker, no systemd)."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
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
    for tool in ("docker", "systemctl", "rc-service", "rc-update"):
        stub = fakebin / tool
        stub.write_text(f'#!/bin/sh\necho "{tool} $*" >> "{log}"\nexit 0\n')
        stub.chmod(0o755)
    env = {
        **os.environ,
        "PATH": f"{fakebin}:{os.environ['PATH']}",
        "MEGACHAT_INSTANCES_DIR": str(tmp_path / "instances"),
        "XDG_CONFIG_HOME": str(tmp_path / "xdg"),
        "MEGACHAT_OPENRC_DIR": str(tmp_path / "initd"),
        "MEGACHAT_OPENRC_RUNLEVELS": str(tmp_path / "runlevels"),
        "MEGACHAT_PUBLIC_HOST": "myhost",
        "MEGACHAT_INIT": "systemd",
        # Interpreter of the test run: spawned servers use it, not the repo
        # .venv (absent in CI checkouts) or the system python.
        "PYTHON": sys.executable,
        # No real server behind the service stubs: skip the readiness wait.
        "MEGACHAT_SERVER_WAIT_S": "0",
    }
    # The suite's own lock/socket overrides would hide the instance's ones.
    for key in ("MEGACHAT_HOME", "MEGACHAT_LOCK_FILE", "MEGACHAT_SOCKET"):
        env.pop(key, None)
    (tmp_path / "initd").mkdir()

    def _run(
        *args: str, stdin: str = "", check: bool = True, extra: dict | None = None
    ):
        result = subprocess.run(
            ["bash", str(SCRIPT), *args],
            env={**env, **(extra or {})},
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
    # The choice is persisted so service starts (which run `up` again) match.
    assert "\nWAHA_DOCKER_LIMITS=0\n" in (root / "a1" / ".env").read_text()
    _run("up", "a1")
    assert "resources.yml" not in _compose_calls(log)[-1]

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


def test_openrc_install_start_stop_uninstall(run, tmp_path: Path) -> None:
    _run, _, log = run
    openrc = {"MEGACHAT_INIT": "openrc"}
    _run("create", "amico")
    service = tmp_path / "initd" / "megachat-amico"
    printed = _run("openrc", "amico", "print", extra=openrc).stdout
    assert printed.startswith("#!/sbin/openrc-run")
    assert "supervisor=supervise-daemon" in printed
    assert "MEGACHAT_HOME=" in printed and "serve --no-web" in printed
    assert (
        subprocess.run(["sh", "-n"], input=printed, text=True, check=False).returncode
        == 0
    )

    _run("install", "amico", extra=openrc)
    assert service.read_text() == printed
    calls = log.read_text()
    assert "rc-update add megachat-amico default" in calls
    assert "rc-service megachat-amico restart" in calls
    assert "service: openrc:installed" in _run("status", "amico").stdout

    out = _run("start", "amico", extra=openrc).stdout
    assert "via openrc" in out and "rc-service megachat-amico start" in log.read_text()
    _run("stop", "amico", extra=openrc)  # rc-service stub: status 0 = active
    assert "rc-service megachat-amico stop" in log.read_text()

    _run("uninstall", "amico", extra=openrc)
    assert not service.exists()
    assert "rc-update del megachat-amico default" in log.read_text()


def test_install_without_init_system(run) -> None:
    _run, _, _ = run
    _run("create", "amico")
    result = _run("install", "amico", check=False, extra={"MEGACHAT_INIT": "none"})
    assert result.returncode != 0 and "start amico" in result.stderr


def test_start_and_list_show_the_status_page_port(run) -> None:
    _run, root, _ = run
    _run("create", "amico1")
    _run("create", "amico2")
    token = json.loads((root / "amico1" / "config.json").read_text())["status_page"][
        "token"
    ]
    out = _run("start", "amico1").stdout
    try:
        assert f"port 4301: http://myhost:4301/{token}/" in out
        listed = _run("list").stdout.splitlines()
        assert (
            listed[0].split()[0] == "amico1" and "status/QR page port 4301" in listed[0]
        )
        assert "WAHA 3025" in listed[1] and "port 4302" in listed[1]
        assert "QR page: port 4301" in _run("status", "amico1").stdout
    finally:
        _run("stop", "amico1")
    _run("destroy", "amico2", "--yes")
    assert "destroyed" in _run("list").stdout.splitlines()[1]


def test_services_create_the_waha_container(run) -> None:
    _run, _, _ = run
    _run("create", "amico")
    unit = _run("systemd", "amico", "print").stdout
    assert "megachat_instance.sh up amico" in unit
    assert "Environment=MEGACHAT_INSTANCES_DIR=" in unit
    rc = _run("openrc", "amico", "print", extra={"MEGACHAT_INIT": "openrc"}).stdout
    assert "megachat_instance.sh' up 'amico'" in rc
    assert (
        subprocess.run(["sh", "-n"], input=rc, text=True, check=False).returncode == 0
    )


def test_doctor_reports_problems(run) -> None:
    _run, _, _ = run
    _run("create", "amico")
    result = _run("doctor", "amico", check=False)
    out = result.stdout
    assert result.returncode == 1
    assert "WAHA API (http://127.0.0.1:3015)" in out
    assert "no answer" in out  # nothing listens on 3015 in the tests
    assert "not running" in out
    assert "not listening yet" in out  # webhook: only a warning before linking
    assert "problem(s) found" in out


def test_up_fails_when_the_container_is_not_running(run, tmp_path: Path) -> None:
    """A silent `docker compose up` success must not hide a dead container."""
    _run, _, _ = run
    _run("create", "amico")
    fakebin = tmp_path / "dockerbin"
    fakebin.mkdir()
    stub = fakebin / "docker"
    stub.write_text(
        "#!/bin/sh\n"
        'case "$1" in\n'
        "  inspect) echo exited ;;\n"
        '  logs) echo "WAHA exited before binding" ;;\n'
        "esac\n"
        "exit 0\n"
    )
    stub.chmod(0o755)
    result = _run(
        "up",
        "amico",
        check=False,
        extra={"PATH": f"{fakebin}:{os.environ['PATH']}", "MEGACHAT_WAHA_WAIT_S": "0"},
    )
    assert result.returncode != 0
    assert "is 'exited' after 'up'" in result.stderr
    assert "WAHA exited before binding" in result.stderr


def test_probe_helper() -> None:
    helper = PROJECT_ROOT / "scripts" / "instance_config.py"
    result = subprocess.run(
        [sys.executable, str(helper), "probe", "http://127.0.0.1:9/"],
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0 and result.stdout.startswith("0 ")


def test_compose_pins_the_container_port(tmp_path: Path) -> None:
    """The instance .env sets WHATSAPP_API_PORT for the host side of the
    mapping; WAHA must still listen on 3000 inside the container."""
    import re

    compose = (PROJECT_ROOT / "docker-compose.yml").read_text()
    assert re.search(r'^\s+WHATSAPP_API_PORT: "3000"$', compose, re.MULTILINE)
    assert '"127.0.0.1:${WHATSAPP_API_PORT:-3005}:3000/tcp"' in compose
