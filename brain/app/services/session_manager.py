"""Per-call session state and an async-safe uuid -> Session map.

A `Session` holds the language pack, resolved phrases, state-machine instance,
transcript, and recording state. `SessionManager` is a local registry with an
optional external store (see SessionManager docstring).
"""

from __future__ import annotations

import asyncio
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import TYPE_CHECKING

from loguru import logger

from app.services import metrics

# Bound the inbound STT PCM buffer: 400 x 20ms frames @16kHz mono s16 ~= 8s of
# speech. Beyond that, drop oldest frame (backpressure) rather than grow unbound.
STT_QUEUE_MAX = 400

# E402: import below STT_QUEUE_MAX is intentional — keeps the buffer constant
# documented next to the comment above; state_machine has no circular concern.
from app.services.state_machine import ConsentVerificationFlow, Event

if TYPE_CHECKING:
    from app.services.webrtc_video import WebRTCPeerSession


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
    audio_files: list[str] = field(default_factory=list)
    pending_agent_text: dict[int, str] = field(default_factory=dict)
    # Chronological spoken turns; each: {turn, speaker, text, ts, intent?}
    transcript: list[dict] = field(default_factory=list)
    started_at: str = field(default_factory=_now_iso)
    ended_at: str | None = None

    # --- Video recording ---
    # Raw WebM appended live; None until first chunk.
    video_recording_path: str | None = None
    video_bytes: int = 0  # telemetry
    video_chunks_received: int = 0  # telemetry
    video_started_at: str | None = None  # first-chunk wall-clock
    recording_status: str = "pending"  # flipped by the mux task
    full_call_video_url: str | None = None  # final MP4 URL, set on mux success

    # --- Zero-transcode H.264 passthrough ---
    # When passthrough is active, video_recording_path is the video-ONLY
    # video_raw.mp4 and the stereo audio mix lands in a SEPARATE audio.m4a here;
    # the mux combines the two with -c copy. None when audio is inline (transcode
    # / VP8 fallback).
    audio_recording_path: str | None = None
    # A/V start-offset (s) at teardown = audio_first - video_first wall-clock.
    # Mux applies it via -itsoffset for lip-sync. Positive = audio started later.
    # None = not measured (both align at PTS 0).
    av_sync_offset_s: float | None = None

    # Live aiortc peer when video_transport=="webrtc"; None for ws. Opaque
    # outside webrtc_video.py and the /offer + /end routes.
    webrtc_peer: WebRTCPeerSession | None = None

    # --- Unified-WebRTC media runtime ---
    # Inbound mic PCM (16kHz mono s16le) decoded off the WebRTC track by
    # pump_to_stt; the turn loop drains it into a fresh Sarvam stream. None until
    # the audio track attaches.
    stt_source: asyncio.Queue[bytes] | None = None
    # time.monotonic() when the synced recorder started; lets /stream/{turn}
    # record each agent turn's play offset for the mux.
    recording_started_monotonic: float | None = None
    # turn_index -> seconds-from-recording-start the agent turn began playing.
    agent_audio_offsets: dict[int, float] = field(default_factory=dict)
    # Recorder strategy (mirrors settings.mux_mode at /offer). Mux uses it to pick
    # cheap remux (inline_stereo/off) vs overlaying agent voice from per-turn WAVs
    # — channel count can't distinguish them since aiortc always decodes to stereo.
    recording_mode: str | None = None

    # --- Session-lifecycle telemetry (defaulted, appended last so no non-default
    # field follows a defaulted one). ---
    # monotonic at create() for the duration histogram (immune to clock jumps).
    started_monotonic: float = field(default_factory=time.monotonic)
    # monotonic at first /end; mux measures POST /end -> terminal-status latency.
    ended_monotonic: float | None = None
    # Wall-clock epoch at create(). Crosses processes (monotonic doesn't) so a
    # rehydrated session ending on another worker still gets a truthful duration.
    started_epoch: float = field(default_factory=time.time)




class SessionManager:
    """uuid -> Session map: local runtime registry + optional external store.

    The local dict holds full runtime sessions (media included) for calls this
    worker owns. With an external store (REDIS_URL), serialisable state is
    mirrored so any worker can answer reads and the media owner can be signalled
    cross-process. With the default in-memory store, behaviour is unchanged.
    """

    def __init__(self) -> None:
        self._sessions: dict[str, Session] = {}
        self._lock = asyncio.Lock()

    @property
    def _store(self):
        from app.services.session_store import get_store  # local: avoid cycle
        return get_store()

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
        await self.persist(session)
        return session

    async def get(self, session_id: str) -> Session | None:
        """Look up a session: local registry first, then external store.

        A remote hit returns a rehydrated read-view (no media, not registered
        locally). A local hit whose media is owned by another worker is
        refreshed from the store first to avoid serving stale turn state.
        """
        async with self._lock:
            session = self._sessions.get(session_id)
        store = self._store
        if session is not None:
            if store.is_external and session.webrtc_peer is None:
                owner = await store.owner(session_id)
                from app.services.session_store import WORKER_ID, apply_snapshot
                if owner is not None and owner != WORKER_ID:
                    snap = await store.load(session_id)
                    if snap:
                        apply_snapshot(session, snap)
            return session
        snap = await store.load(session_id)
        if snap is None:
            return None
        from app.services.session_store import rehydrate
        return rehydrate(snap)

    async def claim_for_media(self, session_id: str) -> Session | None:
        """Resolve the session for `/offer`, claiming media ownership.

        Local: claim (idempotent for the same worker) and return. Remote: claim
        atomically, then rehydrate the full session into the local registry.
        Returns None when another worker already owns the media (caller -> HTTP
        error rather than a second peer).
        """
        store = self._store
        async with self._lock:
            session = self._sessions.get(session_id)
        if session is not None:
            return session if await store.claim_owner(session_id) else None
        snap = await store.load(session_id)
        if snap is None:
            return None
        if not await store.claim_owner(session_id):
            logger.warning(
                f"offer[{session_id[:8]}]: media owned by another worker "
                f"(owner={await store.owner(session_id)})"
            )
            return None
        from app.services.session_store import rehydrate
        session = rehydrate(snap)
        async with self._lock:
            self._sessions[session_id] = session
        return session

    async def persist(self, session: Session) -> None:
        """Write the snapshot to the external store. No-op for in-memory.
        Never raises — a Redis blip must not fail a turn; the local copy stays
        authoritative on the owner."""
        store = self._store
        if not store.is_external:
            return
        try:
            from app.services.session_store import snapshot
            await store.save(
                session.session_id, snapshot(session),
                ended=session.ended_at is not None,
            )
        except Exception as exc:
            logger.warning(f"session persist failed for {session.session_id[:8]}: {exc}")

    def persist_soon(self, session: Session) -> None:
        """Fire-and-forget persist() for sync call sites (turn loop, recorder
        teardown). Write ordering isn't guaranteed, but each carries the full
        snapshot so last-write is always consistent."""
        if self._store.is_external:
            asyncio.ensure_future(self.persist(session))

    async def is_local(self, session_id: str) -> bool:
        """True when THIS worker holds the runtime session (registry hit)."""
        async with self._lock:
            return session_id in self._sessions

    def get_local(self, session_id: str) -> Session | None:
        """Registry-only lookup (no store fallback). For the pub/sub end handler,
        which acts only on sessions this worker owns."""
        return self._sessions.get(session_id)

    def live_sessions(self) -> list[Session]:
        """Snapshot of locally-tracked sessions for the STT queue-depth monitor
        (read-only)."""
        return list(self._sessions.values())


def mark_session_ended(session: Session) -> bool:
    """Record the session's first ended transition exactly once.

    Returns True only if THIS call did the ended_at None->set transition, so the
    caller can gate one-shot post-call work (mux + telemetry) on it. Guarding the
    transition stops a double POST /end (or /end racing the disconnect path) from
    double-decrementing videokyc_active_sessions or double-observing duration.
    """
    if session.ended_at is not None:
        return False  # already ended once — idempotent
    session.ended_at = _now_iso()
    session.ended_monotonic = time.monotonic()
    duration = max(0.0, session.ended_monotonic - session.started_monotonic)
    metrics.session_ended(duration)
    return True


session_manager = SessionManager()
