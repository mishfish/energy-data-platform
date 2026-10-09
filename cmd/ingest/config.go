package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

const (
	defaultHTTPAddr          = ":8080"
	defaultDayAheadInterval  = 15 * time.Minute
	defaultDayAheadLookBack  = 24 * time.Hour
	defaultDayAheadLookAhead = 48 * time.Hour
)

// config is the service configuration, read from environment variables (see .env.example).
type config struct {
	httpAddr      string
	databaseURL   string
	entsoeBaseURL string
	entsoeToken   string

	dayAheadInterval  time.Duration
	dayAheadLookBack  time.Duration
	dayAheadLookAhead time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		httpAddr:      getenv("INGEST_HTTP_ADDR"),
		databaseURL:   getenv("DATABASE_URL"),
		entsoeBaseURL: getenv("ENTSOE_BASE_URL"),
		entsoeToken:   getenv("ENTSOE_API_TOKEN"),
	}
	if cfg.httpAddr == "" {
		cfg.httpAddr = defaultHTTPAddr
	}
	if cfg.entsoeBaseURL == "" {
		cfg.entsoeBaseURL = entsoe.DefaultBaseURL
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is not set")
	}
	if cfg.entsoeToken == "" {
		return config{}, errors.New("ENTSOE_API_TOKEN is not set")
	}

	durations := []struct {
		name     string
		dst      *time.Duration
		fallback time.Duration
		min      time.Duration
	}{
		{"INGEST_DAY_AHEAD_INTERVAL", &cfg.dayAheadInterval, defaultDayAheadInterval, time.Second},
		{"INGEST_DAY_AHEAD_LOOK_BACK", &cfg.dayAheadLookBack, defaultDayAheadLookBack, 0},
		{"INGEST_DAY_AHEAD_LOOK_AHEAD", &cfg.dayAheadLookAhead, defaultDayAheadLookAhead, time.Hour},
	}
	for _, d := range durations {
		*d.dst = d.fallback
		value := getenv(d.name)
		if value == "" {
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return config{}, fmt.Errorf("%s: %w", d.name, err)
		}
		if parsed < d.min {
			return config{}, fmt.Errorf("%s is %s, must be at least %s", d.name, parsed, d.min)
		}
		*d.dst = parsed
	}
	return cfg, nil
}
