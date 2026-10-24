"""Chart colors per theme: the validated reference palette of the dataviz method (ADR 0032).

Categorical slots are assigned in fixed order and follow the entity, never its rank. Light and dark
are separate steps of the same hues, validated against their own surface.
"""

CATEGORICAL: dict[str, list[str]] = {
    "light": ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"],
    "dark": ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"],
}

# Status colors are reserved for state and always shipped with a label.
STATUS: dict[str, str] = {
    "good": "#0ca30c",
    "warning": "#fab219",
    "serious": "#ec835a",
    "critical": "#d03b3b",
}

# A single series and recessive guides.
PRIMARY = {"light": "#2a78d6", "dark": "#3987e5"}
MUTED_TEXT = {"light": "#52514e", "dark": "#c3c2b7"}
# Streamlit's own background per theme: rings around overlapping marks take the surface color.
SURFACE = {"light": "#ffffff", "dark": "#0e1117"}


def mode(theme_type: str | None) -> str:
    """Palette mode for a Streamlit theme type; anything but dark is light."""
    return "dark" if theme_type == "dark" else "light"


def categorical(theme_type: str | None, n: int) -> list[str]:
    """The first n categorical slots. More than eight series must be folded or faceted, never cycled."""
    if n > 8:
        raise ValueError(f"{n} series do not fit the eight categorical slots; fold into Other or facet")
    return CATEGORICAL[mode(theme_type)][:n]
