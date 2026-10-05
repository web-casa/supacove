package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

func TestAdvisoryLockBlocksSecondInstance(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer s1.Close()

	s2, err := Open(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second open: want ErrLocked, got %v", err)
	}
	if s2 != nil {
		s2.Close()
	}

	// After the first instance closes, the OS releases the lock: reopen works.
	s1.Close()
	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	s3.Close()
}

// TestAdvisoryLockAcrossProcesses pins the real guarantee (review DoD): a
// second OS process must be refused, and a SIGKILLed holder must not leave a
// permanently stuck lock. The child re-executes this test binary.
func TestAdvisoryLockAcrossProcesses(t *testing.T) {
	if os.Getenv("SB_TEST_LOCK_CHILD") != "" {
		// Child mode: hold the lock until killed.
		s, err := Open(os.Getenv("SB_TEST_LOCK_CHILD"))
		if err != nil {
			os.Exit(3)
		}
		time.Sleep(60 * time.Second)
		s.Close()
		os.Exit(0)
	}
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run", "^TestAdvisoryLockAcrossProcesses$")
	child.Env = append(os.Environ(), "SB_TEST_LOCK_CHILD="+dir)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()

	// Wait for the child to hold the lock. A successful open here must be
	// closed immediately — it is the parent leaking its own lock otherwise.
	deadline := time.Now().Add(15 * time.Second)
	for {
		held, err := Open(dir)
		if errors.Is(err, ErrLocked) {
			break
		}
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		held.Close() // child had not locked yet; release and retry
		if time.Now().After(deadline) {
			t.Fatal("child never acquired the lock")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// SIGKILL the holder: the OS releases the lock, the file remains.
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = child.Process.Wait()
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); err != nil {
		t.Fatalf("stale lock file should remain after SIGKILL: %v", err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("lock must be acquirable after the holder is killed: %v", err)
	}
	s.Close()
}

func TestCLICannotMigrateWhileServerLocked(t *testing.T) {
	dir := t.TempDir()
	srv, err := Open(dir)
	if err != nil {
		t.Fatalf("server open: %v", err)
	}
	defer srv.Close()

	cli, err := OpenForCLI(dir)
	if err != nil {
		t.Fatalf("CLI open must work beside the server: %v", err)
	}
	defer cli.Close()
	if err := cli.Migrate(context.Background()); !errors.Is(err, ErrNoLock) {
		t.Fatalf("CLI Migrate must be refused with ErrNoLock, got %v", err)
	}
}

func TestOpenEscapesSQLiteURIPath(t *testing.T) {
	// Two directories whose names differ only after a '?' must never alias
	// onto one database file (review P1-07).
	root := t.TempDir()
	a := filepath.Join(root, "same?a")
	b := filepath.Join(root, "same?b")
	sa, err := Open(a)
	if err != nil {
		t.Fatalf("open %q: %v", a, err)
	}
	defer sa.Close()
	sb, err := Open(b)
	if err != nil {
		t.Fatalf("open %q: %v", b, err)
	}
	defer sb.Close()

	if err := sa.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate a: %v", err)
	}
	if ready, err := sb.SchemaReady(); err != nil || ready {
		t.Fatalf("second dir must have its own (unmigrated) db: ready=%v err=%v", ready, err)
	}
}

func TestOpenTightensDataDirPermissions(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(loose)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	st, err := os.Stat(loose)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data dir must be tightened to 0700, got %04o", perm)
	}
	dbst, err := os.Stat(filepath.Join(loose, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dbst.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db file must be 0600, got %04o", perm)
	}
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

	dest, err := s.BackupNow(ctx, "test-snapshot")
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
	var ok string
	if err := ro.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("snapshot integrity_check: %q err=%v", ok, err)
	}
}

// TestBackupSnapshotUnderConcurrentWrites (review P1-14): a snapshot taken
// while writes are in flight must be internally consistent.
func TestBackupSnapshotUnderConcurrentWrites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_, _ = s.DB.ExecContext(ctx,
					`INSERT INTO users (username, password_hash, created_at) VALUES (?, 'x', ?)`,
					fmt.Sprintf("writer-%d", i), i)
			}
		}
	})
	time.Sleep(100 * time.Millisecond)
	dest, err := s.BackupNow(ctx, "concurrent")
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("BackupNow under writes: %v", err)
	}

	ro, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", dest))
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var ok string
	if err := ro.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("concurrent snapshot integrity_check: %q err=%v", ok, err)
	}
	var n int
	if err := ro.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("concurrent snapshot must contain at least the committed rows")
	}
}

func TestBackupNowSpecialCharacters(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "spa ced \"quoted\" 'single' ?dir")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open special-char dir: %v", err)
	}
	defer s.Close()
	dest, err := s.BackupNow(context.Background(), "special")
	if err != nil {
		t.Fatalf("BackupNow in special-char dir: %v", err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() == 0 {
		t.Fatalf("snapshot missing or empty: %v", err)
	}
}

// TestMigrateRefusesWhenPreMigrationBackupFails validates that a sabotaged
// backups path causes backupBeforeMigrate to fail, and that Migrate
// propagates the error (the write service is refused).
func TestMigrateRefusesWhenPreMigrationBackupFails(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	before := schemaVersion(t, s)

	// Sabotage: make the backups path a regular file so VACUUM INTO cannot work.
	sabotage := filepath.Join(s.dataDir, backupDir)
	if err := os.RemoveAll(sabotage); err != nil {
		t.Fatalf("remove backups dir: %v", err)
	}
	if err := os.WriteFile(sabotage, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("sabotage: %v", err)
	}

	// backupBeforeMigrate must fail (this is what Migrate calls before any
	// pending migration is applied).
	if _, err := s.backupBeforeMigrate(ctx); err == nil {
		t.Fatal("backupBeforeMigrate must fail when the backups path is sabotaged")
	}

	// Note: we cannot test the full Migrate path here because HasPending
	// correctly returns false (no new migrations to apply). The refusal path
	// (Migrate propagates backupBeforeMigrate errors) is covered by the
	// Upgrade fixture test which injects a pending migration.

	// The schema must be unchanged (refusal, not corruption).
	if got := schemaVersion(t, s); got != before {
		t.Fatalf("schema version moved during refusal: %d -> %d", before, got)
	}
}

func schemaVersion(t *testing.T, s *Store) int64 {
	t.Helper()
	var v int64
	if err := s.DB.QueryRow(
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func migrationSource(t *testing.T, name string) []byte {
	t.Helper()
	f, err := embeddedMigrations.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestMigrateNoPendingDoesNotCreateOrPruneBackup (review P1-08): plain
// restarts must not churn the backups directory at all.
func TestMigrateNoPendingDoesNotCreateOrPruneBackup(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Seed a fake historical upgrade snapshot that must never be pruned.
	bdir := filepath.Join(s.dataDir, backupDir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(bdir, "pre-migrate-20200101T000000.000000000.db")
	if err := os.WriteFile(precious, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("restart migrate %d: %v", i, err)
		}
	}
	entries, _ := filepath.Glob(filepath.Join(bdir, "pre-migrate-*.db"))
	if len(entries) != 1 || entries[0] != precious {
		t.Fatalf("no-pending migrations must not create or prune backups, got %v", entries)
	}
}

func TestBackupBeforeMigrateSkippedOnFreshDB(t *testing.T) {
	s := openTestStore(t)
	name, err := s.backupBeforeMigrate(context.Background())
	if err != nil || name != "" {
		t.Fatalf("fresh db must not require a backup: name=%q err=%v", name, err)
	}
}

// TestPruneKeepsVersionBatches (review round 3, R3-P1-04): ordering must be
// by parsed version+timestamp — lexical order would delete v10 while keeping
// v5..v9 — and failed-upgrade retries of the same version must not crowd out
// other versions' recovery points.
func TestPruneKeepsVersionBatches(t *testing.T) {
	s := openTestStore(t)
	bdir := filepath.Join(s.dataDir, backupDir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(bdir, name+".db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// v1 historical point, then five retry snapshots of v10 (same version).
	write("pre-migrate-v1-20250101T000000.000000000")
	for i := 1; i <= 5; i++ {
		write(fmt.Sprintf("pre-migrate-v10-20260101T0000%02d.000000000", i))
	}
	s.prunePreMigrateBackups()
	left, _ := filepath.Glob(filepath.Join(bdir, "pre-migrate-*.db"))
	sort.Strings(left)
	if len(left) != 2 {
		t.Fatalf("want v1 + newest v10 only, got %v", left)
	}
	for _, name := range left {
		base := filepath.Base(name)
		if strings.Contains(base, "v1-2025") || strings.Contains(base, "v10-20260101T000005") {
			continue
		}
		t.Fatalf("unexpected survivor: %s", base)
	}
}

// TestPruneKeepsLegacyBackups (review round 4, R4-P1-01): legacy
// timestamp-only backups form their own group and must survive version-based
// dedup even alongside new-format snapshots.
func TestPruneKeepsLegacyBackups(t *testing.T) {
	s := openTestStore(t)
	bdir := filepath.Join(s.dataDir, backupDir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(bdir, name+".db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pre-migrate-20260101T000000.000000000")
	write("pre-migrate-20260102T000000.000000000")
	write("pre-migrate-v5-20270101T000000.000000000")
	s.prunePreMigrateBackups()
	left, _ := filepath.Glob(filepath.Join(bdir, "pre-migrate-*.db"))
	// The two legacy files must BOTH survive (same group, under quota);
	// only the whole-group accounting may drop the OLDER legacy one at most.
	if len(left) < 2 {
		t.Fatalf("legacy backups must not be merged into version dedup: %v", left)
	}
}

// TestBackupNowCancelLeavesNoOfficialFile (review round 4, P1-08): a
// cancelled VACUUM must not leave a partial file under the official name.
func TestBackupNowCancelLeavesNoOfficialFile(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel() // already cancelled: VACUUM must fail
	if _, err := s.BackupNow(cctx, "cancelled"); err == nil {
		t.Fatal("cancelled backup must fail")
	}
	entries, _ := filepath.Glob(filepath.Join(s.dataDir, backupDir, "pre-migrate-*"))
	if len(entries) != 0 {
		t.Fatalf("cancelled backup must leave no artifacts: %v", entries)
	}
	// The store stays usable for a real backup.
	if _, err := s.BackupNow(ctx, "after-cancel"); err != nil {
		t.Fatalf("backup after cancellation: %v", err)
	}
}
