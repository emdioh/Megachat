"""link_whatsapp.py drives megachatd's link.* ops (no WAHA)."""

from __future__ import annotations

import base64
from types import SimpleNamespace

import pytest

import link_whatsapp
from server.client import ApiError


class FakeClient:
    def __init__(self, *, available=True, linked=False, polls=()):
        self.available, self.linked = available, linked
        self.polls = list(polls)
        self.calls: list[tuple[str, dict | None]] = []
        self.closed = False

    def call(self, op, data=None, timeout=None):
        self.calls.append((op, data))
        if op == "link.status":
            return {
                "protocols": {
                    "whatsapp": {"available": self.available, "linked": self.linked}
                }
            }
        if op == "link.start":
            return {"qr": {"kind": "url", "data": "https://wa.me/qr"}}
        if op == "link.poll":
            return self.polls.pop(0)
        return {}

    def close(self):
        self.closed = True


@pytest.fixture
def run(monkeypatch):
    monkeypatch.setattr(link_whatsapp.time, "sleep", lambda s: None)
    shown = []
    monkeypatch.setattr(link_whatsapp, "print_qr_code", shown.append)

    def _run(client, *, spawned=False):
        monkeypatch.setattr(
            link_whatsapp,
            "attach_or_spawn",
            lambda **kw: SimpleNamespace(client=client, spawned=spawned),
        )
        link_whatsapp.main()
        return shown

    return _run


def ops(client):
    return [op for op, _ in client.calls]


def test_links_through_link_ops(run, capsys):
    client = FakeClient(polls=[{"state": "waiting", "qr": None}, {"state": "done"}])
    shown = run(client)
    assert ops(client) == ["link.status", "link.start", "link.poll", "link.poll"]
    assert client.calls[1][1] == {"protocol": "whatsapp"}
    assert shown == ["https://wa.me/qr"]
    assert "WhatsApp collegato" in capsys.readouterr().out
    assert client.closed


def test_refreshed_qr_is_shown_again(run):
    client = FakeClient(
        polls=[
            {"state": "waiting", "qr": {"kind": "url", "data": "https://wa.me/2"}},
            {"state": "done"},
        ]
    )
    assert run(client) == ["https://wa.me/qr", "https://wa.me/2"]


def test_png_qr_is_rendered_from_base64(monkeypatch, capsys):
    monkeypatch.setattr(link_whatsapp, "qr_png_to_ascii", lambda png: f"ASCII:{png!r}")
    kind = link_whatsapp._print_qr(
        {"kind": "png", "data": base64.b64encode(b"PNG").decode()}
    )
    assert kind == "png"
    assert "ASCII:b'PNG'" in capsys.readouterr().out


def test_already_linked_does_not_restart_the_session(run, capsys):
    client = FakeClient(linked=True)
    run(client)
    assert ops(client) == ["link.status"]
    assert "già collegata" in capsys.readouterr().out


def test_whatsapp_disabled_explains_how_to_enable(run, capsys):
    client = FakeClient(available=False)
    with pytest.raises(SystemExit) as exc:
        run(client)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "WHATSAPP_ENABLED=1" in err
    assert ops(client) == ["link.status"]


def test_server_error_is_reported(run, capsys):
    client = FakeClient()
    client.call = lambda op, data=None, timeout=None: (_ for _ in ()).throw(
        ApiError("backend_error", "boom")
    )
    with pytest.raises(SystemExit):
        run(client)
    assert "boom" in capsys.readouterr().err
