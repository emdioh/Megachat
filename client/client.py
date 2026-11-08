"""Client side of the server API: what a frontend (the TUI) talks to.

A :class:`ChatClient` offers the two things every transport carries:

- *operations*: ``call(name, data)`` / ``acall(name, data)`` take and return
  plain JSON data (the registered request/response models validate both
  ends); failures raise :class:`~server.api.ApiError`;
- *events*: ``subscribe(since)`` returns a :class:`ClientSubscription` whose
  events are :class:`~server.bus.Event` envelopes ``(seq, type, payload, ts)``
  with wire-form payloads (:mod:`server.events_api`).  A ``gap`` event
  (:data:`~server.bus.GAP`) means events were lost and the client must
  resync.

:class:`client.socket_client.SocketClient` (the Unix socket to megachatd)
implements both interfaces.  Nothing transport specific lives outside the
concrete client classes.

This module must not import Textual, FastAPI, ``tui`` or ``web``.
"""

from __future__ import annotations

import abc
from types import TracebackType
from typing import Any, Self

from client.bus import GAP, Event
from client.errors import ApiError

__all__ = [
    "GAP",
    "ApiError",
    "ChatClient",
    "ClientSubscription",
    "Event",
]


class ClientSubscription(abc.ABC):
    """A stream of events; same consumer API for every transport."""

    @abc.abstractmethod
    def get(self, timeout: float | None = None) -> Event | None:
        """Block up to *timeout* for the next event; ``None`` on timeout or
        when the subscription is closed."""

    @abc.abstractmethod
    def drain(self) -> list[Event]:
        """Return the events already queued, without blocking."""

    @abc.abstractmethod
    async def aget(self, timeout: float | None = None) -> Event | None:
        """Asyncio flavour of :meth:`get`."""

    @abc.abstractmethod
    def close(self) -> None:
        """Stop the stream and wake blocked getters.  Idempotent."""

    @property
    @abc.abstractmethod
    def closed(self) -> bool: ...

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.close()


class ChatClient(abc.ABC):
    """Operations + events of a chat server, over some transport."""

    @abc.abstractmethod
    def call(
        self,
        name: str,
        data: dict[str, Any] | None = None,
        *,
        timeout: float | None = None,
    ) -> Any:
        """Run operation *name* with JSON request *data*; returns JSON data.

        Raises :class:`ApiError` on a neutral API error.  May block.  *timeout*
        (seconds) bounds how long a remote transport waits for the answer; the
        in-process client runs the operation inline and ignores it.  A remote
        timeout does NOT cancel the operation: its outcome is then unknown
        (see :class:`~server.socket_client.OperationTimeout`).
        """

    @abc.abstractmethod
    async def acall(
        self,
        name: str,
        data: dict[str, Any] | None = None,
        *,
        timeout: float | None = None,
    ) -> Any:
        """Like :meth:`call` from a running event loop."""

    @abc.abstractmethod
    def subscribe(self, since: int | None = None) -> ClientSubscription:
        """Start receiving events; with *since*, replay what was missed after
        that ``seq`` (or get a ``gap`` event when it is no longer available)."""

    @abc.abstractmethod
    def close(self) -> None:
        """Release the transport (subscriptions are closed too)."""

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.close()
