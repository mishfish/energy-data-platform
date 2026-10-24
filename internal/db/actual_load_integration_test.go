//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mishfish/energy-data-platform/internal/db"
	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

func fixtureLoad(t *testing.T, name string) []entsoe.ActualLoad {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "entsoe", "testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	loads, err := entsoe.ParseActualLoad(f)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return loads
}

func upsertLoad(t *testing.T, conn *pgx.Conn, loads []entsoe.ActualLoad) int64 {
	t.Helper()
	changed, err := db.UpsertActualLoad(t.Context(), conn, loads)
	if err != nil {
		t.Fatalf("UpsertActualLoad: %v", err)
	}
	return changed
}

// loadSummary returns the row count, the sum of load_mw as text and the number of rows ever updated.
func loadSummary(t *testing.T, conn *pgx.Conn) (rows int, sum string, updated int) {
	t.Helper()
	err := conn.QueryRow(t.Context(), `
		SELECT count(*), coalesce(sum(load_mw), 0)::text, count(*) FILTER (WHERE updated_at <> ingested_at)
		FROM actual_load`).Scan(&rows, &sum, &updated)
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	return rows, sum, updated
}

func storedLoad(t *testing.T, conn *pgx.Conn, deliveryStart string) string {
	t.Helper()
	var mw string
	err := conn.QueryRow(t.Context(), `
		SELECT load_mw::text FROM actual_load
		WHERE bidding_zone = $1 AND delivery_start = $2::timestamptz AND resolution = interval '15 minutes'`,
		zoneNL, deliveryStart).Scan(&mw)
	if err != nil {
		t.Fatalf("load value at %s: %v", deliveryStart, err)
	}
	return mw
}

// The steps share one table and run in order. Expected values come from
// internal/entsoe/testdata/README.md; sums there are in kW, here in MW.
func TestUpsertActualLoad(t *testing.T) {
	conn := migratedConn(t)

	assertSummary := func(t *testing.T, wantRows int, wantSum string, wantUpdated int) {
		t.Helper()
		rows, sum, updated := loadSummary(t, conn)
		if rows != wantRows || sum != wantSum || updated != wantUpdated {
			t.Errorf("table has %d rows, sum %s, %d updated; want %d rows, sum %s, %d updated",
				rows, sum, updated, wantRows, wantSum, wantUpdated)
		}
	}

	t.Run("partial day inserts the published slots", func(t *testing.T) {
		if changed := upsertLoad(t, conn, fixtureLoad(t, "a65_nl_pt15m_partial.xml")); changed != 56 {
			t.Errorf("changed %d rows, want 56", changed)
		}
		assertSummary(t, 56, "764992.059000", 0)
	})

	t.Run("full day adds only the missing slots", func(t *testing.T) {
		if changed := upsertLoad(t, conn, fixtureLoad(t, "a65_nl_pt15m.xml")); changed != 40 {
			t.Errorf("changed %d rows, want 40", changed)
		}
		assertSummary(t, 96, "1258023.747000", 0)
		if got := storedLoad(t, conn, "2026-10-01T22:00:00Z"); got != "13663.500000" {
			t.Errorf("first slot stored as %s, want 13663.500", got)
		}
	})

	t.Run("revision updates only the changed slots", func(t *testing.T) {
		if changed := upsertLoad(t, conn, fixtureLoad(t, "a65_nl_pt15m_revised.xml")); changed != 2 {
			t.Errorf("changed %d rows, want 2", changed)
		}
		assertSummary(t, 96, "1258301.413000", 2)
		for slot, want := range map[string]string{"2026-10-02T08:00:00Z": "13987.250000", "2026-10-02T15:15:00Z": "12004.000000"} {
			if got := storedLoad(t, conn, slot); got != want {
				t.Errorf("slot %s: %s MW, want %s", slot, got, want)
			}
		}
	})

	t.Run("older document does not roll a revision back", func(t *testing.T) {
		if changed := upsertLoad(t, conn, fixtureLoad(t, "a65_nl_pt15m.xml")); changed != 0 {
			t.Errorf("changed %d rows, want 0", changed)
		}
		assertSummary(t, 96, "1258301.413000", 2)
	})

	t.Run("negative load is rejected by the table", func(t *testing.T) {
		bad := fixtureLoad(t, "a65_nl_pt15m_dst_end.xml")[:1]
		bad[0].QuantityW = -1
		if changed, err := db.UpsertActualLoad(t.Context(), conn, bad); err == nil {
			t.Fatalf("changed %d rows and got no error", changed)
		}
		assertSummary(t, 96, "1258301.413000", 2)
	})
}
