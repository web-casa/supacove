package db

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const backupDir = "backups"
const preMigrateKeep = 5

// BackupNow writes a consistent snapshot of the store to
// <dataDir>/backups/<name>.db via VACUUM INTO (SQLite online-consistent
// copy; copying the .db file alone under WAL is not safe — dev-plan P0-08).
func (s *Store) BackupNow(name string) (string, error) {
	dir := filepath.Join(s.dataDir, backupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	dest := filepath.Join(dir, name+".db")
	// VACUUM INTO fails if the target exists and cannot run inside a transaction;
	// the name is generated internally, so quoting is safe.
	if _, err := s.DB.Exec(fmt.Sprintf("VACUUM INTO %q", dest)); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

// prunePreMigrateBackups keeps only the newest preMigrateKeep snapshots.
func (s *Store) prunePreMigrateBackups() {
	entries, err := filepath.Glob(filepath.Join(s.dataDir, backupDir, "pre-migrate-*.db"))
	if err != nil || len(entries) <= preMigrateKeep {
		return
	}
	// Glob returns sorted names; the timestamp format keeps that chronological.
	for _, old := range entries[:len(entries)-preMigrateKeep] {
		os.Remove(old)
	}
}

// IsFresh reports whether the schema has never been migrated (no version table).
func (s *Store) IsFresh() (bool, error) {
	row := s.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'`)
	var n int
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// backupBeforeMigrate snapshots the store ahead of a schema migration unless
// the database is fresh. A failure here must abort the migration: the store
// may contain the only copy of scheduling state and key references
// (dev-plan P0-08: migrate with an automatic consistent backup, refuse write
// service when the backup fails — we fail startup entirely).
func (s *Store) backupBeforeMigrate() error {
	fresh, err := s.IsFresh()
	if err != nil {
		return fmt.Errorf("check schema state: %w", err)
	}
	if fresh {
		return nil
	}
	name := "pre-migrate-" + time.Now().UTC().Format("20060102T150405.000000000")
	if _, err := s.BackupNow(name); err != nil {
		return fmt.Errorf("pre-migration backup failed, refusing to migrate: %w", err)
	}
	s.prunePreMigrateBackups()
	return nil
}
