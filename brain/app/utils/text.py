"""Text-scrubbing helpers for TTS input and transcript persistence."""

from __future__ import annotations

import re

_VAR_TAG_RE = re.compile(r"</?var>")


def strip_var_markup(text: str) -> str:
    """Remove `<var>` / `</var>` tags used as per-segment TTS hints.

    Streaming TTS uses one voice-settings block per request, so per-segment
    overrides aren't honoured; drop the markup before synthesis and persistence.
    """
    if not text:
        return ""
    return _VAR_TAG_RE.sub("", text).strip()
