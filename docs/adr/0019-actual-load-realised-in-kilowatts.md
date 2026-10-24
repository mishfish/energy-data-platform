# 0019: Actual load: realised values only, carried as integer kilowatts

- Status: accepted; the kW scale is superseded by ADR 0029 (watts)
- Date: 2026-10-15

## Context
M1.5 adds total load (ENTSO-E document type A65) for bidding zone NL. A65 is one document type with several process types: `A16` (realised, i.e. actual load), `A01` (day-ahead forecast), `A31` (week-ahead) and others. TSOs revise realised load days to weeks after publication, so it produces real UPDATEs downstream and exercises the CDC path more than prices do. Quantities arrive as decimal strings in megawatts (`MAW`) with zero to three decimals depending on the TSO. As with prices (ADR 0014), the upsert's no-op guard compares the new value with the stored one, so the value must stay exact from parser to database.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Process types | `A16` only vs `A16` + `A01` (forecast) from the start | **`A16` only.** Forecasts are a separate step: they change the table key (process type) or need their own table, and are not needed to demonstrate revisions. |
| Representation | Fixed-point `int64` + `numeric` (as ADR 0014) vs `float64` + `double precision` | **Fixed-point `int64`.** One convention for every value in the service; exact comparison in the no-op guard. |
| Scale | kW (MW × 1000, 3 decimals) vs MW × 100 (2 decimals, symmetric with cents) | **kW.** Covers every precision ENTSO-E publishes for load; a scale of 2 would reject valid source data. |

## Consequences
- The parser returns `QuantityKW int64` and rejects quantities with more than three decimals or more than nine integer digits instead of rounding, so a format change at the source fails loudly.
- The load column is `numeric(12, 3)` in MW; the store layer converts at the write boundary (`pgtype.Numeric{Int, Exp: -3}`).
- `numeric` columns reach Kafka through Debezium as decimals, which needs `decimal.handling.mode` to be chosen in M2. Prices already have the same requirement, so this adds no new decision.
- Adding forecasts later needs its own ADR on the key or table layout.
