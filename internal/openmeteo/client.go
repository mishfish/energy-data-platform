package openmeteo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mishfish/energy-data-platform/internal/fixedpoint"
)

// DefaultBaseURL is the Open-Meteo forecast endpoint.
const DefaultBaseURL = "https://api.open-meteo.com/v1/forecast"

// Variables fetched for every point (ADR 0031): demand (temperature), wind generation (wind at hub
// height), solar generation (shortwave radiation, cloud cover).
const hourlyVariables = "temperature_2m,wind_speed_100m,shortwave_radiation,cloud_cover"

const (
	// Weather values are stored as numeric(7, 2): hundredths, up to five whole digits (ADR 0031).
	valueDecimals = 2
	valueMaxWhole = 5

	maxResponseBytes = 8 << 20
	timeLayout       = "2006-01-02T15:04"

	// Retry policy, the same shape as the ENTSO-E client's (ADR 0015).
	defaultRequestTimeout = 30 * time.Second
	defaultMaxAttempts    = 5
	defaultBaseDelay      = 500 * time.Millisecond
	defaultMaxDelay       = 30 * time.Second
)

// Weather is the hourly weather of one point of a bidding zone. A value the API returns as null
// stays nil.
type Weather struct {
	BiddingZone     string    // EIC code
	Point           string    // point name, see ZonePoints
	ValidFrom       time.Time // start of the hour, UTC
	Temperature     *int64    // temperature at 2 m, hundredths of °C
	WindSpeed100m   *int64    // wind speed at 100 m, hundredths of m/s
	ShortwaveRad    *int64    // shortwave radiation, hundredths of W/m²
	CloudCover      *int64    // total cloud cover, hundredths of a percent
	SourceCreatedAt time.Time // when the values were fetched; the API has no document timestamp
}

// StatusError is a response with a status other than 200.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("open-meteo: status %d: %s", e.StatusCode, e.Body)
}

// Client calls the Open-Meteo forecast API, retrying transport errors, 429 and 5xx with capped
// exponential backoff and full jitter.
type Client struct {
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
}

// NewClient returns a client for the API at baseURL.
func NewClient(baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base URL %q is not an absolute http(s) URL", baseURL)
	}
	return &Client{baseURL: baseURL, httpClient: &http.Client{Timeout: defaultRequestTimeout}, now: time.Now, sleep: sleep}, nil
}

// Fetch returns the hourly weather of the zone's points for [start, end). start and end are
// expected on UTC midnights around today, as the ingest window is (ADR 0017); the request asks for
// the whole days and the hours outside the window are dropped.
func (c *Client) Fetch(ctx context.Context, zone string, points []Point, start, end time.Time) ([]Weather, error) {
	if len(points) == 0 || len(points) > 5 {
		return nil, fmt.Errorf("zone %s has %d weather points, want 1 to 5", zone, len(points))
	}
	fetched := c.now().UTC()
	today := fetched.Truncate(24 * time.Hour)
	pastDays := max(int(today.Sub(start.UTC())/(24*time.Hour)), 0)
	forecastDays := max(int((end.UTC().Sub(today)+24*time.Hour-1)/(24*time.Hour)), 1)

	lats := make([]string, len(points))
	lons := make([]string, len(points))
	for i, p := range points {
		lats[i] = strconv.FormatFloat(p.Latitude, 'f', -1, 64)
		lons[i] = strconv.FormatFloat(p.Longitude, 'f', -1, 64)
	}
	query := url.Values{
		"latitude":        {strings.Join(lats, ",")},
		"longitude":       {strings.Join(lons, ",")},
		"hourly":          {hourlyVariables},
		"past_days":       {strconv.Itoa(pastDays)},
		"forecast_days":   {strconv.Itoa(forecastDays)},
		"timezone":        {"UTC"},
		"wind_speed_unit": {"ms"},
	}
	body, err := c.get(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fetch weather for %s: %w", zone, err)
	}
	all, err := Parse(bytes.NewReader(body), zone, points, fetched)
	if err != nil {
		return nil, fmt.Errorf("parse weather for %s: %w", zone, err)
	}
	inWindow := all[:0]
	for _, w := range all {
		if !w.ValidFrom.Before(start) && w.ValidFrom.Before(end) {
			inWindow = append(inWindow, w)
		}
	}
	return inWindow, nil
}

type location struct {
	Hourly struct {
		Time               []string       `json:"time"`
		Temperature2m      []*json.Number `json:"temperature_2m"`
		WindSpeed100m      []*json.Number `json:"wind_speed_100m"`
		ShortwaveRadiation []*json.Number `json:"shortwave_radiation"`
		CloudCover         []*json.Number `json:"cloud_cover"`
	} `json:"hourly"`
}

// Parse reads a forecast response for the given points, in request order. Open-Meteo answers one
// location with an object and several with an array. Numbers are read as decimal strings, so values
// stay exact (ADR 0031).
func Parse(r io.Reader, zone string, points []Point, fetched time.Time) ([]Weather, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var locations []location
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		var one location
		if err := dec.Decode(&one); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		locations = []location{one}
	} else if err := dec.Decode(&locations); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(locations) != len(points) {
		return nil, fmt.Errorf("got %d locations for %d points", len(locations), len(points))
	}

	var out []Weather
	for i, loc := range locations {
		h := loc.Hourly
		n := len(h.Time)
		if n == 0 {
			return nil, fmt.Errorf("point %s: no hours", points[i].Name)
		}
		for _, series := range [][]*json.Number{h.Temperature2m, h.WindSpeed100m, h.ShortwaveRadiation, h.CloudCover} {
			if len(series) != n {
				return nil, fmt.Errorf("point %s: %d values for %d hours", points[i].Name, len(series), n)
			}
		}
		for j, ts := range h.Time {
			validFrom, err := time.Parse(timeLayout, ts)
			if err != nil {
				return nil, fmt.Errorf("point %s: time %q: %w", points[i].Name, ts, err)
			}
			w := Weather{BiddingZone: zone, Point: points[i].Name, ValidFrom: validFrom.UTC(), SourceCreatedAt: fetched}
			for _, v := range []struct {
				dst  **int64
				src  *json.Number
				name string
			}{
				{&w.Temperature, h.Temperature2m[j], "temperature_2m"},
				{&w.WindSpeed100m, h.WindSpeed100m[j], "wind_speed_100m"},
				{&w.ShortwaveRad, h.ShortwaveRadiation[j], "shortwave_radiation"},
				{&w.CloudCover, h.CloudCover[j], "cloud_cover"},
			} {
				if v.src == nil {
					continue
				}
				x, ok := fixedpoint.Parse(v.src.String(), valueDecimals, valueMaxWhole, true)
				if !ok {
					return nil, fmt.Errorf("point %s, %s at %s: invalid value %q", points[i].Name, v.name, ts, v.src.String())
				}
				*v.dst = &x
			}
			out = append(out, w)
		}
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, query url.Values) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		body, err := c.do(ctx, query)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil || attempt >= defaultMaxAttempts {
			return nil, fmt.Errorf("attempt %d: %w", attempt, err)
		}
		var status *StatusError
		if errors.As(err, &status) && status.StatusCode != http.StatusTooManyRequests && status.StatusCode < 500 {
			return nil, err
		}
		ceiling := min(defaultBaseDelay<<(attempt-1), defaultMaxDelay)
		if err := c.sleep(ctx, rand.N(ceiling)); err != nil { //nolint:gosec // jitter does not need a cryptographic source
			return nil, err
		}
	}
}

func (c *Client) do(ctx context.Context, query url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response larger than %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(body[:min(len(body), 512)])}
	}
	return body, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
