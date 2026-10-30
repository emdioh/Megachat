"""Transcription providers: config, clients, factory, ops and REST."""

from __future__ import annotations

import base64
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from client_harness import api_for
from fastapi import FastAPI
from fastapi.testclient import TestClient

import protocols.config as pconfig
import transcription.config as tconfig
from server.api import ApiError
from transcription.client import (
    CloudTranscriptionClient,
    GeminiTranscriptionClient,
    OpenRouterTranscriptionClient,
    TranscriptionError,
    build_client,
)
from web.api import create_api_router
from web.auth import install_auth

TOKEN = "providers-secret"
AUTH = {"Authorization": f"Bearer {TOKEN}"}
ENVS = (
    "AI_PROVIDER",
    "OPENAI_API_KEY",
    "OPENAI_TRANSCRIPTION_MODEL",
    "TRANSCRIPTION_MODEL",
    "TRANSCRIPTION_BASE_URL",
    "TRANSCRIPTION_LANGUAGE",
    "TRANSCRIPTION_TIMEOUT",
    "OPENROUTER_API_KEY",
    "OPENROUTER_BASE_URL",
    "OPENROUTER_TRANSCRIPTION_MODEL",
    "OPENROUTER_HTTP_REFERER",
    "OPENROUTER_APP_TITLE",
    "GEMINI_API_KEY",
    "GOOGLE_API_KEY",
    "GEMINI_BASE_URL",
    "GEMINI_TRANSCRIPTION_MODEL",
)
GEMINI_KEY = "AIza" + "x" * 30


@pytest.fixture(autouse=True)
def isolated_config(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    cfg = tmp_path / "cfg"
    cfg.mkdir()
    monkeypatch.setattr(pconfig, "PROJECT_DIR", cfg)
    for name in ENVS:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setattr(tconfig, "_load_dotenv", dict)
    return cfg


def write_cfg(cfg: Path, data: dict) -> None:
    (cfg / "config.json").write_text(json.dumps(data))


def stored(cfg: Path) -> dict:
    return json.loads((cfg / "config.json").read_text())


# -- config ---------------------------------------------------------------------


def test_provider_default_and_invalid_fall_back(isolated_config, monkeypatch):
    assert tconfig.get_transcription_provider() == "openai"
    write_cfg(isolated_config, {"ai_provider": "gemini"})
    assert tconfig.get_transcription_provider() == "gemini"
    monkeypatch.setenv("AI_PROVIDER", "OpenRouter")
    assert tconfig.get_transcription_provider() == "openrouter"
    monkeypatch.setenv("AI_PROVIDER", "deepseek")
    assert tconfig.get_transcription_provider() == "openai"


def test_api_keys_per_provider_with_precedence(isolated_config, monkeypatch):
    write_cfg(
        isolated_config,
        {"openai_api_key": "sk-a", "openrouter_api_key": "sk-or-a"},
    )
    assert tconfig.get_api_key("openai") == "sk-a"
    assert tconfig.get_api_key("openrouter") == "sk-or-a"
    assert tconfig.get_api_key("gemini") is None
    monkeypatch.setenv("OPENROUTER_API_KEY", "sk-or-env")
    assert tconfig.get_api_key("openrouter") == "sk-or-env"
    monkeypatch.setenv("GOOGLE_API_KEY", "google")
    assert tconfig.get_api_key("gemini") == "google"
    monkeypatch.setenv("GEMINI_API_KEY", "gem")
    assert tconfig.get_api_key("gemini") == "gem"
    # enabled = the selected provider has a key
    assert tconfig.get_transcription_enabled() is True
    monkeypatch.setenv("AI_PROVIDER", "gemini")
    monkeypatch.delenv("GEMINI_API_KEY")
    monkeypatch.delenv("GOOGLE_API_KEY")
    assert tconfig.get_transcription_enabled() is False


def test_models_and_urls_defaults_and_precedence(isolated_config, monkeypatch):
    assert tconfig.get_provider_model("openai") == "gpt-transcribe"
    assert tconfig.get_provider_model("openrouter") == "openai/whisper-large-v3"
    assert tconfig.get_provider_model("gemini") == "gemini-flash-latest"
    assert tconfig.get_provider_base_url("openrouter") == "https://openrouter.ai/api/v1"
    # legacy keys keep meaning OpenAI
    write_cfg(
        isolated_config,
        {"transcription_model": "legacy", "transcription_base_url": "http://x/v1/"},
    )
    assert tconfig.get_provider_model("openai") == "legacy"
    assert tconfig.get_provider_base_url("openai") == "http://x/v1"
    write_cfg(
        isolated_config,
        {"transcription_model": "legacy", "openai_transcription_model": "new"},
    )
    assert tconfig.get_provider_model("openai") == "new"
    monkeypatch.setenv("TRANSCRIPTION_MODEL", "env-legacy")
    assert tconfig.get_provider_model("openai") == "env-legacy"
    monkeypatch.setenv("OPENAI_TRANSCRIPTION_MODEL", "env-new")
    assert tconfig.get_provider_model("openai") == "env-new"
    monkeypatch.setenv("GEMINI_TRANSCRIPTION_MODEL", "gemini-x")
    monkeypatch.setenv("GEMINI_BASE_URL", "http://g/")
    assert tconfig.get_provider_model("gemini") == "gemini-x"
    assert tconfig.get_provider_base_url("gemini") == "http://g"


def test_openrouter_headers_only_when_set(isolated_config, monkeypatch):
    assert tconfig.get_openrouter_headers() == {}
    write_cfg(isolated_config, {"openrouter_app_title": "Megachat"})
    assert tconfig.get_openrouter_headers() == {"X-OpenRouter-Title": "Megachat"}
    monkeypatch.setenv("OPENROUTER_HTTP_REFERER", "https://me.test")
    assert tconfig.get_openrouter_headers() == {
        "HTTP-Referer": "https://me.test",
        "X-OpenRouter-Title": "Megachat",
    }


def test_set_settings_removes_legacy_key_on_reset(isolated_config):
    write_cfg(isolated_config, {"transcription_model": "legacy"})
    tconfig.set_transcription_settings({"openai.model": "new", "provider": "gemini"})
    assert stored(isolated_config) == {
        "transcription_model": "legacy",
        "openai_transcription_model": "new",
        "ai_provider": "gemini",
    }
    tconfig.set_transcription_settings({"openai.model": None})
    assert stored(isolated_config) == {"ai_provider": "gemini"}


# -- clients --------------------------------------------------------------------


def _aiohttp_mocks(*, status=200, json_data=None, body=""):
    response = MagicMock(status=status)
    response.json = AsyncMock(return_value=json_data if json_data is not None else {})
    response.text = AsyncMock(return_value=body)
    response_context = MagicMock()
    response_context.__aenter__ = AsyncMock(return_value=response)
    response_context.__aexit__ = AsyncMock(return_value=None)
    session = MagicMock()
    session.post.return_value = response_context
    session_context = MagicMock()
    session_context.__aenter__ = AsyncMock(return_value=session)
    session_context.__aexit__ = AsyncMock(return_value=None)
    return session, session_context


class RecordingFormData:
    def __init__(self) -> None:
        self.fields = []

    def add_field(self, name, value, **kwargs) -> None:
        self.fields.append((name, value, kwargs))


@pytest.mark.asyncio
async def test_openrouter_multipart_request(tmp_path):
    audio = tmp_path / "v.mp3"
    audio.write_bytes(b"a")
    form = RecordingFormData()
    session, ctx = _aiohttp_mocks(json_data={"text": " ciao "})
    client = OpenRouterTranscriptionClient(
        "sk-or-k",
        base_url="https://or.test/api/v1/",
        language="it",
        extra_headers={"HTTP-Referer": "https://me.test"},
    )
    with (
        patch("transcription.client.aiohttp.FormData", return_value=form),
        patch("transcription.client.aiohttp.ClientSession", return_value=ctx),
    ):
        assert await client.transcribe_async(audio) == "ciao"
    fields = {n: v for n, v, _k in form.fields}
    assert fields["model"] == "openai/whisper-large-v3"
    assert fields["language"] == "it"  # never "languages"
    assert (
        session.post.call_args.args[0] == "https://or.test/api/v1/audio/transcriptions"
    )
    assert session.post.call_args.kwargs["headers"] == {
        "Authorization": "Bearer sk-or-k",
        "HTTP-Referer": "https://me.test",
    }


@pytest.mark.asyncio
async def test_openai_headers_have_no_extras(tmp_path):
    audio = tmp_path / "v.mp3"
    audio.write_bytes(b"a")
    session, ctx = _aiohttp_mocks(json_data={"text": "x"})
    with patch("transcription.client.aiohttp.ClientSession", return_value=ctx):
        await CloudTranscriptionClient("sk-1").transcribe_async(audio)
    assert session.post.call_args.kwargs["headers"] == {"Authorization": "Bearer sk-1"}


@pytest.mark.asyncio
async def test_gemini_request_shape(tmp_path):
    audio = tmp_path / "v.oga"
    audio.write_bytes(b"opus-bytes")
    session, ctx = _aiohttp_mocks(
        json_data={
            "candidates": [
                {"content": {"parts": [{"text": " Ciao "}, {"text": "mondo\n"}]}}
            ]
        }
    )
    client = GeminiTranscriptionClient(
        GEMINI_KEY, base_url="https://g.test/v1beta/", model="gemini-x", language="it"
    )
    with patch("transcription.client.aiohttp.ClientSession", return_value=ctx):
        assert await client.transcribe_async(audio) == "Ciao mondo"
    assert session.post.call_args.args[0] == (
        "https://g.test/v1beta/models/gemini-x:generateContent"
    )
    kwargs = session.post.call_args.kwargs
    assert kwargs["headers"] == {"x-goog-api-key": GEMINI_KEY}
    body = kwargs["json"]
    assert body["generationConfig"] == {"temperature": 0}
    prompt, media = body["contents"][0]["parts"]
    assert "verbatim" in prompt["text"] and "'it'" in prompt["text"]
    assert media["inline_data"]["mime_type"] == "audio/ogg"
    assert base64.b64decode(media["inline_data"]["data"]) == b"opus-bytes"


@pytest.mark.asyncio
async def test_gemini_unsupported_format_is_transcoded(tmp_path):
    audio = tmp_path / "v.m4a"
    audio.write_bytes(b"m4a")
    mp3 = tmp_path / "out.mp3"
    mp3.write_bytes(b"mp3-bytes")
    session, ctx = _aiohttp_mocks(
        json_data={"candidates": [{"content": {"parts": [{"text": "x"}]}}]}
    )
    client = GeminiTranscriptionClient(GEMINI_KEY)
    with (
        patch("transcription.client._transcode_to_mp3", return_value=mp3) as transcode,
        patch("transcription.client.aiohttp.ClientSession", return_value=ctx),
    ):
        assert await client.transcribe_async(audio) == "x"
    transcode.assert_called_once()
    part = session.post.call_args.kwargs["json"]["contents"][0]["parts"][1]
    assert part["inline_data"]["mime_type"] == "audio/mp3"
    assert not mp3.exists()


@pytest.mark.asyncio
async def test_gemini_rejects_large_audio(tmp_path):
    audio = tmp_path / "big.mp3"
    with audio.open("wb") as stream:
        stream.truncate(16 * 1024 * 1024)
    with pytest.raises(TranscriptionError, match="troppo grande"):
        await GeminiTranscriptionClient(GEMINI_KEY).transcribe_async(audio)


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("payload", "expected"),
    [
        ({"candidates": [{"content": {"parts": []}}]}, ""),
        ({"candidates": [{"content": {}, "finishReason": "STOP"}]}, ""),
        ({"candidates": []}, ""),
        ({}, ""),
    ],
)
async def test_gemini_empty_responses(tmp_path, payload, expected):
    audio = tmp_path / "v.wav"
    audio.write_bytes(b"a")
    _s, ctx = _aiohttp_mocks(json_data=payload)
    with patch("transcription.client.aiohttp.ClientSession", return_value=ctx):
        assert (
            await GeminiTranscriptionClient(GEMINI_KEY).transcribe_async(audio)
            == expected
        )


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "payload",
    [
        {"promptFeedback": {"blockReason": "SAFETY"}},
        {"candidates": [{"finishReason": "SAFETY"}]},
    ],
)
async def test_gemini_blocked(tmp_path, payload):
    audio = tmp_path / "v.wav"
    audio.write_bytes(b"a")
    _s, ctx = _aiohttp_mocks(json_data=payload)
    with (
        patch("transcription.client.aiohttp.ClientSession", return_value=ctx),
        pytest.raises(TranscriptionError, match="bloccat"),
    ):
        await GeminiTranscriptionClient(GEMINI_KEY).transcribe_async(audio)


@pytest.mark.asyncio
async def test_gemini_http_error_never_leaks_key(tmp_path):
    audio = tmp_path / "v.wav"
    audio.write_bytes(b"a")
    _s, ctx = _aiohttp_mocks(status=400, body=f"bad key {GEMINI_KEY}")
    with (
        patch("transcription.client.aiohttp.ClientSession", return_value=ctx),
        pytest.raises(TranscriptionError) as exc,
    ):
        await GeminiTranscriptionClient(GEMINI_KEY).transcribe_async(audio)
    assert "HTTP 400" in str(exc.value) and GEMINI_KEY not in str(exc.value)


def test_build_client_selects_class():
    kw = {"base_url": "http://x", "model": "m"}
    assert type(build_client("openai", "k", **kw)) is CloudTranscriptionClient
    assert isinstance(
        build_client("openrouter", "k", **kw), OpenRouterTranscriptionClient
    )
    assert isinstance(build_client("gemini", "k", **kw), GeminiTranscriptionClient)
    with pytest.raises(ValueError):
        build_client("deepseek", "k", **kw)


# -- ops / REST -----------------------------------------------------------------


@pytest.fixture
def api():
    instance = api_for(SimpleNamespace(get=lambda p: None))
    yield instance
    instance.transcription.stop()


def test_set_api_key_validation(api, isolated_config):
    bad = [
        ("openai", "pk-1"),
        ("openai", ""),
        ("openrouter", "sk-abc"),
        ("gemini", "short"),
        ("gemini", "A" * 10 + " " + "B" * 20),
        ("openai", 5),
        ("deepseek", "sk-1"),
        (None, "sk-1"),
    ]
    for provider, key in bad:
        with pytest.raises(ApiError) as exc:
            api.call_json(
                "transcribe.set_api_key", {"provider": provider, "api_key": key}
            )
        assert exc.value.code == "invalid_request"
    assert not (isolated_config / "config.json").exists()


def test_set_api_key_saves_and_activates_selected_provider(api, isolated_config):
    api.call_json(
        "transcribe.set_api_key", {"provider": "gemini", "api_key": GEMINI_KEY}
    )
    api.call_json(
        "transcribe.set_api_key", {"provider": "openrouter", "api_key": "sk-or-1"}
    )
    data = stored(isolated_config)
    assert data == {"gemini_api_key": GEMINI_KEY, "openrouter_api_key": "sk-or-1"}
    # selected provider is still openai: no key -> no service
    out = api.call_json("transcribe.settings", {})
    assert out["configured"] is False and out["api_key_set"] is False
    assert out["providers"]["gemini"]["api_key_set"] is True
    out = api.call_json("transcribe.set_settings", {"provider": "gemini"})
    assert out["configured"] is True and out["provider"] == "gemini"
    assert isinstance(api.transcription.service.client, GeminiTranscriptionClient)
    assert out["model"] == "gemini-flash-latest"
    dumped = json.dumps(out)
    assert GEMINI_KEY not in dumped and "sk-or-1" not in dumped


def test_legacy_openai_key_op_delegates(api, isolated_config):
    api.call_json("config.set_openai_key", {"api_key": "sk-legacy"})
    assert stored(isolated_config) == {"openai_api_key": "sk-legacy"}
    assert isinstance(api.transcription.service.client, CloudTranscriptionClient)
    with pytest.raises(ApiError, match="Chiave non valida"):
        api.call_json("config.set_openai_key", {"api_key": "nope"})


def test_provider_switch_reinits_and_keeps_on_done(api, isolated_config):
    write_cfg(
        isolated_config, {"openai_api_key": "sk-1", "openrouter_api_key": "sk-or-1"}
    )
    hook = MagicMock()
    api.transcription.on_done = hook
    api.transcription.init()
    first = api.transcription.service
    assert isinstance(first.client, CloudTranscriptionClient)
    api.call_json(
        "transcribe.set_settings",
        {
            "provider": "openrouter",
            "providers": {
                "openrouter": {"http_referer": "https://me.test", "app_title": "App"}
            },
        },
    )
    second = api.transcription.service
    assert second is not first
    assert isinstance(second.client, OpenRouterTranscriptionClient)
    assert second.client.extra_headers == {
        "HTTP-Referer": "https://me.test",
        "X-OpenRouter-Title": "App",
    }
    second._on_done("whatsapp", "a", {"status": "ok"})
    hook.assert_called_once_with("whatsapp", "a", {"status": "ok"})


def test_provider_settings_and_validation(api, isolated_config):
    out = api.call_json(
        "transcribe.set_settings",
        {
            "providers": {
                "gemini": {
                    "model": "models/gemini-2.5-flash",
                    "base_url": "http://g/v1beta/",
                },
                "openrouter": {"model": "openai/whisper-1"},
            }
        },
    )
    assert out["providers"]["gemini"]["model"] == "gemini-2.5-flash"
    assert out["providers"]["gemini"]["base_url"] == "http://g/v1beta"
    assert out["providers"]["openrouter"]["model"] == "openai/whisper-1"
    assert out["provider"] == "openai"  # unchanged
    data = stored(isolated_config)
    assert data["gemini_transcription_model"] == "gemini-2.5-flash"
    assert data["openrouter_transcription_model"] == "openai/whisper-1"
    # top-level model goes to the provider of the same request
    out = api.call_json(
        "transcribe.set_settings", {"provider": "gemini", "model": "gemini-pro"}
    )
    assert out["model"] == "gemini-pro"
    # empty referer/title reset
    api.call_json(
        "transcribe.set_settings", {"providers": {"openrouter": {"app_title": "T"}}}
    )
    out = api.call_json(
        "transcribe.set_settings", {"providers": {"openrouter": {"app_title": ""}}}
    )
    assert out["providers"]["openrouter"]["app_title"] == ""
    assert "openrouter_app_title" not in stored(isolated_config)


@pytest.mark.parametrize(
    "req",
    [
        {"provider": "deepseek"},
        {"provider": 3},
        {"providers": "x"},
        {"providers": {"deepseek": {}}},
        {"providers": {"gemini": {"model": "Gemini Flash"}}},
        {"providers": {"gemini": {"model": "gemini_flash"}}},
        {"providers": {"openrouter": {"model": "whisper"}}},
        {"providers": {"openrouter": {"http_referer": "ftp://x"}}},
        {"providers": {"openrouter": {"http_referer": 4}}},
        {"providers": {"openrouter": {"app_title": "a\nb"}}},
        {"providers": {"openrouter": {"app_title": "x" * 101}}},
        {"providers": {"gemini": {"base_url": "nope"}}},
    ],
)
def test_provider_invalid_values_save_nothing(api, isolated_config, req):
    with pytest.raises(ApiError) as exc:
        api.call_json("transcribe.set_settings", {"language": "it", **req})
    assert exc.value.code == "invalid_request"
    assert not (isolated_config / "config.json").exists()


def test_env_overrides_per_provider(api, monkeypatch):
    monkeypatch.setenv("AI_PROVIDER", "gemini")
    monkeypatch.setenv("OPENROUTER_TRANSCRIPTION_MODEL", "a/b")
    monkeypatch.setenv("OPENROUTER_APP_TITLE", "t")
    out = api.call_json("transcribe.settings", {})
    assert out["env_overrides"] == [
        "provider",
        "openrouter.model",
        "openrouter.app_title",
    ]
    assert out["provider"] == "gemini"


def test_unconfigured_follows_selected_provider(api, isolated_config):
    write_cfg(isolated_config, {"openai_api_key": "sk-1", "ai_provider": "gemini"})
    api.transcription.init()
    assert api.transcription.service is None
    assert api.call_json("transcribe.config", {}) == {"enabled": False}
    assert api.call_json("autotranscribe.config", {})["configured"] is False


def test_rest_endpoints(api, isolated_config):
    app = FastAPI()
    app.state.api = api
    install_auth(app, TOKEN)
    app.include_router(create_api_router())
    with TestClient(app) as client:
        _rest_checks(client)


def _rest_checks(client):
    response = client.put(
        "/api/transcribe/api-key",
        json={"provider": "gemini", "api_key": GEMINI_KEY},
        headers=AUTH,
    )
    assert (response.status_code, response.json()) == (200, {"status": "ok"})
    response = client.put(
        "/api/transcribe/api-key",
        json={"provider": "gemini", "api_key": "x"},
        headers=AUTH,
    )
    assert response.status_code == 400
    response = client.put(
        "/api/transcribe/settings",
        json={
            "provider": "gemini",
            "providers": {"gemini": {"model": "gemini-x"}},
            "junk": 1,
        },
        headers=AUTH,
    )
    body = response.json()
    assert response.status_code == 200 and body["configured"] is True
    assert body["providers"]["gemini"]["model"] == "gemini-x"
    assert GEMINI_KEY not in response.text
    assert client.get("/api/transcribe/api-key", headers=AUTH).status_code in (404, 405)
