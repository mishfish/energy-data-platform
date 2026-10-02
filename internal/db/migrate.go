// Package db holds the Postgres schema and the helpers that apply it.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations to the database at dsn.
// A Postgres advisory lock serialises concurrent runs, so starting several migrators at once is safe.
func Migrate(ctx context.Context, logger *slog.Logger, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("migrations fs: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
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
