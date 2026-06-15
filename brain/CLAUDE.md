# Brain (Python) — control plane

Service dir: `brain/`, FastAPI on `:8000`. This is the old Python app **slimmed
to a text-only API**. Read `docs/CONTRACTS.md` §1 first.

## Job

Own session state + the consent flow. Decide *what* the agent says. Classify
intent. Write transcript artifacts. **Never touch media** — no WebRTC, no audio,
no STT/TTS calls, no recordings other than text transcripts.

## Copy these files VERBATIM from the old repo (do not rewrite or "improve")

From the provided zip `videokyc-wsfu-dev/app/` → `brain/app/`:

- `services/state_machine.py` — the consent flow. **Unchanged.**
- `services/turn_service.py` — `finalize_turn`, `record_user/agent`,
  `classify_safely_async`, `status_from_state`. **Keep**, see "Changes" below.
- `services/phrase_builder.py` — `build_phrases_for_session`. **Unchanged.**
- `services/language_processor.py` — OpenAI name/address transliteration. **Unchanged.**
- `services/i18n/` — `base.py`, `english.py`, `hindi.py`, `__init__.py`. **Unchanged.**
  (These hold every phrase, formatter, and the `<var>` markup. Do not touch.)
- `pipeline/sarvam_lang.py` — the BCP-47 map (the brain returns `language`; the
  gateway maps it, but keep this here too if `turn_service` references it).
- `pipeline/intent_client.py` — `RemoteIntentClassifier`. **Unchanged.**
- `services/session_manager.py` — keep the `Session` dataclass + registry, but
  **delete the media fields** (`webrtc_peer`, `stt_source`, recording paths,
  `agent_audio_offsets`, mux/av-sync fields). Keep: ids, language, customer data,
  resolved phrases, flow, turn_index, events_history, transcript, timestamps.
- `schemas/session.py` — keep `StartSessionRequest` EXACTLY (it is the current
  schema). Replace the response models per CONTRACTS §1 (add `tts_plan`,
  `voice_id`, `model_id`, `gateway_offer_url`; drop `agent_audio_stream_url` and
  the per-turn audio fields).
- `config.py` — keep only: provider keys (`sarvam`/`elevenlabs`/`openai` —
  elevenlabs/sarvam keys are no longer USED here but i18n/tts-plan code reads
  `tts_speed*`/voice settings; keep those), `intent_service_url`,
  `intent_service_timeout`, `recordings_dir`, AWS/`aws_url` (for transcript URLs),
  `tts_speed`, `tts_speed_long`, `tts_long_phrase_chars`, ICE/`turn_*` (echoed in
  `/start`), `capture_*`, `video_enabled`. **Delete** all webrtc/mux/video-mode
  /audio-rate/loop-monitor settings.

## Changes (the only new/edited logic)

1. **`/turn` now takes a transcript, not audio.** Delete the file-upload path,
   VAD, and the STT call. Handler: `{transcript} → classify_safely_async →
   finalize_turn → response` (CONTRACTS §1). All the hard logic is already in
   `finalize_turn`; just feed it a transcript.

2. **Emit a `tts_plan`.** Add `services/tts_plan.py` (~30 lines) that ports the
   splitting half of the old `pipeline/tts.py:synthesize_stream` but emits the
   plan instead of calling ElevenLabs (CONTRACTS §4). Reuse the existing helpers
   `_spoken_len`, `_speed_for`, `_slow_down_var_text`, and the
   `re.split(r"(<var>.*?</var>)")`. Also expose `resolve_voice_id(language)` /
   `resolve_model_id(language)` (the old `tts._resolve_*`) so `/start`, `/turn`,
   and GET return them. **No HTTP, no audio** — pure text → plan.

3. **Routes** (`routes/sessions.py`) slim to: `POST /start`, `GET /sessions/{id}`,
   `POST /sessions/{id}/turn`, `POST /sessions/{id}/end`, `GET /health`. Build the
   `tts_plan` wherever the old code set `pending_agent_text` for a turn, and store
   it on the session so GET can return the current turn's plan.

4. **`/end`**: mark terminal, write `transcript.txt` + `transcript.json` (port
   the transcript-writing part of `storage/recap.py`; **drop** the WAV-merge
   part — the recorder owns audio now), then `POST gateway /sessions/{id}/close`,
   return `recording_status: "pending"`. GET proxies `recorder /status` for the
   live status.

## DELETE entirely (moved to Go or gone)

`services/webrtc_video.py`, `services/mux.py`, `services/video_stream.py`,
`services/streaming_audio.py`, `services/metrics.py` (media metrics), the WAV-merge
half of `storage/recap.py`, `pipeline/stt.py`, `pipeline/tts.py`, `pipeline/vad.py`,
`utils/audio.py` (make_wav), `demo_cli.py`. Keep `services/intent/` as its own
service (see `intent/CLAUDE.md`).

## Keep the tests that still apply

`test_state_machine.py`, `test_intent.py` (verbatim — they pin the behavior we're
preserving). Drop the media/mux/webrtc tests.

## Do NOT

- call any STT/TTS/WebRTC API from here,
- change any phrase, event name, intent, or state-machine transition,
- add caching/queues/abstractions — this is a thin text API over logic that
  already exists.
