import altair as alt
import streamlit as st

from energy_dashboard import charts, db
from energy_dashboard.timeutil import delivery_day, to_market_time

theme = st.context.theme.type

st.title("Day-ahead prices across Europe")
st.caption(
    "Day-ahead prices per bidding zone from ENTSO-E, read from ClickHouse. Zones with several auctions "
    "per day show their lowest sequence; choosing the coupled price is still open (ADR 0029)."
)

days = db.price_days()
if days.empty:
    st.info("No prices in ClickHouse yet.")
    st.stop()
# Default to the newest day that most zones have published; the latest one may still be partial.
complete = days[days["zones"] >= 0.8 * days["zones"].max()]
default = days.index.get_loc(complete.index[0]) if not complete.empty else 0
zones_by_day = dict(zip(days["day"], days["zones"], strict=True))
day = st.selectbox(
    "Delivery day (CET/CEST)",
    days["day"],
    index=default,
    format_func=lambda d: f"{d.strftime('%a %d %b %Y')} · {zones_by_day[d]} zones",
)
start, end = delivery_day(day)
df = db.prices(start, end)
if df.empty:
    st.info("No prices for this day.")
    st.stop()
df["hours"] = df["resolution_us"] / 3_600_000_000
df["negative_hours"] = df["hours"].where(df["price"] < 0, 0)
daily = (
    df.groupby("zone")
    .agg(
        average=("price", "mean"),
        low=("price", "min"),
        high=("price", "max"),
        negative_hours=("negative_hours", "sum"),
    )
    .reset_index()
)

cheapest, dearest = daily.loc[daily["average"].idxmin()], daily.loc[daily["average"].idxmax()]
c1, c2, c3, c4 = st.columns(4)
c1.metric("Average across zones", f"{daily['average'].mean():.2f} €/MWh")
c2.metric(
    "Cheapest zone",
    cheapest["zone"],
    f"{cheapest['average']:.2f} €/MWh",
    delta_color="off",
    delta_arrow="off",
)
c3.metric(
    "Most expensive zone",
    dearest["zone"],
    f"{dearest['average']:.2f} €/MWh",
    delta_color="off",
    delta_arrow="off",
)
c4.metric(
    "Zones with negative prices",
    int((daily["negative_hours"] > 0).sum()),
    f"of {len(daily)}",
    delta_color="off",
    delta_arrow="off",
)

left, right = st.columns([2, 3])
with left:
    st.subheader("Daily average by zone")
    chart = charts.bars(
        daily,
        "average:Q",
        "zone:N",
        theme,
        "€/MWh",
        [
            "zone",
            alt.Tooltip("average:Q", format=".2f"),
            alt.Tooltip("low:Q", format=".2f"),
            alt.Tooltip("high:Q", format=".2f"),
        ],
    )
    st.altair_chart(chart, use_container_width=True)

with right:
    st.subheader("Intraday profile")
    available = sorted(daily["zone"])
    default = [z for z in ["NL", "DE_LU", "FR", "ES"] if z in available]
    picked = st.multiselect("Zones (up to four)", available, default=default, max_selections=4)
    if picked:
        profile = df[df["zone"].isin(picked)].copy()
        profile["time"] = to_market_time(profile["delivery_start"])
        # The color follows the zone in the order picked, never its rank.
        st.altair_chart(
            charts.lines(profile, "time:T", "price:Q", "zone", picked, theme, "€/MWh").properties(height=360),
            use_container_width=True,
        )
    negative = daily[daily["negative_hours"] > 0].sort_values("negative_hours", ascending=False)
    if not negative.empty:
        st.subheader("Hours with negative prices")
        st.altair_chart(
            charts.bars(negative, "negative_hours:Q", "zone:N", theme, "hours", ["zone", "negative_hours:Q"]),
            use_container_width=True,
        )

with st.expander("Table"):
    st.dataframe(daily.round(2), hide_index=True, use_container_width=True)
