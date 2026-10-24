// Command ingest pulls ENTSO-E and Open-Meteo data into Postgres.
// It fetches ENTSO-E day-ahead prices, actual load and generation, and Open-Meteo weather, for the
// configured bidding zones on a fixed interval, one job per zone and dataset (ADR 0029), and serves
// health, readiness and metrics.
//
// Usage:
//
//	ingest           run the service
//	ingest migrate   apply pending schema migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mishfish/energy-data-platform/internal/db"
	"github.com/mishfish/energy-data-platform/internal/entsoe"
	"github.com/mishfish/energy-data-platform/internal/ingest"
	"github.com/mishfish/energy-data-platform/internal/openmeteo"
)

const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch args := os.Args[1:]; {
	case len(args) == 0:
		err = run(ctx, logger)
	case len(args) == 1 && args[0] == "migrate":
		err = migrate(ctx, logger)
	default:
		err = fmt.Errorf("unknown arguments %q, usage: ingest [migrate]", args)
	}
	if err != nil {
		logger.Error("ingest exited with error", "err", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, logger *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}
	return db.Migrate(ctx, logger, dsn)
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	client, err := entsoe.NewClient(cfg.entsoeBaseURL, cfg.entsoeToken)
	if err != nil {
		return fmt.Errorf("entsoe client: %w", err)
	}
	metrics, err := ingest.NewMetrics(prometheus.DefaultRegisterer)
	if err != nil {
		return err
	}
	weatherClient, err := openmeteo.NewClient(cfg.openMeteoBaseURL)
	if err != nil {
		return fmt.Errorf("open-meteo client: %w", err)
	}
	jobs := newJobs(cfg, client, weatherClient, pool, metrics, logger)

	var ready atomic.Bool
	srv := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           newMux(&ready),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.httpAddr)
		errCh <- srv.ListenAndServe()
	}()
	// Jobs stop when ctx is cancelled; an in-flight fetch or upsert is aborted, which is safe
	// because each upsert is one atomic statement and the next start fetches the same window again.
	// Each job runs in its own goroutine, so a slow source does not delay the others.
	jobCtx, stopJobs := context.WithCancel(ctx)
	defer stopJobs()
	var jobsDone sync.WaitGroup
	for _, job := range jobs {
		jobsDone.Go(func() { job.Run(jobCtx) })
	}
	logger.Info("ingestion jobs started", "jobs", len(jobs),
		"day_ahead_zones", len(cfg.dayAheadZones), "actual_load_zones", len(cfg.actualLoadZones),
		"generation_zones", len(cfg.generationZones), "weather_zones", len(cfg.weatherZones))
	ready.Store(true)

	select {
	case err := <-errCh:
		stopJobs()
		jobsDone.Wait()
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	ready.Store(false)
	jobsDone.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

type runner interface{ Run(context.Context) }

// newJobs returns one job per zone and dataset. Their first runs are spread evenly over the shortest
// interval, so a start does not send every request to the APIs at once (ADR 0029).
func newJobs(cfg config, client *entsoe.Client, weather *openmeteo.Client, pool *pgxpool.Pool, metrics *ingest.Metrics, logger *slog.Logger) []runner {
	storePrices := func(ctx context.Context, prices []entsoe.DayAheadPrice) (int64, error) {
		return db.UpsertDayAheadPrices(ctx, pool, prices)
	}
	storeLoad := func(ctx context.Context, loads []entsoe.ActualLoad) (int64, error) {
		return db.UpsertActualLoad(ctx, pool, loads)
	}
	storeGeneration := func(ctx context.Context, values []entsoe.Generation) (int64, error) {
		return db.UpsertGeneration(ctx, pool, values)
	}

	storeWeather := func(ctx context.Context, rows []openmeteo.Weather) (int64, error) {
		return db.UpsertWeather(ctx, pool, rows)
	}

	total := len(cfg.dayAheadZones) + len(cfg.actualLoadZones) + len(cfg.generationZones) + len(cfg.weatherZones)
	jobs := make([]runner, 0, total)
	// First runs are spread over the shortest interval, so even hourly jobs start within it and the
	// never-succeeded alert does not fire on a normal restart.
	spread := min(cfg.dayAheadInterval, cfg.actualLoadInterval, cfg.generationInterval, cfg.weatherInterval)
	delay := func(time.Duration) time.Duration {
		return time.Duration(len(jobs)) * spread / time.Duration(total)
	}
	for _, zone := range cfg.dayAheadZones {
		jobs = append(jobs, &ingest.Job[entsoe.DayAheadPrice]{
			Dataset: ingest.DatasetDayAheadPrice, Fetch: client.DayAheadPrices, Store: storePrices,
			Metrics: metrics, Logger: logger, Zone: zone,
			Interval: cfg.dayAheadInterval, StartDelay: delay(cfg.dayAheadInterval),
			LookBack: cfg.dayAheadLookBack, LookAhead: cfg.dayAheadLookAhead,
		})
	}
	for _, zone := range cfg.actualLoadZones {
		jobs = append(jobs, &ingest.Job[entsoe.ActualLoad]{
			Dataset: ingest.DatasetActualLoad, Fetch: client.ActualLoad, Store: storeLoad,
			Metrics: metrics, Logger: logger, Zone: zone,
			Interval: cfg.actualLoadInterval, StartDelay: delay(cfg.actualLoadInterval),
			LookBack: cfg.actualLoadLookBack, LookAhead: cfg.actualLoadLookAhead,
		})
	}
	for _, zone := range cfg.generationZones {
		jobs = append(jobs, &ingest.Job[entsoe.Generation]{
			Dataset: ingest.DatasetGeneration, Fetch: client.Generation, Store: storeGeneration,
			Metrics: metrics, Logger: logger, Zone: zone,
			Interval: cfg.generationInterval, StartDelay: delay(cfg.generationInterval),
			LookBack: cfg.generationLookBack, LookAhead: cfg.generationLookAhead,
		})
	}
	for _, zone := range cfg.weatherZones {
		points := openmeteo.ZonePoints[zone.Name]
		jobs = append(jobs, &ingest.Job[openmeteo.Weather]{
			Dataset: ingest.DatasetWeather, Store: storeWeather, Metrics: metrics, Logger: logger, Zone: zone,
			Fetch: func(ctx context.Context, eic string, start, end time.Time) ([]openmeteo.Weather, error) {
				return weather.Fetch(ctx, eic, points, start, end)
			},
			Interval: cfg.weatherInterval, StartDelay: delay(cfg.weatherInterval),
			LookBack: cfg.weatherLookBack, LookAhead: cfg.weatherLookAhead,
		})
	}
	return jobs
}
