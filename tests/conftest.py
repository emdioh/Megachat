"""
Shared fixtures for regression tests.
All tests use in-memory / tmp_path to avoid touching real files or daemons.
"""

from __future__ import annotations

import sqlite3
import sys
from contextlib import contextmanager
from pathlib import Path

import pytest

# ── Fixtures ──────────────────────────────────────────────────────────────────


@pytest.fixture(autouse=True)
def fail_on_unclosed_sqlite(monkeypatch: pytest.MonkeyPatch):
    """Fail any test that leaves a SQLite connection open.

    ``ResourceWarning: unclosed database`` is only emitted when the garbage
    collector finalises a leaked connection, i.e. inside an unrelated later
    test, so ``-W error`` cannot attribute or reliably fail it. Instead every
    connection opened through ``sqlite3.connect`` during the test is recorded
    (with strong references, so none is finalised early) and checked at
    teardown. ``with closing(sqlite3.connect(...)) as conn, conn`` does not close: use
    ``contextlib.closing`` or an explicit ``close()``.
    """
    real_connect = sqlite3.connect
    opened: list[tuple[sqlite3.Connection, str]] = []

    def recording_connect(*args, **kwargs):
        conn = real_connect(*args, **kwargs)
        frame = sys._getframe(1)
        site = f"{frame.f_code.co_filename}:{frame.f_lineno} in {frame.f_code.co_name}"
        opened.append((conn, site))
        return conn

    monkeypatch.setattr(sqlite3, "connect", recording_connect)
    yield
    leaked = []
    for conn, site in opened:
        try:
            conn.execute("SELECT 1")
        except sqlite3.ProgrammingError:
            continue  # closed
        except sqlite3.Error:
            pass  # open but unusable: still a leak
        leaked.append(site)
        conn.close()
    if leaked:
        pytest.fail(
            "SQLite connection(s) left open (use contextlib.closing / close()):\n  "
            + "\n  ".join(leaked),
            pytrace=False,
        )


@pytest.fixture(autouse=True)
def isolate_server_socket(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """No test may probe the user's real server socket: the TUI's attach and
    ``megachat serve`` resolve their default path from this."""
    path = tmp_path / "megachat-run" / "server.sock"
    monkeypatch.setenv("MEGACHAT_SOCKET", str(path))
    return path


@pytest.fixture(autouse=True)
def isolate_instance_lock(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """Child processes (spawned servers, shell aliases) resolve the instance
    lock from this: the real ``/tmp/signal-tui.lock`` is never touched.
    (In-process code reads ``client.lock.LOCK_FILE``, which tests patch.)"""
    path = tmp_path / "signal-tui.lock"
    monkeypatch.setenv("MEGACHAT_LOCK_FILE", str(path))
    return path


@pytest.fixture(autouse=True)
def isolate_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    """Clear SIGNAL_TUI_WEB_TOKEN so web tests are deterministic.

    Only this variable is cleared: SIGNAL_USER_NUMBER, WHATSAPP_API_URL and the
    LIVE_* flags must stay visible so the opt-in live tests remain runnable.
    """
    monkeypatch.delenv("SIGNAL_TUI_WEB_TOKEN", raising=False)


@pytest.fixture
def sample_messages() -> dict[str, list[dict]]:
    """Return a sample in-memory cache with recent timestamps."""
    import time

    now_ms = int(time.time() * 1000)
    return {
        "+391234567890": [
            {
                "text": "Ciao!",
                "is_mine": False,
                "sender": "Mario",
                "timestamp": now_ms,
                "quote_text": None,
                "msg_type": "text",
                "attachment_info": None,
                "attachment_id": None,
                "read": False,
                "status": "read",
            },
            {
                "text": "Come stai?",
                "is_mine": True,
                "sender": "You",
                "timestamp": now_ms + 1,
                "quote_text": None,
                "msg_type": "text",
                "attachment_info": None,
                "attachment_id": None,
                "read": True,
                "status": "sent",
            },
        ],
        "+391111111111": [
            {
                "text": "Messaggio recente",
                "is_mine": False,
                "sender": "Luigi",
                "timestamp": now_ms - 1000,
                "quote_text": None,
                "msg_type": "text",
                "attachment_info": None,
                "attachment_id": None,
                "read": False,
                "status": "read",
            },
        ],
    }


@pytest.fixture
def sample_envelope_text() -> dict:
    """Return a sample envelope with a text dataMessage."""
    return {
        "source": "+391234567890",
        "sourceNumber": "+391234567890",
        "sourceName": "Mario",
        "timestamp": 2000000,
        "dataMessage": {
            "message": "Hello!",
            "timestamp": 2000000,
            "quote": {},
        },
    }


@pytest.fixture
def sample_envelope_image() -> dict:
    """Return a sample envelope with an image attachment."""
    return {
        "source": "+391234567890",
        "sourceNumber": "+391234567890",
        "sourceName": "Mario",
        "timestamp": 3000000,
        "dataMessage": {
            "message": "",
            "timestamp": 3000000,
            "attachments": [
                {
                    "contentType": "image/jpeg",
                    "filename": "photo.jpg",
                    "id": "att-123",
                    "caption": "Guarda!",
                },
            ],
            "quote": {},
        },
    }


@pytest.fixture
def sample_envelope_quoting_image() -> dict:
    """Return a Signal envelope whose ``dataMessage`` quotes an image (no text).

    Sister fixture of ``sample_envelope_image``, used by bug #37 tests to
    exercise the media-quote fallback on the ``dataMessage`` path.
    """
    return {
        "source": "+391234567890",
        "sourceNumber": "+391234567890",
        "sourceName": "Mario",
        "timestamp": 5000000,
        "dataMessage": {
            "message": "Guarda!",
            "timestamp": 5000000,
            "quote": {
                "id": 4999000,
                "author": "+391234567890",
                "attachments": [{"contentType": "image/jpeg"}],
            },
        },
    }


@pytest.fixture
def wa_event_quoting_sticker() -> dict:
    """Return a raw WAHA message that quotes a sticker (nested ``quotedMessage``)."""
    return {
        "chatId": "391234567890@c.us",
        "from": "391234567890@c.us",
        "text": "guarda!",
        "timestamp": 1700000000,
        "quotedMessage": {"stickerMessage": {"id": "sticker-id-1"}},
    }


@pytest.fixture
def cached_media_target() -> dict:
    """Return a Telegram cache entry for a photo without a caption."""
    return {
        "id": "12",
        "text": "",
        "is_mine": False,
        "sender": "Ada",
        "timestamp": 1735787045000,
        "quote_text": None,
        "msg_type": "image",
        "attachment_info": "Photo",
        "attachment_id": "tgref:42:12",
    }


@pytest.fixture
def sample_envelope_receipt() -> dict:
    """Return a sample receiptMessage envelope."""
    return {
        "source": "+391234567890",
        "sourceNumber": "+391234567890",
        "timestamp": 4000000,
        "receiptMessage": {
            "isDelivery": True,
            "isRead": False,
            "timestamps": [1000001],
        },
    }


@pytest.fixture
def sample_contacts_rpc_output() -> list[dict]:
    """Return sample contact list as returned by RPC."""
    return [
        {"number": "+391234567890", "name": "Mario Rossi", "uuid": "uuid-123"},
        {"number": "+391111111111", "name": "Luigi Verdi", "uuid": "uuid-456"},
    ]


@pytest.fixture
def sample_contacts_subprocess_output() -> str:
    """Return sample contact list as returned by signal-cli subprocess."""
    return (
        "Number:+391234567890 Name:Mario ACI:uuid-123\n"
        "Number:+391111111111 Name:Luigi Verdi ACI:uuid-456 Profile name:Luigi\n"
    )


@contextmanager
def _make_test_app():
    """Build a ``SignalTUI`` around a mock ``ChatClient`` with neutralized workers.

    The TUI is a client of megachatd: there are no backends in the process.
    ``app.client`` is a ``MagicMock`` (calls return mocks unless a test
    configures them), ``on_mount`` does not start the poll worker or resync,
    background workers are no-ops, and three Signal contacts are rendered.
    """
    from unittest.mock import MagicMock, patch

    from textual.containers import Vertical

    from models import PROTOCOL_SIGNAL, ChatContact
    from tui.app import SignalTUI

    def _noop_on_mount(self: SignalTUI) -> None:
        """Mount the UI without starting poll/resync workers, then render contacts."""
        self._chat_log = self.query_one("#chat-log", Vertical)
        self._render_contact_list(list(self.contacts))

    with patch.object(SignalTUI, "on_mount", _noop_on_mount):
        app = SignalTUI(client=MagicMock())
        app.run_worker = MagicMock()
        app.contacts = [
            ChatContact(
                id="+391234567890", display_name="Mario", protocol=PROTOCOL_SIGNAL
            ),
            ChatContact(
                id="+391111111111", display_name="Luigi", protocol=PROTOCOL_SIGNAL
            ),
            ChatContact(
                id="+392222222222", display_name="Giulia", protocol=PROTOCOL_SIGNAL
            ),
        ]
        yield app


@pytest.fixture
def app_for_test():
    """Create a ``SignalTUI`` instance ready for ``App.run_test()`` headless.

    No real I/O happens: the client is a mock, the ``on_mount`` workers are
    neutralized, background workers are no-ops, and a small set of fake
    contacts is injected and rendered.
    """
    with _make_test_app() as app:
        yield app
