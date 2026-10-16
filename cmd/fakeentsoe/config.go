package main

import (
	"fmt"
	"strconv"
	"time"
)

const (
	defaultAddr             = ":8080"
	defaultRevisionInterval = time.Hour
	defaultRevisionRate     = 0.02
	// Matches the actual load look-back of ingest (ADR 0020), so every revision lands inside its window.
	defaultRevisionHorizon = 7 * 24 * time.Hour
)

type config struct {
	addr  string
	model model
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		addr: getenv("FAKE_ENTSOE_ADDR"),
		model: model{
			revisionInterval: defaultRevisionInterval,
			revisionRate:     defaultRevisionRate,
			revisionHorizon:  defaultRevisionHorizon,
		},
	}
	if cfg.addr == "" {
		cfg.addr = defaultAddr
	}
	if v := getenv("FAKE_ENTSOE_REVISION_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return config{}, fmt.Errorf("FAKE_ENTSOE_REVISION_INTERVAL %q must be a Go duration of at least 1s", v)
		}
		cfg.model.revisionInterval = d
	}
	if v := getenv("FAKE_ENTSOE_REVISION_RATE"); v != "" {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil || rate < 0 || rate > 1 {
			return config{}, fmt.Errorf("FAKE_ENTSOE_REVISION_RATE %q must be a number in [0, 1]", v)
		}
		cfg.model.revisionRate = rate
	}
	return cfg, nil
}
