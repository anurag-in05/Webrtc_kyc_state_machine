"""Pydantic request/response models for the sessions API. Declarative only —
no I/O, no business logic.

`StartSessionRequest` is kept EXACTLY (it is the current schema). The response
models are the text-only shapes from CONTRACTS §1: the brain returns a `tts_plan`
+ `voice_id`/`model_id` (the gateway synthesizes audio) and never any audio
stream/URL fields.
"""

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


class TurnEvent(BaseModel):
    event_type: str
    payload: dict = Field(default_factory=dict)


# A tts_plan is an ordered list of heterogeneous segments
# ({"kind":"speech",...} / {"kind":"silence","ms":N}) — see CONTRACTS §4. Kept as
# plain dicts; the gateway executes them.
TtsPlan = list[dict]


class StartSessionResponse(BaseModel):
    session_id: str
    state: str
    agent_text: str
    tts_plan: TtsPlan
    voice_id: str
    model_id: str
    language: str
    turn_index: int
    ice_servers: list[dict] = Field(default_factory=list)
    gateway_offer_url: str
    # getUserMedia capture constraints, server-driven so res/fps tune in one place.
    capture_width: int = 640
    capture_height: int = 360
    capture_fps: int = 12
    video_enabled: bool = True


class TurnRequest(BaseModel):
    transcript: str


class TurnResponse(BaseModel):
    session_id: str
    state: str
    intent: str
    transcript: str
    agent_text: str
    tts_plan: TtsPlan
    voice_id: str
    model_id: str
    turn_index: int
    attempt_count: int
    events: list[TurnEvent]
    status: Literal["active", "completed", "failed"]


class SessionSnapshot(BaseModel):
    """GET /sessions/{id} — current snapshot for the gateway (bootstrap) and the
    browser (poll)."""

    session_id: str
    state: str
    language: str
    turn_index: int
    agent_text: str
    tts_plan: TtsPlan
    voice_id: str
    model_id: str
    status: Literal["active", "completed", "failed"]
    events: list[TurnEvent]
    # Proxied from the recorder: pending|complete|partial|audio_only|failed|disabled.
    recording_status: str = "pending"
    full_call_video_url: str | None = None


class EndSessionResponse(BaseModel):
    session_id: str
    state: str
    turn_index: int
    transcript_url: str | None = None
    transcript_json_url: str | None = None
    recording_status: str = "pending"
