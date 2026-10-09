package ingest

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the ingestion metrics shared by all jobs, labelled by dataset.
type Metrics struct {
	runs        *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	rows        *prometheus.CounterVec
	lastSuccess *prometheus.GaugeVec
}

// NewMetrics registers the ingestion metrics with reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_fetches_total",
			Help: "Fetch-and-store runs by dataset and result (success, no_data, fetch_error, store_error).",
		}, []string{"dataset", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ingest_fetch_duration_seconds",
			Help: "Duration of a fetch-and-store run, including retries inside the source client.",
			// A run can last minutes when the client backs off (ADR 0015).
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"dataset", "result"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_rows_upserted_total",
			Help: "Rows inserted or changed in Postgres; unchanged rows of a re-fetch are not counted.",
		}, []string{"dataset"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ingest_last_success_timestamp_seconds",
			Help: "Unix time of the last run that fetched data and stored it.",
		}, []string{"dataset"}),
	}
	for _, c := range []prometheus.Collector{m.runs, m.duration, m.rows, m.lastSuccess} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register ingestion metrics: %w", err)
		}
	}
	return m, nil
}

// initDataset creates the per-result series of a dataset at zero. Without it a series first appears
// with value 1, and increase() over the range that contains its first run would miss that run.
// The last-success gauge is left absent on purpose: zero would read as "succeeded in 1970".
func (m *Metrics) initDataset(dataset string) {
	for _, result := range []string{resultSuccess, resultNoData, resultFetchError, resultStoreError} {
		m.runs.WithLabelValues(dataset, result)
		m.duration.WithLabelValues(dataset, result)
	}
	m.rows.WithLabelValues(dataset)
}

func (m *Metrics) observe(dataset, result string, elapsed time.Duration, rowsChanged int64) {
	m.runs.WithLabelValues(dataset, result).Inc()
	m.duration.WithLabelValues(dataset, result).Observe(elapsed.Seconds())
	if result != resultSuccess {
		return
	}
	m.rows.WithLabelValues(dataset).Add(float64(rowsChanged))
	m.lastSuccess.WithLabelValues(dataset).SetToCurrentTime()
}
