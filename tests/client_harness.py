"""Stand-in clients and app helpers for tests of TUI code paths."""

from __future__ import annotations

from typing import Any
from unittest.mock import MagicMock

from models import ChatContact


class StubClient:
    """Records ``call`` and answers from a script (value or exception)."""

    def __init__(self, *answers: Any) -> None:
        self.answers = list(answers)
        self.calls: list[tuple[str, dict]] = []
        #: Timeout passed with each call, parallel to ``calls``.
        self.timeouts: list[float | None] = []

    def call(
        self, name: str, data: dict | None = None, *, timeout: float | None = None
    ) -> Any:
        self.calls.append((name, data or {}))
        self.timeouts.append(timeout)
        answer = self.answers.pop(0) if self.answers else {"ok": True}
        if isinstance(answer, BaseException):
            raise answer
        return answer


def view_app(client: Any, protocol: str = "signal", cid: str = "+39123"):
    """A ``SignalTUI`` whose DOM-touching parts are stubbed, talking to *client*."""
    from signal_tui import SignalTUI

    app = SignalTUI(client=client)
    contact = ChatContact(id=cid, display_name="Mario", protocol=protocol)
    app.contacts = [contact]
    app.selected_contact = contact
    app._is_completion_visible = MagicMock(return_value=False)
    app.query_one = MagicMock(side_effect=Exception("no DOM in test"))
    app._add_message = MagicMock()
    app._cancel_reply = MagicMock()
    app._status = MagicMock()
    app._update_message_widgets_status = MagicMock()
    app.call_from_thread = MagicMock(side_effect=lambda fn, *a, **k: fn(*a, **k))
    app.run_worker = MagicMock()
    app._reply_to = None
    app._cache = {}
    return app, contact


def run_workers(app) -> None:
    """Run every scheduled ``run_worker`` callable inline."""
    for call in app.run_worker.call_args_list:
        call.args[0]()


def inline_client_calls(app: Any, *mixins: type) -> Any:
    """Give a stand-in app the thread helpers of ``ClientCallsMixin`` (inline).

    ``_offload``/``_ui`` run their callable at once, ``_call_op`` calls
    ``app.client`` with the configured timeout and transport errors are not
    reported.  Methods of *mixins* the app does not define are bound to it, so
    ``SimpleNamespace`` stand-ins can run code that hands work to a worker.
    """
    from types import MethodType

    from tui.client_timeouts import call_op

    defaults: dict[str, Any] = {
        "is_running": False,
        "_offload": lambda work: work(),
        "_ui": lambda fn, *a: fn(*a),
        "_call_op": lambda op, data=None: call_op(app.client, op, data),
        "_report_client_error": lambda exc, what="": False,
    }
    for name, value in defaults.items():
        if not hasattr(app, name):
            setattr(app, name, value)
    for mixin in mixins:
        for name, fn in vars(mixin).items():
            if callable(fn) and not hasattr(app, name):
                setattr(app, name, MethodType(fn, app))
    return app
