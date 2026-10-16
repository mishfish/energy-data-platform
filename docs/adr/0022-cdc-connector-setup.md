# 0022: CDC connector setup for M2

- Status: accepted; the serialization row is superseded by ADR 0025
- Date: 2026-10-16

## Context
ADR 0004 and 0006 chose Debezium on Kafka Connect. M2 makes it run: `day_ahead_price` and `actual_load` are streamed into Kafka, and revisions from the source (real or synthetic, ADR 0021) appear as UPDATE events. Open decision 4 (Avro vs Protobuf, schema registry) is not needed to get events flowing, and is better made together with the sink in M3.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Connect image | `quay.io/debezium/connect` vs `cp-kafka-connect` with the plugin installed in our own Dockerfile | **`quay.io/debezium/connect:3.7.0.Final`**: connector bundled, versions matched upstream, no extra build. |
| Serialization in M2 | JSON with inline schemas now, decide open decision 4 in M3 vs decide Avro/Protobuf and a registry now | **JsonConverter with `schemas.enable=true`**. Open decision 4 stays open until M3; changing converters then means recreating the topics, which is acceptable locally. |
| Publication | Created by a goose migration vs autocreated by Debezium | **Migration** `00003_create_cdc_publication.sql` with an explicit table list; `publication.autocreate.mode=disabled`. What is streamed is reviewed as schema, and the connector needs no table-owner rights. |
| Connector registration | One-shot compose service vs a manual make target | **One-shot `register-connector`** doing an idempotent `PUT /connectors/energy-postgres-source/config` from `deploy/connect/`, after Connect is healthy and migrations have run (same pattern as ADR 0013). |
| Replica identity | `FULL` vs `DEFAULT` (primary key only) | **`FULL`** on both tables: an UPDATE event carries the old and the new value, which is the point of streaming revisions. The extra WAL per update is negligible at tens of updates per hour. |
| `decimal.handling.mode` | `string` vs `precise` (Connect Decimal, base64 in JSON) | **`string`**: exact and readable (`"74.78"`). The scale is not in the schema; the sink owns it, as `parseCents`/`parseKW` do on ingest. |
| `interval.handling.mode` | `numeric` (microseconds) vs `string` (ISO 8601) | **`numeric`**: `resolution` arrives as microseconds (`900000000` for PT15M) and maps directly to a Go duration. Debezium's month approximation does not affect 15/30/60-minute values. |

## Consequences
- Topics: `energy.public.day_ahead_price`, `energy.public.actual_load` (1 partition, keyed by the primary key), plus `__debezium-heartbeat.energy` and the Connect internal topics `_connect_*`. Broker auto-creation stays off; Connect creates the topics it needs.
- The connector config holds no secrets: `${env:...}` placeholders are resolved on the worker by Kafka's `EnvVarConfigProvider`.
- The replication slot `debezium_energy` retains WAL while the connector is down. Monitoring slot lag and WAL retention, and a runbook for a stopped connector, are the next step (M2b); until then a long connector outage can fill the Postgres disk.
- A new table enters CDC only through a migration that adds it to `energy_cdc` and to `table.include.list`.
- The down migration drops the publication; the connector must be stopped first.
