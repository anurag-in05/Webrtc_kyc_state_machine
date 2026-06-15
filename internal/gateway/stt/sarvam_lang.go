package stt

import "strings"

// sarvamLangCode maps our internal language names to Sarvam BCP-47 codes. Ported
// verbatim from app/pipeline/sarvam_lang.py (CONTRACTS §5: copy verbatim). The
// brain only emits the 7 languages in StartSessionRequest, but the full map and
// the default are kept faithful to the source.
var sarvamLangMap = map[string]string{
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

func sarvamLangCode(language string) string {
	// Python: _MAP.get((language or "").lower().strip(), "en-IN").
	if code, ok := sarvamLangMap[strings.ToLower(strings.TrimSpace(language))]; ok {
		return code
	}
	return "en-IN"
}
