"""In-memory log buffer, ``server.logs`` and ``transcribe.diagnostics``."""

from __future__ import annotations

import logging
from pathlib import Path
from types import SimpleNamespace

import pytest
from client_harness import api_for
from fastapi import FastAPI
from fastapi.testclient import TestClient

import protocols.config as pconfig
import protocols.db as backend
from server import logbuffer
from transcription import store
from web.api import create_api_router
from web.auth import install_auth

TOKEN = "logs-secret"
AUTH = {"Authorization": f"Bearer {TOKEN}"}


@pytest.fixture(autouse=True)
def isolated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    cfg = tmp_path / "cfg"
    cfg.mkdir()
    monkeypatch.setattr(pconfig, "PROJECT_DIR", cfg)
    monkeypatch.setattr(backend, "DB_FILE", tmp_path / "protocols.db")
    monkeypatch.setattr(backend, "CACHE_DIR", tmp_path)
    for name in ("AI_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY"):
        monkeypatch.delenv(name, raising=False)
    import transcription.config as tconfig

    monkeypatch.setattr(tconfig, "_load_dotenv", dict)
    root = logging.getLogger()
    level, handlers = root.level, list(root.handlers)
    root.setLevel(logging.INFO)  # as basicConfig in the entry points
    logbuffer.get_handler().clear()
    yield
    # The INFO level above and install()'s handler: undo both so
    # other tests keep the default logging setup.
    root.setLevel(level)
    for handler in list(root.handlers):
        if handler not in handlers:
            root.removeHandler(handler)


@pytest.fixture
def api():
    instance = api_for(SimpleNamespace(get=lambda p: None))
    yield instance
    instance.transcription.stop()


def test_buffer_keeps_records_and_redacts_secrets():
    logbuffer.install()
    log = logging.getLogger("transcription.test")
    log.info("chiave sk-or-v1-abcdef123456 e Bearer token: tok123")
    log.warning("google AIzaSyA1234567890abcdef")
    rows = logbuffer.get_handler().snapshot()
    messages = [r["message"] for r in rows if r["logger"] == "transcription.test"]
    assert messages == ["chiave sk-*** e Bearer token: ***", "google AIza***"]
    assert all("abcdef123456" not in m and "tok123" not in m for m in messages)


def test_install_is_idempotent():
    first = logbuffer.install()
    assert logbuffer.install() is first
    assert logging.getLogger().handlers.count(first) == 1


def test_server_logs_op_after_and_level(api):
    logbuffer.install()
    log = logging.getLogger("transcription.test")
    log.info("uno")
    out = api.call_json("server.logs", {})
    seq = out["last_seq"]
    log.info("due")
    log.error("tre")
    newer = api.call_json("server.logs", {"after": seq})
    assert [r["message"] for r in newer["records"]] == ["due", "tre"]
    errors = api.call_json("server.logs", {"after": seq, "level": "warning"})
    assert [r["message"] for r in errors["records"]] == ["tre"]
    api.call_json("server.logs_clear", {})
    assert api.call_json("server.logs", {})["records"] == []


def test_diagnostics_unconfigured(api):
    out = api.call_json("transcribe.diagnostics", {})
    assert out["provider"] == "openai"
    assert out["api_key_set"] is False
    assert out["service_active"] is False
    assert out["recent"] == []
    assert out["auto_transcribe"] == {
        "whatsapp": False,
        "signal": False,
        "telegram": False,
    }


def test_diagnostics_configured_shows_service_and_recent(api, monkeypatch):
    monkeypatch.setenv("AI_PROVIDER", "openrouter")
    monkeypatch.setenv("OPENROUTER_API_KEY", "sk-or-v1-secretsecret")
    api.transcription.init()
    store.set("signal", "a1", status="failed", error="HTTP 404")
    out = api.call_json("transcribe.diagnostics", {})
    assert out["provider"] == "openrouter"
    assert out["api_key_set"] is True
    assert out["service_active"] is True
    assert out["service_client"] == "OpenRouterTranscriptionClient"
    assert out["worker_alive"] is True
    assert out["recent"][0]["status"] == "failed"
    assert out["recent"][0]["error"] == "HTTP 404"
    assert "secretsecret" not in repr(out)


def test_check_without_key(api):
    out = api.call_json("transcribe.check", {})
    assert out == {"ok": False, "provider": "openai", "detail": "Chiave API mancante"}


def test_rest_routes(api):
    app = FastAPI()
    app.state.api = api
    install_auth(app, TOKEN)
    app.include_router(create_api_router())
    client = TestClient(app)
    logbuffer.install()
    logging.getLogger("transcription.test").info("via rest")
    response = client.get("/api/logs", headers=AUTH)
    assert response.status_code == 200
    assert "via rest" in [r["message"] for r in response.json()["records"]]
    assert client.get("/api/logs").status_code == 401
    diag = client.get("/api/transcribe/diagnostics", headers=AUTH)
    assert diag.status_code == 200
    assert diag.json()["provider"] == "openai"
    assert client.delete("/api/logs", headers=AUTH).status_code == 200
