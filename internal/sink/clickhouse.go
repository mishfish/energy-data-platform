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

// insertSQL per table; only day_ahead_price has a sequence (ADR 0029).
var insertSQL = map[string]string{
	"day_ahead_price": "INSERT INTO day_ahead_price (bidding_zone, sequence, delivery_start, resolution_us, price_eur_mwh, source_created_at, lsn, is_deleted)",
	"actual_load":     "INSERT INTO actual_load (bidding_zone, delivery_start, resolution_us, load_mw, source_created_at, lsn, is_deleted)",
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
		values := []any{r.BiddingZone, r.DeliveryStart, r.ResolutionUS, r.Amount, r.SourceCreatedAt, r.LSN, r.IsDeleted}
		if table == "day_ahead_price" {
			values = append([]any{r.BiddingZone, r.Sequence}, values[1:]...)
		}
		if err := batch.Append(values...); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("append to %s: %w", table, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("insert %d rows into %s: %w", len(rows), table, err)
	}
	return nil
}
