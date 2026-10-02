# 0010: Schema migrations: goose

- Status: accepted
- Date: 2026-09-30

## Context
Postgres is the source of truth and the CDC origin, so its schema has to be versioned, reviewed and reproducible across the local stack, integration tests (testcontainers) and any later deployment. Schema changes also feed schema evolution downstream (Debezium, Kafka, sink), so they must be explicit, ordered SQL.

## Options
1. `pressly/goose` (v3): plain SQL migration files with `-- +goose Up/Down` sections, usable as a library. Migrations can be embedded in the Go binary with `embed.FS`, so the service and tests apply the same files with no extra tooling. Postgres advisory-lock support guards against concurrent runs.
2. `golang-migrate/migrate`: separate `.up.sql`/`.down.sql` files, a CLI and a docker image, which fits a one-shot init container in compose. A failed migration leaves the version marked `dirty`, which needs manual `force` to recover.

## Decision
Option 1, goose, with SQL migrations embedded in the binary.

## Consequences
- One artifact carries both code and schema; integration tests migrate a fresh container with the same embedded files.
- Only SQL migrations, no Go migrations, so every schema change is reviewable as DDL.
- How migrations are triggered (on service start vs a separate `migrate` subcommand or compose one-shot) is settled together with the first schema.
