"""FastAPI entry point for the brain (text-only control plane).

CORS + a /recordings static mount (so transcript URLs are GETtable) + the
sessions router. No media monitor, no Redis store, no intent warmup — intent is
delegated to the intent service over HTTP. Launched via `uvicorn app.main:app`.
"""

from __future__ import annotations

import os

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles

from app.config import settings
from app.routes.sessions import router as sessions_router

app = FastAPI(title="VideoKYC Brain", version="0.1.0")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

os.makedirs(settings.recordings_dir, exist_ok=True)
app.mount("/recordings", StaticFiles(directory=settings.recordings_dir), name="recordings")

app.include_router(sessions_router)


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}
