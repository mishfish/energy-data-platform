# 0034: Synthetic ENTSO-E data calibrated on real data

- Status: accepted
- Date: 2026-10-31
- Supersedes: the value model and the document structure in the Behaviour section of ADR 0021

## Context
The synthetic API (ADR 0021) generated one NL-like profile for every zone: about 11 GW of load and 75 EUR/MWh, always PT15M, one price sequence, three fixed production types plus a random subset. Since ADR 0029–0030 the main stack holds a week of real data for 47 zones, and it differs from the fake in ways the platform has to handle:
- Scale: load from about 330 MW (ME) to 55 GW (DE_LU), average prices from 15 EUR/MWh (SE_1, FI) to 285 (AL).
- Resolution per zone and dataset: prices PT15M or PT60M, load PT15M, PT30M or PT60M, generation PT15M to PT60M.
- Price sequences: AT, DE_LU, DK_1/2 and ES publish 2–4 sequences, one ES sequence covers only the afternoon; DK sends float32 artefacts, IE_SEM three decimals.
- Production types: 1 to 26 series per zone, consumption for many types (NL, AT), pumped storage and B25 only published while running (BE, IT, NO: 20–85 % of slots absent).
- Coverage and timing: no prices for BA and CY, no load for IE_SEM; DK load misses about a quarter of its hours; load curve type A03; publication lag from a few minutes to about 12 hours (BG, MK).

With the fake, the per-zone code paths of ingest and the sink (hourly slots, several sequences, gaps between periods, six decimals) never ran outside unit tests, and dashboards on the fake stack showed the same curve for every zone.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Source of realism | Calibrated profiles (per-zone aggregates, generated values) vs replaying recorded days | **Calibrated profiles.** Works for any date, keeps no raw market data in the repository and stays small (about 300 KB). Replay would match the shape exactly, but repeats a few days in a loop and needs attribution for redistributed ENTSO-E data. |
| Scope | Values and API structure vs values only | **Both.** Resolution, sequences, decimals, production types and directions, gaps, zones without data, curve type A03 and publication lag follow the profile of each zone, so the fake stack exercises the same paths as the live one. |

## Behaviour
- `fakeentsoe calibrate` (`make fake-calibrate`) reads `day_ahead_price`, `actual_load` and `generation` from the ClickHouse of the main stack and writes `cmd/fakeentsoe/profiles.json`, which is embedded in the binary and committed. Run it again when more live data has accumulated.
- Per series the profile holds a mean per hour of the day (Europe/Amsterdam), the spread of whole days, the spread of slots and its jitter (the share that changes from slot to slot, estimated from consecutive differences), and the observed range. Load has separate weekday and weekend shapes.
- Values: profile + day shift + noise. The slow part of the noise is interpolated between draws every 3 hours, so deviations last for hours. A wind regime per day, mostly shared across zones and correlated from one day to the next, raises wind output and lowers prices in all zones together. Generation scales its spreads with the daily shape, so solar stays at zero at night. Values are clamped to the observed range widened by 10 %.
- A zone or dataset without a profile answers with the 999 acknowledgement, as the real API does.
- Revisions keep the epochs of ADR 0021 and change a value by up to 5 % (prices) or 3 % (load, generation).
- Publication lag per zone is the shortest lag seen by ingest (`source_created_at` after the slot ends), so it is accurate to the 15-minute polling interval.

## Consequences
- The fake stack shows realistic scales and shapes per zone, and every path that real data takes (hourly slots, gaps, several sequences, float32 artefacts) runs end to end without a token.
- A unit test drives the real `entsoe.Client` through every zone in the embedded profiles.
- The first run against an existing fake stack revises every slot in the ingest windows, because all values change. This is a burst of UPDATE events through CDC, not a problem.
- The A75 curve type is assumed to be A03 like A65; the stored data does not record it.
- Calibration rests on a few days in October: day-to-day spread, sequence offsets and seasonal shapes (solar, heating load) are rough, and prices have no weekday/weekend split. Recalibrating on a longer window improves this without code changes.
- Generating a 7-day generation window costs more CPU than before (up to 26 series per zone); about 0.2 s for the largest zone (ES, 23 series, 7 days).
