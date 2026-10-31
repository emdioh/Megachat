"""Voice-note transcription operations and the OpenAI key setting.

The service is core state (:class:`server.transcribe.TranscriptionHost`,
``api.transcription``).  Messages are the Italian texts the SPA has always
shown.
"""

from __future__ import annotations

import logging
import re
import sqlite3
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from server.api import ApiError, ChatAPI, operation
from server.models_api import (
    AutoTranscribeChatRequest,
    AutoTranscribeSetChatRequest,
    AutoTranscribeSetGlobalRequest,
    EmptyRequest,
    OpenAIKeyRequest,
    StatusOut,
    TranscribeApiKeyRequest,
    TranscribeConfigOut,
    TranscribeSettingsRequest,
    TranscribeStatusRequest,
    TranscribeSubmitRequest,
)
from server.ops.common import PROTOCOLS

logger = logging.getLogger(__name__)

_UNCONFIGURED = "Trascrizione non configurata"


def _service(api: ChatAPI) -> Any:
    service = api.transcription.service
    if service is None:
        raise ApiError("backend_down", _UNCONFIGURED)
    return service


@operation("transcribe.config", request=EmptyRequest, response=TranscribeConfigOut)
def transcribe_config(api: ChatAPI, req: EmptyRequest) -> TranscribeConfigOut:
    """Whether transcription is available (the selected provider has an API key)."""
    return TranscribeConfigOut(enabled=api.transcription.service is not None)


@operation(
    "transcribe.status",
    request=TranscribeStatusRequest,
    response=dict[str, Any],
)
def transcribe_status(api: ChatAPI, req: TranscribeStatusRequest) -> dict[str, Any]:
    """Stored transcription record of an attachment, or ``{"status": "unknown"}``.

    ``backend_down`` when transcription is not configured.
    """
    current = _service(api).status(req.protocol, req.attachment_id)
    return current if current is not None else {"status": "unknown"}


@operation(
    "transcribe.submit", request=TranscribeSubmitRequest, response=dict[str, Any]
)
def transcribe_submit(api: ChatAPI, req: TranscribeSubmitRequest) -> dict[str, Any]:
    """Queue an attachment for transcription (or report its current state).

    The audio file is resolved through ``media.attachment``, so it is confined
    to the protocol's media root.  Sync: may trigger a lazy download.
    """
    proto = req.protocol
    attachment_id = req.attachment_id
    if (
        not isinstance(proto, str)
        or proto not in PROTOCOLS
        or not isinstance(attachment_id, str)
    ):
        raise ApiError("invalid_request", "Invalid request")
    attachment_id = attachment_id.strip()
    if not attachment_id:
        raise ApiError("invalid_request", "Invalid request")

    service = _service(api)
    current = service.status(proto, attachment_id)
    if current is not None and current["status"] == "ok":
        return {"status": "done", "text": current.get("text", "")}
    if current is not None and current["status"] == "pending":
        return {"status": "pending"}

    try:
        blob = api.call_json(
            "media.attachment", {"protocol": proto, "attachment_id": attachment_id}
        )
    except ApiError:
        raise ApiError("not_found", "Allegato non trovato") from None
    service.submit(proto, attachment_id, Path(blob["path"]))
    return {"status": "pending"}


def _reinit(api: ChatAPI) -> None:
    try:
        api.transcription.init()
    except Exception:
        logger.exception("Impossibile inizializzare la trascrizione")
        raise ApiError("internal", "Errore di inizializzazione") from None


_KEY_ERROR = "Chiave non valida"


def _validated_api_key(provider: Any, api_key: Any) -> tuple[str, str]:
    from transcription.config import PROVIDERS

    if not isinstance(provider, str) or provider not in PROVIDERS:
        raise ApiError("invalid_request", "Provider non valido")
    if not isinstance(api_key, str):
        raise ApiError("invalid_request", _KEY_ERROR)
    api_key = api_key.strip()
    if not api_key or any(c.isspace() for c in api_key) or len(api_key) > 500:
        raise ApiError("invalid_request", _KEY_ERROR)
    if provider == "openai" and not api_key.startswith("sk-"):
        raise ApiError("invalid_request", _KEY_ERROR)
    if provider == "openrouter" and not api_key.startswith("sk-or-"):
        raise ApiError("invalid_request", _KEY_ERROR)
    if provider == "gemini" and len(api_key) < 20:
        raise ApiError("invalid_request", _KEY_ERROR)
    return provider, api_key


@operation(
    "transcribe.set_api_key", request=TranscribeApiKeyRequest, response=StatusOut
)
def transcribe_set_api_key(api: ChatAPI, req: TranscribeApiKeyRequest) -> StatusOut:
    """Save a provider's API key to ``config.json`` and rebuild the service."""
    from transcription.config import set_api_key

    provider, api_key = _validated_api_key(req.provider, req.api_key)
    _save(set_api_key, provider, api_key)
    _reinit(api)
    return StatusOut(status="ok")


@operation("config.set_openai_key", request=OpenAIKeyRequest, response=StatusOut)
def config_set_openai_key(api: ChatAPI, req: OpenAIKeyRequest) -> StatusOut:
    """Save the OpenAI key (``transcribe.set_api_key`` for OpenAI)."""
    return transcribe_set_api_key(
        api, TranscribeApiKeyRequest(provider="openai", api_key=req.api_key)
    )


# -- automatic transcription settings (shared with the "!" chat commands) --------


def _auto_globals() -> dict[str, bool]:
    from transcription.config import get_auto_transcribe_globals

    return get_auto_transcribe_globals()


def _auto_chat(protocol: str, contact_id: str) -> dict[str, Any]:
    from transcription import config as tconfig

    return {
        "global": tconfig.get_auto_transcribe_global(protocol),
        "override": tconfig.get_auto_transcribe_chat(protocol, contact_id),
        "effective": tconfig.auto_transcribe_effective(protocol, contact_id),
    }


def _save(action: Any, *args: Any) -> None:
    try:
        action(*args)
    except OSError:
        logger.exception("Impossibile salvare la configurazione di trascrizione")
        raise ApiError("internal", "Errore di salvataggio") from None


@operation("autotranscribe.config", request=EmptyRequest, response=dict[str, Any])
def autotranscribe_config(api: ChatAPI, req: EmptyRequest) -> dict[str, Any]:
    """Per-protocol global switches and whether transcription is configured."""
    return {
        "configured": api.transcription.service is not None,
        "globals": _auto_globals(),
    }


@operation(
    "autotranscribe.set_global",
    request=AutoTranscribeSetGlobalRequest,
    response=dict[str, bool],
)
def autotranscribe_set_global(
    api: ChatAPI, req: AutoTranscribeSetGlobalRequest
) -> dict[str, bool]:
    """Set one protocol's global switch; returns all globals."""
    from transcription.config import set_auto_transcribe_global

    _save(set_auto_transcribe_global, req.protocol, req.enabled)
    return _auto_globals()


@operation(
    "autotranscribe.chat",
    request=AutoTranscribeChatRequest,
    response=dict[str, Any],
)
def autotranscribe_chat(api: ChatAPI, req: AutoTranscribeChatRequest) -> dict[str, Any]:
    """``{global, override, effective}`` of a chat (``override`` may be null)."""
    return _auto_chat(req.protocol, req.contact_id)


@operation(
    "autotranscribe.set_chat",
    request=AutoTranscribeSetChatRequest,
    response=dict[str, Any],
)
def autotranscribe_set_chat(
    api: ChatAPI, req: AutoTranscribeSetChatRequest
) -> dict[str, Any]:
    """Set (or, with ``enabled=null``, clear) a chat's override."""
    from transcription.config import set_auto_transcribe_chat

    _save(set_auto_transcribe_chat, req.protocol, req.contact_id, req.enabled)
    return _auto_chat(req.protocol, req.contact_id)


# -- transcription service settings (model, language, endpoint, timeout) --------

_LANGUAGE_RE = re.compile(r"[a-z]{2,3}")
_MODEL_MAX = 100
_URL_MAX = 300
_TIMEOUT_RANGE = (5, 600)


def _settings_out(api: ChatAPI) -> dict[str, Any]:
    """Effective settings.  Never contains an API key itself."""
    from transcription import config as tconfig

    provider = tconfig.get_transcription_provider()
    providers = {
        name: {
            "api_key_set": tconfig.get_api_key(name) is not None,
            "model": tconfig.get_provider_model(name),
            "base_url": tconfig.get_provider_base_url(name),
        }
        for name in tconfig.PROVIDERS
    }
    providers["openrouter"]["http_referer"] = tconfig._provider_setting(
        "openrouter", "http_referer"
    )
    providers["openrouter"]["app_title"] = tconfig._provider_setting(
        "openrouter", "app_title"
    )
    return {
        "configured": api.transcription.service is not None,
        "provider": provider,
        "providers": providers,
        "api_key_set": providers[provider]["api_key_set"],
        "model": providers[provider]["model"],
        "language": tconfig.get_transcription_language() or "",
        "base_url": providers[provider]["base_url"],
        "timeout": tconfig.get_transcription_timeout(),
        "env_overrides": tconfig.transcription_env_overrides(),
    }


def _invalid(message: str) -> ApiError:
    return ApiError("invalid_request", message)


def _valid_url(value: str) -> bool:
    try:
        parsed = urlparse(value)
        return (
            parsed.scheme in ("http", "https")
            and bool(parsed.hostname)
            and not any(c.isspace() for c in value)
            and len(value) <= _URL_MAX
        )
    except ValueError:
        return False


_GEMINI_MODEL_RE = re.compile(r"[a-z0-9.-]+")


def _validated_model(provider: str, model: Any) -> str:
    if not isinstance(model, str) or not model.strip():
        raise _invalid("Modello non valido")
    model = model.strip()
    if len(model) > _MODEL_MAX:
        raise _invalid("Modello troppo lungo (massimo 100 caratteri)")
    if provider == "gemini":
        model = model.removeprefix("models/")
        if not _GEMINI_MODEL_RE.fullmatch(model):
            raise _invalid("Modello Gemini non valido (es. gemini-flash-latest)")
    elif provider == "openrouter" and "/" not in model:
        raise _invalid("Modello OpenRouter non valido (formato fornitore/modello)")
    return model


def _validated_base_url(value: Any) -> str | None:
    if not isinstance(value, str):
        raise _invalid("URL non valido")
    value = value.strip()
    if value and not _valid_url(value):
        raise _invalid("URL non valido: deve iniziare con http:// o https://")
    return value.rstrip("/") or None


def _validated_referer(value: Any) -> str | None:
    if not isinstance(value, str):
        raise _invalid("Referer non valido")
    value = value.strip()
    if value and not _valid_url(value):
        raise _invalid("Referer non valido: deve essere un URL http:// o https://")
    return value or None


def _validated_title(value: Any) -> str | None:
    if not isinstance(value, str):
        raise _invalid("Titolo non valido")
    value = value.strip()
    if len(value) > 100 or any(c in value for c in "\r\n") or not value.isprintable():
        raise _invalid("Titolo non valido (una riga, massimo 100 caratteri)")
    return value or None


def _validated_settings(req: TranscribeSettingsRequest) -> dict[str, Any]:
    """Values to store (``None`` = remove the key) for the fields present in *req*.

    Keys: ``provider``, ``language``, ``timeout``, ``<provider>.model`` and
    ``<provider>.base_url`` (top-level ``model``/``base_url`` go to the selected
    provider).
    """
    from transcription.config import PROVIDERS, get_transcription_provider

    present = req.model_fields_set
    out: dict[str, Any] = {}
    selected = get_transcription_provider()
    if "provider" in present:
        provider = req.provider
        if not isinstance(provider, str) or provider not in PROVIDERS:
            raise _invalid("Provider non valido")
        out["provider"] = provider
        selected = provider
    if "model" in present:
        out[f"{selected}.model"] = _validated_model(selected, req.model)
    if "base_url" in present:
        out[f"{selected}.base_url"] = _validated_base_url(req.base_url)
    if "providers" in present:
        providers = req.providers
        if not isinstance(providers, dict):
            raise _invalid("Provider non valido")
        for name, values in providers.items():
            if name not in PROVIDERS or not isinstance(values, dict):
                raise _invalid("Provider non valido")
            if "model" in values:
                out[f"{name}.model"] = _validated_model(name, values["model"])
            if "base_url" in values:
                out[f"{name}.base_url"] = _validated_base_url(values["base_url"])
            if name == "openrouter":
                if "http_referer" in values:
                    out["openrouter.http_referer"] = _validated_referer(
                        values["http_referer"]
                    )
                if "app_title" in values:
                    out["openrouter.app_title"] = _validated_title(values["app_title"])
    if "language" in present:
        language = req.language
        if not isinstance(language, str):
            raise _invalid("Lingua non valida")
        language = language.strip()
        if language and not _LANGUAGE_RE.fullmatch(language):
            raise _invalid(
                "Lingua non valida: usa un codice di 2-3 lettere minuscole (es. it)"
            )
        out["language"] = language or None
    if "timeout" in present:
        timeout = req.timeout
        low, high = _TIMEOUT_RANGE
        if isinstance(timeout, bool) or not isinstance(timeout, int):
            raise _invalid("Timeout non valido")
        if not low <= timeout <= high:
            raise _invalid(f"Timeout non valido: da {low} a {high} secondi")
        out["timeout"] = timeout
    return out


@operation("transcribe.settings", request=EmptyRequest, response=dict[str, Any])
def transcribe_settings(api: ChatAPI, req: EmptyRequest) -> dict[str, Any]:
    """Effective transcription settings and which ones environment variables force."""
    return _settings_out(api)


@operation(
    "transcribe.set_settings",
    request=TranscribeSettingsRequest,
    response=dict[str, Any],
)
def transcribe_set_settings(
    api: ChatAPI, req: TranscribeSettingsRequest
) -> dict[str, Any]:
    """Partially update the settings, then rebuild the service.

    Omitted fields are unchanged; an empty ``language`` / ``base_url`` restores
    the default.  Environment variables still win over what is saved.
    """
    from transcription.config import set_transcription_settings

    values = _validated_settings(req)
    if values:
        _save(set_transcription_settings, values)
        _reinit(api)
    return _settings_out(api)


# -- diagnostics (web "Log" dialog) ---------------------------------------------


@operation("transcribe.diagnostics", request=EmptyRequest, response=dict[str, Any])
def transcribe_diagnostics(api: ChatAPI, req: EmptyRequest) -> dict[str, Any]:
    """What the transcription pipeline sees right now (never the API key)."""
    from transcription import config as tconfig
    from transcription import store
    from transcription.client import _ffmpeg_available

    service = api.transcription.service
    client = getattr(service, "client", None)
    worker = getattr(service, "_worker", None)
    queue = getattr(service, "_queue", None)
    try:
        recent = store.recent(10)
    except (OSError, sqlite3.Error) as exc:
        recent = [{"status": "db_error", "error": str(exc)}]
    provider = tconfig.get_transcription_provider()
    return {
        "provider": provider,
        "api_key_set": tconfig.get_api_key(provider) is not None,
        "model": tconfig.get_provider_model(provider),
        "base_url": tconfig.get_provider_base_url(provider),
        "service_active": service is not None,
        "service_client": type(client).__name__ if client is not None else None,
        "service_model": getattr(client, "model", None),
        "service_base_url": getattr(client, "base_url", None),
        "worker_alive": bool(worker is not None and worker.is_alive()),
        "queue_size": queue.qsize() if queue is not None else None,
        "auto_transcribe": _auto_globals(),
        # The AutoTranscriber sets the hook in start(): None = not listening.
        "auto_listener": api.transcription.on_done is not None,
        "ffmpeg": _ffmpeg_available(),
        "env_overrides": tconfig.transcription_env_overrides(),
        "recent": recent,
    }


def _check_request(provider: str, base_url: str, api_key: str) -> tuple[str, dict]:
    if provider == "openrouter":
        return f"{base_url}/key", {"Authorization": f"Bearer {api_key}"}
    if provider == "gemini":
        return f"{base_url}/models?pageSize=1", {"x-goog-api-key": api_key}
    return f"{base_url}/models", {"Authorization": f"Bearer {api_key}"}


@operation("transcribe.check", request=EmptyRequest, response=dict[str, Any])
async def transcribe_check(api: ChatAPI, req: EmptyRequest) -> dict[str, Any]:
    """Call a cheap authenticated endpoint of the selected provider."""
    import aiohttp

    from server.logbuffer import redact
    from transcription import config as tconfig

    provider = tconfig.get_transcription_provider()
    api_key = tconfig.get_api_key(provider)
    if not api_key:
        return {"ok": False, "provider": provider, "detail": "Chiave API mancante"}
    url, headers = _check_request(
        provider, tconfig.get_provider_base_url(provider), api_key
    )
    try:
        async with (
            aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=20)) as session,
            session.get(url, headers=headers) as response,
        ):
            body = (await response.text())[:500].replace(api_key, "***")
            status = response.status
    except (aiohttp.ClientError, TimeoutError, ValueError) as exc:
        detail = f"Errore di rete: {type(exc).__name__}: {exc}"
        logger.warning("Verifica %s fallita: %s", provider, detail)
        return {"ok": False, "provider": provider, "url": url, "detail": detail}
    ok = status == 200
    log = logger.info if ok else logger.warning
    log("Verifica %s: HTTP %s %s", provider, status, redact(body)[:200])
    return {
        "ok": ok,
        "provider": provider,
        "url": url,
        "http_status": status,
        "detail": redact(body),
    }
