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

// Row is one change of one key, ready for a ClickHouse CDC table: the table's own columns in insert
// order, followed on insert by the version and the delete flag.
type Row struct {
	Table     string
	Columns   []any
	LSN       uint64 // version in ReplacingMergeTree
	IsDeleted uint8
}

// envelope is the Debezium change event value. One image struct serves every table: the decoder
// leaves the fields of the other tables empty.
type envelope struct {
	Before *rowImage `avro:"before"`
	After  *rowImage `avro:"after"`
	Source struct {
		LSN *int64 `avro:"lsn"`
	} `avro:"source"`
	Op string `avro:"op"`
}

// rowImage is a row of any CDC table as Debezium encodes it (ADR 0022): timestamps as ISO strings,
// intervals in microseconds, numerics as decimal strings.
type rowImage struct {
	BiddingZone     string `avro:"bidding_zone"`
	DeliveryStart   string `avro:"delivery_start"`
	Resolution      int64  `avro:"resolution"`
	SourceCreatedAt string `avro:"source_created_at"`

	Sequence    int32  `avro:"sequence"` // day_ahead_price, absent before ADR 0029: stays 0
	PriceEURMWh string `avro:"price_eur_mwh"`
	LoadMW      string `avro:"load_mw"`
	PSRType     string `avro:"psr_type"`  // generation
	Direction   string `avro:"direction"` // generation
	QuantityMW  string `avro:"quantity_mw"`

	Point              string  `avro:"point"`      // weather
	ValidFrom          string  `avro:"valid_from"` // weather
	Temperature2mC     *string `avro:"temperature_2m_c"`
	WindSpeed100mMS    *string `avro:"wind_speed_100m_ms"`
	ShortwaveRadiation *string `avro:"shortwave_radiation_wm2"`
	CloudCoverPct      *string `avro:"cloud_cover_pct"`
}

// tables maps the source table of a topic to the columns of its ClickHouse table, in the order of
// insertSQL (clickhouse.go).
var tables = map[string]func(*rowImage) ([]any, error){
	"day_ahead_price": func(r *rowImage) ([]any, error) {
		if r.Sequence < 0 || r.Sequence > 255 {
			return nil, fmt.Errorf("sequence %d out of range", r.Sequence)
		}
		return slotColumns(r, r.PriceEURMWh, r.BiddingZone, uint8(r.Sequence))
	},
	"actual_load": func(r *rowImage) ([]any, error) {
		return slotColumns(r, r.LoadMW, r.BiddingZone)
	},
	"generation": func(r *rowImage) ([]any, error) {
		if r.PSRType == "" || r.Direction == "" {
			return nil, errors.New("generation row without psr_type or direction")
		}
		return slotColumns(r, r.QuantityMW, r.BiddingZone, r.PSRType, r.Direction)
	},
	"weather": weatherColumns,
}

// slotColumns returns key, delivery_start, resolution_us, amount, source_created_at for the market
// tables, which share everything but their key columns and amount.
func slotColumns(r *rowImage, amount string, key ...any) ([]any, error) {
	deliveryStart, err := time.Parse(time.RFC3339Nano, r.DeliveryStart)
	if err != nil {
		return nil, fmt.Errorf("delivery_start: %w", err)
	}
	createdAt, err := parseCreated(r)
	if err != nil {
		return nil, err
	}
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("amount: %w", err)
	}
	return append(key, deliveryStart.UTC(), r.Resolution, value, createdAt), nil
}

func weatherColumns(r *rowImage) ([]any, error) {
	if r.Point == "" {
		return nil, errors.New("weather row without point")
	}
	validFrom, err := time.Parse(time.RFC3339Nano, r.ValidFrom)
	if err != nil {
		return nil, fmt.Errorf("valid_from: %w", err)
	}
	createdAt, err := parseCreated(r)
	if err != nil {
		return nil, err
	}
	cols := []any{r.BiddingZone, r.Point, validFrom.UTC()}
	for _, v := range []*string{r.Temperature2mC, r.WindSpeed100mMS, r.ShortwaveRadiation, r.CloudCoverPct} {
		if v == nil {
			cols = append(cols, (*decimal.Decimal)(nil))
			continue
		}
		d, err := decimal.NewFromString(*v)
		if err != nil {
			return nil, fmt.Errorf("weather value %q: %w", *v, err)
		}
		cols = append(cols, &d)
	}
	return append(cols, createdAt), nil
}

func parseCreated(r *rowImage) (time.Time, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, r.SourceCreatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("source_created_at: %w", err)
	}
	return createdAt.UTC(), nil
}

// errSkip marks an event that carries no row, such as the tombstone Debezium sends after a delete.
var errSkip = errors.New("event has no row")

// toRow converts a decoded change event of the given table into a row. Creates, snapshot reads and
// updates write the after image; a delete writes the before image with is_deleted set, which
// REPLICA IDENTITY FULL makes complete (ADR 0022).
func toRow(tableName string, ev envelope) (Row, error) {
	columns, ok := tables[tableName]
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

	cols, err := columns(image)
	if err != nil {
		return Row{}, err
	}
	return Row{Table: tableName, Columns: cols, LSN: uint64(*ev.Source.LSN), IsDeleted: deleted}, nil
}
