"""launcher/install.py: building megachatd, Go checks and the Python fallback."""

from __future__ import annotations

import pytest

from launcher import install

MARK = install.FALLBACK_MARK


@pytest.fixture
def project(tmp_path, monkeypatch):
    (tmp_path / "megachatd").mkdir()
    (tmp_path / "megachatd" / "go.mod").write_text(
        "module x\n\ngo 1.26.0\n", encoding="utf-8"
    )
    monkeypatch.setattr(install, "PROJECT_DIR", tmp_path)
    monkeypatch.setattr(install, "MEGACHATD_DIR", tmp_path / "megachatd")
    monkeypatch.delenv("GOTOOLCHAIN", raising=False)
    return tmp_path


def _go(monkeypatch, version: str | None, *, build_rc: int = 0):
    ran = []
    monkeypatch.setattr(install, "command_exists", lambda name: version is not None)
    monkeypatch.setattr(install, "installed_go_version", lambda: version or "")

    def fake_run(cmd, **kwargs):
        ran.append((cmd, kwargs))
        return type("R", (), {"returncode": build_rc})()

    monkeypatch.setattr(install.subprocess, "run", fake_run)
    return ran


def test_required_version_comes_from_go_mod(project):
    assert install.go_required_version() == "1.26.0"


def test_builds_into_repo_bin(project, monkeypatch):
    ran = _go(monkeypatch, "1.26.1")
    assert install.build_megachatd() is True
    ((cmd, kwargs),) = ran
    assert cmd == ["go", "build", "-o", "bin/megachatd", "./cmd/megachatd"]
    assert kwargs["cwd"] == project / "megachatd"
    assert not (project / ".env").exists()


def test_without_go_warns_in_italian_and_falls_back(project, monkeypatch, capsys):
    ran = _go(monkeypatch, None)
    assert install.build_megachatd() is False
    assert ran == []
    out = capsys.readouterr().out
    assert "Go non trovato" in out
    assert "Server Python" in out
    env = (project / ".env").read_text(encoding="utf-8")
    assert "MEGACHAT_SERVER=python" in env
    assert (project / ".env").stat().st_mode & 0o777 == 0o600


def test_too_old_go_falls_back_when_toolchain_download_is_off(
    project, monkeypatch, capsys
):
    monkeypatch.setenv("GOTOOLCHAIN", "local")
    ran = _go(monkeypatch, "1.24.7")
    assert install.build_megachatd() is False
    assert ran == []
    assert "richiede Go 1.26.0" in capsys.readouterr().out
    assert "MEGACHAT_SERVER=python" in (project / ".env").read_text(encoding="utf-8")


def test_go_older_than_go_mod_may_fetch_its_toolchain(project, monkeypatch):
    ran = _go(monkeypatch, "1.24.7")
    assert install.build_megachatd() is True
    assert len(ran) == 1


def test_go_before_toolchain_support_falls_back(project, monkeypatch):
    ran = _go(monkeypatch, "1.20.5")
    assert install.build_megachatd() is False
    assert ran == []


def test_failed_build_falls_back(project, monkeypatch, capsys):
    _go(monkeypatch, "1.26.0", build_rc=1)
    assert install.build_megachatd() is False
    assert "Compilazione di megachatd fallita" in capsys.readouterr().out
    assert "MEGACHAT_SERVER=python" in (project / ".env").read_text(encoding="utf-8")


def test_successful_build_removes_only_the_fallback_it_wrote(project, monkeypatch):
    env = project / ".env"
    env.write_text(f"KEEP=1\n\n{MARK}\nMEGACHAT_SERVER=python\n", encoding="utf-8")
    _go(monkeypatch, "1.26.0")
    assert install.build_megachatd() is True
    text = env.read_text(encoding="utf-8")
    assert "MEGACHAT_SERVER" not in text
    assert "KEEP=1" in text


def test_users_own_setting_is_never_touched(project, monkeypatch):
    env = project / ".env"
    env.write_text("MEGACHAT_SERVER=python\n", encoding="utf-8")  # no marker
    _go(monkeypatch, None)
    install.build_megachatd()
    _go(monkeypatch, "1.26.0")
    install.build_megachatd()
    assert env.read_text(encoding="utf-8") == "MEGACHAT_SERVER=python\n"


def test_missing_sources_skip_quietly(tmp_path, monkeypatch):
    monkeypatch.setattr(install, "MEGACHATD_DIR", tmp_path / "none")
    assert install.build_megachatd() is False
