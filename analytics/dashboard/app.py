"""Energy Data Platform dashboard: analytics on the ClickHouse CDC tables (ADR 0032)."""

import streamlit as st

st.set_page_config(page_title="Energy Data Platform", page_icon=":material/bolt:", layout="wide")

pages = st.navigation(
    [
        st.Page("views/prices.py", title="Prices across Europe", icon=":material/euro:", default=True),
        st.Page("views/zone.py", title="Zone in detail", icon=":material/factory:"),
        st.Page("views/weather.py", title="Weather and demand", icon=":material/thermostat:"),
        st.Page("views/health.py", title="Data health", icon=":material/monitor_heart:"),
    ]
)
pages.run()
