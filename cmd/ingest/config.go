package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/mishfish/energy-data-platform/internal/entsoe"
	"github.com/mishfish/energy-data-platform/internal/openmeteo"
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
	// Checked on 2026-10-24 (ADR 0030): every zone except GB and UA_IPS publishes A75.
	defaultGenerationZones = "AL,AT,BA,BE,BG,CH,CY,CZ,DE_LU,DK_1,DK_2,EE,ES,FI,FR,GR,HR,HU,IE_SEM," +
		"IT_CALA,IT_CNOR,IT_CSUD,IT_NORD,IT_SARD,IT_SICI,IT_SUD,LT,LV,ME,MK,NL," +
		"NO_1,NO_2,NO_3,NO_4,NO_5,PL,PT,RO,RS,SE_1,SE_2,SE_3,SE_4,SI,SK,XK"

	// Generation is revised like load, so it gets the same window (ADR 0030).
	defaultGenerationInterval  = 15 * time.Minute
	defaultGenerationLookBack  = 7 * 24 * time.Hour
	defaultGenerationLookAhead = 24 * time.Hour

	// Weather (ADR 0031): models update hourly; 7 days of analysis back, 2 days of forecast ahead.
	defaultWeatherZones     = defaultGenerationZones
	defaultWeatherInterval  = time.Hour
	defaultWeatherLookBack  = 7 * 24 * time.Hour
	defaultWeatherLookAhead = 48 * time.Hour
)

// config is the service configuration, read from environment variables (see .env.example).
type config struct {
	httpAddr         string
	databaseURL      string
	entsoeBaseURL    string
	entsoeToken      string
	openMeteoBaseURL string

	dayAheadZones     []entsoe.Zone
	dayAheadInterval  time.Duration
	dayAheadLookBack  time.Duration
	dayAheadLookAhead time.Duration

	actualLoadZones     []entsoe.Zone
	actualLoadInterval  time.Duration
	actualLoadLookBack  time.Duration
	actualLoadLookAhead time.Duration

	generationZones     []entsoe.Zone
	generationInterval  time.Duration
	generationLookBack  time.Duration
	generationLookAhead time.Duration

	weatherZones     []entsoe.Zone
	weatherInterval  time.Duration
	weatherLookBack  time.Duration
	weatherLookAhead time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		httpAddr:      getenv("INGEST_HTTP_ADDR"),
		databaseURL:   getenv("DATABASE_URL"),
		entsoeBaseURL: getenv("ENTSOE_BASE_URL"),
		entsoeToken:   getenv("ENTSOE_API_TOKEN"),

		openMeteoBaseURL: getenv("OPEN_METEO_BASE_URL"),
	}
	if cfg.openMeteoBaseURL == "" {
		cfg.openMeteoBaseURL = openmeteo.DefaultBaseURL
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
		{"INGEST_GENERATION_ZONES", &cfg.generationZones, defaultGenerationZones},
		{"INGEST_WEATHER_ZONES", &cfg.weatherZones, defaultWeatherZones},
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
	for _, z := range cfg.weatherZones {
		if len(openmeteo.ZonePoints[z.Name]) == 0 {
			return config{}, fmt.Errorf("INGEST_WEATHER_ZONES: zone %s has no weather points", z.Name)
		}
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
		{"INGEST_GENERATION_INTERVAL", &cfg.generationInterval, defaultGenerationInterval, time.Second},
		{"INGEST_GENERATION_LOOK_BACK", &cfg.generationLookBack, defaultGenerationLookBack, 0},
		{"INGEST_GENERATION_LOOK_AHEAD", &cfg.generationLookAhead, defaultGenerationLookAhead, time.Hour},
		{"INGEST_WEATHER_INTERVAL", &cfg.weatherInterval, defaultWeatherInterval, time.Minute},
		{"INGEST_WEATHER_LOOK_BACK", &cfg.weatherLookBack, defaultWeatherLookBack, 0},
		{"INGEST_WEATHER_LOOK_AHEAD", &cfg.weatherLookAhead, defaultWeatherLookAhead, 24 * time.Hour},
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
