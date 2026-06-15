"""End-of-call transcript artifacts: transcript.txt + transcript.json.

Ported from the old `storage/recap.py` — the transcript half only. The
WAV-merge half is gone: the Go recorder owns audio now (CONTRACTS §3), so the
brain writes pure text. Idempotent; produces transcripts regardless of state.
"""

from __future__ import annotations

import json
from typing import TYPE_CHECKING

from app.storage import storage

if TYPE_CHECKING:
    from app.services.session_manager import Session


def _format_transcript_txt(session: Session) -> str:
    lines: list[str] = [
        f"Session : {session.session_id}",
        f"Language: {session.language}",
        f"Started : {session.started_at}",
        f"Ended   : {session.ended_at or '-'}",
        f"State   : {session.flow.state}",
        "",
        "-" * 60,
        "",
    ]
    for entry in session.transcript:
        header = f"[{entry.get('ts', '')}] turn {entry.get('turn', '?')} / {entry.get('speaker', '?')}"
        if "intent" in entry:
            header += f" (intent={entry['intent']})"
        lines.append(header)
        lines.append("  " + (entry.get("text") or "").strip())
        lines.append("")
    return "\n".join(lines)


def _format_transcript_json(session: Session) -> str:
    payload = {
        "session_id": session.session_id,
        "language": session.language,
        "started_at": session.started_at,
        "ended_at": session.ended_at,
        "final_state": session.flow.state,
        "turns": session.transcript,
        "events": [
            {"event_type": e.event_type, "payload": e.payload}
            for e in session.events_history
        ],
    }
    return json.dumps(payload, ensure_ascii=False, indent=2)


async def finalize_call(session: Session) -> dict[str, str | None]:
    """Write transcript.txt + transcript.json and return their URLs."""
    sid = session.session_id
    txt_url = await storage.write(
        sid, "transcript.txt", _format_transcript_txt(session).encode("utf-8")
    )
    json_url = await storage.write(
        sid, "transcript.json", _format_transcript_json(session).encode("utf-8")
    )
    return {"transcript_url": txt_url, "transcript_json_url": json_url}
