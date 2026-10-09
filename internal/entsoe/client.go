package entsoe

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is the production endpoint of the Transparency Platform API.
const DefaultBaseURL = "https://web-api.tp.entsoe.eu/api"

const (
	// A year of PT15M prices for one zone is a few MiB; anything far beyond that is not a valid answer.
	maxResponseBytes = 32 << 20
	// How much of an unexpected response body is kept in a StatusError.
	maxErrorBodyBytes = 512

	periodLayout = "200601021504"
)

// Retry policy defaults (ADR 0015).
const (
	defaultRequestTimeout = 30 * time.Second
	defaultMaxAttempts    = 5
	defaultBaseDelay      = 500 * time.Millisecond
	defaultMaxDelay       = 30 * time.Second
)

// StatusError is a non-200 response that is not an acknowledgement document.
type StatusError struct {
	StatusCode int
	Body       string // start of the response body

	retryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("entsoe: unexpected status %d: %s", e.StatusCode, e.Body)
}

// Client calls the Transparency Platform API. It retries transient failures with capped
// exponential backoff and full jitter; see retryDelay for what counts as transient.
type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client

	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
}

// NewClient returns a client for the API at baseURL that authenticates with token.
func NewClient(baseURL, token string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("base URL %q is not an absolute http(s) URL", baseURL)
	}
	if token == "" {
		return nil, errors.New("API token is empty")
	}
	return &Client{
		baseURL:     u,
		token:       token,
		httpClient:  &http.Client{Timeout: defaultRequestTimeout},
		maxAttempts: defaultMaxAttempts,
		baseDelay:   defaultBaseDelay,
		maxDelay:    defaultMaxDelay,
		sleep:       sleep,
	}, nil
}

// DayAheadPrices fetches day-ahead prices (A44) of a bidding zone for the interval [start, end).
// When the platform has no data for the interval, the error matches ErrNoData.
func (c *Client) DayAheadPrices(ctx context.Context, zone string, start, end time.Time) ([]DayAheadPrice, error) {
	query := url.Values{
		"documentType": {documentTypeDayAheadPrices},
		"in_Domain":    {zone},
		"out_Domain":   {zone},
		"periodStart":  {start.UTC().Format(periodLayout)},
		"periodEnd":    {end.UTC().Format(periodLayout)},
	}
	body, err := c.get(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fetch day-ahead prices: %w", err)
	}
	prices, err := ParseDayAheadPrices(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse day-ahead prices: %w", err)
	}
	return prices, nil
}

// get performs the request until it succeeds, fails permanently or runs out of attempts.
func (c *Client) get(ctx context.Context, query url.Values) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		body, err := c.do(ctx, query)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		delay, retry := c.retryDelay(err, attempt)
		if !retry {
			return nil, fmt.Errorf("attempt %d: %w", attempt, err)
		}
		if sleepErr := c.sleep(ctx, delay); sleepErr != nil {
			return nil, fmt.Errorf("%w, last error: %w", sleepErr, err)
		}
	}
}

// retryDelay decides whether the failed attempt is retried and how long to wait first.
//
// Retried: transport errors, per-request timeouts, 429 and 5xx. Not retried: acknowledgement
// documents and other 4xx (the same request would fail the same way), and a Retry-After longer than
// maxDelay, which is left to the caller's next scheduled run instead of blocking this one.
func (c *Client) retryDelay(err error, attempt int) (time.Duration, bool) {
	if attempt >= c.maxAttempts {
		return 0, false
	}
	var ack *AcknowledgementError
	if errors.As(err, &ack) {
		return 0, false
	}
	var retryAfter time.Duration
	var status *StatusError
	if errors.As(err, &status) {
		if status.StatusCode != http.StatusTooManyRequests && status.StatusCode < 500 {
			return 0, false
		}
		retryAfter = status.retryAfter
	}
	if retryAfter > c.maxDelay {
		return 0, false
	}

	// Full jitter: a uniform delay below the exponential ceiling spreads out clients that failed together.
	ceiling := c.maxDelay
	if shift := attempt - 1; shift < 32 && c.baseDelay<<shift < ceiling {
		ceiling = c.baseDelay << shift
	}
	delay := rand.N(ceiling) //nolint:gosec // jitter does not need a cryptographic source
	return max(delay, retryAfter), true
}

// do performs a single request and returns the body of a 200 response.
func (c *Client) do(ctx context.Context, query url.Values) ([]byte, error) {
	u := *c.baseURL
	query.Set("securityToken", c.token)
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", redact(err))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", redact(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: fmt.Sprintf("response larger than %d bytes", maxResponseBytes)}
	}
	if resp.StatusCode == http.StatusOK {
		return body, nil
	}

	// "No data" and request errors arrive as an acknowledgement document with a 4xx status.
	if ack := acknowledgement(body); ack != nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return nil, ack
	}
	return nil, &StatusError{
		StatusCode: resp.StatusCode,
		Body:       string(body[:min(len(body), maxErrorBodyBytes)]),
		retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

// redact strips the request URL from an http.Client error, because the URL carries the API token.
func redact(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// acknowledgement returns the acknowledgement document in body, or nil if body is something else.
func acknowledgement(body []byte) *AcknowledgementError {
	dec := xml.NewDecoder(bytes.NewReader(body))
	root, err := rootElement(dec)
	if err != nil || root.Name.Local != "Acknowledgement_MarketDocument" {
		return nil
	}
	var ack *AcknowledgementError
	if errors.As(decodeAcknowledgement(dec, root), &ack) {
		return ack
	}
	return nil
}

// parseRetryAfter reads a Retry-After header in either form (delay in seconds or HTTP date).
// It returns 0 when the header is absent, invalid or in the past.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), 0)
	}
	return 0
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
