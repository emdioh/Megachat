"""The cloud transcription service as core state.

The service (one worker thread plus the on-disk store) belongs to the core,
not to an adapter: ``ChatCore.start`` creates it from the selected provider's
API key, ``ChatCore.shutdown`` stops it, and the ``transcribe.*`` /
``config.set_openai_key`` operations reach it through ``ChatAPI.transcription``.
"""

from __future__ import annotations

import logging
import threading
from collections.abc import Callable
from typing import Any

logger = logging.getLogger(__name__)


class TranscriptionHost:
    """Holds the (optional) :class:`~transcription.service.TranscriptionService`.

    ``service`` is ``None`` while the selected provider has no API key.  :meth:`init` is
    idempotent in the sense that it always stops the previous service before it
    builds the next one (a changed key replaces the client).
    """

    def __init__(
        self, on_done: Callable[[str, str, dict[str, Any]], None] | None = None
    ) -> None:
        self._lock = threading.Lock()
        self.service: Any = None
        #: Completion hook handed to every service built by :meth:`init`
        #: (``ChatCore`` points it at the auto-transcriber).
        self.on_done = on_done

    def init(self) -> Any:
        """(Re)build the service from the current configuration."""
        from transcription import store
        from transcription.client import build_client
        from transcription.config import (
            get_api_key,
            get_openrouter_headers,
            get_provider_base_url,
            get_provider_model,
            get_transcription_language,
            get_transcription_provider,
            get_transcription_timeout,
        )
        from transcription.service import TranscriptionService

        with self._lock:
            current = self.service
            if current is not None:
                current.stop()
            self.service = None

            provider = get_transcription_provider()
            api_key = get_api_key(provider)
            if not api_key:
                logger.info(
                    "Trascrizione cloud disattivata (%s senza chiave)", provider
                )
                return None

            client = build_client(
                provider,
                api_key,
                base_url=get_provider_base_url(provider),
                model=get_provider_model(provider),
                language=get_transcription_language(),
                timeout=get_transcription_timeout(),
                extra_headers=(
                    get_openrouter_headers() if provider == "openrouter" else None
                ),
            )
            self.service = TranscriptionService(
                client, store, on_done=self._dispatch_done
            )
            logger.info("Trascrizione cloud attiva (%s)", provider)
            return self.service

    def _dispatch_done(self, protocol: str, attachment_id: str, row: dict) -> None:
        callback = self.on_done
        if callback is not None:
            callback(protocol, attachment_id, row)

    def stop(self) -> None:
        """Stop the worker (``ChatCore.shutdown``).  Safe to call repeatedly."""
        with self._lock:
            service, self.service = self.service, None
        if service is not None:
            service.stop()
