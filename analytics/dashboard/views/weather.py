from datetime import UTC, datetime, timedelta

import altair as alt
import pandas as pd
import streamlit as st

from energy_dashboard import charts, db, palette
from energy_dashboard.timeutil import to_market_time
from energy_dashboard.zones import ZONES

theme = st.context.theme.type

VARIABLES = {
    "Temperature (°C)": "temperature_2m_c",
    "Wind at 100 m (m/s)": "wind_speed_100m_ms",
    "Shortwave radiation (W/m²)": "shortwave_radiation_wm2",
    "Cloud cover (%)": "cloud_cover_pct",
}

st.title("Weather and demand")
st.caption(
    "Hourly Open-Meteo weather at 3 to 5 points per zone: analysis for the past, the latest forecast "
    "ahead (ADR 0031). Times in CET/CEST."
)

zones = db.query("SELECT DISTINCT bidding_zone FROM weather FINAL")
names = sorted(zones["zone"]) if not zones.empty else []
if not names:
    st.info("No weather in ClickHouse yet.")
    st.stop()
c1, c2 = st.columns([1, 2])
zone = c1.selectbox("Zone", names, index=names.index("NL") if "NL" in names else 0)
label = c2.radio("Variable", list(VARIABLES), horizontal=True)
column = VARIABLES[label]
now = datetime.now(UTC)
params = {"zone": ZONES[zone], "start": now - timedelta(days=7), "end": now + timedelta(days=3)}

weather = db.query(
    "SELECT point, valid_from, toFloat64OrNull(toString(temperature_2m_c)) AS temperature_2m_c, "
    "toFloat64OrNull(toString(wind_speed_100m_ms)) AS wind_speed_100m_ms, "
    "toFloat64OrNull(toString(shortwave_radiation_wm2)) AS shortwave_radiation_wm2, "
    "toFloat64OrNull(toString(cloud_cover_pct)) AS cloud_cover_pct "
    "FROM weather FINAL WHERE bidding_zone = {zone:String} "
    "AND valid_from >= {start:DateTime64(3, 'UTC')} AND valid_from < {end:DateTime64(3, 'UTC')}",
    params,
)
if weather.empty:
    st.info(f"No weather for {zone}.")
    st.stop()
weather["time"] = to_market_time(weather["valid_from"])
points = sorted(weather["point"].unique())

st.subheader(f"{label} per point")
st.caption("The vertical line is now: left of it analysis, right of it forecast.")
series = weather[["time", "point", column]].rename(columns={column: "value"})
now_rule = (
    alt.Chart(pd.DataFrame({"time": to_market_time(pd.Series([now]))}))
    .mark_rule(color=palette.MUTED_TEXT[palette.mode(theme)], strokeDash=[2, 2])
    .encode(x="time:T")
)
st.altair_chart(
    alt.layer(charts.lines(series, "time:T", "value:Q", "point", points, theme, label), now_rule).properties(
        height=360
    ),
    use_container_width=True,
)

st.subheader("Temperature against load")
st.caption("Hourly load against the mean temperature of the zone's points, past week.")
load = db.query(
    "SELECT toStartOfHour(delivery_start) AS hour, avg(toFloat64(load_mw)) AS load_mw FROM actual_load FINAL "
    "WHERE bidding_zone = {zone:String} AND delivery_start >= {start:DateTime64(3, 'UTC')} GROUP BY hour",
    params,
)
temp = (
    weather.groupby("valid_from", as_index=False)["temperature_2m_c"]
    .mean()
    .rename(columns={"valid_from": "hour"})
)
if load.empty:
    st.info(f"No load for {zone}.")
else:
    load["hour"] = pd.to_datetime(load["hour"], utc=True)
    temp["hour"] = pd.to_datetime(temp["hour"], utc=True)
    joined = load.merge(temp, on="hour").dropna()
    st.altair_chart(
        charts.scatter(
            joined,
            "temperature_2m_c:Q",
            "load_mw:Q",
            theme,
            "mean temperature (°C)",
            "load (MW)",
            [
                alt.Tooltip("hour:T", title="hour (UTC)", format="%d %b %H:%M"),
                alt.Tooltip("temperature_2m_c:Q", format=".1f"),
                alt.Tooltip("load_mw:Q", format=",.0f"),
            ],
        ).properties(height=320),
        use_container_width=True,
    )

with st.expander("Table"):
    st.dataframe(series.pivot_table(index="time", columns="point", values="value"), use_container_width=True)
