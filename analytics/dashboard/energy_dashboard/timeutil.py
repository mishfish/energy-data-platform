"""Delivery days are defined in CET/CEST; ClickHouse stores UTC."""

from datetime import UTC, date, datetime, time, timedelta
from zoneinfo import ZoneInfo

import pandas as pd

MARKET_TZ = ZoneInfo("Europe/Amsterdam")


def delivery_day(day: date) -> tuple[datetime, datetime]:
    """UTC bounds of a CET/CEST delivery day; 23 or 25 hours on DST changes."""
    start = datetime.combine(day, time(), MARKET_TZ)
    end = datetime.combine(day + timedelta(days=1), time(), MARKET_TZ)
    return start.astimezone(UTC), end.astimezone(UTC)


def to_market_time(s: pd.Series) -> pd.Series:
    """UTC timestamps as naive CET/CEST wall time, which Altair draws as is."""
    s = pd.to_datetime(s)
    if s.dt.tz is None:
        s = s.dt.tz_localize("UTC")
    return s.dt.tz_convert(MARKET_TZ).dt.tz_localize(None)
