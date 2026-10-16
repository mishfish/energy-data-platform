# Runbook: sink stalled

Covers `SinkDown`, `SinkStalled` and `SinkLagHigh` (`observability/prometheus/rules/sink.yml`, ADR 0026, 0028).

## What is at stake

The sink copies the CDC topics into ClickHouse and commits Kafka offsets only after a batch is written. While it is stalled, ClickHouse falls behind Postgres; nothing is lost, because the events stay in Kafka until they are committed. Topic retention is the limit: events older than retention are deleted whether the sink has read them or not.

## Diagnose

```bash
docker compose ps sink
docker compose logs --tail 100 sink
curl -s localhost:8082/metrics | grep -E '^sink_(batches_total|consumer_lag|records_total)'
```

| Observation | Go to |
|---|---|
| Logs end with `poison message <topic>[<partition>]@<offset>`, container restarting | [Poison message](#poison-message) |
| `batch write failed, retrying`, `write_error` growing | [ClickHouse unavailable](#clickhouse-unavailable) |
| Running, no errors, lag high and falling | [Catching up](#catching-up) |

## Poison message

The sink stops on a record it cannot decode (fail-fast, ADR 0028) and does not commit it, so every restart stops on the same record. Usual causes: the schema ID in the record is unknown to the registry (registry data lost or pointed at a different registry), or a schema change the sink does not understand.

1. Find the record from the log line: topic, partition, offset.
2. Check the registry: `curl -s localhost:8085/apis/ccompat/v7/schemas/ids/<id>`, where the ID is bytes 1–4 of the record value.
3. Fix the cause (restore the registry, deploy a sink that handles the new schema). The sink then continues from the same offset on its next restart.

Skipping the record means losing that change in ClickHouse until the key changes again. There is no DLQ yet; if a skip is unavoidable, it is done by committing past the offset for the group, which needs the owner's approval (CLAUDE.md, Guardrails: resetting consumer-group offsets).

## ClickHouse unavailable

The sink retries the write with backoff up to 30 s and does not poll meanwhile. After `max.poll.interval.ms` (5 minutes) Kafka removes it from the group; when ClickHouse is back, the write succeeds, the commit fails with a warning, and the records are delivered again after the sink rejoins. The second write collapses in `ReplacingMergeTree`.

1. `docker compose ps clickhouse` and `docker compose logs --tail 100 clickhouse`.
2. Bring ClickHouse back (`docker compose up -d clickhouse`). No action on the sink is needed.

## Catching up

After a re-snapshot (`docs/runbooks/cdc-connector-stopped.md`) or a long outage the sink has a backlog. Lag should fall steadily; `SinkLagHigh` resolves on its own. If it does not fall, check `sink_batch_write_duration_seconds` and raise `SINK_BATCH_SIZE`.

## Verify

ClickHouse matches Postgres when the lag is zero:

```bash
docker compose exec postgres psql -U energy -d energy -tAc "SELECT count(*), sum(price_eur_mwh) FROM day_ahead_price"
docker compose exec clickhouse clickhouse-client -u energy --password energy -d energy \
  -q "SELECT count(), sum(price_eur_mwh) FROM day_ahead_price FINAL"
```
