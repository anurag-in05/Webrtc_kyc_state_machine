"""Build the gateway's `tts_plan` from agent text — the split half of the old
`pipeline/tts.py:synthesize_stream`, with no HTTP and no audio (CONTRACTS §4).

The brain decides the segments + pacing; the gateway calls ElevenLabs. A
`<var>...</var>` span (digits / ids) becomes a slow, comma-spaced segment
bracketed by 200 ms silences; everything else is normal-pace speech.
"""

from __future__ import annotations

import re

from app.config import settings
from app.services.i18n import get_pack

# ElevenLabs defaults (mirror the old tts.py); a pack value or env var overrides
# per language.
DEFAULT_VOICE_ID = "21m00Tcm4TlvDq8ikWAM"
DEFAULT_MODEL = "eleven_flash_v2_5"

_VAR_SPLIT = re.compile(r"(<var>.*?</var>)", re.DOTALL)
_VAR_TAGS = re.compile(r"</?var>")
_SILENCE_MS = 200


def _spoken_len(text: str) -> int:
    """Length of the text actually voiced (var tags stripped)."""
    return len(_VAR_TAGS.sub("", text or ""))


def _speed_for(text: str) -> float:
    """A little faster for long phrases, normal otherwise."""
    if _spoken_len(text) >= settings.tts_long_phrase_chars:
        return settings.tts_speed_long
    return settings.tts_speed


def _slow_down_var_text(raw: str) -> str:
    """Join words with commas (micro-pauses) + a trailing period so the <var>
    segment is read slowly. Empty/whitespace tokens dropped."""
    words = [w for w in raw.split() if w]
    return ", ".join(words) + "." if words else ""


def build_tts_plan(agent_text: str) -> list[dict]:
    """Ordered list of speech/silence segments for `agent_text` (CONTRACTS §4)."""
    if not agent_text:
        return []
    body_speed = _speed_for(agent_text)
    plan: list[dict] = []
    for part in _VAR_SPLIT.split(agent_text):
        if not part:
            continue
        is_var = part.startswith("<var>") and part.endswith("</var>")
        clean = _VAR_TAGS.sub("", part).strip()
        if not clean:
            continue
        if is_var:
            spoken = _slow_down_var_text(clean)
            if not spoken:
                continue
            plan.append({"kind": "silence", "ms": _SILENCE_MS})
            plan.append({"kind": "speech", "text": spoken, "slow": True})
            plan.append({"kind": "silence", "ms": _SILENCE_MS})
        else:
            plan.append({"kind": "speech", "text": clean, "slow": False, "speed": body_speed})
    return plan


def resolve_voice_id(language: str) -> str:
    """Pack voice → ELEVENLABS_VOICE_ID env → ElevenLabs default."""
    try:
        pack_voice = get_pack(language).voice_id
    except NotImplementedError:
        pack_voice = None
    return pack_voice or settings.elevenlabs_voice_id or DEFAULT_VOICE_ID


def resolve_model_id(language: str) -> str:
    """Pack model → ELEVENLABS_MODEL_ID env → ElevenLabs default."""
    try:
        pack_model = get_pack(language).model_id
    except NotImplementedError:
        pack_model = None
    return pack_model or settings.elevenlabs_model_id or DEFAULT_MODEL
