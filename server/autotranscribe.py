"""Automatic transcription of voice notes and the ``!`` chat commands.

Core feature (no Textual/FastAPI/tui/web): one daemon thread consumes the
:class:`~server.bus.EventBus` and reacts to *new* ``message`` events:

* a recent voice note (incoming or the user's own) of WhatsApp / Signal /
  Telegram whose effective setting is on is submitted to the
  :class:`~transcription.service.TranscriptionService`; when the worker is done
  (``on_done`` hook) the transcription is posted in the same chat as a quoted
  reply sent from the user's own account;
* a recent own message starting with ``!`` is a command (``!t``,
  ``!transcription ...``); the answer is a quoted reply to the command.

Blocking work (lazy media download, DB lookup, network send) never runs on the
bus thread nor on the core pump: it goes to short-lived daemon threads.

Read state: the posts go through ``messages.send`` without ``optimistic`` and
nothing here calls ``mark_read`` / ``sendSeen``, so the voice note stays unread
on the phone.
"""

from __future__ import annotations

import logging
import sqlite3
import threading
import time
from collections import deque
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from server.bus import GAP, EventBus
from transcription import config as tconfig

logger = logging.getLogger(__name__)

#: Only messages younger than this are handled (resyncs re-emit old history).
RECENT_WINDOW_S = 600
#: ...and not older than the core start minus this margin.
START_MARGIN_S = 60

TRANSCRIPTION_PREFIX = "🎤 Trascrizione:\n\n"
_EMPTY_TEXT = "(nessun testo riconosciuto)"
_ERROR_TEXT = "⚠️ Trascrizione non riuscita. Riprova più tardi con !t."
_NOT_CONFIGURED_TEXT = (
    "⚠️ Trascrizione non configurata (manca la chiave API del provider)."
)
_T_HINT = "ℹ️ Rispondi a una nota vocale con !t per trascriverla."

_PROTOCOLS = frozenset(tconfig.AUTO_TRANSCRIBE_PROTOCOLS)
_MAX_SEEN = 1000


def _now() -> float:
    return time.time()


@dataclass(frozen=True)
class _Quote:
    """The message a reply quotes."""

    protocol: str
    contact_id: str
    message_id: str | None
    timestamp: int
    text: str = ""


def _onoff(value: bool) -> str:
    return "attiva" if value else "disattiva"


def _usage(protocol: str) -> str:
    return (
        "ℹ️ Comandi:\n"
        "• !t (in risposta a una nota vocale): trascrive la nota\n"
        "• !transcription: mostra lo stato\n"
        "• !transcription on|off: trascrizione automatica in questa chat\n"
        f"• !transcription global on|off: per tutte le chat {protocol}"
    )


class AutoTranscriber:
    """Bus consumer: auto-transcription + commands.  See the module docstring."""

    def __init__(
        self,
        api: Any,
        bus: EventBus,
        *,
        inline: bool = False,
        started_at: float | None = None,
    ) -> None:
        self.api = api
        self.bus = bus
        #: Run the jobs on the calling thread (tests).
        self._inline = inline
        self._started_at = started_at
        self._sub: Any = None
        self._thread: threading.Thread | None = None
        self._lock = threading.Lock()
        #: (protocol, attachment_id) -> where to post once transcribed.
        self._pending: dict[tuple[str, str], _Quote] = {}
        #: Voice notes whose transcription was already posted.
        self._posted: set[tuple[str, str]] = set()
        #: Handled event identities (bounded) and command messages.
        self._seen: deque[tuple[Any, ...]] = deque(maxlen=_MAX_SEEN)
        self._warned_unconfigured = False

    # -- lifecycle ---------------------------------------------------------

    def start(self) -> None:
        if self._thread is not None:
            return
        if self._started_at is None:
            self._started_at = _now()
        self._sub = self.bus.subscribe()
        self.api.transcription.on_done = self.on_transcription_done
        self._thread = threading.Thread(
            target=self._loop, name="auto-transcribe", daemon=True
        )
        self._thread.start()

    def stop(self) -> None:
        sub, thread = self._sub, self._thread
        self._sub = self._thread = None
        if self.api.transcription.on_done == self.on_transcription_done:
            self.api.transcription.on_done = None
        if sub is not None:
            sub.close()
        if thread is not None and thread is not threading.current_thread():
            thread.join(1.0)

    def _loop(self) -> None:
        sub = self._sub
        while sub is not None and not sub.closed:
            event = sub.get(timeout=1.0)
            if event is None:
                continue
            if event.type == GAP:
                # Events were lost: nothing to replay (old voice notes are out
                # of the freshness window anyway).
                logger.warning("Auto-trascrizione: eventi persi (%s)", event.payload)
                continue
            if event.type != "message":
                continue
            try:
                self.handle_message(event.payload)
            except Exception:
                logger.exception("Auto-trascrizione: evento non gestito")

    def _run(self, fn: Callable[..., None], *args: Any) -> None:
        """Run *fn* off the current thread (or inline in tests); never raises."""

        def job() -> None:
            try:
                fn(*args)
            except Exception:
                logger.exception("Auto-trascrizione: operazione fallita")

        if self._inline:
            job()
        else:
            threading.Thread(
                target=job, name="auto-transcribe-job", daemon=True
            ).start()

    # -- filtering ---------------------------------------------------------

    def _is_recent(self, ts_ms: Any) -> bool:
        if isinstance(ts_ms, bool) or not isinstance(ts_ms, (int, float)):
            return False
        ts = ts_ms / 1000.0
        now = _now()
        if now - ts > RECENT_WINDOW_S:
            return False
        started = self._started_at
        return started is None or ts >= started - START_MARGIN_S

    def handle_message(self, payload: dict[str, Any]) -> None:
        """React to one ``message`` bus payload (cheap checks, jobs off-thread)."""
        if payload.get("added") is not True:
            return
        protocol = payload.get("protocol")
        contact_id = payload.get("contact_id")
        message = payload.get("message")
        if (
            protocol not in _PROTOCOLS
            or not contact_id
            or not isinstance(message, dict)
        ):
            return
        ts = message.get("timestamp", payload.get("timestamp"))
        media_kind = message.get("media_kind")
        if media_kind:
            logger.info(
                "Auto-trascrizione: messaggio %s %s media_kind=%s mio=%s "
                "attachment_id=%s recente=%s",
                protocol,
                contact_id,
                media_kind,
                message.get("is_mine"),
                message.get("attachment_id"),
                self._is_recent(ts),
            )
        if not self._is_recent(ts):
            return
        identity = (protocol, contact_id, message.get("id"), ts, message.get("text"))
        if identity in self._seen:
            return
        self._seen.append(identity)

        if message.get("media_kind") == "voice":
            self._run(self._on_voice, protocol, contact_id, message)
            return
        text = message.get("text")
        if (
            message.get("is_mine") is True
            and isinstance(text, str)
            and text.lstrip().startswith("!")
        ):
            self._run(self._on_command, protocol, contact_id, message, text.strip())

    # -- voice notes -------------------------------------------------------

    def _on_voice(self, protocol: str, contact_id: str, message: dict) -> None:
        if not tconfig.auto_transcribe_effective(protocol, contact_id):
            logger.info(
                "Auto-trascrizione disattivata per %s %s (globale=%s, chat=%s): "
                "nota vocale ignorata",
                protocol,
                contact_id,
                tconfig.get_auto_transcribe_global(protocol),
                tconfig.get_auto_transcribe_chat(protocol, contact_id),
            )
            return
        if self.api.transcription.service is None:
            if not self._warned_unconfigured:
                self._warned_unconfigured = True
                logger.warning(
                    "Auto-trascrizione ignorata: trascrizione non configurata "
                    "(provider %s senza chiave o servizio non avviato)",
                    tconfig.get_transcription_provider(),
                )
            return
        outcome = self._request(
            self._voice_quote(protocol, contact_id, message), message
        )
        logger.info(
            "Auto-trascrizione %s %s attachment_id=%s: %s",
            protocol,
            contact_id,
            message.get("attachment_id"),
            outcome,
        )

    @staticmethod
    def _voice_quote(protocol: str, contact_id: str, message: dict) -> _Quote:
        raw_id = message.get("id")
        # Signal quotes by timestamp; its synthetic voice text is not quoted.
        return _Quote(
            protocol,
            contact_id,
            str(raw_id) if raw_id not in (None, "") else None,
            int(message.get("timestamp") or 0),
        )

    def _request(
        self,
        quote: _Quote,
        message: dict,
    ) -> str:
        """Queue the transcription of *message*'s voice note.

        Returns ``"queued"``, ``"posted"`` (already transcribed and posted now),
        ``"duplicate"``, ``"unconfigured"`` or ``"error"``.
        """
        attachment_id = message.get("attachment_id")
        if not isinstance(attachment_id, str) or not attachment_id.strip():
            logger.warning("Auto-trascrizione: nota vocale senza attachment_id")
            return "error"
        key = (quote.protocol, attachment_id.strip())
        if quote.protocol in ("whatsapp", "telegram") and not quote.message_id:
            logger.warning("Auto-trascrizione: nota vocale senza id, ignorata")
            return "error"
        service = self.api.transcription.service
        if service is None:
            return "unconfigured"
        with self._lock:
            if key in self._posted or key in self._pending:
                return "duplicate"
            self._pending[key] = quote
        try:
            current = service.status(*key)
            if current is not None and current.get("status") == "ok":
                self._finish(key, current)
                return "posted"
            if current is None or current.get("status") != "pending":
                blob = self.api.call_json(
                    "media.attachment",
                    {"protocol": key[0], "attachment_id": key[1]},
                )
                path = Path(blob["path"])
                logger.info(
                    "Auto-trascrizione: file %s (%s byte) in coda",
                    path.name,
                    path.stat().st_size if path.exists() else "inesistente",
                )
                service.submit(key[0], key[1], path)
            return "queued"
        except Exception:
            with self._lock:
                self._pending.pop(key, None)
            logger.exception("Auto-trascrizione: invio fallito (%s %s)", key[0], key[1])
            return "error"

    def on_transcription_done(
        self, protocol: str, attachment_id: str, row: dict[str, Any]
    ) -> None:
        """``TranscriptionService`` hook (worker thread): post the result."""
        key = (protocol, attachment_id)
        with self._lock:
            known = key in self._pending
        if known:
            self._run(self._finish, key, row)

    def _finish(self, key: tuple[str, str], row: dict[str, Any]) -> None:
        with self._lock:
            quote = self._pending.pop(key, None)
            if quote is None or key in self._posted:
                return
            ok = row.get("status") == "ok"
            if ok:
                self._posted.add(key)
        if not ok and row.get("status") != "failed":
            return
        logger.info(
            "Auto-trascrizione %s %s: esito %s %s",
            key[0],
            key[1],
            row.get("status"),
            row.get("error") or "",
        )
        if ok:
            body = (row.get("text") or "").strip() or _EMPTY_TEXT
            text = TRANSCRIPTION_PREFIX + body
        else:
            text = _ERROR_TEXT
        if not self._post(quote, text) and ok:
            with self._lock:  # let a later !t retry
                self._posted.discard(key)

    # -- commands ----------------------------------------------------------

    def _on_command(
        self, protocol: str, contact_id: str, message: dict, text: str
    ) -> None:
        parts = text.split()
        name = parts[0].lower()
        args = [a.lower() for a in parts[1:]]
        reply = _Quote(
            protocol,
            contact_id,
            str(message["id"]) if message.get("id") not in (None, "") else None,
            int(message.get("timestamp") or 0),
            text,
        )
        if name == "!t":
            self._cmd_t(protocol, contact_id, message, reply)
        elif name == "!transcription":
            self._cmd_transcription(protocol, contact_id, args, reply)

    def _cmd_t(
        self, protocol: str, contact_id: str, message: dict, reply: _Quote
    ) -> None:
        quoted = self._find_quoted(protocol, contact_id, message)
        if (
            quoted is None
            or quoted.get("media_kind") != "voice"
            or not quoted.get("attachment_id")
        ):
            self._post(reply, _T_HINT)
            return
        if self.api.transcription.service is None:
            self._post(reply, _NOT_CONFIGURED_TEXT)
            return
        raw_id = quoted.get("msg_id")
        voice = _Quote(
            protocol,
            contact_id,
            str(raw_id) if raw_id not in (None, "") else None,
            int(quoted.get("timestamp") or 0),
        )
        outcome = self._request(voice, quoted)
        if outcome == "error":
            self._post(reply, _ERROR_TEXT)

    def _cmd_transcription(
        self, protocol: str, contact_id: str, args: list[str], reply: _Quote
    ) -> None:
        if not args:
            glob = tconfig.get_auto_transcribe_global(protocol)
            override = tconfig.get_auto_transcribe_chat(protocol, contact_id)
            effective = tconfig.auto_transcribe_effective(protocol, contact_id)
            chat = "non impostato" if override is None else _onoff(override)
            self._post(
                reply,
                f"ℹ️ Trascrizione automatica ({protocol})\n"
                f"• Globale: {_onoff(glob)}\n"
                f"• Questa chat: {chat}\n"
                f"• Effettivo: {_onoff(effective)}\n\n" + _usage(protocol),
            )
            return
        if args[0] in ("on", "off") and len(args) == 1:
            enabled = args[0] == "on"
            tconfig.set_auto_transcribe_chat(protocol, contact_id, enabled)
            self._post(
                reply,
                f"✅ Trascrizione automatica {_onoff(enabled)} in questa chat.",
            )
            return
        if len(args) == 2 and args[0] == "global" and args[1] in ("on", "off"):
            enabled = args[1] == "on"
            tconfig.set_auto_transcribe_global(protocol, enabled)
            self._post(
                reply,
                f"✅ Trascrizione automatica {_onoff(enabled)} "
                f"per tutte le chat {protocol}.",
            )
            return
        self._post(reply, "⚠️ Argomenti non validi.\n\n" + _usage(protocol))

    def _find_quoted(
        self, protocol: str, contact_id: str, message: dict
    ) -> dict[str, Any] | None:
        """Stored row of the message *message* replies to (DB lookup)."""
        reply_id = message.get("reply_to_message_id")
        quote_ts = message.get("quote_timestamp")
        keys = self._storage_keys(protocol, contact_id)
        from protocols import db

        marks = ", ".join("?" for _ in keys)
        base = (
            "SELECT msg_id, timestamp, media_kind, attachment_id, is_mine "
            f"FROM messages WHERE protocol = ? AND contact_number IN ({marks}) "
        )
        queries: list[tuple[str, tuple[Any, ...]]] = []
        if isinstance(reply_id, str) and reply_id.strip():
            rid = reply_id.strip()
            queries.append(
                (
                    base + "AND (msg_id = ? OR msg_id LIKE '%_' || ?) "
                    "ORDER BY (media_kind = 'voice') DESC LIMIT 1",
                    (protocol, *keys, rid, rid),
                )
            )
        if isinstance(quote_ts, int) and not isinstance(quote_ts, bool) and quote_ts:
            queries.append(
                (
                    base + "AND timestamp = ? "
                    "ORDER BY (media_kind = 'voice') DESC LIMIT 1",
                    (protocol, *keys, quote_ts),
                )
            )
        for sql, params in queries:
            try:
                with db._DB_LOCK:
                    conn = sqlite3.connect(db.DB_FILE)
                    conn.row_factory = sqlite3.Row
                    try:
                        row = conn.execute(sql, params).fetchone()
                    finally:
                        conn.close()
            except (sqlite3.Error, OSError):
                logger.debug("Lookup del messaggio citato fallito", exc_info=True)
                continue
            if row is not None:
                return dict(row)
        return None

    def _storage_keys(self, protocol: str, contact_id: str) -> list[str]:
        """DB keys of the chat (WhatsApp has ``@c.us``/``@lid`` aliases)."""
        try:
            backend = self.api.manager.get(protocol)
            contact = backend.find_contact(contact_id) if backend else None
            if contact is not None:
                from models import contact_storage_keys

                keys = [k for k in contact_storage_keys(contact) if k]
                if contact_id not in keys:
                    keys.append(contact_id)
                return keys
        except Exception:
            logger.debug("Chiavi di storage non risolte", exc_info=True)
        return [contact_id]

    # -- posting -----------------------------------------------------------

    def _post(self, quote: _Quote, text: str) -> bool:
        """Send *text* as a quoted reply from the user's account.

        Replies never start with ``!`` (they echo back as own messages and
        must not be taken for commands).  Returns ``True`` when sent.
        """
        if text.lstrip().startswith("!"):
            text = "ℹ️ " + text
        request: dict[str, Any] = {
            "protocol": quote.protocol,
            "contact_id": quote.contact_id,
            "text": text,
        }
        if quote.protocol == "signal":
            request["quote_timestamp"] = quote.timestamp
            request["quote_author"] = quote.contact_id
        else:
            request["reply_to_message_id"] = quote.message_id
        if quote.text:
            request["quote_message"] = quote.text
        try:
            self.api.call_json("messages.send", request)
        except Exception:
            logger.exception(
                "Auto-trascrizione: risposta non inviata (%s %s)",
                quote.protocol,
                quote.contact_id,
            )
            return False
        return True
