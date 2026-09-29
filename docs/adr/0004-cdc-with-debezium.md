# 0004: CDC with Debezium, no custom reader

- Status: accepted
- Date: 2026-09-29

## Context
Postgres changes have to be streamed to the broker. Two options were considered: Debezium, or a custom Go logical-replication reader (pglogrepl, pgoutput). The time budget is about 8 hours a week.

## Options
1. **Debezium.** It is mature and handles snapshots, incremental snapshots, schema changes and slot management out of the box. It is also the de facto standard in production.
2. **Custom Go reader.** It gives full control over the replication protocol, but it re-implements solved problems (snapshotting, TOAST, DDL, slot lifecycle) and would take roughly 20–30 hours.

## Decision
Debezium. A custom reader isn't worth the time. The platform value is in operating CDC reliably, not in rewriting it.

## Consequences
- The CDC work shifts to operating Debezium: replication slot and WAL-retention monitoring, heartbeats, incremental snapshots for backfill, connector restarts, and recovery runbooks.
- The Go work concentrates on the ingestion service and the sink: idempotency and delivery semantics.
- The Debezium runtime (Kafka Connect vs Debezium Server) is still open.
