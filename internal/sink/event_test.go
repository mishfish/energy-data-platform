package sink

import (
	"strings"
	"testing"
	"time"
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

func TestToRow(t *testing.T) {
	tests := []struct {
		name        string
		table       string
		ev          envelope
		wantAmount  string
		wantDeleted uint8
		wantLSN     uint64
	}{
		{"snapshot read", "day_ahead_price", event("r", nil, image("109.95", ""), lsn(100)), "109.95", 0, 100},
		{"insert", "actual_load", event("c", nil, image("", "13663.500"), lsn(120)), "13663.5", 0, 120},
		{"update writes the after image", "day_ahead_price", event("u", image("109.95", ""), image("122.45", ""), lsn(250)), "122.45", 0, 250},
		{"delete writes the before image", "day_ahead_price", event("d", image("-0.80", ""), nil, lsn(300)), "-0.8", 1, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, err := toRow(tt.table, tt.ev)
			if err != nil {
				t.Fatalf("toRow: %v", err)
			}
			if row.Table != tt.table || row.Amount.String() != tt.wantAmount || row.IsDeleted != tt.wantDeleted || row.LSN != tt.wantLSN {
				t.Errorf("got table %s, amount %s, deleted %d, lsn %d", row.Table, row.Amount, row.IsDeleted, row.LSN)
			}
			if want := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC); !row.DeliveryStart.Equal(want) || row.DeliveryStart.Location() != time.UTC {
				t.Errorf("delivery start %s, want %s in UTC", row.DeliveryStart, want)
			}
			if row.ResolutionUS != 900_000_000 || row.BiddingZone != "10YNL----------L" {
				t.Errorf("resolution %d, zone %q", row.ResolutionUS, row.BiddingZone)
			}
		})
	}
}

func TestToRowErrors(t *testing.T) {
	bad := func(mutate func(*rowImage)) *rowImage {
		img := image("1.00", "1.000")
		mutate(img)
		return img
	}
	tests := []struct {
		name    string
		table   string
		ev      envelope
		wantErr string
	}{
		{"unknown table", "generation", event("c", nil, image("1", ""), lsn(1)), "no ClickHouse table"},
		{"missing LSN", "day_ahead_price", event("c", nil, image("1", ""), nil), "without source LSN"},
		{"truncate", "day_ahead_price", event("t", nil, nil, lsn(1)), `unsupported operation "t"`},
		{"update without after", "day_ahead_price", event("u", image("1", ""), nil, lsn(1)), "without a row image"},
		{"delete without before", "day_ahead_price", event("d", nil, nil, lsn(1)), "without a row image"},
		{"bad delivery start", "day_ahead_price", event("c", nil, bad(func(r *rowImage) { r.DeliveryStart = "yesterday" }), lsn(1)), "delivery_start"},
		{"bad created at", "day_ahead_price", event("c", nil, bad(func(r *rowImage) { r.SourceCreatedAt = "" }), lsn(1)), "source_created_at"},
		{"bad amount", "actual_load", event("c", nil, bad(func(r *rowImage) { r.LoadMW = "n/a" }), lsn(1)), "amount"},
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
		"energy.public.actual_load":     "actual_load",
		"plain":                         "plain",
	} {
		if got := tableOf(topic); got != want {
			t.Errorf("tableOf(%q) = %q, want %q", topic, got, want)
		}
	}
}
