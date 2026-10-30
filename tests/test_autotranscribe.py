"""Auto-transcription of voice notes and the ``!`` chat commands."""

from __future__ import annotations

import json
import threading
import time
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest
from client_harness import api_for

import protocols.config as pconfig
from protocols import db
from server import autotranscribe as at
from server.api import ApiError
from server.autotranscribe import TRANSCRIPTION_PREFIX, AutoTranscriber
from server.bus import GAP, EventBus
from transcription import config as tconfig
from transcription import store
from transcription.service import TranscriptionService

CHAT = "39333@c.us"


@pytest.fixture(autouse=True)
def isolated_config(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    cfg = tmp_path / "cfg"
    cfg.mkdir()
    monkeypatch.setattr(pconfig, "PROJECT_DIR", cfg)
    return cfg


def _now_ms(delta_s: float = 0) -> int:
    return int((time.time() + delta_s) * 1000)


class FakeService:
    def __init__(self, status: dict | None = None) -> None:
        self.rows: dict[tuple[str, str], dict] = {}
        if status is not None:
            self.rows["*"] = status
        self.submitted: list[tuple[str, str, Path]] = []

    def status(self, protocol: str, attachment_id: str):
        return self.rows.get((protocol, attachment_id)) or self.rows.get("*")

    def submit(self, protocol: str, attachment_id: str, path) -> None:
        self.submitted.append((protocol, attachment_id, Path(path)))
        self.rows[(protocol, attachment_id)] = {"status": "pending"}


class FakeAPI:
    def __init__(self, service: Any = None) -> None:
        self.transcription = SimpleNamespace(service=service, on_done=None)
        self.manager = SimpleNamespace(get=lambda protocol: None)
        self.sent: list[dict] = []
        self.calls: list[str] = []
        self.fail_send = False

    def call_json(self, name: str, request: dict | None = None) -> Any:
        self.calls.append(name)
        if name == "media.attachment":
            return {"path": f"/media/{request['attachment_id']}"}
        if name == "messages.send":
            if self.fail_send:
                raise ApiError("backend_error", "x")
            self.sent.append(request)
            return {"ok": True}
        raise AssertionError(name)


def make(service: Any = None, **kw) -> tuple[AutoTranscriber, FakeAPI]:
    api = FakeAPI(service if service is not None else FakeService())
    tr = AutoTranscriber(
        api, EventBus(), inline=True, started_at=time.time() - 100, **kw
    )
    return tr, api


def voice(protocol="whatsapp", att="a1.ogg", mid="MID1", ts=None, **extra) -> dict:
    message = {
        "id": mid,
        "text": "",
        "is_mine": False,
        "sender": "Mario",
        "timestamp": ts if ts is not None else _now_ms(-5),
        "media_kind": "voice",
        "attachment_id": att,
        "content_type": "audio/ogg",
    }
    message.update(extra)
    return {
        "protocol": protocol,
        "contact_id": CHAT if protocol == "whatsapp" else "39333",
        "message": message,
        "timestamp": message["timestamp"],
        "added": True,
        "changed": False,
    }


def text_msg(text: str, protocol="whatsapp", **extra) -> dict:
    payload = voice(protocol)
    payload["message"].update(
        {"text": text, "media_kind": None, "attachment_id": None, "is_mine": True}
    )
    payload["message"].update(extra)
    return payload


def enable(protocol: str = "whatsapp") -> None:
    tconfig.set_auto_transcribe_global(protocol, True)


def finish(tr: AutoTranscriber, protocol: str, att: str, **row) -> None:
    tr.on_transcription_done(protocol, att, {"status": "ok", "text": "ciao", **row})


# -- settings ------------------------------------------------------------------


def test_effective_resolution_global_and_override():
    assert tconfig.get_auto_transcribe_globals() == {
        "whatsapp": False,
        "signal": False,
        "telegram": False,
    }
    assert not tconfig.auto_transcribe_effective("signal", "x")
    tconfig.set_auto_transcribe_global("signal", True)
    assert tconfig.auto_transcribe_effective("signal", "x")
    assert not tconfig.auto_transcribe_effective("telegram", "x")
    tconfig.set_auto_transcribe_chat("signal", "x", False)
    assert tconfig.get_auto_transcribe_chat("signal", "x") is False
    assert not tconfig.auto_transcribe_effective("signal", "x")
    assert tconfig.auto_transcribe_effective("signal", "y")
    tconfig.set_auto_transcribe_global("signal", False)
    tconfig.set_auto_transcribe_chat("signal", "y", True)
    assert tconfig.auto_transcribe_effective("signal", "y")
    tconfig.set_auto_transcribe_chat("signal", "y", None)
    assert tconfig.get_auto_transcribe_chat("signal", "y") is None
    assert not tconfig.auto_transcribe_effective("gmail", "y")


def test_config_persistence_keeps_other_keys(isolated_config: Path):
    path = isolated_config / "config.json"
    path.write_text(json.dumps({"openai_api_key": "sk-x"}))
    tconfig.set_auto_transcribe_global("whatsapp", True)
    tconfig.set_auto_transcribe_chat("telegram", "42", False)
    data = json.loads(path.read_text())
    assert data == {
        "openai_api_key": "sk-x",
        "auto_transcribe": {"whatsapp": True},
        "auto_transcribe_chats": {"telegram:42": False},
    }
    with pytest.raises(ValueError):
        tconfig.set_auto_transcribe_global("gmail", True)


def test_concurrent_writes_do_not_lose_updates(isolated_config: Path):
    threads = [
        threading.Thread(
            target=tconfig.set_auto_transcribe_chat, args=("signal", str(i), True)
        )
        for i in range(20)
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    saved = json.loads((isolated_config / "config.json").read_text())
    assert len(saved["auto_transcribe_chats"]) == 20


# -- voice notes ---------------------------------------------------------------


def test_setting_off_does_nothing():
    tr, api = make()
    tr.handle_message(voice())
    assert api.transcription.service.submitted == []
    assert api.sent == []


def test_old_messages_ignored():
    enable()
    tr, api = make()
    tr.handle_message(voice(ts=_now_ms(-(at.RECENT_WINDOW_S + 30))))
    tr.handle_message(voice(att="b", mid="M2", ts=_now_ms(-5)))  # recent
    tr2 = AutoTranscriber(api, EventBus(), inline=True, started_at=time.time() + 500)
    tr2.handle_message(voice(att="c", mid="M3"))  # before core start
    assert [s[1] for s in api.transcription.service.submitted] == ["b"]


def test_not_added_or_other_protocol_ignored():
    enable()
    tr, api = make()
    p = voice()
    p["added"] = False
    tr.handle_message(p)
    p = voice()
    p["protocol"] = "gmail"
    tr.handle_message(p)
    assert api.transcription.service.submitted == []


@pytest.mark.parametrize("is_mine", [False, True])
def test_voice_submitted_and_posted_whatsapp(is_mine):
    enable()
    tr, api = make()
    tr.handle_message(voice(is_mine=is_mine))
    assert api.transcription.service.submitted == [
        ("whatsapp", "a1.ogg", Path("/media/a1.ogg"))
    ]
    assert api.sent == []  # nothing until the worker is done
    finish(tr, "whatsapp", "a1.ogg")
    assert api.sent == [
        {
            "protocol": "whatsapp",
            "contact_id": CHAT,
            "text": TRANSCRIPTION_PREFIX + "ciao",
            "reply_to_message_id": "MID1",
        }
    ]
    assert "optimistic" not in api.sent[0]


def test_quote_fields_telegram():
    enable("telegram")
    tr, api = make()
    p = voice("telegram", att="tg1", mid="77")
    tr.handle_message(p)
    finish(tr, "telegram", "tg1")
    assert api.sent[0]["reply_to_message_id"] == "77"
    assert "quote_timestamp" not in api.sent[0]


def test_quote_fields_signal():
    enable("signal")
    tr, api = make()
    p = voice("signal", att="s1.ogg", mid=None, ts=_now_ms(-3))
    p["message"].pop("id")
    tr.handle_message(p)
    finish(tr, "signal", "s1.ogg")
    sent = api.sent[0]
    assert sent["quote_timestamp"] == p["message"]["timestamp"]
    assert sent["quote_author"] == "39333"
    assert "reply_to_message_id" not in sent


def test_chat_override_beats_global():
    enable()
    tconfig.set_auto_transcribe_chat("whatsapp", CHAT, False)
    tr, api = make()
    tr.handle_message(voice())
    assert api.transcription.service.submitted == []
    tconfig.set_auto_transcribe_global("whatsapp", False)
    tconfig.set_auto_transcribe_chat("whatsapp", CHAT, True)
    tr.handle_message(voice(att="b", mid="M2"))
    assert len(api.transcription.service.submitted) == 1


def test_already_ok_posts_immediately_without_submit():
    enable()
    service = FakeService({"status": "ok", "text": "gia fatto"})
    tr, api = make(service)
    tr.handle_message(voice())
    assert service.submitted == []
    assert api.sent[0]["text"] == TRANSCRIPTION_PREFIX + "gia fatto"


def test_no_double_post():
    enable()
    tr, api = make()
    tr.handle_message(voice())
    tr.handle_message(voice(mid="MID1b"))  # same attachment, other event
    finish(tr, "whatsapp", "a1.ogg")
    finish(tr, "whatsapp", "a1.ogg")  # duplicate completion
    tr.handle_message(voice(mid="MID1c"))  # arrives after the post
    assert len(api.transcription.service.submitted) == 1
    assert len(api.sent) == 1


def test_failure_posts_short_italian_error_and_allows_retry():
    enable()
    tr, api = make()
    tr.handle_message(voice())
    tr.on_transcription_done("whatsapp", "a1.ogg", {"status": "failed", "error": "x"})
    assert len(api.sent) == 1
    assert api.sent[0]["text"].startswith("⚠️ Trascrizione non riuscita")
    assert api.sent[0]["reply_to_message_id"] == "MID1"
    api.transcription.service.rows[("whatsapp", "a1.ogg")] = {"status": "failed"}
    tr.handle_message(voice(mid="MID1b"))  # a retry is possible
    assert len(api.transcription.service.submitted) == 2


def test_not_configured_is_noop_and_logs_once(caplog):
    enable()
    api = FakeAPI(service=None)
    api.transcription.service = None
    tr = AutoTranscriber(api, EventBus(), inline=True, started_at=time.time() - 100)
    with caplog.at_level("INFO"):
        tr.handle_message(voice())
        tr.handle_message(voice(att="b", mid="M2"))
    assert api.calls == []
    assert sum("non configurata" in r.message for r in caplog.records) == 1


def test_own_posts_not_reprocessed():
    enable()
    tr, api = make()
    tr.handle_message(voice())
    finish(tr, "whatsapp", "a1.ogg")
    echo = text_msg(TRANSCRIPTION_PREFIX + "ciao", reply_to_message_id="MID1")
    tr.handle_message(echo)
    assert len(api.sent) == 1
    # command replies never start with "!" either
    tr.handle_message(text_msg("!transcription"))
    for sent in api.sent[1:]:
        assert not sent["text"].lstrip().startswith("!")
        tr.handle_message(text_msg(sent["text"]))
    assert len(api.sent) == 2


def test_send_failure_allows_later_retry():
    enable()
    tr, api = make()
    api.fail_send = True
    tr.handle_message(voice())
    finish(tr, "whatsapp", "a1.ogg")
    assert api.sent == []
    api.fail_send = False
    tr.handle_message(voice(mid="MID1b"))
    finish(tr, "whatsapp", "a1.ogg")
    assert len(api.sent) == 1


def test_never_marks_read(monkeypatch: pytest.MonkeyPatch):
    from protocols.manager import BackendManager

    calls: list[Any] = []
    monkeypatch.setattr(db, "_mark_as_read", lambda *a, **k: calls.append(a))
    monkeypatch.setattr(
        BackendManager, "mark_read", lambda *a, **k: calls.append(a), raising=False
    )
    enable()
    tr, api = make()
    tr.handle_message(voice())
    finish(tr, "whatsapp", "a1.ogg")
    tr.handle_message(text_msg("!transcription"))
    assert api.sent
    assert "chats.mark_read" not in api.calls
    assert calls == []


# -- commands ------------------------------------------------------------------


def _store_voice(protocol="whatsapp", contact=CHAT, mid="VM1", att="v.ogg", ts=None):
    db._add_message_to_cache(
        contact,
        "",
        False,
        "Mario",
        ts or _now_ms(-30),
        msg_type="attachment",
        attachment_id=att,
        media_kind="voice",
        protocol=protocol,
        msg_id=mid,
    )


def test_t_replies_to_voice_whatsapp():
    _store_voice()
    tr, api = make()
    tr.handle_message(text_msg("!t", reply_to_message_id="VM1", id="CMD1"))
    assert api.transcription.service.submitted == [
        ("whatsapp", "v.ogg", Path("/media/v.ogg"))
    ]
    finish(tr, "whatsapp", "v.ogg")
    # quotes the voice note, not the command
    assert api.sent[0]["reply_to_message_id"] == "VM1"
    assert api.sent[0]["text"].startswith(TRANSCRIPTION_PREFIX)


def test_t_works_with_global_off_and_no_double_with_auto():
    _store_voice()
    tr, api = make()
    tr.handle_message(text_msg("!t", reply_to_message_id="VM1"))
    tr.handle_message(text_msg("!t", reply_to_message_id="VM1", id="CMD2"))
    finish(tr, "whatsapp", "v.ogg")
    tr.handle_message(text_msg("!t", reply_to_message_id="VM1", id="CMD3"))
    assert len(api.transcription.service.submitted) == 1
    assert len(api.sent) == 1


def test_t_signal_by_quote_timestamp():
    ts = _now_ms(-40)
    _store_voice("signal", "39333", mid=None, att="sig.ogg", ts=ts)
    tr, api = make()
    tr.handle_message(text_msg("!t", "signal", quote_timestamp=ts))
    finish(tr, "signal", "sig.ogg")
    assert api.sent[0]["quote_timestamp"] == ts
    assert api.sent[0]["text"].startswith(TRANSCRIPTION_PREFIX)


def test_t_telegram_by_reply_id():
    _store_voice("telegram", "39333", mid="55", att="tg.ogg")
    tr, api = make()
    tr.handle_message(text_msg("!t", "telegram", reply_to_message_id="55"))
    finish(tr, "telegram", "tg.ogg")
    assert api.sent[0]["reply_to_message_id"] == "55"


def test_t_without_quote_or_non_voice_gets_hint():
    db._add_message_to_cache(
        CHAT, "ciao", False, "M", _now_ms(-30), protocol="whatsapp", msg_id="T1"
    )
    tr, api = make()
    tr.handle_message(text_msg("!t"))
    tr.handle_message(text_msg("!t", reply_to_message_id="T1", id="C2"))
    tr.handle_message(text_msg("!t", reply_to_message_id="NOPE", id="C3"))
    assert api.transcription.service.submitted == []
    assert len(api.sent) == 3
    assert all("nota vocale" in s["text"] for s in api.sent)
    # replies quote the command message
    assert api.sent[1]["reply_to_message_id"] == "C2"


def test_t_unconfigured_replies():
    _store_voice()
    api = FakeAPI()
    api.transcription.service = None
    tr = AutoTranscriber(api, EventBus(), inline=True, started_at=time.time() - 100)
    tr.handle_message(text_msg("!t", reply_to_message_id="VM1"))
    assert "non configurata" in api.sent[0]["text"]


def test_transcription_status_and_set():
    tr, api = make()
    tr.handle_message(text_msg("!transcription", id="C1"))
    status = api.sent[-1]
    assert status["reply_to_message_id"] == "C1"
    for needle in ("Globale: disattiva", "non impostato", "Effettivo: disattiva"):
        assert needle in status["text"]
    tr.handle_message(text_msg("!transcription on", id="C2"))
    assert tconfig.get_auto_transcribe_chat("whatsapp", CHAT) is True
    assert "attiva" in api.sent[-1]["text"]
    tr.handle_message(text_msg("!transcription off", id="C3"))
    assert tconfig.get_auto_transcribe_chat("whatsapp", CHAT) is False
    tr.handle_message(text_msg("!transcription global on", id="C4"))
    assert tconfig.get_auto_transcribe_global("whatsapp") is True
    assert tconfig.get_auto_transcribe_global("signal") is False
    tr.handle_message(text_msg("!transcription global off", id="C5"))
    assert tconfig.get_auto_transcribe_global("whatsapp") is False
    tr.handle_message(text_msg("!transcription", id="C6"))
    assert "Questa chat: disattiva" in api.sent[-1]["text"]


def test_transcription_invalid_args_show_usage():
    tr, api = make()
    for i, cmd in enumerate(
        ["!transcription maybe", "!transcription global", "!transcription on off"]
    ):
        tr.handle_message(text_msg(cmd, id=f"C{i}"))
    assert len(api.sent) == 3
    assert all("Argomenti non validi" in s["text"] for s in api.sent)
    assert tconfig.get_auto_transcribe_chat("whatsapp", CHAT) is None


def test_commands_from_others_or_old_ignored():
    tr, api = make()
    tr.handle_message(text_msg("!transcription on", is_mine=False))
    tr.handle_message(
        text_msg("!transcription on", ts=_now_ms(-(at.RECENT_WINDOW_S + 60)))
    )
    tr.handle_message(text_msg("!unknown"))
    tr.handle_message(text_msg("ciao !t"))
    assert api.sent == []
    assert tconfig.get_auto_transcribe_chat("whatsapp", CHAT) is None


# -- service callback / bus / ops ----------------------------------------------


class _Client:
    model = "m"

    def __init__(self, error: Exception | None = None) -> None:
        self.error = error

    def transcribe(self, path):
        if self.error:
            raise self.error
        return "testo"


def _wait(cond, timeout=5.0):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if cond():
            return True
        time.sleep(0.01)
    return False


@pytest.mark.parametrize("error", [None, RuntimeError("boom")])
def test_service_on_done_callback(error):
    calls: list[tuple] = []
    service = TranscriptionService(
        _Client(error), store, on_done=lambda *a: calls.append(a)
    )
    try:
        service.submit("signal", "x", Path("/tmp/x"))
        assert _wait(lambda: calls)
    finally:
        service.stop()
    protocol, att, row = calls[0]
    assert (protocol, att) == ("signal", "x")
    assert row["status"] == ("failed" if error else "ok")


def test_service_on_done_errors_do_not_kill_worker():
    calls: list[str] = []

    def bad(protocol, att, row):
        calls.append(att)
        raise RuntimeError("cb")

    service = TranscriptionService(_Client(), store, on_done=bad)
    try:
        service.submit("signal", "a", Path("/tmp/a"))
        service.submit("signal", "b", Path("/tmp/b"))
        assert _wait(lambda: len(calls) == 2)
    finally:
        service.stop()


def test_service_without_callback_unchanged():
    service = TranscriptionService(_Client(), store)
    try:
        service.submit("signal", "z", Path("/tmp/z"))
        assert _wait(
            lambda: (service.status("signal", "z") or {}).get("status") == "ok"
        )
    finally:
        service.stop()


def test_bus_thread_end_to_end_and_gap():
    enable()
    bus = EventBus()
    api = FakeAPI(FakeService())
    tr = AutoTranscriber(api, bus)
    tr.start()
    try:
        assert api.transcription.on_done == tr.on_transcription_done
        bus.publish("gap_test", {})
        bus.publish("message", {**voice(), "contact": {}})
        # a GAP does not stop the consumer
        tr._sub._push(
            SimpleNamespace(type=GAP, payload={"reason": "overflow"}, seq=1, ts=0)
        )
        assert _wait(lambda: api.transcription.service.submitted)
        tr.on_transcription_done("whatsapp", "a1.ogg", {"status": "ok", "text": "t"})
        assert _wait(lambda: api.sent)
    finally:
        tr.stop()
    assert api.transcription.on_done is None


def test_ops_roundtrip_share_state_with_commands():
    api = api_for(SimpleNamespace(get=lambda p: None))
    cfg = api.call_json("autotranscribe.config", {})
    assert cfg == {
        "configured": False,
        "globals": {"whatsapp": False, "signal": False, "telegram": False},
    }
    out = api.call_json(
        "autotranscribe.set_global", {"protocol": "signal", "enabled": True}
    )
    assert out["signal"] is True
    assert tconfig.get_auto_transcribe_global("signal") is True
    chat = api.call_json(
        "autotranscribe.chat", {"protocol": "signal", "contact_id": "1"}
    )
    assert chat == {"global": True, "override": None, "effective": True}
    chat = api.call_json(
        "autotranscribe.set_chat",
        {"protocol": "signal", "contact_id": "1", "enabled": False},
    )
    assert chat == {"global": True, "override": False, "effective": False}
    # a command sees the same state
    tr, fake = make()
    tr.handle_message(text_msg("!transcription", "signal"))
    assert "Questa chat: non impostato" in fake.sent[-1]["text"]
    chat = api.call_json(
        "autotranscribe.set_chat",
        {"protocol": "signal", "contact_id": "1", "enabled": None},
    )
    assert chat["override"] is None


@pytest.mark.parametrize(
    ("name", "req"),
    [
        ("autotranscribe.set_global", {"protocol": "gmail", "enabled": True}),
        ("autotranscribe.set_global", {"protocol": "signal", "enabled": "yes"}),
        ("autotranscribe.chat", {"protocol": "gmail", "contact_id": "a"}),
        ("autotranscribe.chat", {"protocol": "signal", "contact_id": ""}),
        (
            "autotranscribe.set_chat",
            {"protocol": "gmail", "contact_id": "a", "enabled": 1},
        ),
        ("autotranscribe.set_chat", {"protocol": "signal", "contact_id": "a"}),
    ],
)
def test_ops_reject_invalid(name, req):
    api = api_for(SimpleNamespace(get=lambda p: None))
    with pytest.raises(ApiError) as exc:
        api.call_json(name, req)
    assert exc.value.code == "invalid_request"
