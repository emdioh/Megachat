from __future__ import annotations

import json
import threading

import protocols.config as _cfg
from protocols.config import _get, _get_int, _load_dotenv

#: Protocols with automatic transcription (Gmail has no voice notes).
AUTO_TRANSCRIBE_PROTOCOLS = ("whatsapp", "signal", "telegram")

_AUTO_GLOBAL_KEY = "auto_transcribe"
_AUTO_CHATS_KEY = "auto_transcribe_chats"
_write_lock = threading.Lock()


def get_openai_api_key() -> str | None:
    value = _get("openai_api_key", "OPENAI_API_KEY", "").strip()
    if not value:
        # Fallback su .env (come get_whatsapp_api_key): _get non legge .env.
        value = _load_dotenv().get("OPENAI_API_KEY", "").strip()
    return value or None


def get_transcription_model() -> str:
    value = _get("transcription_model", "TRANSCRIPTION_MODEL", "gpt-transcribe").strip()
    return value or "gpt-transcribe"


def get_transcription_language() -> str | None:
    value = _get("transcription_language", "TRANSCRIPTION_LANGUAGE", "").strip()
    return value or None


def get_transcription_base_url() -> str:
    value = _get(
        "transcription_base_url",
        "TRANSCRIPTION_BASE_URL",
        "https://api.openai.com/v1",
    ).strip()
    return (value or "https://api.openai.com/v1").rstrip("/")


def get_transcription_timeout() -> int:
    timeout = _get_int("transcription_timeout", "TRANSCRIPTION_TIMEOUT", 120)
    return timeout if timeout > 0 else 120


def get_transcription_enabled() -> bool:
    return get_openai_api_key() is not None


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

#: setting name -> (config.json key, environment variable)
TRANSCRIPTION_SETTINGS = {
    "model": ("transcription_model", "TRANSCRIPTION_MODEL"),
    "language": ("transcription_language", "TRANSCRIPTION_LANGUAGE"),
    "base_url": ("transcription_base_url", "TRANSCRIPTION_BASE_URL"),
    "timeout": ("transcription_timeout", "TRANSCRIPTION_TIMEOUT"),
}


def transcription_env_overrides() -> list[str]:
    """Settings currently forced by an environment variable (``_get`` gives env priority)."""
    import os

    return [
        name
        for name, (_key, env) in TRANSCRIPTION_SETTINGS.items()
        if os.environ.get(env)
    ]


def set_transcription_settings(values: dict[str, object]) -> None:
    """Persist the given settings; ``None`` removes the key (back to default)."""

    def mutate(data: dict) -> None:
        for name, value in values.items():
            key = TRANSCRIPTION_SETTINGS[name][0]
            if value is None:
                data.pop(key, None)
            else:
                data[key] = value

    _update_config(mutate)
