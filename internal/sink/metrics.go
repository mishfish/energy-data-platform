package sink

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the sink metrics.
type Metrics struct {
	records       *prometheus.CounterVec
	batches       *prometheus.CounterVec
	writeDuration prometheus.Histogram
	lastCommit    prometheus.Gauge
	lag           *prometheus.GaugeVec
}

// NewMetrics registers the sink metrics with reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		records: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sink_records_total",
			Help: "Consumed records by topic and outcome (decoded, skipped tombstone, poison).",
		}, []string{"topic", "outcome"}),
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sink_batches_total",
			Help: "Batch flushes by result (success, write_error per failed attempt, commit_error).",
		}, []string{"result"}),
		writeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sink_batch_write_duration_seconds",
			Help:    "Duration of one ClickHouse write attempt for a batch.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
		}),
		lastCommit: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sink_last_commit_timestamp_seconds",
			Help: "Unix time of the last offset commit, i.e. of the last batch known to be in ClickHouse.",
		}),
		lag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "sink_consumer_lag",
			Help: "Records between the committed offset and the end of the partition, from librdkafka statistics.",
		}, []string{"topic", "partition"}),
	}
	for _, c := range []prometheus.Collector{m.records, m.batches, m.writeDuration, m.lastCommit, m.lag} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register sink metrics: %w", err)
		}
	}
	for _, result := range []string{"success", "write_error", "commit_error"} {
		m.batches.WithLabelValues(result)
	}
	return m, nil
}

// librdkafkaStats is the part of librdkafka's statistics JSON that carries consumer lag.
type librdkafkaStats struct {
	Topics map[string]struct {
		Partitions map[string]struct {
			ConsumerLag int64 `json:"consumer_lag"`
		} `json:"partitions"`
	} `json:"topics"`
}

// ObserveStats updates the lag gauges from a librdkafka statistics event. Partitions with an
// unknown lag (-1) and the internal partition "-1" are skipped.
func (m *Metrics) ObserveStats(statsJSON string) error {
	var stats librdkafkaStats
	if err := json.Unmarshal([]byte(statsJSON), &stats); err != nil {
		return fmt.Errorf("parse librdkafka stats: %w", err)
	}
	for topic, t := range stats.Topics {
		for partition, p := range t.Partitions {
			if id, err := strconv.Atoi(partition); err != nil || id < 0 || p.ConsumerLag < 0 {
				continue
			}
			m.lag.WithLabelValues(topic, partition).Set(float64(p.ConsumerLag))
		}
	}
	return nil
}
