# 0005: Apache Kafka (KRaft) as the broker

- Status: accepted
- Date: 2026-09-29

## Context
Debezium (ADR 0004) needs a broker for change events. Both candidates speak the Kafka protocol.

## Options
1. **Apache Kafka (KRaft, no ZooKeeper).** It is the industry standard, and Kafka Connect and Debezium run on it natively. Its footprint is heavier (JVM), and a schema registry has to be run separately.
2. **Redpanda.** It is a single binary with a built-in schema registry and is light to run locally. It is less widely deployed, and some of its features are license-gated.

## Decision
Apache Kafka in KRaft mode.

## Consequences
- The operational practices in this project (topic config, retention, consumer lag, broker metrics) apply directly to typical production Kafka deployments.
- If Avro or Protobuf is chosen (open decision), a separate schema registry service is needed.
- The local stack is heavier, so the compose setup should use a single broker with conservative JVM heap settings.
