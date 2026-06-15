"""No-op metrics shim.

The old brain exported Prometheus counters; the media metrics moved to Go and
the rest aren't needed for the text API. This stub keeps the verbatim
`session_manager` / `turn_service` imports resolving without dragging
prometheus_client into the control plane. Functions accept any args so call
sites stay untouched.
"""

from __future__ import annotations

from typing import Any


def session_started(*args: Any, **kwargs: Any) -> None: ...
def session_ended(*args: Any, **kwargs: Any) -> None: ...
def inc_intent_failure(*args: Any, **kwargs: Any) -> None: ...
