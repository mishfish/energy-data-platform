# 0003: Platform engineering focus

- Status: accepted
- Date: 2026-09-29
- Supersedes: [0001](0001-scope-and-dataset.md), [0002](0002-local-first-development.md)

## Context
The previous scope (batch ingestion, a dbt star schema, Airflow, Python) centred on ETL and modeling work, not platform engineering: reliable ingestion, CDC, delivery guarantees and failure handling. The owner's background is Go tooling and infrastructure for CDC pipelines and databases.

## Options
1. Keep the batch/dbt scope. It is simpler, but it leaves out the platform concerns (CDC, delivery semantics, failure handling).
2. Build a streaming platform: a Go ingestion service writes to Postgres, CDC streams changes to a broker, and a sink loads them into an analytical store. Observability, failure handling and delivery semantics are treated as the core.

## Decision
Option 2. The domain stays ENTSO-E + KNMI, because ENTSO-E revises published values, so CDC carries real UPDATEs and not only appends. Go is the primary language. dbt and ML are a thin layer at the end. Development stays local-first (`docker compose`). Tool choices (broker, CDC implementation, analytical store, cloud) are open and get their own ADRs.

## Consequences
- The Python/Airflow skeleton was removed.
- Source volume is low (thousands of rows per day), so throughput and lag comparisons need a synthetic load generator.
