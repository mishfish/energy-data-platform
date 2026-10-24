# 0028: ClickHouse sink implementation

- Status: accepted
- Date: 2026-10-16

## Context
ADR 0024–0027 fixed the store, the event format, the delivery semantics and the infrastructure. This ADR records the choices made while building the sink itself (`cmd/sink`, `internal/sink`).

## Options and decisions
| Topic | Options | Decision |
|---|---|---|
| Kafka client | `confluent-kafka-go` (librdkafka, cgo) vs `franz-go` (pure Go) | **`confluent-kafka-go` v2.** The reference client behaviour of librdkafka, and its statistics give consumer lag without an extra exporter. Costs cgo, see Build. |
| Build | Shared Dockerfile with an Alpine builder and musl vs a separate Debian Dockerfile for the sink | **Shared Dockerfile**: the builder is now `golang:1.27-alpine`; `--build-arg CGO=1` installs gcc/musl-dev and builds with `-tags musl` (librdkafka is bundled statically). Other services still build with `CGO_ENABLED=0` and the same Alpine runtime. Amends ADR 0008. |
| Avro decoding | `schemaregistry/serde/avrov2` from confluent-kafka-go (built on `hamba/avro`) vs a hand-written layer over `hamba/avro` | **`avrov2` serde**: reads the Confluent wire format (magic byte and 4-byte schema ID) that the Apicurio converter writes, and resolves the schema references Apicurio registers for `Value`, `Source` and `event.block`, through Apicurio's ccompat API. |
| ClickHouse client | `clickhouse-go/v2` native batches vs `ch-go` | **`clickhouse-go/v2`**, already used for migrations; the volume does not need `ch-go`. |
| Poison messages | Fail-fast vs a dead-letter topic | **Fail-fast**: the sink exits without committing the record, compose restarts it, and it stops again on the same record until someone fixes the cause. A skipped revision would leave a stale value in ClickHouse without anyone noticing. A DLQ is a separate step. |

## Behaviour
- Batches flush at 10,000 records or 2 s. Offsets are committed after the batch is written; a failed write is retried with backoff (1 s to 30 s) until it succeeds or the sink stops.
- A commit that fails after a successful write is logged and not fatal; the records are redelivered and collapse.
- On partition revocation the uncommitted batch is dropped; the next owner re-reads it from the last commit.
- On SIGTERM the sink flushes once more within 10 s, then closes the consumer.
- Row version is the event's `source.lsn`; deletes are written from the `before` image with `is_deleted = 1`; tombstones are skipped.
- Metrics: `sink_records_total{topic,outcome}`, `sink_batches_total{result}`, `sink_batch_write_duration_seconds`, `sink_last_commit_timestamp_seconds`, `sink_consumer_lag{topic,partition}`. Alerts in `rules/sink.yml`, runbook `docs/runbooks/sink-stalled.md`.

## Consequences
- `make test` and `make lint` need cgo (gcc locally); `-race` needed it already.
- The schema registry package of confluent-kafka-go pulls in cloud auth SDKs (Azure, GCP) as module dependencies; they are not used at runtime.
- On the synthetic stack, ClickHouse `FINAL` matches Postgres row for row and to the cent after a full load, including a second full load of the same events by another consumer group.
- `internal/sink/sink_integration_test.go` runs the ADR 0026 scenarios against Kafka (`confluent-local:8.2.4`), Apicurio and ClickHouse in testcontainers: snapshot, revision, delete with tombstone and a replayed snapshot end in the right `FINAL` state; a restart of the same group reads nothing again; a sink killed between write and commit has everything redelivered, and the state is unchanged. CI pulls these three images.
- `topic.metadata.refresh.interval.ms` is 10 s. On a fresh stack the sink subscribes before Debezium creates the topics, and a topic created after the first assignment otherwise waits for librdkafka's 5-minute periodic refresh; found on the first live run, covered by `TestSinkPicksUpTopicsCreatedAfterSubscribing`.
- Not covered yet: a DLQ.
