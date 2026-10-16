package sink

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func testGauge(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}

func countSeries(t *testing.T, c prometheus.Collector) int {
	t.Helper()
	ch := make(chan prometheus.Metric, 100)
	c.Collect(ch)
	close(ch)
	n := 0
	for range ch {
		n++
	}
	return n
}
