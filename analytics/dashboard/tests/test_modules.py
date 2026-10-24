import re
from datetime import timedelta
from pathlib import Path

import pytest

from energy_dashboard import freshness, generation, palette
from energy_dashboard.zones import ZONES, zone_name

REPO = Path(__file__).resolve().parents[3]


def go_map(path: str, var: str) -> dict[str, str]:
    """The string-to-string map literal named var in a Go source file."""
    src = (REPO / path).read_text()
    block = src[src.index(f"var {var} = map[string]string{{") :]
    block = block[: block.index("\n}")]
    return dict(re.findall(r'"([^"]+)":\s*"([^"]+)"', block))


def test_zones_match_go():
    assert go_map("internal/entsoe/zones.go", "BiddingZones") == ZONES


def test_zone_name():
    assert zone_name("10YNL----------L") == "NL"
    assert zone_name("unknown") == "unknown"


def test_every_production_type_has_a_group():
    assert set(go_map("internal/entsoe/generation.go", "PSRTypes")) == set(generation.PSR_GROUP)
    assert set(generation.PSR_GROUP.values()) <= set(generation.GROUPS)
    assert len(generation.GROUPS) == 8
    assert generation.group_of("B99") == "Other"


def test_palette():
    assert palette.categorical("dark", 3) == ["#3987e5", "#d95926", "#199e70"]
    assert palette.categorical(None, 1) == ["#2a78d6"]
    with pytest.raises(ValueError):
        palette.categorical("light", 9)


@pytest.mark.parametrize(
    ("dataset", "horizon", "want"),
    [
        ("actual_load", timedelta(hours=-1), "good"),
        ("actual_load", timedelta(hours=-3), "warning"),
        ("generation", timedelta(hours=-5), "critical"),
        ("day_ahead_price", timedelta(hours=10), "good"),
        ("day_ahead_price", timedelta(minutes=30), "warning"),
        ("day_ahead_price", timedelta(hours=-1), "critical"),
        ("weather", timedelta(hours=40), "good"),
        ("weather", timedelta(hours=10), "warning"),
        ("weather", timedelta(hours=2), "critical"),
    ],
)
def test_freshness(dataset, horizon, want):
    assert freshness.status(dataset, horizon) == want
