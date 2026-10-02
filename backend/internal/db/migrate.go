package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

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
		backupName, err := s.backupBeforeMigrate(ctx)
		if err != nil {
			return err
		}
		if _, err := provider.Up(ctx); err != nil {
			// Failed upgrade: the pre-migration snapshot is preserved under
			// its versioned name and pruning does NOT run, so repeated failed
			// attempts can never discard the historical recovery point
			// (review round 2, P1-08 remainder).
			return fmt.Errorf("migrate (rollback snapshot %s kept): %w", backupName, err)
		}
		// Prune only after a successful upgrade.
		s.prunePreMigrateBackups()
		return nil
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

// EnsureFreshSchemaForCLI verifies the schema is migrated AND not newer than
// this binary understands before a CLI utility performs business writes. CLI
// stores never migrate themselves (P1-06); a future-schema database must not
// be written by an older binary (review round 2, P1-06 remainder).
func (s *Store) EnsureFreshSchemaForCLI(ctx context.Context) error {
	ready, err := s.SchemaReady()
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("database schema is not initialized yet; start the server first and retry")
	}
	var dbVersion int64
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&dbVersion); err != nil {
		return err
	}
	if max := MaxEmbeddedMigrationVersion(); dbVersion > max {
		return fmt.Errorf("database schema (v%d) is newer than this binary supports (v%d); upgrade the binary before running CLI commands", dbVersion, max)
	}
	return nil
}

// MaxEmbeddedMigrationVersion reports the highest migration version compiled
// into this binary, from the embedded files and registered Go migrations.
func MaxEmbeddedMigrationVersion() int64 {
	var max int64
	fs.WalkDir(embeddedMigrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		var v int64
		if _, err := fmt.Sscanf(filepath.Base(path), "%d_", &v); err == nil && v > max {
			max = v
		}
		return nil
	})
	return max
}

// SchemaVersionCompatible reports whether the applied schema does not exceed
// this binary's embedded migrations. Used by serve as a startup guard.
func (s *Store) SchemaVersionCompatible(ctx context.Context) error {
	var dbVersion int64
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&dbVersion); err != nil {
		return err
	}
	if max := MaxEmbeddedMigrationVersion(); dbVersion > max {
		return fmt.Errorf("database schema (v%d) is newer than this binary (v%d); refusing to start with a newer database", dbVersion, max)
	}
	return nil
}
