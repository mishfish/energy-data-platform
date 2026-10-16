# 0027: M3 infrastructure: ClickHouse version, registry storage, ClickHouse migrations

- Status: accepted
- Date: 2026-10-16

## Context
ADR 0024–0026 chose ClickHouse, Avro with Apicurio Registry, and at-least-once delivery with an idempotent sink. Running them in the local stack needs three more choices before the sink is written.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| ClickHouse version | 26.8 LTS vs 26.9 stable | **`clickhouse/clickhouse-server:26.8.28.14`** (LTS): about a year of fixes, and everything the schema needs (`ReplacingMergeTree(version, is_deleted)`). Same reasoning as pinning Postgres 17. |
| Registry storage | SQL storage in a separate database on our Postgres vs KafkaSQL | **SQL, database `registry`** on the existing Postgres. Schemas survive restarts (Avro messages reference them by ID), with one database to back up. The database is created by the idempotent one-shot `registry-db`, because Postgres init scripts only run on an empty volume. |
| ClickHouse schema | goose (ClickHouse dialect) vs container init scripts | **goose**, SQL migrations embedded in the sink binary (`internal/chdb/migrations`), applied by the one-shot `sink-migrate` service before the sink starts. Same pattern as ADR 0010 and 0013. |

## Consequences
- New dependency `github.com/ClickHouse/clickhouse-go/v2`: goose needs a `database/sql` driver, which rules out the lower-level `ch-go` for migrations. Which client the sink itself uses is a separate choice.
- ClickHouse has no advisory lock, so only one migrator may run at a time; the one-shot service guarantees that locally.
- The CDC tables use `ReplacingMergeTree(lsn, is_deleted)` keyed like the Postgres primary keys; an integration test checks that a replayed snapshot, a revision and a delete collapse to the right state with `FINAL` and after `OPTIMIZE`.
- The Debezium image ships Apicurio converters 3.2.5, the registry runs 3.3.3; both speak the v3 API. Upgrade them together.
- Apicurio's health endpoint is on the Quarkus management port 9000, not on the API port.
- The registry is reachable on host port 8085, ClickHouse on 8123 (HTTP) and 9000 (native).
