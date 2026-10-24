package db

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mishfish/energy-data-platform/internal/openmeteo"
)

// Same statement shape as the market tables (ADR 0012, 0016); a row changes when any value does,
// NULLs included.
const upsertWeatherSQL = `
INSERT INTO weather AS current
    (bidding_zone, point, valid_from, temperature_2m_c, wind_speed_100m_ms, shortwave_radiation_wm2, cloud_cover_pct, source_created_at)
SELECT *
FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::numeric[], $5::numeric[], $6::numeric[], $7::numeric[], $8::timestamptz[])
ON CONFLICT (bidding_zone, point, valid_from) DO UPDATE
SET temperature_2m_c        = EXCLUDED.temperature_2m_c,
    wind_speed_100m_ms      = EXCLUDED.wind_speed_100m_ms,
    shortwave_radiation_wm2 = EXCLUDED.shortwave_radiation_wm2,
    cloud_cover_pct         = EXCLUDED.cloud_cover_pct,
    source_created_at       = EXCLUDED.source_created_at,
    updated_at              = now()
WHERE (EXCLUDED.temperature_2m_c, EXCLUDED.wind_speed_100m_ms, EXCLUDED.shortwave_radiation_wm2, EXCLUDED.cloud_cover_pct)
      IS DISTINCT FROM (current.temperature_2m_c, current.wind_speed_100m_ms, current.shortwave_radiation_wm2, current.cloud_cover_pct)
  AND EXCLUDED.source_created_at > current.source_created_at`

// UpsertWeather writes weather rows atomically and returns how many were inserted or changed.
// A key (zone, point, hour) must appear at most once in rows.
func UpsertWeather(ctx context.Context, db Execer, rows []openmeteo.Weather) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	zones := make([]string, len(rows))
	points := make([]string, len(rows))
	validFroms := make([]time.Time, len(rows))
	temps := make([]pgtype.Numeric, len(rows))
	winds := make([]pgtype.Numeric, len(rows))
	rads := make([]pgtype.Numeric, len(rows))
	clouds := make([]pgtype.Numeric, len(rows))
	createdAts := make([]time.Time, len(rows))
	for i, w := range rows {
		zones[i], points[i], validFroms[i], createdAts[i] = w.BiddingZone, w.Point, w.ValidFrom, w.SourceCreatedAt
		temps[i], winds[i], rads[i], clouds[i] = centis(w.Temperature), centis(w.WindSpeed100m), centis(w.ShortwaveRad), centis(w.CloudCover)
	}
	tag, err := db.Exec(ctx, upsertWeatherSQL, zones, points, validFroms, temps, winds, rads, clouds, createdAts)
	if err != nil {
		return 0, fmt.Errorf("upsert %d weather rows: %w", len(rows), err)
	}
	return tag.RowsAffected(), nil
}

// centis converts hundredths to an exact numeric with two decimals; nil becomes NULL.
func centis(v *int64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: big.NewInt(*v), Exp: -2, Valid: true}
}
