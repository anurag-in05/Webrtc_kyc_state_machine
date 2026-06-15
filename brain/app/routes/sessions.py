"""HTTP surface for the session control plane (CONTRACTS §1).

Thin: I/O at the edges, state-machine work delegated to `turn_service`. No media
— a transcript comes in, agent text + a `tts_plan` go out. The brain never sees
audio; the gateway synthesizes/streams it and the recorder captures it.
"""

from __future__ import annotations

import httpx
from fastapi import APIRouter, HTTPException
from loguru import logger

from app.config import settings
from app.schemas.session import (
    EndSessionResponse,
    SessionSnapshot,
    StartSessionRequest,
    StartSessionResponse,
    TurnEvent,
    TurnRequest,
    TurnResponse,
)
from app.services.language_processor import language_preprocessor
from app.services.phrase_builder import build_phrases_for_session
from app.services.session_manager import Session, mark_session_ended, session_manager
from app.services.state_machine import Event
from app.services.turn_service import (
    classify_safely_async,
    finalize_turn,
    record_agent,
    status_from_state,
)
from app.services.tts_plan import build_tts_plan, resolve_model_id, resolve_voice_id
from app.storage.recap import finalize_call

router = APIRouter(prefix="/api/v1/sessions", tags=["sessions"])

# Best-effort calls to the Go services run with a short timeout — the brain must
# answer even when the gateway/recorder are down (e.g. curl tests).
_DOWNSTREAM_TIMEOUT = 2.0


def _events_to_schema(events: list[Event]) -> list[TurnEvent]:
    return [TurnEvent(event_type=e.event_type, payload=e.payload) for e in events]


async def _get_or_404(session_id: str) -> Session:
    session = await session_manager.get(session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session not found")
    return session


def _ice_servers() -> list[dict]:
    """STUN always; the configured TURN relay when set (needed off localhost)."""
    servers: list[dict] = [{"urls": "stun:stun.l.google.com:19302"}]
    if settings.turn_url:
        servers.append({
            "urls": settings.turn_url,
            "username": settings.turn_username,
            "credential": settings.turn_credential,
        })
    return servers


# ---------- POST /start -----------------------------------------------------
@router.post("/start", response_model=StartSessionResponse)
async def start_session(request: StartSessionRequest) -> StartSessionResponse:
    customer_data = request.model_dump()
    language = customer_data.pop("language")
    customer_data.pop("video_transport", None)  # media transport is the gateway's concern

    processed = await language_preprocessor.preprocess_customer_data(customer_data, language)
    resolved_phrases = build_phrases_for_session(language, customer_data, processed)

    session = await session_manager.create(
        language=language,
        customer_data=customer_data,
        processed_customer_data=processed,
        resolved_phrases=resolved_phrases,
        has_alt_mobile=bool(customer_data.get("alternative_mobile")),
    )

    # The greeting is turn 0: the agent speaks first.
    initial = session.flow.initial()
    agent_text = resolved_phrases[initial.agent_phrase_key]
    session.pending_agent_text[session.turn_index] = agent_text
    record_agent(session, session.turn_index, agent_text)

    logger.info(f"start[{session.session_id[:8]}] lang={language} state={session.flow.state}")
    return StartSessionResponse(
        session_id=session.session_id,
        state=session.flow.state,
        agent_text=agent_text,
        tts_plan=build_tts_plan(agent_text),
        voice_id=resolve_voice_id(language),
        model_id=resolve_model_id(language),
        language=language,
        turn_index=session.turn_index,
        ice_servers=_ice_servers(),
        gateway_offer_url=f"{settings.gateway_url}/sessions/{session.session_id}/offer",
        capture_width=settings.capture_width,
        capture_height=settings.capture_height,
        capture_fps=settings.capture_fps,
        video_enabled=settings.video_enabled,
    )


# ---------- POST /{sid}/turn  (transcript in, text + plan out) --------------
@router.post("/{session_id}/turn", response_model=TurnResponse)
async def session_turn(session_id: str, body: TurnRequest) -> TurnResponse:
    session = await _get_or_404(session_id)
    if session.flow.state in {"failed", "completed"}:
        raise HTTPException(status_code=410, detail=f"Session already {session.flow.state}")

    session.turn_index += 1
    # Any pipeline failure (empty/garbled transcript, intent error) folds to
    # please_repeat inside classify_safely_async — the call degrades, never breaks.
    intent = await classify_safely_async(body.transcript, session.language)
    payload = finalize_turn(
        session, transcript=body.transcript, intent=intent, user_audio_url=""
    )

    agent_text = payload["agent_text"]
    return TurnResponse(
        session_id=session.session_id,
        state=payload["state"],
        intent=payload["intent"],
        transcript=payload["transcript"],
        agent_text=agent_text,
        tts_plan=build_tts_plan(agent_text),
        voice_id=resolve_voice_id(session.language),
        model_id=resolve_model_id(session.language),
        turn_index=payload["turn_index"],
        attempt_count=payload["attempt_count"],
        events=[TurnEvent(**e) for e in payload["events"]],
        status=payload["status"],
    )


# ---------- GET /{sid}  (snapshot; gateway bootstrap + browser poll) --------
@router.get("/{session_id}", response_model=SessionSnapshot)
async def get_session(session_id: str) -> SessionSnapshot:
    session = await _get_or_404(session_id)
    agent_text = session.pending_agent_text.get(session.turn_index, "")
    recording_status, video_url = await _recording_status(session)
    return SessionSnapshot(
        session_id=session.session_id,
        state=session.flow.state,
        language=session.language,
        turn_index=session.turn_index,
        agent_text=agent_text,
        tts_plan=build_tts_plan(agent_text),
        voice_id=resolve_voice_id(session.language),
        model_id=resolve_model_id(session.language),
        status=status_from_state(session.flow.state),
        events=_events_to_schema(session.events_history),
        recording_status=recording_status,
        full_call_video_url=video_url,
    )


# ---------- POST /{sid}/end -------------------------------------------------
@router.post("/{session_id}/end", response_model=EndSessionResponse)
async def end_session(session_id: str) -> EndSessionResponse:
    session = await _get_or_404(session_id)

    # Force a terminal state if the flow didn't reach one, then stamp the ended
    # transition exactly once (idempotent against a double /end).
    if session.flow.state not in {"completed", "failed"}:
        session.flow.state = "failed"
    first_end = mark_session_ended(session)

    artifacts: dict = {}
    if settings.recordings_enabled:
        try:
            artifacts = await finalize_call(session)  # transcript.txt + .json
        except Exception as exc:
            logger.exception(f"transcript write failed for {session.session_id}: {exc}")
        if first_end:
            session.recording_status = "pending"  # recorder finalizes; GET proxies it
    else:
        session.recording_status = "disabled"

    # The gateway closes the peer, flushes the recorder, and triggers finalize.
    await _gateway_close(session_id)

    return EndSessionResponse(
        session_id=session.session_id,
        state=session.flow.state,
        turn_index=session.turn_index,
        transcript_url=artifacts.get("transcript_url"),
        transcript_json_url=artifacts.get("transcript_json_url"),
        recording_status=session.recording_status,
    )


# ---------- best-effort downstream calls ------------------------------------
async def _gateway_close(session_id: str) -> None:
    """Tell the gateway to tear down (CONTRACTS §2). Best-effort: a missing
    gateway must not fail /end."""
    url = f"{settings.gateway_url}/sessions/{session_id}/close"
    try:
        async with httpx.AsyncClient(timeout=_DOWNSTREAM_TIMEOUT) as client:
            await client.post(url)
    except Exception as exc:
        logger.warning(f"gateway /close unreachable for {session_id[:8]}: {exc}")


async def _recording_status(session: Session) -> tuple[str, str | None]:
    """Proxy the recorder's finalize status (CONTRACTS §3) once the call has
    ended; fall back to the session's last-known status when it's unreachable."""
    if session.ended_at is None:
        return session.recording_status, session.full_call_video_url
    url = f"{settings.recorder_url}/sessions/{session.session_id}/status"
    try:
        async with httpx.AsyncClient(timeout=_DOWNSTREAM_TIMEOUT) as client:
            resp = await client.get(url)
            resp.raise_for_status()
            data = resp.json()
            return data.get("status", session.recording_status), data.get("url")
    except Exception:
        return session.recording_status, session.full_call_video_url
