"""Altair chart builders that follow the dataviz method (ADR 0032): thin marks, one axis per chart,
hover tooltips on every mark, a crosshair on lines, fixed categorical slots per entity."""

import altair as alt
import pandas as pd

from energy_dashboard import palette


def bars(df: pd.DataFrame, x: str, y: str, theme: str | None, x_title: str, tooltip: list) -> alt.Chart:
    """Horizontal bars of one series, sorted by value; a single series needs no legend."""
    return (
        alt.Chart(df)
        .mark_bar(color=palette.PRIMARY[palette.mode(theme)], cornerRadiusEnd=4, size=10)
        .encode(
            x=alt.X(x, title=x_title),
            y=alt.Y(y, sort="-x", title=None, scale=alt.Scale(paddingInner=0.35)),
            tooltip=tooltip,
        )
        .properties(height=alt.Step(22))  # a fixed row height, however many zones there are
    )


def lines(
    df: pd.DataFrame, x: str, y: str, series: str, order: list[str], theme: str | None, y_title: str
) -> alt.LayerChart:
    """Lines per entity with a crosshair: hover anywhere picks the nearest time and shows every series."""
    colors = palette.categorical(theme, len(order))
    color = alt.Color(
        series, scale=alt.Scale(domain=order, range=colors), legend=alt.Legend(title=None, orient="top")
    )
    base = alt.Chart(df).encode(x=alt.X(x, title=None))
    nearest = alt.selection_point(nearest=True, on="pointerover", fields=[x], empty=False)
    line = base.mark_line(strokeWidth=2).encode(y=alt.Y(y, title=y_title), color=color)
    points = base.mark_point(size=60, filled=True).encode(
        y=y, color=color, opacity=alt.condition(nearest, alt.value(1), alt.value(0))
    )
    rule = (
        base.mark_rule(color=palette.MUTED_TEXT[palette.mode(theme)], strokeWidth=1)
        .encode(
            opacity=alt.condition(nearest, alt.value(0.6), alt.value(0)),
            tooltip=[alt.Tooltip(x, title="time", format="%d %b %H:%M")]
            + [alt.Tooltip(f"{s}:Q", format=",.2f") for s in order],
        )
        .transform_pivot(series, value=y, groupby=[x])
        .add_params(nearest)
    )
    return alt.layer(line, points, rule)


def stacked_area(
    df: pd.DataFrame, x: str, y: str, series: str, order: list[str], theme: str | None, y_title: str
) -> alt.Chart:
    """Stacked areas in a fixed group order, with a hover tooltip per group."""
    colors = palette.categorical(theme, len(order))
    return (
        alt.Chart(df)
        .mark_area(opacity=0.9, stroke=None)
        .encode(
            x=alt.X(x, title=None),
            y=alt.Y(y, title=y_title, stack="zero"),
            color=alt.Color(
                series,
                scale=alt.Scale(domain=order, range=colors),
                legend=alt.Legend(title=None, orient="top"),
            ),
            order=alt.Order("stack_order:Q"),
            tooltip=[
                alt.Tooltip(x, title="time", format="%d %b %H:%M"),
                series,
                alt.Tooltip(y, format=",.0f"),
            ],
        )
        .transform_calculate(stack_order=f"indexof({order!r}, datum['{series}'])")
    )


def scatter(
    df: pd.DataFrame, x: str, y: str, theme: str | None, x_title: str, y_title: str, tooltip: list
) -> alt.Chart:
    """One series of dots; 8px markers with a surface ring for overlaps."""
    return (
        alt.Chart(df)
        .mark_circle(
            size=64,
            opacity=0.6,
            color=palette.PRIMARY[palette.mode(theme)],
            stroke=palette.SURFACE[palette.mode(theme)],
            strokeWidth=1,
        )
        .encode(
            # A scatter compares spread, not magnitude: no forced zero on either axis.
            x=alt.X(x, title=x_title, scale=alt.Scale(zero=False)),
            y=alt.Y(y, title=y_title, scale=alt.Scale(zero=False)),
            tooltip=tooltip,
        )
    )
