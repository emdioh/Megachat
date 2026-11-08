"""``launcher.py server`` options passed through to ``megachatd serve``."""

from __future__ import annotations

import json

import pytest

from launcher import aliases, server

ServeOptions = server.ServeOptions


class _FakeProc:
    pid = 4321

    def __init__(self, returncode=None):
        self.returncode = returncode

    def poll(self):
        return self.returncode


@pytest.fixture
def env(monkeypatch, tmp_path):
    """Isolate start_all/stop_all from tmux, the real lock and sleeping."""
    calls = {"spawn": []}
    monkeypatch.setenv("MEGACHATD_BIN", "/opt/megachatd")
    monkeypatch.setattr(server, "LOCK_FILE", tmp_path / "signal-tui.lock")
    monkeypatch.setattr(server, "_kill_tmux_session", lambda name="tui": None)
    monkeypatch.setattr(server, "SERVER_LOG", tmp_path / "serve.log")
    monkeypatch.setattr(server.time, "sleep", lambda *_: None)
    monkeypatch.setattr(server, "_local_ip", lambda: "10.0.0.5")
    monkeypatch.setattr(server, "_read_web_token", lambda: "tok123")

    def _popen(cmd, **kwargs):
        calls["spawn"].append(cmd)
        return _FakeProc()

    monkeypatch.setattr(server.subprocess, "Popen", _popen)
    return calls


class TestServeArgs:
    def test_defaults_match_the_historical_command(self):
        assert ServeOptions().serve_args() == [
            "--web-port",
            "4242",
            "--web-host",
            "0.0.0.0",
        ]
        assert server.SERVE_ARGS == ServeOptions().serve_args()

    def test_all_options_are_passed_through(self):
        opts = ServeOptions(
            web_host="192.168.1.10",
            web_port=8080,
            web_no_auth=True,
            socket="/run/mc.sock",
            debug=True,
        )
        assert opts.serve_args() == [
            "--web-port",
            "8080",
            "--web-host",
            "192.168.1.10",
            "--web-no-auth",
            "--socket",
            "/run/mc.sock",
            "--debug",
        ]

    def test_no_web_drops_the_web_flags(self):
        opts = ServeOptions(no_web=True, web_no_auth=True, no_socket=True)
        assert opts.serve_args() == ["--no-web", "--no-socket"]

    @pytest.mark.parametrize(
        ("host", "probe"),
        [
            ("0.0.0.0", "127.0.0.1"),
            ("localhost", "127.0.0.1"),
            ("10.1.2.3", "10.1.2.3"),
            ("127.0.0.1,192.168.1.10", "127.0.0.1"),
            ("0.0.0.0, ::", "127.0.0.1"),
        ],
    )
    def test_probe_host(self, host, probe):
        assert ServeOptions(web_host=host).probe_host() == probe

    def test_web_hosts_splits_a_comma_separated_list(self):
        opts = ServeOptions(web_host="127.0.0.1, 192.168.1.10,")
        assert opts.web_hosts() == ["127.0.0.1", "192.168.1.10"]

    def test_web_url_prefers_a_non_loopback_host(self):
        opts = ServeOptions(web_host="127.0.0.1,192.168.1.10", web_port=8080)
        assert server._web_url(opts) == "http://192.168.1.10:8080"


class TestStartWithOptions:
    def test_spawns_with_options_and_probes_their_port(self, monkeypatch, env, capsys):
        probed = []
        monkeypatch.setattr(
            server,
            "port_listening",
            lambda port, host="127.0.0.1": probed.append((port, host)) or True,
        )
        opts = ServeOptions(web_port=8080, web_no_auth=True)
        assert server.start_all(opts=opts) == 0
        assert env["spawn"][0] == [
            "/opt/megachatd",
            "serve",
            *opts.serve_args(),
            "--project-dir",
            str(server.PROJECT_DIR),
        ]
        assert probed == [(8080, "127.0.0.1")]
        out = capsys.readouterr().out
        assert "http://10.0.0.5:8080" in out
        assert "tok123" not in out
        assert "disattivato" in out
        assert "[WARN]" in out  # no token on a network-reachable address

    def test_writes_state_read_back_by_status(self, monkeypatch, env, capsys):
        monkeypatch.setattr(
            server, "port_listening", lambda port, host="127.0.0.1": True
        )
        opts = ServeOptions(web_host="127.0.0.1", web_port=9000)
        assert server.start_all(opts=opts) == 0
        state = json.loads(server._state_file().read_text())
        assert state["pid"] == _FakeProc.pid
        assert state["web_port"] == 9000

        server.LOCK_FILE.write_text(str(_FakeProc.pid))
        monkeypatch.setattr(server, "_pid_alive", lambda pid: True)
        monkeypatch.setattr(server, "_pgrep", lambda pattern: False)
        capsys.readouterr()
        server.status()
        assert "in ascolto su 127.0.0.1:9000" in capsys.readouterr().out

    def test_status_ignores_state_of_another_pid(self, monkeypatch, env):
        server._state_file().write_text(json.dumps({"pid": 1, "web_port": 9000}))
        assert server._read_state(2) == ServeOptions()

    def test_no_web_waits_for_the_lock_instead_of_a_port(
        self, monkeypatch, env, capsys
    ):
        monkeypatch.setattr(
            server, "port_listening", lambda *a, **k: pytest.fail("no port to probe")
        )
        monkeypatch.setattr(server, "server_pid", lambda: _FakeProc.pid)
        assert server.start_all(opts=ServeOptions(no_web=True)) == 0
        assert "senza Web UI" in capsys.readouterr().out

    def test_missing_binary_is_reported_in_italian(self, monkeypatch, env, capsys):
        monkeypatch.delenv("MEGACHATD_BIN")
        monkeypatch.setattr(server.serverbin.shutil, "which", lambda name: None)
        monkeypatch.setattr(server.serverbin, "REPO_BIN_REL", "nope/megachatd")
        with pytest.raises(SystemExit):
            server.start_all()
        assert "megachatd non trovato" in capsys.readouterr().err
        assert env["spawn"] == []

    def test_foreground_execs_serve_in_this_process(self, monkeypatch, env):
        execs = []

        def _execv(path, argv):
            execs.append(argv)
            raise SystemExit(0)

        monkeypatch.setattr(server.os, "execv", _execv)
        monkeypatch.setattr(server.os, "chdir", lambda path: None)
        opts = ServeOptions(web_port=8080)
        with pytest.raises(SystemExit):
            server.start_all(opts=opts, foreground=True)
        assert execs[0] == [
            "/opt/megachatd",
            "serve",
            *opts.serve_args(),
            "--project-dir",
            str(server.PROJECT_DIR),
        ]
        assert env["spawn"] == []


class TestStop:
    def test_stop_removes_the_state_file(self, env):
        server._state_file().write_text("{}")
        assert server.stop_server()
        assert not server._state_file().exists()


class TestAliasesUseTheLauncher:
    def test_bg_forwards_arguments_and_stop_is_plain(self):
        block = aliases._ALIASES_TEMPLATE
        assert 'server start --no-token "$@"' in block
        assert 'launcher.py" server stop;' in block
        assert "--keep-whatsapp" not in block
        assert "--waha" not in block
        assert "kill -INT" not in block
