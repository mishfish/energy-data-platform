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

	defaultActualLoadInterval = 15 * time.Minute
	// Realised load is revised days to weeks after publication (ADR 0020).
	defaultActualLoadLookBack  = 7 * 24 * time.Hour
	defaultActualLoadLookAhead = 24 * time.Hour

	// Zones with data on the Transparency Platform, checked against the live API on 2026-10-24
	// (ADR 0029). Not listed for prices: BA, CY and GB (no data), UA_IPS (prices in UAH). Not listed
	// for load: GB, IE_SEM and UA_IPS (no data).
	defaultDayAheadZones = "AL,AT,BE,BG,CH,CZ,DE_LU,DK_1,DK_2,EE,ES,FI,FR,GR,HR,HU,IE_SEM," +
		"IT_CALA,IT_CNOR,IT_CSUD,IT_NORD,IT_SARD,IT_SICI,IT_SUD,LT,LV,ME,MK,NL," +
		"NO_1,NO_2,NO_3,NO_4,NO_5,PL,PT,RO,RS,SE_1,SE_2,SE_3,SE_4,SI,SK,XK"
	defaultActualLoadZones = "AL,AT,BA,BE,BG,CH,CY,CZ,DE_LU,DK_1,DK_2,EE,ES,FI,FR,GR,HR,HU," +
		"IT_CALA,IT_CNOR,IT_CSUD,IT_NORD,IT_SARD,IT_SICI,IT_SUD,LT,LV,ME,MK,NL," +
		"NO_1,NO_2,NO_3,NO_4,NO_5,PL,PT,RO,RS,SE_1,SE_2,SE_3,SE_4,SI,SK,XK"
)

// config is the service configuration, read from environment variables (see .env.example).
type config struct {
	httpAddr      string
	databaseURL   string
	entsoeBaseURL string
	entsoeToken   string

	dayAheadZones     []entsoe.Zone
	dayAheadInterval  time.Duration
	dayAheadLookBack  time.Duration
	dayAheadLookAhead time.Duration

	actualLoadZones     []entsoe.Zone
	actualLoadInterval  time.Duration
	actualLoadLookBack  time.Duration
	actualLoadLookAhead time.Duration
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

	for _, z := range []struct {
		name     string
		dst      *[]entsoe.Zone
		fallback string
	}{
		{"INGEST_DAY_AHEAD_ZONES", &cfg.dayAheadZones, defaultDayAheadZones},
		{"INGEST_ACTUAL_LOAD_ZONES", &cfg.actualLoadZones, defaultActualLoadZones},
	} {
		list := getenv(z.name)
		if list == "" {
			list = z.fallback
		}
		zones, err := entsoe.ParseZones(list)
		if err != nil {
			return config{}, fmt.Errorf("%s: %w", z.name, err)
		}
		*z.dst = zones
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
		{"INGEST_ACTUAL_LOAD_INTERVAL", &cfg.actualLoadInterval, defaultActualLoadInterval, time.Second},
		{"INGEST_ACTUAL_LOAD_LOOK_BACK", &cfg.actualLoadLookBack, defaultActualLoadLookBack, 0},
		{"INGEST_ACTUAL_LOAD_LOOK_AHEAD", &cfg.actualLoadLookAhead, defaultActualLoadLookAhead, time.Hour},
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
