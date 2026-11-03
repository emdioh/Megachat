"""Minimal status/QR page (server/status_page.py) and its config helper."""

from __future__ import annotations

import json
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

import pytest

from server import status_page

PROJECT_ROOT = Path(__file__).resolve().parent.parent
PNG = b"\x89PNG\r\n\x1a\nfake"


class FakeRest:
    session_name = "default"

    def __init__(self, state: dict[str, Any]) -> None:
        self.state = state
        self.last_status = 200
        self.calls: list[str] = []

    def _request(self, method, path, payload=None, timeout=30, log_level=0):
        status = self.state.get("status")
        if status is None:
            self.last_status = self.state.get("http", 0)
            return None
        self.last_status = 200
        data: dict[str, Any] = {"status": status}
        if status == "WORKING":
            data["me"] = {"id": "393331234567@c.us", "pushName": "Mario"}
        return data

    def get_session_qr(self):
        return self.state.get("qr", PNG)

    def request_pairing_code(self, phone):
        self.calls.append(f"pair:{phone}")
        return self.state.get("code", "ABCD-EFGH")

    def restart_session(self):
        self.calls.append("restart")

    def start_session(self):
        self.calls.append("start")

    def configure_webhook(self):
        self.calls.append("webhook")
        return True


@pytest.fixture
def page(monkeypatch):
    monkeypatch.setattr(
        status_page,
        "_transcription",
        lambda: {"provider": "openai", "configured": True, "auto_whatsapp": True},
    )
    state: dict[str, Any] = {"status": "SCAN_QR_CODE"}
    rest = FakeRest(state)
    handle = status_page.start(
        "127.0.0.1", 0, "s3cret", name="mario", rest_factory=lambda: rest
    )
    base = f"http://127.0.0.1:{handle.server.server_address[1]}"
    yield base, state, rest
    status_page.stop(handle)


def _get(url: str, data: bytes | None = None) -> tuple[int, bytes, str]:
    req = urllib.request.Request(
        url, data=data, method="POST" if data is not None else "GET"
    )

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    opener = urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(req, timeout=10) as resp:
            return resp.status, resp.read(), resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as err:
        return err.code, err.read(), err.headers.get("Location") or ""


def test_wrong_or_missing_token_is_404(page) -> None:
    base, _, _ = page
    assert _get(f"{base}/")[0] == 404
    assert _get(f"{base}/nope/")[0] == 404
    assert _get(f"{base}/s3cretX/status.json")[0] == 404
    code, _, location = _get(f"{base}/s3cret")
    assert code == 303 and location == "/s3cret/"


def test_qr_state_page_and_png(page) -> None:
    base, _, _ = page
    code, body, _ = _get(f"{base}/s3cret/")
    text = body.decode()
    assert code == 200
    assert "In attesa di collegamento" in text and "qr.png" in text
    assert 'data-state="qr"' in text
    code, body, ctype = _get(f"{base}/s3cret/qr.png")
    assert (code, body, ctype) == (200, PNG, "image/png")
    status = json.loads(_get(f"{base}/s3cret/status.json")[1])
    assert status["whatsapp"]["state"] == "qr"
    assert status["transcription"]["configured"] is True


def test_text_qr_is_rendered_to_png(page) -> None:
    base, state, _ = page
    state["qr"] = "2@some-raw-qr-value"
    code, body, ctype = _get(f"{base}/s3cret/qr.png")
    assert code == 200 and ctype == "image/png" and body.startswith(b"\x89PNG")


def test_linked_state_hides_qr(page) -> None:
    base, state, _ = page
    state["status"] = "WORKING"
    text = _get(f"{base}/s3cret/")[1].decode()
    assert "WhatsApp collegato" in text and "Mario" in text and 'class="qr"' not in text
    assert _get(f"{base}/s3cret/qr.png")[0] == 404


def test_pairing_code_validation_and_cooldown(page) -> None:
    base, _, rest = page
    assert _get(f"{base}/s3cret/pair", b"phone=abc")[0] == 400
    code, body, _ = _get(f"{base}/s3cret/pair", b"phone=%2B39+333+123+4567")
    assert code == 200 and b"ABCD-EFGH" in body
    assert rest.calls == ["pair:393331234567"]
    assert _get(f"{base}/s3cret/pair", b"phone=393331234567")[0] == 429


def test_start_restarts_failed_and_starts_stopped(page) -> None:
    base, state, rest = page
    state["status"] = "FAILED"
    assert "Riavvia" in _get(f"{base}/s3cret/")[1].decode()
    assert _get(f"{base}/s3cret/start", b"")[0] == 303
    state["status"] = "STOPPED"
    assert _get(f"{base}/s3cret/start", b"")[0] == 303
    assert rest.calls == ["restart", "start", "webhook"]


def test_waha_unreachable(page) -> None:
    base, state, _ = page
    state["status"] = None
    status = json.loads(_get(f"{base}/s3cret/status.json")[1])
    assert status["whatsapp"]["state"] == "unreachable"
    state["http"] = 404
    status = json.loads(_get(f"{base}/s3cret/status.json")[1])
    assert status["whatsapp"]["state"] == "stopped"


def test_empty_token_refused() -> None:
    with pytest.raises(ValueError):
        status_page.start("127.0.0.1", 0, "")


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("+39 333 123 4567", "393331234567"),
        ("0039-333-1234567", "393331234567"),
        ("(39) 333.1234567", "393331234567"),
        ("12345", None),
        ("39333abc4567", None),
    ],
)
def test_normalize_phone(raw: str, expected: str | None) -> None:
    assert status_page.normalize_phone(raw) == expected


def test_config_getters(monkeypatch, tmp_path: Path) -> None:
    from protocols import config

    monkeypatch.setenv("MEGACHAT_HOME", str(tmp_path))
    assert config.status_page_enabled() is False
    assert config.status_page_host() == "127.0.0.1"
    assert config.status_page_port() == 4300
    (tmp_path / "config.json").write_text(
        json.dumps(
            {
                "status_page": {
                    "enabled": True,
                    "host": "0.0.0.0",
                    "port": 4305,
                    "token": "t",
                }
            }
        )
    )
    assert config.status_page_enabled() is True
    assert config.status_page_host() == "0.0.0.0"
    assert config.status_page_port() == 4305
    assert config.status_page_token() == "t"


def _helper(cfg: Path, offset: int) -> tuple[int, str]:
    result = subprocess.run(
        [
            sys.executable,
            str(PROJECT_ROOT / "scripts" / "instance_config.py"),
            "status-page",
            str(cfg),
            str(offset),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    return result.returncode, result.stdout.strip()


def test_instance_config_adds_and_keeps_status_page(tmp_path: Path) -> None:
    cfg = tmp_path / "config.json"
    cfg.write_text('{"auto_transcribe": {"whatsapp": true}}')
    rc, out = _helper(cfg, 3)
    port, token = out.split()
    assert rc == 10 and port == "4303" and len(token) == 48
    data = json.loads(cfg.read_text())
    assert data["auto_transcribe"] == {"whatsapp": True}
    assert data["status_page"]["enabled"] is True
    assert _helper(cfg, 3) == (0, out)  # unchanged on the second run
