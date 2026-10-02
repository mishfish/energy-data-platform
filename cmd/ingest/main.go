// Command ingest pulls ENTSO-E and Open-Meteo data into Postgres.
// For now it only runs the HTTP endpoints (health, readiness, metrics) and shuts down gracefully.
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

	"github.com/mishfish/energy-data-platform/internal/db"
)

const (
	defaultHTTPAddr = ":8080"
	shutdownTimeout = 10 * time.Second
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
	addr := os.Getenv("INGEST_HTTP_ADDR")
	if addr == "" {
		addr = defaultHTTPAddr
	}

	var ready atomic.Bool
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(&ready),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()
	ready.Store(true)

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	ready.Store(false)

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
