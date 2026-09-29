# 0006: Run Debezium on Kafka Connect

- Status: accepted
- Date: 2026-09-29

## Context
Debezium (ADR 0004) can run as a Kafka Connect connector or as the standalone Debezium Server. The broker is Kafka (ADR 0005).

## Options
1. **Kafka Connect.** This is the standard production setup. It provides a REST API for connector lifecycle (deploy, pause, restart) and JMX metrics that can be exported to Prometheus. It adds one more JVM service with its own internal topics (offsets, configs, status) and rebalancing behaviour.
2. **Debezium Server.** A single process configured by a file, with no Connect internals. It is less common in production and has weaker lifecycle tooling.

## Decision
Kafka Connect, in distributed mode with a single worker locally.

## Consequences
- Connector configs are versioned in the repo and applied through the Connect REST API (for example with a Makefile target).
- Connect's internal topics and their replication and retention settings become part of the platform configuration.
- Connect and Debezium JMX metrics feed the lag and health dashboards.
