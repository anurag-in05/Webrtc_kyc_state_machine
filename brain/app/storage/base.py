"""Storage-backend protocol implemented by LocalFilesystemStorage and S3Storage.
The factory in `app.storage.__init__` picks one; callers see only this shape.
"""

from typing import Protocol


class StorageBackend(Protocol):
    async def write(self, session_id: str, filename: str, data: bytes) -> str:
        """Write bytes for `session_id`/`filename` and return a URL the client can GET."""
        ...
