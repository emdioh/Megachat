"""Which server gets spawned: megachatd (binary lookup) or the Python fallback."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from launcher import serverbin


@pytest.fixture
def repo(tmp_path, monkeypatch) -> Path:
    """A fake checkout; nothing on PATH, no env override."""
    monkeypatch.delenv("MEGACHATD_BIN", raising=False)
    monkeypatch.delenv("MEGACHAT_SERVER", raising=False)
    monkeypatch.setenv("PATH", str(tmp_path / "emptybin"))
    return tmp_path / "repo"


def _fake_binary(path: Path) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("#!/bin/sh\n", encoding="utf-8")
    path.chmod(0o755)
    return path


class TestResolveMegachatd:
    def test_env_wins_over_repo_build_and_path(self, repo, monkeypatch, tmp_path):
        _fake_binary(repo / serverbin.REPO_BIN_REL)
        on_path = _fake_binary(tmp_path / "pathbin" / "megachatd")
        monkeypatch.setenv("PATH", str(on_path.parent))
        monkeypatch.setenv("MEGACHATD_BIN", "/custom/megachatd")
        assert serverbin.resolve_megachatd(repo) == "/custom/megachatd"

    def test_repo_build_wins_over_path(self, repo, monkeypatch, tmp_path):
        built = _fake_binary(repo / "megachatd" / "bin" / "megachatd")
        on_path = _fake_binary(tmp_path / "pathbin" / "megachatd")
        monkeypatch.setenv("PATH", str(on_path.parent))
        assert serverbin.resolve_megachatd(repo) == str(built)

    def test_falls_back_to_path(self, repo, monkeypatch, tmp_path):
        on_path = _fake_binary(tmp_path / "pathbin" / "megachatd")
        monkeypatch.setenv("PATH", str(on_path.parent))
        assert serverbin.resolve_megachatd(repo) == str(on_path)

    def test_non_executable_repo_build_is_skipped(self, repo):
        built = _fake_binary(repo / "megachatd" / "bin" / "megachatd")
        built.chmod(0o644)
        assert serverbin.resolve_megachatd(repo) is None

    def test_nothing_found(self, repo):
        assert serverbin.resolve_megachatd(repo) is None


class TestServeCommand:
    def test_megachatd_gets_flags_and_project_dir(self, repo, monkeypatch):
        monkeypatch.setenv("MEGACHATD_BIN", "/opt/megachatd")
        cmd = serverbin.serve_command(["--web-port", "1"], repo=repo)
        assert cmd == [
            "/opt/megachatd",
            "serve",
            "--web-port",
            "1",
            "--project-dir",
            str(repo),
        ]

    def test_explicit_project_dir_is_kept(self, repo, monkeypatch):
        monkeypatch.setenv("MEGACHATD_BIN", "/opt/megachatd")
        cmd = serverbin.serve_command(["--project-dir", "/elsewhere"], repo=repo)
        assert cmd.count("--project-dir") == 1
        assert "/elsewhere" in cmd

    def test_environment_beats_dotenv(self, repo, monkeypatch):
        repo.mkdir()
        (repo / ".env").write_text("MEGACHAT_SERVER=python\n", encoding="utf-8")
        monkeypatch.setenv("MEGACHAT_SERVER", "megachatd")
        assert serverbin.use_python(repo) is False
        assert serverbin.engine_name(repo) == "megachatd"

    def test_default_is_megachatd(self, repo):
        assert serverbin.use_python(repo) is False
        assert os.environ.get("MEGACHAT_SERVER") is None
