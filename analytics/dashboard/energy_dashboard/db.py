"""ClickHouse access for the dashboard. All reads use FINAL, so they see the current state of every
key even before background merges collapse the replayed and revised rows (ADR 0024, 0026)."""

import os
from datetime import datetime

import clickhouse_connect
import pandas as pd
import streamlit as st

from energy_dashboard.zones import zone_name


@st.cache_resource
def client():
    return clickhouse_connect.get_client(
        host=os.environ.get("CLICKHOUSE_HOST", "localhost"),
        port=int(os.environ.get("CLICKHOUSE_HTTP_PORT", "8123")),
        username=os.environ.get("CLICKHOUSE_USER", "energy"),
        password=os.environ.get("CLICKHOUSE_PASSWORD", "energy"),
        database=os.environ.get("CLICKHOUSE_DB", "energy"),
    )


@st.cache_data(ttl=60, show_spinner=False)
def query(sql: str, params: dict | None = None) -> pd.DataFrame:
    """Run a parameterised query; results are cached for a minute, as the data changes every 15."""
    df = client().query_df(sql, parameters=params or {})
    if "bidding_zone" in df.columns:
        df["zone"] = df["bidding_zone"].map(zone_name)
    return df


# Prices: zones with several sequences (auctions) per day keep the lowest one here. Which sequence is
# the coupled day-ahead price is a modelling decision still open for dbt (ADR 0029).
PRICES_SQL = """
SELECT bidding_zone, delivery_start, resolution_us, toFloat64(price_eur_mwh) AS price
FROM (
    SELECT bidding_zone, sequence, delivery_start, resolution_us, price_eur_mwh,
           min(sequence) OVER (PARTITION BY bidding_zone) AS first_sequence
    FROM day_ahead_price FINAL
    WHERE delivery_start >= {start:DateTime64(3, 'UTC')} AND delivery_start < {end:DateTime64(3, 'UTC')}
)
WHERE sequence = first_sequence
"""


def prices(start: datetime, end: datetime) -> pd.DataFrame:
    return query(PRICES_SQL, {"start": start, "end": end})


def price_days() -> pd.DataFrame:
    """Delivery days (CET/CEST) with prices and how many zones each covers, newest first. A day is
    published zone by zone and auction by auction, so the newest one can cover only a few zones."""
    return query(
        "SELECT toDate(delivery_start, 'Europe/Amsterdam') AS day, uniqExact(bidding_zone) AS zones "
        "FROM day_ahead_price FINAL GROUP BY day ORDER BY day DESC LIMIT 14"
    )
