package entsoe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "secret-token"

var (
	windowStart = time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC)
	windowEnd   = time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

// response is one scripted answer of the fake API.
type response struct {
	status     int
	body       []byte
	retryAfter string
}

// fakeAPI serves the scripted responses in order and repeats the last one.
// It returns the client under test, the request counter and the delays the client asked to sleep.
func fakeAPI(t *testing.T, script ...response) (*Client, *atomic.Int32, *[]time.Duration) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1))
		resp := script[min(n, len(script))-1]
		if resp.retryAfter != "" {
			w.Header().Set("Retry-After", resp.retryAfter)
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write(resp.body)
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var delays []time.Duration
	client.sleep = func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}
	return client, &calls, &delays
}

func TestClientDayAheadPricesRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write(readFixture(t, "a44_nl_pt15m.xml"))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL+"/api", testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// A non-UTC window must be sent in UTC.
	amsterdam := time.FixedZone("CEST", 2*60*60)
	prices, err := client.DayAheadPrices(context.Background(), zoneNL, windowStart.In(amsterdam), windowEnd.In(amsterdam))
	if err != nil {
		t.Fatalf("DayAheadPrices: %v", err)
	}
	if len(prices) != 96 {
		t.Errorf("got %d prices, want 96", len(prices))
	}

	if got.Method != http.MethodGet || got.URL.Path != "/api" {
		t.Errorf("got %s %s, want GET /api", got.Method, got.URL.Path)
	}
	want := map[string]string{
		"securityToken": testToken,
		"documentType":  "A44",
		"in_Domain":     zoneNL,
		"out_Domain":    zoneNL,
		"periodStart":   "202610012200",
		"periodEnd":     "202610022200",
	}
	query := got.URL.Query()
	if len(query) != len(want) {
		t.Errorf("got %d query parameters, want %d: %v", len(query), len(want), query)
	}
	for name, value := range want {
		if query.Get(name) != value {
			t.Errorf("query parameter %s = %q, want %q", name, query.Get(name), value)
		}
	}
}

func TestClientActualLoadRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write(readFixture(t, "a65_nl_pt15m.xml"))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL+"/api", testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	loads, err := client.ActualLoad(context.Background(), zoneNL, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("ActualLoad: %v", err)
	}
	if len(loads) != 96 {
		t.Errorf("got %d slots, want 96", len(loads))
	}

	want := map[string]string{
		"securityToken":         testToken,
		"documentType":          "A65",
		"processType":           "A16",
		"outBiddingZone_Domain": zoneNL,
		"periodStart":           "202610012200",
		"periodEnd":             "202610022200",
	}
	query := got.URL.Query()
	if len(query) != len(want) {
		t.Errorf("got %d query parameters, want %d: %v", len(query), len(want), query)
	}
	for name, value := range want {
		if query.Get(name) != value {
			t.Errorf("query parameter %s = %q, want %q", name, query.Get(name), value)
		}
	}
}

func TestClientGenerationRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(genDocument(genSeries("in", "B19", "A01",
			hourPeriod("2026-10-01T22:00Z", qty("1", "1"), qty("2", "2"), qty("3", "3"), qty("4", "4"))))))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL+"/api", testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	gen, err := client.Generation(context.Background(), zoneNL, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if len(gen) != 4 {
		t.Errorf("got %d values, want 4", len(gen))
	}
	want := map[string]string{
		"securityToken": testToken,
		"documentType":  "A75",
		"processType":   "A16",
		"in_Domain":     zoneNL,
		"periodStart":   "202610012200",
		"periodEnd":     "202610022200",
	}
	query := got.URL.Query()
	if len(query) != len(want) {
		t.Errorf("got %d query parameters, want %d: %v", len(query), len(want), query)
	}
	for name, value := range want {
		if query.Get(name) != value {
			t.Errorf("query parameter %s = %q, want %q", name, query.Get(name), value)
		}
	}
}

func TestClientDayAheadPricesRetries(t *testing.T) {
	prices := readFixture(t, "a44_nl_pt15m.xml")
	noData := readFixture(t, "acknowledgement_no_data.xml")

	tests := []struct {
		name       string
		script     []response
		wantCalls  int32
		wantPrices int
		wantStatus int  // expected StatusError code, 0 for none
		wantNoData bool // expect an error matching ErrNoData
		minDelay   time.Duration
	}{
		{
			name:      "success on the first attempt",
			script:    []response{{status: 200, body: prices}},
			wantCalls: 1, wantPrices: 96,
		},
		{
			name:      "5xx and 429 are retried",
			script:    []response{{status: 503}, {status: 429}, {status: 502}, {status: 200, body: prices}},
			wantCalls: 4, wantPrices: 96,
		},
		{
			name:      "gives up after max attempts",
			script:    []response{{status: 500, body: []byte("boom")}},
			wantCalls: defaultMaxAttempts, wantStatus: 500,
		},
		{
			name:      "401 is not retried",
			script:    []response{{status: 401, body: []byte("Unauthorized")}},
			wantCalls: 1, wantStatus: 401,
		},
		{
			name:      "no data with 200",
			script:    []response{{status: 200, body: noData}},
			wantCalls: 1, wantNoData: true,
		},
		{
			name:      "no data with 400",
			script:    []response{{status: 400, body: noData}},
			wantCalls: 1, wantNoData: true,
		},
		{
			name:      "acknowledgement body on 503 is still retried",
			script:    []response{{status: 503, body: noData}, {status: 200, body: prices}},
			wantCalls: 2, wantPrices: 96,
		},
		{
			name:      "Retry-After is honoured",
			script:    []response{{status: 429, retryAfter: "7"}, {status: 200, body: prices}},
			wantCalls: 2, wantPrices: 96, minDelay: 7 * time.Second,
		},
		{
			name:      "Retry-After beyond the max delay stops retrying",
			script:    []response{{status: 429, retryAfter: "600"}},
			wantCalls: 1, wantStatus: 429,
		},
		{
			name:      "malformed 200 body is not retried",
			script:    []response{{status: 200, body: []byte("<html>maintenance</html>")}},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, calls, delays := fakeAPI(t, tt.script...)
			got, err := client.DayAheadPrices(context.Background(), zoneNL, windowStart, windowEnd)

			if calls.Load() != tt.wantCalls {
				t.Errorf("got %d requests, want %d", calls.Load(), tt.wantCalls)
			}
			if len(*delays) != int(tt.wantCalls)-1 {
				t.Errorf("got %d sleeps, want %d", len(*delays), tt.wantCalls-1)
			}
			for i, d := range *delays {
				if d < tt.minDelay || d > defaultMaxDelay {
					t.Errorf("sleep %d: %s, want between %s and %s", i+1, d, tt.minDelay, defaultMaxDelay)
				}
			}
			if len(got) != tt.wantPrices {
				t.Errorf("got %d prices, want %d", len(got), tt.wantPrices)
			}
			if (err == nil) != (tt.wantPrices > 0) {
				t.Fatalf("got error %v with %d prices expected", err, tt.wantPrices)
			}
			if errors.Is(err, ErrNoData) != tt.wantNoData {
				t.Errorf("errors.Is(err, ErrNoData) = %t, want %t: %v", !tt.wantNoData, tt.wantNoData, err)
			}
			var status *StatusError
			if tt.wantStatus != 0 && (!errors.As(err, &status) || status.StatusCode != tt.wantStatus) {
				t.Errorf("got error %v, want status %d", err, tt.wantStatus)
			}
			if err != nil && strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks the API token: %v", err)
			}
		})
	}
}

func TestClientBackoffCeiling(t *testing.T) {
	client, err := NewClient(DefaultBaseURL, testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.maxAttempts = 100
	transient := &StatusError{StatusCode: 503}

	tests := []struct {
		attempt int
		ceiling time.Duration
	}{
		{1, 500 * time.Millisecond},
		{2, time.Second},
		{3, 2 * time.Second},
		{6, 16 * time.Second},
		{7, 30 * time.Second},
		{60, 30 * time.Second}, // the shift must not overflow
		{99, 30 * time.Second},
	}
	for _, tt := range tests {
		for range 200 {
			delay, retry := client.retryDelay(transient, tt.attempt)
			if !retry {
				t.Fatalf("attempt %d: not retried", tt.attempt)
			}
			if delay < 0 || delay >= tt.ceiling {
				t.Fatalf("attempt %d: delay %s, want in [0, %s)", tt.attempt, delay, tt.ceiling)
			}
		}
	}
	if _, retry := client.retryDelay(transient, 100); retry {
		t.Error("retried after the last attempt")
	}
}

func TestClientTransportErrorRedactsToken(t *testing.T) {
	client, calls, _ := fakeAPI(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused from now on
	var err error
	if client.baseURL, err = client.baseURL.Parse(srv.URL); err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	attempts := 0
	client.sleep = func(context.Context, time.Duration) error {
		attempts++
		return nil
	}

	_, err = client.DayAheadPrices(context.Background(), zoneNL, windowStart, windowEnd)
	if err == nil {
		t.Fatal("got no error from a closed server")
	}
	if attempts != defaultMaxAttempts-1 {
		t.Errorf("got %d retries, want %d", attempts, defaultMaxAttempts-1)
	}
	if calls.Load() != 0 {
		t.Errorf("got %d requests on the fake API, want 0", calls.Load())
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "securityToken") {
		t.Errorf("error leaks the API token: %v", err)
	}
}

func TestClientStopsOnContextCancel(t *testing.T) {
	t.Run("while backing off", func(t *testing.T) {
		client, calls, _ := fakeAPI(t, response{status: 503})
		ctx, cancel := context.WithCancel(context.Background())
		client.sleep = func(ctx context.Context, d time.Duration) error {
			cancel()
			return sleep(ctx, d)
		}

		_, err := client.DayAheadPrices(ctx, zoneNL, windowStart, windowEnd)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got error %v, want context.Canceled", err)
		}
		var status *StatusError
		if !errors.As(err, &status) || status.StatusCode != 503 {
			t.Errorf("error does not carry the last failure: %v", err)
		}
		if calls.Load() != 1 {
			t.Errorf("got %d requests, want 1", calls.Load())
		}
	})

	t.Run("before the request", func(t *testing.T) {
		client, calls, delays := fakeAPI(t, response{status: 200})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.DayAheadPrices(ctx, zoneNL, windowStart, windowEnd)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got error %v, want context.Canceled", err)
		}
		if calls.Load() != 0 || len(*delays) != 0 {
			t.Errorf("got %d requests and %d sleeps, want none", calls.Load(), len(*delays))
		}
	})
}

func TestNewClientValidation(t *testing.T) {
	tests := []struct {
		name, baseURL, token string
	}{
		{"empty token", DefaultBaseURL, ""},
		{"relative URL", "/api", testToken},
		{"unsupported scheme", "ftp://example.org/api", testToken},
		{"unparsable URL", "http://[::1", testToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClient(tt.baseURL, tt.token); err == nil {
				t.Error("got no error")
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 0},
		{"7", 7 * time.Second},
		{"0", 0},
		{"-5", 0},
		{"soon", 0},
		{"Sat, 10 Oct 2026 12:00:30 GMT", 30 * time.Second},
		{"Sat, 10 Oct 2026 11:59:00 GMT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := parseRetryAfter(tt.value, now); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}
