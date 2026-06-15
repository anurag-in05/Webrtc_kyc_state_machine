"""Hindi language pack. Phrases are Bajaj-Capital-approved copy; the inline
English loanwords (Policy, mobile number, clients, call, message…) are kept
by design — do NOT translate them. Dates/numbers are converted by the
formatters below before reaching the declaration template."""

from __future__ import annotations

import re
from datetime import datetime
from typing import Any

try:
    from zoneinfo import ZoneInfo
except ImportError:
    ZoneInfo = None  # type: ignore


DIGIT_WORDS_HI = {
    "0": "शून्य", "1": "एक", "2": "दो", "3": "तीन", "4": "चार",
    "5": "पाँच", "6": "छह", "7": "सात", "8": "आठ", "9": "नौ",
}


# 0..99 — each number is largely irregular in Hindi, so we hard-code the table.
_HI_1_99 = [
    "", "एक", "दो", "तीन", "चार", "पाँच", "छह", "सात", "आठ", "नौ",
    "दस", "ग्यारह", "बारह", "तेरह", "चौदह", "पंद्रह", "सोलह", "सत्रह", "अठारह", "उन्नीस",
    "बीस", "इक्कीस", "बाईस", "तेईस", "चौबीस", "पच्चीस", "छब्बीस", "सत्ताईस", "अट्ठाईस", "उनतीस",
    "तीस", "इकतीस", "बत्तीस", "तैंतीस", "चौंतीस", "पैंतीस", "छत्तीस", "सैंतीस", "अड़तीस", "उनतालीस",
    "चालीस", "इकतालीस", "बयालीस", "तैंतालीस", "चौंतालीस", "पैंतालीस", "छियालीस", "सैंतालीस", "अड़तालीस", "उनचास",
    "पचास", "इक्यावन", "बावन", "तिरपन", "चौवन", "पचपन", "छप्पन", "सत्तावन", "अट्ठावन", "उनसठ",
    "साठ", "इकसठ", "बासठ", "तिरसठ", "चौंसठ", "पैंसठ", "छियासठ", "सड़सठ", "अड़सठ", "उनहत्तर",
    "सत्तर", "इकहत्तर", "बहत्तर", "तिहत्तर", "चौहत्तर", "पचहत्तर", "छिहत्तर", "सतहत्तर", "अठहत्तर", "उन्यासी",
    "अस्सी", "इक्यासी", "बयासी", "तिरासी", "चौरासी", "पचासी", "छियासी", "सत्तासी", "अट्ठासी", "नवासी",
    "नब्बे", "इक्यानवे", "बानवे", "तिरानवे", "चौरानवे", "पचानवे", "छियानवे", "सत्तानवे", "अट्ठानवे", "निन्यानवे",
]


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


_HI_FREQUENCY_PHRASES = {
    "monthly": "प्रत्येक महीने",
    "quarterly": "हर तीन महीने",
    "half_yearly": "प्रत्येक छह महीने",
    "yearly": "प्रत्येक वर्ष",
    "single": "एकमुश्त",
}


_HI_MONTHS = {
    'January': 'जनवरी', 'February': 'फरवरी', 'March': 'मार्च', 'April': 'अप्रैल',
    'May': 'मई', 'June': 'जून', 'July': 'जुलाई', 'August': 'अगस्त',
    'September': 'सितम्बर', 'October': 'अक्टूबर', 'November': 'नवंबर', 'December': 'दिसंबर',
    'Jan': 'जनवरी', 'Feb': 'फरवरी', 'Mar': 'मार्च', 'Apr': 'अप्रैल',
    'Jun': 'जून', 'Jul': 'जुलाई', 'Aug': 'अगस्त',
    'Sep': 'सितम्बर', 'Oct': 'अक्टूबर', 'Nov': 'नवंबर', 'Dec': 'दिसंबर',
}

_HI_DAYS = {
    '1': 'एक', '2': 'दो', '3': 'तीन', '4': 'चार', '5': 'पांच',
    '6': 'छह', '7': 'सात', '8': 'आठ', '9': 'नौ', '10': 'दस',
    '11': 'ग्यारह', '12': 'बारह', '13': 'तेरह', '14': 'चौदह', '15': 'पंद्रह',
    '16': 'सोलह', '17': 'सत्रह', '18': 'अठारह', '19': 'उन्नीस', '20': 'बीस',
    '21': 'इक्कीस', '22': 'बाईस', '23': 'तेईस', '24': 'चौबीस', '25': 'पच्चीस',
    '26': 'छब्बीस', '27': 'सत्ताईस', '28': 'अठ्ठाईस', '29': 'उनतीस',
    '30': 'तीस', '31': 'इकतीस',
}


PHRASES: dict[str, str] = {
    "greeting": (
        "{{TIME_BASED_GREETING}}, मैं बजाज कैपिटल से काव्या बोल रही हूँ। "
        "क्या मैं {{CUSTOMER_NAME}} जी से बात कर रही हूँ?"
    ),

    "congrats": (
        "{{CUSTOMER_NAME}} जी, आपको बहुत बहुत बधाई हो, आपने अपने और अपने "
        "परिवार के सुरक्षित भविष्य के लिए एक बहुत ही समझदारी भरा फैसला लिया है, "
        "बजाज कैपिटल परिवार में आपका हार्दिक स्वागत है। "
        "पिछले साठ वर्षों से हम देश के लोगों को उनकी जरूरत के अनुसार सही निवेश "
        "और वित्तीय संबंधी समाधान प्रदान कर रहे हैं। आज देश भर में लाखों clients "
        "हम पर अपना विश्वास जताते हैं। हमारा उद्देश्य है कि हर एक client को सही "
        "सलाह, उत्तम सेवा और व्यक्तिगत ध्यान मिल सके। "
        "आपने भारत की एक प्रमुख insurance company {{COMPANY_NAME}} की policy ली "
        "है। इसके माध्यम से आपने अपने और अपने परिवार के भविष्य को सुरक्षित और "
        "निश्चिंत बनाया है। "
        "यह call आपकी policy की जानकारी आपसे साझा करने के लिए की जा रही है। "
        "क्वालिटी और ट्रेनिंग के उद्देश्य से यह call record भी हो रही है। "
        "क्या हम शुरू करें?"
    ),

    "declaration_intro": (
        "अब मैं आपकी policy की जानकारी पढ़कर सुनाती हूँ, कृपया ध्यान से सुनें।"
    ),
    "declaration_text": "{{DECLARATION_TEXT}}",

    "verification_intro": (
        "अब मैं आपसे कुछ महत्वपूर्ण personal details confirm करना चाहूंगी। "
        "हाँ या ना में जवाब दें।"
    ),

    "q_mobile": "आपका mobile number <var>{{PHONE_HINDI}}</var> है?",
    "q_alt_mobile": "क्या आपका alternative mobile number <var>{{ALT_PHONE_HINDI}}</var> है?",
    "q_email": "E mail address <var>{{EMAIL_TTS}}</var> है?",
    "q_address": "आपका Address <var>{{ADDRESS_TTS}}</var> है?",

    "q_medical": (
        "आशा करती हूं बीमित व्यक्ति पूर्ण रूप से स्वस्थ है और इस समय कोई "
        "गंभीर बीमारी से ग्रस्त नहीं है?"
    ),

    "q_no_merge": (
        "क्या आपको ये जानकारी है कि ये एक नई Life Insurance Policy है, और "
        "आपकी किसी भी पुरानी policy को इस नई policy के साथ merge या जोड़ा "
        "नहीं जा सकता और आपकी पुरानी policies के benefits या लाभ इस नई "
        "policy में ट्रांसफर नहीं किए जा सकते।"
    ),

    "q_own_discretion": (
        "और, आपने यह policy अपनी पूर्ण संतुष्टि और इच्छा से ली है, बिना "
        "किसी दबाव, प्रलोभन या बहेकावे के?"
    ),

    "awareness": (
        "मैं आपको एक महत्वपूर्ण बात भी बताना चाहूंगी। कभी-कभी कुछ लोग भ्रमित "
        "करने वाले calls करके unclaimed bonus या agency commission के नाम पर "
        "पैसे मांगते हैं, बजाज कैपिटल या Insurance companies कभी भी ऐसा नहीं "
        "करती। "
        "इस तरह के call या message मिलने पर तुरंत हमारे toll-free number "
        "<var>सात छह सात आठ एक दो तीन एक दो तीन</var> पर संपर्क करें या "
        "care at the rate bajaj capital dot com पर email भेजकर अपनी शिकायत "
        "दर्ज करें। "
        "कृपया ध्यान रहे, कभी भी अपना premium किसी भी व्यक्ति के personal "
        "account में deposit ना करें। "
        "Policy का पूरा लाभ लेने के लिए अपने renewal premium का समय पर "
        "भुगतान करना बहुत जरूरी है। अगर किसी कारण premium का भुगतान नहीं हो "
        "पाता, तो आप policy के पूरे benefits लेने से वंचित रह जाएंगे। "
        "आशा करती हूं कि आप दी गई policy की जानकारी से पूर्ण रूप से संतुष्ट हैं?"
    ),

    "closing": (
        "शुक्रिया, {{CUSTOMER_NAME}} जी। यह policy आपके जीवन की एक "
        "महत्वपूर्ण financial सुरक्षा है, जो किसी भी आपातकालीन परिस्थिति "
        "में आपके और आपके परिवार के लिए एक मजबूत सहारा बनेगी, "
        "बजाज कैपिटल की तरफ से आपको और आपके परिवार को एक सुरक्षित और "
        "समृद्ध भविष्य की शुभकामनाएं। "
        "आपका दिन शुभ रहे। धन्यवाद।"
    ),

    "failed": (
        "माफ़ी चाहती हूँ आप का verification पूरा नहीं हो सका। आप को हमारी "
        "सर्विस टीम दोबारा संपर्क करेगी। धन्यवाद।"
    ),

    "silence_prompt": (
        "माफ कीजिये मैं सुन नहीं पायी, क्या आप वापस दोहरा सकते हैं?"
    ),
}


def _spell_digits_hi(text: str) -> str:
    out: list[str] = []
    for ch in text:
        if ch.isdigit():
            if out and out[-1] not in (" ", "") and not out[-1].endswith(" "):
                out.append(" ")
            out.append(DIGIT_WORDS_HI[ch])
            out.append(" ")
        else:
            out.append(ch)
    return re.sub(r"\s+", " ", "".join(out)).strip()


class HindiPack:
    name = "hindi"
    # "Anjura – KYC Verification & Onboarding" — female, Hindi-native,
    # purpose-built for KYC/onboarding flows. Swap by editing this line.
    voice_id: str | None = "HyqroD9zcoxw9WeTvVb0"
    # None -> uses tts.DEFAULT_MODEL (eleven_multilingual_v2, ~700ms TTFB).
    # Better prosody on code-mixed Hindi/English than flash; preferred default.
    model_id: str | None = None
    phrases = PHRASES

    def time_based_greeting(self) -> str:
        if ZoneInfo is not None:
            now = datetime.now(ZoneInfo("Asia/Kolkata"))
        else:
            now = datetime.utcnow()
        hour = now.hour
        if 5 <= hour < 12:
            return "सुप्रभात"
        if 12 <= hour < 17:
            return "नमस्कार"
        return "शुभ संध्या"

    def name_with_title(self, processed: dict[str, Any]) -> str:
        name = processed.get("customer_name", "")
        gender = (processed.get("gender") or "").lower()
        if gender in {"male", "m"}:
            return f"श्रीमान {name}"
        if gender in {"female", "f"}:
            return f"श्रीमती {name}"
        return name

    def format_phone(self, phone: str) -> str:
        if not phone:
            return ""
        return " ".join(DIGIT_WORDS_HI[ch] for ch in str(phone) if ch.isdigit())

    def format_email(self, email: str) -> str:
        if not email:
            return ""
        # Tech-context "at" and "dot" are commonly said in English even in Hindi
        # speech; keeping as English loanwords for naturalness.
        text = email.replace("@", " ऐट ").replace(".", " डॉट ")
        return _spell_digits_hi(text)

    def format_address(self, addr: str) -> str:
        if not addr:
            return ""
        return _spell_digits_hi(addr)

    def number_to_words(self, n: int) -> str:
        """Hindi words using Indian numbering (करोड़/लाख/हज़ार/सौ)."""
        n = abs(int(n))
        if n == 0:
            return "शून्य"

        def two_digits(x: int) -> str:
            if x == 0:
                return ""
            return _HI_1_99[x]

        def three_digits(x: int) -> str:
            if x == 0:
                return ""
            h, r = divmod(x, 100)
            if h and r:
                return f"{_HI_1_99[h]} सौ {two_digits(r)}"
            if h:
                return f"{_HI_1_99[h]} सौ"
            return two_digits(r)

        def build(x: int) -> str:
            parts: list[str] = []
            if x >= 10_000_000:
                c, x = divmod(x, 10_000_000)
                parts.append(f"{build(c)} करोड़")
            if x >= 100_000:
                lakh, x = divmod(x, 100_000)
                parts.append(f"{two_digits(lakh)} लाख")
            if x >= 1000:
                t, x = divmod(x, 1000)
                parts.append(f"{two_digits(t)} हज़ार")
            if x > 0:
                parts.append(three_digits(x))
            return " ".join(p for p in parts if p)

        return build(n).strip()

    def ensure_number_words(self, value: str) -> str:
        if not value:
            return ""
        if not isinstance(value, str):
            value = str(value)
        if re.search(r"[A-Za-zऀ-ॿ]", value):
            # Already has letters (Latin or Devanagari) — leave as-is.
            return value
        cleaned = re.sub(r"[^\d-]", "", value)
        if not cleaned or cleaned in {"-", ""}:
            return value
        try:
            return self.number_to_words(int(cleaned))
        except Exception:
            return value

    def format_date(self, date_str: str) -> str:
        """English date string → Hindi: years 1000-1999 in conversational form
        (1990 → "उन्नीस सौ नब्बे"), month names mapped, days 1-31 spelled out."""
        if not date_str:
            return ""
        result = date_str

        def _convert_year(m: re.Match[str]) -> str:
            year_int = int(m.group(0))
            if 1000 <= year_int < 2000:
                century = year_int // 100
                remainder = year_int % 100
                parts = [self.number_to_words(century), "सौ"]
                if remainder:
                    parts.append(self.number_to_words(remainder))
                return " ".join(parts)
            return self.number_to_words(year_int)

        # 4-digit years first (so day conversion doesn't steal them)
        result = re.sub(r'\b\d{4}\b', _convert_year, result)

        # Months (long names before short, but dict iteration order handles this
        # since Python 3.7+ preserves insertion order)
        for eng, hi in _HI_MONTHS.items():
            result = result.replace(eng, hi)

        # Strip ordinal suffixes on day numbers (e.g. "3rd" -> "3")
        result = re.sub(r'(\d+)(st|nd|rd|th)\b', r'\1', result)

        # Convert remaining standalone day numbers (1-31)
        def _convert_day(token: str) -> str:
            clean = re.sub(r'[^\d]', '', token)
            if clean and len(clean) <= 2 and 1 <= int(clean) <= 31 and clean in _HI_DAYS:
                return token.replace(clean, _HI_DAYS[clean])
            return token

        return " ".join(_convert_day(t) for t in result.split())

    def format_premium_schedule(self, processed: dict[str, Any]) -> str:
        """Hindi schedule sentence with three cases:
        1. Single payment → 'आपने X रुपये का एकमुश्त भुगतान किया है।'
        2. Term + frequency known → '...जिसे आपको Y वर्षों तक F जमा करना होगा।'
        3. Frequency only → '...जिसे आपको F जमा करना होगा।'
        """
        amt_raw = processed.get("premium_amount", "") or ""
        freq_raw = processed.get("premium_frequency", "") or ""
        term_raw = (processed.get("premium_paying_term", "") or "").strip()

        freq_key = _normalize_frequency(freq_raw)
        freq_hi = _HI_FREQUENCY_PHRASES.get(freq_key, "")

        amt_hi = f"{amt_raw} रुपये" if amt_raw else ""

        if freq_key == "single":
            return (
                f"आपने {amt_hi} का एकमुश्त भुगतान किया है।"
                if amt_hi else "एकमुश्त भुगतान किया गया है।"
            )

        if freq_hi:
            if term_raw:
                yrs_hi = (term_raw + " वर्ष") if term_raw == "1" else (term_raw + " वर्षों")
                return (
                    f"आपने {amt_hi} का भुगतान किया है, जिसे आपको "
                    f"{yrs_hi} तक {freq_hi} जमा करना होगा।"
                    if amt_hi else
                    f"इसे आपको {yrs_hi} तक {freq_hi} जमा करना होगा।"
                )
            return (
                f"आपने {amt_hi} का भुगतान किया है, जिसे आपको {freq_hi} जमा करना होगा।"
                if amt_hi else
                f"इसे आपको {freq_hi} जमा करना होगा।"
            )

        # Frequency unknown
        return (
            f"आपने {amt_hi} का भुगतान किया है।"
            if amt_hi else "भुगतान दर्ज किया गया है।"
        )

    def build_declaration(self, processed: dict[str, Any]) -> str:
        nominee = processed.get("nominee_name") or ""
        nominee_text = (
            f" Policy में nominee {nominee} जी हैं।" if nominee else ""
        )
        schedule = self.format_premium_schedule(processed)
        policy_term = processed.get("policy_term", "")
        policy_term_hi = f"{policy_term} साल" if policy_term else ""
        sum_insured = processed.get("sum_insured", "")

        return (
            f"आपने {processed.get('application_date', '')} के दिन "
            f"{processed.get('company', '')} की {processed.get('plan_name', '')} "
            f"Policy के लिए आवेदन किया है। "
            f"इस policy में बीमित व्यक्ति {processed.get('insured_name', '')} जी हैं, "
            f"जिनकी जन्म तिथि {processed.get('dob_life_assured', '')} है।"
            f"{nominee_text} "
            f"Policy की अवधि {policy_term_hi} है, आपकी बीमित राशि "
            f"{sum_insured} रुपये है। "
            f"{schedule}\n"
            f"मैंने जो जानकारी आपसे साझा की है, क्या वह सही है? "
            f"कृपया हाँ या ना में जवाब दें।"
        )
