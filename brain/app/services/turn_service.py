"""Per-turn orchestration shared by the REST and WebSocket entry points.

Given a final user transcript: classify intent, advance the state machine,
record both sides, build the response payload. Stays HTTP-agnostic (returns
plain dicts); the route layer shapes them into FastAPI/WS responses.
"""

from __future__ import annotations

from datetime import datetime, timezone

from app.config import settings
from app.services import metrics
from app.services.session_manager import Session
from app.services.state_machine import Event
from app.utils.text import strip_var_markup

AGENT_AUDIO_FORMAT = "pcm_s16le"
AGENT_AUDIO_SAMPLE_RATE = 24_000  # mirrors app.pipeline.tts.STREAM_SAMPLE_RATE


def now_iso() -> str:
    """ISO-8601 timestamp with second precision, in local timezone."""
    return datetime.now(timezone.utc).astimezone().isoformat(timespec="seconds")


def stream_url(session_id: str, turn_index: int) -> str:
    return f"/api/v1/sessions/{session_id}/stream/{turn_index}"


def status_from_state(state: str) -> str:
    if state in {"completed", "failed"}:
        return state
    return "active"


def events_to_payload(events: list[Event]) -> list[dict]:
    return [{"event_type": e.event_type, "payload": e.payload} for e in events]


def record_agent(session: Session, turn: int, text: str) -> None:
    session.transcript.append({
        "turn": turn,
        "speaker": "agent",
        "text": strip_var_markup(text or ""),
        "ts": now_iso(),
    })


def record_user(session: Session, turn: int, text: str, intent: str) -> None:
    session.transcript.append({
        "turn": turn,
        "speaker": "user",
        "text": (text or "").strip(),
        "intent": intent,
        "ts": now_iso(),
    })


# Cached remote client (connection pooling). Only built when delegating.
_remote_classifier = None


def _get_classifier():
    """Remote HTTP client when `intent_service_url` is set, else the in-process
    ensemble. The in-process classifier is imported LAZILY to avoid pulling in
    torch / the ~1.5 GB model when delegating. Same classify/classify_async
    surface. The container always sets INTENT_SERVICE_URL; the local fallback
    only exists for dev (services/ isn't shipped in the app image)."""
    global _remote_classifier
    if settings.intent_service_url:
        if _remote_classifier is None:
            from app.pipeline.intent_client import RemoteIntentClassifier
            _remote_classifier = RemoteIntentClassifier(
                settings.intent_service_url, settings.intent_service_timeout
            )
        return _remote_classifier
    from services.intent.classifier import intent_classifier
    return intent_classifier


async def classify_safely_async(transcript: str, language: str) -> str:
    """Classify with any failure folding to please_repeat. Runs the CPU-bound
    ensemble off the event loop (or remote), so the media loop never stalls on
    classification (Fix 1)."""
    transcript = (transcript or "").strip()
    if not transcript:
        return "please_repeat"
    try:
        return await _get_classifier().classify_async(transcript, language)
    except Exception:  # pragma: no cover — defensive fallback
        # Genuine intent failure folding to please_repeat (invariant #5).
        metrics.inc_intent_failure()
        return "please_repeat"


def finalize_turn(
    session: Session,
    *,
    transcript: str,
    intent: str,
    user_audio_url: str,
) -> dict:
    """Advance state, record user/agent turns, return a response-shaped dict.

    `session.turn_index` must already be incremented to the user's turn
    before calling this.
    """
    record_user(session, session.turn_index, transcript, intent)
    step = session.flow.step(intent)

    if step.next_state == "completed":
        # closing phrase already spoken last turn; nothing left to say here.
        agent_text = ""
        agent_stream = ""
    else:
        agent_text = session.resolved_phrases.get(step.agent_phrase_key, "")
        session.pending_agent_text[session.turn_index] = agent_text
        agent_stream = stream_url(session.session_id, session.turn_index)

    if agent_text:
        record_agent(session, session.turn_index, agent_text)
    session.events_history.extend(step.events)

    return {
        "session_id": session.session_id,
        "state": session.flow.state,
        "intent": intent,
        "transcript": transcript,
        "agent_text": agent_text,
        "agent_audio_stream_url": agent_stream,
        "agent_audio_sample_rate": AGENT_AUDIO_SAMPLE_RATE,
        "agent_audio_format": AGENT_AUDIO_FORMAT,
        "user_audio_url": user_audio_url,
        "turn_index": session.turn_index,
        "attempt_count": step.attempt_count,
        "events": events_to_payload(step.events),
        "status": status_from_state(session.flow.state),
    }
