"""
Regression tests for scripts/megachat-service.sh.

The script is bash, so these tests execute it as a subprocess.  Every path it
writes is redirected with ``--root <tmp>`` and the instance lock is redirected
with ``$MEGACHAT_LOCK_FILE``, so the real ``/etc``, ``/var`` and
``/tmp/signal-tui.lock`` are never touched.  ``--root`` also makes the script
skip systemctl / rc-service / rc-update; stub executables on PATH record any
invocation that would escape.
"""

from __future__ import annotations

import grp
import os
import pwd
import stat
import subprocess
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SERVICE_SCRIPT = PROJECT_ROOT / "scripts" / "megachat-service.sh"


# ─── Helpers ──────────────────────────────────────────────────────────────────


def _make_executable(path: Path) -> None:
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


def _write_stub(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    _make_executable(path)


def _non_root_user() -> pwd.struct_passwd:
    """A user the script accepts as the service user (any existing user)."""
    me = pwd.getpwuid(os.getuid())
    if me.pw_uid != 0:
        return me
    for entry in pwd.getpwall():
        if entry.pw_uid != 0 and entry.pw_dir:
            return entry
    pytest.skip("no non-root user available")


@pytest.fixture
def service_user() -> pwd.struct_passwd:
    return _non_root_user()


@pytest.fixture
def project(tmp_path: Path) -> Path:
    """A repo-like project dir; no megachatd binary unless a test adds one."""
    project = tmp_path / "project"
    (project / "megachatd" / "bin").mkdir(parents=True)
    return project


@pytest.fixture
def megachatd_bin(project: Path) -> Path:
    binary = project / "megachatd" / "bin" / "megachatd"
    _write_stub(binary, "#!/bin/sh\nexit 0\n")
    return binary


@pytest.fixture
def root(tmp_path: Path) -> Path:
    return tmp_path / "root"


@pytest.fixture
def fake_bin(tmp_path: Path) -> Path:
    """Stub init commands that record any call (they must stay unused)."""
    bin_dir = tmp_path / "fakebin"
    bin_dir.mkdir()
    marker = tmp_path / "init-called"
    for name in ("systemctl", "rc-service", "rc-update"):
        _write_stub(
            bin_dir / name,
            f'#!/bin/sh\nprintf "%s %s\\n" "{name}" "$*" >> {str(marker)!r}\n',
        )
    return bin_dir


def _env(tmp_path: Path, fake_bin: Path | None = None) -> dict[str, str]:
    env = {
        **os.environ,
        # Never read or write the real /tmp/signal-tui.lock (mc-cvp).
        "MEGACHAT_LOCK_FILE": str(tmp_path / "signal-tui.lock"),
        "HOME": str(tmp_path / "home"),
    }
    env.pop("SUDO_USER", None)  # the tests pass --user explicitly
    if fake_bin is not None:
        env["PATH"] = f"{fake_bin}:{os.environ['PATH']}"
    return env


def _run(
    tmp_path: Path,
    *args: str,
    fake_bin: Path | None = None,
    timeout: int = 60,
) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["bash", str(SERVICE_SCRIPT), *args],
        capture_output=True,
        text=True,
        cwd=tmp_path,
        env=_env(tmp_path, fake_bin),
        timeout=timeout,
        check=False,
    )


def _install_args(
    root: Path,
    user: str,
    project: Path,
    *,
    init: str,
    binary: Path | None = None,
    name: str = "megachat-test",
) -> list[str]:
    args = [
        "--root",
        str(root),
        "install",
        "--init",
        init,
        "--user",
        user,
        "--project-dir",
        str(project),
        "--name",
        name,
    ]
    if binary is not None:
        args += ["--bin", str(binary)]
    return args


def _socket_for(user: pwd.struct_passwd) -> Path:
    return Path(user.pw_dir) / ".local" / "state" / "megachat" / "server.sock"


def _group_name(user: pwd.struct_passwd) -> str:
    return grp.getgrgid(user.pw_gid).gr_name


# ─── systemd ─────────────────────────────────────────────────────────────────


class TestSystemdInstall:
    def test_renders_unit(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
                name="svc-test",
            ),
            "--extra",
            "--web-port",
            "--extra",
            "8080",
        )
        assert result.returncode == 0, result.stderr

        unit = root / "etc" / "systemd" / "system" / "svc-test.service"
        assert unit.is_file()
        text = unit.read_text(encoding="utf-8")
        socket = _socket_for(service_user)
        assert f"User={service_user.pw_name}" in text
        assert f"Group={_group_name(service_user)}" in text
        assert f"WorkingDirectory={project}" in text
        assert f"Environment=HOME={service_user.pw_dir}" in text
        assert (
            f"ExecStart={megachatd_bin} serve --project-dir {project} "
            f"--socket {socket} --web-port 8080"
        ) in text
        assert "Restart=on-failure" in text
        assert "After=network-online.target" in text
        # A system service has no login session: $XDG_RUNTIME_DIR is unusable,
        # and the TUI/server lock lives in /tmp (so no PrivateTmp).
        assert "PrivateTmp" not in text

    def test_default_name_and_project_binary(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        root = tmp_path / "root"
        result = _run(
            tmp_path,
            "--root",
            str(root),
            "install",
            "--init",
            "systemd",
            "--user",
            service_user.pw_name,
            "--project-dir",
            str(project),
        )
        assert result.returncode == 0, result.stderr
        unit = root / "etc" / "systemd" / "system" / "megachat.service"
        assert unit.is_file()
        assert f"ExecStart={megachatd_bin} serve" in unit.read_text(encoding="utf-8")


# ─── OpenRC ──────────────────────────────────────────────────────────────────


class TestOpenRCInstall:
    def test_renders_init_script(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="openrc",
                binary=megachatd_bin,
                name="svc-rc",
            ),
            "--extra",
            "--web-port",
            "--extra",
            "8080",
        )
        assert result.returncode == 0, result.stderr

        init_script = root / "etc" / "init.d" / "svc-rc"
        assert init_script.is_file()
        assert os.access(init_script, os.X_OK)
        text = init_script.read_text(encoding="utf-8")
        socket = _socket_for(service_user)
        assert text.startswith("#!/sbin/openrc-run\n")
        assert f"command={megachatd_bin}" in text
        assert (
            f'command_args="serve --project-dir {project} '
            f'--socket {socket} --web-port 8080"'
        ) in text
        assert (
            f'command_user="{service_user.pw_name}:{_group_name(service_user)}"'
        ) in text
        assert f"directory={project}" in text
        assert 'supervisor="supervise-daemon"' in text
        assert "respawn_delay=" in text
        assert f"export HOME={service_user.pw_dir}" in text
        assert "PrivateTmp" not in text

        log = root / "var" / "log" / "svc-rc.log"
        assert log.is_file()
        assert f"output_log={log}" in text
        assert f"error_log={log}" in text


# ─── safety / validation ─────────────────────────────────────────────────────


class TestRefusals:
    def test_accepts_root_as_service_user(
        self, tmp_path: Path, project: Path, megachatd_bin: Path, root: Path
    ):
        result = _run(
            tmp_path,
            *_install_args(root, "root", project, init="systemd", binary=megachatd_bin),
        )
        assert result.returncode == 0, result.stderr
        unit = root / "etc" / "systemd" / "system" / "megachat-test.service"
        assert "User=root" in unit.read_text()

    def test_unknown_user_is_refused(
        self, tmp_path: Path, project: Path, megachatd_bin: Path, root: Path
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root, "no-such-user-mc", project, init="systemd", binary=megachatd_bin
            ),
        )
        assert result.returncode != 0
        assert "unknown user" in result.stderr

    def test_missing_binary_has_clear_error(
        self, tmp_path: Path, project: Path, root: Path, service_user: pwd.struct_passwd
    ):
        missing = tmp_path / "no-such-megachatd"
        result = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=missing,
            ),
        )
        assert result.returncode != 0
        assert "make megachatd" in result.stderr
        assert not (
            root / "etc" / "systemd" / "system" / "megachat-test.service"
        ).exists()

    def test_refuses_while_another_server_holds_the_lock(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        root: Path,
        service_user: pwd.struct_passwd,
    ):
        # pytest itself is a live pid, exactly what the lock check looks for.
        (tmp_path / "signal-tui.lock").write_text(f"{os.getpid()}\n", encoding="utf-8")
        result = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
        )
        assert result.returncode != 0
        assert "launcher.py server stop" in result.stderr
        assert not (
            root / "etc" / "systemd" / "system" / "megachat-test.service"
        ).exists()

    def test_rejects_unknown_init(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        root: Path,
        service_user: pwd.struct_passwd,
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root, service_user.pw_name, project, init="sysv", binary=megachatd_bin
            ),
        )
        assert result.returncode != 0
        assert "systemd" in result.stderr and "openrc" in result.stderr

    def test_install_needs_root_without_dry_run_or_root(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        if os.getuid() == 0:
            pytest.skip("running as root")
        result = _run(
            tmp_path,
            "install",
            "--init",
            "systemd",
            "--user",
            service_user.pw_name,
            "--project-dir",
            str(project),
            "--bin",
            str(megachatd_bin),
        )
        assert result.returncode != 0
        assert "root" in result.stderr


# ─── uninstall ───────────────────────────────────────────────────────────────


class TestUninstall:
    def test_systemd_uninstall_removes_unit(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        install = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
        )
        assert install.returncode == 0, install.stderr
        unit = root / "etc" / "systemd" / "system" / "megachat-test.service"
        assert unit.is_file()

        result = _run(
            tmp_path,
            "--root",
            str(root),
            "uninstall",
            "--init",
            "systemd",
            "--name",
            "megachat-test",
        )
        assert result.returncode == 0, result.stderr
        assert not unit.exists()

    def test_openrc_uninstall_removes_init_and_keeps_log(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        install = _run(
            tmp_path,
            *_install_args(
                root, service_user.pw_name, project, init="openrc", binary=megachatd_bin
            ),
        )
        assert install.returncode == 0, install.stderr
        init_script = root / "etc" / "init.d" / "megachat-test"
        log = root / "var" / "log" / "megachat-test.log"
        assert init_script.is_file() and log.is_file()

        result = _run(
            tmp_path,
            "--root",
            str(root),
            "uninstall",
            "--init",
            "openrc",
            "--name",
            "megachat-test",
        )
        assert result.returncode == 0, result.stderr
        assert not init_script.exists()
        assert log.exists(), "logs stay unless --purge-logs is given"

    def test_purge_logs_removes_only_the_log(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        install = _run(
            tmp_path,
            *_install_args(
                root, service_user.pw_name, project, init="openrc", binary=megachatd_bin
            ),
        )
        assert install.returncode == 0, install.stderr
        log = root / "var" / "log" / "megachat-test.log"
        assert log.is_file()

        result = _run(
            tmp_path,
            "--root",
            str(root),
            "uninstall",
            "--init",
            "openrc",
            "--name",
            "megachat-test",
            "--purge-logs",
        )
        assert result.returncode == 0, result.stderr
        assert not log.exists()

    def test_uninstall_leaves_project_data_alone(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        # config.json (and the data dirs it points at) belong to the user.
        config = project / "config.json"
        config.write_text("{}", encoding="utf-8")
        state = project / ".signal-cli-data"
        state.mkdir()
        install = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
        )
        assert install.returncode == 0, install.stderr

        result = _run(
            tmp_path,
            "--root",
            str(root),
            "uninstall",
            "--init",
            "systemd",
            "--name",
            "megachat-test",
            "--purge-logs",
        )
        assert result.returncode == 0, result.stderr
        assert config.is_file()
        assert state.is_dir()


# ─── dry-run and --root ──────────────────────────────────────────────────────


class TestDryRun:
    def test_writes_nothing_and_prints_unit(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        root = tmp_path / "root"
        result = _run(
            tmp_path,
            "--dry-run",
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
        )
        assert result.returncode == 0, result.stderr
        assert not root.exists()
        assert "# would write" in result.stdout
        assert "ExecStart=" in result.stdout
        assert "# would run: systemctl" in result.stdout

    def test_openrc_dry_run_writes_nothing(
        self,
        tmp_path: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        root = tmp_path / "root"
        result = _run(
            tmp_path,
            "--dry-run",
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="openrc",
                binary=megachatd_bin,
            ),
        )
        assert result.returncode == 0, result.stderr
        assert not root.exists()
        assert "# would write" in result.stdout
        assert "supervisor=" in result.stdout
        assert "# would run: rc-update" in result.stdout


class TestRootNeverCallsInitTools:
    def test_systemd_install_skips_systemctl(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
        fake_bin: Path,
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
            fake_bin=fake_bin,
        )
        assert result.returncode == 0, result.stderr
        assert not (tmp_path / "init-called").exists()

    def test_openrc_install_skips_rc_commands(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
        fake_bin: Path,
    ):
        result = _run(
            tmp_path,
            *_install_args(
                root, service_user.pw_name, project, init="openrc", binary=megachatd_bin
            ),
            fake_bin=fake_bin,
        )
        assert result.returncode == 0, result.stderr
        assert not (tmp_path / "init-called").exists()


# ─── status ─────────────────────────────────────────────────────────────────


class TestStatus:
    def test_systemd_status_shows_path_and_unknown_state_under_root(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        install = _run(
            tmp_path,
            *_install_args(
                root,
                service_user.pw_name,
                project,
                init="systemd",
                binary=megachatd_bin,
            ),
        )
        assert install.returncode == 0, install.stderr
        result = _run(
            tmp_path,
            "--root",
            str(root),
            "status",
            "--init",
            "systemd",
            "--name",
            "megachat-test",
        )
        assert result.returncode == 0, result.stderr
        assert "init: systemd" in result.stdout
        assert (
            str(root / "etc" / "systemd" / "system" / "megachat-test.service")
            in result.stdout
        )
        assert "enabled: unknown (--root)" in result.stdout
        assert "active: unknown (--root)" in result.stdout

    def test_openrc_status(
        self,
        tmp_path: Path,
        root: Path,
        project: Path,
        megachatd_bin: Path,
        service_user: pwd.struct_passwd,
    ):
        install = _run(
            tmp_path,
            *_install_args(
                root, service_user.pw_name, project, init="openrc", binary=megachatd_bin
            ),
        )
        assert install.returncode == 0, install.stderr
        result = _run(
            tmp_path,
            "--root",
            str(root),
            "status",
            "--init",
            "openrc",
            "--name",
            "megachat-test",
        )
        assert result.returncode == 0, result.stderr
        assert "init: openrc" in result.stdout
        assert str(root / "etc" / "init.d" / "megachat-test") in result.stdout
        assert "unknown (--root)" in result.stdout


def test_default_user_ignores_a_stale_sudo_user_when_not_root(tmp_path):
    """Without root, the service user defaults to whoever runs the script,
    not to a leftover $SUDO_USER from an outer sudo."""
    import getpass

    if os.geteuid() == 0:
        pytest.skip("must run as a non-root user")
    binary = tmp_path / "megachatd"
    binary.write_text("#!/bin/sh\n")
    binary.chmod(0o755)
    env = {
        **os.environ,
        "SUDO_USER": "someone-else",
        "MEGACHAT_LOCK_FILE": str(tmp_path / "lock"),
    }
    result = subprocess.run(
        [
            "bash",
            str(SERVICE_SCRIPT),
            "--root",
            str(tmp_path / "root"),
            "--init",
            "systemd",
            "install",
            "--bin",
            str(binary),
        ],
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    unit = (tmp_path / "root" / "etc/systemd/system/megachat.service").read_text()
    assert f"User={getpass.getuser()}\n" in unit
