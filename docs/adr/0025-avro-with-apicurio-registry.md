# 0025: Avro events with the Apicurio schema registry

- Status: accepted
- Date: 2026-10-16

## Context
Open decision 4. In M2 the connector emits JSON with an inline schema in every message (ADR 0022), which was meant to last until the sink's requirements were known. The sink (M3) needs typed events and a defined path for schema evolution from Postgres to ClickHouse; the JSON setup gives neither compatibility checks nor compact messages.

## Options
1. **Avro with Apicurio Registry.** The Apicurio converters ship with the Debezium Connect image and are enabled by configuration. The registry exposes a Confluent-compatible API and enforces compatibility rules per subject. Go reads Avro with `hamba/avro` and fetches writer schemas from the registry. One more service in compose.
2. **Protobuf with Confluent Schema Registry.** The most natural fit for Go (generated types). The Debezium image has no Protobuf converter, so Connect needs a custom image with Confluent's converter, plus Confluent SR.
3. **Keep JSON with inline schemas.** No new components, but every message carries its schema, and nothing checks compatibility.

## Decision
Option 1: Avro, with Apicurio Registry as the schema registry.

## Consequences
- The connector switches from JsonConverter to the Apicurio Avro converter. Existing JSON topics are recreated; locally that means a re-snapshot, which needs owner approval (topic deletion, CLAUDE.md Guardrails).
- Compatibility is `BACKWARD` per subject: a schema change that breaks existing readers is rejected at the registry, before it reaches the sink. Schema evolution scenarios get their own runbook.
- `decimal.handling.mode=string` and `interval.handling.mode=numeric` from ADR 0022 stay: Avro then carries the amounts as strings and durations as longs, and the sink keeps owning the scale.
- The serialization row of ADR 0022 is superseded by this ADR.
