"""Start/stop/status the headless server directly on this machine
(``megachatd serve`` as a detached background process).

Ported from ``scripts/start_on_server.sh`` — a disaster-recovery utility:
if the local machine is unreachable, this can be run from a server console
without depending on the handover scripts that normally run from local.
"""

from __future__ import annotations

import json
import os
import signal
import socket
import subprocess
import time
from dataclasses import asdict, dataclass
from pathlib import Path

from client.paths import candidate_socket_paths, runtime_file_path

from . import serverbin
from .common import PROJECT_DIR, command_exists, die, info, ok, port_listening, warn

# The instance lock belongs to the server.
LOCK_FILE = Path(os.environ.get("MEGACHAT_LOCK_FILE") or "/tmp/signal-tui.lock")
SERVER_LOG = Path("/tmp/megachat-serve.log")
SERVER_PORT = 4242
SERVER_HOST = "0.0.0.0"
#: Seconds to wait for the web adapter to listen after spawning the server.
READY_TIMEOUT_S = 30
#: Seconds to wait for a signalled server to exit before giving up.
STOP_TIMEOUT_S = 30


class ServerStartError(RuntimeError):
    """The server exited before becoming ready; the message is user facing.

    Carries the server's own startup error (e.g. an enabled integration with
    missing settings), not a generic "went away" message.
    """


@dataclass(frozen=True)
class ServeOptions:
    """Options of ``megachat serve`` that the launcher passes through.

    The defaults are the launcher's historical ones: web on all interfaces,
    port 4242, token required, Unix socket on its default path.
    """

    web_host: str = SERVER_HOST
    web_port: int = SERVER_PORT
    web_no_auth: bool = False
    no_web: bool = False
    no_socket: bool = False
    socket: str | None = None
    debug: bool = False

    def serve_args(self) -> list[str]:
        """``serve`` flags of megachatd; see :func:`serve_argv` for the full
        command."""
        args: list[str] = []
        if self.no_web:
            args.append("--no-web")
        else:
            args += ["--web-port", str(self.web_port), "--web-host", self.web_host]
            if self.web_no_auth:
                args.append("--web-no-auth")
        if self.no_socket:
            args.append("--no-socket")
        elif self.socket:
            args += ["--socket", self.socket]
        if self.debug:
            args.append("--debug")
        return args

    def web_hosts(self) -> list[str]:
        """Every bind address of ``web_host`` (comma-separated)."""
        hosts = [h.strip() for h in self.web_host.split(",") if h.strip()]
        return hosts or [""]

    def probe_host(self) -> str:
        """Address to connect to when checking that the web UI listens."""
        host = self.web_hosts()[0]
        if host in ("0.0.0.0", "::", "", "localhost"):
            return "127.0.0.1"
        return host


#: Arguments of the default detached server (web on all interfaces; the Unix
#: socket is on by default).  The remote handover script is built from it.
SERVE_ARGS = ServeOptions().serve_args()


def _state_file() -> Path:
    """Options of the running server, written by ``start`` next to the lock."""
    return LOCK_FILE.with_name(LOCK_FILE.name + ".json")


def _write_state(pid: int, opts: ServeOptions) -> None:
    try:
        _state_file().write_text(json.dumps({"pid": pid, **asdict(opts)}))
    except OSError:
        pass  # status falls back to the defaults


def _read_state(pid: int | None) -> ServeOptions:
    """Options the running server *pid* was started with, else the defaults."""
    try:
        data = json.loads(_state_file().read_text())
        if pid is not None and data.pop("pid", None) == pid:
            return ServeOptions(**data)
    except (OSError, ValueError, TypeError, AttributeError):
        pass
    return ServeOptions()


def _kill_tmux_session(name: str = "tui") -> None:
    """Kill a legacy tmux session (older versions ran the TUI inside tmux)."""
    try:
        subprocess.run(
            ["tmux", "kill-session", "-t", name], capture_output=True, check=False
        )
    except OSError:
        pass  # tmux not installed: nothing legacy to clean up


def serve_argv(opts: ServeOptions) -> list[str]:
    """Command that starts the server: ``megachatd serve ... --project-dir
    <repo>``.

    Raises :class:`serverbin.ServerBinaryNotFound` (Italian message)."""
    return serverbin.serve_command(opts.serve_args(), repo=PROJECT_DIR)


def _spawn_server(opts: ServeOptions) -> subprocess.Popen:
    """Start the headless server detached from this terminal and session."""
    argv = serve_argv(opts)
    # The child inherits its own descriptor; closing ours after Popen is safe.
    with open(SERVER_LOG, "ab") as log:
        return subprocess.Popen(
            argv,
            cwd=PROJECT_DIR,
            start_new_session=True,
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=log,
        )


def _pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except PermissionError:
        return True  # exists, owned by someone else
    except OSError:
        return False
    try:  # a zombie nobody reaped yet is as good as gone
        with open(f"/proc/{pid}/stat", "rb") as handle:
            return handle.read().rpartition(b")")[2].split()[0] != b"Z"
    except (OSError, IndexError):
        return True


def server_pid() -> int | None:
    """Pid in the instance lock if that process is alive, else ``None``.

    "Running" means the lock holder is alive: the lock belongs to the
    ``megachat serve`` process.
    """
    try:
        pid = int(LOCK_FILE.read_text().strip())
    except (OSError, ValueError):
        return None
    return pid if _pid_alive(pid) else None


def _socket_server() -> tuple[Path, int] | None:
    """``(socket, pid)`` of a server that published its runtime file, if alive.

    Covers servers started by a system service, which listens on
    ``~/.local/state/megachat/server.sock`` (see
    :func:`client.paths.candidate_socket_paths`).
    """
    for socket_path in candidate_socket_paths():
        try:
            data = json.loads(runtime_file_path(socket_path).read_text("utf-8"))
            pid = data.get("pid")
        except (OSError, ValueError, AttributeError):
            continue
        if isinstance(pid, int) and not isinstance(pid, bool) and _pid_alive(pid):
            return socket_path, pid
    return None


def _server_pids() -> list[int]:
    """Live server pids known from the lock and from the socket runtime files."""
    pids: list[int] = []
    pid = server_pid()
    if pid is not None:
        pids.append(pid)
    found = _socket_server()
    if found is not None and found[1] not in pids:
        pids.append(found[1])
    return pids


def stop_server() -> bool:
    """Stop the instance holding the lock (and a system-service server on its
    socket), SIGINT = clean stop, plus any legacy tmux session; wait for the
    processes to exit.  Attached TUIs just lose their connection.

    Returns ``False`` (lock left in place, so no second server can start next
    to a half-stopped one) when a process is still alive after the timeout.
    """
    pids = _server_pids()
    for pid in pids:
        try:
            os.kill(pid, signal.SIGINT)
        except OSError:
            pass
    for _ in range(int(STOP_TIMEOUT_S / 0.5)):
        if not any(_pid_alive(pid) for pid in pids):
            break
        time.sleep(0.5)
    if any(_pid_alive(pid) for pid in pids):
        return False
    _kill_tmux_session()
    LOCK_FILE.unlink(missing_ok=True)
    _state_file().unlink(missing_ok=True)
    return True


def _ready(proc: subprocess.Popen, opts: ServeOptions) -> bool:
    if opts.no_web:
        # No port to probe: the server is up once it holds the lock.
        return server_pid() == proc.pid
    return port_listening(opts.web_port, opts.probe_host())


def spawn_and_wait(opts: ServeOptions | None = None) -> bool:
    """Spawn the detached server and wait until it is up.

    "Up" is the web adapter listening, or (with ``no_web``) the server
    holding the lock.  ``False`` when it never came up.  ``ServerStartError``
    when it exited before becoming ready (carrying its own error, e.g. an
    enabled integration missing its settings).  ``OSError`` from the spawn
    itself and ``serverbin.ServerBinaryNotFound`` propagate.
    """
    opts = opts or ServeOptions()
    offset = _log_offset()
    proc = _spawn_server(opts)
    for _ in range(READY_TIMEOUT_S):
        if proc.poll() is not None:
            raise ServerStartError(_startup_error(offset))
        if _ready(proc, opts):
            _write_state(proc.pid, opts)
            _show_notices(offset)
            return True
        time.sleep(1)
    return False


def _log_offset() -> int:
    try:
        return SERVER_LOG.stat().st_size
    except OSError:
        return 0


def _log_since(offset: int) -> str:
    try:
        with open(SERVER_LOG, "rb") as handle:
            handle.seek(offset)
            return handle.read().decode("utf-8", "replace")
    except OSError:
        return ""


def _startup_error(offset: int) -> str:
    """The server's own reason for not starting, from its output after *offset*.

    megachatd prefixes the startup validation error with ``❌``; when there is
    no marker, the tail of this run's output is used instead.
    """
    text = _log_since(offset)
    marker = text.find("❌")
    if marker >= 0:
        detail = text[marker:].strip()
    else:
        detail = "\n".join(text.strip().splitlines()[-10:])
    if not detail:
        return "Il server non è partito prima di mettersi in ascolto."
    return "Il server non è partito:\n" + detail


def _show_notices(offset: int) -> None:
    """Print the one-time notices (lines starting with ``ℹ️``, e.g. the WhatsApp
    re-link notice) that megachatd wrote to its output during this start."""
    for line in _log_since(offset).splitlines():
        if line.startswith("ℹ️"):
            print(line)


def _local_ip() -> str:
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.connect(("8.8.8.8", 80))
            return sock.getsockname()[0]
    except OSError:
        return "127.0.0.1"


def _read_web_token() -> str:
    config_file = PROJECT_DIR / "config.json"
    try:
        data = json.loads(config_file.read_text(encoding="utf-8"))
        return str((data.get("web") or {}).get("token", ""))
    except (OSError, json.JSONDecodeError, AttributeError):
        return ""


def _pgrep(pattern: str) -> bool:
    if not command_exists("pgrep"):
        return False
    return (
        subprocess.run(
            ["pgrep", "-f", pattern], capture_output=True, check=False
        ).returncode
        == 0
    )


def _web_url(opts: ServeOptions) -> str:
    # The address worth sharing: the first one other machines can reach.
    hosts = opts.web_hosts()
    loopback = ("127.0.0.1", "::1", "localhost")
    host = next((h for h in hosts if h not in loopback), hosts[0])
    if host in ("0.0.0.0", "::", ""):
        host = _local_ip()
    return f"http://{host}:{opts.web_port}"


def _describe(opts: ServeOptions) -> str:
    if opts.no_web:
        return "senza Web UI"
    auth = ", senza token" if opts.web_no_auth else ""
    return f"con Web UI su {opts.web_host}:{opts.web_port}{auth}"


def start_all(
    *,
    print_token: bool = True,
    opts: ServeOptions | None = None,
    foreground: bool = False,
) -> int:
    """Restart the headless server (megachatd)."""
    opts = opts or ServeOptions()
    loopback = all(
        host in ("127.0.0.1", "::1", "localhost") for host in opts.web_hosts()
    )
    if opts.web_no_auth and not opts.no_web and not loopback:
        warn(
            "Web UI senza token su un indirizzo raggiungibile dalla rete: chiunque "
            f"raggiunga {opts.web_host}:{opts.web_port} legge e invia messaggi."
        )
    info(f"Avvio il server headless (megachatd serve) {_describe(opts)}...")
    # Restart semantics: stop a running instance (lock pid, SIGINT) and any
    # legacy tmux 'tui' session before taking the lock again.
    if not stop_server():
        die(
            f"Il server (pid {server_pid()}) non si ferma: non ne avvio un altro. "
            "Controlla: tail -50 /tmp/signal-tui.log"
        )
        return 1
    if foreground:
        # Same process, this terminal: Ctrl+C stops it cleanly.
        try:
            argv = serve_argv(opts)
        except serverbin.ServerBinaryNotFound as exc:
            die(str(exc))
            return 1
        os.chdir(PROJECT_DIR)
        _write_state(os.getpid(), opts)  # exec keeps the pid
        os.execv(argv[0], argv)
    try:
        up = spawn_and_wait(opts)
    except serverbin.ServerBinaryNotFound as exc:
        die(str(exc))
        return 1
    except ServerStartError as exc:
        die(str(exc))
        return 1
    except OSError as exc:
        die(f"Impossibile avviare il server: {exc}")
        return 1
    if up:
        if opts.no_web:
            ok("Server attivo (senza Web UI)")
        else:
            ok(f"Server attivo — Web UI su {_web_url(opts)}")
            if opts.web_no_auth:
                print("   Token Web UI: disattivato (--web-no-auth)")
            elif print_token:
                print(f"   Token Web UI: {_read_web_token()}")
        print("   Log:          tail -f /tmp/signal-tui.log")
        print("   Stop:         python3 launcher.py server stop")
        print("   Interattivo:  python3 megachat.py  (si collega al server)")
        return 0
    where = "senza Web UI" if opts.no_web else f"sulla porta {opts.web_port}"
    die(
        f"Il server non risulta attivo {where}. Controlla: "
        f"tail -50 /tmp/signal-tui.log {SERVER_LOG}"
    )
    return 1


def stop_all() -> int:
    """Stop the server."""
    info("Spegnimento server (SIGINT pulito)...")
    if not stop_server():
        die(f"Il server (pid {server_pid()}) non si ferma.")
        return 1
    ok("Server fermato")
    ok("Server spento. Per riaccendere: python3 launcher.py server start")
    return 0


def status() -> int:
    pid = server_pid()
    socket_path: Path | None = None
    if pid is None:
        found = _socket_server()
        if found is not None:
            socket_path, pid = found
    if pid is not None:
        where = f", socket {socket_path}" if socket_path else ""
        print(f"Server: ATTIVO (pid {pid}, megachatd{where})")
    else:
        print("Server: spento")
    print(
        "daemon signal-cli: attivo"
        if _pgrep("signal-cli.*daemon")
        else "daemon signal-cli: spento"
    )
    opts = _read_state(pid)
    if opts.no_web:
        print("Web UI: disattivata (--no-web)")
    elif port_listening(opts.web_port, opts.probe_host()):
        auth = ", senza token" if opts.web_no_auth else ""
        print(f"Web UI: in ascolto su {opts.web_host}:{opts.web_port}{auth}")
    else:
        print("Web UI: spenta")
    return 0
