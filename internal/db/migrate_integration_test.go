//go:build integration

package db_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mishfish/energy-data-platform/internal/db"
)

// startPostgres runs the same Postgres major version as the compose stack and returns its DSN.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	ctr, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("energy"),
		postgres.WithUsername("energy"),
		postgres.WithPassword("energy"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrate(t *testing.T) {
	dsn := startPostgres(t)
	ctx := t.Context()

	// Re-running on an up-to-date schema must be a no-op.
	for range 2 {
		if err := db.Migrate(ctx, discardLogger(), dsn); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	var pk []string
	err = conn.QueryRow(ctx, `
		SELECT array_agg(a.attname ORDER BY array_position(i.indkey, a.attnum))
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		WHERE i.indrelid = 'day_ahead_price'::regclass AND i.indisprimary`).Scan(&pk)
	if err != nil {
		t.Fatalf("query primary key: %v", err)
	}
	want := []string{"bidding_zone", "sequence", "delivery_start", "resolution"}
	if !slices.Equal(pk, want) {
		t.Fatalf("primary key = %v, want %v", pk, want)
	}

	tests := []struct {
		name       string
		resolution string
		wantErr    bool
	}{
		{name: "15 minutes", resolution: "PT15M"},
		{name: "60 minutes", resolution: "PT60M"},
		{name: "unsupported resolution", resolution: "PT5M", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, `
				INSERT INTO day_ahead_price (bidding_zone, delivery_start, resolution, price_eur_mwh, source_created_at)
				VALUES ('10YNL----------L', '2026-10-01T22:00:00Z', $1::interval, -0.01, now())`, tt.resolution)
			if (err != nil) != tt.wantErr {
				t.Fatalf("insert with resolution %s: err = %v, wantErr %v", tt.resolution, err, tt.wantErr)
			}
		})
	}
}

// The CDC publication must list exactly the streamed tables, each with REPLICA IDENTITY FULL (ADR 0022).
func TestMigrateCDCPublication(t *testing.T) {
	conn := migratedConn(t)

	rows, err := conn.Query(t.Context(), `
		SELECT p.tablename, c.relreplident
		FROM pg_publication_tables p
		JOIN pg_class c ON c.oid = format('%I.%I', p.schemaname, p.tablename)::regclass
		WHERE p.pubname = 'energy_cdc'
		ORDER BY p.tablename`)
	if err != nil {
		t.Fatalf("query publication: %v", err)
	}
	got := map[string]string{}
	for rows.Next() {
		var table, identity string
		if err := rows.Scan(&table, &identity); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[table] = identity
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := map[string]string{"actual_load": "f", "day_ahead_price": "f", "generation": "f", "weather": "f"} // f = FULL
	if len(got) != len(want) {
		t.Fatalf("publication energy_cdc has tables %v, want %v", got, want)
	}
	for table, identity := range want {
		if got[table] != identity {
			t.Errorf("table %s: replica identity %q, want %q", table, got[table], identity)
		}
	}
}

// Several migrators starting at once (e.g. replicas) must serialise on the advisory lock, not fail.
func TestMigrateConcurrent(t *testing.T) {
	dsn := startPostgres(t)
	ctx := t.Context()

	const n = 4
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { errs <- db.Migrate(ctx, discardLogger(), dsn) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}
