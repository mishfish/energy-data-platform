package entsoe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseLoadFixture(t *testing.T, name string) ([]ActualLoad, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	return ParseActualLoad(f)
}

// Expected values come from testdata/README.md.
func TestParseActualLoadFixtures(t *testing.T) {
	tests := []struct {
		file      string
		createdAt string
		slots     int
		sumKW     int64
		minKW     int64
		minAt     string
		maxKW     int64
		maxAt     string
		first     string
		last      string
	}{
		{
			file: "a65_nl_pt15m.xml", createdAt: "2026-10-03T06:14:52Z",
			slots: 96, sumKW: 1258023747,
			minKW: 10659000, minAt: "2026-10-02T17:30:00Z",
			maxKW: 15581560, maxAt: "2026-10-02T10:30:00Z",
			first: "2026-10-01T22:00:00Z", last: "2026-10-02T21:45:00Z",
		},
		{
			file: "a65_nl_pt15m_revised.xml", createdAt: "2026-10-09T08:12:44Z",
			slots: 96, sumKW: 1258301413,
			minKW: 10659000, minAt: "2026-10-02T17:30:00Z",
			maxKW: 15581560, maxAt: "2026-10-02T10:30:00Z",
			first: "2026-10-01T22:00:00Z", last: "2026-10-02T21:45:00Z",
		},
		{
			file: "a65_nl_pt15m_partial.xml", createdAt: "2026-10-02T12:58:03Z",
			slots: 56, sumKW: 764992059,
			minKW: 11876000, minAt: "2026-10-02T04:15:00Z",
			maxKW: 15581560, maxAt: "2026-10-02T10:30:00Z",
			first: "2026-10-01T22:00:00Z", last: "2026-10-02T11:45:00Z",
		},
		{
			file: "a65_nl_pt15m_gap.xml", createdAt: "2026-10-03T06:15:30Z",
			slots: 92, sumKW: 1209537187,
			minKW: 10659000, minAt: "2026-10-02T17:30:00Z",
			maxKW: 15581560, maxAt: "2026-10-02T10:30:00Z",
			first: "2026-10-01T22:00:00Z", last: "2026-10-02T21:45:00Z",
		},
		{
			file: "a65_nl_pt15m_dst_end.xml", createdAt: "2026-10-26T06:20:11Z",
			slots: 100, sumKW: 1312494489,
			minKW: 10883390, minAt: "2026-10-25T16:30:00Z",
			maxKW: 15471964, maxAt: "2026-10-25T10:45:00Z",
			first: "2026-10-24T22:00:00Z", last: "2026-10-25T22:45:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			loads, err := parseLoadFixture(t, tt.file)
			if err != nil {
				t.Fatalf("ParseActualLoad: %v", err)
			}
			if len(loads) != tt.slots {
				t.Fatalf("got %d slots, want %d", len(loads), tt.slots)
			}

			createdAt := mustTime(t, tt.createdAt)
			var sum int64
			lowest, highest := loads[0], loads[0]
			for i, l := range loads {
				if i > 0 && !l.DeliveryStart.After(loads[i-1].DeliveryStart) {
					t.Errorf("slot %d: delivery start %s is not after %s", i+1, l.DeliveryStart, loads[i-1].DeliveryStart)
				}
				if l.DeliveryStart.Location() != time.UTC {
					t.Errorf("slot %d: delivery start is not in UTC", i+1)
				}
				if l.BiddingZone != zoneNL || l.Resolution != 15*time.Minute || !l.SourceCreatedAt.Equal(createdAt) {
					t.Errorf("slot %d: zone %q, resolution %s, created at %s", i+1, l.BiddingZone, l.Resolution, l.SourceCreatedAt)
				}
				sum += l.QuantityKW
				if l.QuantityKW < lowest.QuantityKW {
					lowest = l
				}
				if l.QuantityKW > highest.QuantityKW {
					highest = l
				}
			}
			if sum != tt.sumKW {
				t.Errorf("sum %d kW, want %d", sum, tt.sumKW)
			}
			if lowest.QuantityKW != tt.minKW || !lowest.DeliveryStart.Equal(mustTime(t, tt.minAt)) {
				t.Errorf("min %d at %s, want %d at %s", lowest.QuantityKW, lowest.DeliveryStart, tt.minKW, tt.minAt)
			}
			if highest.QuantityKW != tt.maxKW || !highest.DeliveryStart.Equal(mustTime(t, tt.maxAt)) {
				t.Errorf("max %d at %s, want %d at %s", highest.QuantityKW, highest.DeliveryStart, tt.maxKW, tt.maxAt)
			}
			if got := loads[0].DeliveryStart; !got.Equal(mustTime(t, tt.first)) {
				t.Errorf("first slot %s, want %s", got, tt.first)
			}
			if got := loads[len(loads)-1].DeliveryStart; !got.Equal(mustTime(t, tt.last)) {
				t.Errorf("last slot %s, want %s", got, tt.last)
			}
		})
	}
}

// The source gap must stay a gap: no slot is synthesized between the two time series.
func TestParseActualLoadGap(t *testing.T) {
	loads, err := parseLoadFixture(t, "a65_nl_pt15m_gap.xml")
	if err != nil {
		t.Fatalf("ParseActualLoad: %v", err)
	}
	gapStart, gapEnd := mustTime(t, "2026-10-02T04:00:00Z"), mustTime(t, "2026-10-02T05:00:00Z")
	for _, l := range loads {
		if !l.DeliveryStart.Before(gapStart) && l.DeliveryStart.Before(gapEnd) {
			t.Errorf("unexpected slot at %s inside the source gap", l.DeliveryStart)
		}
	}
}

func TestParseActualLoadRevision(t *testing.T) {
	original, err := parseLoadFixture(t, "a65_nl_pt15m.xml")
	if err != nil {
		t.Fatalf("original: %v", err)
	}
	revised, err := parseLoadFixture(t, "a65_nl_pt15m_revised.xml")
	if err != nil {
		t.Fatalf("revised: %v", err)
	}
	if len(original) != len(revised) {
		t.Fatalf("got %d original and %d revised slots", len(original), len(revised))
	}

	type change struct{ from, to int64 }
	want := map[string]change{
		"2026-10-02T08:00:00Z": {14046500, 13987250},
		"2026-10-02T15:15:00Z": {11667084, 12004000},
	}
	got := map[string]change{}
	for i := range original {
		if original[i].QuantityKW != revised[i].QuantityKW {
			got[original[i].DeliveryStart.Format(time.RFC3339)] = change{original[i].QuantityKW, revised[i].QuantityKW}
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
}

func TestParseActualLoadAcknowledgement(t *testing.T) {
	loads, err := parseLoadFixture(t, "acknowledgement_no_data.xml")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("got error %v, want ErrNoData", err)
	}
	if loads != nil {
		t.Errorf("got %d slots, want none", len(loads))
	}
}

// loadDocument builds a minimal A65 document around the given periods.
func loadDocument(processType, curveType string, periods ...string) string {
	return `<GL_MarketDocument>
  <type>A65</type>
  <process.processType>` + processType + `</process.processType>
  <createdDateTime>2026-10-03T06:14:52Z</createdDateTime>
  <TimeSeries>
    <outBiddingZone_Domain.mRID>` + zoneNL + `</outBiddingZone_Domain.mRID>
    <quantity_Measure_Unit.name>MAW</quantity_Measure_Unit.name>
    <curveType>` + curveType + `</curveType>` + strings.Join(periods, "") + `
  </TimeSeries>
</GL_MarketDocument>`
}

func qty(position, quantity string) string {
	return `<Point><position>` + position + `</position><quantity>` + quantity + `</quantity></Point>`
}

func TestParseActualLoadCurveTypes(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int64
	}{
		{
			name: "A01 with every position",
			doc:  loadDocument("A16", "A01", hourPeriod("2026-10-01T22:00Z", qty("1", "13663.5"), qty("2", "13837"), qty("3", "13903.34"), qty("4", "13557.505"))),
			want: []int64{13663500, 13837000, 13903340, 13557505},
		},
		{
			name: "A03 fills gaps and the tail",
			doc:  loadDocument("A16", "A03", hourPeriod("2026-10-01T22:00Z", qty("1", "12000"), qty("3", "12500.25"))),
			want: []int64{12000000, 12000000, 12500250, 12500250},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loads, err := ParseActualLoad(strings.NewReader(tt.doc))
			if err != nil {
				t.Fatalf("ParseActualLoad: %v", err)
			}
			if len(loads) != len(tt.want) {
				t.Fatalf("got %d slots, want %d", len(loads), len(tt.want))
			}
			for i, l := range loads {
				if l.QuantityKW != tt.want[i] {
					t.Errorf("slot %d: %d kW, want %d", i+1, l.QuantityKW, tt.want[i])
				}
			}
		})
	}
}

// The wanted substrings follow the A44 parser's messages; adjust them to the implementation.
func TestParseActualLoadErrors(t *testing.T) {
	valid := loadDocument("A16", "A01", hourPeriod("2026-10-01T22:00Z", qty("1", "1"), qty("2", "2"), qty("3", "3"), qty("4", "4")))
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"empty body", "", "read root element"},
		{"price document", document("A03", hourPeriod("2026-10-01T22:00Z", pt("1", "10.00"))), `unexpected root element "Publication_MarketDocument"`},
		{"wrong document type", strings.Replace(valid, "<type>A65", "<type>A75", 1), `document type "A75"`},
		{"forecast process type", loadDocument("A01", "A01", hourPeriod("2026-10-01T22:00Z", qty("1", "1"), qty("2", "2"), qty("3", "3"), qty("4", "4"))), `process type "A01"`},
		{"missing zone", strings.Replace(valid, zoneNL, "", 1), "missing outBiddingZone_Domain.mRID"},
		{"wrong quantity unit", strings.Replace(valid, "MAW", "KWT", 1), `quantity measure unit "KWT"`},
		{"A01 with a gap", loadDocument("A16", "A01", hourPeriod("2026-10-01T22:00Z", qty("1", "1"), qty("3", "1"))), "missing position 2"},
		{"bad quantity", strings.Replace(valid, "<quantity>1<", "<quantity>n/a<", 1), `invalid quantity "n/a"`},
		{
			"overlapping periods",
			loadDocument("A16", "A03", hourPeriod("2026-10-01T22:00Z", qty("1", "1")), hourPeriod("2026-10-01T22:00Z", qty("1", "2"))),
			"duplicate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loads, err := ParseActualLoad(strings.NewReader(tt.doc))
			if err == nil {
				t.Fatalf("got %d slots and no error, want error containing %q", len(loads), tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseKW(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "13663.5", want: 13663500},
		{in: "13837", want: 13837000},
		{in: "13903.34", want: 13903340},
		{in: "13557.505", want: 13557505},
		{in: "0.001", want: 1},
		{in: "0", want: 0},
		{in: "999999999.999", want: 999999999999},
		{in: "1000000000", wantErr: true}, // does not fit numeric(12, 3)
		{in: "1.2345", wantErr: true},
		{in: "", wantErr: true},
		{in: "75.", wantErr: true},
		{in: ".5", wantErr: true},
		{in: "+5", wantErr: true},
		{in: "1e3", wantErr: true},
		{in: "1,5", wantErr: true},
		{in: " 1.5", wantErr: true},
		{in: "NaN", wantErr: true},
		{in: "-5", wantErr: true}, // total load cannot be negative
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseKW(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseKW(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseKW(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
