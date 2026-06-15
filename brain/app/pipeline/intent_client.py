"""HTTP client for the shared intent service.

Drop-in replacement for in-process `EnsembleIntentClassifier` (same
`classify`/`classify_async`), so app processes share ONE ~1.5 GB ensemble copy
in the intent container instead of each loading it.

Failure policy (CLAUDE.md invariant #5): ANY error folds to the safe
`please_repeat` re-prompt, degrading the call rather than breaking it.
httpx clients are lazy and REUSED — never construct one per request.
"""

from __future__ import annotations

from typing import Literal

import httpx
from loguru import logger

from app.services import metrics

Intent = Literal["yes", "no", "please_repeat"]

# Intents the state machine understands; anything else → please_repeat.
_VALID_INTENTS = {"yes", "no", "please_repeat"}


class RemoteIntentClassifier:
    """Calls the intent service over HTTP; same surface as in-process
    `EnsembleIntentClassifier`."""

    def __init__(self, base_url: str, timeout: float = 2.0) -> None:
        self._base_url = base_url.rstrip("/")
        self._timeout = timeout
        self._client: httpx.Client | None = None
        self._aclient: httpx.AsyncClient | None = None

    # Lazy, reused clients (connection pooling).
    def _sync_client(self) -> httpx.Client:
        if self._client is None:
            self._client = httpx.Client(base_url=self._base_url, timeout=self._timeout)
        return self._client

    def _async_client(self) -> httpx.AsyncClient:
        if self._aclient is None:
            self._aclient = httpx.AsyncClient(base_url=self._base_url, timeout=self._timeout)
        return self._aclient

    @staticmethod
    def _coerce(intent: object) -> Intent:
        return intent if intent in _VALID_INTENTS else "please_repeat"  # type: ignore[return-value]

    def classify(self, text: str, language: str = "english") -> Intent:
        text = (text or "").strip()
        if not text:
            return "please_repeat"
        try:
            resp = self._sync_client().post(
                "/classify", json={"text": text, "language": language}
            )
            resp.raise_for_status()
            return self._coerce(resp.json().get("intent"))
        except Exception as exc:  # network/timeout/bad-body → safe default
            # Genuine failure, not a legitimate please_repeat classification.
            metrics.inc_intent_failure()
            logger.warning(f"remote intent classify failed ({self._base_url}): {exc}")
            return "please_repeat"

    async def classify_async(self, text: str, language: str = "english") -> Intent:
        text = (text or "").strip()
        if not text:
            return "please_repeat"
        try:
            resp = await self._async_client().post(
                "/classify", json={"text": text, "language": language}
            )
            resp.raise_for_status()
            return self._coerce(resp.json().get("intent"))
        except Exception as exc:  # network/timeout/bad-body → safe default
            # Genuine failure, not a legitimate please_repeat classification.
            metrics.inc_intent_failure()
            logger.warning(f"remote intent classify failed ({self._base_url}): {exc}")
            return "please_repeat"
