"""Parity: the Go binary and the Python server (``python -m server``) are
started on copies of the same seeded SQLite DB, the same calls are made on
both, and the JSON is diffed.  Skipped when the Python server's dependencies
(fastapi, uvicorn, ...) are missing."""

from __future__ import annotations

import json
from typing import Any

import pytest
from harness import Server, WSConn
from routes_table import SAMPLES, SIG
from test_websocket import HANDSHAKES

WA = "393331110001@c.us"
WA_GROUP = "120363000000000001@g.us"
TG = "424242"
CHATS = [("signal", SIG), ("whatsapp", WA), ("whatsapp", WA_GROUP), ("telegram", TG)]


def outcome(srv: Server, op: str, params: dict | None) -> Any:
    try:
        return srv.call(op, params)
    except Exception as exc:  # ApiError & friends
        return {
            "__error__": type(exc).__name__,
            "code": getattr(exc, "code", None),
            "message": getattr(exc, "message", str(exc)),
            "details": getattr(exc, "details", None),
        }


def norm(value: Any, *servers: Server) -> Any:
    """JSON round-trip with each server's private HOME replaced by <HOME>."""
    text = json.dumps(value, sort_keys=True, ensure_ascii=False)
    for srv in servers:
        text = text.replace(str(srv.home), "<HOME>")
    return json.loads(text)


def assert_same(go: Server, py: Server, op: str, params: dict | None, **kw: Any) -> None:
    a = norm(outcome(go, op, params), go, py)
    b = norm(outcome(py, op, params), go, py)
    assert a == b, f"{op} {params}\n go: {json.dumps(a)[:1500]}\n py: {json.dumps(b)[:1500]}"


# -- messages.page / window / snapshot -------------------------------------------


@pytest.mark.parametrize("proto,cid", CHATS)
def test_messages_page(go_seeded, py_seeded, proto, cid):
    assert_same(go_seeded, py_seeded, "messages.page", {"protocol": proto, "contact_id": cid})
    rows = go_seeded.call("messages.page", {"protocol": proto, "contact_id": cid})
    assert len(rows) >= 3  # not vacuous


@pytest.mark.parametrize("proto,cid", CHATS)
@pytest.mark.parametrize("extra", [{"limit": 1}, {"limit": 2}, {"limit": 2000}, {"refresh": True}, {"since": 0}, {"limit": 0}, {"limit": 2001}])
def test_messages_page_variants(go_seeded, py_seeded, proto, cid, extra):
    assert_same(go_seeded, py_seeded, "messages.page", {"protocol": proto, "contact_id": cid, **extra})


def test_messages_page_since_cuts(go_seeded, py_seeded):
    rows = go_seeded.call("messages.page", {"protocol": "signal", "contact_id": SIG})
    for r in rows:
        assert_same(go_seeded, py_seeded, "messages.page", {"protocol": "signal", "contact_id": SIG, "since": r["timestamp"]})


@pytest.mark.parametrize("proto,cid", [c for c in CHATS if c[0] == "signal"])
def test_messages_window(go_seeded, py_seeded, proto, cid):
    # Only the Signal backend (always registered, cache loaded from the DB)
    # exists on both servers without credentials.
    assert_same(go_seeded, py_seeded, "messages.window", {"protocol": proto, "contact_id": cid})
    assert len(go_seeded.call("messages.window", {"protocol": proto, "contact_id": cid})["messages"]) == 7


@pytest.mark.parametrize("proto,cid", [c for c in CHATS if c[0] != "signal"])
def test_messages_window_without_backend(go_seeded, py_seeded, proto, cid):
    assert_same(go_seeded, py_seeded, "messages.window", {"protocol": proto, "contact_id": cid})


def test_messages_snapshot_signal(go_seeded, py_seeded):
    go = go_seeded.call("messages.snapshot", {"protocol": "signal"})
    py = py_seeded.call("messages.snapshot", {"protocol": "signal"})
    # The Python Signal backend seeds its cache with EVERY protocol's rows
    # (protocols.db._load_cache() without a protocol, legacy behaviour); the
    # Go port keeps only the protocol's own, which is what the TUI mirrors.
    py_only_signal = {
        cid: [r for r in rows if r.get("protocol", "signal") == "signal"] for cid, rows in py.items()
    }
    py_only_signal = {cid: rows for cid, rows in py_only_signal.items() if rows}
    assert norm(go, go_seeded, py_seeded) == norm(py_only_signal, go_seeded, py_seeded)
    assert list(go) == [SIG]


@pytest.mark.parametrize("proto", ["whatsapp", "telegram"])
def test_messages_snapshot_without_backend(go_seeded, py_seeded, proto):
    assert_same(go_seeded, py_seeded, "messages.snapshot", {"protocol": proto})


# -- read-only operations ------------------------------------------------------------

READ_ONLY = [
    ("server.status", None),
    ("server.config", None),
    ("emoji.list", None),
    ("reactions.available", {"protocol": "signal"}),
    ("reactions.available", {"protocol": "whatsapp"}),
    ("reactions.available", {"protocol": "telegram"}),
    ("transcribe.config", None),
    ("transcribe.settings", None),
    ("transcribe.diagnostics", None),
    ("transcribe.check", None),
    ("transcribe.status", {"protocol": "signal", "attachment_id": "sigvoice1"}),
    ("transcribe.status", {"protocol": "telegram", "attachment_id": "tg-doc-1"}),
    ("transcribe.status", {"protocol": "signal", "attachment_id": "nope"}),
    ("autotranscribe.config", None),
    ("autotranscribe.chat", {"protocol": "signal", "contact_id": "x"}),
    ("link.required", None),
    ("gmail.status", None),
    ("gmail.unread", None),
    ("gmail.address_book", None),
    ("contacts.list", None),
    ("contacts.list", {"q": "mario"}),
    ("contacts.book", {"q": "a"}),
    ("contacts.book", {"q": ""}),
    ("contacts.for_protocol", {"protocol": "signal"}),
    ("contacts.address_book", None),
    ("contacts.check_number", {"phone": "+390000"}),
    ("contacts.avatar", {"protocol": "signal", "contact_id": "nobody"}),
    ("media.attachment", {"protocol": "signal", "attachment_id": "sigimg1"}),
    ("media.attachment", {"protocol": "signal", "attachment_id": "sigvoice1"}),
    ("media.attachment", {"protocol": "signal", "attachment_id": "missing"}),
    ("media.thumbnail", {"protocol": "signal", "attachment_id": "sigimg1", "width": 96}),
    ("media.quote", {"protocol": "signal", "message_row_id": 4}),
    ("media.quote", {"protocol": "signal", "message_row_id": 4, "width": 96}),
    ("media.quoted_attachment", {"protocol": "signal", "contact_key": SIG, "quote_timestamp": 1}),
    ("links.preview", {"url": "x"}),
    ("gmail.image", {"token": "nope"}),
    ("server.client_log", {"message": "parity"}),
    ("chats.mark_read", {"protocol": "signal", "contact_id": SIG}),
    ("messages.send", {"protocol": "signal", "contact_id": SIG, "text": "  "}),
    ("messages.send", {"protocol": "signal", "contact_id": "ghost", "text": "hi"}),
    ("messages.send.check", {"protocol": "signal", "contact_id": SIG, "text": "hi"}),
    ("messages.edit", {"protocol": "signal", "contact_id": SIG, "message_id": "x", "new_text": "y"}),
    ("reactions.set", {"protocol": "signal", "contact_id": SIG, "message_id": "x", "emoji": "\U0001F44D"}),
    ("reactions.set", {"protocol": "signal", "contact_id": SIG, "message_id": "x", "emoji": "nope"}),
    ("chats.archive", {"protocol": "whatsapp", "contact_id": WA, "archived": True}),
    ("link.cancel", {"protocol": "signal"}),
    ("link.poll", {"protocol": "signal"}),
]


@pytest.mark.parametrize("op,params", READ_ONLY, ids=[f"{o}-{i}" for i, (o, _) in enumerate(READ_ONLY)])
def test_operation_parity(go_seeded, py_seeded, op, params):
    assert_same(go_seeded, py_seeded, op, params)


def test_link_status_shape_matches(go_seeded, py_seeded):
    # SIGNAL_ENABLED differs between the two fixtures (Python needs it to load
    # its Signal cache), so the signal account/linked members legitimately differ.
    a = go_seeded.call("link.status")["protocols"]
    b = py_seeded.call("link.status")["protocols"]
    assert set(a) == set(b)
    for proto in a:
        assert set(a[proto]) == set(b[proto])
    assert a["whatsapp"] == b["whatsapp"] and a["telegram"] == b["telegram"]


def test_hello_operations(go_seeded, py_seeded):
    a, b = go_seeded.client.server_info, py_seeded.client.server_info
    assert (a["api_version"], a["operations"]) == (b["api_version"], b["operations"])


# -- error paths: every operation, empty and junk params -------------------------------

NOT_COMPARABLE = {"server.shutdown", "server.logs", "server.ping", "server.clients", "link.status"}


def _all_ops() -> list[str]:
    import server.api as api

    api._load_operations()
    return sorted(set(api._REGISTRY) - NOT_COMPARABLE)


@pytest.mark.parametrize("op", _all_ops())
@pytest.mark.parametrize("params", [{}, {"protocol": "zzz", "contact_id": 5, "q": 1, "uid": "x", "url": 3}], ids=["empty", "junk"])
def test_invalid_params_parity(go_seeded, py_seeded, op, params):
    assert_same(go_seeded, py_seeded, op, params)


def test_unknown_operation_error_code(go_seeded, py_seeded):
    a = outcome(go_seeded, "no.such.op", None)
    b = outcome(py_seeded, "no.such.op", None)
    assert a["code"] == b["code"] and a["message"] == b["message"]


# -- HTTP ------------------------------------------------------------------------------

#: Skipped from the body diff: volatile (log lines carry ports and times).
HTTP_VOLATILE = {"/api/logs?limit=5", "/api/link/status"}  # link/status: SIGNAL_ENABLED differs, see test_link_status_shape_matches


def _hdrs(resp, keys=("content-type", "cache-control", "www-authenticate", "accept-ranges", "content-range")):
    return {k: v for k, v in resp.headers.items() if k in keys}


def _body(resp):
    if "application/json" in resp.headers.get("content-type", ""):
        return resp.json()
    return resp.body


@pytest.mark.parametrize("method,path,body", SAMPLES, ids=[f"{m} {p.split('?')[0]}" for m, p, _ in SAMPLES])
def test_route_parity(go_seeded, py_seeded, method, path, body):
    a = go_seeded.http(method, path, json_body=body)
    b = py_seeded.http(method, path, json_body=body)
    assert a.status == b.status, (a.body[:300], b.body[:300])
    assert _hdrs(a) == _hdrs(b)
    if path not in HTTP_VOLATILE:
        assert norm(_body(a), go_seeded, py_seeded) == norm(_body(b), go_seeded, py_seeded)


@pytest.mark.parametrize("method,path,body", SAMPLES, ids=[f"{m} {p.split('?')[0]}" for m, p, _ in SAMPLES])
def test_route_unauthorized_parity(go_seeded, py_seeded, method, path, body):
    a = go_seeded.http(method, path, token=None, json_body=body)
    b = py_seeded.http(method, path, token=None, json_body=body)
    assert a.status == b.status
    if a.status == 401:
        assert a.json() == b.json() and _hdrs(a) == _hdrs(b)


HTTP_EXTRA = [
    ("GET", f"/api/messages?proto=signal&contact_id=%2B15550100001"),
    ("GET", f"/api/messages?proto=signal&contact_id=%2B15550100001&limit=2"),
    ("GET", f"/api/messages?proto=signal&contact_id=%2B15550100001&limit=0"),
    ("GET", f"/api/messages?proto=signal&contact_id=%2B15550100001&limit=abc"),
    ("GET", "/api/messages?proto=whatsapp&contact_id=120363000000000001%40g.us"),
    ("GET", "/api/messages?proto=telegram&contact_id=424242&refresh=true"),
    ("GET", "/api/messages?proto=bogus&contact_id=x"),
    ("GET", "/api/messages?contact_id=x"),
    ("GET", "/api/reactions"),
    ("GET", "/api/contacts/book"),
    ("GET", "/api/media/bogus/abc"),
    ("GET", "/api/quote-media/signal/abc"),
    ("GET", "/api/gmail/messages/abc"),
    ("GET", "/api/does-not-exist"),
    ("GET", "/api/transcribe/signal/sigvoice1"),
    ("GET", "/api/transcribe/bogus/x"),
    ("GET", "/health"),
    ("POST", "/api/messages/read"),
    ("PUT", "/api/autotranscribe/global/bogus"),
    ("PUT", "/api/autotranscribe/chat?proto=signal&contact_id=x"),
    ("POST", "/api/status"),
]


@pytest.mark.parametrize("method,path", HTTP_EXTRA)
def test_route_edge_parity(go_seeded, py_seeded, method, path):
    body = {} if method in ("PUT", "POST") and "read" not in path else None
    a = go_seeded.http(method, path, json_body=body)
    b = py_seeded.http(method, path, json_body=body)
    assert a.status == b.status, (a.body[:300], b.body[:300])
    if path == "/health":
        ja, jb = a.json(), b.json()
        assert ja["status"] == jb["status"] and ja["auth_required"] == jb["auth_required"]
        return
    if a.status == 422 and isinstance(a.json()["detail"], list):  # FastAPI's list: status + locations matter
        assert [d["loc"] for d in a.json()["detail"]] == [d["loc"] for d in b.json()["detail"]]
        return
    assert norm(_body(a), go_seeded, py_seeded) == norm(_body(b), go_seeded, py_seeded)


BLOBS = [
    ("/api/media/signal/sigimg1", {}),
    ("/api/media/signal/sigimg1?w=96", {}),
    ("/api/media/signal/sigvoice1", {}),
    ("/api/media/signal/sigimg1", {"Range": "bytes=0-3"}),
    ("/api/media/signal/sigimg1", {"Range": "bytes=60-"}),
    ("/api/media/signal/sigimg1", {"Range": "bytes=500-600"}),
    ("/api/media/signal/missing", {}),
    ("/api/media/telegram/tg-doc-1", {}),
    ("/api/quote-media/signal/4", {}),
    ("/api/contact-avatar?proto=signal&contact_id=x", {}),
]


@pytest.mark.parametrize("path,headers", BLOBS)
def test_blob_route_parity(go_seeded, py_seeded, path, headers):
    a = go_seeded.get(path, headers=headers)
    b = py_seeded.get(path, headers=headers)
    assert a.status == b.status, (a.body[:200], b.body[:200])
    assert _hdrs(a) == _hdrs(b)
    assert a.body == b.body
    if a.status == 200:
        assert "etag" in a.headers and "last-modified" in a.headers


# -- WebSocket handshake -------------------------------------------------------------------


@pytest.mark.parametrize("name,path,sub,hdr,status,selected", HANDSHAKES, ids=[h[0] for h in HANDSHAKES])
def test_handshake_parity(go_seeded, py_seeded, name, path, sub, hdr, status, selected):
    for srv in (go_seeded, py_seeded):
        ws = WSConn.open(srv.port, path, subprotocols=sub, headers=hdr)
        try:
            assert (ws.status, ws.headers.get("sec-websocket-protocol")) == (status, selected), srv.kind
        finally:
            ws.close()


def test_v2_replay_parity(go_seeded, py_seeded):
    """Both servers replay the boot events from seq 1 with the same envelope."""
    frames = []
    for srv in (go_seeded, py_seeded):
        ws = WSConn.open(srv.port, "/ws/v2?since=0", subprotocols=["signal-tui-bearer", f"signal-tui-token.{__import__('harness').b64url(srv.token)}"])
        try:
            f = ws.recv_json(5)
        finally:
            ws.close()
        frames.append((f["seq"], f["type"], f["payload"]))
    # The Python fixture runs with SIGNAL_ENABLED=1 (see conftest), so its
    # first event is backend_connecting where the Go one is backend_done: only
    # the envelope is comparable.
    assert frames[0][0] == frames[1][0] == 1
    assert all(isinstance(f[2], dict) and f[2]["protocol"] == "signal" for f in frames)
