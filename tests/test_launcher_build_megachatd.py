"""launcher/install.py: building megachatd and the Go checks."""

from __future__ import annotations

import pytest

from launcher import install


@pytest.fixture
def project(tmp_path, monkeypatch):
    (tmp_path / "megachatd").mkdir()
    (tmp_path / "megachatd" / "go.mod").write_text(
        "module x\n\ngo 1.26.0\n", encoding="utf-8"
    )
    monkeypatch.setattr(install, "PROJECT_DIR", tmp_path)
    monkeypatch.setattr(install, "MEGACHATD_DIR", tmp_path / "megachatd")
    monkeypatch.delenv("GOTOOLCHAIN", raising=False)
    monkeypatch.setattr(install.serverbin, "resolve_megachatd", lambda repo=None: None)
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


def test_without_go_and_no_binary_dies(project, monkeypatch):
    ran = _go(monkeypatch, None)
    with pytest.raises(SystemExit):
        install.build_megachatd()
    assert ran == []


def test_without_go_uses_an_existing_release_binary(project, monkeypatch, capsys):
    monkeypatch.setattr(
        install.serverbin, "resolve_megachatd", lambda repo=None: "/usr/bin/megachatd"
    )
    ran = _go(monkeypatch, None)
    assert install.build_megachatd() is True
    assert ran == []
    assert "uso il binario megachatd già presente" in capsys.readouterr().out


def test_too_old_go_without_toolchain_download_dies(project, monkeypatch, capsys):
    monkeypatch.setenv("GOTOOLCHAIN", "local")
    ran = _go(monkeypatch, "1.24.7")
    with pytest.raises(SystemExit):
        install.build_megachatd()
    assert ran == []
    assert "richiede Go 1.26.0" in capsys.readouterr().err


def test_go_older_than_go_mod_may_fetch_its_toolchain(project, monkeypatch):
    ran = _go(monkeypatch, "1.24.7")
    assert install.build_megachatd() is True
    assert len(ran) == 1


def test_go_before_toolchain_support_dies(project, monkeypatch):
    ran = _go(monkeypatch, "1.20.5")
    with pytest.raises(SystemExit):
        install.build_megachatd()
    assert ran == []


def test_failed_build_dies(project, monkeypatch, capsys):
    _go(monkeypatch, "1.26.0", build_rc=1)
    with pytest.raises(SystemExit):
        install.build_megachatd()
    assert "Compilazione di megachatd fallita" in capsys.readouterr().err


def test_missing_sources_use_an_existing_binary(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(install, "MEGACHATD_DIR", tmp_path / "none")
    monkeypatch.setattr(
        install.serverbin, "resolve_megachatd", lambda repo=None: "/usr/bin/megachatd"
    )
    assert install.build_megachatd() is True
    assert "uso il binario già presente" in capsys.readouterr().out


def test_missing_sources_without_binary_die(tmp_path, monkeypatch):
    monkeypatch.setattr(install, "MEGACHATD_DIR", tmp_path / "none")
    monkeypatch.setattr(install.serverbin, "resolve_megachatd", lambda repo=None: None)
    with pytest.raises(SystemExit):
        install.build_megachatd()
