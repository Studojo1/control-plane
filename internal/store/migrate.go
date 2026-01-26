package store

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs all pending migrations. dbURL must be a postgres DSN (e.g. postgres://user:pass@host:5432/db?sslmode=disable).
func Migrate(dbURL string) error {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	src, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
