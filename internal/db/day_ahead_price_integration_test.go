//go:build integration

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mishfish/energy-data-platform/internal/db"
	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const zoneNL = "10YNL----------L"

func migratedConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := startPostgres(t)
	if err := db.Migrate(t.Context(), discardLogger(), dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func fixturePrices(t *testing.T, name string) []entsoe.DayAheadPrice {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "entsoe", "testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	prices, err := entsoe.ParseDayAheadPrices(f)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return prices
}

// withCreatedAt returns a copy of prices as if they came from a document created at createdAt.
func withCreatedAt(prices []entsoe.DayAheadPrice, createdAt time.Time) []entsoe.DayAheadPrice {
	out := make([]entsoe.DayAheadPrice, len(prices))
	for i, p := range prices {
		p.SourceCreatedAt = createdAt
		out[i] = p
	}
	return out
}

func upsert(t *testing.T, conn *pgx.Conn, prices []entsoe.DayAheadPrice) int64 {
	t.Helper()
	changed, err := db.UpsertDayAheadPrices(t.Context(), conn, prices)
	if err != nil {
		t.Fatalf("UpsertDayAheadPrices: %v", err)
	}
	return changed
}

type storedPrice struct {
	price           string // numeric as text, to check the exact stored value
	sourceCreatedAt time.Time
	ingestedAt      time.Time
	updatedAt       time.Time
}

func loadPrice(t *testing.T, conn *pgx.Conn, deliveryStart string) storedPrice {
	t.Helper()
	var p storedPrice
	err := conn.QueryRow(t.Context(), `
		SELECT price_eur_mwh::text, source_created_at, ingested_at, updated_at
		FROM day_ahead_price
		WHERE bidding_zone = $1 AND delivery_start = $2::timestamptz AND resolution = interval '15 minutes'`,
		zoneNL, deliveryStart).Scan(&p.price, &p.sourceCreatedAt, &p.ingestedAt, &p.updatedAt)
	if err != nil {
		t.Fatalf("load price at %s: %v", deliveryStart, err)
	}
	return p
}

// tableSummary returns the row count, the sum of prices as text and the number of rows ever updated.
func tableSummary(t *testing.T, conn *pgx.Conn) (rows int, sum string, updated int) {
	t.Helper()
	err := conn.QueryRow(t.Context(), `
		SELECT count(*), coalesce(sum(price_eur_mwh), 0)::text, count(*) FILTER (WHERE updated_at <> ingested_at)
		FROM day_ahead_price`).Scan(&rows, &sum, &updated)
	if err != nil {
		t.Fatalf("table summary: %v", err)
	}
	return rows, sum, updated
}

// The steps share one table and run in order: each one starts from the state the previous one left.
// Expected values come from internal/entsoe/testdata/README.md.
func TestUpsertDayAheadPrices(t *testing.T) {
	conn := migratedConn(t)
	original := fixturePrices(t, "a44_nl_pt15m.xml")
	revised := fixturePrices(t, "a44_nl_pt15m_revised.xml")
	originalCreatedAt, revisedCreatedAt := original[0].SourceCreatedAt, revised[0].SourceCreatedAt

	const (
		revisedSlotA  = "2026-10-02T07:00:00Z" // 109.95 -> 122.45
		revisedSlotB  = "2026-10-02T10:45:00Z" // -0.80 -> -4.99
		untouchedSlot = "2026-10-01T22:00:00Z" // 74.78 in both documents
	)

	assertSummary := func(t *testing.T, wantRows int, wantSum string, wantUpdated int) {
		t.Helper()
		rows, sum, updated := tableSummary(t, conn)
		if rows != wantRows || sum != wantSum || updated != wantUpdated {
			t.Errorf("table has %d rows, sum %s, %d updated; want %d rows, sum %s, %d updated",
				rows, sum, updated, wantRows, wantSum, wantUpdated)
		}
	}

	t.Run("empty batch", func(t *testing.T) {
		if changed := upsert(t, conn, nil); changed != 0 {
			t.Errorf("changed %d rows, want 0", changed)
		}
		assertSummary(t, 0, "0", 0)
	})

	t.Run("first document inserts every slot", func(t *testing.T) {
		if changed := upsert(t, conn, original); changed != 96 {
			t.Errorf("changed %d rows, want 96", changed)
		}
		assertSummary(t, 96, "6780.94", 0)
		if got := loadPrice(t, conn, revisedSlotB); got.price != "-0.80" || !got.sourceCreatedAt.Equal(originalCreatedAt) {
			t.Errorf("slot %s: price %s from %s, want -0.80 from %s", revisedSlotB, got.price, got.sourceCreatedAt, originalCreatedAt)
		}
	})

	t.Run("re-fetch with the same prices writes nothing", func(t *testing.T) {
		// Every response has its own createdDateTime, so a re-fetch is newer but otherwise identical.
		refetched := withCreatedAt(original, originalCreatedAt.Add(time.Hour))
		if changed := upsert(t, conn, refetched); changed != 0 {
			t.Errorf("changed %d rows, want 0", changed)
		}
		assertSummary(t, 96, "6780.94", 0)
		if got := loadPrice(t, conn, untouchedSlot); !got.sourceCreatedAt.Equal(originalCreatedAt) {
			t.Errorf("source_created_at moved to %s, want %s", got.sourceCreatedAt, originalCreatedAt)
		}
	})

	t.Run("revision updates only the changed slots", func(t *testing.T) {
		if changed := upsert(t, conn, revised); changed != 2 {
			t.Errorf("changed %d rows, want 2", changed)
		}
		assertSummary(t, 96, "6789.25", 2)
		for slot, want := range map[string]string{revisedSlotA: "122.45", revisedSlotB: "-4.99"} {
			got := loadPrice(t, conn, slot)
			if got.price != want || !got.sourceCreatedAt.Equal(revisedCreatedAt) {
				t.Errorf("slot %s: price %s from %s, want %s from %s", slot, got.price, got.sourceCreatedAt, want, revisedCreatedAt)
			}
			if !got.updatedAt.After(got.ingestedAt) {
				t.Errorf("slot %s: updated_at %s is not after ingested_at %s", slot, got.updatedAt, got.ingestedAt)
			}
		}
		got := loadPrice(t, conn, untouchedSlot)
		if got.price != "74.78" || !got.sourceCreatedAt.Equal(originalCreatedAt) || !got.updatedAt.Equal(got.ingestedAt) {
			t.Errorf("untouched slot changed: %+v", got)
		}
	})

	t.Run("older document does not roll a revision back", func(t *testing.T) {
		if changed := upsert(t, conn, original); changed != 0 {
			t.Errorf("changed %d rows, want 0", changed)
		}
		assertSummary(t, 96, "6789.25", 2)
	})

	t.Run("document with the same timestamp does not overwrite", func(t *testing.T) {
		sameAge := withCreatedAt(original, revisedCreatedAt)
		if changed := upsert(t, conn, sameAge); changed != 0 {
			t.Errorf("changed %d rows, want 0", changed)
		}
		assertSummary(t, 96, "6789.25", 2)
	})

	t.Run("batch mixes inserts, updates and no-ops", func(t *testing.T) {
		nextDay := fixturePrices(t, "a44_nl_pt15m_dst_end.xml")
		later := withCreatedAt(original, revisedCreatedAt.Add(time.Hour)) // reverts the two revised slots
		if changed := upsert(t, conn, append(later, nextDay...)); changed != 102 {
			t.Errorf("changed %d rows, want 102 (100 inserts, 2 updates)", changed)
		}
		assertSummary(t, 196, "13876.15", 2) // 6780.94 + 7095.21
	})

	t.Run("resolution is part of the key", func(t *testing.T) {
		hourly := []entsoe.DayAheadPrice{{
			BiddingZone:     zoneNL,
			DeliveryStart:   original[0].DeliveryStart,
			Resolution:      time.Hour,
			PriceCents:      -50000,
			SourceCreatedAt: originalCreatedAt,
		}}
		if changed := upsert(t, conn, hourly); changed != 1 {
			t.Errorf("changed %d rows, want 1", changed)
		}
		assertSummary(t, 197, "13376.15", 2)
		if got := loadPrice(t, conn, untouchedSlot); got.price != "74.78" {
			t.Errorf("PT15M slot overwritten by the PT60M one: %s", got.price)
		}
	})

	t.Run("duplicate key that is written twice fails without partial writes", func(t *testing.T) {
		batch := fixturePrices(t, "a44_nl_pt15m_revised.xml")
		batch = withCreatedAt(batch, revisedCreatedAt.Add(2*time.Hour))
		batch = append(batch, batch[36]) // position 37 is a revised slot, so the row would be written twice
		if changed, err := db.UpsertDayAheadPrices(t.Context(), conn, batch); err == nil {
			t.Fatalf("changed %d rows and got no error", changed)
		}
		assertSummary(t, 197, "13376.15", 2)
	})
}
