# Energy Data Platform

A data platform for the European electricity market. A Go service ingests ENTSO-E (prices, load, generation) and KNMI weather into Postgres. Debezium on Kafka Connect streams the changes into Kafka, and a Go sink loads them into an analytical store. The focus is on platform engineering: delivery guarantees, observability, and failure handling.

> Work in progress. Design decisions are in [`docs/adr/`](docs/adr/).

## Quickstart (local)

```bash
cp .env.example .env   # fill in API tokens
make up                # Postgres, Kafka (KRaft), ingest, Prometheus, Grafana
make ps
make test lint
```

| Service | URL |
|---|---|
| Grafana | http://localhost:3000 |
| Prometheus | http://localhost:9090 |
| ingest (health, metrics) | http://localhost:8080/readyz, /metrics |
| Kafka (host) | localhost:9092 |
| Postgres | localhost:5432 |
