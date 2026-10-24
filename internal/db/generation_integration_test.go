//go:build integration

package db_test

import (
	"testing"
	"time"

	"github.com/mishfish/energy-data-platform/internal/db"
	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

func TestUpsertGeneration(t *testing.T) {
	conn := migratedConn(t)
	created := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	slot := time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC)
	value := func(psr, direction string, watts int64, at time.Time) entsoe.Generation {
		return entsoe.Generation{BiddingZone: zoneNL, PSRType: psr, Direction: direction, DeliveryStart: slot,
			Resolution: 15 * time.Minute, QuantityW: watts, SourceCreatedAt: at}
	}
	upsertGen := func(t *testing.T, values ...entsoe.Generation) int64 {
		t.Helper()
		changed, err := db.UpsertGeneration(t.Context(), conn, values)
		if err != nil {
			t.Fatalf("UpsertGeneration: %v", err)
		}
		return changed
	}

	// Generation and consumption of pumped storage in the same slot are different rows.
	if changed := upsertGen(t,
		value("B10", entsoe.DirectionGeneration, 300_000000, created),
		value("B10", entsoe.DirectionConsumption, 410_500000, created),
		value("B19", entsoe.DirectionGeneration, 47823_885889, created),
	); changed != 3 {
		t.Fatalf("changed %d rows, want 3", changed)
	}
	if changed := upsertGen(t, value("B10", entsoe.DirectionGeneration, 300_000000, created.Add(time.Hour))); changed != 0 {
		t.Errorf("unchanged re-fetch changed %d rows", changed)
	}
	if changed := upsertGen(t, value("B10", entsoe.DirectionConsumption, 400_000000, created.Add(time.Hour))); changed != 1 {
		t.Errorf("revision changed %d rows, want 1", changed)
	}

	var stored string
	if err := conn.QueryRow(t.Context(), `SELECT string_agg(psr_type || '/' || direction || '=' || quantity_mw::text, ',' ORDER BY psr_type, direction) FROM generation`).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if want := "B10/consumption=400.000000,B10/generation=300.000000,B19/generation=47823.885889"; stored != want {
		t.Errorf("stored %s, want %s", stored, want)
	}

	for name, bad := range map[string]entsoe.Generation{
		"unknown production type": value("B99", entsoe.DirectionGeneration, 1, created),
		"unknown direction":       value("B04", "export", 1, created),
		"negative quantity":       value("B04", entsoe.DirectionGeneration, -1, created),
	} {
		if changed, err := db.UpsertGeneration(t.Context(), conn, []entsoe.Generation{bad}); err == nil {
			t.Errorf("%s: changed %d rows and got no error", name, changed)
		}
	}
}

// The reference table must list exactly the production types the parser accepts.
func TestPSRTypesMatchParser(t *testing.T) {
	conn := migratedConn(t)
	rows, err := conn.Query(t.Context(), `SELECT code, name FROM psr_type`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got := map[string]string{}
	for rows.Next() {
		var code, name string
		if err := rows.Scan(&code, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[code] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != len(entsoe.PSRTypes) {
		t.Errorf("psr_type has %d codes, entsoe.PSRTypes %d", len(got), len(entsoe.PSRTypes))
	}
	for code, name := range entsoe.PSRTypes {
		if got[code] != name {
			t.Errorf("psr_type %s = %q, want %q", code, got[code], name)
		}
	}
}
