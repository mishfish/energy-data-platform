# 0013: Run migrations as a separate one-shot job

- Status: accepted
- Date: 2026-10-02
- Refines: [0010](0010-schema-migrations-goose.md) (how migrations are triggered)

## Context
Migrations are SQL files embedded in the `ingest` binary (ADR 0010). They have to run before the service writes to Postgres, in the local stack and in any later deployment.

## Options
1. On service start: `ingest` applies pending migrations before serving, guarded by goose's Postgres advisory lock. No extra moving parts, but every replica start touches the schema, the service needs DDL privileges, and a slow or failing migration shows up as a crash-looping service.
2. A `migrate` subcommand of the same binary, run as a one-shot job that the service depends on (compose `service_completed_successfully`; a Kubernetes Job or init container later). Schema changes are a separate, visible step with their own exit code and logs, and the service can run with DML-only privileges.

## Decision
Option 2. `ingest migrate` applies pending migrations and exits. In compose, the `migrate` service runs it and `ingest` waits for it to exit 0. `make migrate` runs it by hand.

## Consequences
- Same image and embedded files for both, so code and schema versions cannot drift apart.
- The advisory lock stays on: concurrent `migrate` runs serialise instead of failing.
- Separate database roles for migrations and the service are possible later; locally both use the same user.
- `migrate` only goes up. Rollbacks are new forward migrations; down sections exist for local use with the goose CLI.
