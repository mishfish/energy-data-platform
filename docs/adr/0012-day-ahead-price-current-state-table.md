# 0012: Day-ahead prices as a current-state table

- Status: accepted
- Date: 2026-10-02

## Context
ENTSO-E republishes day-ahead prices (document type A44) with corrections, and the ingestion service re-fetches a sliding window, so the same market time unit arrives many times, sometimes with a changed value. The Postgres table is the CDC origin (ADR 0004), and its shape decides what Debezium emits. Since the move of SDAC to a 15-minute market time unit, the same zone has PT60M data for older dates and PT15M for newer ones.

## Options
1. Current-state table keyed by `(bidding_zone, delivery_start, resolution)`, written with an upsert. A revision becomes an UPDATE, so CDC carries real change events. Revision history lives downstream (Kafka, sink), not in Postgres.
2. Append-only revisions table plus a "latest" view. Full history and point-in-time reads in Postgres, but CDC only ever sees INSERTs, and picking the latest revision moves to the sink or analytics.

## Decision
Option 1. `resolution` is part of the key, so PT60M and PT15M series for the same instant never overwrite each other. `source_created_at` stores the `createdDateTime` of the document that carried the value.

## Consequences
- The upsert must skip no-op writes (`WHERE ... IS DISTINCT FROM ...`); otherwise every re-fetch of the window produces empty UPDATEs that inflate WAL and Kafka traffic.
- The upsert must not let an older document overwrite a newer one (compare `source_created_at`), so a delayed retry cannot roll a value back.
- With the default `REPLICA IDENTITY`, Debezium's before image of an UPDATE contains only the key columns, not the old price. Whether to switch to `REPLICA IDENTITY FULL` is decided with the CDC setup in M2.
- `price_eur_mwh` assumes EUR, which holds for the SDAC zones in scope; a non-EUR zone needs a currency column first.
