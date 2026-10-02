package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrate runs pending schema migrations. Only an actual upgrade takes a
// consistent pre-migration backup (plain restarts must not churn or prune the
// rollback point); if that backup fails, migration — and therefore the write
// service — is refused. Must be called on a locked (server) store.
//
// Freshness is decided BEFORE consulting goose: provider.HasPending
// initializes goose_db_version, which would otherwise erase the "never
// migrated" signal.
func (s *Store) Migrate(ctx context.Context) error {
	if s.lockFile == nil {
		return ErrNoLock
	}
	fresh, err := s.IsFresh()
	if err != nil {
		return fmt.Errorf("check schema state: %w", err)
	}
	if !fresh {
		provider, err := s.migrationProvider()
		if err != nil {
			return err
		}
		pending, err := provider.HasPending(ctx)
		if err != nil {
			return fmt.Errorf("check pending migrations: %w", err)
		}
		if !pending {
			return nil
		}
		if err := s.backupBeforeMigrate(ctx); err != nil {
			return err
		}
	}
	provider, err := s.migrationProvider()
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// migrationProvider builds a goose v3 provider from the embedded migrations.
// The instance provider avoids the package-level SetBaseFS/SetDialect global
// state (review P1-08 recommendation).
func (s *Store) migrationProvider() (*goose.Provider, error) {
	fsys, err := fs.Sub(s.migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("embed migrations: %w", err)
	}
	return goose.NewProvider(goose.DialectSQLite3, s.DB, fsys)
}

// SchemaReady reports whether at least one migration has been applied — the
// readiness signal for /api/ready. A fresh database (no version table) is not
// ready.
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

// EnsureFreshSchemaForCLI verifies the schema is migrated before a CLI utility
// performs business writes. CLI stores never migrate themselves (P1-06): an
// empty or unmigrated store means the owning server has not finished
// initializing.
func (s *Store) EnsureFreshSchemaForCLI(ctx context.Context) error {
	ready, err := s.SchemaReady()
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("database schema is not initialized yet; start the server first and retry")
	}
	return nil
}
