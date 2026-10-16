# 0024: ClickHouse as the analytical store

- Status: accepted
- Date: 2026-10-16

## Context
Open decision 3. M3 adds a Go sink that loads the CDC topics (ADR 0022) into an analytical store, which later feeds dbt models and the forecast API. The store has to absorb at-least-once delivery (ADR 0026): duplicates, replays and re-snapshots must collapse to one current value per key. Development is local-first (ADR 0002): the full pipeline, its tests and its failure demos must run in compose.

## Options
1. **ClickHouse.** Runs in compose and in testcontainers. `ReplacingMergeTree` with a version column keeps the latest version of each key, which matches CDC upserts; `FINAL` or `argMax` give exact reads before background merges. Self-hosted in the cloud means running it ourselves or paying for ClickHouse Cloud.
2. **BigQuery.** Fully managed, and the Storage Write API with stream offsets gives exactly-once appends. It needs a GCP project and billing from the first test, and has no complete local emulator, so integration tests and demos depend on the cloud.

## Decision
Option 1, ClickHouse.

## Consequences
- The sink writes every event as a row with a version derived from the source (the Postgres LSN of the change), and tables use `ReplacingMergeTree(version)` keyed like the Postgres primary keys. Deletes become rows with a deleted flag.
- Reads that need exact current state use `FINAL` (or `argMax` by version) until merges have run; dbt models must account for this.
- ClickHouse joins the compose stack and the integration tests (testcontainers module).
- Open decision 6 (cloud and runtime) now includes where ClickHouse runs.
