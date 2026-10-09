# 0017: Fetch scheduling with an in-process ticker

- Status: accepted
- Date: 2026-10-10

## Context
Day-ahead prices are published once a day (around 13:00 CET) and can be revised afterwards, so the ingestion service has to re-fetch a window of delivery days repeatedly. Re-fetching is cheap: unchanged rows are no-ops in Postgres (ADR 0016), and one zone with one document type is far below the API rate limit (ADR 0015). The freshness alert needs a steady heartbeat of successful runs.

## Options
1. `time.Ticker` inside the `ingest` process: fixed interval, first run at startup. No dependency, and the loop stops on the same context as the rest of the service. The schedule is not aligned with the publication time.
2. A cron library (`robfig/cron`) in the process: denser polling around publication and sparser otherwise. A new dependency and a more complex schedule that brings little for one zone.

## Decision
Option 1, in `ingest.DayAheadJob`:
- One run immediately at startup, then one per `INGEST_DAY_AHEAD_INTERVAL` (default 15 minutes).
- Every run fetches `[midnight - INGEST_DAY_AHEAD_LOOK_BACK, midnight + INGEST_DAY_AHEAD_LOOK_AHEAD)` around the current UTC midnight (defaults 24 h and 48 h), which covers yesterday, today and tomorrow's delivery day.
- Runs are sequential in one goroutine. A run that outlasts the interval delays the next one; the ticks it missed are dropped, not queued.
- A failed run is not retried by the job. The client already retries transient failures, and the next tick fetches the same window.

## Consequences
- A new publication is picked up within one interval, not at a fixed time.
- A revision of a delivery day older than the look-back is never seen; widening the window or a separate backfill covers that.
- Metrics per run: `ingest_fetches_total` and `ingest_fetch_duration_seconds` by result (`success`, `no_data`, `fetch_error`, `store_error`), `ingest_rows_upserted_total`, `ingest_last_success_timestamp_seconds`.
- `no_data` does not advance the last-success timestamp. The window always includes past delivery days, so an empty answer means the source is not serving data and freshness should degrade.
- A run interrupted by shutdown is aborted and not counted. The upsert is one atomic statement, so nothing is left half-written.
- With more than one replica every replica fetches. That is safe (idempotent upsert) but multiplies API calls; leader election is out of scope while the service runs as a single instance.
- The last-success series does not exist until the first successful run, so the freshness alert must also handle an absent series.
