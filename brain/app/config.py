"""Pydantic-settings wrapper around `.env`. Unknown keys are ignored.

The brain is the text-only control plane, so this keeps only what the control
flow, i18n / tts-plan, intent delegation, transcript storage, and the `/start`
echo need. Every media/webrtc/mux/audio-rate/redis knob moved to the Go
gateway + recorder and is gone from here.
"""

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # External providers. The elevenlabs/sarvam keys are no longer CALLED from
    # the brain (media moved to Go), but the i18n + tts-plan code still reads the
    # voice/model ids and the tts_speed* pacing below.
    sarvam_api_key: str = ""
    elevenlabs_api_key: str = ""
    elevenlabs_voice_id: str = ""
    elevenlabs_model_id: str = ""
    openai_api_key: str = ""  # name/address transliteration; graceful no-op if unset

    # Intent service: classification is always delegated over HTTP now (no
    # in-process ensemble in the brain). On error the caller folds to please_repeat.
    intent_service_url: str = ""
    intent_service_timeout: float = 2.0

    # Transcript storage. aws_storage="s3" uploads + returns a CDN URL; anything
    # else writes under recordings_dir and returns a local path.
    recordings_dir: str = "./recordings"
    recordings_enabled: bool = True
    aws_storage: str = ""
    aws_s3_bucket: str = ""
    aws_media_folder: str = ""
    aws_region: str = ""
    aws_url: str = ""

    # Downstream Go services. gateway_url builds the gateway_offer_url returned by
    # /start and receives POST /close on /end; recorder_url is proxied for the
    # live recording_status. Both calls are best-effort (the brain runs without
    # them up, e.g. curl tests).
    gateway_url: str = "http://localhost:8080"
    recorder_url: str = "http://localhost:9090"

    # TTS pacing read by the tts-plan builder: phrases whose spoken length is
    # >= tts_long_phrase_chars use tts_speed_long, else tts_speed.
    tts_speed: float = 1.0
    tts_speed_long: float = 1.1
    tts_long_phrase_chars: int = 200

    # ICE / TURN echoed in /start so the browser can reach the gateway across NAT.
    turn_url: str = ""
    turn_username: str = ""
    turn_credential: str = ""

    # Browser capture constraints, echoed in /start (server-driven so res/fps
    # tune in one place). video_enabled=false = mic-only (no camera track).
    video_enabled: bool = True
    capture_width: int = 640
    capture_height: int = 360
    capture_fps: int = 12

    # Server
    host: str = "0.0.0.0"
    port: int = 8000
    log_level: str = "info"


settings = Settings()
