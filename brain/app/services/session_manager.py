"""Per-call session state and a uuid -> Session map (in-memory, single process).

The brain is the text-only control plane, so a `Session` holds only text state:
the resolved phrases, the state-machine instance, the transcript, and the
recording status it relays from the recorder — no media. The registry is a plain
dict behind an asyncio lock. The old Redis-mirrored multi-worker store (claim /
persist / snapshot / rehydrate) is gone: it solved a coordination problem a
single-process text API doesn't have, and re-adding a shared store later is an
isolated change.
"""

from __future__ import annotations

import asyncio
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone

from app.services import metrics
from app.services.state_machine import ConsentVerificationFlow, Event


def _now_iso() -> str:
    return datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds")


@dataclass
class Session:
    session_id: str
    language: str
    customer_data: dict
    processed_customer_data: dict
    resolved_phrases: dict[str, str]
    flow: ConsentVerificationFlow
    turn_index: int = 0
    events_history: list[Event] = field(default_factory=list)
    # turn_index -> the agent text for that turn; the source for its tts_plan.
    pending_agent_text: dict[int, str] = field(default_factory=dict)
    # Chronological spoken turns; each: {turn, speaker, text, ts, intent?}.
    transcript: list[dict] = field(default_factory=list)
    started_at: str = field(default_factory=_now_iso)
    ended_at: str | None = None
    # Recording is owned by the Go recorder; the brain only relays its status.
    recording_status: str = "pending"
    full_call_video_url: str | None = None


class SessionManager:
    """uuid -> Session, in-memory."""

    def __init__(self) -> None:
        self._sessions: dict[str, Session] = {}
        self._lock = asyncio.Lock()

    async def create(
        self,
        *,
        language: str,
        customer_data: dict,
        processed_customer_data: dict,
        resolved_phrases: dict[str, str],
        has_alt_mobile: bool,
    ) -> Session:
        """Create a fresh session with a uuid4 id and return it."""
        async with self._lock:
            session_id = uuid.uuid4().hex
            session = Session(
                session_id=session_id,
                language=language,
                customer_data=customer_data,
                processed_customer_data=processed_customer_data,
                resolved_phrases=resolved_phrases,
                flow=ConsentVerificationFlow(has_alt_mobile=has_alt_mobile),
            )
            self._sessions[session_id] = session
            metrics.session_started()
        return session

    async def get(self, session_id: str) -> Session | None:
        async with self._lock:
            return self._sessions.get(session_id)


def mark_session_ended(session: Session) -> bool:
    """Set `ended_at` exactly once. Returns True only on the first transition, so
    the caller can gate one-shot end work (idempotent against a double POST /end
    or /end racing a disconnect)."""
    if session.ended_at is not None:
        return False
    session.ended_at = _now_iso()
    metrics.session_ended()
    return True


session_manager = SessionManager()
