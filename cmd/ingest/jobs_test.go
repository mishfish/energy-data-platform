package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
	"github.com/mishfish/energy-data-platform/internal/ingest"
)

func TestNewJobsSpreadsStarts(t *testing.T) {
	zones := func(list string) []entsoe.Zone {
		z, err := entsoe.ParseZones(list)
		if err != nil {
			t.Fatalf("ParseZones: %v", err)
		}
		return z
	}
	cfg := config{
		dayAheadZones: zones("NL,BE,DE_LU"), dayAheadInterval: 15 * time.Minute,
		actualLoadZones: zones("NL"), actualLoadInterval: 15 * time.Minute,
	}
	metrics, err := ingest.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	jobs := newJobs(cfg, nil, nil, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))

	type got struct {
		dataset, zone string
		delay         time.Duration
	}
	var all []got
	for _, j := range jobs {
		switch j := j.(type) {
		case *ingest.Job[entsoe.DayAheadPrice]:
			all = append(all, got{j.Dataset, j.Zone.Name, j.StartDelay})
		case *ingest.Job[entsoe.ActualLoad]:
			all = append(all, got{j.Dataset, j.Zone.Name, j.StartDelay})
		}
	}
	want := []got{
		{ingest.DatasetDayAheadPrice, "NL", 0},
		{ingest.DatasetDayAheadPrice, "BE", 225 * time.Second},
		{ingest.DatasetDayAheadPrice, "DE_LU", 450 * time.Second},
		{ingest.DatasetActualLoad, "NL", 675 * time.Second},
	}
	if len(all) != len(want) {
		t.Fatalf("got %d jobs %v, want %v", len(all), all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Errorf("job %d = %+v, want %+v", i, all[i], want[i])
		}
	}
}
