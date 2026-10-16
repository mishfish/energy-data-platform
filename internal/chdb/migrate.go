// Package chdb holds the ClickHouse schema and the helper that applies it (ADR 0027).
package chdb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/ClickHouse/clickhouse-go/v2" // registers the "clickhouse" database/sql driver for goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations to the ClickHouse database at dsn
// (clickhouse://user:password@host:9000/database).
//
// Unlike the Postgres migrator there is no advisory lock: ClickHouse has none, so run a single
// migrator at a time (the one-shot compose service does).
func Migrate(ctx context.Context, logger *slog.Logger, dsn string) error {
	sqlDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectClickHouse, sqlDB, migrations)
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }() // closes sqlDB

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	for _, r := range results {
		logger.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration.String())
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("db version: %w", err)
	}
	logger.Info("schema up to date", "version", version, "applied", len(results))
	return nil
}
