from datetime import UTC, datetime

import pandas as pd
import streamlit as st

from energy_dashboard import db, freshness, palette

st.title("Data health")
st.caption(
    "How far the newest stored slot of each dataset and zone reaches, read from ClickHouse. This is "
    "data freshness; run freshness and alerts are in Grafana and Alertmanager."
)

TIME_COLUMN = {
    "day_ahead_price": "delivery_start",
    "actual_load": "delivery_start",
    "generation": "delivery_start",
    "weather": "valid_from",
}
ICON = {"good": "● good", "warning": "▲ warning", "critical": "■ critical"}

frames = []
for dataset, column in TIME_COLUMN.items():
    df = db.query(
        f"SELECT bidding_zone, max({column}) AS latest, count() AS rows "
        f"FROM {dataset} FINAL GROUP BY bidding_zone"
    )
    df["dataset"] = dataset
    frames.append(df)
latest = pd.concat(frames, ignore_index=True)
if latest.empty:
    st.info("ClickHouse has no data yet.")
    st.stop()

now = datetime.now(UTC)
latest["latest"] = pd.to_datetime(latest["latest"], utc=True)
latest["horizon_h"] = ((latest["latest"] - now).dt.total_seconds() / 3600).round(1)
latest["status"] = [
    freshness.status(d, pd.Timedelta(hours=h).to_pytimedelta())
    for d, h in zip(latest["dataset"], latest["horizon_h"], strict=True)
]

cols = st.columns(len(TIME_COLUMN))
for col, dataset in zip(cols, TIME_COLUMN, strict=True):
    part = latest[latest["dataset"] == dataset]
    counts = part["status"].value_counts()
    col.metric(
        f"{dataset} · zones fresh",
        f"{counts.get('good', 0)}/{len(part)}",
        f"{int(part['rows'].sum()):,} rows",
        delta_color="off",
        delta_arrow="off",
    )
    if counts.get("critical", 0):
        col.caption(f"{ICON['critical']}: {', '.join(sorted(part[part['status'] == 'critical']['zone']))}")

st.subheader("Per zone")
st.caption(
    "Horizon is the start of the newest slot minus now, in hours: realised load and generation trail now, "
    "prices and the weather forecast lead it. "
    + "; ".join(f"{d}: {e.description}" for d, e in freshness.EXPECTATIONS.items())
)
pick = st.multiselect("Status", list(ICON), default=["warning", "critical"], format_func=ICON.get)
shown = latest[latest["status"].isin(pick)] if pick else latest
table = shown.assign(status=shown["status"].map(ICON))[
    ["dataset", "zone", "latest", "horizon_h", "status", "rows"]
]
table = table.sort_values(["dataset", "horizon_h"])


def color_status(value: str) -> str:
    for key, label in ICON.items():
        if value == label:
            return f"color: {palette.STATUS[key]}; font-weight: 600"
    return ""


if table.empty:
    st.success("Every dataset and zone is within its expected horizon.")
else:
    st.dataframe(
        table.style.map(color_status, subset=["status"]),
        hide_index=True,
        use_container_width=True,
        column_config={
            "horizon_h": st.column_config.NumberColumn("horizon (h)", format="%.1f"),
            "latest": st.column_config.DatetimeColumn("newest slot (UTC)", format="YYYY-MM-DD HH:mm"),
        },
    )

st.subheader("Row versions in ClickHouse")
st.caption(
    "Rows on disk against current rows (FINAL). The difference is versions superseded by revisions, "
    "redeliveries and re-snapshots that background merges have not collapsed yet (ADR 0026)."
)
versions = db.query(
    " UNION ALL ".join(
        f"SELECT '{t}' AS dataset, (SELECT count() FROM {t}) AS on_disk, "
        f"(SELECT count() FROM {t} FINAL) AS current"
        for t in TIME_COLUMN
    )
)
versions["superseded"] = versions["on_disk"] - versions["current"]
st.dataframe(versions, hide_index=True, use_container_width=True)
