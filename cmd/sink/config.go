package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr      = ":8080"
	defaultGroupID       = "energy-clickhouse-sink"
	defaultTopics        = "energy.public.day_ahead_price,energy.public.actual_load,energy.public.generation,energy.public.weather"
	defaultBatchSize     = 10000
	defaultFlushInterval = 2 * time.Second
	retryBase            = time.Second
	retryMax             = 30 * time.Second
)

// config is the sink configuration, read from environment variables (see .env.example).
type config struct {
	httpAddr      string
	brokers       string
	groupID       string
	topics        []string
	registryURL   string
	clickhouseDSN string
	batchSize     int
	flushInterval time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	get := func(name, fallback string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return fallback
	}
	cfg := config{
		httpAddr:      get("SINK_HTTP_ADDR", defaultHTTPAddr),
		brokers:       getenv("KAFKA_BOOTSTRAP_SERVERS"),
		groupID:       get("SINK_GROUP_ID", defaultGroupID),
		topics:        strings.Split(get("SINK_TOPICS", defaultTopics), ","),
		registryURL:   getenv("SCHEMA_REGISTRY_URL"),
		clickhouseDSN: getenv("CLICKHOUSE_DSN"),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
	}
	for name, v := range map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS": cfg.brokers,
		"SCHEMA_REGISTRY_URL":     cfg.registryURL,
		"CLICKHOUSE_DSN":          cfg.clickhouseDSN,
	} {
		if v == "" {
			return config{}, fmt.Errorf("%s is not set", name)
		}
	}
	for _, t := range cfg.topics {
		if strings.TrimSpace(t) == "" {
			return config{}, errors.New("SINK_TOPICS contains an empty topic")
		}
	}
	if v := getenv("SINK_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return config{}, fmt.Errorf("SINK_BATCH_SIZE %q must be a positive integer", v)
		}
		cfg.batchSize = n
	}
	if v := getenv("SINK_FLUSH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return config{}, fmt.Errorf("SINK_FLUSH_INTERVAL %q must be a positive Go duration", v)
		}
		cfg.flushInterval = d
	}
	return cfg, nil
}
