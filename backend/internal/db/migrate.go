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
		if err := s.CheckSchemaCompatibility(ctx); err != nil {
			return fmt.Errorf("refusing to migrate: %w", err)
		}
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

// CheckSchemaCompatibility verifies the database's applied version does not
// exceed the migration set compiled into THIS binary (SQL + registered Go
// migrations, via the goose provider's authoritative target version). Note:
// scanning the embedded FS is NOT sufficient — go:embed directory patterns
// exclude .go files, so Go migrations would be invisible to it (review
// round 3, R3-P1-01).
func (s *Store) CheckSchemaCompatibility(ctx context.Context) error {
	provider, err := s.migrationProvider()
	if err != nil {
		return err
	}
	current, target, err := provider.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("read schema versions: %w", err)
	}
	if current > target {
		return fmt.Errorf("database schema (v%d) is newer than this binary supports (v%d); upgrade the binary first", current, target)
	}
	return nil
}

// EnsureFreshSchemaForCLI verifies the schema is migrated AND not newer than
// this binary understands before a CLI utility performs business writes. CLI
// stores never migrate themselves (P1-06); an unfinished upgrade or a
// future-schema database must not be written (review round 2, P1-06).
func (s *Store) EnsureFreshSchemaForCLI(ctx context.Context) error {
	ready, err := s.SchemaReady()
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("database schema is not initialized yet; start the server first and retry")
	}
	return s.CheckSchemaCompatibility(ctx)
}
