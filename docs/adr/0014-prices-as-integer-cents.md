# 0014: Prices as integer cents in Go

- Status: accepted
- Date: 2026-10-10

## Context
`day_ahead_price.price_eur_mwh` is `numeric(10, 2)` (ADR 0012), and ENTSO-E publishes day-ahead prices as decimal strings with two decimals, negative values included. The value travels from the XML parser to the upsert, and the upsert's no-op guard compares it with the stored one, so it must stay exact; a float would turn rounding noise into spurious UPDATEs in the CDC stream. The ingestion service does no arithmetic on prices beyond comparison.

## Options
1. `int64` cents (EUR cents per MWh). Exact, comparable with `==`, trivial to assert in tests, no dependency. Needs a small decimal-string parser and a conversion to `numeric` at the write boundary (`pgtype.Numeric{Int, Exp: -2}`). Fixes the scale at two decimals.
2. `pgtype.Numeric` end to end. No conversion at the pgx boundary and arbitrary precision, also without a new dependency. The type is awkward in domain structs and tests (comparison and sums go through `big.Int`), and the parser package would depend on pgx.

## Decision
Option 1. Prices are carried as `int64` cents in `entsoe.DayAheadPrice.PriceCents`.

## Consequences
- The parser rejects amounts with more than two decimals or more than eight integer digits instead of rounding, so a format change at the source fails loudly rather than storing altered values.
- The store layer owns the conversion between cents and `numeric(10, 2)`.
- A source with finer price granularity needs a wider scale in both the column and this representation.
- `internal/entsoe` has no dependency on pgx.
