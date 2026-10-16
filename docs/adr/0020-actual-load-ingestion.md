# 0020: Actual load ingestion: generic job, wide window, own table

- Status: accepted
- Date: 2026-10-15

## Context
M1.5 adds realised total load for NL (ADR 0019) next to day-ahead prices. The M1 job (`ingest.DayAheadJob`, ADR 0017) was written for prices only, and more datasets follow (generation A75, Open-Meteo weather). TSOs revise realised load days to weeks after publication, much later than price revisions, so the re-fetch window has to reach further back than the 24 h used for prices.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Job per dataset | Generalise the M1 job vs a copy (`ActualLoadJob`) per dataset | **Generic `ingest.Job[T]`** with `Dataset`, `Fetch` and `Store`. Scheduling, metrics, logging and shutdown semantics stay those of ADR 0017 and are shared by every dataset. |
| Revision window | One wide window on the normal interval vs a short frequent window plus a long daily one | **One window**: `[midnight - 7 d, midnight + 1 d)` every 15 minutes (`INGEST_ACTUAL_LOAD_*`). About 770 slots and one request per run; unchanged rows are no-ops (ADR 0016). |
| Storage | Own `actual_load` table vs one shared table for all series | **Own table** with the layout of `day_ahead_price` (ADR 0012) and `load_mw numeric(12, 3)`. Typed columns with a fixed scale, and one Debezium topic per entity in M2. |

## Consequences
- Each job runs in its own goroutine, so a slow or failing source does not delay the others. Both share one API client and its retry policy (ADR 0015), and ENTSO-E's rate limit applies to their sum.
- Metrics keep their names; the `dataset` label gains the value `actual_load`. Dashboards already group by `dataset`.
- `IngestNeverSucceeded` lists datasets explicitly, because `absent()` cannot discover a series that never existed. Every new dataset has to be added to the rule and its tests.
- Revisions older than seven days are not picked up. If measured revisions turn out to reach further back, the look-back is widened by configuration, or a separate backfill job is added.
- `actual_load` rejects negative values with a CHECK constraint, matching the parser (ADR 0019).
