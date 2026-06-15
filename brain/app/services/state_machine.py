"""Consent-verification flow as a pure-logic state machine.

Retryable states allow up to MAX_ATTEMPTS; the 3rd failure goes to `failed`.
Never speaks — returns a `StepResult` with a phrase *key* and `Event`s for
the route layer to record.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal

State = Literal[
    "greeting", "congrats", "declaration",
    "q_mobile", "q_alt_mobile", "q_email", "q_address",
    "q_medical", "q_no_merge", "q_own_discretion",
    "awareness", "closing", "failed", "completed",
]

Intent = Literal["yes", "no", "please_repeat"]

MAX_ATTEMPTS = 3

PHRASE_KEY: dict[State, str] = {
    "greeting": "greeting",
    "congrats": "congrats",
    "declaration": "declaration_text",
    "q_mobile": "q_mobile",
    "q_alt_mobile": "q_alt_mobile",
    "q_email": "q_email",
    "q_address": "q_address",
    "q_medical": "q_medical",
    "q_no_merge": "q_no_merge",
    "q_own_discretion": "q_own_discretion",
    "awareness": "awareness",
    "closing": "closing",
    "failed": "failed",
    "completed": "failed",
}

RETRY_PHRASE_KEY: dict[State, str] = {
    "greeting": "greeting",
    "congrats": "congrats",
    "declaration": "declaration_text",
    "q_mobile": "q_mobile",
    "q_alt_mobile": "q_alt_mobile",
    "q_email": "q_email",
    "q_address": "q_address",
    "q_medical": "q_medical",
    "q_no_merge": "q_no_merge",
    "q_own_discretion": "q_own_discretion",
    "awareness": "awareness",
}

VERIFICATION_STEP_NAME: dict[State, str] = {
    "declaration": "client_declaration",
    "q_mobile": "primary_mobile",
    "q_alt_mobile": "alternative_mobile",
    "q_email": "email_id",
    "q_address": "address",
    "q_medical": "medical_fitness",
    "q_no_merge": "no_policy_merging",
    "q_own_discretion": "own_discretion",
    "awareness": "final_satisfaction",
}


@dataclass
class Event:
    event_type: str
    payload: dict = field(default_factory=dict)


@dataclass
class StepResult:
    next_state: State
    agent_phrase_key: str
    events: list[Event] = field(default_factory=list)
    attempt_count: int = 0
    terminal: bool = False


class ConsentVerificationFlow:
    def __init__(self, has_alt_mobile: bool):
        self.state: State = "greeting"
        self.attempts: dict[State, int] = {}
        self.has_alt_mobile = has_alt_mobile

    def initial(self) -> StepResult:
        return StepResult(
            next_state="greeting",
            agent_phrase_key="greeting",
            events=[],
            attempt_count=0,
            terminal=False,
        )

    def _next_on_yes(self, current: State) -> State:
        if current == "greeting":
            return "congrats"
        if current == "congrats":
            return "declaration"
        if current == "declaration":
            return "q_mobile"
        if current == "q_mobile":
            return "q_alt_mobile" if self.has_alt_mobile else "q_email"
        if current == "q_alt_mobile":
            return "q_email"
        if current == "q_email":
            return "q_address"
        if current == "q_address":
            return "q_medical"
        if current == "q_medical":
            return "q_no_merge"
        if current == "q_no_merge":
            return "q_own_discretion"
        if current == "q_own_discretion":
            return "awareness"
        if current == "awareness":
            return "closing"
        if current == "closing":
            return "completed"
        return current  # failed / completed are idempotent

    def step(self, intent: Intent) -> StepResult:
        current = self.state
        if current in {"failed", "completed"}:
            return StepResult(
                next_state=current,
                agent_phrase_key=PHRASE_KEY[current],
                events=[],
                attempt_count=self.attempts.get(current, 0),
                terminal=True,
            )

        if intent == "yes":
            events: list[Event] = []
            step_name = VERIFICATION_STEP_NAME.get(current)
            if step_name:
                status = "satisfied" if current == "awareness" else "verified"
                events.append(Event(
                    event_type="record_verification_step",
                    payload={"step": step_name, "status": status},
                ))
            if current == "greeting":
                events.append(Event(event_type="greeting_finished"))
            elif current == "congrats":
                events.append(Event(event_type="customer_ready"))
            elif current == "declaration":
                events.insert(0, Event(event_type="declaration_recorded"))

            nxt = self._next_on_yes(current)
            self.state = nxt
            self.attempts[nxt] = 0

            if nxt == "completed":
                # closing phrase already spoken last turn; mark consent verified here.
                events.append(Event(
                    event_type="consent_verified", payload={"verified": True},
                ))
                events.append(Event(event_type="session_completed"))
                return StepResult(
                    next_state="completed",
                    agent_phrase_key=PHRASE_KEY["completed"],
                    events=events,
                    attempt_count=0,
                    terminal=True,
                )

            return StepResult(
                next_state=nxt,
                agent_phrase_key=PHRASE_KEY[nxt],
                events=events,
                attempt_count=0,
                terminal=False,
            )

        # intent in {"no", "please_repeat"}
        if current == "closing":
            # closing isn't really retryable; treat any non-yes the same as completion.
            self.state = "completed"
            return StepResult(
                next_state="completed",
                agent_phrase_key=PHRASE_KEY["completed"],
                events=[Event(event_type="session_completed")],
                attempt_count=0,
                terminal=True,
            )

        self.attempts[current] = self.attempts.get(current, 0) + 1
        if self.attempts[current] >= MAX_ATTEMPTS:
            step_name = VERIFICATION_STEP_NAME.get(current, current)
            events = [
                Event(
                    event_type="record_verification_step",
                    payload={"step": step_name, "status": "failed"},
                ),
                Event(event_type="consent_verified", payload={"verified": False}),
                Event(event_type="session_failed"),
            ]
            self.state = "failed"
            return StepResult(
                next_state="failed",
                agent_phrase_key=PHRASE_KEY["failed"],
                events=events,
                attempt_count=self.attempts[current],
                terminal=True,
            )

        return StepResult(
            next_state=current,
            agent_phrase_key=RETRY_PHRASE_KEY[current],
            events=[],
            attempt_count=self.attempts[current],
            terminal=False,
        )
