package db

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

// Execer is the part of pgxpool.Pool, pgx.Conn and pgx.Tx that the write helpers need.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// One statement for the whole batch: the columns travel as parallel arrays and unnest turns them
// back into rows. The WHERE clause holds the two guards from ADR 0012:
//   - a row is rewritten only when the price differs, so re-fetching unchanged data writes nothing
//     and produces no WAL or CDC traffic;
//   - a document older than the one that set the current value never overwrites it.
//
// source_created_at therefore stays at the document that first carried the current price.
const upsertDayAheadPricesSQL = `
INSERT INTO day_ahead_price AS current
    (bidding_zone, sequence, delivery_start, resolution, price_eur_mwh, source_created_at)
SELECT *
FROM unnest($1::text[], $2::smallint[], $3::timestamptz[], $4::interval[], $5::numeric[], $6::timestamptz[])
ON CONFLICT (bidding_zone, sequence, delivery_start, resolution) DO UPDATE
SET price_eur_mwh     = EXCLUDED.price_eur_mwh,
    source_created_at = EXCLUDED.source_created_at,
    updated_at        = now()
WHERE EXCLUDED.price_eur_mwh IS DISTINCT FROM current.price_eur_mwh
  AND EXCLUDED.source_created_at > current.source_created_at`

// UpsertDayAheadPrices writes prices into day_ahead_price atomically and returns how many rows
// were inserted or changed. Rows that already hold the same price, or a price from a newer
// document, are left untouched and not counted.
//
// A key (zone, sequence, delivery start, resolution) must appear at most once in prices. Postgres rejects a
// batch only when it would write the same row twice; a duplicate that the guards skip goes through
// silently, so the caller, not the database, has to keep keys unique.
func UpsertDayAheadPrices(ctx context.Context, db Execer, prices []entsoe.DayAheadPrice) (int64, error) {
	if len(prices) == 0 {
		return 0, nil
	}

	zones := make([]string, len(prices))
	sequences := make([]int16, len(prices))
	starts := make([]time.Time, len(prices))
	resolutions := make([]time.Duration, len(prices))
	amounts := make([]pgtype.Numeric, len(prices))
	createdAts := make([]time.Time, len(prices))
	for i, p := range prices {
		zones[i] = p.BiddingZone
		sequences[i] = int16(p.Sequence) //nolint:gosec // the parser bounds the sequence to 0..99
		starts[i] = p.DeliveryStart
		resolutions[i] = p.Resolution
		amounts[i] = microsToNumeric(p.PriceMicros)
		createdAts[i] = p.SourceCreatedAt
	}

	tag, err := db.Exec(ctx, upsertDayAheadPricesSQL, zones, sequences, starts, resolutions, amounts, createdAts)
	if err != nil {
		return 0, fmt.Errorf("upsert %d day-ahead prices: %w", len(prices), err)
	}
	return tag.RowsAffected(), nil
}

// microsToNumeric converts integer millionths (ADR 0029) to an exact numeric with six decimals.
func microsToNumeric(micros int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(micros), Exp: -6, Valid: true}
}
