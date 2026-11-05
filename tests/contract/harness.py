"""Process + wire helpers for the black-box contract tests.

Nothing here imports server internals except ``server.socket_client`` (the
client the TUI uses): the servers under test are separate processes that are
reached only through their Unix socket and their HTTP/WebSocket port.
"""

from __future__ import annotations

import base64
import http.client
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

REPO = Path(__file__).resolve().parents[2]
TOKEN = "contract-token"

#: Env vars that would switch a backend on; scrubbed for both servers.
_SCRUB = (
    "SIGNAL_ENABLED", "SIGNAL_USER_NUMBER", "TELEGRAM_API_ID", "TELEGRAM_API_HASH",
    "WHATSAPP_ENABLED", "WHATSAPP_API_URL", "GMAIL_ADDRESS", "GMAIL_APP_PASSWORD",
    "OPENAI_API_KEY", "MEGACHAT_SOCKET", "MEGACHAT_FAKE_BACKEND",
    "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
)


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def short_tmp(prefix: str = "mc") -> Path:
    """A short directory: Unix socket paths are limited to ~103 bytes."""
    return Path(tempfile.mkdtemp(prefix=prefix, dir="/tmp"))


@dataclass
class Resp:
    status: int
    headers: dict[str, str]
    body: bytes

    def json(self) -> Any:
        return json.loads(self.body)


@dataclass
class Server:
    kind: str  # "go" | "py"
    root: Path
    home: Path
    port: int
    token: str
    socket_path: Path
    proc: subprocess.Popen[bytes]
    log_path: Path
    _client: Any = field(default=None, repr=False)

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    @property
    def client(self):
        """A connected :class:`server.socket_client.SocketClient` (lazy)."""
        if self._client is None:
            from server.socket_client import SocketClient

            self._client = SocketClient.connect(
                self.socket_path, timeout=10, reconnect=False
            )
        return self._client

    def call(self, op: str, data: dict[str, Any] | None = None, **kw: Any) -> Any:
        return self.client.call(op, data, **kw)

    # -- HTTP ----------------------------------------------------------------

    def http(
        self,
        method: str,
        path: str,
        *,
        token: str | None = "default",
        headers: dict[str, str] | None = None,
        json_body: Any = None,
        body: bytes | None = None,
        timeout: float = 15,
    ) -> Resp:
        hdrs = dict(headers or {})
        tok = self.token if token == "default" else token
        if tok is not None and "Authorization" not in hdrs:
            hdrs["Authorization"] = f"Bearer {tok}"
        if json_body is not None:
            body = json.dumps(json_body).encode()
            hdrs.setdefault("Content-Type", "application/json")
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=timeout)
        try:
            conn.request(method, path, body=body, headers=hdrs)
            r = conn.getresponse()
            data = r.read()
            return Resp(r.status, {k.lower(): v for k, v in r.getheaders()}, data)
        finally:
            conn.close()

    def get(self, path: str, **kw: Any) -> Resp:
        return self.http("GET", path, **kw)

    # -- lifecycle -----------------------------------------------------------

    def stop(self) -> None:
        if self._client is not None:
            try:
                self._client.close()
            except Exception:
                pass
            self._client = None
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(8)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(5)
        shutil.rmtree(self.root, ignore_errors=True)

    def log(self) -> str:
        out = ""
        for p in (self.log_path, self.root / "server.log"):
            try:
                out += p.read_text(errors="replace")
            except OSError:
                pass
        return out


def _env(root: Path, home: Path, token: str | None) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if k not in _SCRUB}
    run = root / "run"
    run.mkdir(exist_ok=True)
    proj = root / "proj"
    proj.mkdir(exist_ok=True)
    if not (proj / "web").exists():  # the SPA the Go server serves from <project>/web/static
        (proj / "web").symlink_to(REPO / "web")
    env.update(
        HOME=str(home),
        XDG_RUNTIME_DIR=str(run),
        MEGACHAT_LOCK_FILE=str(root / "lock"),
        MEGACHAT_LOG_FILE=str(root / "server.log"),
        MEGACHAT_PROJECT_DIR=str(proj),
        SIGNAL_ENABLED="0",
        GMAIL_UNREAD_POLL_S="0",
        GMAIL_IMAGE_PREFETCH="0",
        PYTHONUNBUFFERED="1",
    )
    if token is not None:
        env["SIGNAL_TUI_WEB_TOKEN"] = token
    else:
        env.pop("SIGNAL_TUI_WEB_TOKEN", None)
    return env


def start_server(
    kind: str,
    *,
    binary: str | None = None,
    seed_home: Path | None = None,
    token: str | None = TOKEN,
    extra_args: tuple[str, ...] = (),
    extra_env: dict[str, str] | None = None,
    py_root: Path | None = None,
) -> Server:
    """Start the Go binary (``kind="go"``) or the Python server (``"py"``) in
    a fresh temp HOME (a copy of *seed_home* when given), no real backend."""
    root = short_tmp("mc" + kind)
    home = root / "home"
    if seed_home is not None:
        shutil.copytree(seed_home, home, symlinks=True)
    else:
        home.mkdir()
    port = free_port()
    sock = root / "run" / "s.sock"
    env = _env(root, home, token)
    env.update(extra_env or {})
    args = ["--web-port", str(port), "--socket", str(sock), *extra_args]
    if kind == "go":
        assert binary
        cmd = [binary, "serve", "--project-dir", str(root / "proj"), *args]
    else:
        cmd = [sys.executable, "-m", "server", *args]
    log = root / "stdout.log"
    with log.open("wb") as fh:
        proc = subprocess.Popen(
            cmd, env=env, cwd=str(py_root or REPO) if kind == "py" else str(root),
            stdout=fh, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
        )
    srv = Server(kind, root, home, port, token or "", sock, proc, log)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            out = srv.log()
            srv.stop()
            raise RuntimeError(f"{kind} server exited early ({proc.returncode}):\n{out[-3000:]}")
        if sock.exists() and _port_open(port):
            return srv
        time.sleep(0.05)
    out = srv.log()
    srv.stop()
    raise RuntimeError(f"{kind} server did not come up:\n{out[-3000:]}")


def copy_python_project(dest: Path) -> Path:
    """A private copy of the Python sources.

    ``protocols.config`` reads and WRITES ``<project>/config.json`` (and reads
    ``.env``) next to the sources, so the Python server must not run from the
    developer's checkout: settings writes would leak into it and between runs.

    ``bin`` (where ``signal-cli`` is installed) is dropped on purpose: the Go
    server runs with an empty ``--project-dir``, so copying the developer's
    ``bin/signal-cli-*`` would give the two sides different link.* behaviour
    (Python would run the installed CLI, Go would report it as missing) and
    make the parity tests depend on the machine, not on the wire contract.
    """
    skip = shutil.ignore_patterns(
        ".git", "tests", "docs", "documentation", "assets", "megachatd", "profiling", "bin",
        "__pycache__", "*.png", "*.pyc", "config.json", ".env", ".venv*", "node_modules",
    )
    shutil.copytree(REPO, dest, ignore=skip)
    return dest


def _port_open(port: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.3):
            return True
    except OSError:
        return False


# -- minimal RFC 6455 client (handshake control the stdlib lacks) ---------------


def b64url(token: str) -> str:
    return base64.urlsafe_b64encode(token.encode()).decode().rstrip("=")


class WSConn:
    def __init__(self, sock: socket.socket, status: int, headers: dict[str, str], rest: bytes):
        self.sock = sock
        self.status = status
        self.headers = headers
        self._buf = rest

    @classmethod
    def open(
        cls,
        port: int,
        path: str,
        *,
        subprotocols: list[str] | None = None,
        headers: dict[str, str] | None = None,
        timeout: float = 5,
    ) -> WSConn:
        sock = socket.create_connection(("127.0.0.1", port), timeout=timeout)
        key = base64.b64encode(os.urandom(16)).decode()
        lines = [
            f"GET {path} HTTP/1.1",
            f"Host: 127.0.0.1:{port}",
            "Upgrade: websocket",
            "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}",
            "Sec-WebSocket-Version: 13",
        ]
        if subprotocols:
            lines.append("Sec-WebSocket-Protocol: " + ", ".join(subprotocols))
        for k, v in (headers or {}).items():
            lines.append(f"{k}: {v}")
        sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = sock.recv(4096)
            if not chunk:
                break
            buf += chunk
        head, _, rest = buf.partition(b"\r\n\r\n")
        first, *hl = head.decode("latin-1").split("\r\n")
        status = int(first.split()[1])
        hd = {}
        for h in hl:
            k, _, v = h.partition(":")
            hd[k.strip().lower()] = v.strip()
        return cls(sock, status, hd, rest)

    def _read(self, n: int) -> bytes:
        while len(self._buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise ConnectionError("closed")
            self._buf += chunk
        out, self._buf = self._buf[:n], self._buf[n:]
        return out

    def recv(self, timeout: float = 5) -> tuple[int, bytes]:
        """Next data/close frame as (opcode, payload); pings are answered."""
        self.sock.settimeout(timeout)
        while True:
            b0, b1 = self._read(2)
            op, ln = b0 & 0x0F, b1 & 0x7F
            if ln == 126:
                ln = int.from_bytes(self._read(2), "big")
            elif ln == 127:
                ln = int.from_bytes(self._read(8), "big")
            payload = self._read(ln)
            if op == 0x9:
                self._send(0xA, payload)
                continue
            if op == 0xA:
                continue
            return op, payload

    def recv_json(self, timeout: float = 5) -> Any:
        op, payload = self.recv(timeout)
        assert op == 1, (op, payload)
        return json.loads(payload)

    def _send(self, op: int, payload: bytes) -> None:
        mask = os.urandom(4)
        n = len(payload)
        head = bytes([0x80 | op])
        if n < 126:
            head += bytes([0x80 | n])
        elif n < 65536:
            head += bytes([0x80 | 126]) + n.to_bytes(2, "big")
        else:
            head += bytes([0x80 | 127]) + n.to_bytes(8, "big")
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(head + mask + masked)

    def send_json(self, obj: Any) -> None:
        self._send(1, json.dumps(obj).encode())

    def close(self) -> None:
        try:
            self.sock.close()
        except OSError:
            pass
