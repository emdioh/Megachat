"""Voice-note transcription operations and the OpenAI key setting.

The service is core state (:class:`server.transcribe.TranscriptionHost`,
``api.transcription``).  Messages are the Italian texts the SPA has always
shown.
"""

from __future__ import annotations

import json
import logging
import re
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
    """Whether transcription is available (an OpenAI key is configured)."""
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


@operation("config.set_openai_key", request=OpenAIKeyRequest, response=StatusOut)
def config_set_openai_key(api: ChatAPI, req: OpenAIKeyRequest) -> StatusOut:
    """Save the OpenAI key to ``config.json`` and (re)start transcription."""
    api_key = req.api_key
    if not isinstance(api_key, str):
        raise ApiError("invalid_request", "Chiave non valida")
    api_key = api_key.strip()
    if not api_key or not api_key.startswith("sk-"):
        raise ApiError("invalid_request", "Chiave non valida")

    from protocols.config import PROJECT_DIR, _load_config

    config_path = PROJECT_DIR / "config.json"
    data = _load_config()
    data["openai_api_key"] = api_key
    try:
        with config_path.open("w", encoding="utf-8") as config_file:
            json.dump(data, config_file, indent=2)
            config_file.write("\n")
    except OSError:
        logger.exception("Impossibile salvare la configurazione OpenAI")
        raise ApiError("internal", "Errore di salvataggio") from None

    try:
        api.transcription.init()
    except Exception:
        logger.exception("Impossibile inizializzare la trascrizione OpenAI")
        raise ApiError("internal", "Errore di inizializzazione") from None
    return StatusOut(status="ok")


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
    """Effective settings.  Never contains the API key itself."""
    from transcription import config as tconfig

    return {
        "configured": api.transcription.service is not None,
        "api_key_set": tconfig.get_openai_api_key() is not None,
        "model": tconfig.get_transcription_model(),
        "language": tconfig.get_transcription_language() or "",
        "base_url": tconfig.get_transcription_base_url(),
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


def _validated_settings(req: TranscribeSettingsRequest) -> dict[str, Any]:
    """Values to store (``None`` = remove the key) for the fields present in *req*."""
    present = req.model_fields_set
    out: dict[str, Any] = {}
    if "model" in present:
        model = req.model
        if not isinstance(model, str) or not model.strip():
            raise _invalid("Modello non valido")
        model = model.strip()
        if len(model) > _MODEL_MAX:
            raise _invalid("Modello troppo lungo (massimo 100 caratteri)")
        out["model"] = model
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
    if "base_url" in present:
        base_url = req.base_url
        if not isinstance(base_url, str):
            raise _invalid("URL non valido")
        base_url = base_url.strip()
        if base_url and not _valid_url(base_url):
            raise _invalid("URL non valido: deve iniziare con http:// o https://")
        out["base_url"] = base_url.rstrip("/") or None
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
        try:
            api.transcription.init()
        except Exception:
            logger.exception("Impossibile inizializzare la trascrizione")
            raise ApiError("internal", "Errore di inizializzazione") from None
    return _settings_out(api)
