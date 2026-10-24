"""Production types grouped into eight categories for charts (ADR 0032).

ENTSO-E reports up to 21 production types per zone; a stacked chart can tell eight apart safely, so
the types fold into fixed groups. The order is the stacking order and the palette slot order.
"""

GROUPS: list[str] = [
    "Wind",
    "Solar",
    "Hydro",
    "Nuclear",
    "Fossil gas",
    "Coal and lignite",
    "Biomass and waste",
    "Other",
]

PSR_GROUP: dict[str, str] = {
    "B01": "Biomass and waste",
    "B02": "Coal and lignite",
    "B03": "Coal and lignite",
    "B04": "Fossil gas",
    "B05": "Coal and lignite",
    "B06": "Other",  # oil
    "B07": "Coal and lignite",  # oil shale burns like lignite
    "B08": "Coal and lignite",  # peat
    "B09": "Biomass and waste",  # geothermal
    "B10": "Hydro",  # pumped storage
    "B11": "Hydro",
    "B12": "Hydro",
    "B13": "Other",  # marine
    "B14": "Nuclear",
    "B15": "Biomass and waste",  # other renewable
    "B16": "Solar",
    "B17": "Biomass and waste",
    "B18": "Wind",
    "B19": "Wind",
    "B20": "Other",
    "B21": "Other",
    "B22": "Other",
    "B23": "Other",
    "B24": "Other",
    "B25": "Other",  # energy storage
}

RENEWABLE_GROUPS: frozenset[str] = frozenset({"Wind", "Solar", "Hydro", "Biomass and waste"})


def group_of(psr_type: str) -> str:
    """The chart group of a production type code; unknown codes fall into Other."""
    return PSR_GROUP.get(psr_type, "Other")
