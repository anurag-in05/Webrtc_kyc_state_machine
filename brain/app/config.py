"""Pydantic-settings wrapper around `.env`. Unknown keys are ignored."""

from typing import Literal

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # External providers
    sarvam_api_key: str = ""
    elevenlabs_api_key: str = ""
    elevenlabs_voice_id: str = ""
    elevenlabs_model_id: str = ""
    openai_api_key: str = ""

    # Storage
    recordings_dir: str = "./recordings"
    # Master switch for ALL recording artifacts. False = call runs end-to-end but
    # writes nothing (media drained to a blackhole). Dev/testing only.
    recordings_enabled: bool = True

    # AWS / S3 — set aws_storage="s3" to upload artifacts and return a CDN URL.
    # boto3 reads credentials from env / IAM role / ~/.aws.
    aws_storage: str = ""
    aws_s3_bucket: str = ""
    aws_media_folder: str = ""
    aws_region: str = ""
    aws_url: str = ""

    # Server
    host: str = "0.0.0.0"
    port: int = 8000
    log_level: str = "info"

    # Warm intent models at startup so turn 1 doesn't pay the load mid-call.
    # Disabled in tests (conftest) to avoid loading the ~100 MB encoder.
    warm_models_on_startup: bool = True

    # Intent service: when set, delegate classification to a shared HTTP service
    # (services.intent) instead of loading the ~1.5 GB ensemble in-process; this
    # also skips local warmup. Empty keeps the in-process classifier.
    intent_service_url: str = ""
    # Short timeout so a slow/dead intent service can't wedge a turn — on error
    # the caller folds to `please_repeat` (safe re-prompt default).
    intent_service_timeout: float = 2.0

    # Redis: when set, session metadata is mirrored so any worker can serve
    # GET /sessions, /offer claims media atomically, and /end forwards to the
    # owner. Live media stays on the worker that answered /offer. Empty = in-memory.
    redis_url: str = ""

    # TTS pacing: phrases >= tts_long_phrase_chars use tts_speed_long, else
    # tts_speed. ElevenLabs speed range is 0.7–1.2 (1.0 = normal).
    tts_speed: float = 1.0
    tts_speed_long: float = 1.1
    tts_long_phrase_chars: int = 200

    # Media transport. webrtc (default) carries mic + camera in and agent voice
    # out over one peer; the legacy per-turn WS path is off unless enable_ws_video.
    video_transport: Literal["ws", "webrtc"] = "webrtc"
    enable_ws_video: bool = False

    # ICE / TURN. STUN alone usually can't connect media on a cloud VM (NAT /
    # security groups block UDP), so set a TURN relay for any non-localhost deploy.
    # Example: turn:65.0.122.126:3478?transport=udp
    turn_url: str = ""
    turn_username: str = ""
    turn_credential: str = ""

    # Master switch for the customer video flow. False = mic-only capture (no
    # camera, no server-side decode) — the big lever for concurrency testing.
    # Production KYC needs the selfie; default ON.
    video_enabled: bool = True

    # Browser capture defaults (a KYC selfie needs no more). Low res to cut decode CPU.
    capture_width: int = 640
    capture_height: int = 360
    capture_fps: int = 12

    # Prefer H.264/AAC MP4 from the recorder so the post-call step only stream-copies
    # video. Falls back to VP8/Opus WebM if H.264 send-negotiation fails.
    prefer_h264: bool = True

    # Video recording strategy (segfault/CPU kill-switch):
    #   passthrough : H.264 sessions write already-encoded Annex-B straight to
    #                 video_raw.mp4 off a writer thread — no decode, no re-encode
    #                 (avoids the av16 MediaRecorder.__run_track segfault); stereo
    #                 audio records separately to audio.m4a. Non-H.264 or writer-init
    #                 failure falls back to MediaRecorder. (overlay mux stays on it.)
    #   transcode   : MediaRecorder for every call (decode + re-encode live). Rollback.
    video_recording_mode: Literal["passthrough", "transcode"] = "passthrough"

    # Audio rates: WebRTC delivers 48 kHz, Sarvam wants 16 kHz, ElevenLabs emits
    # 24 kHz (resampled to 48 kHz for the outbound track + stereo recorder).
    record_audio_rate: int = 48_000
    stt_rate: int = 16_000
    tts_rate: int = 24_000
    # Outbound agent frame size. 20 ms @ 48 kHz = 960 samples — the track always
    # emits at this cadence (silence when idle) so the RTP stream never stalls.
    agent_frame_ms: int = 20

    # How the agent voice lands in the recording:
    #   inline_stereo : mixer writes user→L / agent→R live; post-call is a stream
    #                   copy. Default.
    #   overlay       : record raw mono mic, lay agent voice onto R post-call from
    #                   per-turn WAVs (Phase 1 fallback).
    #   off           : live file is canonical; post-call only remuxes to full_call.mp4.
    # The mux auto-detects the path by audio channel count.
    mux_mode: Literal["overlay", "inline_stereo", "off"] = "inline_stereo"

    # Process-wide media-health monitor (main.media_monitor_loop): one task samples
    # event-loop lag and max inbound STT queue depth across peers. STT queue is
    # bounded at STT_QUEUE_MAX=400 frames (~20 ms each); alert near the threshold.
    metrics_monitor_interval_s: float = 1.0
    stt_queue_alert_depth: int = 300

settings = Settings()
