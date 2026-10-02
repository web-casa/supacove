package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const backupDir = "backups"
const preMigrateKeep = 5

// BackupNow writes a consistent snapshot of the store to
// <dataDir>/backups/<name>.db via VACUUM INTO (SQLite online-consistent
// copy; copying the .db file alone under WAL is not safe — dev-plan P0-08).
func (s *Store) BackupNow(ctx context.Context, name string) (string, error) {
	// The name is generated internally; refuse anything that is not a plain
	// basename so it can never escape the backups directory.
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid backup name %q", name)
	}
	dir := filepath.Join(s.dataDir, backupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	dest := filepath.Join(dir, name+".db")
	// VACUUM INTO fails if the target exists and cannot run inside a
	// transaction; the target path is passed as a bound parameter so path
	// characters cannot alter the statement (review P1-07).
	if _, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return "", fmt.Errorf("vacuum into backup snapshot: %w", err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

// prunePreMigrateBackups keeps at most preMigrateKeep UPGRADE BATCHES: for
// each source version, only the newest snapshot survives, so retries of a
// failed upgrade can never crowd out the historical recovery point (review
// round 2 P1-08 remainder and round 3 R3-P1-04). Ordering is by the parsed
// version and timestamp, never by filename lexicographic order (v10 sorts
// before v5 lexically).
func (s *Store) prunePreMigrateBackups() {
	entries, err := filepath.Glob(filepath.Join(s.dataDir, backupDir, "pre-migrate-*.db"))
	if err != nil {
		return
	}
	type snapshot struct {
		path      string
		version   int64
		timestamp string
	}
	var snaps []snapshot
	for _, e := range entries {
		base := strings.TrimSuffix(filepath.Base(e), ".db")
		var v int64
		var ts string
		if _, err := fmt.Sscanf(base, "pre-migrate-v%d-%s", &v, &ts); err != nil {
			// Unrecognized name (legacy timestamp-only format): keep it —
			// never delete what we cannot classify.
			snaps = append(snaps, snapshot{path: e, version: -1, timestamp: "9999"})
			continue
		}
		snaps = append(snaps, snapshot{path: e, version: v, timestamp: ts})
	}
	// Newest batch per version.
	newestByVersion := map[int64]snapshot{}
	for _, sn := range snaps {
		if cur, ok := newestByVersion[sn.version]; !ok || sn.timestamp > cur.timestamp {
			newestByVersion[sn.version] = sn
		}
	}
	unique := make([]snapshot, 0, len(newestByVersion))
	for _, sn := range newestByVersion {
		unique = append(unique, sn)
	}
	// Newest batches first (version desc, then timestamp desc).
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].version != unique[j].version {
			return unique[i].version > unique[j].version
		}
		return unique[i].timestamp > unique[j].timestamp
	})
	keep := make(map[string]bool, preMigrateKeep)
	for i, sn := range unique {
		if i < preMigrateKeep {
			keep[sn.path] = true
		}
	}
	for _, e := range entries {
		if !keep[e] {
			if rmErr := os.Remove(e); rmErr != nil {
				// Pruning failures must be observable but never block a migration.
				fmt.Fprintf(os.Stderr, "warning: removing old pre-migration backup %s: %v\n", e, rmErr)
			}
		}
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

// backupBeforeMigrate snapshots the store ahead of an actual schema upgrade.
// The snapshot name records the schema version being upgraded from, so
// recovery points remain attributable across retries (review round 2, P1-08).
// Pruning happens only after the upgrade SUCCEEDS (see Migrate). Returns the
// backup file base name.
func (s *Store) backupBeforeMigrate(ctx context.Context) (string, error) {
	// A never-migrated database has nothing to roll back to: skip the
	// snapshot and let fresh installs migrate directly (review P1-08).
	fresh, err := s.IsFresh()
	if err != nil {
		return "", fmt.Errorf("check schema state: %w", err)
	}
	if fresh {
		return "", nil
	}
	version := schemaVersionForName(ctx, s.DB)
	name := fmt.Sprintf("pre-migrate-v%d-%s", version, time.Now().UTC().Format("20060102T150405.000000000"))
	if _, err := s.BackupNow(ctx, name); err != nil {
		return "", fmt.Errorf("pre-migration backup failed, refusing to migrate: %w", err)
	}
	return name, nil
}

func schemaVersionForName(ctx context.Context, db *sql.DB) int64 {
	var v int64
	_ = db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&v)
	return v
}
