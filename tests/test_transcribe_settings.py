"""``transcribe.settings`` / ``transcribe.set_settings`` and REST ``/api/transcribe/settings``."""

from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import pytest
from client_harness import api_for
from fastapi import FastAPI
from fastapi.testclient import TestClient

import protocols.config as pconfig
from server.api import ApiError
from web.api import create_api_router
from web.auth import install_auth

TOKEN = "settings-secret"
AUTH = {"Authorization": f"Bearer {TOKEN}"}
SECRET = "sk-super-secret-key"
ENVS = (
    "OPENAI_API_KEY",
    "TRANSCRIPTION_MODEL",
    "TRANSCRIPTION_LANGUAGE",
    "TRANSCRIPTION_BASE_URL",
    "TRANSCRIPTION_TIMEOUT",
)


@pytest.fixture(autouse=True)
def isolated_config(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    cfg = tmp_path / "cfg"
    cfg.mkdir()
    monkeypatch.setattr(pconfig, "PROJECT_DIR", cfg)
    for name in ENVS:
        monkeypatch.delenv(name, raising=False)
    # .env fallback of the key must not leak in from the real project
    import transcription.config as tconfig

    monkeypatch.setattr(tconfig, "_load_dotenv", dict)
    return cfg


@pytest.fixture
def api():
    instance = api_for(SimpleNamespace(get=lambda p: None))
    yield instance
    instance.transcription.stop()


def stored(cfg: Path) -> dict:
    return json.loads((cfg / "config.json").read_text())


def test_defaults(api):
    out = api.call_json("transcribe.settings", {})
    assert out == {
        "configured": False,
        "api_key_set": False,
        "model": "gpt-transcribe",
        "language": "",
        "base_url": "https://api.openai.com/v1",
        "timeout": 120,
        "env_overrides": [],
    }


def test_partial_update_and_reset(api, isolated_config):
    (isolated_config / "config.json").write_text(json.dumps({"other": 1}))
    out = api.call_json(
        "transcribe.set_settings",
        {"model": " whisper-1 ", "language": "it", "timeout": 30},
    )
    assert out["model"] == "whisper-1"
    assert out["language"] == "it"
    assert out["timeout"] == 30
    assert out["base_url"] == "https://api.openai.com/v1"
    assert stored(isolated_config) == {
        "other": 1,
        "transcription_model": "whisper-1",
        "transcription_language": "it",
        "transcription_timeout": 30,
    }

    # omitted = unchanged; empty language/base_url = default
    out = api.call_json(
        "transcribe.set_settings", {"base_url": "http://localhost:8000/v1/"}
    )
    assert out["base_url"] == "http://localhost:8000/v1"
    assert out["model"] == "whisper-1"
    out = api.call_json("transcribe.set_settings", {"language": "", "base_url": ""})
    assert out["language"] == ""
    assert out["base_url"] == "https://api.openai.com/v1"
    data = stored(isolated_config)
    assert "transcription_language" not in data
    assert "transcription_base_url" not in data
    assert data["other"] == 1 and data["transcription_model"] == "whisper-1"

    # empty request changes nothing
    assert api.call_json("transcribe.set_settings", {}) == api.call_json(
        "transcribe.settings", {}
    )


@pytest.mark.parametrize(
    "req",
    [
        {"model": ""},
        {"model": "   "},
        {"model": 5},
        {"model": None},
        {"model": "x" * 101},
        {"language": "IT"},
        {"language": "italiano"},
        {"language": "i"},
        {"language": "pt-br"},
        {"language": 3},
        {"base_url": "ftp://x"},
        {"base_url": "api.openai.com"},
        {"base_url": "https://"},
        {"base_url": "https://a b"},
        {"base_url": 4},
        {"timeout": 4},
        {"timeout": 601},
        {"timeout": "30"},
        {"timeout": 30.5},
        {"timeout": True},
        {"timeout": None},
    ],
)
def test_invalid_values(api, isolated_config, req):
    with pytest.raises(ApiError) as exc:
        api.call_json("transcribe.set_settings", {"model": "ok-model", **req})
    assert exc.value.code == "invalid_request"
    # nothing was saved, not even the valid field
    assert not (isolated_config / "config.json").exists()


def test_valid_bounds(api):
    assert api.call_json("transcribe.set_settings", {"timeout": 5})["timeout"] == 5
    assert api.call_json("transcribe.set_settings", {"timeout": 600})["timeout"] == 600
    assert (
        api.call_json("transcribe.set_settings", {"language": "fil"})["language"]
        == "fil"
    )
    assert (
        api.call_json("transcribe.set_settings", {"model": "m" * 100})["model"]
        == "m" * 100
    )


def test_env_overrides_reported(api, monkeypatch):
    monkeypatch.setenv("TRANSCRIPTION_MODEL", "env-model")
    monkeypatch.setenv("TRANSCRIPTION_TIMEOUT", "99")
    out = api.call_json("transcribe.set_settings", {"model": "saved", "language": "de"})
    assert out["env_overrides"] == ["model", "timeout"]
    assert out["model"] == "env-model"  # env still wins
    assert out["timeout"] == 99
    assert out["language"] == "de"
    assert api.call_json("transcribe.settings", {})["env_overrides"] == [
        "model",
        "timeout",
    ]


def test_never_returns_api_key(api, isolated_config, monkeypatch):
    (isolated_config / "config.json").write_text(json.dumps({"openai_api_key": SECRET}))
    api.transcription.init()
    for out in (
        api.call_json("transcribe.settings", {}),
        api.call_json("transcribe.set_settings", {"language": "en"}),
    ):
        assert out["api_key_set"] is True
        assert out["configured"] is True
        assert SECRET not in json.dumps(out)
    monkeypatch.setenv("OPENAI_API_KEY", SECRET + "-env")
    assert SECRET not in json.dumps(api.call_json("transcribe.settings", {}))


def test_reinit_applies_settings_and_keeps_on_done(api, isolated_config):
    (isolated_config / "config.json").write_text(json.dumps({"openai_api_key": SECRET}))
    calls = []
    api.transcription.on_done = lambda *a: calls.append(a)
    first = api.transcription.init()
    assert first is not None
    api.call_json("transcribe.set_settings", {"model": "whisper-1", "language": "fr"})
    second = api.transcription.service
    assert second is not None and second is not first
    client = second.client
    assert client.model == "whisper-1"
    assert client.language == "fr"
    # on_done still reaches the hook through the new service
    second._on_done("whatsapp", "a1", {"status": "ok"})
    assert calls == [("whatsapp", "a1", {"status": "ok"})]


@pytest.fixture
def client():
    app = FastAPI()
    app.state.api = api_for(SimpleNamespace(get=lambda p: None))
    install_auth(app, TOKEN)
    app.include_router(create_api_router())
    with TestClient(app) as test_client:
        test_client.api = app.state.api
        yield test_client
    app.state.api.transcription.stop()


def test_web_requires_auth(client):
    assert client.get("/api/transcribe/settings").status_code == 401
    assert client.put("/api/transcribe/settings", json={}).status_code == 401


def test_web_roundtrip_matches_operation(client):
    got = client.get("/api/transcribe/settings", headers=AUTH)
    assert got.status_code == 200
    assert got.json() == client.api.call_json("transcribe.settings", {})
    put = client.put(
        "/api/transcribe/settings",
        json={"model": "whisper-1", "language": "es", "timeout": 60, "junk": 1},
        headers=AUTH,
    )
    assert put.status_code == 200
    assert put.json()["model"] == "whisper-1"
    assert client.get("/api/transcribe/settings", headers=AUTH).json() == put.json()


def test_web_invalid_is_400(client):
    for body in ({"timeout": 1}, {"language": "xx1"}, {"base_url": "x"}, {"model": ""}):
        response = client.put("/api/transcribe/settings", json=body, headers=AUTH)
        assert response.status_code == 400


def test_web_route_does_not_shadow_status(client):
    # the two-segment status route still works next to /transcribe/settings
    response = client.get("/api/transcribe/whatsapp/abc.ogg", headers=AUTH)
    assert response.status_code != 404 or "settings" not in response.text
    assert client.get("/api/transcribe/config", headers=AUTH).json() == {
        "enabled": False
    }
