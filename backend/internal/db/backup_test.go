package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBackupNowProducesConsistentSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seed := fmt.Sprintf("u%d", time.Now().UnixNano())
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, 'x', 0)`, seed); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	dest, err := s.BackupNow("test-snapshot")
	if err != nil {
		t.Fatalf("BackupNow: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(dest), "test-snapshot") {
		t.Fatalf("unexpected backup name %s", dest)
	}

	// The snapshot must contain the seeded row: verify via a read-only handle.
	ro, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", dest))
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer ro.Close()
	var n int
	if err := ro.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, seed).Scan(&n); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if n != 1 {
		t.Fatalf("snapshot users count = %d, want 1", n)
	}
}

func TestMigrateRefusesWhenPreMigrationBackupFails(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Sabotage: make the backups path a regular file so VACUUM INTO cannot work.
	sabotage := filepath.Join(s.dataDir, backupDir)
	if err := os.RemoveAll(sabotage); err != nil {
		t.Fatalf("remove backups dir: %v", err)
	}
	if err := os.WriteFile(sabotage, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("sabotage: %v", err)
	}

	if err := s.Migrate(ctx); err == nil {
		t.Fatal("second migrate must fail when the pre-migration backup fails")
	}

	// The store must still be usable and its schema intact (refusal, not corruption).
	fresh, err := s.IsFresh()
	if err != nil || fresh {
		t.Fatalf("schema state after refused migration: fresh=%v err=%v", fresh, err)
	}
	var version int64
	if err := s.DB.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version == 0 {
		t.Fatalf("schema version after refusal: %d err=%v", version, err)
	}
}

func TestBackupBeforeMigrateSkippedOnFreshDB(t *testing.T) {
	s := openTestStore(t)
	if err := s.backupBeforeMigrate(); err != nil {
		t.Fatalf("fresh db must not require a backup: %v", err)
	}
}
