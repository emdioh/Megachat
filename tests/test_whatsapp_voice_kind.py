"""WhatsApp voice notes are ``voice`` in every WAHA payload shape."""

from __future__ import annotations

import pytest

from protocols.whatsapp_events import _event_from_message, _wa_is_voice_note

URL = "http://localhost:3000/api/files/default/false_123@g.us_AC22_456@lid.oga"


def _flat(**extra) -> dict:
    raw = {
        "id": "false_123@g.us_AC22_456@lid",
        "from": "123@g.us",
        "participant": "456@lid",
        "fromMe": False,
        "timestamp": 1_700_000_000,
        "body": "",
        "hasMedia": True,
        "media": {"url": URL, "mimetype": "audio/ogg; codecs=opus"},
    }
    raw.update(extra)
    return raw


@pytest.mark.parametrize(
    "extra",
    [
        {"type": "ptt"},  # WEBJS
        {"_data": {"type": "ptt"}},  # WEBJS raw
        {"_data": {"message": {"audioMessage": {"ptt": True}}}},  # NOWEB
        {"_data": {"Message": {"audioMessage": {"PTT": True}}}},  # GOWS
    ],
)
def test_flat_payload_voice_note(extra):
    events = _event_from_message(_flat(**extra))
    assert len(events) == 1
    assert events[0].payload["media_kind"] == "voice"
    assert events[0].payload["attachment_id"] == URL


def test_flat_payload_plain_audio_stays_audio():
    raw = _flat(type="audio")
    raw["media"]["mimetype"] = "audio/mpeg"
    assert _wa_is_voice_note(raw) is False
    events = _event_from_message(raw)
    assert events[0].payload["media_kind"] == "audio"
