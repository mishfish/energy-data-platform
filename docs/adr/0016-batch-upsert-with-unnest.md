# 0016: Batch upsert of prices with one unnest statement

- Status: accepted
- Date: 2026-10-10

## Context
Each scheduled fetch returns a window of day-ahead prices, 96 to a few hundred rows, most of them unchanged since the previous fetch. They go into the current-state table from ADR 0012 with two guards: skip no-op writes, and never let an older document overwrite a newer one. The number of rows actually written feeds the ingestion metrics and equals the number of change events CDC will emit.

## Options
1. One `INSERT ... SELECT FROM unnest($1::text[], ...) ON CONFLICT DO UPDATE ... WHERE`. One round trip and one statement per window, atomic without an explicit transaction, and `RowsAffected` is the exact count of inserted plus changed rows. Columns travel as parallel arrays with explicit casts.
2. `pgx.Batch` with one upsert per row. Simpler SQL and per-row errors, but hundreds of statements per window, an explicit transaction for atomicity, and the changed-row count has to be summed from every result.

## Decision
Option 1, in `db.UpsertDayAheadPrices`. The update guard is:

```sql
WHERE EXCLUDED.price_eur_mwh IS DISTINCT FROM current.price_eur_mwh
  AND EXCLUDED.source_created_at > current.source_created_at
```

## Consequences
- A re-fetch with unchanged prices writes nothing, so it produces no WAL and no CDC events, even though every API response carries a new `createdDateTime`.
- `source_created_at` is the document that first carried the current price, not the last document seen. It does not advance on unchanged re-fetches.
- A document with the same `createdDateTime` as the stored one never overwrites it (strict `>`). A value lost that way is picked up by the next fetch, which has a later timestamp.
- The count does not distinguish inserts from updates. Splitting them needs `RETURNING (xmax = 0)` and is deferred until a metric needs it.
- Postgres rejects a batch only when it would write the same row twice ("ON CONFLICT DO UPDATE command cannot affect row a second time"); a duplicate key that the guards skip passes silently. Key uniqueness within a batch is the caller's job: the parser guarantees it per document, and a caller that merges documents must deduplicate.
- The arrays are bounded by the fetch window. A large backfill must chunk its input or switch to `COPY` into a staging table.
