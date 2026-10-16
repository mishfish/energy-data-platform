# 0023: Replication slot monitoring and a cap on retained WAL

- Status: accepted
- Date: 2026-10-16

## Context
The Debezium connector (ADR 0022) reads through the logical replication slot `debezium_energy`. When the connector stops, Postgres retains WAL for the slot indefinitely by default, so a long outage of a non-critical consumer can fill the disk and stop the source of truth, and with it ingestion. The platform needs to see this coming and to bound the damage.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Source of slot metrics | `postgres_exporter` vs a custom collector in `ingest` | **`postgres_exporter` v0.20.1**: standard, no code, and independent of `ingest`, whose outage would otherwise blind CDC monitoring too. Its `pg_replication_slots_*` metrics give `slot_is_active`, `safe_wal_size_bytes`, `wal_status` and lag in bytes. |
| Connector metrics | Slot metrics only vs also a JMX exporter on Connect | **Slot metrics only** for now. A stopped connector, failed task or dead worker all show up as an inactive slot with growing retention, seen from the database side. JMX (lag behind source, task state) is added when its detail is needed. |
| Retained WAL | `max_slot_wal_keep_size = 2GB` vs unlimited (default) | **2 GB.** Postgres invalidates the slot instead of filling the disk. Losing the slot means a re-snapshot, which is recoverable; a full disk takes down Postgres and ingestion with it. |

## Alerts (`observability/prometheus/rules/cdc.yml`)
- `CDCSlotInactive` (warning): no consumer for 5 minutes.
- `CDCSlotWALRetentionHigh` (critical): less than 1 GiB of headroom left (`safe_wal_size_bytes`), i.e. half the cap used.
- `CDCSlotLost` (critical): `wal_status = lost`, re-snapshot required.
- `CDCSlotMissing` (critical): the slot is not reported for 10 minutes (dropped, never created, or exporter down).

## Consequences
- An outage up to 2 GB of WAL is invisible to consumers: the connector resumes where it stopped. Beyond that, consumers get a fresh snapshot with the current value of every row, and revisions made during the outage are not replayed. Sinks must treat snapshot events as upserts (open decision 5).
- The cap applies to every slot in the cluster, including any added later.
- Recovery steps are in `docs/runbooks/cdc-connector-stopped.md`; the "Slot lost" procedure drops a slot and resets offsets, which needs the owner's approval.
- The CDC Grafana dashboard (`observability/grafana/dashboards/cdc.json`) shows consumer state, WAL status, headroom and lag.
- Changing `max_slot_wal_keep_size` needs a Postgres restart; Debezium reconnects on its own (observed on the local stack).
