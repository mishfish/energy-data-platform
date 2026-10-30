// Command fakeentsoe serves synthetic ENTSO-E Transparency Platform responses for local development
// and demos (ADR 0021). It answers day-ahead prices (A44), realised total load (A65/A16) and
// generation per production type (A75/A16) for any window, publishes and revises values over time
// like the real platform, and can inject failures through /admin/fault. The values are generated,
// never real market data, but each zone follows a profile calibrated from real data (ADR 0034):
// `fakeentsoe calibrate` derives the profiles from the ClickHouse of the main stack.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "calibrate" {
		if err := calibrate(ctx, os.Args[2:], os.Getenv); err != nil {
			logger.Error("calibrate failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(ctx, logger); err != nil {
		logger.Error("fakeentsoe exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.model.profiles, err = parseProfiles(embeddedProfiles); err != nil {
		return err
	}
	s := &server{model: cfg.model, now: time.Now, logger: logger}
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("fake ENTSO-E API listening", "addr", cfg.addr,
			"revision_interval", cfg.model.revisionInterval.String(), "revision_rate", cfg.model.revisionRate,
			"zones", len(cfg.model.profiles.Zones))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}
