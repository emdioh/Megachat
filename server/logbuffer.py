"""In-memory ring buffer of recent log records (the web "Log" dialog).

``install()`` attaches one :class:`RingBufferHandler` to the root logger
(idempotent).  Records are kept already formatted and redacted: API keys
(``sk-...``, Google ``AIza...``) and Bearer tokens never leave the process.
"""

from __future__ import annotations

import logging
import re
import threading
import time
from collections import deque
from typing import Any

MAX_RECORDS = 2000
_MAX_MESSAGE = 4000

_REDACTIONS = (
    (re.compile(r"\bsk-[A-Za-z0-9_\-]{6,}"), "sk-***"),
    (re.compile(r"\bAIza[0-9A-Za-z_\-]{10,}"), "AIza***"),
    (re.compile(r"(Bearer token:\s*)\S+"), r"\1***"),
    (re.compile(r"(Bearer\s+)[A-Za-z0-9._\-]{8,}"), r"\1***"),
)


def redact(text: str) -> str:
    for pattern, replacement in _REDACTIONS:
        text = pattern.sub(replacement, text)
    return text


class RingBufferHandler(logging.Handler):
    def __init__(self, capacity: int = MAX_RECORDS) -> None:
        super().__init__(level=logging.DEBUG)
        self._records: deque[dict[str, Any]] = deque(maxlen=capacity)
        self._seq = 0
        self._buf_lock = threading.Lock()

    def emit(self, record: logging.LogRecord) -> None:
        try:
            message = record.getMessage()
            if record.exc_info:
                message += "\n" + logging.Formatter().formatException(record.exc_info)
            message = redact(message)[:_MAX_MESSAGE]
            with self._buf_lock:
                self._seq += 1
                self._records.append(
                    {
                        "seq": self._seq,
                        "time": record.created,
                        "level": record.levelname,
                        "logger": record.name,
                        "message": message,
                    }
                )
        except Exception:  # noqa: BLE001 - never break logging
            self.handleError(record)

    def snapshot(
        self, after: int = 0, limit: int = 500, min_level: int = 0
    ) -> list[dict[str, Any]]:
        with self._buf_lock:
            rows = [
                r
                for r in self._records
                if r["seq"] > after and logging.getLevelName(r["level"]) >= min_level
            ]
        return rows[-limit:] if limit > 0 else rows

    def clear(self) -> None:
        with self._buf_lock:
            self._records.clear()


_handler: RingBufferHandler | None = None
_install_lock = threading.Lock()
_installed_at: float | None = None


def install() -> RingBufferHandler:
    """Attach the buffer to the root logger (once).

    The level stays the entry point's (``basicConfig`` INFO, DEBUG with
    ``--debug``): the transcription diagnostics are INFO.
    """
    global _handler, _installed_at
    with _install_lock:
        if _handler is None:
            _handler = RingBufferHandler()
            _installed_at = time.time()
        root = logging.getLogger()
        if _handler not in root.handlers:
            root.addHandler(_handler)
        return _handler


def get_handler() -> RingBufferHandler:
    return _handler if _handler is not None else install()


def installed_at() -> float | None:
    return _installed_at
