"""The few config.json settings the TUI reads itself (startup only).

Same resolution as megachatd (CONTRACTS.md section 9.1): the environment
variable ``UPPER(domain_setting)`` when set and non-blank, else
``config.json[domain][setting]`` when present and not null, else the default.
``config.json`` lives in ``$MEGACHAT_PROJECT_DIR``, else the repository root.
Everything else is configured in megachatd.
"""

from __future__ import annotations

import json
import logging
import os
from pathlib import Path
from typing import Any

__all__ = ["image_protocol", "web_enabled", "web_host", "web_port", "web_token"]

logger = logging.getLogger(__name__)

REPO_DIR = Path(__file__).resolve().parent.parent
PROJECT_DIR_ENV = "MEGACHAT_PROJECT_DIR"
_TRUE = {"1", "true", "yes", "on"}
_FALSE = {"0", "false", "no", "off"}


def config_file() -> Path:
    project = os.environ.get(PROJECT_DIR_ENV, "").strip()
    return (Path(project).expanduser() if project else REPO_DIR) / "config.json"


def _load() -> dict[str, Any]:
    try:
        data = json.loads(config_file().read_text(encoding="utf-8"))
    except FileNotFoundError:
        return {}
    except (OSError, ValueError):
        logger.warning("Cannot read %s", config_file(), exc_info=True)
        return {}
    return data if isinstance(data, dict) else {}


def _value(domain: str, setting: str) -> Any:
    """Raw value of ``domain.setting``: env override, else config.json, else None."""
    env = os.environ.get(f"{domain}_{setting}".upper(), "")
    if env.strip():
        return env
    section = _load().get(domain)
    if isinstance(section, dict):
        return section.get(setting)
    return None


def _bool(domain: str, setting: str, default: bool) -> bool:
    value = _value(domain, setting)
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        text = value.strip().lower()
        if text in _TRUE:
            return True
        if text in _FALSE:
            return False
    return default


def web_enabled() -> bool:
    """``web.enabled`` (default off)."""
    return _bool("web", "enabled", False)


def web_port() -> int:
    """``web.port`` (default 4242; out-of-range values fall back to it)."""
    value = _value("web", "port")
    try:
        port = int(value) if value is not None and not isinstance(value, bool) else 4242
    except (TypeError, ValueError):
        return 4242
    return port if 1 <= port <= 65535 else 4242


def web_host() -> str:
    """``web.host`` (default 127.0.0.1); a list is returned comma-separated."""
    value = _value("web", "host")
    if value is None:
        return "127.0.0.1"
    if isinstance(value, (list, tuple)):
        return ",".join(str(item) for item in value) or "127.0.0.1"
    return str(value).strip() or "127.0.0.1"


def web_token() -> str:
    """``web.token`` (default empty)."""
    value = _value("web", "token")
    return str(value) if value is not None else ""


def image_protocol() -> str:
    """``image.protocol``: auto (default), kitty, catimg or off."""
    value = _value("image", "protocol")
    text = str(value).strip().lower() if value is not None else ""
    return text or "auto"
