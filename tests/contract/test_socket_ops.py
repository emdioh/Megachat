"""Every operation of ``api.Ops`` that works without a real backend, driven
through the Unix socket with the TUI's own ``SocketClient`` against the Go
binary (empty and seeded databases), plus the JSON-RPC error paths."""

from __future__ import annotations

import json
import socket
import time

import pytest
from harness import Server, start_server

from server.api import ApiError
from server.socket_client import SocketClient

SIG = "+15550100001"
WA = "393331110001@c.us"
WA_GROUP = "120363000000000001@g.us"
TG = "424242"
CHATS = [("signal", SIG), ("whatsapp", WA), ("whatsapp", WA_GROUP), ("telegram", TG)]


def _python_ops() -> list[str]:
    import server.api as api

    api._load_operations()
    return sorted(api._REGISTRY)


def err_of(srv: Server, op: str, params=None) -> ApiError:
    with pytest.raises(ApiError) as exc:
        srv.call(op, params)
    return exc.value


# -- handshake / coverage ------------------------------------------------------


def test_hello_lists_every_python_operation(go_server: Server):
    info = go_server.client.server_info
    assert info["api_version"] == 1
    assert info["operations"] == sorted(info["operations"])
    assert info["operations"] == _python_ops()


def test_ping(go_server: Server):
    out = go_server.call("server.ping")
    assert out["pong"] is True and isinstance(out["ts"], float)


# -- server / meta -------------------------------------------------------------


def test_status_no_backend(go_server: Server):
    assert go_server.call("server.status") == {
        "signal": False,
        "telegram": False,
        "whatsapp": False,
    }


def test_config(go_server: Server):
    cfg = go_server.call("server.config")
    assert set(cfg) == {
        "thumbnail_max_lines",
        "thumbnail_max_cols",
        "picker_max_results",
        "picker_preferred_backend",
        "wa_lazy_media",
    }
    assert isinstance(cfg["thumbnail_max_lines"], int)
    assert isinstance(cfg["wa_lazy_media"], bool)


def test_clients_counts_the_caller(go_server: Server):
    out = go_server.call("server.clients")
    assert out["self_included"] is True and out["connections"] >= 1
    assert "web" in out


def test_logs_and_clear(go_fresh: Server):
    srv = go_fresh
    srv.call("server.client_log", {"message": "contract-marker"})
    out = srv.call("server.logs", {"limit": 500})
    assert set(out) >= {"records", "last_seq"}
    assert any("contract-marker" in r["message"] for r in out["records"])
    rec = out["records"][0]
    assert set(rec) >= {"seq", "time", "level", "logger", "message"}
    after = srv.call("server.logs", {"after": out["last_seq"]})
    assert after["records"] == []
    assert srv.call("server.logs_clear") == {"status": "ok"}
    cleared = srv.call("server.logs", {"level": "ERROR"})
    assert cleared["records"] == []


def test_emoji_list(go_server: Server):
    cats = go_server.call("emoji.list")
    assert len(cats) > 5
    for cat in cats:
        assert set(cat) >= {"category", "icon", "emojis"}
        assert cat["emojis"] and all(isinstance(e, str) for e in cat["emojis"])


def test_reactions_available_without_backend(go_server: Server):
    for proto in ("signal", "whatsapp", "telegram"):
        assert go_server.call("reactions.available", {"protocol": proto}) == {"emojis": []}


# -- transcription -------------------------------------------------------------


def test_transcribe_config_and_settings(go_server: Server):
    assert go_server.call("transcribe.config") == {"enabled": False}
    st = go_server.call("transcribe.settings")
    assert st["configured"] is False and st["provider"] == "openai"
    assert {"openai", "gemini", "openrouter"} <= set(st["providers"])
    assert st["providers"]["openai"]["api_key_set"] is False


def test_transcribe_set_settings_roundtrip(go_fresh: Server):
    out = go_fresh.call("transcribe.set_settings", {"language": "it"})
    assert out["configured"] is False
    again = go_fresh.call("transcribe.settings")
    assert again == out


def test_transcribe_check_and_diagnostics(go_server: Server):
    chk = go_server.call("transcribe.check")
    assert chk["ok"] is False and chk["provider"] == "openai"
    diag = go_server.call("transcribe.diagnostics")
    assert isinstance(diag, dict)


def test_transcribe_status_unconfigured(go_server: Server):
    e = err_of(go_server, "transcribe.status", {"protocol": "signal", "attachment_id": "a"})
    assert e.code == "backend_down"


def test_autotranscribe_global_and_chat(go_fresh: Server):
    srv = go_fresh
    cfg = srv.call("autotranscribe.config")
    assert cfg["globals"] == {"signal": False, "telegram": False, "whatsapp": False}
    assert srv.call("autotranscribe.set_global", {"protocol": "signal", "enabled": True})["signal"] is True
    assert srv.call("autotranscribe.config")["globals"]["signal"] is True
    assert srv.call("autotranscribe.chat", {"protocol": "signal", "contact_id": "c"}) == {
        "global": True, "override": None, "effective": True,
    }
    res = srv.call("autotranscribe.set_chat", {"protocol": "signal", "contact_id": "c", "enabled": False})
    assert res == {"global": True, "override": False, "effective": False}
    res = srv.call("autotranscribe.set_chat", {"protocol": "signal", "contact_id": "c", "enabled": None})
    assert res["override"] is None and res["effective"] is True


# -- link / gmail --------------------------------------------------------------


def test_link_status_and_required(go_server: Server):
    st = go_server.call("link.status")["protocols"]
    assert set(st) == {"signal", "telegram", "whatsapp"}
    for body in st.values():
        assert set(body) == {"available", "account", "linked"}
        assert body["available"] is False  # no <X>_ENABLED flags in the fixture
    assert go_server.call("link.required") == {"protocols": {"whatsapp": False}}


def test_no_flags_exposes_no_backends(go_server: Server):
    """Default-off: no flag means no chat backend registered and Gmail off."""
    assert go_server.call("server.status") == {"signal": False, "whatsapp": False, "telegram": False}
    st = go_server.call("link.status")["protocols"]
    assert {p: body["available"] for p, body in st.items()} == {
        "signal": False, "whatsapp": False, "telegram": False,
    }
    assert go_server.call("gmail.status")["configured"] is False
    assert go_server.call("gmail.unread")["unread"] == 0
    # A disabled protocol has no backend, so its contact snapshot is unavailable.
    e = err_of(go_server, "contacts.for_protocol", {"protocol": "signal"})
    assert (e.code, e.message) == ("backend_down", "Backend unavailable")


def test_link_start_disabled_names_the_flag(go_server: Server):
    for proto, flag in (("signal", "SIGNAL_ENABLED"), ("whatsapp", "WHATSAPP_ENABLED"),
                        ("telegram", "TELEGRAM_ENABLED")):
        e = err_of(go_server, "link.start", {"protocol": proto})
        assert e.code == "backend_down" and f"{flag}=1" in e.message, (proto, e.message)
    e = err_of(go_server, "link.submit_2fa", {"password": "x"})
    assert e.code == "backend_down" and "TELEGRAM_ENABLED=1" in e.message


def test_flags_start_exactly_their_integration(go_binary):
    # SIGNAL_ENABLED registers Signal; TELEGRAM_ENABLED without credentials and
    # GMAIL_ENABLED without credentials must not start anything.
    srv = start_server("go", binary=go_binary, extra_env={
        "SIGNAL_ENABLED": "1",
        "TELEGRAM_ENABLED": "1",
        "GMAIL_ENABLED": "1",
    })
    try:
        st = srv.call("link.status")["protocols"]
        assert st["signal"]["available"] is True
        assert st["whatsapp"]["available"] is False
        assert st["telegram"]["available"] is False
        assert srv.call("gmail.status")["configured"] is False
    finally:
        srv.stop()


def test_gmail_status_unconfigured(go_server: Server):
    st = go_server.call("gmail.status")
    assert st["configured"] is False and st["connected"] is None
    assert st["folder"] == "INBOX"
    assert st["limits"]["max_messages_limit"] == 200
    unread = go_server.call("gmail.unread")
    assert unread["unread"] == 0


@pytest.mark.parametrize(
    "op,params",
    [
        ("gmail.address_book", None),
        ("gmail.messages.list", None),
        ("gmail.messages.get", {"uid": 1}),
        ("gmail.messages.delete", {"uid": 1}),
        ("gmail.messages.mark_read", {"uid": 1}),
        ("gmail.messages.mark_unread", {"uid": 1}),
        ("gmail.messages.archive", {"uid": 1}),
        ("gmail.messages.bulk_delete", {"uids": [1]}),
        ("gmail.attachment", {"uid": 1, "index": 0}),
    ],
)
def test_gmail_ops_unconfigured_are_backend_down(go_server: Server, op, params):
    e = err_of(go_server, op, params)
    assert (e.code, e.message) == ("backend_down", "Gmail non configurato")


def test_gmail_image_unknown_token(go_server: Server):
    assert err_of(go_server, "gmail.image", {"token": "nope"}).code == "not_found"


# -- contacts ------------------------------------------------------------------


def test_contacts_empty(go_server: Server):
    assert go_server.call("contacts.list") == []
    assert go_server.call("contacts.book", {"q": ""}) == []
    assert go_server.call("contacts.address_book") == {"contacts": [], "errors": {}}
    assert go_server.call("contacts.check_number", {"phone": "+390000"}) == {"exists": None}


def test_contacts_for_protocol_with_registered_backend(go_signal: Server):
    assert go_signal.call("contacts.for_protocol", {"protocol": "signal"}) == []


def test_contacts_register_then_list(go_signal: Server):
    srv = go_signal
    reg = srv.call("contacts.register", {"id": " x1 ", "display_name": "X One", "protocol": "signal"})
    assert reg["id"] == "x1" and reg["cache_key"] == "signal:x1"
    assert reg["extras"] == {"ghost": True}
    lst = srv.call("contacts.list")
    assert [c["id"] for c in lst] == ["x1"]
    assert set(lst[0]) == {"id", "display_name", "protocol", "extras", "last_message_ts", "unread"}
    assert srv.call("contacts.list", {"q": "zzz"}) == []
    assert [c["id"] for c in srv.call("contacts.list", {"q": "ONE"})] == ["x1"]


def test_contacts_avatar_unknown(go_server: Server):
    assert err_of(go_server, "contacts.avatar", {"protocol": "signal", "contact_id": "nobody"}).code == "not_found"


# -- messages on an empty DB ---------------------------------------------------


def test_messages_empty_db(go_server: Server):
    assert go_server.call("messages.page", {"protocol": "signal", "contact_id": "x"}) == []
    assert go_server.call("messages.window", {"protocol": "whatsapp", "contact_id": "x"}) == {
        "messages": [], "refreshed": False,
    }
    for proto in ("signal", "whatsapp", "telegram"):
        assert go_server.call("messages.snapshot", {"protocol": proto}) == {}


# -- messages on the seeded DB -------------------------------------------------


def test_messages_page_seeded_signal(go_seeded: Server):
    rows = go_seeded.call("messages.page", {"protocol": "signal", "contact_id": SIG})
    assert [r["text"] for r in rows][:2] == ["Ciao Mario", "Ciao! Come va?"]
    assert [r["timestamp"] for r in rows] == sorted(r["timestamp"] for r in rows)
    first, second = rows[0], rows[1]
    assert first["direction"] == "in" and first["status"] is None
    assert second["direction"] == "out" and second["status"] == "read"
    assert first["reactions"][0]["emoji"] == "\U0001F44D"
    assert second["reactions"][0]["is_mine"] is True
    img = next(r for r in rows if r["attachment"] and r["attachment"]["attachment_id"] == "sigimg1")
    assert img["attachment"]["type"] == "image/png" and img["attachment"]["name"] == "foto.png"
    assert "version" in img["attachment"]
    quote = next(r for r in rows if r["text"] == "Bellissima")
    assert quote["quote_text"] == "foto.png" and quote["quote_attachment_id"] == "sigimg1"
    assert quote["quote_thumb_url"].startswith("api/media/signal/")
    assert quote["quote_author"] == ""  # 1:1 chat
    failed = next(r for r in rows if r["text"] == "failed one")
    assert failed["status"] == "failed"


def test_messages_page_filters(go_seeded: Server):
    base = {"protocol": "signal", "contact_id": SIG}
    allrows = go_seeded.call("messages.page", base)
    newest2 = go_seeded.call("messages.page", {**base, "limit": 2})
    assert newest2 == allrows[-2:]
    since = allrows[3]["timestamp"]
    assert go_seeded.call("messages.page", {**base, "since": since}) == allrows[3:]
    for bad in (0, 2001, -1):
        e = err_of(go_seeded, "messages.page", {**base, "limit": bad})
        assert (e.code, e.message) == ("invalid_request", "Invalid limit")


def test_messages_page_other_protocols(go_seeded: Server):
    wa = go_seeded.call("messages.page", {"protocol": "whatsapp", "contact_id": WA})
    assert [r["text"] for r in wa][:2] == ["Hello from WhatsApp", "Reply here"]
    grp = go_seeded.call("messages.page", {"protocol": "whatsapp", "contact_id": WA_GROUP})
    assert len(grp) == 3 and all(r.get("is_group") for r in grp)
    tg = go_seeded.call("messages.page", {"protocol": "telegram", "contact_id": TG})
    assert [r["direction"] for r in tg] == ["in", "out", "in"]
    assert tg[0]["reactions"][0]["count"] == 2


def test_messages_window_and_snapshot_seeded(go_seeded: Server):
    win = go_seeded.call("messages.window", {"protocol": "signal", "contact_id": SIG})
    assert win["refreshed"] is False
    assert len(win["messages"]) == 7
    assert {"id", "text", "is_mine", "sender", "timestamp", "msg_type", "protocol"} <= set(win["messages"][0])
    snap = go_seeded.call("messages.snapshot", {"protocol": "signal"})
    assert list(snap) == [SIG] and snap[SIG] == win["messages"]


def test_chats_mark_read_persists(go_seeded: Server):
    assert go_seeded.call("chats.mark_read", {"protocol": "signal", "contact_id": SIG}) == {"status": "ok"}
    rows = go_seeded.call("messages.page", {"protocol": "signal", "contact_id": SIG})
    assert all(r["read"] for r in rows)


def test_transcription_rows_visible(go_seeded: Server):
    # no provider configured -> the status op answers backend_down, not data
    e = err_of(go_seeded, "transcribe.status", {"protocol": "signal", "attachment_id": "sigvoice1"})
    assert e.code == "backend_down"


# -- operations that need a backend --------------------------------------------


def test_backend_ops_without_backend(go_server: Server):
    e = err_of(go_server, "messages.send", {"protocol": "signal", "contact_id": "x", "text": "hi"})
    assert e.code == "not_found"
    e = err_of(go_server, "messages.send", {"protocol": "signal", "contact_id": "x", "text": "  "})
    assert e.code == "invalid_request"
    e = err_of(go_server, "messages.send.check", {"protocol": "signal", "contact_id": "x", "text": "hi"})
    assert e.code == "not_found"
    e = err_of(go_server, "chats.archive", {"protocol": "whatsapp", "contact_id": "x", "archived": True})
    assert e.code in ("backend_down", "not_found")
    e = err_of(go_server, "messages.edit", {"protocol": "signal", "contact_id": "x", "message_id": "1", "new_text": "y"})
    assert e.code == "not_found"
    e = err_of(go_server, "reactions.set", {"protocol": "signal", "contact_id": "x", "message_id": "1", "emoji": "\U0001F44D"})
    assert e.code == "not_found"
    e = err_of(go_server, "link.start", {"protocol": "whatsapp"})
    assert e.code in ("backend_down", "not_supported", "backend_error")


# -- error paths ---------------------------------------------------------------


def test_unknown_operation(go_server: Server):
    with pytest.raises(ApiError) as exc:
        go_server.call("no.such.op")
    assert exc.value.code in ("not_found", "method_not_found") or "Unknown" in exc.value.message


def test_invalid_params_report_every_field(go_server: Server):
    e = err_of(go_server, "messages.page", {})
    assert (e.code, e.message) == ("invalid_request", "Invalid request")
    assert [(d["type"], d["loc"]) for d in e.details] == [
        ("missing", ["protocol"]),
        ("missing", ["contact_id"]),
    ]
    e = err_of(go_server, "messages.page", {"protocol": "bogus", "contact_id": 5, "limit": "x"})
    assert [d["loc"] for d in e.details] == [["protocol"], ["contact_id"], ["limit"]]
    e = err_of(go_server, "autotranscribe.set_chat", {"protocol": "zzz"})
    assert [d["loc"][0] for d in e.details] == ["protocol", "contact_id", "enabled"]
    e = err_of(go_server, "chats.archive", {"protocol": "whatsapp", "contact_id": "x", "archived": "yes"})
    assert e.details[0]["type"] == "bool_type"


@pytest.mark.parametrize("op", ["messages.page", "link.start", "reactions.set", "contacts.register", "media.attachment"])
def test_invalid_details_are_pydantic_lists(go_server: Server, op):
    e = err_of(go_server, op, {})
    assert e.code == "invalid_request"
    assert isinstance(e.details, list) and e.details
    assert all(set(d) == {"type", "loc", "msg"} for d in e.details)


# -- raw JSON-RPC protocol errors ----------------------------------------------


class Raw:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX)
        self.s.settimeout(8)
        self.s.connect(str(path))
        self.buf = b""

    def send(self, data: bytes):
        self.s.sendall(data)

    def line(self) -> dict:
        while b"\n" not in self.buf:
            chunk = self.s.recv(65536)
            if not chunk:
                raise ConnectionError("closed")
            self.buf += chunk
        line, _, self.buf = self.buf.partition(b"\n")
        return json.loads(line)

    def rpc(self, **req):
        self.send(json.dumps({"jsonrpc": "2.0", **req}).encode() + b"\n")
        return self.line()

    def close(self):
        self.s.close()


@pytest.fixture
def raw(go_server: Server):
    r = Raw(go_server.socket_path)
    yield r
    r.close()


def test_rpc_parse_error_keeps_connection(raw: Raw):
    raw.send(b"{not json\n")
    msg = raw.line()
    assert msg["error"]["code"] == -32700 and msg["id"] is None
    assert raw.rpc(id=1, method="server.ping")["result"]["pong"] is True


def test_rpc_envelope_errors(raw: Raw):
    raw.send(b"[]\n")
    assert raw.line()["error"] == {"code": -32600, "message": "Batch requests are not supported"}
    raw.send(b"5\n")
    assert raw.line()["error"]["code"] == -32600
    assert raw.rpc(id=True, method="server.ping")["error"]["code"] == -32600
    assert raw.rpc(id=2, method=5)["error"]["message"] == "method must be a string"
    raw.send(json.dumps({"jsonrpc": "1.0", "id": 3, "method": "server.ping"}).encode() + b"\n")
    assert raw.line()["error"]["message"] == "jsonrpc must be '2.0'"
    assert raw.rpc(id=4, method="server.ping", params=[1])["error"]["message"] == "params must be an object"
    assert raw.rpc(id=5, method="server.ping", timeout=0)["error"]["message"] == "timeout must be a number in (0, 600]"


def test_rpc_unknown_method(raw: Raw):
    msg = raw.rpc(id="a", method="nope.nope")
    assert msg["id"] == "a"
    assert msg["error"]["code"] == -32601
    assert msg["error"]["message"] == "Unknown method 'nope.nope'"
    assert msg["error"]["data"]["code"] == "method_not_found"


def test_rpc_error_code_mapping(raw: Raw):
    msg = raw.rpc(id=1, method="messages.page", params={})
    assert msg["error"]["code"] == -32602
    assert msg["error"]["data"]["code"] == "invalid_request"
    msg = raw.rpc(id=2, method="gmail.status")
    assert msg["result"]["configured"] is False
    msg = raw.rpc(id=3, method="gmail.messages.list")
    assert msg["error"]["code"] == -32008  # backend_down


def test_rpc_notification_is_not_answered(raw: Raw):
    raw.send(json.dumps({"jsonrpc": "2.0", "method": "server.ping"}).encode() + b"\n")
    assert raw.rpc(id=9, method="server.ping")["id"] == 9


def test_rpc_events_subscribe_and_replay(raw: Raw):
    res = raw.rpc(id=1, method="events.subscribe", params={"since": 0})
    assert res["result"]["subscribed"] is True
    last = res["result"]["last_seq"]
    seqs = []
    for _ in range(last):
        note = raw.line()
        assert note["method"] == "event"
        seqs.append(note["params"]["seq"])
    assert seqs == list(range(1, last + 1))
    bad = raw.rpc(id=2, method="events.subscribe", params={"since": -1})
    assert bad["error"]["code"] == -32602
    assert raw.rpc(id=3, method="events.unsubscribe")["result"] == {"subscribed": False}


def test_rpc_heartbeat_when_idle(raw: Raw):
    raw.rpc(id=1, method="events.subscribe")
    t0 = time.monotonic()
    while True:
        msg = raw.line()
        if msg.get("method") == "heartbeat":
            assert "ts" in msg["params"]
            break
        assert time.monotonic() - t0 < 9
    assert 3 < time.monotonic() - t0 < 8


def test_socket_client_roundtrip_and_close(go_fresh: Server):
    c = SocketClient.connect(go_fresh.socket_path, timeout=5, reconnect=False)
    assert c.connected and c.call("server.status")["signal"] is False
    c.close()
    assert not c.connected


def test_shutdown_stops_the_process(go_fresh: Server):
    out = go_fresh.call("server.shutdown")
    assert out.get("status") == "ok" or out.get("ok") is True
    go_fresh.proc.wait(10)
    assert go_fresh.proc.returncode == 0
