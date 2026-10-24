package main

import (
	"errors"
	"io"
	"log/slog"
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

var noRevisions = model{}

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
	m := model{revisionInterval: time.Hour, revisionRate: 0.05, revisionHorizon: 7 * 24 * time.Hour}
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
		{"unknown document type", valid + "&documentType=A75", http.StatusBadRequest},
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
