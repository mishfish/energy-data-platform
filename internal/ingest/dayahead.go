// Package ingest runs the scheduled jobs that pull source data into Postgres.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const datasetDayAheadPrice = "day_ahead_price"

// Results of one run, used as the "result" metric label.
const (
	resultSuccess    = "success"
	resultNoData     = "no_data"
	resultFetchError = "fetch_error"
	resultStoreError = "store_error"
)

// PriceFetcher fetches day-ahead prices of a bidding zone for [start, end).
type PriceFetcher interface {
	DayAheadPrices(ctx context.Context, zone string, start, end time.Time) ([]entsoe.DayAheadPrice, error)
}

// PriceStore writes prices idempotently and returns how many rows were inserted or changed.
type PriceStore func(ctx context.Context, prices []entsoe.DayAheadPrice) (int64, error)

// DayAheadJob re-fetches a sliding window of day-ahead prices on a fixed interval and upserts it.
// Re-fetching is what picks up revisions; the store turns unchanged rows into no-ops.
type DayAheadJob struct {
	Fetcher PriceFetcher
	Store   PriceStore
	Metrics *Metrics
	Logger  *slog.Logger

	Zone     string
	Interval time.Duration
	// The window is [today - LookBack, today + LookAhead), where today is the current UTC midnight.
	LookBack  time.Duration
	LookAhead time.Duration
}

// Run fetches once immediately and then on every tick until ctx is cancelled. Runs never overlap:
// a run that outlasts the interval delays the next one, and the ticks it missed are dropped.
func (j *DayAheadJob) Run(ctx context.Context) {
	j.Metrics.initDataset(datasetDayAheadPrice)
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	for {
		j.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runOnce does one fetch and upsert and records its outcome. A failed run is not retried here
// beyond what the fetcher already does; the next tick fetches the same window again.
func (j *DayAheadJob) runOnce(ctx context.Context) {
	started := time.Now()
	start, end := window(started, j.LookBack, j.LookAhead)
	logger := j.Logger.With("dataset", datasetDayAheadPrice, "zone", j.Zone,
		"window_start", start.Format(time.RFC3339), "window_end", end.Format(time.RFC3339))

	result, fetched, changed, err := j.fetchAndStore(ctx, start, end)
	if ctx.Err() != nil {
		// Shutdown interrupted the run; it is neither a success nor a failure of the source.
		logger.Info("run interrupted by shutdown")
		return
	}

	elapsed := time.Since(started)
	j.Metrics.observe(datasetDayAheadPrice, result, elapsed, changed)
	logger = logger.With("result", result, "duration", elapsed.String())
	switch result {
	case resultSuccess:
		logger.Info("run finished", "rows_fetched", fetched, "rows_changed", changed)
	case resultNoData:
		logger.Warn("source has no data for the window")
	default:
		logger.Error("run failed", "err", err)
	}
}

func (j *DayAheadJob) fetchAndStore(ctx context.Context, start, end time.Time) (result string, fetched int, changed int64, err error) {
	prices, err := j.Fetcher.DayAheadPrices(ctx, j.Zone, start, end)
	switch {
	case errors.Is(err, entsoe.ErrNoData):
		return resultNoData, 0, 0, err
	case err != nil:
		return resultFetchError, 0, 0, err
	}
	changed, err = j.Store(ctx, prices)
	if err != nil {
		return resultStoreError, len(prices), 0, err
	}
	return resultSuccess, len(prices), changed, nil
}

// window returns the fetch interval around the UTC midnight of now. Anchoring it to midnight keeps
// the request identical for a whole day instead of sliding with every tick.
func window(now time.Time, lookBack, lookAhead time.Duration) (start, end time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)
	return today.Add(-lookBack), today.Add(lookAhead)
}
