"""
Regression tests for install.sh — the automatic installation script.

The script is a bash script, so these tests execute it as a subprocess in an
isolated temporary directory (tmp_path). Network calls (curl/wget) and system
commands (python3, java, pip) are mocked with stub executables placed in a fake
PATH, so no real downloads or installs happen. A stub ``megachatd`` stands in
for an already-installed release binary.
"""

from __future__ import annotations

import io
import json
import os
import shutil
import stat
import subprocess
import sys
import tarfile
import time
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parent.parent
INSTALL_SCRIPT = PROJECT_ROOT / "install.sh"
REAL_PYTHON = sys.executable


# ─── Helpers ──────────────────────────────────────────────────────────────────


def _make_executable(path: Path) -> None:
    """Make a file executable."""
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


def _write_stub(path: Path, content: str) -> None:
    """Write an executable stub script."""
    path.write_text(content, encoding="utf-8")
    _make_executable(path)


def _create_fake_signal_cli(bin_dir: Path, version: str) -> Path:
    """Create a fake signal-cli install with the expected structure.

    Returns the path to the fake executable.
    """
    exe = bin_dir / f"signal-cli-{version}" / "bin" / "signal-cli"
    exe.parent.mkdir(parents=True, exist_ok=True)
    exe.write_text("#!/bin/sh\necho fake-signal-cli\n", encoding="utf-8")
    _make_executable(exe)
    # Add a lib/ dir to mimic the JVM build
    (exe.parent.parent / "lib").mkdir(parents=True, exist_ok=True)
    return exe


def _create_fake_tarball(path: Path, version: str) -> None:
    """Create a fake signal-cli tarball with the expected structure."""
    with tarfile.open(path, "w:gz") as tar:
        bin_info = tarfile.TarInfo(f"signal-cli-{version}/bin/signal-cli")
        bin_data = b"#!/bin/sh\necho fake\n"
        bin_info.size = len(bin_data)
        bin_info.mode = 0o755
        tar.addfile(bin_info, io.BytesIO(bin_data))
        lib_info = tarfile.TarInfo(f"signal-cli-{version}/lib/placeholder.jar")
        lib_data = b"fake-jar"
        lib_info.size = len(lib_data)
        tar.addfile(lib_info, io.BytesIO(lib_data))


def _extract_function(script: Path, func_name: str) -> str:
    """Extract a single bash function definition from install.sh.

    Returns the function body (without the surrounding main script), so it can
    be sourced without executing the whole installer.
    """
    lines = script.read_text(encoding="utf-8").splitlines()
    start = None
    for i, line in enumerate(lines):
        if line.startswith(f"{func_name}()"):
            start = i
            break
    if start is None:
        raise ValueError(f"Function {func_name} not found")
    # Find the closing brace at column 0
    for i in range(start, len(lines)):
        if lines[i].strip() == "}":
            return "\n".join(lines[start : i + 1])
    raise ValueError(f"Function {func_name} not closed")


@pytest.fixture
def fake_bin(tmp_path: Path) -> Path:
    """Create a fake bin/ directory with a stub signal-cli install."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    _create_fake_signal_cli(bin_dir, "0.14.6")
    return bin_dir


@pytest.fixture
def fake_path(tmp_path: Path, monkeypatch) -> Path:
    """Create a fake PATH with stub executables for curl, wget, python3, java, pip.

    Returns the fake bin directory.
    """
    fake_bin_dir = tmp_path / "fakebin"
    fake_bin_dir.mkdir()

    # curl stub: if the URL contains "releases/latest", print the latest version;
    # otherwise download a fake tarball using the real python.
    curl_stub = f"""#!/bin/sh
if echo "$*" | grep -q "releases/latest"; then
    echo '{{"tag_name": "v0.14.7"}}'
    exit 0
fi
out=""
prev=""
for a in "$@"; do
    if [ "$prev" = "-o" ]; then out="$a"; fi
    prev="$a"
done
if [ -n "$out" ]; then
    version="$(echo "$out" | sed 's/.*signal-cli-\\([0-9.]*\\)\\.tar\\.gz/\\1/')"
    {REAL_PYTHON} -c "import sys; sys.path.insert(0, '{tmp_path}'); from test_helpers import make_tarball; make_tarball('$out', '$version')"
    exit 0
fi
exit 1
"""
    _write_stub(fake_bin_dir / "curl", curl_stub)

    # wget stub: same behaviour as curl.
    wget_stub = f"""#!/bin/sh
if echo "$*" | grep -q "releases/latest"; then
    echo '{{"tag_name": "v0.14.7"}}'
    exit 0
fi
out=""
prev=""
for a in "$@"; do
    if [ "$prev" = "-O" ]; then out="$a"; fi
    prev="$a"
done
if [ -n "$out" ]; then
    version="$(echo "$out" | sed 's/.*signal-cli-\\([0-9.]*\\)\\.tar\\.gz/\\1/')"
    {REAL_PYTHON} -c "import sys; sys.path.insert(0, '{tmp_path}'); from test_helpers import make_tarball; make_tarball('$out', '$version')"
    exit 0
fi
exit 1
"""
    _write_stub(fake_bin_dir / "wget", wget_stub)

    # python3 stub: supports --version, -c (version detection), -m venv, -m pip.
    python_stub = """#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "Python 3.13.3"
    exit 0
fi
if [ "$1" = "-c" ]; then
    echo "3.13"
    exit 0
fi
if [ "$1" = "-m" ] && [ "$2" = "venv" ]; then
    mkdir -p "$3/bin"
    cat > "$3/bin/pip" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$PIP_ARGS_LOG"
case "$*" in
    *requirements-web.txt*)
        if [ "${PIP_FAIL_WEB:-0}" = "1" ]; then exit 1; fi
        ;;
esac
exit 0
EOF
    touch "$3/bin/python"
    chmod +x "$3/bin/pip" "$3/bin/python"
    exit 0
fi
if [ "$1" = "-m" ] && [ "$2" = "pip" ]; then
    exit 0
fi
exit 0
"""
    _write_stub(fake_bin_dir / "python3", python_stub)

    # java stub: reports Java 25.
    java_stub = """#!/bin/sh
echo 'openjdk version "25.0.1" 2025-04-15'
exit 0
"""
    _write_stub(fake_bin_dir / "java", java_stub)

    # pip stub: no-op.
    pip_stub = """#!/bin/sh
exit 0
"""
    _write_stub(fake_bin_dir / "pip", pip_stub)
    _write_stub(fake_bin_dir / "pip3", pip_stub)

    # megachatd stub: a release binary is already installed, so the installer
    # does not need Go (the tmp copy has no megachatd sources).
    _write_stub(fake_bin_dir / "megachatd", "#!/bin/sh\nexit 0\n")

    # tar is real (needed to extract the fake tarball).
    monkeypatch.setenv("PIP_ARGS_LOG", str(tmp_path / "pip_args.log"))
    monkeypatch.setenv("PATH", f"{fake_bin_dir}:{os.environ['PATH']}")
    return fake_bin_dir


@pytest.fixture
def test_helpers(tmp_path: Path) -> Path:
    """Create a test_helpers.py module in tmp_path for the curl/wget stubs."""
    helpers = tmp_path / "test_helpers.py"
    helpers.write_text(
        "import tarfile, io\n"
        "def make_tarball(path, version):\n"
        "    with tarfile.open(path, 'w:gz') as tar:\n"
        "        bin_info = tarfile.TarInfo(f'signal-cli-{version}/bin/signal-cli')\n"
        "        data = b'#!/bin/sh\\necho fake\\n'\n"
        "        bin_info.size = len(data)\n"
        "        bin_info.mode = 0o755\n"
        "        tar.addfile(bin_info, io.BytesIO(data))\n"
        "        lib_info = tarfile.TarInfo(f'signal-cli-{version}/lib/placeholder.jar')\n"
        "        lib_data = b'fake-jar'\n"
        "        lib_info.size = len(lib_data)\n"
        "        tar.addfile(lib_info, io.BytesIO(lib_data))\n",
        encoding="utf-8",
    )
    return helpers


def _run_install(tmp_path: Path, *args: str) -> subprocess.CompletedProcess:
    """Copy install.sh into tmp_path and run it with the given args."""
    script = tmp_path / "install.sh"
    fake_home = tmp_path / "home"
    fake_home.mkdir(exist_ok=True)
    shutil.copy(INSTALL_SCRIPT, script)
    _make_executable(script)
    return subprocess.run(  # noqa: PLW1510 — return code inspected by each test
        ["bash", str(script), *args],
        capture_output=True,
        text=True,
        cwd=str(tmp_path),
        env={
            **os.environ,
            "HOME": str(fake_home),
            "SHELL": "/bin/bash",
            "MEGACHATD_BIN": "",
        },
        timeout=60,
    )


# ─── Tests ────────────────────────────────────────────────────────────────────


class TestInstallScriptHelp:
    """📖 Help e parsing argomenti."""

    def test_help_flag(self, tmp_path: Path):
        """--help mostra l'uso ed esce con 0."""
        result = _run_install(tmp_path, "--help")
        assert result.returncode == 0
        assert "install.sh" in result.stdout
        assert "--update" in result.stdout

    def test_unknown_flag(self, tmp_path: Path):
        """Flag sconosciuto → errore, exit ≠ 0."""
        result = _run_install(tmp_path, "--bogus")
        assert result.returncode != 0
        assert "Opzione sconosciuta" in result.stderr

    def test_missing_version_arg(self, tmp_path: Path):
        """--version senza argomento → errore."""
        result = _run_install(tmp_path, "--version")
        assert result.returncode != 0
        assert "--version richiede" in result.stderr


class TestInstalledVersionDetection:
    """🔍 Rilevamento versione installata."""

    def test_installed_version_detected(self, tmp_path: Path, fake_bin: Path):
        """Rileva la versione da bin/signal-cli-*/."""
        func = _extract_function(INSTALL_SCRIPT, "get_installed_version")
        script = tmp_path / "detect.sh"
        script.write_text(
            f"{func}\nBIN_DIR='{fake_bin}'\necho \"$(get_installed_version)\"\n",
            encoding="utf-8",
        )
        result = subprocess.run(  # noqa: PLW1510 — return code asserted below
            ["bash", str(script)], capture_output=True, text=True, timeout=30
        )
        assert result.returncode == 0
        assert result.stdout.strip() == "0.14.6"

    def test_no_installed_version(self, tmp_path: Path):
        """Nessuna versione in bin/ → stringa vuota."""
        empty_bin = tmp_path / "bin"
        empty_bin.mkdir()
        func = _extract_function(INSTALL_SCRIPT, "get_installed_version")
        script = tmp_path / "detect.sh"
        script.write_text(
            f"{func}\nBIN_DIR='{empty_bin}'\necho \"[$(get_installed_version)]\"\n",
            encoding="utf-8",
        )
        result = subprocess.run(  # noqa: PLW1510 — return code asserted below
            ["bash", str(script)], capture_output=True, text=True, timeout=30
        )
        assert result.returncode == 0
        assert result.stdout.strip() == "[]"


class TestUpdateFlag:
    """🔄 Aggiornamento signal-cli."""

    def test_update_already_latest(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """--update con versione già aggiornata → nessun download."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.7")

        result = _run_install(tmp_path, "--update")
        assert result.returncode == 0
        assert "già all'ultima versione" in result.stdout
        assert "Scaricamento signal-cli" not in result.stdout

    def test_update_downloads_new(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """--update con versione vecchia → scarica la nuova e rimuove la vecchia."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.6")

        result = _run_install(tmp_path, "--update")
        assert result.returncode == 0
        assert "Scaricamento signal-cli v0.14.7" in result.stdout
        assert "Rimozione vecchia versione" in result.stdout
        assert (bin_dir / "signal-cli-0.14.6").exists() is False
        assert (bin_dir / "signal-cli-0.14.7").exists() is True


class TestSkipSignalCli:
    """⏭️ Flag --skip-signal-cli."""

    def test_skip_no_version(self, tmp_path: Path, fake_path: Path, test_helpers: Path):
        """--skip-signal-cli senza versioni → warning."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode == 0
        assert "Nessuna versione di signal-cli trovata" in result.stdout

    def test_skip_with_version(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """--skip-signal-cli con versione presente → OK."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.6")
        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode == 0
        assert "signal-cli v0.14.6 trovato" in result.stdout


class TestDownload:
    """⬇️ Download di signal-cli."""

    def test_download_specific_version(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """--version X.Y.Z → scarica quella versione specifica."""
        result = _run_install(tmp_path, "--version", "0.14.7", "--no-venv")
        assert result.returncode == 0
        assert "Scaricamento signal-cli v0.14.7" in result.stdout
        assert (tmp_path / "bin" / "signal-cli-0.14.7" / "bin" / "signal-cli").exists()

    def test_download_creates_correct_structure(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """Dopo il download, la struttura bin/signal-cli-*/bin/signal-cli è corretta."""
        result = _run_install(tmp_path, "--version", "0.14.7", "--no-venv")
        assert result.returncode == 0
        exe = tmp_path / "bin" / "signal-cli-0.14.7" / "bin" / "signal-cli"
        assert exe.exists()
        assert os.access(exe, os.X_OK)
        assert (tmp_path / "bin" / "signal-cli-0.14.7" / "lib").is_dir()

    def test_remove_old_versions(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        """Le versioni vecchie vengono rimosse dopo il download."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.5")
        result = _run_install(tmp_path, "--version", "0.14.7", "--no-venv")
        assert result.returncode == 0
        assert (bin_dir / "signal-cli-0.14.5").exists() is False
        assert (bin_dir / "signal-cli-0.14.7").exists() is True


class TestPrerequisites:
    """🧪 Verifica prerequisiti."""

    def test_python_version_check(self, tmp_path: Path, fake_path: Path):
        """Python troppo vecchio → errore."""
        fake_bin = fake_path
        python_stub = """#!/bin/sh
if [ "$1" = "-c" ]; then
    echo "3.8"
    exit 0
fi
exit 0
"""
        (fake_bin / "python3").write_text(python_stub, encoding="utf-8")
        _make_executable(fake_bin / "python3")

        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode != 0
        assert "Python 3.12+ richiesto" in result.stderr

    def test_python_3_11_rejected(self, tmp_path: Path, fake_path: Path):
        """3.11 sotto il floor → errore con hint."""
        fake_bin = fake_path
        python_stub = """#!/bin/sh
if [ "$1" = "-c" ]; then
    echo "3.11"
    exit 0
fi
exit 0
"""
        (fake_bin / "python3").write_text(python_stub, encoding="utf-8")
        _make_executable(fake_bin / "python3")
        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode != 0
        assert "Python 3.12+ richiesto" in result.stderr
        assert "trovato 3.11" in result.stderr
        assert "brew install python@3.12" in result.stderr

    def test_python_3_12_accepted(self, tmp_path: Path, fake_path: Path):
        """3.12 esattamente al floor → accettato."""
        fake_bin = fake_path
        python_stub = """#!/bin/sh
if [ "$1" = "-c" ]; then
    echo "3.12"
    exit 0
fi
exit 0
"""
        (fake_bin / "python3").write_text(python_stub, encoding="utf-8")
        _make_executable(fake_bin / "python3")
        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode == 0
        assert "Python 3.12 trovato" in result.stdout

    def test_java_old_warns(self, tmp_path: Path, fake_path: Path):
        """Java troppo vecchio → warning ma non blocca."""
        fake_bin = fake_path
        java_stub = """#!/bin/sh
echo 'openjdk version "17.0.1" 2021-10-19'
exit 0
"""
        (fake_bin / "java").write_text(java_stub, encoding="utf-8")
        _make_executable(fake_bin / "java")

        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode == 0
        assert "Java 17 trovato" in result.stdout
        assert "richiede Java 25" in result.stdout


class TestVenv:
    """📦 Gestione virtualenv."""

    def test_venv_created(self, tmp_path: Path, fake_path: Path, test_helpers: Path):
        """Crea .venv quando DO_VENV=1."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.6")
        result = _run_install(tmp_path, "--skip-signal-cli")
        assert result.returncode == 0
        assert (tmp_path / ".venv").is_dir()

    def test_no_venv_flag(self, tmp_path: Path, fake_path: Path, test_helpers: Path):
        """--no-venv non crea .venv."""
        bin_dir = tmp_path / "bin"
        bin_dir.mkdir()
        _create_fake_signal_cli(bin_dir, "0.14.6")
        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")
        assert result.returncode == 0
        assert (tmp_path / ".venv").exists() is False


class TestWebConfig:
    def _run_ensure_web_config(self, tmp_path: Path):
        func = _extract_function(INSTALL_SCRIPT, "ensure_web_config")
        script = tmp_path / "ensure-web-config.sh"
        script.write_text(
            "set -euo pipefail\n"
            "ok() { :; }; err() { :; }\n"
            f"PROJECT_DIR={str(tmp_path)!r}\n"
            f"{func}\n"
            "ensure_web_config\n",
            encoding="utf-8",
        )
        return subprocess.run(
            ["bash", str(script)],
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )

    def test_creates_enabled_web_config_with_secure_token(self, tmp_path: Path):
        result = self._run_ensure_web_config(tmp_path)

        assert result.returncode == 0, result.stderr
        config_file = tmp_path / "config.json"
        config = json.loads(config_file.read_text(encoding="utf-8"))
        assert config["web"]["enabled"] is True
        assert config["web"]["host"] == "127.0.0.1"
        assert config["web"]["port"] == 4242
        assert len(config["web"]["token"]) >= 32
        assert stat.S_IMODE(config_file.stat().st_mode) == 0o600

    def test_preserves_existing_config_and_is_idempotent(self, tmp_path: Path):
        config_file = tmp_path / "config.json"
        config_file.write_text(
            json.dumps(
                {
                    "user_number": "+391234",
                    "web": {
                        "enabled": False,
                        "host": "0.0.0.0",
                        "port": 5000,
                        "token": "existing-token",
                    },
                }
            ),
            encoding="utf-8",
        )

        first = self._run_ensure_web_config(tmp_path)
        first_content = config_file.read_text(encoding="utf-8")
        second = self._run_ensure_web_config(tmp_path)

        assert first.returncode == 0, first.stderr
        assert second.returncode == 0, second.stderr
        assert config_file.read_text(encoding="utf-8") == first_content
        config = json.loads(first_content)
        assert config["user_number"] == "+391234"
        assert config["web"] == {
            "enabled": False,
            "host": "0.0.0.0",
            "port": 5000,
            "token": "existing-token",
        }

    def test_rejects_invalid_config_without_overwriting_it(self, tmp_path: Path):
        config_file = tmp_path / "config.json"
        config_file.write_text("{invalid", encoding="utf-8")

        result = self._run_ensure_web_config(tmp_path)

        assert result.returncode != 0
        assert config_file.read_text(encoding="utf-8") == "{invalid"


class TestAliasIsolation:
    def test_aliases_mode_generates_web_config(self, tmp_path: Path):
        script = tmp_path / "install.sh"
        home = tmp_path / "home"
        home.mkdir()
        shutil.copy(INSTALL_SCRIPT, script)
        _make_executable(script)

        result = subprocess.run(
            ["bash", str(script), "--aliases"],
            capture_output=True,
            text=True,
            cwd=tmp_path,
            env={**os.environ, "HOME": str(home), "SHELL": "/bin/bash"},
            timeout=30,
            check=False,
        )

        assert result.returncode == 0, result.stderr
        config = json.loads((tmp_path / "config.json").read_text(encoding="utf-8"))
        assert config["web"]["enabled"] is True
        assert config["web"]["token"]
        assert (home / ".bashrc").exists()

    def test_install_writes_aliases_only_to_isolated_home(
        self, tmp_path: Path, fake_path: Path, test_helpers: Path
    ):
        real_bashrc = Path.home() / ".bashrc"
        real_bashrc_before = real_bashrc.read_bytes() if real_bashrc.exists() else None

        result = _run_install(tmp_path, "--skip-signal-cli", "--no-venv")

        assert result.returncode == 0
        isolated_bashrc = tmp_path / "home" / ".bashrc"
        assert isolated_bashrc.exists()
        assert "web-signal-tui-bg" in isolated_bashrc.read_text(encoding="utf-8")
        real_bashrc_after = real_bashrc.read_bytes() if real_bashrc.exists() else None
        assert real_bashrc_after == real_bashrc_before

    def test_background_alias_is_idempotent_and_stop_alias_works(self, tmp_path: Path):
        script = tmp_path / "install.sh"
        home = tmp_path / "home"
        fake_bin = tmp_path / "fakebin"
        tmux_state = tmp_path / "tmux.state"
        home.mkdir()
        fake_bin.mkdir()
        shutil.copy(INSTALL_SCRIPT, script)
        _make_executable(script)
        # The background alias delegates to ``launcher.py server start``:
        # record the invocation instead of starting a real server.
        serve_calls = tmp_path / "serve.calls"
        venv_python = tmp_path / ".venv" / "bin" / "python"
        venv_python.parent.mkdir(parents=True)
        _write_stub(
            venv_python,
            f'#!/bin/sh\necho "$@" >> {str(serve_calls)!r}\n',
        )
        _write_stub(
            fake_bin / "tmux",
            "#!/bin/sh\n"
            f"state={str(tmux_state)!r}\n"
            'case "$1" in\n'
            '  has-session) [ -f "$state" ] ;;\n'
            '  new-session) touch "$state" ;;\n'
            '  kill-session) rm -f "$state" ;;\n'
            "esac\n",
        )
        env = {
            **os.environ,
            "HOME": str(home),
            "SHELL": "/bin/bash",
            # Never read or remove the real /tmp/signal-tui.lock (mc-cvp).
            "MEGACHAT_LOCK_FILE": str(tmp_path / "signal-tui.lock"),
            "PATH": f"{fake_bin}:{os.environ['PATH']}",
        }
        install = subprocess.run(
            ["bash", str(script), "--aliases"],
            capture_output=True,
            text=True,
            cwd=tmp_path,
            env=env,
            timeout=30,
            check=False,
        )
        assert install.returncode == 0, install.stderr
        runner = tmp_path / "run-aliases.sh"
        runner.write_text(
            "shopt -s expand_aliases\n"
            f"source {str(home / '.bashrc')!r}\n"
            "web-signal-tui-bg\n"
            "signal-tui-bg\n"
            "signal-tui-stop\n",
            encoding="utf-8",
        )

        result = subprocess.run(
            ["bash", str(runner)],
            capture_output=True,
            text=True,
            cwd=tmp_path,
            env=env,
            timeout=30,
            check=False,
        )

        token = json.loads((tmp_path / "config.json").read_text())["web"]["token"]
        assert result.returncode == 0, result.stderr
        assert result.stdout.count(f"token: {token}") == 2
        assert "Server bg fermato." in result.stdout
        # The server is no longer hosted in tmux: only the stop alias still
        # kills a legacy session, and nothing creates one.
        assert not tmux_state.exists()
        deadline = time.monotonic() + 5
        while not serve_calls.exists() and time.monotonic() < deadline:
            time.sleep(0.05)
        recorded_calls = serve_calls.read_text().splitlines()
        assert recorded_calls
        assert all(
            line.endswith("launcher.py server start --no-token")
            for line in recorded_calls
        )


class TestBuildMegachatd:
    """install.sh builds megachatd with Go, or uses a release binary; no Go and
    no binary is fatal."""

    FUNCS = (
        "go_required_version",
        "version_ge",
        "megachatd_binary",
        "build_megachatd",
    )

    def _run(
        self,
        tmp_path: Path,
        *,
        go_version: str | None,
        build_ok: bool = True,
        binary: bool = False,
        sources: bool = True,
        gotoolchain: str | None = None,
    ):
        project = tmp_path / "proj"
        if sources:
            (project / "megachatd").mkdir(parents=True)
            (project / "megachatd" / "go.mod").write_text(
                "module x\n\ngo 1.26.0\n", encoding="utf-8"
            )
        stubs = tmp_path / "stubs"
        stubs.mkdir()
        go_log = tmp_path / "go.log"
        if go_version is not None:
            _write_stub(
                stubs / "go",
                "#!/bin/sh\n"
                f'if [ "$1" = version ]; then echo "go version go{go_version} linux/amd64"; exit 0; fi\n'
                f'echo "$PWD: $*" >> {go_log}\n'
                f"exit {0 if build_ok else 1}\n",
            )
        if binary:
            _write_stub(stubs / "megachatd", "#!/bin/sh\nexit 0\n")
        lines = INSTALL_SCRIPT.read_text(encoding="utf-8").splitlines()
        assigns = [ln for ln in lines if ln.startswith("MEGACHATD_DIR=")]
        funcs = "\n".join(_extract_function(INSTALL_SCRIPT, f) for f in self.FUNCS)
        script = tmp_path / "build.sh"
        script.write_text(
            "set -euo pipefail\n"
            'info() { echo "[INFO] $*"; }\nok() { echo "[OK] $*"; }\n'
            'die() { echo "[ERROR] $*" >&2; exit 1; }\n'
            f"PROJECT_DIR='{project}'\n" + "\n".join(assigns) + f"\n{funcs}\n"
            "build_megachatd\n",
            encoding="utf-8",
        )
        # Only the stubs (and the system tools the script needs) on PATH: a real
        # `go` of the developer must never be picked up.
        tools = tmp_path / "tools"
        tools.mkdir()
        for name in (
            "sed",
            "grep",
            "sort",
            "head",
            "mktemp",
            "rm",
            "cat",
            "touch",
            "chmod",
            "printf",
        ):
            real = shutil.which(name)
            assert real
            (tools / name).symlink_to(real)
        env = {"PATH": f"{stubs}:{tools}", "HOME": str(tmp_path)}
        if gotoolchain:
            env["GOTOOLCHAIN"] = gotoolchain
        bash = shutil.which("bash")
        assert bash
        result = subprocess.run(  # noqa: PLW1510
            [bash, str(script)], capture_output=True, text=True, env=env, timeout=30
        )
        return result, project, go_log

    def test_builds_with_the_makefile_command(self, tmp_path: Path):
        result, project, go_log = self._run(tmp_path, go_version="1.26.2")
        assert result.returncode == 0, result.stderr
        assert "megachatd compilato" in result.stdout
        assert go_log.read_text().strip() == (
            f"{project}/megachatd: build -o bin/megachatd ./cmd/megachatd"
        )

    def test_missing_go_without_binary_dies(self, tmp_path: Path):
        result, _, _ = self._run(tmp_path, go_version=None)
        assert result.returncode != 0
        assert "Go non trovato" in result.stderr

    def test_missing_go_uses_an_existing_release_binary(self, tmp_path: Path):
        result, _, go_log = self._run(tmp_path, go_version=None, binary=True)
        assert result.returncode == 0, result.stderr
        assert "uso il binario megachatd già presente" in result.stdout
        assert not go_log.exists()

    def test_old_go_without_toolchain_download_dies(self, tmp_path: Path):
        result, _, go_log = self._run(
            tmp_path, go_version="1.24.7", gotoolchain="local"
        )
        assert result.returncode != 0
        assert "richiede Go 1.26.0+" in result.stderr
        assert not go_log.exists()

    def test_old_go_may_download_its_toolchain(self, tmp_path: Path):
        result, _, go_log = self._run(tmp_path, go_version="1.24.7")
        assert result.returncode == 0, result.stderr
        assert "scaricherà la toolchain" in result.stdout
        assert go_log.exists()

    def test_failed_build_dies(self, tmp_path: Path):
        result, _, _ = self._run(tmp_path, go_version="1.26.0", build_ok=False)
        assert result.returncode != 0
        assert "Compilazione di megachatd fallita" in result.stderr

    def test_missing_sources_use_an_existing_binary(self, tmp_path: Path):
        result, _, _ = self._run(tmp_path, go_version=None, sources=False, binary=True)
        assert result.returncode == 0, result.stderr
        assert "uso il binario già presente" in result.stdout

    def test_missing_sources_without_binary_die(self, tmp_path: Path):
        result, _, _ = self._run(tmp_path, go_version=None, sources=False)
        assert result.returncode != 0
        assert "Sorgenti di megachatd non trovati" in result.stderr
