package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
)

func TestLoadConfig(t *testing.T) {
	required := map[string]string{
		"DATABASE_URL":     "postgres://energy:energy@localhost:5432/energy",
		"ENTSOE_API_TOKEN": "token",
	}
	with := func(extra map[string]string) map[string]string {
		env := map[string]string{}
		for k, v := range required {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}

	t.Run("defaults", func(t *testing.T) {
		env := with(nil)
		cfg, err := loadConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		want := config{
			httpAddr:          ":8080",
			databaseURL:       required["DATABASE_URL"],
			entsoeBaseURL:     entsoe.DefaultBaseURL,
			entsoeToken:       "token",
			dayAheadInterval:  15 * time.Minute,
			dayAheadLookBack:  24 * time.Hour,
			dayAheadLookAhead: 48 * time.Hour,

			actualLoadInterval:  15 * time.Minute,
			actualLoadLookBack:  7 * 24 * time.Hour,
			actualLoadLookAhead: 24 * time.Hour,
		}
		if cfg != want {
			t.Errorf("got %+v, want %+v", cfg, want)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		env := with(map[string]string{
			"INGEST_HTTP_ADDR":              ":9000",
			"ENTSOE_BASE_URL":               "http://localhost:8081/api",
			"INGEST_DAY_AHEAD_INTERVAL":     "1m",
			"INGEST_DAY_AHEAD_LOOK_BACK":    "0s",
			"INGEST_DAY_AHEAD_LOOK_AHEAD":   "72h",
			"INGEST_ACTUAL_LOAD_INTERVAL":   "5m",
			"INGEST_ACTUAL_LOAD_LOOK_BACK":  "720h",
			"INGEST_ACTUAL_LOAD_LOOK_AHEAD": "2h",
		})
		cfg, err := loadConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.httpAddr != ":9000" || cfg.entsoeBaseURL != "http://localhost:8081/api" ||
			cfg.dayAheadInterval != time.Minute || cfg.dayAheadLookBack != 0 || cfg.dayAheadLookAhead != 72*time.Hour ||
			cfg.actualLoadInterval != 5*time.Minute || cfg.actualLoadLookBack != 720*time.Hour || cfg.actualLoadLookAhead != 2*time.Hour {
			t.Errorf("got %+v", cfg)
		}
	})

	invalid := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing database URL", map[string]string{"DATABASE_URL": ""}, "DATABASE_URL"},
		{"missing token", map[string]string{"ENTSOE_API_TOKEN": ""}, "ENTSOE_API_TOKEN"},
		{"unparsable interval", map[string]string{"INGEST_DAY_AHEAD_INTERVAL": "15"}, "INGEST_DAY_AHEAD_INTERVAL"},
		{"zero interval", map[string]string{"INGEST_DAY_AHEAD_INTERVAL": "0s"}, "INGEST_DAY_AHEAD_INTERVAL"},
		{"negative look-back", map[string]string{"INGEST_DAY_AHEAD_LOOK_BACK": "-24h"}, "INGEST_DAY_AHEAD_LOOK_BACK"},
		{"zero look-ahead", map[string]string{"INGEST_DAY_AHEAD_LOOK_AHEAD": "0s"}, "INGEST_DAY_AHEAD_LOOK_AHEAD"},
		{"zero load interval", map[string]string{"INGEST_ACTUAL_LOAD_INTERVAL": "0s"}, "INGEST_ACTUAL_LOAD_INTERVAL"},
		{"negative load look-back", map[string]string{"INGEST_ACTUAL_LOAD_LOOK_BACK": "-1h"}, "INGEST_ACTUAL_LOAD_LOOK_BACK"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			env := with(tt.env)
			_, err := loadConfig(func(k string) string { return env[k] })
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %v, want it to mention %s", err, tt.wantErr)
			}
		})
	}
}
