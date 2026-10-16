// Command sink loads the CDC topics from Kafka into ClickHouse (ADR 0024–0026, 0028).
//
// Usage:
//
//	sink           run the sink
//	sink migrate   apply pending ClickHouse schema migrations and exit
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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mishfish/energy-data-platform/internal/chdb"
	"github.com/mishfish/energy-data-platform/internal/sink"
)

// Bounds the final flush on SIGTERM; compose's stop_grace_period must be longer.
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
		err = fmt.Errorf("unknown arguments %q, usage: sink [migrate]", args)
	}
	if err != nil {
		logger.Error("sink exited with error", "err", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, logger *slog.Logger) error {
	dsn := os.Getenv("CLICKHOUSE_DSN")
	if dsn == "" {
		return errors.New("CLICKHOUSE_DSN is not set")
	}
	return chdb.Migrate(ctx, logger, dsn)
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	metrics, err := sink.NewMetrics(prometheus.DefaultRegisterer)
	if err != nil {
		return err
	}
	decoder, err := sink.NewAvroDecoder(cfg.registryURL)
	if err != nil {
		return err
	}
	writer, err := sink.NewClickHouseWriter(ctx, cfg.clickhouseDSN)
	if err != nil {
		return err
	}
	defer func() { _ = writer.Close() }()

	s := &sink.Sink{
		Decoder:       decoder,
		Writer:        writer,
		Metrics:       metrics,
		Logger:        logger,
		BatchSize:     cfg.batchSize,
		FlushInterval: cfg.flushInterval,
		RetryBase:     retryBase,
		RetryMax:      retryMax,
	}
	consumer, err := sink.NewKafkaConsumer(cfg.brokers, cfg.groupID, cfg.topics, metrics, logger, s.DropBatch)
	if err != nil {
		return err
	}
	// Closed after Run returns, i.e. after the final flush and commit.
	defer func() { _ = consumer.Close() }()
	s.Consumer = consumer

	var ready atomic.Bool
	srv := &http.Server{Addr: cfg.httpAddr, Handler: newMux(&ready), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("sink started", "topics", cfg.topics, "group", cfg.groupID,
		"batch_size", cfg.batchSize, "flush_interval", cfg.flushInterval.String())
	ready.Store(true)

	// The final flush gets its own deadline that starts when the shutdown signal arrives.
	shutdownCtx, cancelShutdown := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelShutdown()
	go func() {
		<-ctx.Done()
		ready.Store(false)
		time.AfterFunc(shutdownTimeout, cancelShutdown)
	}()

	if err := s.Run(ctx, shutdownCtx); err != nil {
		return err
	}
	logger.Info("sink stopped")
	return nil
}

// newMux serves liveness, readiness and Prometheus metrics.
func newMux(ready *atomic.Bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
