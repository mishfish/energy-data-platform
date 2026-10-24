package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

// Same statement shape and guards as upsertDayAheadPricesSQL (ADR 0012, 0016).
const upsertGenerationSQL = `
INSERT INTO generation AS current
    (bidding_zone, psr_type, direction, delivery_start, resolution, quantity_mw, source_created_at)
SELECT *
FROM unnest($1::text[], $2::text[], $3::text[], $4::timestamptz[], $5::interval[], $6::numeric[], $7::timestamptz[])
ON CONFLICT (bidding_zone, psr_type, direction, delivery_start, resolution) DO UPDATE
SET quantity_mw       = EXCLUDED.quantity_mw,
    source_created_at = EXCLUDED.source_created_at,
    updated_at        = now()
WHERE EXCLUDED.quantity_mw IS DISTINCT FROM current.quantity_mw
  AND EXCLUDED.source_created_at > current.source_created_at`

// UpsertGeneration writes generation values atomically and returns how many rows were inserted or
// changed. The guards and the key uniqueness requirement are those of UpsertDayAheadPrices.
func UpsertGeneration(ctx context.Context, db Execer, values []entsoe.Generation) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}

	zones := make([]string, len(values))
	psrTypes := make([]string, len(values))
	directions := make([]string, len(values))
	starts := make([]time.Time, len(values))
	resolutions := make([]time.Duration, len(values))
	quantities := make([]pgtype.Numeric, len(values))
	createdAts := make([]time.Time, len(values))
	for i, v := range values {
		zones[i] = v.BiddingZone
		psrTypes[i] = v.PSRType
		directions[i] = v.Direction
		starts[i] = v.DeliveryStart
		resolutions[i] = v.Resolution
		quantities[i] = microsToNumeric(v.QuantityW) // MW with six decimals is watts
		createdAts[i] = v.SourceCreatedAt
	}

	tag, err := db.Exec(ctx, upsertGenerationSQL, zones, psrTypes, directions, starts, resolutions, quantities, createdAts)
	if err != nil {
		return 0, fmt.Errorf("upsert %d generation values: %w", len(values), err)
	}
	return tag.RowsAffected(), nil
}
