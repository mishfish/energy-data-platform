//go:build integration

package db_test

import (
	"testing"
	"time"

	"github.com/mishfish/energy-data-platform/internal/db"
	"github.com/mishfish/energy-data-platform/internal/openmeteo"
)

func ptr(v int64) *int64 { return &v }

func TestUpsertWeather(t *testing.T) {
	conn := migratedConn(t)
	fetched := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	hour := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	row := func(temp, cloud *int64, at time.Time) openmeteo.Weather {
		return openmeteo.Weather{BiddingZone: zoneNL, Point: "Rotterdam", ValidFrom: hour,
			Temperature: temp, WindSpeed100m: ptr(810), ShortwaveRad: ptr(31250), CloudCover: cloud, SourceCreatedAt: at}
	}
	upsertW := func(t *testing.T, rows ...openmeteo.Weather) int64 {
		t.Helper()
		changed, err := db.UpsertWeather(t.Context(), conn, rows)
		if err != nil {
			t.Fatalf("UpsertWeather: %v", err)
		}
		return changed
	}
	stored := func(t *testing.T) string {
		t.Helper()
		var s string
		if err := conn.QueryRow(t.Context(), `SELECT concat_ws('|', temperature_2m_c, wind_speed_100m_ms, shortwave_radiation_wm2, coalesce(cloud_cover_pct::text, 'NULL')) FROM weather`).Scan(&s); err != nil {
			t.Fatalf("query: %v", err)
		}
		return s
	}

	if changed := upsertW(t, row(ptr(-340), nil, fetched)); changed != 1 {
		t.Fatalf("insert changed %d rows", changed)
	}
	if got := stored(t); got != "-3.40|8.10|312.50|NULL" {
		t.Errorf("stored %s", got)
	}
	// The next model run repeats the same forecast: no-op.
	if changed := upsertW(t, row(ptr(-340), nil, fetched.Add(time.Hour))); changed != 0 {
		t.Errorf("unchanged forecast changed %d rows", changed)
	}
	// A null that becomes a value is a change.
	if changed := upsertW(t, row(ptr(-340), ptr(10000), fetched.Add(2*time.Hour))); changed != 1 {
		t.Errorf("null to value changed %d rows, want 1", changed)
	}
	// A stale fetch does not roll back.
	if changed := upsertW(t, row(ptr(500), ptr(10000), fetched.Add(30*time.Minute))); changed != 0 {
		t.Errorf("older fetch changed %d rows", changed)
	}
	if got := stored(t); got != "-3.40|8.10|312.50|100.00" {
		t.Errorf("stored %s", got)
	}
}
