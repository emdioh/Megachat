"""Per-instance locations: run several Megachat servers on one machine.

Every piece of per-user state (``config.json``, ``.env``, the SQLite cache and
media, the Telegram session, the signal-cli account data, the lock file, the
Unix socket, the logs) is resolved here.

* ``MEGACHAT_HOME`` unset (the default): the historical layout, unchanged —
  ``config.json`` / ``.env`` in the project directory, data under
  ``~/.local/share/signal-tui-client``, lock in ``/tmp/signal-tui.lock``, logs
  in ``/tmp``.
* ``MEGACHAT_HOME=/path/to/instance``: everything lives under that directory::

      <home>/config.json     settings (web, auto_transcribe, user_number, ...)
      <home>/.env            secrets + ports (WAHA_API_KEY, WHATSAPP_API_PORT,
                             CLIENT_WEBHOOK_PORT, OPENAI_API_KEY, ...)
      <home>/data/           messages.db, whatsapp-media/, telegram.session ...
      <home>/signal-cli/     signal-cli --config directory
      <home>/cache/          web thumbnails
      <home>/run/            lock file, server.sock, server.json
      <home>/logs/           signal-tui.log, megachat-serve.log, ...

  On import the instance ``.env`` is also loaded into ``os.environ`` (without
  overriding variables already set), so the ports it declares
  (``WHATSAPP_API_PORT``, ``CLIENT_WEBHOOK_PORT``, ``SIGNAL_DAEMON_PORT``,
  ``MEGACHAT_DOWNLOAD_PORT``) reach the module-level constants that read them.

``scripts/megachat_instance.sh`` creates and runs such instances.  Stdlib only;
import this module before any module that reads those environment variables.
"""

from __future__ import annotations

import hashlib
import os
from collections.abc import Mapping
from pathlib import Path

__all__ = [
    "HOME_ENV",
    "PROJECT_DIR",
    "cache_dir",
    "config_file",
    "data_dir",
    "dotenv_file",
    "home",
    "load_instance_env",
    "lock_file",
    "log_file",
    "signal_cli_config_args",
    "signal_cli_dir",
    "socket_file",
]

HOME_ENV = "MEGACHAT_HOME"
PROJECT_DIR = Path(__file__).resolve().parent


def home() -> Path | None:
    """The instance directory (``$MEGACHAT_HOME``), or ``None`` (default layout)."""
    raw = os.environ.get(HOME_ENV, "").strip()
    if not raw:
        return None
    return Path(raw).expanduser().resolve()


def _settings_dir() -> Path:
    return home() or PROJECT_DIR


def config_file() -> Path:
    """``config.json`` of this instance."""
    return _settings_dir() / "config.json"


def dotenv_file() -> Path:
    """``.env`` of this instance."""
    return _settings_dir() / ".env"


def data_dir() -> Path:
    """Message cache, media and session files."""
    root = home()
    if root is not None:
        return root / "data"
    return Path.home() / ".local" / "share" / "signal-tui-client"


def signal_cli_dir() -> Path:
    """signal-cli data directory (accounts, attachments, avatars)."""
    root = home()
    if root is not None:
        return root / "signal-cli"
    return Path.home() / ".local" / "share" / "signal-cli"


def signal_cli_config_args() -> list[str]:
    """Global signal-cli options selecting this instance's data directory.

    Empty in the default layout, so signal-cli keeps using its own default
    (``~/.local/share/signal-cli``) exactly as before.
    """
    if home() is None:
        return []
    return ["--config", str(signal_cli_dir())]


def cache_dir() -> Path | None:
    """Disposable cache root, or ``None`` for the default (``XDG_CACHE_HOME``)."""
    root = home()
    return root / "cache" if root is not None else None


def lock_file() -> str:
    """Single-instance lock (``$MEGACHAT_LOCK_FILE`` always wins)."""
    explicit = os.environ.get("MEGACHAT_LOCK_FILE")
    if explicit:
        return explicit
    root = home()
    if root is not None:
        return str(root / "run" / "signal-tui.lock")
    return "/tmp/signal-tui.lock"


#: ``sun_path`` limit (104 bytes on BSD/macOS, NUL included), as in
#: ``server.socket_adapter``.
_MAX_SOCKET_PATH_BYTES = 103


def socket_file(env: Mapping[str, str] | None = None) -> Path | None:
    """Default server socket of this instance, or ``None`` (XDG default).

    ``<home>/run/server.sock`` — unless that path is too long for a Unix
    socket (deep instance directories): then a short per-instance name in
    ``$XDG_RUNTIME_DIR/megachat/`` (else ``/tmp/megachat-<uid>/``).
    """
    env = os.environ if env is None else env
    raw = env.get(HOME_ENV, "").strip()
    if not raw:
        return None
    root = Path(raw).expanduser().resolve()
    preferred = root / "run" / "server.sock"
    if len(os.fsencode(preferred)) <= _MAX_SOCKET_PATH_BYTES:
        return preferred
    digest = hashlib.sha1(os.fsencode(root)).hexdigest()[:8]
    runtime = env.get("XDG_RUNTIME_DIR", "")
    if runtime and os.path.isabs(runtime):
        base = Path(runtime) / "megachat"
    else:
        base = Path("/tmp") / f"megachat-{os.getuid()}"
    return base / f"{root.name[:32]}-{digest}.sock"


def log_file(name: str) -> str:
    """Path of log *name* (``/tmp/<name>`` by default, else ``<home>/logs``)."""
    root = home()
    if root is None:
        return f"/tmp/{name}"
    logs = root / "logs"
    try:
        logs.mkdir(parents=True, exist_ok=True)
    except OSError:
        return f"/tmp/{name}"
    return str(logs / name)


def _parse_dotenv(path: Path) -> dict[str, str]:
    data: dict[str, str] = {}
    try:
        with path.open(encoding="utf-8") as handle:
            for raw in handle:
                line = raw.strip()
                if line.startswith("export "):
                    line = line[len("export ") :].strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                key, _, value = line.partition("=")
                value = value.strip()
                if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                    value = value[1:-1]
                data[key.strip()] = value
    except OSError:
        return {}
    return data


def load_instance_env() -> None:
    """Load ``<home>/.env`` into ``os.environ`` (never overriding a set value).

    No-op in the default layout: there the project ``.env`` keeps being read
    on demand only for the few settings that look at it.
    """
    root = home()
    if root is None:
        return
    for key, value in _parse_dotenv(root / ".env").items():
        if key and key not in os.environ:
            os.environ[key] = value


load_instance_env()
