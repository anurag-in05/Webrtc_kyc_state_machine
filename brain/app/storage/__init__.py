"""Storage backend factory.

Picks the backend at import time from `AWS_STORAGE`: `s3` → S3Storage, anything
else → LocalFilesystemStorage. Callers import the module-level `storage`
singleton. S3Storage always writes a local copy first and falls back to a local
URL on upload failure / missing creds.
"""

from __future__ import annotations

from loguru import logger

from app.config import settings
from app.storage.base import StorageBackend


def _make_storage() -> StorageBackend:
    backend = (settings.aws_storage or "").strip().lower()
    if backend == "s3":
        from app.storage.s3 import S3Storage
        logger.info(f"Storage backend: S3 (bucket={settings.aws_s3_bucket})")
        return S3Storage()
    from app.storage.local import LocalFilesystemStorage
    logger.info(f"Storage backend: local ({settings.recordings_dir})")
    return LocalFilesystemStorage()


storage = _make_storage()
