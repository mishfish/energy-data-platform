package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const zoneNL = "10YNL----------L"

type (
	fetchFunc = func(ctx context.Context, zone string, start, end time.Time) ([]entsoe.DayAheadPrice, error)
	storeFunc = func(ctx context.Context, prices []entsoe.DayAheadPrice) (int64, error)
)

// newJob returns a day-ahead price job with a 15-minute interval and its own metrics registry.
func newJob(t *testing.T, fetch fetchFunc, store storeFunc) (*Job[entsoe.DayAheadPrice], *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	metrics, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return &Job[entsoe.DayAheadPrice]{
		Dataset:   DatasetDayAheadPrice,
		Fetch:     fetch,
		Store:     store,
		Metrics:   metrics,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Zone:      entsoe.Zone{Name: "NL", EIC: zoneNL},
		Interval:  15 * time.Minute,
		LookBack:  24 * time.Hour,
		LookAhead: 48 * time.Hour,
	}, reg
}

// metricValue returns the value of a counter or gauge, or the sample count of a histogram, for the
// series with the given labels. A series that does not exist reads as 0.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
	series:
		for _, m := range family.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			for _, label := range m.GetLabel() {
				if labels[label.GetName()] != label.GetValue() {
					continue series
				}
			}
			switch {
			case m.GetCounter() != nil:
				return m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue()
			default:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return 0
}

func TestWindow(t *testing.T) {
	tests := []struct {
		name               string
		now                time.Time
		lookBack           time.Duration
		lookAhead          time.Duration
		wantStart, wantEnd string
	}{
		{
			name: "middle of the day", now: time.Date(2026, 10, 10, 13, 5, 0, 0, time.UTC),
			lookBack: 24 * time.Hour, lookAhead: 48 * time.Hour,
			wantStart: "2026-10-09T00:00:00Z", wantEnd: "2026-10-12T00:00:00Z",
		},
		{
			name: "just before midnight", now: time.Date(2026, 10, 10, 23, 59, 59, 0, time.UTC),
			lookBack: 24 * time.Hour, lookAhead: 48 * time.Hour,
			wantStart: "2026-10-09T00:00:00Z", wantEnd: "2026-10-12T00:00:00Z",
		},
		{
			name: "local time is converted to UTC first", now: time.Date(2026, 10, 11, 1, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
			lookBack: 24 * time.Hour, lookAhead: 48 * time.Hour,
			wantStart: "2026-10-09T00:00:00Z", wantEnd: "2026-10-12T00:00:00Z",
		},
		{
			name: "no look-back", now: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
			lookBack: 0, lookAhead: 24 * time.Hour,
			wantStart: "2026-10-10T00:00:00Z", wantEnd: "2026-10-11T00:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := window(tt.now, tt.lookBack, tt.lookAhead)
			if got := start.Format(time.RFC3339); got != tt.wantStart {
				t.Errorf("start = %s, want %s", got, tt.wantStart)
			}
			if got := end.Format(time.RFC3339); got != tt.wantEnd {
				t.Errorf("end = %s, want %s", got, tt.wantEnd)
			}
		})
	}
}

func TestJobRunOnceResults(t *testing.T) {
	prices := make([]entsoe.DayAheadPrice, 96)
	tests := []struct {
		name        string
		fetchErr    error
		storeErr    error
		wantResult  string
		wantStored  bool
		wantRows    float64
		wantSuccess bool
	}{
		{name: "success", wantResult: resultSuccess, wantStored: true, wantRows: 2, wantSuccess: true},
		{
			name:       "no data",
			fetchErr:   &entsoe.AcknowledgementError{Code: "999", Text: "No matching data found"},
			wantResult: resultNoData,
		},
		{name: "fetch error", fetchErr: &entsoe.StatusError{StatusCode: 503}, wantResult: resultFetchError},
		{name: "store error", storeErr: errors.New("connection refused"), wantResult: resultStoreError, wantStored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				stored := false
				job, reg := newJob(t,
					func(_ context.Context, zone string, start, end time.Time) ([]entsoe.DayAheadPrice, error) {
						// The synctest clock starts at 2000-01-01T00:00:00Z.
						if zone != zoneNL || start.Format(time.DateOnly) != "1999-12-31" || end.Format(time.DateOnly) != "2000-01-03" {
							t.Errorf("fetched zone %s, window %s/%s", zone, start, end)
						}
						time.Sleep(3 * time.Second)
						return prices, tt.fetchErr
					},
					func(_ context.Context, got []entsoe.DayAheadPrice) (int64, error) {
						stored = true
						if len(got) != len(prices) {
							t.Errorf("stored %d prices, want %d", len(got), len(prices))
						}
						return 2, tt.storeErr
					})

				job.runOnce(t.Context())

				if stored != tt.wantStored {
					t.Errorf("store called = %t, want %t", stored, tt.wantStored)
				}
				for _, result := range []string{resultSuccess, resultNoData, resultFetchError, resultStoreError} {
					want := 0.0
					if result == tt.wantResult {
						want = 1
					}
					labels := map[string]string{"dataset": DatasetDayAheadPrice, "zone": "NL", "result": result}
					if got := metricValue(t, reg, "ingest_fetches_total", labels); got != want {
						t.Errorf("ingest_fetches_total{result=%q} = %v, want %v", result, got, want)
					}
					histogram := map[string]string{"dataset": DatasetDayAheadPrice, "result": result}
					if got := metricValue(t, reg, "ingest_fetch_duration_seconds", histogram); got != want {
						t.Errorf("ingest_fetch_duration_seconds{result=%q} count = %v, want %v", result, got, want)
					}
				}
				dataset := map[string]string{"dataset": DatasetDayAheadPrice, "zone": "NL"}
				if got := metricValue(t, reg, "ingest_rows_upserted_total", dataset); got != tt.wantRows {
					t.Errorf("ingest_rows_upserted_total = %v, want %v", got, tt.wantRows)
				}
				wantLastSuccess := 0.0
				if tt.wantSuccess {
					wantLastSuccess = float64(time.Now().Unix())
				}
				if got := metricValue(t, reg, "ingest_last_success_timestamp_seconds", dataset); got != wantLastSuccess {
					t.Errorf("ingest_last_success_timestamp_seconds = %v, want %v", got, wantLastSuccess)
				}
			})
		})
	}
}

func TestJobRunSchedule(t *testing.T) {
	tests := []struct {
		name          string
		fetchDuration time.Duration
		after         time.Duration
		wantFetches   int32
	}{
		{name: "first run is immediate", fetchDuration: time.Second, after: time.Minute, wantFetches: 1},
		{name: "one run per tick", fetchDuration: time.Second, after: 46 * time.Minute, wantFetches: 4},
		// Runs start at 0 and 40 min; the ticks at 15 and 30 min fall into the first run and collapse into one.
		{name: "slow run delays the next one and drops ticks", fetchDuration: 40 * time.Minute, after: 85 * time.Minute, wantFetches: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var fetches, running, overlaps atomic.Int32
				job, _ := newJob(t,
					func(ctx context.Context, _ string, _, _ time.Time) ([]entsoe.DayAheadPrice, error) {
						fetches.Add(1)
						if running.Add(1) > 1 {
							overlaps.Add(1)
						}
						defer running.Add(-1)
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(tt.fetchDuration):
							return nil, nil
						}
					},
					func(context.Context, []entsoe.DayAheadPrice) (int64, error) { return 0, nil })

				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() {
					defer close(done)
					job.Run(ctx)
				}()

				time.Sleep(tt.after)
				synctest.Wait()
				if got := fetches.Load(); got != tt.wantFetches {
					t.Errorf("got %d fetches after %s, want %d", got, tt.after, tt.wantFetches)
				}
				if overlaps.Load() != 0 {
					t.Errorf("%d runs overlapped", overlaps.Load())
				}

				cancel()
				<-done // Run must return on cancellation, also in the middle of a fetch.
			})
		})
	}
}

func TestJobShutdownDuringRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job, reg := newJob(t,
			func(ctx context.Context, _ string, _, _ time.Time) ([]entsoe.DayAheadPrice, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			func(context.Context, []entsoe.DayAheadPrice) (int64, error) {
				t.Error("store called after shutdown")
				return 0, nil
			})

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			job.Run(ctx)
		}()
		synctest.Wait()
		cancel()
		<-done

		// An interrupted run says nothing about the source, so it must not count as a failure.
		for _, result := range []string{resultSuccess, resultNoData, resultFetchError, resultStoreError} {
			labels := map[string]string{"dataset": DatasetDayAheadPrice, "zone": "NL", "result": result}
			if got := metricValue(t, reg, "ingest_fetches_total", labels); got != 0 {
				t.Errorf("ingest_fetches_total{result=%q} = %v, want 0", result, got)
			}
		}
	})
}

func TestJobRunInitialisesSeries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job, reg := newJob(t,
			func(ctx context.Context, _ string, _, _ time.Time) ([]entsoe.DayAheadPrice, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			func(context.Context, []entsoe.DayAheadPrice) (int64, error) { return 0, nil })

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			job.Run(ctx)
		}()
		synctest.Wait()

		// Before any run has finished, every result series exists at zero and the freshness gauge is absent.
		families, err := reg.Gather()
		if err != nil {
			t.Fatalf("gather metrics: %v", err)
		}
		got := map[string]int{}
		for _, family := range families {
			got[family.GetName()] = len(family.GetMetric())
		}
		want := map[string]int{
			"ingest_fetches_total":          4,
			"ingest_fetch_duration_seconds": 4,
			"ingest_rows_upserted_total":    1,
			"ingest_job_info":               1,
			"ingest_job_interval_seconds":   1,
		}
		if len(got) != len(want) {
			t.Errorf("got metric families %v, want %v", got, want)
		}
		for name, series := range want {
			if got[name] != series {
				t.Errorf("%s has %d series, want %d", name, got[name], series)
			}
		}

		cancel()
		<-done
	})
}

func TestJobStartDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fetches atomic.Int32
		job, _ := newJob(t,
			func(context.Context, string, time.Time, time.Time) ([]entsoe.DayAheadPrice, error) {
				fetches.Add(1)
				return nil, nil
			},
			func(context.Context, []entsoe.DayAheadPrice) (int64, error) { return 0, nil })
		job.StartDelay = 5 * time.Minute

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			job.Run(ctx)
		}()

		time.Sleep(4 * time.Minute)
		synctest.Wait()
		if got := fetches.Load(); got != 0 {
			t.Errorf("%d fetches before the start delay", got)
		}
		time.Sleep(2 * time.Minute) // first run at 5m, next at 20m
		synctest.Wait()
		if got := fetches.Load(); got != 1 {
			t.Errorf("%d fetches after the start delay, want 1", got)
		}

		cancel()
		<-done
	})
}

func TestJobStartDelayCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job, _ := newJob(t,
			func(context.Context, string, time.Time, time.Time) ([]entsoe.DayAheadPrice, error) {
				t.Error("fetched although stopped during the start delay")
				return nil, nil
			},
			func(context.Context, []entsoe.DayAheadPrice) (int64, error) { return 0, nil })
		job.StartDelay = time.Hour

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			job.Run(ctx)
		}()
		time.Sleep(time.Minute)
		cancel()
		<-done
	})
}
