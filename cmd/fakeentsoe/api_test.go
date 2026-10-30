package main

import (
	"errors"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const zoneNL = "10YNL----------L"

// fakeAt starts the fake API with its clock at *now and returns a real client for it.
func fakeAt(t *testing.T, m model, now *time.Time) (*entsoe.Client, string) {
	t.Helper()
	s := &server{model: m, now: func() time.Time { return *now }, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)
	client, err := entsoe.NewClient(srv.URL+"/api", "fake-token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client, srv.URL
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

const (
	zone60 = "10YTEST-HOURLY-1" // hourly prices with several sequences, hourly load with gaps
	zoneGB = "10YGB----------A" // no profile: the real platform publishes nothing for GB
)

// flat is a constant daily shape around v with a small spread.
func flat(v float64) curve {
	var c curve
	for h := range c.Hourly {
		c.Hourly[h] = v
	}
	c.SlotSD, c.DaySD, c.Min, c.Max = v/50, v/20, v/2, 2*v
	return c
}

var testProfiles = &profileSet{Zones: map[string]zoneProfile{
	zoneNL: {
		Price: &priceProfile{ResolutionMin: 15, curve: flat(80), Sequences: []priceSequence{{Decimals: 2, LastHour: 24}}},
		Load:  &loadProfile{ResolutionMin: 15, Decimals: 3, LagMin: 60, Weekday: flat(11000), Weekend: flat(9000)},
		Generation: &genProfile{ResolutionMin: 15, Decimals: 3, LagMin: 60, Series: []genSeriesProfile{
			{PSRType: "B04", Direction: "generation", curve: flat(3000)},
			{PSRType: "B10", Direction: "generation", curve: flat(300)},
			{PSRType: "B10", Direction: "consumption", curve: flat(200)},
			{PSRType: "B19", Direction: "generation", curve: flat(1500)},
		}},
	},
	zone60: {
		Price: &priceProfile{ResolutionMin: 60, curve: flat(150), Sequences: []priceSequence{
			{Position: 0, Decimals: 2, LastHour: 24},
			{Position: 2, Decimals: 6, LastHour: 24},
			{Position: 3, Decimals: 2, FirstHour: 12, LastHour: 24, Offset: 5, OffsetSD: 3},
		}},
		Load: &loadProfile{ResolutionMin: 60, Decimals: 4, LagMin: 2, MissingRate: 0.25, Weekday: flat(2500), Weekend: flat(2300)},
	},
}}

var noRevisions = model{profiles: testProfiles}

// call sends one request and returns its status and headers; the body is drained and closed.
func call(t *testing.T, method, url, body string) (int, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header
}

// The ingest window is anchored at UTC midnight; delivery days are local, so the first and last
// overlapping days are returned whole. 2026-10-25 is the end of CEST and has 100 slots.
func TestPricesFollowPublication(t *testing.T) {
	start, end := mustTime(t, "2026-10-23T00:00:00Z"), mustTime(t, "2026-10-26T00:00:00Z")
	tests := []struct {
		name      string
		now       string
		wantSlots int
		wantFirst string
		wantLast  string
	}{
		// Days 23 and 24 are published, 25 is published at 2026-10-24T11:00Z.
		{"before the auction", "2026-10-24T10:59:00Z", 192, "2026-10-22T22:00:00Z", "2026-10-24T21:45:00Z"},
		{"after the auction", "2026-10-24T11:00:00Z", 292, "2026-10-22T22:00:00Z", "2026-10-25T22:45:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := mustTime(t, tt.now)
			client, _ := fakeAt(t, noRevisions, &now)
			prices, err := client.DayAheadPrices(t.Context(), zoneNL, start, end)
			if err != nil {
				t.Fatalf("DayAheadPrices: %v", err)
			}
			if len(prices) != tt.wantSlots {
				t.Fatalf("got %d prices, want %d", len(prices), tt.wantSlots)
			}
			if got := prices[0].DeliveryStart; !got.Equal(mustTime(t, tt.wantFirst)) {
				t.Errorf("first slot %s, want %s", got, tt.wantFirst)
			}
			if got := prices[len(prices)-1].DeliveryStart; !got.Equal(mustTime(t, tt.wantLast)) {
				t.Errorf("last slot %s, want %s", got, tt.wantLast)
			}
			if !prices[0].SourceCreatedAt.Equal(now) {
				t.Errorf("createdDateTime %s, want the request time %s", prices[0].SourceCreatedAt, now)
			}
		})
	}
}

func TestLoadFollowsPublicationLag(t *testing.T) {
	// Slot 12:45 ends at 13:00 and is published at 14:00.
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	loads, err := client.ActualLoad(t.Context(), zoneNL, mustTime(t, "2026-10-23T00:00:00Z"), mustTime(t, "2026-10-25T00:00:00Z"))
	if err != nil {
		t.Fatalf("ActualLoad: %v", err)
	}
	if len(loads) != 96+52 {
		t.Fatalf("got %d slots, want %d", len(loads), 96+52)
	}
	if got := loads[len(loads)-1].DeliveryStart; !got.Equal(mustTime(t, "2026-10-24T12:45:00Z")) {
		t.Errorf("last slot %s, want 2026-10-24T12:45:00Z", got)
	}
	for _, l := range loads {
		if l.QuantityW < 5_000_000_000 || l.QuantityW > 25_000_000_000 {
			t.Errorf("slot %s: implausible load %d W", l.DeliveryStart, l.QuantityW)
		}
	}
}

func TestGenerationParsesWithTheRealClient(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	gen, err := client.Generation(t.Context(), zoneNL, mustTime(t, "2026-10-23T00:00:00Z"), mustTime(t, "2026-10-25T00:00:00Z"))
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	slots := map[string]int{}
	for _, g := range gen {
		slots[g.PSRType+"/"+g.Direction]++
		if g.QuantityW < 0 {
			t.Errorf("negative quantity %+v", g)
		}
	}
	for _, key := range []string{"B04/generation", "B10/generation", "B10/consumption", "B19/generation"} {
		if slots[key] != 96+52 {
			t.Errorf("%s: %d slots, want %d (same publication lag as load)", key, slots[key], 96+52)
		}
	}
}

// 2026-10-23 and 24 are whole hourly days: sequence 0 and 2 cover them, sequence 3 only 12:00-24:00.
func TestPriceSequencesFollowTheProfile(t *testing.T) {
	now := mustTime(t, "2026-10-24T10:00:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	prices, err := client.DayAheadPrices(t.Context(), zone60, mustTime(t, "2026-10-23T00:00:00Z"), mustTime(t, "2026-10-24T00:00:00Z"))
	if err != nil {
		t.Fatalf("DayAheadPrices: %v", err)
	}
	slots := map[int]int{}
	var artefacts int
	for _, p := range prices {
		slots[p.Sequence]++
		if p.Resolution != time.Hour {
			t.Fatalf("resolution %s, want 1h", p.Resolution)
		}
		if p.Sequence == 3 {
			if h := p.DeliveryStart.In(amsterdam).Hour(); h < 12 {
				t.Errorf("sequence 3 has a price at %d:00, outside its hours", h)
			}
		}
		if p.Sequence == 2 && p.PriceMicros%10_000 != 0 {
			artefacts++
		}
	}
	if want := map[int]int{0: 48, 2: 48, 3: 24}; !maps.Equal(slots, want) {
		t.Errorf("slots per sequence %v, want %v", slots, want)
	}
	if artefacts < 24 {
		t.Errorf("only %d of 48 sequence 2 prices carry float32 artefacts", artefacts)
	}
}

func TestLoadGapsStayGaps(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	start := mustTime(t, "2026-10-17T00:00:00Z")
	loads, err := client.ActualLoad(t.Context(), zone60, start, mustTime(t, "2026-10-24T00:00:00Z"))
	if err != nil {
		t.Fatalf("ActualLoad: %v", err)
	}
	// 7 hourly days with a quarter of the hours missing.
	if len(loads) < 100 || len(loads) > 150 {
		t.Fatalf("got %d of 168 hourly slots, want about three quarters", len(loads))
	}
	seen := map[time.Time]bool{}
	for _, l := range loads {
		if l.Resolution != time.Hour || seen[l.DeliveryStart] || l.DeliveryStart.Before(start) {
			t.Fatalf("unexpected slot %+v", l)
		}
		seen[l.DeliveryStart] = true
	}
}

func TestZoneWithoutProfileHasNoData(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	start, end := mustTime(t, "2026-10-22T00:00:00Z"), mustTime(t, "2026-10-24T00:00:00Z")

	if _, err := client.DayAheadPrices(t.Context(), zoneGB, start, end); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("prices: got %v, want ErrNoData", err)
	}
	if _, err := client.ActualLoad(t.Context(), zoneGB, start, end); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("load: got %v, want ErrNoData", err)
	}
	if _, err := client.Generation(t.Context(), zoneGB, start, end); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("generation: got %v, want ErrNoData", err)
	}
	if _, err := client.Generation(t.Context(), zone60, start, end); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("generation of a zone with prices and load only: got %v, want ErrNoData", err)
	}
}

// Every calibrated zone must parse with the real client and stay close to what it was calibrated on.
func TestEmbeddedProfiles(t *testing.T) {
	profiles, err := parseProfiles(embeddedProfiles)
	if err != nil {
		t.Fatalf("parseProfiles: %v", err)
	}
	if len(profiles.Zones) < 40 {
		t.Fatalf("only %d zones in the embedded profiles", len(profiles.Zones))
	}
	m := model{revisionInterval: time.Hour, revisionRate: 0.02, revisionHorizon: 7 * 24 * time.Hour, profiles: profiles}
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, m, &now)
	start, end := mustTime(t, "2026-10-21T00:00:00Z"), mustTime(t, "2026-10-24T00:00:00Z")

	for zone, z := range profiles.Zones {
		t.Run(zone, func(t *testing.T) {
			if p := z.Price; p != nil {
				prices, err := client.DayAheadPrices(t.Context(), zone, start, end)
				if err != nil {
					t.Fatalf("DayAheadPrices: %v", err)
				}
				lo, hi := widen(p.curve).Min, widen(p.curve).Max
				for _, s := range p.Sequences {
					lo = min(lo, lo+s.Offset-5*s.OffsetSD)
					hi = max(hi, hi+s.Offset+5*s.OffsetSD)
				}
				for _, pr := range prices {
					eur := float64(pr.PriceMicros) / 1e6
					if pr.Resolution != time.Duration(p.ResolutionMin)*time.Minute || eur < lo-0.06*math.Abs(lo)-1 || eur > hi+0.06*math.Abs(hi)+1 {
						t.Fatalf("price %+v outside the profile (%d min, %.2f..%.2f)", pr, p.ResolutionMin, lo, hi)
					}
				}
			}
			if p := z.Load; p != nil {
				loads, err := client.ActualLoad(t.Context(), zone, start, end)
				if err != nil {
					t.Fatalf("ActualLoad: %v", err)
				}
				hi := max(widen(p.Weekday).Max, widen(p.Weekend).Max) * 1.04
				for _, l := range loads {
					if l.Resolution != time.Duration(p.ResolutionMin)*time.Minute || l.QuantityW < 0 || float64(l.QuantityW)/1e6 > hi {
						t.Fatalf("load %+v outside the profile (%d min, up to %.0f MW)", l, p.ResolutionMin, hi)
					}
				}
			}
			if p := z.Generation; p != nil {
				gen, err := client.Generation(t.Context(), zone, start, end)
				if err != nil {
					t.Fatalf("Generation: %v", err)
				}
				series := map[string]bool{}
				for _, g := range gen {
					series[g.PSRType+"/"+g.Direction] = true
					if g.QuantityW < 0 {
						t.Fatalf("negative generation %+v", g)
					}
				}
				for _, s := range p.Series {
					if !series[s.PSRType+"/"+s.Direction] && s.MissingRate < 0.99 && meanHourly(s.Hourly) > 0 {
						t.Errorf("series %s/%s missing from the document", s.PSRType, s.Direction)
					}
				}
			}
		})
	}
}

func TestNoDataBeforePublication(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, noRevisions, &now)
	future := mustTime(t, "2026-10-26T00:00:00Z")

	if _, err := client.ActualLoad(t.Context(), zoneNL, future, future.Add(24*time.Hour)); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("load: got %v, want ErrNoData", err)
	}
	if _, err := client.DayAheadPrices(t.Context(), zoneNL, future, future.Add(24*time.Hour)); !errors.Is(err, entsoe.ErrNoData) {
		t.Errorf("prices: got %v, want ErrNoData", err)
	}
}

func TestRevisions(t *testing.T) {
	m := model{revisionInterval: time.Hour, revisionRate: 0.05, revisionHorizon: 7 * 24 * time.Hour, profiles: testProfiles}
	now := mustTime(t, "2026-10-24T14:07:00Z")
	client, _ := fakeAt(t, m, &now)
	start, end := mustTime(t, "2026-10-14T00:00:00Z"), mustTime(t, "2026-10-24T00:00:00Z")

	fetch := func() map[time.Time]int64 {
		t.Helper()
		loads, err := client.ActualLoad(t.Context(), zoneNL, start, end)
		if err != nil {
			t.Fatalf("ActualLoad: %v", err)
		}
		out := make(map[time.Time]int64, len(loads))
		for _, l := range loads {
			out[l.DeliveryStart] = l.QuantityW
		}
		return out
	}

	before := fetch()
	if again := fetch(); !equalValues(before, again) {
		t.Fatal("two requests at the same time disagree")
	}

	now = now.Add(6 * time.Hour)
	after := fetch()
	var revised int
	for slot, kw := range before {
		if after[slot] == kw {
			continue
		}
		revised++
		if now.Sub(slot) > m.revisionHorizon+6*time.Hour {
			t.Errorf("slot %s revised beyond the horizon", slot)
		}
	}
	// 6 epochs, about 670 slots inside the horizon, 5% per epoch.
	if revised < 50 {
		t.Errorf("only %d slots revised after 6 epochs", revised)
	}
}

func equalValues(a, b map[time.Time]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestBadRequests(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	_, base := fakeAt(t, noRevisions, &now)
	valid := "securityToken=t&periodStart=202610230000&periodEnd=202610240000"
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"missing token", "documentType=A44&in_Domain=Z&out_Domain=Z&periodStart=202610230000&periodEnd=202610240000", http.StatusUnauthorized},
		{"unknown document type", valid + "&documentType=A73", http.StatusBadRequest},
		{"A75 forecast", valid + "&documentType=A75&processType=A01&in_Domain=Z", http.StatusBadRequest},
		{"A44 with different domains", valid + "&documentType=A44&in_Domain=A&out_Domain=B", http.StatusBadRequest},
		{"A65 forecast", valid + "&documentType=A65&processType=A01&outBiddingZone_Domain=Z", http.StatusBadRequest},
		{"bad period", "securityToken=t&documentType=A44&in_Domain=Z&out_Domain=Z&periodStart=2026-10-23&periodEnd=202610240000", http.StatusBadRequest},
		{"window over a year", "securityToken=t&documentType=A44&in_Domain=Z&out_Domain=Z&periodStart=202501010000&periodEnd=202610240000", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status, _ := call(t, http.MethodGet, base+"/api?"+tt.query, ""); status != tt.want {
				t.Errorf("status %d, want %d", status, tt.want)
			}
		})
	}
}

func TestFaultInjection(t *testing.T) {
	now := mustTime(t, "2026-10-24T14:07:00Z")
	_, base := fakeAt(t, noRevisions, &now)
	apiURL := base + "/api?securityToken=t&documentType=A65&processType=A16&outBiddingZone_Domain=" + zoneNL +
		"&periodStart=202610230000&periodEnd=202610240000"

	admin := func(method, body string) int {
		t.Helper()
		status, _ := call(t, method, base+"/admin/fault", body)
		return status
	}
	get := func() (int, http.Header) {
		t.Helper()
		return call(t, http.MethodGet, apiURL, "")
	}

	if got, _ := get(); got != http.StatusOK {
		t.Fatalf("before the fault: status %d", got)
	}
	if got := admin(http.MethodPut, `{"status":429,"retry_after":"7"}`); got != http.StatusNoContent {
		t.Fatalf("PUT fault: status %d", got)
	}
	if status, header := get(); status != http.StatusTooManyRequests || header.Get("Retry-After") != "7" {
		t.Errorf("with the fault: status %d, Retry-After %q", status, header.Get("Retry-After"))
	}
	if got := admin(http.MethodDelete, ""); got != http.StatusNoContent {
		t.Fatalf("DELETE fault: status %d", got)
	}
	if got, _ := get(); got != http.StatusOK {
		t.Errorf("after clearing: status %d", got)
	}

	for _, body := range []string{`{}`, `{"status":200}`, `{"delay":"soon"}`, `{"retry_after":"5"}`, `{"stauts":503}`} {
		if got := admin(http.MethodPut, body); got != http.StatusBadRequest {
			t.Errorf("PUT %s: status %d, want 400", body, got)
		}
	}
}

func TestFormatFixed(t *testing.T) {
	tests := []struct {
		v        int64
		decimals int
		want     string
	}{
		{13663500, 3, "13663.5"},
		{13837000, 3, "13837"},
		{13557505, 3, "13557.505"},
		{-467, 2, "-4.67"},
		{-5, 2, "-0.05"},
		{7500, 2, "75"},
		{0, 2, "0"},
	}
	for _, tt := range tests {
		if got := formatFixed(tt.v, tt.decimals); got != tt.want {
			t.Errorf("formatFixed(%d, %d) = %q, want %q", tt.v, tt.decimals, got, tt.want)
		}
	}
}
