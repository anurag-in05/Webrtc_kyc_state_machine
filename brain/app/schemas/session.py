"""Pydantic request/response models for the sessions API. Declarative only —
no I/O, no business logic."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field


class StartSessionRequest(BaseModel):
    language: Literal[
        "english", "hindi", "bengali", "gujarati", "kannada", "tamil", "telugu"
    ] = "english"
    company_id: int = Field(..., examples=[1])
    workspace_id: int = Field(..., examples=[1])
    room_name: str | None = Field(default=None, examples=["kyc-session-abc"])
    customer_name: str
    insured_name: str
    primary_mobile: str
    alternative_mobile: str | None = None
    email: str
    address: str
    company: str
    plan_name: str
    policy_term: int
    premium_amount: str
    premium_paying_term: int
    premium_frequency: str
    sum_insured: str
    application_date: str
    dob_life_assured: str
    free_look_period: int = 15
    currency: str = "INR"
    relation_to_insured: str = "myself"
    premium_payment_mode: str
    gender: str = ""
    nominee_name: str | None = None
    # webrtc video transport; ignored if video_transport=ws
    video_transport: Literal["ws", "webrtc"] | None = None



class StartSessionResponse(BaseModel):
    session_id: str
    state: str
    agent_text: str
    agent_audio_stream_url: str
    agent_audio_sample_rate: int
    agent_audio_format: str
    turn_index: int
    # Transport discriminator; selects the client media block.
    video_transport: Literal["ws", "webrtc"] = "webrtc"
    # Present iff video_transport == "webrtc" (legacy "ws" is audio-only).
    video_offer_url: str | None = None
    ice_servers: list[dict] | None = None
    # getUserMedia capture constraints, server-driven so res/fps tune in one place.
    capture_width: int = 640
    capture_height: int = 360
    capture_fps: int = 12
    # False (VIDEO_ENABLED=false) = client requests mic only, no camera/video track.
    video_enabled: bool = True

class TurnEvent(BaseModel):
    event_type: str
    payload: dict = Field(default_factory=dict)

class TurnResponse(BaseModel):
    session_id: str
    state: str
    intent: str
    transcript: str
    agent_text: str
    agent_audio_stream_url: str
    agent_audio_sample_rate: int
    agent_audio_format: str
    user_audio_url: str
    turn_index: int
    attempt_count: int
    events: list[TurnEvent]
    status: Literal["active", "completed", "failed"]


class EndSessionResponse(BaseModel):
    session_id: str
    state: str
    turn_index: int
    audio_files: list[str]
    events: list[TurnEvent]
    full_call_audio_url: str | None = None
    transcript_url: str | None = None
    transcript_json_url: str | None = None
    # Mux runs in background; starts "pending", client polls GET /sessions/{sid}
    # until terminal. "disabled" = RECORDINGS_ENABLED=false (no artifacts, no mux).
    recording_status: Literal[
        "pending", "complete", "partial", "audio_only", "failed", "disabled"
    ] = "pending"
    full_call_video_url: str | None = None


class SessionStatusResponse(EndSessionResponse):
    pass
