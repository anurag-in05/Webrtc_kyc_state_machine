"""Resolve all `{{...}}` placeholders into a flat state -> ready-to-speak dict.

The state machine and routes never see placeholders. Language-specific content
lives in `app/services/i18n/<lang>.py`.
"""

from __future__ import annotations

from typing import Any

from app.services.i18n import get_pack


def build_phrases_for_session(
    language: str,
    customer_data: dict[str, Any],
    processed_customer_data: dict[str, Any],
) -> dict[str, str]:
    pack = get_pack(language)

    phone = pack.format_phone(customer_data.get("primary_mobile", ""))
    alt_phone = pack.format_phone(customer_data.get("alternative_mobile", ""))
    email_tts = pack.format_email(processed_customer_data.get("email", ""))
    address_tts = pack.format_address(processed_customer_data.get("address", ""))

    processed_for_decl = dict(processed_customer_data)
    for key in ("policy_term", "sum_insured", "premium_amount", "premium_paying_term"):
        processed_for_decl[key] = pack.ensure_number_words(processed_for_decl.get(key, ""))
    for key in ("application_date", "dob_life_assured"):
        processed_for_decl[key] = pack.format_date(processed_for_decl.get(key, ""))
    declaration_text = pack.build_declaration(processed_for_decl)

    repl = {
        "{{CUSTOMER_NAME}}": pack.name_with_title(processed_customer_data),
        "{{COMPANY_NAME}}": processed_customer_data.get("company", ""),
        "{{EMAIL_TTS}}": email_tts,
        "{{ADDRESS_TTS}}": address_tts,
        "{{DECLARATION_TEXT}}": declaration_text,
        "{{TIME_BASED_GREETING}}": pack.time_based_greeting(),
    }

    # Phone/alt-phone placeholders are tagged by language for historical reasons
    # (one template per language). All tags resolve to the same formatted string.
    _PHONE_LANGS = ("ENGLISH", "HINDI", "GUJARATI", "KANNADA", "BENGALI", "TAMIL", "TELUGU")
    for lang in _PHONE_LANGS:
        repl[f"{{{{PHONE_{lang}}}}}"] = phone
        repl[f"{{{{ALT_PHONE_{lang}}}}}"] = alt_phone

    resolved: dict[str, str] = {}
    for state, template in pack.phrases.items():
        text = template
        for placeholder, value in repl.items():
            text = text.replace(placeholder, value)
        resolved[state] = text

    return resolved
