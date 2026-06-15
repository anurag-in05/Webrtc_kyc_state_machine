"""Language pack registry.

Add a new language by:
  1. Create `app/services/i18n/<lang>.py` exposing a class with the
     `LanguagePack` Protocol (see `base.py`).
  2. Register it in `_PACKS` below.
Nothing else in the codebase needs to change.
"""

from __future__ import annotations

from app.services.i18n.base import LanguagePack
from app.services.i18n.english import EnglishPack
from app.services.i18n.hindi import HindiPack


_PACKS: dict[str, LanguagePack] = {
    "english": EnglishPack(),
    "en": EnglishPack(),
    "hindi": HindiPack(),
    "hi": HindiPack(),
}


def get_pack(language: str) -> LanguagePack:
    lang = (language or "english").lower().strip()
    if lang not in _PACKS:
        raise NotImplementedError(
            f"Language '{lang}' not implemented. "
            f"Available: {sorted(set(p.name for p in _PACKS.values()))}"
        )
    return _PACKS[lang]


def supported_languages() -> list[str]:
    return sorted(set(p.name for p in _PACKS.values()))


__all__ = ["LanguagePack", "get_pack", "supported_languages"]
