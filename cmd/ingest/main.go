// Command ingest pulls ENTSO-E and Open-Meteo data into Postgres.
// For now it fetches ENTSO-E day-ahead prices and actual load for NL on a fixed interval and serves
// the HTTP endpoints (health, readiness, metrics).
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
)

const (
	shutdownTimeout = 10 * time.Second
	// M1 and M1.5 ingest a single bidding zone.
	biddingZoneNL = "10YNL----------L"
)

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
	jobs := []interface{ Run(context.Context) }{
		&ingest.Job[entsoe.DayAheadPrice]{
			Dataset: ingest.DatasetDayAheadPrice,
			Fetch:   client.DayAheadPrices,
			Store: func(ctx context.Context, prices []entsoe.DayAheadPrice) (int64, error) {
				return db.UpsertDayAheadPrices(ctx, pool, prices)
			},
			Metrics:   metrics,
			Logger:    logger,
			Zone:      biddingZoneNL,
			Interval:  cfg.dayAheadInterval,
			LookBack:  cfg.dayAheadLookBack,
			LookAhead: cfg.dayAheadLookAhead,
		},
		&ingest.Job[entsoe.ActualLoad]{
			Dataset: ingest.DatasetActualLoad,
			Fetch:   client.ActualLoad,
			Store: func(ctx context.Context, loads []entsoe.ActualLoad) (int64, error) {
				return db.UpsertActualLoad(ctx, pool, loads)
			},
			Metrics:   metrics,
			Logger:    logger,
			Zone:      biddingZoneNL,
			Interval:  cfg.actualLoadInterval,
			LookBack:  cfg.actualLoadLookBack,
			LookAhead: cfg.actualLoadLookAhead,
		},
	}

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
	logger.Info("ingestion jobs started", "jobs", len(jobs), "zone", biddingZoneNL)
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
