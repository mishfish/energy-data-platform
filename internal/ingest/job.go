// Package ingest runs the scheduled jobs that pull source data into Postgres.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

// Datasets, used as the "dataset" metric label and log field.
const (
	DatasetDayAheadPrice = "day_ahead_price"
	DatasetActualLoad    = "actual_load"
)

// Results of one run, used as the "result" metric label.
const (
	resultSuccess    = "success"
	resultNoData     = "no_data"
	resultFetchError = "fetch_error"
	resultStoreError = "store_error"
)

// Job re-fetches a sliding window of one dataset on a fixed interval and upserts it (ADR 0017, 0020).
// Re-fetching is what picks up revisions; the store turns unchanged rows into no-ops.
type Job[T any] struct {
	Dataset string
	// Fetch returns the values of a bidding zone, by EIC code, for [start, end). An error matching
	// entsoe.ErrNoData is reported as no_data rather than as a failure.
	Fetch func(ctx context.Context, zone string, start, end time.Time) ([]T, error)
	// Store writes values idempotently and returns how many rows were inserted or changed.
	Store   func(ctx context.Context, values []T) (int64, error)
	Metrics *Metrics
	Logger  *slog.Logger

	Zone     entsoe.Zone
	Interval time.Duration
	// StartDelay postpones the first run. Jobs of one service spread their first runs over the
	// interval this way, so they do not all hit the API at once (ADR 0029).
	StartDelay time.Duration
	// The window is [today - LookBack, today + LookAhead), where today is the current UTC midnight.
	LookBack  time.Duration
	LookAhead time.Duration
}

// Run fetches once after StartDelay and then on every tick until ctx is cancelled. Runs never
// overlap: a run that outlasts the interval delays the next one, and the ticks it missed are dropped.
func (j *Job[T]) Run(ctx context.Context) {
	j.Metrics.initJob(j.Dataset, j.Zone.Name)
	if j.StartDelay > 0 {
		timer := time.NewTimer(j.StartDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
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
func (j *Job[T]) runOnce(ctx context.Context) {
	started := time.Now()
	start, end := window(started, j.LookBack, j.LookAhead)
	logger := j.Logger.With("dataset", j.Dataset, "zone", j.Zone.Name,
		"window_start", start.Format(time.RFC3339), "window_end", end.Format(time.RFC3339))

	result, fetched, changed, err := j.fetchAndStore(ctx, start, end)
	if ctx.Err() != nil {
		// Shutdown interrupted the run; it is neither a success nor a failure of the source.
		logger.Info("run interrupted by shutdown")
		return
	}

	elapsed := time.Since(started)
	j.Metrics.observe(j.Dataset, j.Zone.Name, result, elapsed, changed)
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

func (j *Job[T]) fetchAndStore(ctx context.Context, start, end time.Time) (result string, fetched int, changed int64, err error) {
	values, err := j.Fetch(ctx, j.Zone.EIC, start, end)
	switch {
	case errors.Is(err, entsoe.ErrNoData):
		return resultNoData, 0, 0, err
	case err != nil:
		return resultFetchError, 0, 0, err
	}
	changed, err = j.Store(ctx, values)
	if err != nil {
		return resultStoreError, len(values), 0, err
	}
	return resultSuccess, len(values), changed, nil
}

// window returns the fetch interval around the UTC midnight of now. Anchoring it to midnight keeps
// the request identical for a whole day instead of sliding with every tick.
func window(now time.Time, lookBack, lookAhead time.Duration) (start, end time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)
	return today.Add(-lookBack), today.Add(lookAhead)
}
