"""Coverage for launcher/backend.py's signal-cli restart orchestration."""

from __future__ import annotations

import pytest

from launcher import backend


class TestRun:
    def test_wires_signal_timeout(self, monkeypatch):
        calls = {}

        def _fake_restart_signal(wait, timeout):
            calls["signal"] = (wait, timeout)

        monkeypatch.setattr(backend, "_restart_signal_daemon", _fake_restart_signal)
        monkeypatch.delenv("SIGNAL_DAEMON_TIMEOUT_SECONDS", raising=False)

        assert backend.run(no_wait=True) == 0
        assert calls["signal"] == (False, 30)

    def test_reads_custom_timeout_from_env(self, monkeypatch):
        calls = {}
        monkeypatch.setattr(
            backend, "_restart_signal_daemon", lambda wait, t: calls.update(signal=t)
        )
        monkeypatch.setenv("SIGNAL_DAEMON_TIMEOUT_SECONDS", "5")
        backend.run(no_wait=False)
        assert calls == {"signal": 5}


class TestConfiguredDaemonPids:
    def test_empty_when_no_matching_process(self):
        assert backend._configured_daemon_pids("+1000000000") == []


class TestSignalNumberInvalidJson:
    def test_invalid_json_returns_empty(self, tmp_path, monkeypatch):
        monkeypatch.setattr(backend, "PROJECT_DIR", tmp_path)
        monkeypatch.delenv("SIGNAL_USER_NUMBER", raising=False)
        (tmp_path / "config.json").write_text("{not json", encoding="utf-8")
        assert backend._signal_number() == ""


class TestRestartSignalDaemon:
    def test_skips_when_not_configured(self, monkeypatch, capsys):
        monkeypatch.setattr(backend, "_signal_number", lambda: "")
        backend._restart_signal_daemon(True, 5)
        assert "non configurato" in capsys.readouterr().out

    def test_skips_when_binary_not_found(self, monkeypatch, capsys):
        monkeypatch.setattr(backend, "_signal_number", lambda: "+1000")
        monkeypatch.setattr(backend, "_find_signal_cli", lambda: None)
        backend._restart_signal_daemon(True, 5)
        assert "non disponibile" in capsys.readouterr().out

    def test_dies_when_port_never_frees_up(self, monkeypatch):
        monkeypatch.setattr(backend, "_signal_number", lambda: "+1000")
        monkeypatch.setattr(backend, "_find_signal_cli", lambda: "/usr/bin/signal-cli")
        monkeypatch.setattr(backend, "_stop_configured_signal_daemon", lambda n: None)
        monkeypatch.setattr(backend, "port_listening", lambda port: True)
        monkeypatch.setattr(backend.time, "sleep", lambda *_: None)
        times = iter([0, 100])
        monkeypatch.setattr(backend.time, "monotonic", lambda: next(times, 100))
        with pytest.raises(SystemExit):
            backend._restart_signal_daemon(True, 5)

    def test_no_wait_returns_after_spawning(self, monkeypatch, capsys):
        monkeypatch.setattr(backend, "_signal_number", lambda: "+1000")
        monkeypatch.setattr(backend, "_find_signal_cli", lambda: "/usr/bin/signal-cli")
        monkeypatch.setattr(backend, "_stop_configured_signal_daemon", lambda n: None)
        monkeypatch.setattr(backend, "port_listening", lambda port: False)
        monkeypatch.setattr(backend.subprocess, "Popen", lambda *a, **k: None)
        backend._restart_signal_daemon(False, 5)
        assert "avviato (--no-wait)" in capsys.readouterr().out

    def test_waits_and_confirms_jsonrpc_ready(self, monkeypatch, capsys):
        monkeypatch.setattr(backend, "_signal_number", lambda: "+1000")
        monkeypatch.setattr(backend, "_find_signal_cli", lambda: "/usr/bin/signal-cli")
        monkeypatch.setattr(backend, "_stop_configured_signal_daemon", lambda n: None)
        monkeypatch.setattr(backend, "port_listening", lambda port: False)
        monkeypatch.setattr(backend.subprocess, "Popen", lambda *a, **k: None)
        monkeypatch.setattr(
            backend,
            "http_post",
            lambda *a, **k: (200, b'{"jsonrpc":"2.0","result":[]}'),
        )
        backend._restart_signal_daemon(True, 5)
        assert "pronto" in capsys.readouterr().out

    def test_dies_on_jsonrpc_timeout(self, monkeypatch):
        monkeypatch.setattr(backend, "_signal_number", lambda: "+1000")
        monkeypatch.setattr(backend, "_find_signal_cli", lambda: "/usr/bin/signal-cli")
        monkeypatch.setattr(backend, "_stop_configured_signal_daemon", lambda n: None)
        monkeypatch.setattr(backend, "port_listening", lambda port: False)
        monkeypatch.setattr(backend.subprocess, "Popen", lambda *a, **k: None)
        monkeypatch.setattr(backend, "http_post", lambda *a, **k: None)
        monkeypatch.setattr(backend.time, "sleep", lambda *_: None)
        times = iter([0, 0, 100])
        monkeypatch.setattr(backend.time, "monotonic", lambda: next(times, 100))
        with pytest.raises(SystemExit):
            backend._restart_signal_daemon(True, 5)
