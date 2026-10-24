package entsoe

import (
	"errors"
	"strings"
	"testing"
)

// genSeries builds an A75 time series; side is "in" (generation) or "out" (consumption).
func genSeries(side, psrType, curveType string, periods ...string) string {
	domain := "inBiddingZone_Domain.mRID"
	if side == "out" {
		domain = "outBiddingZone_Domain.mRID"
	}
	return `<TimeSeries>
    <` + domain + `>` + zoneNL + `</` + domain + `>
    <quantity_Measure_Unit.name>MAW</quantity_Measure_Unit.name>
    <MktPSRType><psrType>` + psrType + `</psrType></MktPSRType>
    <curveType>` + curveType + `</curveType>` + strings.Join(periods, "") + `
  </TimeSeries>`
}

func genDocument(series ...string) string {
	return `<GL_MarketDocument>
  <type>A75</type>
  <process.processType>A16</process.processType>
  <createdDateTime>2026-10-24T10:02:11Z</createdDateTime>` + strings.Join(series, "") + `
</GL_MarketDocument>`
}

func TestParseGeneration(t *testing.T) {
	doc := genDocument(
		genSeries("in", "B16", "A03", hourPeriod("2026-10-23T22:00Z", qty("1", "0"), qty("3", "12.5"))),
		genSeries("in", "B10", "A01", hourPeriod("2026-10-23T22:00Z", qty("1", "300"), qty("2", "250.123456"), qty("3", "0"), qty("4", "0"))),
		genSeries("out", "B10", "A03", hourPeriod("2026-10-23T22:00Z", qty("1", "0"), qty("3", "410.5"))),
	)
	got, err := ParseGeneration(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ParseGeneration: %v", err)
	}
	if len(got) != 12 {
		t.Fatalf("got %d values, want 12", len(got))
	}

	type key struct{ psr, direction string }
	byKey := map[key][]int64{}
	for _, g := range got {
		if g.BiddingZone != zoneNL || g.SourceCreatedAt.IsZero() {
			t.Errorf("value %+v without zone or created at", g)
		}
		byKey[key{g.PSRType, g.Direction}] = append(byKey[key{g.PSRType, g.Direction}], g.QuantityW)
	}
	want := map[key][]int64{
		{"B16", DirectionGeneration}:  {0, 0, 12_500000, 12_500000},
		{"B10", DirectionGeneration}:  {300_000000, 250_123456, 0, 0},
		{"B10", DirectionConsumption}: {0, 0, 410_500000, 410_500000},
	}
	for k, w := range want {
		g := byKey[k]
		if len(g) != len(w) {
			t.Errorf("%v: got %v, want %v", k, g, w)
			continue
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("%v slot %d: %d W, want %d", k, i+1, g[i], w[i])
			}
		}
	}
}

func TestParseGenerationErrors(t *testing.T) {
	valid := genSeries("in", "B19", "A01", hourPeriod("2026-10-23T22:00Z", qty("1", "1"), qty("2", "2"), qty("3", "3"), qty("4", "4")))
	both := strings.Replace(valid, "<quantity_Measure_Unit.name>", "<outBiddingZone_Domain.mRID>"+zoneNL+"</outBiddingZone_Domain.mRID><quantity_Measure_Unit.name>", 1)
	tests := []struct {
		name, doc, wantErr string
	}{
		{"load document", strings.Replace(genDocument(valid), "<type>A75", "<type>A65", 1), `document type "A65"`},
		{"forecast", strings.Replace(genDocument(valid), "<process.processType>A16", "<process.processType>A01", 1), `process type "A01"`},
		{"unknown production type", genDocument(strings.Replace(valid, "B19", "B99", 1)), `unknown production type "B99"`},
		{"no domain", genDocument(strings.Replace(valid, "<inBiddingZone_Domain.mRID>"+zoneNL+"</inBiddingZone_Domain.mRID>", "", 1)), "missing inBiddingZone_Domain"},
		{"both domains", genDocument(both), "both inBiddingZone_Domain and outBiddingZone_Domain"},
		{"wrong unit", genDocument(strings.Replace(valid, "MAW", "KWT", 1)), `quantity measure unit "KWT"`},
		{"negative quantity", genDocument(strings.Replace(valid, "<quantity>2<", "<quantity>-2<", 1)), `invalid quantity "-2"`},
		{"same series twice", genDocument(valid, valid), "duplicate B19 generation"},
		{"no time series", genDocument(), "without time series"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGeneration(strings.NewReader(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got %d values and error %v, want it to contain %q", len(got), err, tt.wantErr)
			}
		})
	}
}

func TestParseGenerationAcknowledgement(t *testing.T) {
	_, err := ParseGeneration(strings.NewReader(`<Acknowledgement_MarketDocument><Reason><code>999</code><text>No matching data found</text></Reason></Acknowledgement_MarketDocument>`))
	if !errors.Is(err, ErrNoData) {
		t.Errorf("got %v, want ErrNoData", err)
	}
}
