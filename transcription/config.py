from __future__ import annotations

import json
import os
import threading

import protocols.config as _cfg
from protocols.config import _get, _get_int, _load_dotenv

#: Protocols with automatic transcription (Gmail has no voice notes).
AUTO_TRANSCRIBE_PROTOCOLS = ("whatsapp", "signal", "telegram")

_AUTO_GLOBAL_KEY = "auto_transcribe"
_AUTO_CHATS_KEY = "auto_transcribe_chats"
_write_lock = threading.Lock()


#: Provider di trascrizione selezionabili (DeepSeek non ha input audio).
PROVIDERS = ("openai", "openrouter", "gemini")
DEFAULT_PROVIDER = "openai"

#: provider -> chiave API: (config.json key, variabili d'ambiente in ordine)
_API_KEYS = {
    "openai": ("openai_api_key", ("OPENAI_API_KEY",)),
    "openrouter": ("openrouter_api_key", ("OPENROUTER_API_KEY",)),
    "gemini": ("gemini_api_key", ("GEMINI_API_KEY", "GOOGLE_API_KEY")),
}

#: provider -> setting -> (default, [(config.json key, env)] in priority order).
#: Ambiente e config.json seguono il formato di botty (``AI_PROVIDER``,
#: ``<PROVIDER>_TRANSCRIPTION_MODEL``, ...).  OpenAI ricade sulle vecchie chiavi
#: ``transcription_model`` / ``transcription_base_url`` (retrocompatibilità).
_PROVIDER_SETTINGS = {
    "openai": {
        "model": (
            "gpt-transcribe",
            [
                ("openai_transcription_model", "OPENAI_TRANSCRIPTION_MODEL"),
                ("transcription_model", "TRANSCRIPTION_MODEL"),
            ],
        ),
        "base_url": (
            "https://api.openai.com/v1",
            [("transcription_base_url", "TRANSCRIPTION_BASE_URL")],
        ),
    },
    "openrouter": {
        "model": (
            "openai/whisper-large-v3",
            [("openrouter_transcription_model", "OPENROUTER_TRANSCRIPTION_MODEL")],
        ),
        "base_url": (
            "https://openrouter.ai/api/v1",
            [("openrouter_base_url", "OPENROUTER_BASE_URL")],
        ),
        "http_referer": (
            "",
            [("openrouter_http_referer", "OPENROUTER_HTTP_REFERER")],
        ),
        "app_title": (
            "",
            [("openrouter_app_title", "OPENROUTER_APP_TITLE")],
        ),
    },
    "gemini": {
        "model": (
            "gemini-flash-latest",
            [("gemini_transcription_model", "GEMINI_TRANSCRIPTION_MODEL")],
        ),
        "base_url": (
            "https://generativelanguage.googleapis.com/v1beta",
            [("gemini_base_url", "GEMINI_BASE_URL")],
        ),
    },
}


def _check_provider(provider: str) -> str:
    if provider not in PROVIDERS:
        raise ValueError(f"unsupported transcription provider: {provider!r}")
    return provider


def get_transcription_provider() -> str:
    """Selected provider (env > config.json > ``openai``); unknown values -> openai."""
    value = _get("ai_provider", "AI_PROVIDER", "")
    value = value.strip().lower() if isinstance(value, str) else ""
    return value if value in PROVIDERS else DEFAULT_PROVIDER


def get_api_key(provider: str | None = None) -> str | None:
    provider = _check_provider(provider or get_transcription_provider())
    key, envs = _API_KEYS[provider]
    value = _get(key, envs[0], "")
    value = value.strip() if isinstance(value, str) else ""
    if not value:
        for env in envs[1:]:
            value = os.environ.get(env, "").strip()
            if value:
                break
    if not value:
        # Fallback su .env (come get_whatsapp_api_key): _get non legge .env.
        dotenv = _load_dotenv()
        for env in envs:
            value = dotenv.get(env, "").strip()
            if value:
                break
    return value or None


def get_openai_api_key() -> str | None:
    return get_api_key("openai")


def _get_chain(pairs: list[tuple[str, str]], default: str) -> str:
    """First non-empty env var, then first non-empty config.json key, else *default*."""
    for _key, env in pairs:
        value = os.environ.get(env, "").strip()
        if value:
            return value
    config = _cfg._load_config()
    for key, _env in pairs:
        value = config.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return default


def _provider_setting(provider: str, name: str) -> str:
    default, pairs = _PROVIDER_SETTINGS[_check_provider(provider)][name]
    value = _get_chain(pairs, default)
    return value.rstrip("/") if name == "base_url" else value


def get_openrouter_headers() -> dict[str, str]:
    """Optional OpenRouter attribution headers (like botty), only when set."""
    headers: dict[str, str] = {}
    referer = _provider_setting("openrouter", "http_referer")
    title = _provider_setting("openrouter", "app_title")
    if referer:
        headers["HTTP-Referer"] = referer
    if title:
        headers["X-OpenRouter-Title"] = title
    return headers


def get_provider_model(provider: str | None = None) -> str:
    return _provider_setting(provider or get_transcription_provider(), "model")


def get_provider_base_url(provider: str | None = None) -> str:
    return _provider_setting(provider or get_transcription_provider(), "base_url")


def get_transcription_model() -> str:
    """Model of the *selected* provider."""
    return get_provider_model()


def get_transcription_base_url() -> str:
    """Base URL of the *selected* provider."""
    return get_provider_base_url()


def get_transcription_language() -> str | None:
    value = _get("transcription_language", "TRANSCRIPTION_LANGUAGE", "").strip()
    return value or None


def get_transcription_timeout() -> int:
    timeout = _get_int("transcription_timeout", "TRANSCRIPTION_TIMEOUT", 120)
    return timeout if timeout > 0 else 120


def get_transcription_enabled() -> bool:
    """Enabled = the selected provider has an API key."""
    return get_api_key() is not None


# -- trascrizione automatica delle note vocali ----------------------------------


def _chat_key(protocol: str, contact_id: str) -> str:
    return f"{protocol}:{contact_id}"


def _check_protocol(protocol: str) -> None:
    if protocol not in AUTO_TRANSCRIBE_PROTOCOLS:
        raise ValueError(f"unsupported protocol: {protocol!r}")


def get_auto_transcribe_global(protocol: str) -> bool:
    """Global switch of *protocol* (``False`` when unset or invalid)."""
    section = _cfg._load_config().get(_AUTO_GLOBAL_KEY)
    if not isinstance(section, dict):
        return False
    return section.get(protocol) is True


def get_auto_transcribe_globals() -> dict[str, bool]:
    return {p: get_auto_transcribe_global(p) for p in AUTO_TRANSCRIBE_PROTOCOLS}


def get_auto_transcribe_chat(protocol: str, contact_id: str) -> bool | None:
    """Per-chat override: ``True``/``False``, or ``None`` when not set."""
    section = _cfg._load_config().get(_AUTO_CHATS_KEY)
    if not isinstance(section, dict):
        return None
    value = section.get(_chat_key(protocol, contact_id))
    return value if isinstance(value, bool) else None


def auto_transcribe_effective(protocol: str, contact_id: str) -> bool:
    """Chat override if set, else the protocol's global value."""
    if protocol not in AUTO_TRANSCRIBE_PROTOCOLS:
        return False
    override = get_auto_transcribe_chat(protocol, contact_id)
    if override is not None:
        return override
    return get_auto_transcribe_global(protocol)


def _update_config(mutate) -> None:
    """Load the whole ``config.json``, apply *mutate*, write it back."""
    with _write_lock:
        config_path = _cfg.PROJECT_DIR / "config.json"
        data = _cfg._load_config()
        mutate(data)
        with config_path.open("w", encoding="utf-8") as config_file:
            json.dump(data, config_file, indent=2)
            config_file.write("\n")


def set_auto_transcribe_global(protocol: str, enabled: bool) -> None:
    _check_protocol(protocol)

    def mutate(data: dict) -> None:
        section = data.get(_AUTO_GLOBAL_KEY)
        if not isinstance(section, dict):
            section = {}
        section[protocol] = bool(enabled)
        data[_AUTO_GLOBAL_KEY] = section

    _update_config(mutate)


def set_auto_transcribe_chat(
    protocol: str, contact_id: str, enabled: bool | None
) -> None:
    """Set the per-chat override; ``None`` removes it."""
    _check_protocol(protocol)
    key = _chat_key(protocol, contact_id)

    def mutate(data: dict) -> None:
        section = data.get(_AUTO_CHATS_KEY)
        if not isinstance(section, dict):
            section = {}
        if enabled is None:
            section.pop(key, None)
        else:
            section[key] = bool(enabled)
        data[_AUTO_CHATS_KEY] = section

    _update_config(mutate)


# -- transcription service settings (editable from the web UI) -----------------

#: setting name -> (config.json key, environment variable); shared settings
SHARED_SETTINGS = {
    "provider": ("ai_provider", "AI_PROVIDER"),
    "language": ("transcription_language", "TRANSCRIPTION_LANGUAGE"),
    "timeout": ("transcription_timeout", "TRANSCRIPTION_TIMEOUT"),
}


def _provider_names() -> dict[tuple[str, str], list[tuple[str, str]]]:
    return {
        (provider, name): pairs
        for provider, settings in _PROVIDER_SETTINGS.items()
        for name, (_default, pairs) in settings.items()
    }


def transcription_env_overrides() -> list[str]:
    """Settings forced by an environment variable (``_get`` gives env priority).

    Names: ``provider``, ``language``, ``timeout`` and ``<provider>.model`` /
    ``<provider>.base_url``.
    """
    out = [name for name, (_key, env) in SHARED_SETTINGS.items() if os.environ.get(env)]
    out += [
        f"{provider}.{name}"
        for (provider, name), pairs in _provider_names().items()
        if any(os.environ.get(env) for _key, env in pairs)
    ]
    return out


def set_transcription_settings(values: dict[str, object]) -> None:
    """Persist the given settings; ``None`` removes the key (back to default).

    Names: ``provider``, ``language``, ``timeout``, ``<provider>.model``,
    ``<provider>.base_url``.
    """
    # Si scrive sempre la chiave primaria (per OpenAI ``openai_transcription_model``).
    provider_keys = {
        f"{provider}.{name}": [key for key, _env in pairs]
        for (provider, name), pairs in _provider_names().items()
    }

    def mutate(data: dict) -> None:
        for name, value in values.items():
            keys = (
                [SHARED_SETTINGS[name][0]]
                if name in SHARED_SETTINGS
                else provider_keys[name]
            )
            if value is None:
                for key in keys:  # tutte le chiavi (anche quella legacy)
                    data.pop(key, None)
            else:
                data[keys[0]] = value

    _update_config(mutate)


def set_api_key(provider: str, api_key: str) -> None:
    key = _API_KEYS[_check_provider(provider)][0]

    def mutate(data: dict) -> None:
        data[key] = api_key

    _update_config(mutate)
