# 0007: Local stack baseline

- Status: accepted
- Date: 2026-09-29

## Context
M0 needs a pinned toolchain and base images for the local stack.

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Go module path | — | `github.com/mishfish/energy-data-platform` |
| Go version | 1.27 (latest, installed) vs 1.26 | **1.27** |
| Postgres | 17 (known Debezium support, failover slots) vs 18 (newest, Debezium support unverified) | **17** |
| Kafka image | `apache/kafka` (plain ASF build) vs `confluentinc/cp-kafka` (Confluent distribution, pairs with `cp-schema-registry` and `cp-kafka-connect`) | **`confluentinc/cp-kafka`** |

## Consequences
- The Confluent 8.x line runs in KRaft mode only, which matches ADR 0005.
- Choosing the Kafka Connect image (`cp-kafka-connect` with the Debezium plugin, or `debezium/connect`) is deferred to M2.
