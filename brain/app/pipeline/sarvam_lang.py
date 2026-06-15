"""Map our internal language names to Sarvam BCP-47 codes."""

from __future__ import annotations

_MAP = {
    "english": "en-IN", "en": "en-IN",
    "hindi": "hi-IN", "hi": "hi-IN",
    "bengali": "bn-IN", "bangla": "bn-IN", "bn": "bn-IN",
    "gujarati": "gu-IN", "gu": "gu-IN",
    "kannada": "kn-IN", "kn": "kn-IN",
    "malayalam": "ml-IN", "ml": "ml-IN",
    "marathi": "mr-IN", "mr": "mr-IN",
    "odia": "od-IN", "oriya": "od-IN", "od": "od-IN",
    "punjabi": "pa-IN", "pa": "pa-IN",
    "tamil": "ta-IN", "ta": "ta-IN",
    "telugu": "te-IN", "te": "te-IN",
}


def get_sarvam_language_code(language: str) -> str:
    return _MAP.get((language or "").lower().strip(), "en-IN")
