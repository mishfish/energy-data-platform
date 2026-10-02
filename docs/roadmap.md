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

## M1.5: More sources (tentative)
Load (A65), generation per production type (A75), Open-Meteo weather (ADR 0009). More bidding zones once open decision 10 (zone-to-coordinate mapping) is settled.

## M2: CDC into Kafka (tentative)
Debezium on Kafka Connect (ADR 0004, 0006) streams the Postgres tables into Kafka. Replication slot lag and WAL retention are monitored.

## M3: Sink into the analytical store (tentative)
Idempotent Go sink from Kafka. Depends on open decisions 3 (analytical store), 4 (serialization) and 5 (delivery semantics).
