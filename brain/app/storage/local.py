"""Filesystem storage backend. Writes under settings.recordings_dir."""

from __future__ import annotations

import os

from app.config import settings


class LocalFilesystemStorage:
    def __init__(self, base_dir: str | None = None) -> None:
        self.base_dir = base_dir or settings.recordings_dir

    async def write(self, session_id: str, filename: str, data: bytes) -> str:
        target_dir = os.path.join(self.base_dir, session_id)
        os.makedirs(target_dir, exist_ok=True)
        path = os.path.join(target_dir, filename)
        with open(path, "wb") as fh:
            fh.write(data)
        return f"/recordings/{session_id}/{filename}"
