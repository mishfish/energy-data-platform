package entsoe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const zoneNL = "10YNL----------L"

func parseFixture(t *testing.T, name string) ([]DayAheadPrice, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	return ParseDayAheadPrices(f)
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// Expected values come from testdata/README.md.
func TestParseDayAheadPricesFixtures(t *testing.T) {
	tests := []struct {
		file          string
		periodStart   string
		createdAt     string
		slots         int
		sumCents      int64
		minCents      int64
		minAt         string
		maxCents      int64
		maxAt         string
		trailingCents int64 // price of the last three slots, all omitted in the document
	}{
		{
			file:        "a44_nl_pt15m.xml",
			periodStart: "2026-10-01T22:00:00Z", createdAt: "2026-10-01T11:02:37Z",
			slots: 96, sumCents: 678094,
			minCents: -467, minAt: "2026-10-02T11:30:00Z",
			maxCents: 11561, maxAt: "2026-10-02T06:30:00Z",
			trailingCents: 6871,
		},
		{
			file:        "a44_nl_pt15m_revised.xml",
			periodStart: "2026-10-01T22:00:00Z", createdAt: "2026-10-02T09:15:04Z",
			slots: 96, sumCents: 678925,
			minCents: -499, minAt: "2026-10-02T10:45:00Z",
			maxCents: 12245, maxAt: "2026-10-02T07:00:00Z",
			trailingCents: 6871,
		},
		{
			file:        "a44_nl_pt15m_dst_end.xml",
			periodStart: "2026-10-24T22:00:00Z", createdAt: "2026-10-24T11:03:12Z",
			slots: 100, sumCents: 709521,
			minCents: -2718, minAt: "2026-10-25T11:30:00Z",
			maxCents: 11774, maxAt: "2026-10-25T05:30:00Z",
			trailingCents: 7500,
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			prices, err := parseFixture(t, tt.file)
			if err != nil {
				t.Fatalf("ParseDayAheadPrices: %v", err)
			}
			if len(prices) != tt.slots {
				t.Fatalf("got %d prices, want %d", len(prices), tt.slots)
			}

			start, createdAt := mustTime(t, tt.periodStart), mustTime(t, tt.createdAt)
			var sum int64
			lowest, highest := prices[0], prices[0]
			for i, p := range prices {
				if want := start.Add(time.Duration(i) * 15 * time.Minute); !p.DeliveryStart.Equal(want) {
					t.Errorf("slot %d: delivery start %s, want %s", i+1, p.DeliveryStart, want)
				}
				if p.DeliveryStart.Location() != time.UTC {
					t.Errorf("slot %d: delivery start is not in UTC", i+1)
				}
				if p.BiddingZone != zoneNL || p.Resolution != 15*time.Minute || !p.SourceCreatedAt.Equal(createdAt) {
					t.Errorf("slot %d: zone %q, resolution %s, created at %s", i+1, p.BiddingZone, p.Resolution, p.SourceCreatedAt)
				}
				sum += p.PriceCents
				if p.PriceCents < lowest.PriceCents {
					lowest = p
				}
				if p.PriceCents > highest.PriceCents {
					highest = p
				}
			}
			if sum != tt.sumCents {
				t.Errorf("sum %d cents, want %d", sum, tt.sumCents)
			}
			if lowest.PriceCents != tt.minCents || !lowest.DeliveryStart.Equal(mustTime(t, tt.minAt)) {
				t.Errorf("min %d at %s, want %d at %s", lowest.PriceCents, lowest.DeliveryStart, tt.minCents, tt.minAt)
			}
			if highest.PriceCents != tt.maxCents || !highest.DeliveryStart.Equal(mustTime(t, tt.maxAt)) {
				t.Errorf("max %d at %s, want %d at %s", highest.PriceCents, highest.DeliveryStart, tt.maxCents, tt.maxAt)
			}
			for i, p := range prices[len(prices)-3:] {
				if p.PriceCents != tt.trailingCents {
					t.Errorf("trailing slot %d: %d cents, want %d", len(prices)-2+i, p.PriceCents, tt.trailingCents)
				}
			}
		})
	}
}

func TestParseDayAheadPricesRevision(t *testing.T) {
	original, err := parseFixture(t, "a44_nl_pt15m.xml")
	if err != nil {
		t.Fatalf("original: %v", err)
	}
	revised, err := parseFixture(t, "a44_nl_pt15m_revised.xml")
	if err != nil {
		t.Fatalf("revised: %v", err)
	}
	if len(original) != len(revised) {
		t.Fatalf("got %d original and %d revised prices", len(original), len(revised))
	}

	type change struct{ from, to int64 }
	want := map[string]change{
		"2026-10-02T07:00:00Z": {10995, 12245},
		"2026-10-02T10:45:00Z": {-80, -499},
	}
	got := map[string]change{}
	for i := range original {
		if original[i].PriceCents != revised[i].PriceCents {
			got[original[i].DeliveryStart.Format(time.RFC3339)] = change{original[i].PriceCents, revised[i].PriceCents}
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d changed slots, want %d: %v", len(got), len(want), got)
	}
	for at, w := range want {
		if got[at] != w {
			t.Errorf("slot %s: change %v, want %v", at, got[at], w)
		}
	}
	if !revised[0].SourceCreatedAt.After(original[0].SourceCreatedAt) {
		t.Errorf("revised document is not newer: %s vs %s", revised[0].SourceCreatedAt, original[0].SourceCreatedAt)
	}
}

func TestParseDayAheadPricesAcknowledgement(t *testing.T) {
	t.Run("no data", func(t *testing.T) {
		prices, err := parseFixture(t, "acknowledgement_no_data.xml")
		if !errors.Is(err, ErrNoData) {
			t.Fatalf("got error %v, want ErrNoData", err)
		}
		if prices != nil {
			t.Errorf("got %d prices, want none", len(prices))
		}
		var ack *AcknowledgementError
		if !errors.As(err, &ack) || ack.Code != "999" || !strings.Contains(ack.Text, "No matching data found") {
			t.Errorf("got acknowledgement %+v", ack)
		}
	})

	t.Run("other reason", func(t *testing.T) {
		doc := `<Acknowledgement_MarketDocument><Reason><code>A59</code><text>Too many requests</text></Reason></Acknowledgement_MarketDocument>`
		_, err := ParseDayAheadPrices(strings.NewReader(doc))
		var ack *AcknowledgementError
		if !errors.As(err, &ack) || ack.Code != "A59" {
			t.Fatalf("got error %v, want acknowledgement A59", err)
		}
		if errors.Is(err, ErrNoData) {
			t.Error("acknowledgement A59 matches ErrNoData")
		}
	})
}

// document builds a minimal A44 document around the given periods.
func document(curveType string, periods ...string) string {
	return `<Publication_MarketDocument>
  <type>A44</type>
  <createdDateTime>2026-10-01T11:02:37Z</createdDateTime>
  <TimeSeries>
    <in_Domain.mRID>` + zoneNL + `</in_Domain.mRID>
    <currency_Unit.name>EUR</currency_Unit.name>
    <price_Measure_Unit.name>MWH</price_Measure_Unit.name>
    <curveType>` + curveType + `</curveType>` + strings.Join(periods, "") + `
  </TimeSeries>
</Publication_MarketDocument>`
}

// hourPeriod builds a PT15M period of one hour (4 positions) starting at start.
func hourPeriod(start string, points ...string) string {
	end := start[:11] + "23:00Z"
	return `<Period><timeInterval><start>` + start + `</start><end>` + end + `</end></timeInterval>
<resolution>PT15M</resolution>` + strings.Join(points, "") + `</Period>`
}

func pt(position, price string) string {
	return `<Point><position>` + position + `</position><price.amount>` + price + `</price.amount></Point>`
}

func TestParseDayAheadPricesCurveTypes(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int64
	}{
		{
			name: "A03 fills gaps and the tail",
			doc:  document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "10.00"), pt("3", "-0.05"))),
			want: []int64{1000, 1000, -5, -5},
		},
		{
			name: "A01 with every position",
			doc:  document("A01", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("2", "2.5"), pt("3", "3.25"), pt("4", "0"))),
			want: []int64{100, 250, 325, 0},
		},
		{
			name: "several periods",
			doc: document("A03",
				hourPeriod("2026-10-01T22:00Z", pt("1", "1.00")),
				hourPeriod("2026-10-02T22:00Z", pt("1", "2.00"), pt("4", "3.00"))),
			want: []int64{100, 100, 100, 100, 200, 200, 200, 300},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices, err := ParseDayAheadPrices(strings.NewReader(tt.doc))
			if err != nil {
				t.Fatalf("ParseDayAheadPrices: %v", err)
			}
			if len(prices) != len(tt.want) {
				t.Fatalf("got %d prices, want %d", len(prices), len(tt.want))
			}
			for i, p := range prices {
				if p.PriceCents != tt.want[i] {
					t.Errorf("slot %d: %d cents, want %d", i+1, p.PriceCents, tt.want[i])
				}
			}
		})
	}
}

func TestParseDayAheadPricesErrors(t *testing.T) {
	valid := document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "10.00")))
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"empty body", "", "read root element"},
		{"not xml", "502 Bad Gateway", "read root element"},
		{"truncated", valid[:len(valid)-40], "decode publication document"},
		{"unknown root", "<html><body>Service unavailable</body></html>", `unexpected root element "html"`},
		{"acknowledgement without reason", "<Acknowledgement_MarketDocument/>", "without a reason"},
		{"wrong document type", strings.Replace(valid, "A44", "A65", 1), `document type "A65"`},
		{"bad createdDateTime", strings.Replace(valid, "2026-10-01T11:02:37Z", "yesterday", 1), "createdDateTime"},
		{"no time series", "<Publication_MarketDocument><type>A44</type><createdDateTime>2026-10-01T11:02:37Z</createdDateTime></Publication_MarketDocument>", "without time series"},
		{"missing zone", strings.Replace(valid, zoneNL, "", 1), "missing in_Domain.mRID"},
		{"non-EUR currency", strings.Replace(valid, "EUR", "PLN", 1), `currency "PLN"`},
		{"wrong price unit", strings.Replace(valid, "MWH", "KWH", 1), `price measure unit "KWH"`},
		{"unknown curve type", document("A02", hourPeriod("2026-10-01T22:00Z", pt("1", "1"))), `unsupported curve type "A02"`},
		{"no periods", document("A03"), "no periods"},
		{"unsupported resolution", strings.Replace(valid, "PT15M", "P1D", 1), `unsupported resolution "P1D"`},
		{"bad interval start", strings.Replace(valid, "2026-10-01T22:00Z", "2026-10-01", 1), "interval start"},
		{"interval not a multiple of the resolution", strings.Replace(valid, "23:00Z", "23:10Z", 1), "not a positive multiple"},
		{"empty interval", strings.Replace(valid, "23:00Z", "22:00Z", 1), "not a positive multiple"},
		{"no points", document("A03", hourPeriod("2026-10-01T22:00Z")), "missing position 1"},
		{"A03 without position 1", document("A03", hourPeriod("2026-10-01T22:00Z", pt("2", "1"))), "missing position 1"},
		{"A01 with a gap", document("A01", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("3", "1"))), "missing position 2"},
		{"position beyond the interval", document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("5", "1"))), "point position 5"},
		{"positions out of order", document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("3", "1"), pt("2", "1"))), "point position 2"},
		{"repeated position", document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("1", "2"))), "point position 1"},
		{"bad price", document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "n/a"))), `position 1: invalid price amount "n/a"`},
		{
			"overlapping periods",
			document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "1")), hourPeriod("2026-10-01T22:00Z", pt("1", "2"))),
			"duplicate price",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices, err := ParseDayAheadPrices(strings.NewReader(tt.doc))
			if err == nil {
				t.Fatalf("got %d prices and no error, want error containing %q", len(prices), tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %q, want it to contain %q", err, tt.wantErr)
			}
			if errors.Is(err, ErrNoData) {
				t.Error("error matches ErrNoData")
			}
		})
	}
}

func TestParseCents(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "74.78", want: 7478},
		{in: "-4.67", want: -467},
		{in: "-0.80", want: -80},
		{in: "0.05", want: 5},
		{in: "75", want: 7500},
		{in: "75.5", want: 7550},
		{in: "0", want: 0},
		{in: "-0.00", want: 0},
		{in: "4000.00", want: 400000},
		{in: "99999999.99", want: 9999999999},
		{in: "100000000.00", wantErr: true}, // does not fit numeric(10, 2)
		{in: "1.234", wantErr: true},
		{in: "", wantErr: true},
		{in: "-", wantErr: true},
		{in: "75.", wantErr: true},
		{in: ".5", wantErr: true},
		{in: "+5.00", wantErr: true},
		{in: "--5.00", wantErr: true},
		{in: "1e3", wantErr: true},
		{in: "1,50", wantErr: true},
		{in: " 1.50", wantErr: true},
		{in: "NaN", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseCents(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCents(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseCents(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
