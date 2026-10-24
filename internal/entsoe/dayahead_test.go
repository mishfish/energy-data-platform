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
		file           string
		periodStart    string
		createdAt      string
		slots          int
		sumMicros      int64
		minMicros      int64
		minAt          string
		maxMicros      int64
		maxAt          string
		trailingMicros int64 // price of the last three slots, all omitted in the document
	}{
		{
			file:        "a44_nl_pt15m.xml",
			periodStart: "2026-10-01T22:00:00Z", createdAt: "2026-10-01T11:02:37Z",
			slots: 96, sumMicros: 6780940000,
			minMicros: -4670000, minAt: "2026-10-02T11:30:00Z",
			maxMicros: 115610000, maxAt: "2026-10-02T06:30:00Z",
			trailingMicros: 68710000,
		},
		{
			file:        "a44_nl_pt15m_revised.xml",
			periodStart: "2026-10-01T22:00:00Z", createdAt: "2026-10-02T09:15:04Z",
			slots: 96, sumMicros: 6789250000,
			minMicros: -4990000, minAt: "2026-10-02T10:45:00Z",
			maxMicros: 122450000, maxAt: "2026-10-02T07:00:00Z",
			trailingMicros: 68710000,
		},
		{
			file:        "a44_nl_pt15m_dst_end.xml",
			periodStart: "2026-10-24T22:00:00Z", createdAt: "2026-10-24T11:03:12Z",
			slots: 100, sumMicros: 7095210000,
			minMicros: -27180000, minAt: "2026-10-25T11:30:00Z",
			maxMicros: 117740000, maxAt: "2026-10-25T05:30:00Z",
			trailingMicros: 75000000,
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
				sum += p.PriceMicros
				if p.PriceMicros < lowest.PriceMicros {
					lowest = p
				}
				if p.PriceMicros > highest.PriceMicros {
					highest = p
				}
			}
			if sum != tt.sumMicros {
				t.Errorf("sum %d micros, want %d", sum, tt.sumMicros)
			}
			if lowest.PriceMicros != tt.minMicros || !lowest.DeliveryStart.Equal(mustTime(t, tt.minAt)) {
				t.Errorf("min %d at %s, want %d at %s", lowest.PriceMicros, lowest.DeliveryStart, tt.minMicros, tt.minAt)
			}
			if highest.PriceMicros != tt.maxMicros || !highest.DeliveryStart.Equal(mustTime(t, tt.maxAt)) {
				t.Errorf("max %d at %s, want %d at %s", highest.PriceMicros, highest.DeliveryStart, tt.maxMicros, tt.maxAt)
			}
			for i, p := range prices[len(prices)-3:] {
				if p.PriceMicros != tt.trailingMicros {
					t.Errorf("trailing slot %d: %d micros, want %d", len(prices)-2+i, p.PriceMicros, tt.trailingMicros)
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
		"2026-10-02T07:00:00Z": {109950000, 122450000},
		"2026-10-02T10:45:00Z": {-800000, -4990000},
	}
	got := map[string]change{}
	for i := range original {
		if original[i].PriceMicros != revised[i].PriceMicros {
			got[original[i].DeliveryStart.Format(time.RFC3339)] = change{original[i].PriceMicros, revised[i].PriceMicros}
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
			want: []int64{10000000, 10000000, -50000, -50000},
		},
		{
			name: "A01 with every position",
			doc:  document("A01", hourPeriod("2026-10-01T22:00Z", pt("1", "1"), pt("2", "2.5"), pt("3", "3.25"), pt("4", "0"))),
			want: []int64{1000000, 2500000, 3250000, 0},
		},
		{
			name: "several periods",
			doc: document("A03",
				hourPeriod("2026-10-01T22:00Z", pt("1", "1.00")),
				hourPeriod("2026-10-02T22:00Z", pt("1", "2.00"), pt("4", "3.00"))),
			want: []int64{1000000, 1000000, 1000000, 1000000, 2000000, 2000000, 2000000, 3000000},
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
				if p.PriceMicros != tt.want[i] {
					t.Errorf("slot %d: %d micros, want %d", i+1, p.PriceMicros, tt.want[i])
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

func TestParsePriceMicros(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "74.78", want: 74_780000},
		{in: "-4.67", want: -4_670000},
		{in: "-0.80", want: -800000},
		{in: "0.05", want: 50000},
		{in: "75", want: 75_000000},
		{in: "173.745", want: 173_745000},    // IE_SEM publishes three decimals
		{in: "173.869995", want: 173_869995}, // DK_1 publishes float32 artefacts, stored as sent
		{in: "0", want: 0},
		{in: "-0.00", want: 0},
		{in: "9999999999.999999", want: 9999999999_999999},
		{in: "10000000000", wantErr: true}, // does not fit numeric(16, 6)
		{in: "1.2345678", wantErr: true},
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
			got, err := parsePriceMicros(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePriceMicros(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parsePriceMicros(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseDayAheadPricesSequences(t *testing.T) {
	series := func(position, price string) string {
		seq := ""
		if position != "" {
			seq = "<classificationSequence_AttributeInstanceComponent.position>" + position + "</classificationSequence_AttributeInstanceComponent.position>"
		}
		return `<TimeSeries>
    <in_Domain.mRID>` + zoneNL + `</in_Domain.mRID>
    <currency_Unit.name>EUR</currency_Unit.name>
    <price_Measure_Unit.name>MWH</price_Measure_Unit.name>` + seq + `
    <curveType>A03</curveType>` + hourPeriod("2026-10-01T22:00Z", pt("1", price)) + `
  </TimeSeries>`
	}
	doc := func(series ...string) string {
		return `<Publication_MarketDocument><type>A44</type><createdDateTime>2026-10-01T11:02:37Z</createdDateTime>` +
			strings.Join(series, "") + `</Publication_MarketDocument>`
	}

	t.Run("several auctions for one day are kept apart", func(t *testing.T) {
		prices, err := ParseDayAheadPrices(strings.NewReader(doc(series("2", "186.44"), series("1", "174.31"))))
		if err != nil {
			t.Fatalf("ParseDayAheadPrices: %v", err)
		}
		got := map[int]int64{}
		for _, p := range prices {
			got[p.Sequence] = p.PriceMicros
		}
		if len(prices) != 8 || got[1] != 174_310000 || got[2] != 186_440000 {
			t.Errorf("got %d prices, by sequence %v", len(prices), got)
		}
	})

	t.Run("a single sequence without a position is 0", func(t *testing.T) {
		prices, err := ParseDayAheadPrices(strings.NewReader(doc(series("", "10"))))
		if err != nil {
			t.Fatalf("ParseDayAheadPrices: %v", err)
		}
		if prices[0].Sequence != 0 {
			t.Errorf("sequence %d, want 0", prices[0].Sequence)
		}
	})

	errs := []struct {
		name, doc, wantErr string
	}{
		{"same sequence twice", doc(series("1", "1"), series("1", "2")), "duplicate price"},
		{"bad position", doc(series("first", "1")), `invalid classificationSequence position "first"`},
		{"zero position", doc(series("0", "1")), `invalid classificationSequence position "0"`},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDayAheadPrices(strings.NewReader(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
