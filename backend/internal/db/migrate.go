package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs pending schema migrations. A consistent pre-migration backup is
// taken first; if that fails, migration (and therefore the write service) is
// refused. Run before the HTTP server starts.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.backupBeforeMigrate(); err != nil {
		return err
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, s.DB, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// SchemaReady reports whether at least one migration has been applied — the
// readiness signal for /api/ready. A fresh database (no version table) is not ready.
func (s *Store) SchemaReady() (bool, error) {
	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'`).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	var v int64
	if err := s.DB.QueryRow(
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&v); err != nil {
		return false, err
	}
	return v > 0, nil
}
