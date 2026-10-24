package sink

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func lsn(v int64) *int64 { return &v }

func image(price, load string) *rowImage {
	return &rowImage{
		BiddingZone:     "10YNL----------L",
		DeliveryStart:   "2026-10-02T07:00:00.000000Z",
		Resolution:      900_000_000,
		PriceEURMWh:     price,
		LoadMW:          load,
		SourceCreatedAt: "2026-10-02T09:15:04.000000Z",
	}
}

func event(op string, before, after *rowImage, sourceLSN *int64) envelope {
	ev := envelope{Before: before, After: after, Op: op}
	ev.Source.LSN = sourceLSN
	return ev
}

func str(s string) *string { return &s }

var (
	slot    = time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	created = time.Date(2026, 10, 2, 9, 15, 4, 0, time.UTC)
)

// sameColumns compares insert values, decimals by value and times by instant.
func sameColumns(t *testing.T, got, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d columns %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		switch w := want[i].(type) {
		case decimal.Decimal:
			if g, ok := got[i].(decimal.Decimal); !ok || !g.Equal(w) {
				t.Errorf("column %d = %v, want %s", i, got[i], w)
			}
		case *decimal.Decimal:
			g, ok := got[i].(*decimal.Decimal)
			if !ok || (g == nil) != (w == nil) || (w != nil && !g.Equal(*w)) {
				t.Errorf("column %d = %v, want %v", i, got[i], w)
			}
		case time.Time:
			if g, ok := got[i].(time.Time); !ok || !g.Equal(w) || g.Location() != time.UTC {
				t.Errorf("column %d = %v, want %s in UTC", i, got[i], w)
			}
		default:
			if got[i] != w {
				t.Errorf("column %d = %v (%T), want %v (%T)", i, got[i], got[i], w, w)
			}
		}
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestToRow(t *testing.T) {
	gen := image("", "")
	gen.QuantityMW, gen.PSRType, gen.Direction = "410.5", "B10", "consumption"
	seq := image("186.44", "")
	seq.Sequence = 2

	tests := []struct {
		name        string
		table       string
		ev          envelope
		want        []any
		wantDeleted uint8
		wantLSN     uint64
	}{
		{"snapshot read", "day_ahead_price", event("r", nil, image("109.95", ""), lsn(100)),
			[]any{"10YNL----------L", uint8(0), slot, int64(900_000_000), dec("109.95"), created}, 0, 100},
		{"price sequence", "day_ahead_price", event("c", nil, seq, lsn(101)),
			[]any{"10YNL----------L", uint8(2), slot, int64(900_000_000), dec("186.44"), created}, 0, 101},
		{"insert load", "actual_load", event("c", nil, image("", "13663.500"), lsn(120)),
			[]any{"10YNL----------L", slot, int64(900_000_000), dec("13663.5"), created}, 0, 120},
		{"update writes the after image", "day_ahead_price", event("u", image("109.95", ""), image("122.45", ""), lsn(250)),
			[]any{"10YNL----------L", uint8(0), slot, int64(900_000_000), dec("122.45"), created}, 0, 250},
		{"delete writes the before image", "day_ahead_price", event("d", image("-0.80", ""), nil, lsn(300)),
			[]any{"10YNL----------L", uint8(0), slot, int64(900_000_000), dec("-0.8"), created}, 1, 300},
		{"six decimals", "day_ahead_price", event("c", nil, image("173.869995", ""), lsn(310)),
			[]any{"10YNL----------L", uint8(0), slot, int64(900_000_000), dec("173.869995"), created}, 0, 310},
		{"generation", "generation", event("u", gen, gen, lsn(7)),
			[]any{"10YNL----------L", "B10", "consumption", slot, int64(900_000_000), dec("410.5"), created}, 0, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, err := toRow(tt.table, tt.ev)
			if err != nil {
				t.Fatalf("toRow: %v", err)
			}
			if row.Table != tt.table || row.IsDeleted != tt.wantDeleted || row.LSN != tt.wantLSN {
				t.Errorf("got table %s, deleted %d, lsn %d", row.Table, row.IsDeleted, row.LSN)
			}
			sameColumns(t, row.Columns, tt.want)
			if v := values(row); len(v) != len(tt.want)+2 || v[len(v)-2] != tt.wantLSN || v[len(v)-1] != tt.wantDeleted {
				t.Errorf("insert values %v do not end with lsn and is_deleted", v)
			}
		})
	}
}

func TestToRowWeather(t *testing.T) {
	img := &rowImage{
		BiddingZone: "10YNL----------L", Point: "Rotterdam", ValidFrom: "2026-10-24T05:00:00.000000Z",
		Temperature2mC: str("-3.40"), WindSpeed100mMS: str("8.10"), ShortwaveRadiation: nil, CloudCoverPct: str("100.00"),
		SourceCreatedAt: "2026-10-24T10:00:00.000000Z",
	}
	row, err := toRow("weather", event("c", nil, img, lsn(9)))
	if err != nil {
		t.Fatalf("toRow: %v", err)
	}
	temp, wind, cloud := dec("-3.4"), dec("8.1"), dec("100")
	sameColumns(t, row.Columns, []any{
		"10YNL----------L", "Rotterdam", time.Date(2026, 10, 24, 5, 0, 0, 0, time.UTC),
		&temp, &wind, (*decimal.Decimal)(nil), &cloud, time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
	})

	img.Point = ""
	if _, err := toRow("weather", event("c", nil, img, lsn(9))); err == nil {
		t.Error("no error for a weather row without point")
	}
}

func TestToRowErrors(t *testing.T) {
	bad := func(mutate func(*rowImage)) *rowImage {
		img := image("1.00", "1.000")
		mutate(img)
		return img
	}
	noPSR := image("", "")
	noPSR.QuantityMW = "1"
	tests := []struct {
		name    string
		table   string
		ev      envelope
		wantErr string
	}{
		{"unknown table", "outages", event("c", nil, image("1", ""), lsn(1)), "no ClickHouse table"},
		{"missing LSN", "day_ahead_price", event("c", nil, image("1", ""), nil), "without source LSN"},
		{"truncate", "day_ahead_price", event("t", nil, nil, lsn(1)), `unsupported operation "t"`},
		{"update without after", "day_ahead_price", event("u", image("1", ""), nil, lsn(1)), "without a row image"},
		{"delete without before", "day_ahead_price", event("d", nil, nil, lsn(1)), "without a row image"},
		{"bad delivery start", "day_ahead_price", event("c", nil, bad(func(r *rowImage) { r.DeliveryStart = "yesterday" }), lsn(1)), "delivery_start"},
		{"bad created at", "day_ahead_price", event("c", nil, bad(func(r *rowImage) { r.SourceCreatedAt = "" }), lsn(1)), "source_created_at"},
		{"bad amount", "actual_load", event("c", nil, bad(func(r *rowImage) { r.LoadMW = "n/a" }), lsn(1)), "amount"},
		{"sequence out of range", "day_ahead_price", event("c", nil, bad(func(r *rowImage) { r.Sequence = 300 }), lsn(1)), "sequence 300"},
		{"generation without psr_type", "generation", event("c", nil, noPSR, lsn(1)), "without psr_type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := toRow(tt.table, tt.ev)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestTableOf(t *testing.T) {
	for topic, want := range map[string]string{
		"energy.public.day_ahead_price": "day_ahead_price",
		"energy.public.weather":         "weather",
		"plain":                         "plain",
	} {
		if got := tableOf(topic); got != want {
			t.Errorf("tableOf(%q) = %q, want %q", topic, got, want)
		}
	}
}
