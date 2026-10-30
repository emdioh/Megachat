"""REST routes ``/api/autotranscribe*`` equal the ``autotranscribe.*`` ops."""

from __future__ import annotations

from pathlib import Path

import pytest
from client_harness import api_for
from fastapi import FastAPI
from fastapi.testclient import TestClient

import protocols.config as pconfig
from web.api import create_api_router
from web.auth import install_auth

TOKEN = "auto-secret"
AUTH = {"Authorization": f"Bearer {TOKEN}"}
CHAT = {"proto": "whatsapp", "contact_id": "39333@c.us"}


class Manager:
    def get(self, proto):
        return None


@pytest.fixture(autouse=True)
def isolated_config(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    cfg = tmp_path / "cfg"
    cfg.mkdir()
    monkeypatch.setattr(pconfig, "PROJECT_DIR", cfg)
    return cfg


@pytest.fixture
def client():
    app = FastAPI()
    app.state.api = api_for(Manager())
    install_auth(app, TOKEN)
    app.include_router(create_api_router())
    with TestClient(app) as test_client:
        test_client.api = app.state.api
        yield test_client


def test_requires_auth(client):
    assert client.get("/api/autotranscribe").status_code == 401
    assert client.get("/api/autotranscribe/chat", params=CHAT).status_code == 401
    assert (
        client.put(
            "/api/autotranscribe/global/signal", json={"enabled": True}
        ).status_code
        == 401
    )


def test_config_matches_operation(client):
    response = client.get("/api/autotranscribe", headers=AUTH)
    assert response.status_code == 200
    assert response.json() == client.api.call_json("autotranscribe.config")
    assert response.json()["configured"] is False
    assert set(response.json()["globals"]) == {"whatsapp", "signal", "telegram"}


def test_set_global_roundtrip(client):
    response = client.put(
        "/api/autotranscribe/global/telegram", json={"enabled": True}, headers=AUTH
    )
    assert response.status_code == 200
    assert response.json()["telegram"] is True
    config = client.get("/api/autotranscribe", headers=AUTH).json()
    assert config["globals"]["telegram"] is True
    assert config["globals"]["signal"] is False


def test_set_global_invalid_protocol_and_value(client):
    assert (
        client.put(
            "/api/autotranscribe/global/gmail", json={"enabled": True}, headers=AUTH
        ).status_code
        == 422
    )
    assert (
        client.put(
            "/api/autotranscribe/global/signal", json={"enabled": "yes"}, headers=AUTH
        ).status_code
        == 400
    )


def test_chat_override_set_and_clear(client):
    url = "/api/autotranscribe/chat"
    base = client.get(url, params=CHAT, headers=AUTH).json()
    assert base == {"global": False, "override": None, "effective": False}

    on = client.put(url, params=CHAT, json={"enabled": True}, headers=AUTH)
    assert on.json() == {"global": False, "override": True, "effective": True}
    assert client.get(url, params=CHAT, headers=AUTH).json() == on.json()
    assert on.json() == client.api.call_json(
        "autotranscribe.chat", {"protocol": "whatsapp", "contact_id": "39333@c.us"}
    )

    off = client.put(url, params=CHAT, json={"enabled": False}, headers=AUTH)
    assert off.json() == {"global": False, "override": False, "effective": False}

    client.put(
        "/api/autotranscribe/global/whatsapp", json={"enabled": True}, headers=AUTH
    )
    cleared = client.put(url, params=CHAT, json={"enabled": None}, headers=AUTH)
    assert cleared.json() == {"global": True, "override": None, "effective": True}


def test_chat_contact_id_with_special_chars(client):
    params = {"proto": "signal", "contact_id": "+39 333/4&x=1"}
    url = "/api/autotranscribe/chat"
    client.put(url, params=params, json={"enabled": True}, headers=AUTH)
    assert client.get(url, params=params, headers=AUTH).json()["override"] is True
    other = {"proto": "signal", "contact_id": "+39"}
    assert client.get(url, params=other, headers=AUTH).json()["override"] is None


def test_chat_invalid_requests(client):
    url = "/api/autotranscribe/chat"
    assert (
        client.get(
            url, params={"proto": "gmail", "contact_id": "a"}, headers=AUTH
        ).status_code
        == 422
    )
    assert client.get(url, params={"proto": "signal"}, headers=AUTH).status_code == 422
    assert (
        client.get(
            url, params={"proto": "signal", "contact_id": ""}, headers=AUTH
        ).status_code
        == 400
    )
    assert (
        client.put(url, params=CHAT, json={"enabled": "x"}, headers=AUTH).status_code
        == 400
    )
    # A missing key must not silently clear the override.
    client.put(url, params=CHAT, json={"enabled": True}, headers=AUTH)
    assert client.put(url, params=CHAT, json={}, headers=AUTH).status_code == 422
    assert client.get(url, params=CHAT, headers=AUTH).json()["override"] is True


def test_static_ui_wiring():
    """The SPA ships the settings section and the per-chat select."""
    static = Path(__file__).resolve().parent.parent / "web" / "static"
    html = (static / "index.html").read_text(encoding="utf-8")
    app = (static / "app.js").read_text(encoding="utf-8")
    for element_id in (
        "autotranscribe-toggles",
        "autotranscribe-button",
        "autotranscribe-menu",
        "autotranscribe-caption",
        "autotranscribe-control",
    ):
        assert f'id="{element_id}"' in html
    settings = html.split('id="settings-dialog"')[1]
    assert 'id="autotranscribe-toggles"' in settings
    assert 'id="autotranscribe-toggles"' not in html.split('id="settings-dialog"')[0].split('id="link-dialog"')[1]
    # Loaded when Impostazioni opens, not when the token dialog opens.
    opener = app.split('querySelector("#open-settings")')[1][:500]
    assert "loadAutoTranscribeConfig()" in opener
    assert "loadTranscriptionSettings()" in opener
    for option in ("Attiva", "Disattiva", "Predefinito"):
        assert f">{option}" in html
    # Icon button + popup menu (radio-like options), not a <select>.
    assert "autotranscribe-select" not in html + app
    assert 'aria-haspopup="menu"' in html.split('id="autotranscribe-button"')[1][:400]
    assert html.count('role="menuitemradio"') == 3
    assert "closeAutoTranscribeMenu" in app and 'event.key !== "Escape"' in app
    assert "api/autotranscribe/global/" in app
    assert "api/autotranscribe/chat?" in app
    # Changing the setting must never touch the read state.
    start = app.index("// --- Trascrizione automatica")
    end = app.index("function enableOpenaiKeyEdit")
    assert "mark_read" not in app[start:end] and "markRead" not in app[start:end]


def test_static_wiring_for_transcription_settings():
    index = Path("web/static/index.html").read_text(encoding="utf-8")
    app = Path("web/static/app.js").read_text(encoding="utf-8")
    for element_id in (
        "transcription-settings",
        "transcription-model",
        "transcription-language",
        "transcription-base-url",
        "transcription-timeout",
        "save-transcription-settings",
        "transcription-settings-status",
    ):
        assert f'id="{element_id}"' in index
        assert f"#{element_id}" in app
    assert index.index("openai-key-input") < index.index("transcription-model")
    assert index.index("transcription-model") < index.index("autotranscribe-toggles")
    assert "api/transcribe/settings" in app
    assert "impostato da variabile d'ambiente" in app
