"""``messages.send`` / ``POST /api/send`` carry the real backend message ids.

The Go server is started with the contract-only fake backend hook
(``MEGACHAT_FAKE_BACKEND``, compiled in only for ``-tags contracttest``: see
megachatd/cmd/megachatd/fakebackend_contracttest.go): the hook registers
megachatd/internal/proto/fake and seeds the contacts below.  The assertions
are the documented contract (megachatd/CONTRACTS.md 4.3.2 and 7.2, mirroring
server/ops/send.py, tests/test_send_message_ids.py):

* a successful text send answers ``{"ok":true,"message_ids":[<backend id>]}``
  (``[]`` when the backend returns no id);
* an attachment send answers ``{"ok":true,"message_ids":[<ids>]}``;
* the idempotent duplicate replay answers ``{"ok":true,"duplicate":true}``
  with no ``message_ids`` member.
"""

from __future__ import annotations

import uuid

import pytest
from harness import Server, start_server

#: Seeded by the fake-backend hook.
CONTACT = "fake-contact"

#: Seeded by the fake-backend hook; its sends carry no backend id.
EMPTY_ID_CONTACT = "fake-empty-id"

#: Smallest valid PNG (the same bytes tests/test_send_message_ids.py uses).
_PNG_1X1 = bytes.fromhex(
    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
    "0000000d49444154789c6360000002000154a24f5d0000000049454e44ae426082"
)


@pytest.fixture(scope="module")
def send_server(go_binary) -> Server:
    srv = start_server(
        "go", binary=go_binary, extra_env={"MEGACHAT_FAKE_BACKEND": "signal"}
    )
    yield srv
    srv.stop()


def _multipart(fields: dict[str, str], files: list[tuple[str, str, str, bytes]]) -> tuple[bytes, str]:
    boundary = "----mc" + uuid.uuid4().hex
    out = bytearray()
    for name, value in fields.items():
        out += (
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n'
            f"{value}\r\n"
        ).encode()
    for name, filename, content_type, data in files:
        out += (
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; '
            f'filename="{filename}"\r\nContent-Type: {content_type}\r\n\r\n'
        ).encode()
        out += data + b"\r\n"
    out += f"--{boundary}--\r\n".encode()
    return bytes(out), boundary


def _payload(fields: dict[str, str]) -> tuple[bytes, dict[str, str]]:
    body, boundary = _multipart(fields, [("file", "photo.png", "image/png", _PNG_1X1)])
    return body, {"Content-Type": f"multipart/form-data; boundary={boundary}"}


def _text_body(client_msg_id: str | None = None, text: str = "ciao") -> dict[str, str]:
    body = {"protocol": "signal", "contact_id": CONTACT, "text": text}
    if client_msg_id is not None:
        body["client_msg_id"] = client_msg_id
    return body


def _ids(out: dict) -> list[str]:
    assert out["ok"] is True
    ids = out["message_ids"]
    assert isinstance(ids, list)
    for mid in ids:
        assert isinstance(mid, str) and mid
    return ids


def test_text_send_returns_distinct_backend_ids(send_server: Server):
    first = send_server.http("POST", "/api/send", json_body=_text_body("contract-ids-text-1"))
    second = send_server.http("POST", "/api/send", json_body=_text_body("contract-ids-text-2"))
    assert first.status == 200, first.body
    assert second.status == 200, second.body
    a, b = first.json(), second.json()
    assert set(a) == {"ok", "message_ids"}
    assert set(b) == {"ok", "message_ids"}
    (id_a,) = _ids(a)  # exactly one id for a one-message text send
    (id_b,) = _ids(b)
    assert id_a != id_b  # ids are backend-generated, not a fixed stub


def test_text_send_duplicate_replay_omits_message_ids(send_server: Server):
    body = _text_body("contract-ids-duplicate")
    first = send_server.http("POST", "/api/send", json_body=body)
    assert first.status == 200, first.body
    (first_id,) = _ids(first.json())

    replay = send_server.http("POST", "/api/send", json_body=body)
    assert replay.status == 200, replay.body
    assert replay.json() == {"ok": True, "duplicate": True}
    assert "message_ids" not in replay.json()
    assert first_id  # the original send still carried the real id


def test_attachment_send_returns_backend_ids(send_server: Server):
    body, headers = _payload(
        {"protocol": "signal", "contact_id": CONTACT, "text": "caption"}
    )
    r = send_server.http("POST", "/api/send", body=body, headers=headers)
    assert r.status == 200, r.body
    out = r.json()
    assert set(out) == {"ok", "message_ids"}
    (mid,) = _ids(out)  # one file -> one message
    assert mid


def test_multi_attachment_send_returns_one_id_per_file(send_server: Server):
    png = _PNG_1X1
    body, boundary = _multipart(
        {"protocol": "signal", "contact_id": CONTACT, "text": ""},
        [
            ("file", "one.png", "image/png", png),
            ("file", "two.png", "image/png", png),
        ],
    )
    r = send_server.http(
        "POST",
        "/api/send",
        body=body,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    assert r.status == 200, r.body
    ids = _ids(r.json())
    assert len(ids) == 2
    assert len(set(ids)) == 2


def test_socket_op_send_returns_message_ids(send_server: Server):
    out = send_server.call(
        "messages.send",
        {
            "protocol": "signal",
            "contact_id": CONTACT,
            "text": "socket",
            "client_msg_id": "contract-ids-socket",
        },
    )
    assert set(out) == {"ok", "message_ids"}
    (mid,) = _ids(out)
    assert mid

    replay = send_server.call(
        "messages.send",
        {
            "protocol": "signal",
            "contact_id": CONTACT,
            "text": "socket",
            "client_msg_id": "contract-ids-socket",
        },
    )
    assert replay == {"ok": True, "duplicate": True}


def test_send_without_backend_id_answers_empty_message_ids(send_server: Server):
    """A send the backend acknowledges without an id still answers ok with []."""
    out = send_server.call(
        "messages.send",
        {"protocol": "signal", "contact_id": EMPTY_ID_CONTACT, "text": "no id"},
    )
    assert out == {"ok": True, "message_ids": []}

    r = send_server.http(
        "POST",
        "/api/send",
        json_body={"protocol": "signal", "contact_id": EMPTY_ID_CONTACT, "text": "no id"},
    )
    assert r.status == 200, r.body
    assert r.json() == {"ok": True, "message_ids": []}
