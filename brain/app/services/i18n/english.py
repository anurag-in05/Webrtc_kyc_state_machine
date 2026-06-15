"""English language pack: phrases, declaration template, and the
digit/number/phone/email/address/date formatters."""

from __future__ import annotations

import re
from datetime import datetime
from typing import Any

try:
    from zoneinfo import ZoneInfo
except ImportError:
    ZoneInfo = None  # type: ignore


DIGIT_WORDS = {
    "0": "zero", "1": "one", "2": "two", "3": "three", "4": "four",
    "5": "five", "6": "six", "7": "seven", "8": "eight", "9": "nine",
}


_FREQUENCY_SYNONYMS = {
    "monthly": {"monthly", "per month", "every month"},
    "quarterly": {"quarterly", "quaterly", "every three months",
                  "once in three months", "three months once"},
    "half_yearly": {"half yearly", "half-yearly", "halfyearly",
                    "biyearly", "bi yearly", "bi-yearly", "biannual",
                    "bi annual", "every six months", "twice a year",
                    "two times in a year"},
    "yearly": {"yearly", "annual", "annually", "once a year", "per year"},
    "single": {"one time", "one-time", "single", "single premium",
               "one off", "lumpsum", "lump sum"},
}


def _normalize_frequency(freq_raw: str) -> str:
    if not freq_raw:
        return ""
    f = freq_raw.strip().lower().replace("_", " ").replace("-", " ")
    f = re.sub(r"\s+", " ", f)
    for canonical, synonyms in _FREQUENCY_SYNONYMS.items():
        if f in synonyms:
            return canonical
    return f


_EN_FREQUENCY_PHRASES = {
    "monthly": "every month",
    "quarterly": "every three months",
    "half_yearly": "twice a year",
    "yearly": "every year",
    "single": "a single payment",
}


PHRASES: dict[str, str] = {
    "greeting": "{{TIME_BASED_GREETING}}, This is Kavya calling from बजाज Capital. Am I speaking with {{CUSTOMER_NAME}}?",

    "congrats": (
        "{{CUSTOMER_NAME}}, Congratulations! You've made a very wise and thoughtful "
        "decision for securing your and your family's future. A warm welcome to the "
        "Bajaj Capital family! "
        "For the past sixty years, we've been providing tailored investment and "
        "financial solutions to people across the country, meeting their diverse "
        "needs. Today, we're privileged to have earned the trust of lakhs of clients "
        "nationwide. Our mission is to deliver personalized advice, exceptional "
        "service, and dedicated attention to each and every client. "
        "You've chosen a policy from {{COMPANY_NAME}}, one of the premier insurance "
        "providers in India. Through this, you've secured your and your family's "
        "future ensuring peace of mind for your loved ones. "
        "This call is being made to share information about your policy. For quality "
        "and training purposes, this call is also being recorded. Shall we start?"
    ),

    "declaration_intro": "Now I will read your policy information to you. Please listen carefully.",
    "declaration_text": "{{DECLARATION_TEXT}}",

    "verification_intro": (
        "Thank you. Now, I would like to confirm some important personal details "
        "with you. Please respond with Yes or No."
    ),

    "q_mobile": "Is your mobile number <var>{{PHONE_ENGLISH}}</var>?",
    "q_alt_mobile": "Is your alternate mobile number <var>{{ALT_PHONE_ENGLISH}}</var>?",
    "q_email": "Is your email address <var>{{EMAIL_TTS}}</var>?",
    "q_address": "Is your address <var>{{ADDRESS_TTS}}</var>?",

    "q_medical": (
        "I hope the life assured is in perfect health and not suffering from any "
        "serious illness at this time. Is that correct?"
    ),
    "q_no_merge": (
        "Hope you are aware that this is a new life insurance policy and this "
        "policy cannot be merged with any of your old life insurance policies. "
        "Also benefits of any of your old life insurance policy cannot be "
        "transferred or merged with this new life insurance policy?"
    ),
    "q_own_discretion": (
        "Have you taken this policy entirely of your own will and satisfaction, "
        "without any pressure, inducement, or misrepresentation?"
    ),

    "awareness": (
        "I would also like to inform you. Sometimes, people make fraudulent and "
        "misleading calls asking for money in the name of unclaimed bonus or agency "
        "commission. Please note that Bajaj Capital and insurance companies never ask "
        "for such payments. "
        "If you receive such a call or message, immediately contact us on our "
        "toll-free number seven six seven eight one two three one two three or write "
        "to us at care at the rate bajaj capital dot com to report it. "
        "Also, please ensure that you never deposit your premium into any individual's "
        "personal bank account. "
        "To enjoy the full benefits of your policy, it is important to pay your "
        "renewal premiums on time. If the premium is not paid, you will lose out on "
        "the benefits of the policy. "
        "Are you fully satisfied with the policy information provided to you?"
    ),

    "closing": (
        "Thank you, {{CUSTOMER_NAME}}. Rest assured this policy is an important "
        "financial security in your life, which will be a strong support for you and "
        "your family in any unforeseen circumstances. "
        "On behalf of Bajaj Capital, I wish you and your family a secure and "
        "prosperous future. "
        "Have a wonderful day! Thank you."
    ),

    "failed": (
        "I'm sorry, your verification could not be completed. Our service team will "
        "contact you again. Thank you."
    ),
    "silence_prompt": "I couldn't hear you. Can you repeat that please?",
}


def _spell_digits(text: str) -> str:
    out: list[str] = []
    for ch in text:
        if ch.isdigit():
            if out and out[-1] not in (" ", "") and not out[-1].endswith(" "):
                out.append(" ")
            out.append(DIGIT_WORDS[ch])
            out.append(" ")
        else:
            out.append(ch)
    return re.sub(r"\s+", " ", "".join(out)).strip()


class EnglishPack:
    name = "english"
    voice_id: str | None = None  # falls back to ELEVENLABS_VOICE_ID env var
    model_id: str | None = None  # falls back to ELEVENLABS_MODEL_ID env var / DEFAULT_MODEL
    phrases = PHRASES

    def time_based_greeting(self) -> str:
        if ZoneInfo is not None:
            now = datetime.now(ZoneInfo("Asia/Kolkata"))
        else:
            now = datetime.utcnow()
        hour = now.hour
        if 5 <= hour < 12:
            return "Good morning"
        if 12 <= hour < 17:
            return "Good afternoon"
        return "Good evening"

    def name_with_title(self, processed: dict[str, Any]) -> str:
        name = processed.get("customer_name", "")
        gender = (processed.get("gender") or "").lower()
        if gender in {"male", "m"}:
            return f"Mister {name}"
        if gender in {"female", "f"}:
            return f"Miss {name}"
        return name

    def format_phone(self, phone: str) -> str:
        if not phone:
            return ""
        return " ".join(DIGIT_WORDS[ch] for ch in str(phone) if ch.isdigit())

    def format_email(self, email: str) -> str:
        if not email:
            return ""
        text = email.replace("@", " at the rate ").replace(".", " dot ")
        return _spell_digits(text)

    def format_address(self, addr: str) -> str:
        if not addr:
            return ""
        return _spell_digits(addr)

    def number_to_words(self, n: int) -> str:
        """English words using Indian numbering (crore/lakh/thousand/hundred)."""
        n = abs(int(n))
        if n == 0:
            return "zero"

        units = [
            "", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
            "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
            "sixteen", "seventeen", "eighteen", "nineteen",
        ]
        tens_words = [
            "", "", "twenty", "thirty", "forty", "fifty",
            "sixty", "seventy", "eighty", "ninety",
        ]

        def two_digits(x: int) -> str:
            if x == 0:
                return ""
            if x < 20:
                return units[x]
            t, u = divmod(x, 10)
            return tens_words[t] + (f"-{units[u]}" if u else "")

        def three_digits(x: int) -> str:
            if x == 0:
                return ""
            h, r = divmod(x, 100)
            if h and r:
                return f"{units[h]} hundred and {two_digits(r)}"
            if h:
                return f"{units[h]} hundred"
            return two_digits(r)

        def build(x: int) -> str:
            parts: list[str] = []
            if x >= 10_000_000:
                c, x = divmod(x, 10_000_000)
                parts.append(f"{build(c)} crore")
            if x >= 100_000:
                lakh, x = divmod(x, 100_000)
                parts.append(f"{two_digits(lakh)} lakh")
            if x >= 1000:
                t, x = divmod(x, 1000)
                parts.append(f"{two_digits(t)} thousand")
            if x > 0:
                parts.append(three_digits(x))
            return " ".join(p for p in parts if p)

        return build(n).strip()

    def ensure_number_words(self, value: str) -> str:
        """If `value` is a bare number, spell it out; otherwise return as-is."""
        if not value:
            return ""
        if not isinstance(value, str):
            value = str(value)
        if re.search(r"[A-Za-z]", value):
            return value
        cleaned = re.sub(r"[^\d-]", "", value)
        if not cleaned or cleaned in {"-", ""}:
            return value
        try:
            return self.number_to_words(int(cleaned))
        except Exception:
            return value

    def format_date(self, date_str: str) -> str:
        """English dates pass through — already speakable as-is."""
        return date_str or ""

    def format_premium_schedule(self, processed: dict[str, Any]) -> str:
        """Build the "you have paid X which you will pay Y for Z years" sentence.

        Returns the schedule clause only; the caller decides how to slot it in.
        """
        amt = processed.get("premium_amount", "")
        freq_raw = processed.get("premium_frequency", "")
        term = processed.get("premium_paying_term", "")

        freq_key = _normalize_frequency(freq_raw)
        freq_phrase = _EN_FREQUENCY_PHRASES.get(freq_key, freq_raw)

        if freq_key == "single":
            return (
                f"You have paid Rupees {amt} as a single payment."
                if amt else "This is a single-payment policy."
            )

        base = f"You have paid Rupees {amt}" if amt else "Your premium has been recorded"
        if freq_phrase and term:
            return f"{base} which you will pay {freq_phrase} for {term} years."
        if freq_phrase:
            return f"{base} which you will pay {freq_phrase}."
        if term:
            return f"{base} for {term} years."
        return f"{base}."

    def build_declaration(self, processed: dict[str, Any]) -> str:
        nominee = processed.get("nominee_name") or ""
        nominee_text = f" The nominee on this policy is {nominee}." if nominee else ""
        schedule = self.format_premium_schedule(processed)
        return (
            f"On {processed.get('application_date', '')}, you have applied for the "
            f"{processed.get('company', '')} {processed.get('plan_name', '')} Policy. "
            f"The insured person in this policy is {processed.get('insured_name', '')}, "
            f"date of birth {processed.get('dob_life_assured', '')}.{nominee_text} "
            f"The policy term is for {processed.get('policy_term', '')} years. "
            f"The sum assured is Rupees {processed.get('sum_insured', '')}. "
            f"{schedule}\n"
            f"Is the information I shared with you accurate? "
            f"Please respond with a Yes or No."
        )
