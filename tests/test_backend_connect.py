"""Regression tests for ``BackendConnectMixin`` auto-selection.

L'auto-selezione del primo contatto attende che TUTTI i backend abbiano
riportato un esito (ready o fallito): solo allora, se non c'è ancora una
selezione e ci sono contatti, seleziona il contatto in cima alla lista.
"""

from __future__ import annotations

import sys
from pathlib import Path
from unittest.mock import MagicMock

PROJECT_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(PROJECT_ROOT))

from models import (
    PROTOCOL_SIGNAL,
    ChatContact,
)
from signal_tui import SignalTUI


def _make_app() -> SignalTUI:
    """App with the heavy/UI-touching methods of _on_backend_ready stubbed."""
    app = SignalTUI()
    app._render_contact_list = MagicMock()
    app._update_unread_badges = MagicMock()
    app._status = MagicMock()

    # Real _select_contact touches the DOM; emulate only its selection effect.
    def _select(contact: ChatContact) -> None:
        app.selected_contact = contact

    app._select_contact = MagicMock(side_effect=_select)
    return app


def _ready(app: SignalTUI, backend: MagicMock) -> None:
    """Run ``_on_backend_ready``: contacts reach the TUI through the core's
    ``contacts.for_protocol`` operation, which reads ``manager.get(protocol)``."""
    app.core.manager = MagicMock()
    app.core.manager.get.return_value = backend
    app._on_backend_ready(backend.protocol)


def _make_backend(
    contacts: list[ChatContact], protocol: str = PROTOCOL_SIGNAL
) -> MagicMock:
    backend = MagicMock()
    backend.protocol = protocol
    backend.cache = {}
    backend.contacts = contacts
    return backend


class TestBackendReadyAutoSelect:
    def test_last_backend_failure_still_selects_first_contact(self):
        app = _make_app()
        anna = ChatContact(id="+1", display_name="Anna", protocol=PROTOCOL_SIGNAL)
        app.contacts = [anna]
        # L'ultimo backend (fallito) non ha ancora riportato.
        app._pending_backends = {"signal"}

        app._mark_backend_done("signal")

        app._select_contact.assert_called_once_with(anna)
        assert app.selected_contact is anna

    def test_no_selection_when_no_contacts(self):
        app = _make_app()
        app._pending_backends = {"signal"}

        app._mark_backend_done("signal")

        app._select_contact.assert_not_called()
        assert app.selected_contact is None

    def test_mark_backend_connecting_adds_to_pending(self):
        app = _make_app()

        app._mark_backend_connecting("signal")
        app._mark_backend_connecting("whatsapp")

        assert app._pending_backends == {"signal", "whatsapp"}


def _media_msg(
    *,
    mid: str,
    att: str | None,
    text: str,
    is_mine: bool = True,
    ts: int = 1787648916285,
    msg_type: str = "image",
    status: str = "sent",
) -> dict:
    """Build a cached media-message dict coherent with the UI cache."""
    return {
        "id": mid,
        "text": text,
        "is_mine": is_mine,
        "sender": "You" if is_mine else "Mario",
        "timestamp": ts,
        "msg_type": msg_type,
        "attachment_info": text,
        "attachment_id": att,
        "content_type": "image/jpeg",
        "read": is_mine,
        "status": status,
    }
