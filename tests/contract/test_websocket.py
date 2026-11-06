"""``/ws`` (legacy thin frames) and ``/ws/v2`` (typed events with resume): the
handshake with the subprotocol token, the since/subscribe protocol and the
5-second heartbeat, against the Go server."""

from __future__ import annotations

import time

import pytest
from harness import TOKEN, Server, WSConn, b64url

BEARER = {"Authorization": f"Bearer {TOKEN}"}
SUB_OK = ["signal-tui-bearer", f"signal-tui-token.{b64url(TOKEN)}"]


def recv_until(ws, type_: str, timeout: float = 5.0):
    """Read frames until one of *type_* shows up (startup events are async)."""
    deadline = time.monotonic() + timeout
    while True:
        left = deadline - time.monotonic()
        if left <= 0:
            raise AssertionError(f"no {type_!r} frame within {timeout}s")
        frame = ws.recv_json(left)
        if frame.get("type") == type_:
            return frame


#: (id, path, subprotocols, headers, expected status, selected subprotocol).
#: Shared with test_parity.py: the Python server must answer the same.
HANDSHAKES = [
    ("subprotocol-token", "/ws", SUB_OK, None, 101, "signal-tui-bearer"),
    ("subprotocol-token-v2", "/ws/v2", SUB_OK, None, 101, "signal-tui-bearer"),
    ("header-bearer", "/ws", None, BEARER, 101, None),
    ("header-bearer-v2", "/ws/v2", None, BEARER, 101, None),
    ("header-and-offered-protocol", "/ws", ["signal-tui-bearer"], BEARER, 101, None),
    ("token-protocol-only", "/ws/v2", [f"signal-tui-token.{b64url(TOKEN)}"], None, 101, None),
    ("no-credentials", "/ws", None, None, 403, None),
    ("no-credentials-v2", "/ws/v2", None, None, 403, None),
    ("wrong-token", "/ws", ["signal-tui-bearer", f"signal-tui-token.{b64url('bad')}"], None, 403, None),
    ("invalid-base64", "/ws", ["signal-tui-bearer", "signal-tui-token.!!!"], None, 403, None),
    ("bearer-protocol-only", "/ws", ["signal-tui-bearer"], None, 403, None),
    ("wrong-header", "/ws", None, {"Authorization": "Bearer nope"}, 403, None),
    ("v2-since-junk", "/ws/v2?since=abc", None, BEARER, 403, None),
    ("v2-since-negative", "/ws/v2?since=-1", None, BEARER, 403, None),
]


@pytest.mark.parametrize("name,path,sub,hdr,status,selected", HANDSHAKES, ids=[h[0] for h in HANDSHAKES])
def test_handshake(go_server: Server, name, path, sub, hdr, status, selected):
    ws = WSConn.open(go_server.port, path, subprotocols=sub, headers=hdr)
    try:
        assert ws.status == status
        assert ws.headers.get("sec-websocket-protocol") == selected
    finally:
        ws.close()


def test_handshake_no_auth_server(go_binary):
    from harness import start_server

    srv = start_server("go", binary=go_binary, token=None, extra_args=("--web-no-auth",))
    try:
        for path in ("/ws", "/ws/v2"):
            ws = WSConn.open(srv.port, path)
            assert ws.status == 101
            ws.close()
    finally:
        srv.stop()


def test_v2_since_zero_replays_from_the_ring(go_signal: Server):
    # SIGNAL_ENABLED=1: the registered backend publishes backend_connecting,
    # two backend_status frames and backend_done (its connect has no account).
    ws = WSConn.open(go_signal.port, "/ws/v2?since=0", headers=BEARER)
    try:
        first = ws.recv_json(5)
        assert first["seq"] == 1 and set(first) == {"seq", "type", "payload", "ts"}
        assert first["type"] == "backend_connecting"
        assert first["payload"] == {"protocol": "signal"}
        done = recv_until(ws, "backend_done")
        assert done["payload"] == {"protocol": "signal", "ok": False}
    finally:
        ws.close()


def test_v2_since_future_is_clamped_and_live_only(go_server: Server):
    ws = WSConn.open(go_server.port, "/ws/v2?since=999999", headers=BEARER)
    try:
        # no backend on this server: nothing to replay, the next frame is the
        # heartbeat, never an old event
        frame = ws.recv_json(8)
        assert frame == {"type": "heartbeat"}
    finally:
        ws.close()


def test_v2_subscribe_frame_restarts_the_stream(go_signal: Server):
    ws = WSConn.open(go_signal.port, "/ws/v2", headers=BEARER)
    try:
        ws.send_json({"type": "subscribe", "since": 0})
        frame = ws.recv_json(5)
        assert frame["seq"] == 1 and frame["type"] == "backend_connecting"
    finally:
        ws.close()


@pytest.mark.parametrize("since", [True, -1, 1.5, "junk", [1]])
def test_v2_invalid_subscribe(go_signal: Server, since):
    ws = WSConn.open(go_signal.port, "/ws/v2", headers=BEARER)
    try:
        ws.send_json({"type": "subscribe", "since": since})
        assert ws.recv_json(5) == {"type": "error", "payload": {"reason": "invalid_subscribe"}}
        # the connection stays usable
        ws.send_json({"type": "subscribe", "since": "0"})
        assert ws.recv_json(5)["seq"] == 1
    finally:
        ws.close()


def test_v2_ignores_non_subscribe_frames(go_signal: Server):
    ws = WSConn.open(go_signal.port, "/ws/v2", headers=BEARER)
    try:
        ws._send(1, b"not json")
        ws.send_json({"type": "hello"})
        ws.send_json({"type": "subscribe", "since": 0})
        assert ws.recv_json(5)["seq"] == 1
    finally:
        ws.close()


@pytest.mark.parametrize("path", ["/ws", "/ws/v2"])
def test_heartbeat_when_idle(go_server: Server, path):
    ws = WSConn.open(go_server.port, path, subprotocols=SUB_OK)
    try:
        t0 = time.monotonic()
        while True:
            frame = ws.recv_json(9)
            if frame.get("type") == "heartbeat":
                assert frame == {"type": "heartbeat"}
                break
            assert time.monotonic() - t0 < 9, frame
        assert time.monotonic() - t0 < 8
    finally:
        ws.close()


def test_legacy_ws_ignores_client_messages(go_server: Server):
    ws = WSConn.open(go_server.port, "/ws", headers=BEARER)
    try:
        ws.send_json({"type": "ping"})
        ws._send(1, b"garbage")
        frame = ws.recv_json(9)
        assert frame == {"type": "heartbeat"}
    finally:
        ws.close()


def test_client_connections_are_counted(go_fresh: Server):
    base = go_fresh.call("server.clients")
    ws = WSConn.open(go_fresh.port, "/ws/v2", headers=BEARER)
    try:
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            if go_fresh.call("server.clients")["web"] >= 1:
                break
            time.sleep(0.05)
        assert go_fresh.call("server.clients")["web"] >= 1
        assert base["web"] == 0
    finally:
        ws.close()
