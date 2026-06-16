"""S3 storage backend.

Uploads transcript artifacts to S3 (returns an `aws_url`-fronted URL) and keeps a
local copy so other code resolves them without re-downloading. The recorder owns
the MP4 now (CONTRACTS §3), so the brain only pushes the text it writes.
Credentials via the standard boto3 chain. Falls back to a local `/recordings/...`
URL if boto3 is missing or the upload fails, so calls keep working in dev.
"""

from __future__ import annotations

import asyncio
import os
from urllib.parse import quote

from loguru import logger

from app.config import settings


def _get_s3_client():
    try:
        import boto3  # type: ignore
    except ImportError:
        logger.warning("boto3 not installed; S3 uploads disabled.")
        return None
    try:
        return boto3.client(
            "s3",
            region_name=settings.aws_region or None,
            aws_access_key_id=os.getenv("AWS_ACCESS_KEY_ID") or None,
            aws_secret_access_key=os.getenv("AWS_SECRET_ACCESS_KEY") or None,
        )
    except Exception as exc:
        logger.warning(f"Failed to init S3 client: {exc}")
        return None


# The brain writes the transcript artifacts (recap.py: transcript.txt +
# transcript.json); those go to S3. The recorder uploads the MP4 itself.
S3_UPLOAD_SUFFIXES = (".json", ".txt")


def _content_type(filename: str) -> str:
    if filename.endswith(".mp4"):
        return "video/mp4"
    if filename.endswith(".wav"):
        return "audio/wav"
    if filename.endswith(".json"):
        return "application/json"
    if filename.endswith(".txt"):
        return "text/plain; charset=utf-8"
    return "application/octet-stream"


class S3Storage:
    def __init__(self) -> None:
        self.local_base = settings.recordings_dir
        self.bucket = settings.aws_s3_bucket
        self.folder = settings.aws_media_folder.strip("/")
        self.base_url = settings.aws_url.rstrip("/")
        self._s3 = _get_s3_client()
        if self._s3 is not None and not self.bucket:
            logger.warning("AWS_S3_BUCKET is empty; falling back to local URLs.")

    def _key(self, session_id: str, filename: str) -> str:
        if self.folder:
            return f"{self.folder}/{session_id}/{filename}"
        return f"{session_id}/{filename}"

    def _url(self, key: str) -> str:
        # AWS_MEDIA_FOLDER may contain spaces; URL-encode each segment.
        encoded = "/".join(quote(p) for p in key.split("/"))
        return f"{self.base_url}/{encoded}"

    async def write(self, session_id: str, filename: str, data: bytes) -> str:
        # Persist locally first — other code reads artifacts back from disk.
        target_dir = os.path.join(self.local_base, session_id)
        os.makedirs(target_dir, exist_ok=True)
        path = os.path.join(target_dir, filename)
        with open(path, "wb") as fh:
            fh.write(data)

        local_url = f"/recordings/{session_id}/{filename}"
        if not filename.lower().endswith(S3_UPLOAD_SUFFIXES):
            return local_url
        if self._s3 is None or not self.bucket:
            return local_url

        key = self._key(session_id, filename)
        try:
            await asyncio.to_thread(
                self._s3.put_object,
                Bucket=self.bucket,
                Key=key,
                Body=data,
                ContentType=_content_type(filename),
            )
            url = self._url(key)
            logger.info(
                f"S3 RECORDING UPLOADED [{session_id}] -> {url} "
                f"({len(data):,} bytes; s3://{self.bucket}/{key})"
            )
            return url
        except Exception as exc:
            logger.error(
                f"S3 upload failed for s3://{self.bucket}/{key}: {exc}; "
                "returning local URL instead."
            )
            return local_url
