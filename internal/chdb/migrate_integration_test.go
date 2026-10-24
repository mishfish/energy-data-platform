//go:build integration

package chdb_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/clickhouse"

	"github.com/mishfish/energy-data-platform/internal/chdb"
)

// startClickHouse runs the same ClickHouse version as the compose stack and returns its DSN.
func startClickHouse(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	ctr, err := clickhouse.Run(ctx, "clickhouse/clickhouse-server:26.8.28.14",
		clickhouse.WithDatabase("energy"),
		clickhouse.WithUsername("energy"),
		clickhouse.WithPassword("energy"),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start clickhouse: %v", err)
	}
	dsn, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

func migrated(t *testing.T) *sql.DB {
	t.Helper()
	dsn := startClickHouse(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Re-running on an up-to-date schema must be a no-op.
	for range 2 {
		if err := chdb.Migrate(t.Context(), logger, dsn); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The tables must collapse redelivered and revised events to the row with the highest LSN, and
// drop a key whose latest row is a delete (ADR 0026). This is what makes at-least-once safe.
func TestCDCTablesCollapseByLSN(t *testing.T) {
	db := migrated(t)
	ctx := t.Context()

	exec := func(t *testing.T, query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	current := func(t *testing.T) map[string]string {
		t.Helper()
		rows, err := db.QueryContext(ctx, `
			SELECT toString(delivery_start), toString(price_eur_mwh)
			FROM day_ahead_price FINAL
			ORDER BY delivery_start`)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer func() { _ = rows.Close() }()
		got := map[string]string{}
		for rows.Next() {
			var slot, price string
			if err := rows.Scan(&slot, &price); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got[slot] = price
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return got
	}
	const insert = `INSERT INTO day_ahead_price
		(bidding_zone, delivery_start, resolution_us, price_eur_mwh, source_created_at, lsn, is_deleted) VALUES `

	// Snapshot of two slots at LSN 100.
	exec(t, insert+`
		('10YNL----------L', '2026-10-02 07:00:00', 900000000, 109.95, '2026-10-01 11:02:37', 100, 0),
		('10YNL----------L', '2026-10-02 10:45:00', 900000000, -0.80, '2026-10-01 11:02:37', 100, 0)`)
	// A revision of the first slot, then the whole snapshot redelivered (at-least-once replay),
	// then a delete of the second slot.
	exec(t, insert+`('10YNL----------L', '2026-10-02 07:00:00', 900000000, 122.45, '2026-10-02 09:15:04', 250, 0)`)
	exec(t, insert+`
		('10YNL----------L', '2026-10-02 07:00:00', 900000000, 109.95, '2026-10-01 11:02:37', 100, 0),
		('10YNL----------L', '2026-10-02 10:45:00', 900000000, -0.80, '2026-10-01 11:02:37', 100, 0)`)
	exec(t, insert+`('10YNL----------L', '2026-10-02 10:45:00', 900000000, -0.80, '2026-10-01 11:02:37', 300, 1)`)

	got := current(t)
	want := map[string]string{"2026-10-02 07:00:00.000": "122.45"}
	if len(got) != len(want) || got["2026-10-02 07:00:00.000"] != want["2026-10-02 07:00:00.000"] {
		t.Errorf("FINAL read = %v, want %v", got, want)
	}

	// Merging must reach the same state that FINAL reads before the merge.
	exec(t, `OPTIMIZE TABLE day_ahead_price FINAL`)
	if got := current(t); len(got) != 1 || got["2026-10-02 07:00:00.000"] != "122.45" {
		t.Errorf("after OPTIMIZE: %v, want %v", got, want)
	}
}

func TestCDCTablesExist(t *testing.T) {
	db := migrated(t)
	rows, err := db.QueryContext(context.Background(), `
		SELECT name, engine FROM system.tables
		WHERE database = currentDatabase() AND name IN ('day_ahead_price', 'actual_load')
		ORDER BY name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var name, engine string
		if err := rows.Scan(&name, &engine); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = engine
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, table := range []string{"actual_load", "day_ahead_price"} {
		if got[table] != "ReplacingMergeTree" {
			t.Errorf("table %s: engine %q, want ReplacingMergeTree", table, got[table])
		}
	}
}

// Price sequences of one slot are different keys and must not collapse into each other (ADR 0029).
func TestPriceSequencesAreSeparateKeys(t *testing.T) {
	db := migrated(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO day_ahead_price
		(bidding_zone, sequence, delivery_start, resolution_us, price_eur_mwh, source_created_at, lsn, is_deleted) VALUES
		('10Y1001A1001A82H', 1, '2026-10-22 22:00:00', 900000000, 174.31, '2026-10-22 11:00:00', 10, 0),
		('10Y1001A1001A82H', 2, '2026-10-22 22:00:00', 900000000, 186.44, '2026-10-22 11:00:00', 11, 0),
		('10Y1001A1001A82H', 0, '2026-10-22 22:15:00', 900000000, 173.869995, '2026-10-22 11:00:00', 12, 0)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var rows uint64
	var prices string
	if err := db.QueryRowContext(ctx, `
		SELECT count(), arrayStringConcat(groupArray(toString(price_eur_mwh)), ',')
		FROM (SELECT * FROM day_ahead_price FINAL ORDER BY delivery_start, sequence)`).Scan(&rows, &prices); err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows != 3 || prices != "174.31,186.44,173.869995" {
		t.Errorf("FINAL has %d rows %s, want 3 rows 174.31,186.44,173.869995", rows, prices)
	}
}
