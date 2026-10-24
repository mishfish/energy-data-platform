# Roadmap

Milestones are small and demoable. Later milestones are tentative and get refined when the previous one lands.

## M0: Scaffolding (done)
Go module, Makefile, compose stack (Postgres, Kafka, Prometheus, Grafana), CI, `cmd/ingest` skeleton with `/healthz`, `/readyz`, `/metrics` and graceful shutdown.

## M1: First data in Postgres
- ENTSO-E day-ahead prices (document type A44), bidding zone NL only.
- Scheduled fetch over a sliding window, idempotent upsert into Postgres. Re-fetching revised values produces real UPDATEs.
- Schema managed by goose (ADR 0010), access via pgx/v5 (ADR 0011).
- Metrics: fetch count and duration by result, rows upserted, last successful fetch timestamp.
- Alert on ingestion freshness; Grafana panels for freshness and fetch errors.
- Integration test against a real Postgres (testcontainers).

Demo: the stack runs, prices appear in Postgres, stopping the fetcher fires the freshness alert.

## M1.5: More sources and zones (done)
Load (A65, ADR 0019, 0020), every bidding zone with data (ADR 0029), generation per production type (A75, ADR 0030) and hourly Open-Meteo weather per zone point (ADR 0009, 0031).

## M2a: CDC into Kafka
Debezium on Kafka Connect (ADR 0004, 0006, 0022) streams `day_ahead_price` and `actual_load` into Kafka. Revisions appear as UPDATE events with the old and the new value.

Demo: on the synthetic stack (`make up-fake`, ADR 0021), a revised slot shows up in Postgres and as an `op: u` event in its topic.

## M2b: CDC operations (tentative)
Replication slot lag and WAL retention are monitored and alerted on; connector and task state are visible in Grafana. A runbook covers a stopped connector: what WAL retention does to Postgres, how to recover, when a re-snapshot is needed.

## M3: Sink into ClickHouse
Connector switched to Avro with Apicurio Registry (ADR 0025). Idempotent Go sink from Kafka into ClickHouse `ReplacingMergeTree` tables versioned by LSN (ADR 0024, 0026), with consumer lag monitored.

Demo: kill the sink between write and offset commit; after restart the redelivered batch leaves no duplicates in `FINAL` reads.
