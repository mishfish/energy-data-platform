"""Smoke test of every page against a running ClickHouse (the local stack); skipped without one."""

import os
import urllib.request
from pathlib import Path

import pytest
from streamlit.testing.v1 import AppTest

VIEWS = Path(__file__).resolve().parents[1] / "views"
HOST = os.environ.get("CLICKHOUSE_HOST", "localhost")
PORT = os.environ.get("CLICKHOUSE_HTTP_PORT", "8123")


def clickhouse_up() -> bool:
    try:
        with urllib.request.urlopen(f"http://{HOST}:{PORT}/ping", timeout=2) as resp:
            return resp.status == 200
    except OSError:
        return False


pytestmark = pytest.mark.skipif(not clickhouse_up(), reason="no ClickHouse at CLICKHOUSE_HOST")


@pytest.mark.parametrize("page", ["prices.py", "zone.py", "weather.py", "health.py"])
def test_page_renders(page):
    at = AppTest.from_file(str(VIEWS / page), default_timeout=60).run()
    assert not at.exception, [e.value for e in at.exception]
    assert at.get("arrow_vega_lite_chart") or at.dataframe, "page rendered no chart and no table"
