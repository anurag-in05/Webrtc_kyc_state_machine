"""Customer-data preprocessor.

Transliterates name fields and address to the target language's native script
via OpenAI gpt-4o-mini. Email/numbers are deterministic (i18n packs) and left
untouched. English or no OPENAI_API_KEY -> pass-through that stringifies values.
Signature mirrors the LiveKit reference's preprocess_customer_data.
"""

from __future__ import annotations

import asyncio
import os
from typing import Any

from loguru import logger

from app.services.i18n import supported_languages


NAME_FIELDS = (
    "customer_name",
    "insured_name",
    "nominee_name",
    "company",
    "plan_name",
)
ADDRESS_FIELD = "address"


def _get_openai_client():
    """Lazy-init the AsyncOpenAI client; return None if no key."""
    try:
        from openai import AsyncOpenAI  # type: ignore
    except ImportError:
        logger.warning("openai package not installed; preprocessing disabled.")
        return None
    api_key = os.getenv("OPENAI_API_KEY", "")
    if not api_key:
        logger.warning(
            "OPENAI_API_KEY not set; name/address transliteration disabled."
        )
        return None
    return AsyncOpenAI(api_key=api_key)


_SCRIPT_NAMES = {
    "hindi": "Devanagari script",
    "gujarati": "Gujarati script",
    "kannada": "Kannada script",
    "bengali": "Bengali script",
    "tamil": "Tamil script",
    "telugu": "Telugu script",
}


def _name_prompt(name: str, language: str) -> str:
    script = _SCRIPT_NAMES.get(language, language)
    return (
        f'Convert the English name/text "{name}" to {script} for natural '
        f'pronunciation in {language} language.\n\n'
        f'Rules:\n'
        f'- Convert to the appropriate script for {language}\n'
        f'- For person names: maintain the original pronunciation as closely as possible\n'
        f'- For company names: convert to native script (e.g. "Max Bupa" -> "मैक्स बूपा" for Hindi)\n'
        f'- For plan names: convert to native script while keeping common English terms if widely used\n'
        f'- For abbreviations like Mohd, expand to full form (e.g. "Mohd" -> "मोहम्मद" for Hindi)\n'
        f'- Return ONLY the converted text, no explanations\n\n'
        f'Convert: "{name}"'
    )


def _address_prompt(addr: str, language: str) -> str:
    script = _SCRIPT_NAMES.get(language, language)
    return (
        f'Convert the address "{addr}" to {script} for {language} language.\n\n'
        f'Rules:\n'
        f'- Convert names and place names to {script}\n'
        f'- Convert numbers to words in {language}\n'
        f'- EXCEPT for the postal/pincode, which must be spoken digit by digit\n'
        f'  (e.g. 110096 -> "one one zero zero nine six" in the target language script)\n'
        f'- Convert "S/O" to the {language} equivalent of "son of"\n'
        f'- Convert "C/O" to the {language} equivalent of "care of"\n'
        f'- Convert "D/O" to the {language} equivalent of "daughter of"\n'
        f'- Keep English words like "Street", "Road", "Apartment" as-is\n'
        f'- If already in target script, just adjust s/o, c/o, d/o; otherwise return as-is\n'
        f'- Return ONLY the converted address, no explanations\n\n'
        f'Convert: "{addr}"'
    )


class LanguagePreprocessor:
    def __init__(self) -> None:
        self._client = _get_openai_client()

    async def preprocess_customer_data(
        self, customer_data: dict[str, Any], language: str
    ) -> dict[str, str]:
        language = (language or "english").lower()
        if language not in supported_languages():
            raise NotImplementedError(
                f"Language '{language}' has no i18n pack. "
                f"Available: {supported_languages()}"
            )

        processed: dict[str, str] = {
            k: ("" if v is None else str(v)) for k, v in customer_data.items()
        }

        # English doesn't need script conversion. Same if no OpenAI client.
        if language == "english" or self._client is None:
            return processed

        # Collect fields that have non-empty values and need LLM conversion.
        tasks: list = []
        keys: list[str] = []
        for field in NAME_FIELDS:
            if processed.get(field):
                tasks.append(self._convert_name(processed[field], language))
                keys.append(field)
        if processed.get(ADDRESS_FIELD):
            tasks.append(self._convert_address(processed[ADDRESS_FIELD], language))
            keys.append(ADDRESS_FIELD)

        if not tasks:
            return processed

        results = await asyncio.gather(*tasks, return_exceptions=True)
        for key, result in zip(keys, results, strict=False):
            if isinstance(result, Exception):
                logger.warning(
                    f"Preprocess {key} failed ({type(result).__name__}: {result}); "
                    "keeping original value."
                )
                continue
            processed[key] = result
        return processed

    async def _convert_name(self, name: str, language: str) -> str:
        try:
            resp = await self._client.chat.completions.create(  # type: ignore[union-attr]
                model="gpt-4o-mini",
                messages=[
                    {
                        "role": "system",
                        "content": (
                            "You are a language conversion expert. Convert the "
                            "given name to the target language script for natural "
                            "pronunciation. Return ONLY the converted name, nothing else."
                        ),
                    },
                    {"role": "user", "content": _name_prompt(name, language)},
                ],
                temperature=0.1,
                max_tokens=150,
            )
            converted = (resp.choices[0].message.content or "").strip()
            logger.info(f"Name '{name}' -> '{converted}' [{language}]")
            return converted or name
        except Exception as exc:
            logger.error(f"OpenAI name conversion failed for '{name}': {exc}")
            return name

    async def _convert_address(self, addr: str, language: str) -> str:
        try:
            resp = await self._client.chat.completions.create(  # type: ignore[union-attr]
                model="gpt-4o-mini",
                messages=[
                    {
                        "role": "system",
                        "content": (
                            "You are an address conversion expert. Convert the "
                            "address to the target language script, keeping numbers "
                            "as words. Return ONLY the converted address, nothing else."
                        ),
                    },
                    {"role": "user", "content": _address_prompt(addr, language)},
                ],
                temperature=0.1,
                max_tokens=300,
            )
            converted = (resp.choices[0].message.content or "").strip()
            logger.info(f"Address '{addr}' -> '{converted}' [{language}]")
            return converted or addr
        except Exception as exc:
            logger.error(f"OpenAI address conversion failed for '{addr}': {exc}")
            return addr


language_preprocessor = LanguagePreprocessor()
