package sink

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ClickHouseWriter inserts rows into the CDC tables with the native batch protocol.
type ClickHouseWriter struct {
	conn driver.Conn
}

// NewClickHouseWriter connects to the database at dsn (clickhouse://user:password@host:9000/db).
func NewClickHouseWriter(ctx context.Context, dsn string) (*ClickHouseWriter, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse ClickHouse DSN: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping ClickHouse: %w", err)
	}
	return &ClickHouseWriter{conn: conn}, nil
}

// Close closes the connection.
func (w *ClickHouseWriter) Close() error { return w.conn.Close() }

// insertSQL per table, columns in the order the tables map of event.go returns them, then the
// version and the delete flag.
var insertSQL = map[string]string{
	"day_ahead_price": "INSERT INTO day_ahead_price (bidding_zone, sequence, delivery_start, resolution_us, price_eur_mwh, source_created_at, lsn, is_deleted)",
	"actual_load":     "INSERT INTO actual_load (bidding_zone, delivery_start, resolution_us, load_mw, source_created_at, lsn, is_deleted)",
	"generation":      "INSERT INTO generation (bidding_zone, psr_type, direction, delivery_start, resolution_us, quantity_mw, source_created_at, lsn, is_deleted)",
	"weather": "INSERT INTO weather (bidding_zone, point, valid_from, temperature_2m_c, wind_speed_100m_ms, " +
		"shortwave_radiation_wm2, cloud_cover_pct, source_created_at, lsn, is_deleted)",
}

func values(r Row) []any {
	return append(append(make([]any, 0, len(r.Columns)+2), r.Columns...), r.LSN, r.IsDeleted)
}

// Write inserts the rows, one insert per table. Tables are not written atomically together: if the
// second insert fails, the retry writes the first table again, which collapses by key and LSN.
func (w *ClickHouseWriter) Write(ctx context.Context, rows []Row) error {
	byTable := make(map[string][]Row)
	for _, r := range rows {
		byTable[r.Table] = append(byTable[r.Table], r)
	}
	for table, tableRows := range byTable {
		if err := w.insert(ctx, table, tableRows); err != nil {
			return err
		}
	}
	return nil
}

func (w *ClickHouseWriter) insert(ctx context.Context, table string, rows []Row) error {
	query, ok := insertSQL[table]
	if !ok {
		return fmt.Errorf("unknown table %q", table)
	}
	batch, err := w.conn.PrepareBatch(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare insert into %s: %w", table, err)
	}
	for _, r := range rows {
		if err := batch.Append(values(r)...); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("append to %s: %w", table, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("insert %d rows into %s: %w", len(rows), table, err)
	}
	return nil
}
