"""The REST route table of the Go server, with and without auth, the CSRF
check, FastAPI-style 422s, blob routes and the static SPA."""

from __future__ import annotations

import json

import pytest
from harness import TOKEN, Server, start_server
from routes_table import SAMPLES, SIG, SIGQ, is_public

#: status of each sample request on a server with no backend (identical on the
#: Python server; test_parity.py diffs the bodies).
EXPECTED = {
    ("GET", "/api/emoji"): 200,
    ("GET", "/api/reactions?protocol=signal"): 200,
    ("POST", "/api/send"): 400,
    ("GET", "/api/status"): 200,
    ("GET", "/api/contacts"): 200,
    ("GET", "/api/contacts/book?q=zz"): 200,
    ("GET", "/api/contact-avatar?proto=signal&contact_id=nobody"): 404,
    ("GET", "/api/link-preview?url=notaurl"): 400,
    ("GET", "/api/link-preview/image?url=notaurl"): 400,
    ("POST", "/api/log"): 200,
    ("POST", "/api/transcribe"): 400,
    ("POST", "/api/config/openai-key"): 400,
    ("GET", "/api/logs?limit=5"): 200,
    ("DELETE", "/api/logs"): 200,
    ("GET", "/api/transcribe/diagnostics"): 200,
    ("POST", "/api/transcribe/check"): 200,
    ("GET", "/api/transcribe/config"): 200,
    ("GET", "/api/transcribe/settings"): 200,
    ("PUT", "/api/transcribe/settings"): 200,
    ("PUT", "/api/transcribe/api-key"): 400,
    ("GET", "/api/autotranscribe"): 200,
    ("PUT", "/api/autotranscribe/global/signal"): 200,
    ("GET", f"/api/autotranscribe/chat?proto=signal&contact_id={SIGQ}"): 200,
    ("PUT", f"/api/autotranscribe/chat?proto=signal&contact_id={SIGQ}"): 200,
    ("GET", "/api/transcribe/signal/sigvoice1"): 503,
    ("GET", "/api/transcribe/status?protocol=signal&attachment_id=sigvoice1"): 503,
    ("GET", f"/api/messages?proto=signal&contact_id={SIGQ}"): 200,
    ("POST", "/api/messages/read"): 200,
    ("POST", "/api/chats/archive"): 400,
    ("POST", "/api/messages/edit"): 404,
    ("POST", "/api/messages/reaction"): 400,
    ("GET", "/api/link/status"): 200,
    ("GET", "/api/link/required"): 200,
    ("POST", "/api/link/start"): 400,
    ("POST", "/api/link/poll"): 400,
    ("POST", "/api/link/finalize"): 502,
    ("POST", "/api/link/cancel"): 400,
    ("POST", "/api/link/submit_2fa"): 503,
    ("POST", "/api/link/reconnect"): 200,
    ("GET", "/api/media/signal/does-not-exist"): 404,
    ("GET", "/api/quote-media/signal/999999"): 404,
    ("GET", "/api/gmail/status"): 200,
    ("GET", "/api/gmail/unread"): 200,
    ("GET", "/api/gmail/address-book"): 503,
    ("GET", "/api/gmail/messages"): 503,
    ("POST", "/api/gmail/messages/bulk-delete"): 503,
    ("DELETE", "/api/gmail/messages/1"): 503,
    ("GET", "/api/gmail/messages/1"): 503,
    ("GET", "/api/gmail/messages/1/attachments/0"): 503,
    ("POST", "/api/gmail/messages/1/read"): 503,
    ("POST", "/api/gmail/messages/1/unread"): 503,
    ("POST", "/api/gmail/messages/1/archive"): 503,
    ("POST", "/api/gmail/send"): 503,
    ("GET", "/gmail-image?token=nope"): 404,
}


def test_samples_cover_every_route():
    assert {(m, p) for m, p, _ in SAMPLES} == set(EXPECTED)
    assert len(SAMPLES) + 1 == 55  # CONTRACTS.md section 7 counts /health too


def test_health(go_server: Server):
    r = go_server.get("/health", token=None)
    assert r.status == 200
    assert r.json() == {"status": "ok", "port": go_server.port, "auth_required": True}


@pytest.mark.parametrize("method,path,body", SAMPLES, ids=[f"{m} {p.split('?')[0]}" for m, p, _ in SAMPLES])
def test_route_with_token(go_server: Server, method, path, body):
    r = go_server.http(method, path, json_body=body)
    assert r.status == EXPECTED[(method, path)], r.body[:200]
    if "application/json" in r.headers.get("content-type", ""):
        data = r.json()
        if r.status >= 400:
            assert isinstance(data, dict) and "detail" in data


@pytest.mark.parametrize("method,path,body", SAMPLES, ids=[f"{m} {p.split('?')[0]}" for m, p, _ in SAMPLES])
def test_route_auth_required(go_server: Server, method, path, body):
    for token in (None, "wrong"):
        r = go_server.http(method, path, token=token, json_body=body)
        if is_public(path):
            assert r.status != 401
            continue
        assert r.status == 401, (token, r.status)
        assert r.json() == {"detail": "Unauthorized"}
        assert r.headers["www-authenticate"] == "Bearer"


def test_auth_runs_before_routing(go_server: Server):
    assert go_server.get("/api/does-not-exist", token=None).status == 401
    assert go_server.get("/api/does-not-exist").status == 404
    assert go_server.get("/api/does-not-exist").json() == {"detail": "Not Found"}


@pytest.mark.parametrize(
    "header,ok",
    [
        (f"Bearer {TOKEN}", True),
        (f"bearer {TOKEN}", True),
        (f"BEARER {TOKEN}", True),
        (f"Bearer  {TOKEN}", False),
        (f"Bearer {TOKEN}x", False),
        ("Bearer ", False),
        ("Bearer", False),
        (f"Basic {TOKEN}", False),
        (TOKEN, False),
    ],
)
def test_authorization_header_forms(go_server: Server, header, ok):
    r = go_server.get("/api/status", headers={"Authorization": header}, token=None)
    assert (r.status == 200) is ok


def test_no_cors_headers(go_server: Server):
    r = go_server.get("/api/status", headers={"Origin": "http://elsewhere.example"})
    assert not any(k.startswith("access-control-") for k in r.headers)


def test_csrf_origin_check_on_send(go_server: Server):
    body = {"protocol": "signal", "contact_id": "x", "text": "a"}
    r = go_server.http("POST", "/api/send", headers={"Origin": "http://evil.example"}, json_body=body)
    assert (r.status, r.json()) == (403, {"detail": "Forbidden"})
    ok = go_server.http("POST", "/api/send", headers={"Origin": f"http://127.0.0.1:{go_server.port}"}, json_body=body)
    assert ok.status == 404  # passed the CSRF check, contact unknown
    # X-Forwarded-Host is only trusted for an https Origin
    r = go_server.http(
        "POST", "/api/send",
        headers={"Origin": "https://proxy.example", "X-Forwarded-Host": "proxy.example"},
        json_body=body,
    )
    assert r.status == 404
    r = go_server.http(
        "POST", "/api/send",
        headers={"Origin": "http://proxy.example", "X-Forwarded-Host": "proxy.example"},
        json_body=body,
    )
    assert r.status == 403


def test_send_body_errors(go_server: Server):
    r = go_server.http("POST", "/api/send", body=b"not json", headers={"Content-Type": "application/json"})
    assert (r.status, r.json()) == (400, {"detail": "Invalid request"})
    r = go_server.http("POST", "/api/send", json_body=[1])
    assert (r.status, r.json()) == (400, {"detail": "Invalid request"})


def test_validation_422(go_server: Server):
    for path in (
        "/api/reactions",
        "/api/messages?proto=bogus&contact_id=x",
        "/api/messages?contact_id=x",
        f"/api/messages?proto=signal&contact_id={SIGQ}&limit=abc",
        "/api/contacts/book",
        "/api/autotranscribe/chat?proto=signal",
        "/api/media/bogus/abc",
        "/api/quote-media/signal/notint",
        "/api/gmail/messages/abc",
    ):
        r = go_server.get(path)
        assert r.status == 422, path
        detail = r.json()["detail"]
        assert isinstance(detail, list) and detail and "loc" in detail[0]
    r = go_server.http("PUT", "/api/autotranscribe/global/bogus", json_body={"enabled": True})
    assert r.status == 422
    r = go_server.http("POST", "/api/messages/read", json_body=[1, 2])
    assert r.status == 422


def test_messages_limit_range(go_server: Server):
    r = go_server.get(f"/api/messages?proto=signal&contact_id={SIGQ}&limit=0")
    assert (r.status, r.json()) == (400, {"detail": "Invalid limit"})


def test_autotranscribe_chat_put_requires_enabled(go_server: Server):
    r = go_server.http("PUT", f"/api/autotranscribe/chat?proto=signal&contact_id={SIGQ}", json_body={})
    assert (r.status, r.json()) == (422, {"detail": "enabled richiesto"})


def test_emoji_headers(go_server: Server):
    r = go_server.get("/api/emoji")
    assert r.headers["cache-control"] == "public, max-age=3600"
    assert r.headers["content-type"].startswith("application/json")


def test_method_not_allowed(go_server: Server):
    assert go_server.http("POST", "/api/status").status == 405
    assert go_server.http("GET", "/api/messages/read").status == 405


def test_static_spa(go_server: Server):
    for path in ("/", "/index.html"):
        r = go_server.get(path, token=None)
        assert r.status == 200 and b"<html" in r.body.lower()
        assert r.headers["cache-control"] == "no-cache"
    assert go_server.get("/app.js", token=None).status == 200
    assert go_server.get("/style.css", token=None).headers["content-type"].startswith("text/css")
    assert go_server.get("/nope.js", token=None).status == 404


def test_seeded_messages_route_equals_op(go_seeded: Server):
    via_http = go_seeded.get(f"/api/messages?proto=signal&contact_id={SIGQ}").json()
    via_sock = go_seeded.call("messages.page", {"protocol": "signal", "contact_id": SIG})
    assert via_http == via_sock and len(via_http) == 7
    r = go_seeded.get(f"/api/messages?proto=signal&contact_id={SIGQ}&limit=2&since={via_http[3]['timestamp']}")
    assert r.json() == via_http[-2:]


def test_seeded_blob_routes(go_seeded: Server):
    r = go_seeded.get("/api/media/signal/sigimg1")
    assert r.status == 200 and r.body[:4] == b"\x89PNG"
    assert r.headers["content-type"] == "application/octet-stream"  # extensionless file, no stored type
    assert r.headers["accept-ranges"] == "bytes"
    assert "cache-control" in r.headers
    rng = go_seeded.get("/api/media/signal/sigimg1", headers={"Range": "bytes=0-3"})
    assert rng.status == 206 and rng.body == b"\x89PNG"
    thumb = go_seeded.get("/api/media/signal/sigimg1?w=96")
    assert thumb.status == 200
    assert go_seeded.get("/api/media/signal/../../etc/passwd").status in (400, 404)


def test_seeded_transcription_status_route(go_seeded: Server):
    # no provider configured -> 503, same as the op
    assert go_seeded.get("/api/transcribe/signal/sigvoice1").status == 503


def test_web_no_auth_flag(go_binary):
    srv = start_server("go", binary=go_binary, token=None, extra_args=("--web-no-auth",))
    try:
        assert srv.get("/health", token=None).json()["auth_required"] is False
        assert srv.get("/api/status", token=None).status == 200
    finally:
        srv.stop()


def test_refuses_to_start_without_token(go_binary):
    with pytest.raises(RuntimeError) as exc:
        start_server("go", binary=go_binary, token=None)
    assert "Bearer token" in str(exc.value)


def test_token_from_config_json(go_binary, tmp_path):
    # web.token in <project-dir>/config.json is the fallback of WEB_TOKEN
    import harness

    orig = harness._env

    def env_with_cfg(root, home, token):
        env = orig(root, home, None)
        (root / "proj" / "config.json").write_text(json.dumps({"web": {"token": "cfg-token"}}))
        return env

    harness._env = env_with_cfg
    try:
        srv = start_server("go", binary=go_binary, token=None)
    finally:
        harness._env = orig
    try:
        assert srv.get("/api/status", token="cfg-token").status == 200
        assert srv.get("/api/status", token="other").status == 401
    finally:
        srv.stop()
