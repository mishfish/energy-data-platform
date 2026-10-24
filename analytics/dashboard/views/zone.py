from datetime import UTC, datetime, timedelta

import altair as alt
import pandas as pd
import streamlit as st

from energy_dashboard import charts, db, generation, palette
from energy_dashboard.timeutil import to_market_time
from energy_dashboard.zones import ZONES

theme = st.context.theme.type

st.title("Zone in detail")
st.caption("Generation mix, load and price of one bidding zone from ClickHouse; times in CET/CEST.")

zones = db.query("SELECT DISTINCT bidding_zone FROM generation FINAL")
names = sorted(zones["zone"]) if not zones.empty else []
if not names:
    st.info("No generation in ClickHouse yet.")
    st.stop()
c1, c2 = st.columns([1, 2])
zone = c1.selectbox("Zone", names, index=names.index("NL") if "NL" in names else 0)
days = c2.slider("Days back", 1, 7, 3)
eic = ZONES[zone]
end = datetime.now(UTC)
start = (end - timedelta(days=days)).replace(minute=0, second=0, microsecond=0)
params = {"zone": eic, "start": start, "end": end}

gen = db.query(
    "SELECT psr_type, delivery_start, toFloat64(quantity_mw) AS mw FROM generation FINAL "
    "WHERE bidding_zone = {zone:String} AND direction = 'generation' "
    "AND delivery_start >= {start:DateTime64(3, 'UTC')} AND delivery_start < {end:DateTime64(3, 'UTC')}",
    params,
)
load = db.query(
    "SELECT delivery_start, toFloat64(load_mw) AS mw FROM actual_load FINAL "
    "WHERE bidding_zone = {zone:String} "
    "AND delivery_start >= {start:DateTime64(3, 'UTC')} AND delivery_start < {end:DateTime64(3, 'UTC')}",
    params,
)
prices = db.prices(start, end + timedelta(days=1))
prices = prices[prices["zone"] == zone]

if gen.empty:
    st.info(f"No generation for {zone} in this window.")
    st.stop()
gen["group"] = gen["psr_type"].map(generation.group_of)
mix = gen.groupby(["delivery_start", "group"], as_index=False)["mw"].sum()
mix["time"] = to_market_time(mix["delivery_start"])
order = [g for g in generation.GROUPS if g in set(mix["group"])]

st.subheader("Generation mix and load")
st.caption("Load shares the megawatt axis with the stacked generation; the gap is imports or exports.")
area = charts.stacked_area(mix, "time:T", "mw:Q", "group", order, theme, "MW")
layers = [area]
if not load.empty:
    load["time"] = to_market_time(load["delivery_start"])
    load["series"] = "Load"
    layers.append(
        alt.Chart(load)
        .mark_line(strokeWidth=2, color=palette.MUTED_TEXT[palette.mode(theme)], strokeDash=[4, 2])
        .encode(
            x="time:T",
            y="mw:Q",
            tooltip=[
                alt.Tooltip("time:T", format="%d %b %H:%M"),
                alt.Tooltip("mw:Q", title="load MW", format=",.0f"),
            ],
        )
    )
st.altair_chart(alt.layer(*layers).properties(height=380), use_container_width=True)

share = mix.assign(renewable=mix["group"].isin(generation.RENEWABLE_GROUPS) * mix["mw"])
share = share.groupby("delivery_start", as_index=False)[["mw", "renewable"]].sum()
share["renewable_share"] = 100 * share["renewable"] / share["mw"].where(share["mw"] > 0)

left, right = st.columns(2)
with left:
    st.subheader("Price")
    if prices.empty:
        st.info("No prices for this zone.")
    else:
        p = prices.assign(time=to_market_time(prices["delivery_start"]))
        st.altair_chart(
            charts.lines(p, "time:T", "price:Q", "zone", [zone], theme, "€/MWh").properties(height=300),
            use_container_width=True,
        )
with right:
    st.subheader("Renewable share against price")
    joined = pd.merge_asof(
        share.sort_values("delivery_start"),
        prices[["delivery_start", "price"]].sort_values("delivery_start"),
        on="delivery_start",
        direction="backward",
        # Only a price of the same hour counts; never carry one across a gap in the data.
        tolerance=pd.Timedelta(hours=1),
    ).dropna()
    if joined.empty:
        st.info("Not enough overlapping data.")
    else:
        st.altair_chart(
            charts.scatter(
                joined,
                "renewable_share:Q",
                "price:Q",
                theme,
                "renewable share of generation (%)",
                "€/MWh",
                [
                    alt.Tooltip("delivery_start:T", title="time (UTC)", format="%d %b %H:%M"),
                    alt.Tooltip("renewable_share:Q", format=".1f"),
                    alt.Tooltip("price:Q", format=".2f"),
                ],
            ).properties(height=300),
            use_container_width=True,
        )

with st.expander("Table"):
    st.dataframe(
        mix.pivot_table(index="time", columns="group", values="mw").round(0), use_container_width=True
    )
