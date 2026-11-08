"""Coverage for launcher/install.py's run() — the full installer orchestration.

The individual helpers (download_signal_cli, check_java, ensure_web_config,
etc.) are already covered in tests/test_launcher_install.py; here we drive
run(args) end-to-end with every helper mocked, to cover its branch wiring
(--update, --skip-signal-cli, --aliases, the final next-steps banner).
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from launcher import install


def _args(**overrides):
    defaults = {
        "no_venv": False,
        "skip_signal_cli": False,
        "version": "",
        "aliases": False,
        "update": False,
    }
    defaults.update(overrides)
    return SimpleNamespace(**defaults)


@pytest.fixture
def wired(monkeypatch, tmp_path):
    """Stub every side-effecting helper run() calls, tracking what fired."""
    calls: dict[str, object] = {}
    monkeypatch.setattr(install, "command_exists", lambda name: True)
    monkeypatch.setattr(
        install, "check_python", lambda: calls.setdefault("python", True)
    )
    monkeypatch.setattr(install, "check_java", lambda: calls.setdefault("java", True))
    monkeypatch.setattr(install, "get_latest_version", lambda: "0.14.7")
    monkeypatch.setattr(install, "get_installed_version", lambda: "")
    monkeypatch.setattr(
        install, "download_signal_cli", lambda v: calls.setdefault("downloaded", v)
    )
    monkeypatch.setattr(
        install, "remove_old_versions", lambda v: calls.setdefault("removed", v)
    )
    monkeypatch.setattr(install, "install_python_deps", lambda venv: None)
    monkeypatch.setattr(
        install, "build_megachatd", lambda: calls.setdefault("megachatd", True)
    )
    monkeypatch.setattr(install, "PROJECT_DIR", tmp_path)
    monkeypatch.setattr(
        install, "ensure_web_config", lambda: calls.setdefault("web_config", True)
    )
    monkeypatch.setattr(install, "install_aliases", lambda: True)
    return calls


class TestAliasesShortCircuit:
    def test_aliases_flag_only_runs_aliases(self, wired, monkeypatch):
        monkeypatch.setattr(install, "run_aliases_only", lambda: 0)
        assert install.run(_args(aliases=True)) == 0
        assert "python" not in wired  # rest of the installer never ran


class TestMissingTar:
    def test_dies_without_tar(self, wired, monkeypatch):
        monkeypatch.setattr(install, "command_exists", lambda name: False)
        with pytest.raises(SystemExit):
            install.run(_args())


class TestUpdateMode:
    def test_updates_when_newer_version_available(self, wired):
        assert install.run(_args(update=True)) == 0
        assert wired["downloaded"] == "0.14.7"
        assert wired["removed"] == "0.14.7"

    def test_noop_when_already_latest(self, wired, monkeypatch):
        monkeypatch.setattr(install, "get_installed_version", lambda: "0.14.7")
        assert install.run(_args(update=True)) == 0
        assert "downloaded" not in wired


class TestSignalCliInstall:
    def test_skips_signal_cli_download_when_flagged(self, wired, monkeypatch, capsys):
        monkeypatch.setattr(install, "get_installed_version", lambda: "")
        assert install.run(_args(skip_signal_cli=True)) == 0
        assert "downloaded" not in wired
        assert "non funzionerà" in capsys.readouterr().out

    def test_skips_download_when_already_installed_at_same_version(
        self, wired, monkeypatch
    ):
        monkeypatch.setattr(install, "get_installed_version", lambda: "0.14.7")
        assert install.run(_args(version="0.14.7")) == 0
        assert "downloaded" not in wired

    def test_downloads_specified_version(self, wired):
        assert install.run(_args(version="0.14.7")) == 0
        assert wired["downloaded"] == "0.14.7"
        assert wired["removed"] == "0.14.7"


class TestAliasesSoftFailure:
    def test_warns_but_completes_when_aliases_fail(self, wired, monkeypatch, capsys):
        monkeypatch.setattr(install, "install_aliases", lambda: False)
        assert install.run(_args()) == 0
        assert "alias shell non riuscita" in capsys.readouterr().out


class TestFinalBanner:
    def test_no_docker_suggestion_by_default(self, wired, capsys):
        install.run(_args())
        out = capsys.readouterr().out
        assert "Hai Docker installato" not in out
        assert "WAHA" not in out

    def test_default_install_builds_megachatd(self, wired):
        install.run(_args())
        assert wired["megachatd"] is True

    def test_always_explains_how_to_enable_each_integration(self, wired, capsys):
        install.run(_args())
        out = capsys.readouterr().out
        for flag in (
            "SIGNAL_ENABLED=1",
            "WHATSAPP_ENABLED=1",
            "TELEGRAM_ENABLED=1",
            "GMAIL_ENABLED=1",
        ):
            assert flag in out
        assert "link_account.py" in out
        assert "link_whatsapp.py" in out
