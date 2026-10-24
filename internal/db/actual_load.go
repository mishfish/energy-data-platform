package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

// Same statement shape and guards as upsertDayAheadPricesSQL (ADR 0012, 0016).
const upsertActualLoadSQL = `
INSERT INTO actual_load AS current
    (bidding_zone, delivery_start, resolution, load_mw, source_created_at)
SELECT *
FROM unnest($1::text[], $2::timestamptz[], $3::interval[], $4::numeric[], $5::timestamptz[])
ON CONFLICT (bidding_zone, delivery_start, resolution) DO UPDATE
SET load_mw           = EXCLUDED.load_mw,
    source_created_at = EXCLUDED.source_created_at,
    updated_at        = now()
WHERE EXCLUDED.load_mw IS DISTINCT FROM current.load_mw
  AND EXCLUDED.source_created_at > current.source_created_at`

// UpsertActualLoad writes load values into actual_load atomically and returns how many rows were
// inserted or changed. The guards and the key uniqueness requirement are those of UpsertDayAheadPrices.
func UpsertActualLoad(ctx context.Context, db Execer, loads []entsoe.ActualLoad) (int64, error) {
	if len(loads) == 0 {
		return 0, nil
	}

	zones := make([]string, len(loads))
	starts := make([]time.Time, len(loads))
	resolutions := make([]time.Duration, len(loads))
	quantities := make([]pgtype.Numeric, len(loads))
	createdAts := make([]time.Time, len(loads))
	for i, l := range loads {
		zones[i] = l.BiddingZone
		starts[i] = l.DeliveryStart
		resolutions[i] = l.Resolution
		quantities[i] = microsToNumeric(l.QuantityW) // MW with six decimals is watts
		createdAts[i] = l.SourceCreatedAt
	}

	tag, err := db.Exec(ctx, upsertActualLoadSQL, zones, starts, resolutions, quantities, createdAts)
	if err != nil {
		return 0, fmt.Errorf("upsert %d actual load values: %w", len(loads), err)
	}
	return tag.RowsAffected(), nil
}
