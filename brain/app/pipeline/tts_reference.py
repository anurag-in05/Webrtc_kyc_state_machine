"""Async ElevenLabs text-to-speech via HTTP streaming (24 kHz mono s16le PCM).

`<var>...</var>` segments stream with word-level commas (slower prosody) and
200ms silence brackets, restoring Sarvam slow-pace on a TTS with no pace knob.
"""

from __future__ import annotations

import re
import time
from collections.abc import AsyncIterator

import httpx
from loguru import logger

from app.config import settings
from app.services import metrics
from app.services.i18n import get_pack
from app.utils.audio import make_wav

ELEVENLABS_API_BASE = "https://api.elevenlabs.io/v1/text-to-speech"
# Rachel — ElevenLabs default English voice; override via ELEVENLABS_VOICE_ID.
DEFAULT_VOICE_ID = "21m00Tcm4TlvDq8ikWAM"
# higher-quality multilingual, ~700ms TTFB; better prosody on code-mixed Hi/En.
DEFAULT_MODEL = "eleven_flash_v2_5"
STREAM_SAMPLE_RATE = 24000
STREAM_CHUNK_SIZE = 4096

# 200ms of silence at 16-bit mono @ 24kHz = 24000 * 0.2 * 2 bytes
_SILENCE_200MS = b"\x00" * int(STREAM_SAMPLE_RATE * 0.2 * 2)

# Shared pooled client: one TLS handshake per process, not per phrase (a fresh
# client cost ~200-600ms DNS+TCP+TLS per synthesis, multiplied by <var> splits).
_client: httpx.AsyncClient | None = None


def _get_client() -> httpx.AsyncClient:
    global _client
    if _client is None or _client.is_closed:
        _client = httpx.AsyncClient(
            timeout=httpx.Timeout(10.0, read=30.0),
            # 120s keepalive spans a whole call; httpx's 5s default dies between
            # turns and re-pays the TLS handshake.
            limits=httpx.Limits(keepalive_expiry=120.0),
        )
    return _client


def _resolve_voice_id(language: str) -> str:
    try:
        pack_voice = get_pack(language).voice_id
    except NotImplementedError:
        pack_voice = None
    return pack_voice or settings.elevenlabs_voice_id or DEFAULT_VOICE_ID


def _resolve_model_id(language: str) -> str:
    try:
        pack_model = get_pack(language).model_id
    except NotImplementedError:
        pack_model = None
    return pack_model or settings.elevenlabs_model_id or DEFAULT_MODEL


def _stream_url(language: str) -> str:
    voice_id = _resolve_voice_id(language)
    return (
        f"{ELEVENLABS_API_BASE}/{voice_id}/stream"
        f"?output_format=pcm_{STREAM_SAMPLE_RATE}"
    )


def _build_headers() -> dict:
    return {
        "xi-api-key": settings.elevenlabs_api_key,
        "Content-Type": "application/json",
    }


def _build_payload(text: str, language: str, speed: float = 1.0) -> dict:
    return {
        "text": text,
        "model_id": _resolve_model_id(language),
        "voice_settings": {
            "stability": 0.5,
            "similarity_boost": 0.75,
            "style": 0.0,
            "use_speaker_boost": True,
            # >1.0 reads faster; used to pick up the pace on long phrases.
            "speed": speed,
        },
    }


def _spoken_len(text: str) -> int:
    """Length of the text ElevenLabs will actually voice (var tags stripped)."""
    return len(re.sub(r"</?var>", "", text or ""))


def _speed_for(text: str) -> float:
    """A little faster for long phrases, normal otherwise."""
    if _spoken_len(text) >= settings.tts_long_phrase_chars:
        return settings.tts_speed_long
    return settings.tts_speed


def _build_payload_slow(text: str, language: str) -> dict:
    """Voice settings tuned for clear enunciation in <var> segments."""
    return {
        "text": text,
        "model_id": _resolve_model_id(language),
        "voice_settings": {
            "stability": 0.7,
            "similarity_boost": 0.75,
            "style": 0.0,
            "use_speaker_boost": True,
        },
    }


def _slow_down_var_text(raw: str) -> str:
    """Join words with commas (micro-pauses) + trailing period so ElevenLabs
    reads the segment slower. Empty/whitespace tokens are dropped."""
    words = [w for w in raw.split() if w]
    if not words:
        return ""
    return ", ".join(words) + "."


async def _stream_one(
    text: str, language: str, *, slow: bool, speed: float = 1.0
) -> AsyncIterator[bytes]:
    """Stream PCM for a single text segment from ElevenLabs.

    `slow` (<var> digits) uses clear-enunciation settings at normal pace;
    everything else honours `speed` (>1.0 = faster).
    """
    url = _stream_url(language)
    headers = _build_headers()
    payload = (
        _build_payload_slow(text, language)
        if slow
        else _build_payload(text, language, speed)
    )

    try:
        client = _get_client()
        t_req = time.perf_counter()
        # Billed chars per request that reaches ElevenLabs (cache bypasses on
        # hits); this IS the "chars/call -80%" gate metric.
        metrics.observe_tts_request(len(text))
        logger.info(
            f"TTS [{language}] -> ElevenLabs HTTP POST "
            f"({len(text)} chars, slow={slow}, speed={speed:g})"
        )
        async with client.stream("POST", url, json=payload, headers=headers) as resp:
            status_ms = (time.perf_counter() - t_req) * 1000
            if resp.status_code != 200:
                body = await resp.aread()
                logger.error(f"ElevenLabs TTS {resp.status_code}: {body!r:.200}")
                # 429 = rate-limited, 5xx = vendor outage (failure-rate dashboard).
                if resp.status_code == 429:
                    metrics.inc_tts_failure("http_429")
                elif resp.status_code >= 500:
                    metrics.inc_tts_failure("http_5xx")
                else:
                    metrics.inc_tts_failure("error")
                raise RuntimeError(f"ElevenLabs TTS failed: {resp.status_code}")
            logger.info(
                f"TTS [{language}] <- ElevenLabs {resp.status_code} headers in {status_ms:.0f}ms"
            )
            async for chunk in resp.aiter_bytes(chunk_size=STREAM_CHUNK_SIZE):
                if chunk:
                    yield chunk
    except (httpx.TimeoutException, httpx.ConnectError) as exc:
        # Transport-level failure before/while reading the stream.
        kind = "timeout" if isinstance(exc, httpx.TimeoutException) else "error"
        metrics.inc_tts_failure(kind)
        raise


async def synthesize_stream(text: str, language: str) -> AsyncIterator[bytes]:
    """Yield raw 16-bit PCM chunks. <var> segments use word-level commas
    (slower prosody) bracketed by 200ms silences; other text is normal pace."""
    if not settings.elevenlabs_api_key:
        raise RuntimeError("ELEVENLABS_API_KEY is not configured in .env")
    if not text:
        return

    parts = re.split(r"(<var>.*?</var>)", text, flags=re.DOTALL)

    # Per-utterance pace (long phrase = a little faster); non-var segments only.
    body_speed = _speed_for(text)

    t0 = time.perf_counter()
    first_chunk_at: float | None = None
    chunk_count = 0
    byte_count = 0

    # In-flight gauge: inc here, dec in finally so error/cancel can't leak it.
    metrics.tts_stream_started()
    try:
        for part in parts:
            if not part:
                continue
            is_var = part.startswith("<var>") and part.endswith("</var>")
            clean = re.sub(r"</?var>", "", part).strip()
            if not clean:
                continue

            if is_var:
                yield _SILENCE_200MS
                byte_count += len(_SILENCE_200MS)
                clean = _slow_down_var_text(clean)
                if not clean:
                    continue

            async for chunk in _stream_one(clean, language, slow=is_var, speed=body_speed):
                if first_chunk_at is None:
                    first_chunk_at = time.perf_counter()
                    ttfa_ms = (first_chunk_at - t0) * 1000
                    logger.info(f"TTS-stream [{language}] first chunk in {ttfa_ms:.0f}ms")
                chunk_count += 1
                byte_count += len(chunk)
                yield chunk

            if is_var:
                yield _SILENCE_200MS
                byte_count += len(_SILENCE_200MS)

        total_ms = (time.perf_counter() - t0) * 1000
        logger.info(
            f"TTS-stream [{language}] done: {chunk_count} chunks, "
            f"{byte_count} bytes, {total_ms:.0f}ms total"
        )
    finally:
        metrics.tts_stream_finished()


async def synthesize(text: str, language: str) -> bytes:
    """Synthesize the full utterance and return a WAV blob."""
    pcm_buffer = bytearray()
    async for chunk in synthesize_stream(text, language):
        pcm_buffer.extend(chunk)
    return make_wav(bytes(pcm_buffer), STREAM_SAMPLE_RATE)
