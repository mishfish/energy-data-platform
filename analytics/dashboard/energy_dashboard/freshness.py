"""Freshness of the data itself in the analytical store (ADR 0032).

Run freshness (did the last fetch succeed) lives in Prometheus and Grafana. This is the other half:
how far the newest stored slot reaches relative to now. source_created_at cannot serve for that,
because the upsert keeps the first document that carried the current value (ADR 0012).
"""

from dataclasses import dataclass
from datetime import timedelta


@dataclass(frozen=True)
class Expectation:
    """Bounds on latest_slot - now: realised data trails now, prices and forecasts lead it."""

    good: timedelta
    warning: timedelta
    description: str


EXPECTATIONS: dict[str, Expectation] = {
    # Realised values arrive about 35 minutes after a slot ends (live probe, ADR 0021).
    "actual_load": Expectation(timedelta(hours=-2), timedelta(hours=-4), "realised, trails now"),
    "generation": Expectation(timedelta(hours=-2), timedelta(hours=-4), "realised, trails now"),
    # Day-ahead prices cover at least the rest of today.
    "day_ahead_price": Expectation(timedelta(hours=1), timedelta(0), "covers ahead of now"),
    # The forecast reaches two days ahead.
    "weather": Expectation(timedelta(hours=24), timedelta(hours=6), "forecast, leads now"),
}


def status(dataset: str, horizon: timedelta) -> str:
    """good, warning or critical for horizon = start of the latest stored slot minus now."""
    e = EXPECTATIONS[dataset]
    if horizon >= e.good:
        return "good"
    if horizon >= e.warning:
        return "warning"
    return "critical"
