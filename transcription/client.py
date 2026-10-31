from __future__ import annotations

import asyncio
import base64
import json
import logging
import os
import subprocess
import tempfile
from collections.abc import Mapping
from pathlib import Path
from typing import ClassVar

import aiohttp

logger = logging.getLogger(__name__)

_MAX_AUDIO_SIZE = 25 * 1024 * 1024

# Formati accettati dall'API OpenAI (i vocali Signal sono .oga/opus → transcodifica).
_SUPPORTED_EXTENSIONS = {".mp3", ".mp4", ".mpeg", ".mpga", ".m4a", ".wav", ".webm"}


class TranscriptionError(Exception):
    pass


def _transcode_to_mp3(path: Path) -> Path:
    """Transcodifica l'audio in mp3 con ffmpeg (formati non supportati da OpenAI)."""
    if not _ffmpeg_available():
        raise TranscriptionError(
            "Formato audio non supportato dal provider e ffmpeg non disponibile "
            "per la transcodifica"
        )
    logger.info("Trascrizione: transcodifica %s in mp3 con ffmpeg", path.name)
    fd, tmp_path = tempfile.mkstemp(suffix=".mp3")
    os.close(fd)
    try:
        result = subprocess.run(
            [
                "ffmpeg",
                "-y",
                "-loglevel",
                "error",
                "-i",
                str(path),
                "-ac",
                "1",
                "-b:a",
                "64k",
                tmp_path,
            ],
            capture_output=True,
            timeout=120,
            check=False,
        )
        if result.returncode != 0:
            raise TranscriptionError(
                "Transcodifica ffmpeg fallita: "
                f"{result.stderr.decode(errors='replace')[:200]}"
            )
        return Path(tmp_path)
    except subprocess.TimeoutExpired as exc:
        raise TranscriptionError("Transcodifica ffmpeg in timeout") from exc
    except Exception:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
        raise


def _ffmpeg_available() -> bool:
    try:
        result = subprocess.run(
            ["ffmpeg", "-version"], capture_output=True, timeout=10, check=False
        )
        return result.returncode == 0
    except (OSError, subprocess.TimeoutExpired):
        return False


class CloudTranscriptionClient:
    def __init__(
        self,
        api_key: str,
        base_url: str = "https://api.openai.com/v1",
        model: str = "gpt-transcribe",
        language: str | None = None,
        timeout: int = 120,
    ) -> None:
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.language = language
        self.timeout = timeout

    #: Extra HTTP headers (OpenRouter attribution).
    extra_headers: ClassVar[Mapping[str, str]] = {}

    def _language_field(self) -> str:
        return "language" if self.model == "whisper-1" else "languages"

    async def transcribe_async(self, audio_path: str | os.PathLike[str]) -> str:
        path = Path(audio_path)
        if os.path.getsize(path) > _MAX_AUDIO_SIZE:
            raise TranscriptionError("File audio troppo grande (max 25MB)")

        upload_path = path
        cleanup = False
        if path.suffix.lower() not in _SUPPORTED_EXTENSIONS:
            upload_path = _transcode_to_mp3(path)
            cleanup = True

        try:
            try:
                return await self._upload(upload_path)
            except TranscriptionError as exc:
                # L'API può rifiutare formati non documentati (es. .aac): se non
                # abbiamo ancora transcodificato, riprova con ffmpeg.
                if cleanup or "Unsupported file format" not in str(exc):
                    raise
                upload_path = _transcode_to_mp3(path)
                cleanup = True
                return await self._upload(upload_path)
        finally:
            if cleanup:
                upload_path.unlink(missing_ok=True)

    async def _upload(self, upload_path: Path) -> str:
        form = aiohttp.FormData()
        form.add_field("model", self.model)
        if self.language:
            form.add_field(self._language_field(), self.language)

        timeout = aiohttp.ClientTimeout(total=self.timeout)
        headers = {"Authorization": f"Bearer {self.api_key}", **self.extra_headers}
        url = f"{self.base_url}/audio/transcriptions"
        logger.info(
            "Trascrizione: POST %s model=%s file=%s (%d byte) lingua=%s",
            url,
            self.model,
            upload_path.name,
            upload_path.stat().st_size,
            self.language or "auto",
        )
        try:
            with upload_path.open("rb") as audio_file:
                form.add_field("file", audio_file, filename=upload_path.name)
                async with (
                    aiohttp.ClientSession(timeout=timeout) as session,
                    session.post(url, data=form, headers=headers) as response,
                ):
                    if response.status != 200:
                        body = _error_body(await response.text()).replace(
                            self.api_key, "***"
                        )
                        logger.warning(
                            "Trascrizione: risposta HTTP %s: %s",
                            response.status,
                            body,
                        )
                        raise TranscriptionError(
                            f"Errore trascrizione HTTP {response.status}: {body}"
                        )
                    try:
                        data = await response.json(content_type=None)
                    except ValueError:
                        body = _error_body(await response.text())
                        raise TranscriptionError(f"Risposta non JSON: {body}") from None
        except (aiohttp.ClientError, TimeoutError) as exc:
            raise TranscriptionError(
                f"Errore di rete: {type(exc).__name__}: {exc}"
            ) from None
        if not isinstance(data, dict):
            raise TranscriptionError(f"Risposta non valida: {str(data)[:200]}")
        text = data.get("text")
        logger.info(
            "Trascrizione: risposta HTTP 200, campi=%s, testo di %s caratteri",
            sorted(data),
            len(text) if isinstance(text, str) else "?",
        )
        if not isinstance(text, str):
            raise TranscriptionError(
                f"Risposta senza campo 'text': {_error_body(json.dumps(data))}"
            )
        return text.strip()

    def transcribe(self, audio_path: str | os.PathLike[str]) -> str:
        return asyncio.run(self.transcribe_async(audio_path))


def _error_body(text: str) -> str:
    return text.strip().replace("\n", " ")[:200]


class _InlineAudioClient:
    """Base for providers that take the audio inline (base64) in the request."""

    #: file extension -> accepted format/mime string (subclasses)
    FORMATS: ClassVar[dict[str, str]] = {}
    MAX_SIZE = 25 * 1024 * 1024
    SIZE_MESSAGE = "File audio troppo grande"
    LABEL = "provider"

    def __init__(
        self,
        api_key: str,
        base_url: str,
        model: str,
        language: str | None = None,
        timeout: int = 120,
    ) -> None:
        self.api_key = api_key
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.language = language
        self.timeout = timeout

    async def transcribe_async(self, audio_path: str | os.PathLike[str]) -> str:
        path = Path(audio_path)
        if os.path.getsize(path) > self.MAX_SIZE:
            raise TranscriptionError(self.SIZE_MESSAGE)
        upload_path = path
        cleanup = False
        fmt = self.FORMATS.get(path.suffix.lower())
        if fmt is None:
            upload_path = _transcode_to_mp3(path)
            cleanup = True
            fmt = self.FORMATS[".mp3"]
        try:
            if os.path.getsize(upload_path) > self.MAX_SIZE:
                raise TranscriptionError(self.SIZE_MESSAGE)
            data = base64.b64encode(upload_path.read_bytes()).decode("ascii")
        finally:
            if cleanup:
                upload_path.unlink(missing_ok=True)
        return await self._request(data, fmt)

    async def _post(self, url: str, headers: dict[str, str], body: dict) -> dict:
        timeout = aiohttp.ClientTimeout(total=self.timeout)
        try:
            async with (
                aiohttp.ClientSession(timeout=timeout) as session,
                session.post(url, json=body, headers=headers) as response,
            ):
                if response.status != 200:
                    detail = _error_body(await response.text())
                    raise TranscriptionError(
                        f"Errore trascrizione {self.LABEL} HTTP {response.status}: "
                        f"{detail}".replace(self.api_key, "***")
                    )
                data = await response.json()
        except (aiohttp.ClientError, TimeoutError) as exc:
            raise TranscriptionError(
                f"Errore di rete con {self.LABEL}: {type(exc).__name__}"
            ) from None
        if not isinstance(data, dict):
            raise TranscriptionError(f"Risposta {self.LABEL} non valida")
        return data

    async def _request(self, data: str, fmt: str) -> str:
        raise NotImplementedError

    def transcribe(self, audio_path: str | os.PathLike[str]) -> str:
        return asyncio.run(self.transcribe_async(audio_path))


class OpenRouterTranscriptionClient(CloudTranscriptionClient):
    """OpenRouter: OpenAI-compatible multipart ``POST {base}/audio/transcriptions``.

    Same as botty: optional ``HTTP-Referer`` / ``X-OpenRouter-Title`` headers.
    """

    def __init__(
        self,
        api_key: str,
        base_url: str = "https://openrouter.ai/api/v1",
        model: str = "openai/whisper-large-v3",
        language: str | None = None,
        timeout: int = 120,
        extra_headers: dict[str, str] | None = None,
    ) -> None:
        super().__init__(api_key, base_url, model, language, timeout)
        self.extra_headers = dict(extra_headers or {})  # type: ignore[misc]

    def _language_field(self) -> str:
        return "language"


GEMINI_PROMPT = (
    "Transcribe the audio verbatim in its original language{language}. "
    "Output only the transcription text: no comments, no timestamps, no quotes, "
    "no speaker labels. If there is no speech, output nothing."
)

#: Gemini inline request limit (~20MB total); base64 grows the payload by 4/3.
_GEMINI_MAX_AUDIO = 15 * 1024 * 1024


class GeminiTranscriptionClient(_InlineAudioClient):
    """Gemini ``models/{model}:generateContent`` with inline audio."""

    LABEL = "Gemini"
    MAX_SIZE = _GEMINI_MAX_AUDIO
    SIZE_MESSAGE = "File audio troppo grande per Gemini (max 15MB)"
    # WAV, MP3, AIFF, AAC, OGG, FLAC (Signal/WhatsApp voice notes: ogg/opus).
    FORMATS: ClassVar[dict[str, str]] = {
        ".wav": "audio/wav",
        ".mp3": "audio/mp3",
        ".aiff": "audio/aiff",
        ".aif": "audio/aiff",
        ".aac": "audio/aac",
        ".ogg": "audio/ogg",
        ".oga": "audio/ogg",
        ".opus": "audio/ogg",
        ".flac": "audio/flac",
    }

    def __init__(
        self,
        api_key: str,
        base_url: str = "https://generativelanguage.googleapis.com/v1beta",
        model: str = "gemini-flash-latest",
        language: str | None = None,
        timeout: int = 120,
    ) -> None:
        super().__init__(api_key, base_url, model, language, timeout)

    def _prompt(self) -> str:
        language = f" (in language code '{self.language}')" if self.language else ""
        return GEMINI_PROMPT.format(language=language)

    async def _request(self, data: str, fmt: str) -> str:
        body = {
            "contents": [
                {
                    "parts": [
                        {"text": self._prompt()},
                        {"inline_data": {"mime_type": fmt, "data": data}},
                    ]
                }
            ],
            "generationConfig": {"temperature": 0},
        }
        headers = {"x-goog-api-key": self.api_key}
        url = f"{self.base_url}/models/{self.model}:generateContent"
        result = await self._post(url, headers, body)
        return self._parse(result)

    @staticmethod
    def _parse(result: dict) -> str:
        candidates = result.get("candidates")
        if not isinstance(candidates, list) or not candidates:
            feedback = result.get("promptFeedback")
            reason = feedback.get("blockReason") if isinstance(feedback, dict) else None
            if reason:
                raise TranscriptionError(f"Richiesta bloccata da Gemini ({reason})")
            return ""
        candidate = candidates[0] if isinstance(candidates[0], dict) else {}
        content = candidate.get("content")
        parts = content.get("parts") if isinstance(content, dict) else None
        texts = [
            part["text"]
            for part in (parts if isinstance(parts, list) else [])
            if isinstance(part, dict) and isinstance(part.get("text"), str)
        ]
        text = "".join(texts).strip()
        reason = candidate.get("finishReason")
        if not text and reason in {"SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII"}:
            raise TranscriptionError(f"Risposta bloccata da Gemini ({reason})")
        return text


def build_client(
    provider: str,
    api_key: str,
    *,
    base_url: str,
    model: str,
    language: str | None = None,
    timeout: int = 120,
    extra_headers: dict[str, str] | None = None,
):
    """Client of *provider* (``openai`` | ``openrouter`` | ``gemini``)."""
    classes = {
        "openai": CloudTranscriptionClient,
        "openrouter": OpenRouterTranscriptionClient,
        "gemini": GeminiTranscriptionClient,
    }
    try:
        cls = classes[provider]
    except KeyError:
        raise ValueError(f"unsupported transcription provider: {provider!r}") from None
    kwargs: dict = {}
    if provider == "openrouter":
        kwargs["extra_headers"] = extra_headers
    return cls(
        api_key,
        base_url=base_url,
        model=model,
        language=language,
        timeout=timeout,
        **kwargs,
    )
