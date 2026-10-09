// Command ingest pulls ENTSO-E and Open-Meteo data into Postgres.
// For now it fetches ENTSO-E day-ahead prices for NL on a fixed interval and serves the HTTP
// endpoints (health, readiness, metrics).
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
	// M1 ingests a single bidding zone.
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
	job := &ingest.DayAheadJob{
		Fetcher: client,
		Store: func(ctx context.Context, prices []entsoe.DayAheadPrice) (int64, error) {
			return db.UpsertDayAheadPrices(ctx, pool, prices)
		},
		Metrics:   metrics,
		Logger:    logger,
		Zone:      biddingZoneNL,
		Interval:  cfg.dayAheadInterval,
		LookBack:  cfg.dayAheadLookBack,
		LookAhead: cfg.dayAheadLookAhead,
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
	// The job stops when ctx is cancelled; an in-flight fetch or upsert is aborted, which is safe
	// because the upsert is one atomic statement and the next start fetches the same window again.
	jobCtx, stopJob := context.WithCancel(ctx)
	defer stopJob()
	jobDone := make(chan struct{})
	go func() {
		defer close(jobDone)
		logger.Info("day-ahead price job started", "zone", job.Zone, "interval", job.Interval.String())
		job.Run(jobCtx)
	}()
	ready.Store(true)

	select {
	case err := <-errCh:
		stopJob()
		<-jobDone
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	ready.Store(false)
	<-jobDone

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
