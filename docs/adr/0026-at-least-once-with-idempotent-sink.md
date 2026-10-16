# 0026: At-least-once delivery with an idempotent sink

- Status: accepted
- Date: 2026-10-16

## Context
Open decision 5. Events can be delivered more than once on every hop: Debezium replays from its last committed offset after a crash, a lost slot leads to a re-snapshot (ADR 0023), and the sink can fail between writing to ClickHouse (ADR 0024) and committing its Kafka offsets. The target state in the store is current state per key, not a log of deliveries.

## Options
1. **At-least-once with an idempotent sink.** The sink commits Kafka offsets only after the batch is written. Redelivered events are written again and collapse by key and version in `ReplacingMergeTree`. Simple to build and to reason about; snapshots and replays need no special handling.
2. **Exactly-once.** Offsets are stored in the store atomically with the data, and the sink resumes from them instead of from Kafka. ClickHouse has no multi-table transactions, so this rests on insert deduplication tokens and careful batching. More moving parts, for the same end state on current-state tables.

## Decision
Option 1.

## Consequences
- Version per row: the LSN of the change from the Debezium source block. Snapshot events carry the snapshot LSN, so a later real change always wins over a replayed snapshot row.
- Offset handling in the sink: commit after a successful write, never before; on shutdown (SIGTERM) finish or abandon the in-flight batch, then commit what was written.
- Failure scenarios to demonstrate: sink killed between write and commit (duplicates collapse), Connect restarted (replay collapses), re-snapshot (no duplicates in `FINAL` reads).
- Consumers that need the full revision history (every intermediate value) are a different use case: they would read the topics directly or a separate append-only table.
