"""Operation errors as megachatd reports them (CONTRACTS.md, error codes)."""

from __future__ import annotations

from typing import Any

__all__ = ["ERROR_CODES", "ApiError"]

ERROR_CODES = (
    "invalid_request",
    "not_found",
    "unauthorized",
    "forbidden",
    "too_large",
    "conflict",
    "unprocessable",
    "not_supported",
    "backend_down",
    "backend_error",
    "internal",
)


class ApiError(Exception):
    """Neutral operation error: a ``code`` from :data:`ERROR_CODES` and the
    exact user-facing ``message``."""

    def __init__(self, code: str, message: str, details: Any = None) -> None:
        if code not in ERROR_CODES:
            raise ValueError(f"unknown ApiError code {code!r}")
        super().__init__(message)
        self.code = code
        self.message = message
        self.details = details

    def __repr__(self) -> str:
        return f"ApiError({self.code!r}, {self.message!r})"
