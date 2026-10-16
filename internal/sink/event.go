// Package sink loads Debezium change events from Kafka into ClickHouse with at-least-once delivery
// (ADR 0024–0026, 0028). Offsets are committed only after the batch they belong to is written, and
// redelivered events collapse in ReplacingMergeTree by key and LSN.
package sink

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Row is one change of one key, ready for a ClickHouse CDC table. Both tables share this shape;
// only the name of the amount column differs.
type Row struct {
	Table           string
	BiddingZone     string
	DeliveryStart   time.Time
	ResolutionUS    int64
	Amount          decimal.Decimal // price_eur_mwh or load_mw
	SourceCreatedAt time.Time
	LSN             uint64 // version in ReplacingMergeTree
	IsDeleted       uint8
}

// envelope is the Debezium change event value. One struct serves both tables: the decoder leaves
// the amount field of the other table empty.
type envelope struct {
	Before *rowImage `avro:"before"`
	After  *rowImage `avro:"after"`
	Source struct {
		LSN *int64 `avro:"lsn"`
	} `avro:"source"`
	Op string `avro:"op"`
}

// rowImage is a row of day_ahead_price or actual_load as Debezium encodes it (ADR 0022):
// timestamps as ISO strings, the resolution in microseconds, amounts as decimal strings.
type rowImage struct {
	BiddingZone     string `avro:"bidding_zone"`
	DeliveryStart   string `avro:"delivery_start"`
	Resolution      int64  `avro:"resolution"`
	PriceEURMWh     string `avro:"price_eur_mwh"`
	LoadMW          string `avro:"load_mw"`
	SourceCreatedAt string `avro:"source_created_at"`
}

// table describes how a topic maps onto a ClickHouse table.
type table struct {
	name   string
	amount func(*rowImage) string
}

var tables = map[string]table{
	"day_ahead_price": {name: "day_ahead_price", amount: func(r *rowImage) string { return r.PriceEURMWh }},
	"actual_load":     {name: "actual_load", amount: func(r *rowImage) string { return r.LoadMW }},
}

// errSkip marks an event that carries no row, such as the tombstone Debezium sends after a delete.
var errSkip = errors.New("event has no row")

// toRow converts a decoded change event of the given table into a row. Creates, snapshot reads and
// updates write the after image; a delete writes the before image with is_deleted set, which
// REPLICA IDENTITY FULL makes complete (ADR 0022).
func toRow(tableName string, ev envelope) (Row, error) {
	t, ok := tables[tableName]
	if !ok {
		return Row{}, fmt.Errorf("no ClickHouse table for %q", tableName)
	}
	if ev.Source.LSN == nil || *ev.Source.LSN < 0 {
		return Row{}, errors.New("event without source LSN")
	}

	var image *rowImage
	var deleted uint8
	switch ev.Op {
	case "c", "r", "u":
		image = ev.After
	case "d":
		image, deleted = ev.Before, 1
	default:
		return Row{}, fmt.Errorf("unsupported operation %q", ev.Op)
	}
	if image == nil {
		return Row{}, fmt.Errorf("operation %q without a row image", ev.Op)
	}

	deliveryStart, err := time.Parse(time.RFC3339Nano, image.DeliveryStart)
	if err != nil {
		return Row{}, fmt.Errorf("delivery_start: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, image.SourceCreatedAt)
	if err != nil {
		return Row{}, fmt.Errorf("source_created_at: %w", err)
	}
	amount, err := decimal.NewFromString(t.amount(image))
	if err != nil {
		return Row{}, fmt.Errorf("amount: %w", err)
	}
	return Row{
		Table:           t.name,
		BiddingZone:     image.BiddingZone,
		DeliveryStart:   deliveryStart.UTC(),
		ResolutionUS:    image.Resolution,
		Amount:          amount,
		SourceCreatedAt: createdAt.UTC(),
		LSN:             uint64(*ev.Source.LSN),
		IsDeleted:       deleted,
	}, nil
}
