package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	required := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS": "kafka:29092",
		"SCHEMA_REGISTRY_URL":     "http://apicurio:8080/apis/ccompat/v7",
		"CLICKHOUSE_DSN":          "clickhouse://energy:energy@clickhouse:9000/energy",
	}
	with := func(extra map[string]string) func(string) string {
		env := map[string]string{}
		for k, v := range required {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return func(k string) string { return env[k] }
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadConfig(with(nil))
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.httpAddr != ":8080" || cfg.groupID != "energy-clickhouse-sink" || cfg.batchSize != 10000 || cfg.flushInterval != 2*time.Second {
			t.Errorf("got %+v", cfg)
		}
		if want := []string{"energy.public.day_ahead_price", "energy.public.actual_load", "energy.public.generation", "energy.public.weather"}; !slices.Equal(cfg.topics, want) {
			t.Errorf("topics %v, want %v", cfg.topics, want)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg, err := loadConfig(with(map[string]string{
			"SINK_GROUP_ID": "replay", "SINK_TOPICS": "a,b,c", "SINK_BATCH_SIZE": "500", "SINK_FLUSH_INTERVAL": "250ms",
		}))
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.groupID != "replay" || len(cfg.topics) != 3 || cfg.batchSize != 500 || cfg.flushInterval != 250*time.Millisecond {
			t.Errorf("got %+v", cfg)
		}
	})

	invalid := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing brokers", map[string]string{"KAFKA_BOOTSTRAP_SERVERS": ""}, "KAFKA_BOOTSTRAP_SERVERS"},
		{"missing registry", map[string]string{"SCHEMA_REGISTRY_URL": ""}, "SCHEMA_REGISTRY_URL"},
		{"missing ClickHouse", map[string]string{"CLICKHOUSE_DSN": ""}, "CLICKHOUSE_DSN"},
		{"empty topic", map[string]string{"SINK_TOPICS": "a,,b"}, "SINK_TOPICS"},
		{"zero batch", map[string]string{"SINK_BATCH_SIZE": "0"}, "SINK_BATCH_SIZE"},
		{"bad interval", map[string]string{"SINK_FLUSH_INTERVAL": "soon"}, "SINK_FLUSH_INTERVAL"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(with(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want it to mention %s", err, tt.wantErr)
			}
		})
	}
}
