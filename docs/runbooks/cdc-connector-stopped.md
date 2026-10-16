# Runbook: CDC connector stopped

Covers the alerts `CDCSlotInactive`, `CDCSlotWALRetentionHigh`, `CDCSlotLost` and `CDCSlotMissing` (`observability/prometheus/rules/cdc.yml`, ADR 0022, 0023).

## What is at stake

Debezium reads `day_ahead_price` and `actual_load` through the logical replication slot `debezium_energy`. While nothing reads from the slot, Postgres keeps every WAL segment written since the slot's confirmed position.

- **Up to 2 GB of retained WAL** (`max_slot_wal_keep_size`): nothing is lost. When the connector comes back it resumes from its last committed offset and streams everything it missed.
- **Beyond 2 GB**: Postgres invalidates the slot (`wal_status = lost`) and frees the WAL. The database is protected, but the connector cannot resume. Recovery is a re-snapshot, and intermediate revisions made during the outage are not recoverable from WAL: consumers receive only the current value of each row.

At the current write rate (tens of rows per hour) 2 GB takes days to accumulate; WAL from other activity (vacuum, checkpoints, other tables) counts as well.

## Diagnose

```bash
curl -s localhost:8083/connectors/energy-postgres-source/status    # connector and task state, stack trace on FAILED
docker compose ps connect                                          # is the Connect worker up?
docker compose logs --tail 200 connect
docker compose exec postgres psql -U energy -d energy -c \
  "SELECT slot_name, active, wal_status, safe_wal_size,
          pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)) AS retained
   FROM pg_replication_slots"
```

| Observation | Go to |
|---|---|
| Connect container down | [Connect worker down](#connect-worker-down) |
| Connector or task `FAILED`, slot `wal_status` not `lost` | [Task failed](#task-failed) |
| Connector not listed | [Connector missing](#connector-missing) |
| Slot `wal_status = lost`, or the task fails with "replication slot ... has been invalidated" | [Slot lost](#slot-lost) |
| No row for `debezium_energy` | [Slot missing](#slot-missing) |

## Connect worker down

```bash
docker compose up -d connect
```

Connector configs and offsets live in the `_connect_*` topics, so the connector resumes on its own. Check that `CDCSlotInactive` resolves within a few minutes.

## Task failed

Read the trace in the status output first. A failure caused by something outside Connect (Postgres restarted, network) is usually fixed by a restart:

```bash
curl -s -X POST 'localhost:8083/connectors/energy-postgres-source/restart?includeTasks=true&onlyFailed=true'
```

If it fails again with the same trace, it is not transient: fix the cause (credentials, a dropped publication, a schema change the connector rejects) before restarting.

## Connector missing

The config was not registered, for example after the `_connect_configs` topic was lost. Re-register it; the call is idempotent:

```bash
docker compose run --rm register-connector
```

If the `_connect_offsets` topic was lost as well, the connector starts without offsets and takes a new snapshot (`snapshot.mode=initial`).

## Slot lost

The slot exists but cannot be resumed from. Recover with a fresh snapshot. **The steps below drop a replication slot and reset connector offsets: get the owner's approval first (CLAUDE.md, Guardrails).**

1. Stop the connector, so it releases the slot and stops retrying:
   ```bash
   curl -s -X PUT localhost:8083/connectors/energy-postgres-source/stop
   ```
2. Delete its offsets, so it does not try to resume from an LSN the slot no longer has:
   ```bash
   curl -s -X DELETE localhost:8083/connectors/energy-postgres-source/offsets
   ```
3. Drop the invalidated slot:
   ```bash
   docker compose exec postgres psql -U energy -d energy -c "SELECT pg_drop_replication_slot('debezium_energy')"
   ```
4. Resume. Without offsets, Debezium creates the slot again and snapshots both tables:
   ```bash
   curl -s -X PUT localhost:8083/connectors/energy-postgres-source/resume
   ```
5. Verify: task `RUNNING`, slot `active = t`, and snapshot events (`"op":"r"`) in `energy.public.*`.

The snapshot re-emits every row. Consumers must treat snapshot events as upserts keyed by the primary key; that requirement is part of the sink design (open decision 5).

## Slot missing

If the connector is `RUNNING` but no slot is reported, check that `postgres-exporter` is up and scraped (`up{job="postgres"}` in Prometheus) before anything else. If the slot is really gone, follow [Slot lost](#slot-lost) from step 1: without its slot, the connector cannot resume either.

## After recovery

- `CDCSlotInactive` and `CDCSlotWALRetentionHigh` resolve on their own once the connector reads again and Postgres recycles the retained WAL at the next checkpoints.
- Record what happened and how long the connector was down, and whether the alert thresholds gave enough time to react.
