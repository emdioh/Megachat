from __future__ import annotations

import logging
import os
import queue
import threading
from collections.abc import Callable
from typing import Any

logger = logging.getLogger(__name__)


class TranscriptionService:
    def __init__(
        self,
        client: Any,
        store: Any,
        on_done: Callable[[str, str, dict[str, Any]], None] | None = None,
    ) -> None:
        """*on_done(protocol, attachment_id, row)* runs on the worker thread
        after a record turns ``ok`` or ``failed``; its errors are only logged."""
        self.client = client
        self.store = store
        self._on_done = on_done
        self._queue: queue.Queue[tuple[str, str, str | os.PathLike[str]] | None] = (
            queue.Queue()
        )
        self._submit_lock = threading.Lock()
        self._stopped = False
        self._worker = threading.Thread(
            target=self._run, daemon=True, name="transcription-worker"
        )
        self._worker.start()

    def submit(
        self, protocol: str, attachment_id: str, audio_path: str | os.PathLike[str]
    ) -> None:
        with self._submit_lock:
            current = self.store.get(protocol, attachment_id)
            if current is not None and current["status"] in {"ok", "pending"}:
                return
            self.store.set(protocol, attachment_id, status="pending")
            self._queue.put((protocol, attachment_id, audio_path))

    def status(self, protocol: str, attachment_id: str) -> dict[str, Any] | None:
        return self.store.get(protocol, attachment_id)

    def stop(self) -> None:
        with self._submit_lock:
            if self._stopped:
                return
            self._stopped = True
            self._queue.put(None)

    def _run(self) -> None:
        while True:
            item = self._queue.get()
            processed = False
            try:
                if item is None or self._stopped:
                    return
                protocol, attachment_id, audio_path = item
                processed = True
                text = self.client.transcribe(audio_path)
                self.store.set(
                    protocol,
                    attachment_id,
                    status="ok",
                    text=text,
                    model=self.client.model,
                )
                logger.info(
                    "Trascrizione completata: protocol=%s attachment_id=%s",
                    protocol,
                    attachment_id,
                )
            except Exception as exc:
                self.store.set(protocol, attachment_id, status="failed", error=str(exc))
                logger.exception(
                    "Trascrizione fallita: protocol=%s attachment_id=%s",
                    protocol,
                    attachment_id,
                )
            finally:
                if processed:
                    self._notify_done(protocol, attachment_id)
                self._queue.task_done()

    def _notify_done(self, protocol: str, attachment_id: str) -> None:
        if self._on_done is None:
            return
        try:
            row = self.store.get(protocol, attachment_id)
            if row is not None:
                self._on_done(protocol, attachment_id, row)
        except Exception:
            logger.exception(
                "Callback di trascrizione fallita: protocol=%s attachment_id=%s",
                protocol,
                attachment_id,
            )
