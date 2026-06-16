"""Standalone intent-classification service (shared model container).

Own process/container so the ~1.5 GB ensemble loads once and is shared by every
app pointing INTENT_SERVICE_URL at it. Reuses the in-process classifier, so the
voting + please_repeat fold (CLAUDE.md invariant #5) are identical.
Self-contained (no app.* imports) so its Docker image ships only intent/.

Run:  uvicorn service:app --host 0.0.0.0 --port 8000
"""

import asyncio
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI
from loguru import logger
from pydantic import BaseModel

from classifier import intent_classifier


def _warm_on_startup() -> bool:
    """WARM_MODELS_ON_STARTUP env flag (default true). Read directly, not via
    app.config, to keep the container image free of the main app's source tree."""
    return os.environ.get("WARM_MODELS_ON_STARTUP", "true").strip().lower() not in {
        "false", "0", "no", "off",
    }


class ClassifyRequest(BaseModel):
    text: str
    language: str = "english"


class ClassifyResponse(BaseModel):
    intent: str


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Warm the model off the loop so the first request doesn't pay the load.
    # Gated by WARM_MODELS_ON_STARTUP (conftest sets it false to skip the encoder).
    if _warm_on_startup():
        try:
            await asyncio.to_thread(intent_classifier.warmup)
            logger.info("Intent service: models warmed at startup")
        except Exception as exc:  # pragma: no cover — defensive
            logger.warning(f"Intent service warmup failed (will lazy-load): {exc}")
    yield


app = FastAPI(title="VideoKYC Intent Service", version="0.1.0", lifespan=lifespan)


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/classify", response_model=ClassifyResponse)
async def classify(req: ClassifyRequest) -> ClassifyResponse:
    # classify_async runs off the loop and folds failure / empty to please_repeat.
    intent = await intent_classifier.classify_async(req.text, req.language)
    return ClassifyResponse(intent=intent)
