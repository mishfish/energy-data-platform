package openmeteo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const zoneNL = "10YNL----------L"

var twoPoints = []Point{{"Amsterdam", 52.37, 4.90}, {"Rotterdam", 51.92, 4.48}}

// response builds an Open-Meteo answer with the given hours for each location.
func response(locations int, times []string, temp []string) string {
	vals := "[" + strings.Join(temp, ",") + "]"
	loc := `{"latitude":52.366,"longitude":4.901,"hourly":{"time":["` + strings.Join(times, `","`) + `"],` +
		`"temperature_2m":` + vals + `,"wind_speed_100m":` + vals + `,"shortwave_radiation":` + vals + `,"cloud_cover":` + vals + `}}`
	if locations == 1 {
		return loc
	}
	return "[" + strings.TrimSuffix(strings.Repeat(loc+",", locations), ",") + "]"
}

func TestParse(t *testing.T) {
	fetched := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	times := []string{"2026-10-24T00:00", "2026-10-24T01:00"}

	t.Run("several locations, null kept as nil", func(t *testing.T) {
		got, err := Parse(strings.NewReader(response(2, times, []string{"-3.4", "null"})), zoneNL, twoPoints, fetched)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("got %d rows, want 4", len(got))
		}
		first := got[0]
		if first.Point != "Amsterdam" || first.BiddingZone != zoneNL || !first.ValidFrom.Equal(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)) ||
			first.Temperature == nil || *first.Temperature != -340 || !first.SourceCreatedAt.Equal(fetched) {
			t.Errorf("first row %+v", first)
		}
		if got[1].Temperature != nil || got[1].CloudCover != nil {
			t.Errorf("null values are not nil: %+v", got[1])
		}
		if got[2].Point != "Rotterdam" {
			t.Errorf("third row from point %s, want Rotterdam", got[2].Point)
		}
	})

	t.Run("one location is an object", func(t *testing.T) {
		got, err := Parse(strings.NewReader(response(1, times, []string{"12.4", "11"})), zoneNL, twoPoints[:1], fetched)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(got) != 2 || *got[0].WindSpeed100m != 1240 || *got[1].ShortwaveRad != 1100 {
			t.Errorf("got %+v", got)
		}
	})

	errs := []struct {
		name, body, wantErr string
		points              []Point
	}{
		{"fewer locations than points", response(1, times, []string{"1", "2"}), "1 locations for 2 points", twoPoints},
		{"value with three decimals", response(2, times, []string{"1.234", "2"}), `invalid value "1.234"`, twoPoints},
		{"value too large", response(2, times, []string{"123456", "2"}), `invalid value "123456"`, twoPoints},
		{"bad time", response(2, []string{"yesterday", "2026-10-24T01:00"}, []string{"1", "2"}), `time "yesterday"`, twoPoints},
		{"series length", strings.Replace(response(2, times, []string{"1", "2"}), `"cloud_cover":[1,2]`, `"cloud_cover":[1]`, 1), "1 values for 2 hours", twoPoints},
		{"not json", "<html>rate limited</html>", "decode response", twoPoints},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.body), zoneNL, tt.points, fetched)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The live response saved during the probe (data/live is gitignored); skipped when absent.
func TestParseLiveSample(t *testing.T) {
	f, err := os.Open("../../data/live/weather/sample.json")
	if err != nil {
		t.Skip("no live sample")
	}
	defer func() { _ = f.Close() }()
	got, err := Parse(f, zoneNL, twoPoints, time.Now())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2*216 {
		t.Errorf("got %d rows, want %d", len(got), 2*216)
	}
}

func TestFetchRequestAndWindow(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(response(2, []string{"2026-10-16T23:00", "2026-10-17T00:00", "2026-10-26T00:00"}, []string{"1", "2", "3"})))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL + "/v1/forecast")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.now = func() time.Time { return time.Date(2026, 10, 24, 10, 5, 0, 0, time.UTC) }

	start, end := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	rows, err := c.Fetch(context.Background(), zoneNL, twoPoints, start, end)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Only 2026-10-17T00:00 is inside [start, end) for each point.
	if len(rows) != 2 {
		t.Errorf("got %d rows in the window, want 2", len(rows))
	}
	q := got.URL.Query()
	want := map[string]string{
		"latitude": "52.37,51.92", "longitude": "4.9,4.48", "hourly": hourlyVariables,
		"past_days": "7", "forecast_days": "2", "timezone": "UTC", "wind_speed_unit": "ms",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestFetchRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte(response(1, []string{"2026-10-24T00:00"}, []string{"1"})))
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.now = func() time.Time { return time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC) }

	day := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	if _, err := c.Fetch(context.Background(), zoneNL, twoPoints[:1], day, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("%d calls, want 3 (429 and 502 retried)", calls.Load())
	}

	calls.Store(100)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":true,"reason":"Latitude must be in range"}`, http.StatusBadRequest)
	}))
	defer bad.Close()
	c2, _ := NewClient(bad.URL)
	c2.sleep = c.sleep
	if _, err := c2.Fetch(context.Background(), zoneNL, twoPoints[:1], day, day.Add(24*time.Hour)); err == nil || calls.Load() != 101 {
		t.Errorf("400 was retried or did not fail: err %v, calls %d", err, calls.Load()-100)
	}
}

// Every zone that is fetched for generation has weather points, 3 to 5, inside Europe.
func TestZonePoints(t *testing.T) {
	for name := range entsoe.BiddingZones {
		if name == "GB" || name == "UA_IPS" {
			continue
		}
		points, ok := ZonePoints[name]
		if !ok {
			t.Errorf("zone %s has no weather points", name)
			continue
		}
		if len(points) < 3 || len(points) > 5 {
			t.Errorf("zone %s has %d points, want 3 to 5", name, len(points))
		}
		seen := map[string]bool{}
		for _, p := range points {
			if p.Latitude < 34 || p.Latitude > 71 || p.Longitude < -11 || p.Longitude > 35 {
				t.Errorf("zone %s point %s at %.2f,%.2f is outside Europe", name, p.Name, p.Latitude, p.Longitude)
			}
			if seen[p.Name] {
				t.Errorf("zone %s lists point %s twice", name, p.Name)
			}
			seen[p.Name] = true
		}
	}
	for name := range ZonePoints {
		if _, ok := entsoe.BiddingZones[name]; !ok {
			t.Errorf("weather points for unknown zone %s", name)
		}
	}
}
