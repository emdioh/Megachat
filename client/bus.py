"""The event envelope clients receive from megachatd.

- ``Event`` — immutable envelope ``{seq, type, payload, ts}``.
- ``GAP`` — synthetic event type telling a subscriber it lost events and must
  resync (reason ``"buffer"``: ``since`` fell out of the server's ring buffer;
  reason ``"overflow"``: the subscriber's queue overflowed).

Consumers must treat payloads as immutable.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

__all__ = ["GAP", "Event"]

GAP = "gap"


@dataclass(frozen=True)
class Event:
    """One server event.  ``seq`` is monotonic per server and starts at 1."""

    seq: int
    type: str
    payload: dict[str, Any]
    ts: float
