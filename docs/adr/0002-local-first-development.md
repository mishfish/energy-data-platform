# 0002: Local-first development, GCP as deploy target

- Status: superseded by [0003](0003-platform-engineering-focus.md) (the local-first principle still stands; the tool choices are reopened)
- Date: 2026-09-29

## Context
The platform targets GCP (GCS, BigQuery), but a cloud-only setup costs money during development and makes the project harder for a reviewer to run.

## Decision
- The whole stack runs locally via `docker compose`. GCP is added later as the production target and provisioned with Terraform.
- Warehouse: DuckDB locally and BigQuery in prod. dbt models stay portable through cross-database macros.
- Raw storage: a local directory in dev and GCS in prod, selected by `RAW_DATA_URI`. No object-store emulator (MinIO, fake-gcs-server) is used: DuckDB reads local files natively, so an emulator would add a container without adding coverage.
- Orchestration: Airflow runs as a single `standalone` container backed by Postgres. Cloud Composer is ruled out for cost; the prod option is decided later.

## Consequences
- `git clone && docker compose up` gives a working platform without any cloud account.
- SQL dialect differences between DuckDB and BigQuery must be handled in dbt, and CI should run against both.
- The GCS code path is exercised only in prod and CI, not locally.
