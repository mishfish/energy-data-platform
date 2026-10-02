# 0011: Postgres access: pgx/v5 directly

- Status: accepted
- Date: 2026-09-30

## Context
The ingestion service writes time series into Postgres with idempotent upserts, possibly in bulk during backfills. Later tooling (lag checks, replication slot inspection) also talks to Postgres. The query surface is small: a handful of statements per source.

## Options
1. `jackc/pgx/v5` with `pgxpool`, used directly: native protocol, `pgx.Batch` and `CopyFrom` for bulk writes, `pgx.CollectRows`/`RowToStructByName` for scanning, typed errors (`pgconn.PgError`) for SQLSTATE handling.
2. `pgx/v5` + `sqlc`: typed Go code generated from `.sql` files. Compile-time checked queries, but one more generator in the toolchain and generated code in the repo, for only a few queries.
3. `jmoiron/sqlx` on `database/sql` with the `pgx/v5/stdlib` driver: struct scanning and named parameters, but pgx-specific features (Batch, COPY) are only reachable through `conn.Raw`.

## Decision
Option 1, `pgx/v5` with `pgxpool`, no query builder or generator.

## Consequences
- Bulk writes and SQLSTATE-based retry decisions use pgx APIs directly.
- Queries are hand-written SQL strings, so integration tests against a real Postgres are the only check that they are correct.
- goose (ADR 0010) needs a `*sql.DB`; it gets one through `pgx/v5/stdlib`, used only for migrations.
- If the query count grows, sqlc can be added on top of pgx without changing the driver.
