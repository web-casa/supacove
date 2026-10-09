package db

// File-name migration tests (rename plan §4.3.8): the happy path with
// WAL-resident commits, interrupted migrations, every rejected state, the
// lockless-CLI refusal, the legacy-generation lock, the aborted legacy start
// litter, fresh directories, and the rollback/re-upgrade cycle.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const (
	markerCheckpointed = "rename_probe_checkpointed"
	markerWALOnly      = "rename_probe_wal_only"
)

func setMarker(t *testing.T, dbh *sql.DB, key string) {
	t.Helper()
	if _, err := dbh.Exec(`INSERT INTO settings (key, value) VALUES (?, '1')`, key); err != nil {
		t.Fatalf("plant marker %s: %v", key, err)
	}
}

func markerPresent(t *testing.T, dbh *sql.DB, key string) bool {
	t.Helper()
	var n int
	if err := dbh.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, key).Scan(&n); err != nil {
		t.Fatalf("read marker %s: %v", key, err)
	}
	return n > 0
}

// buildLegacyNamedDB creates a fully-migrated database under the LEGACY file
// name with a checkpointed marker row — what a pre-rename binary leaves
// behind after a clean shutdown.
func buildLegacyNamedDB(t *testing.T, dir string) {
	t.Helper()
	dbh, err := sql.Open("sqlite", "file:"+filepath.Join(dir, legacyDBFileName)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer dbh.Close()
	if err := migrateFixture(t, dbh); err != nil {
		t.Fatal(err)
	}
	setMarker(t, dbh, markerCheckpointed)
}

// snapshotLegacyWithOpenWAL copies a legacy three-file set whose newest
// commit exists ONLY in the WAL into dst while the source connection is
// still open — a byte-exact simulation of a crash or SIGKILL between
// commit and checkpoint. The fixture self-certifies the split: the main
// file alone must contain the checkpointed marker and not the WAL-only one.
func snapshotLegacyWithOpenWAL(t *testing.T, dst string) {
	t.Helper()
	src := t.TempDir()
	dbh, err := sql.Open("sqlite", "file:"+filepath.Join(src, legacyDBFileName)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer dbh.Close()
	if _, err := dbh.Exec(`PRAGMA wal_autocheckpoint=0;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateFixture(t, dbh); err != nil {
		t.Fatal(err)
	}
	setMarker(t, dbh, markerCheckpointed)
	// Force the first marker into the main file, then commit the second one
	// and leave it in the WAL.
	if _, err := dbh.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		t.Fatal(err)
	}
	setMarker(t, dbh, markerWALOnly)
	for _, name := range []string{legacyDBFileName, legacyWalFileName} {
		if _, err := os.Stat(filepath.Join(src, name)); err != nil {
			t.Fatalf("fixture %s missing (WAL not resident?): %v", name, err)
		}
	}
	// Self-certification: a copy of the main file alone sees exactly the
	// checkpointed marker — proving markerWALOnly lives only in the WAL.
	scratch := t.TempDir()
	copyFixtureFile(t, filepath.Join(src, legacyDBFileName), filepath.Join(scratch, legacyDBFileName))
	mainOnly, err := sql.Open("sqlite", "file:"+filepath.Join(scratch, legacyDBFileName)+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatal(err)
	}
	if !markerPresent(t, mainOnly, markerCheckpointed) {
		mainOnly.Close()
		t.Fatal("fixture broken: checkpointed marker not in the main file")
	}
	if markerPresent(t, mainOnly, markerWALOnly) {
		mainOnly.Close()
		t.Fatal("fixture broken: WAL-only marker already checkpointed")
	}
	mainOnly.Close()

	for _, name := range []string{legacyDBFileName, legacyWalFileName, legacyShmFileName} {
		copyFixtureFile(t, filepath.Join(src, name), filepath.Join(dst, name))
	}
}

// migrateFixture brings a raw handle up to the full current schema, the way
// the production provider does.
func migrateFixture(t *testing.T, dbh *sql.DB) error {
	t.Helper()
	fsys, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, dbh, fsys, goose.WithAllowOutofOrder(true))
	if err != nil {
		return err
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return err
	}
	return nil
}

// copyFixtureFile copies src to dst (owner-only); a missing source is skipped
// so callers can copy an optional -shm.
func copyFixtureFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireNoLegacyFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{legacyDBFileName, legacyWalFileName, legacyShmFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still present after migration", name)
		}
	}
}

// TestMigrateLegacyFilesPreservesWALCommits covers the whole happy path: the
// three-file crash set (newest commit only in the WAL) migrates wholesale,
// both markers survive, legacy names disappear, and the CLI path works on
// the migrated directory afterwards.
func TestMigrateLegacyFilesPreservesWALCommits(t *testing.T) {
	dir := t.TempDir()
	snapshotLegacyWithOpenWAL(t, dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !markerPresent(t, s.DB, markerCheckpointed) || !markerPresent(t, s.DB, markerWALOnly) {
		s.Close()
		t.Fatal("markers lost across the file-name migration")
	}
	s.Close()
	requireNoLegacyFiles(t, dir)
	if _, err := os.Stat(filepath.Join(dir, dbFileName)); err != nil {
		t.Fatalf("%s missing after migration: %v", dbFileName, err)
	}

	cli, err := OpenForCLI(dir)
	if err != nil {
		t.Fatalf("cli open after migration: %v", err)
	}
	cli.Close()
}

// TestMigrateResumesAfterSidecarRename simulates crashes after the sidecars
// moved but before the main database did: the state must classify as
// resumable and the completion must apply the WAL.
func TestMigrateResumesAfterSidecarRename(t *testing.T) {
	t.Run("both sidecars moved", func(t *testing.T) {
		dir := t.TempDir()
		snapshotLegacyWithOpenWAL(t, dir)
		if err := os.Rename(filepath.Join(dir, legacyWalFileName), filepath.Join(dir, dbWalFileName)); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, legacyShmFileName), filepath.Join(dir, dbShmFileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}

		s, err := Open(dir)
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		defer s.Close()
		if !markerPresent(t, s.DB, markerWALOnly) {
			t.Fatal("WAL-resident marker lost when resuming the migration")
		}
	})

	t.Run("only the wal moved", func(t *testing.T) {
		dir := t.TempDir()
		snapshotLegacyWithOpenWAL(t, dir)
		if err := os.Rename(filepath.Join(dir, legacyWalFileName), filepath.Join(dir, dbWalFileName)); err != nil {
			t.Fatal(err)
		}

		s, err := Open(dir)
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		defer s.Close()
		if !markerPresent(t, s.DB, markerWALOnly) {
			t.Fatal("WAL-resident marker lost when resuming the migration")
		}
	})
}

// TestCLIRefusesUnmigratedDirectory pins the lockless path: no migration, no
// empty side-by-side current database — just a refusal with guidance.
func TestCLIRefusesUnmigratedDirectory(t *testing.T) {
	dir := t.TempDir()
	buildLegacyNamedDB(t, dir)

	if _, err := OpenForCLI(dir); !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("want ErrMigrationPending, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lockless CLI open must not create supacove.db")
	}
}

// TestLegacyLockHeldBlocksOpen pins the cross-generation guard: a holder of
// the legacy lock (a running supabackup binary) stops a locked open before
// anything is touched.
func TestLegacyLockHeldBlocksOpen(t *testing.T) {
	dir := t.TempDir()
	buildLegacyNamedDB(t, dir)

	lf, err := os.OpenFile(filepath.Join(dir, legacyLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck // best-effort release

	if _, err := Open(dir); !errors.Is(err, ErrLegacyInstance) {
		t.Fatalf("want ErrLegacyInstance, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyDBFileName)); err != nil {
		t.Fatalf("legacy db must be untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused open must not create supacove.db")
	}
}

// TestOpenHoldsLegacyLockForLifetime pins that a serving supacove instance
// keeps the legacy lock held, so a legacy binary cannot start beside it.
func TestOpenHoldsLegacyLockForLifetime(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	lf, err := os.OpenFile(filepath.Join(dir, legacyLockFileName), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("legacy lock must be held while the server runs, flock err=%v", err)
	}
}

func assertInconsistent(t *testing.T, err error, wantSubstrings ...string) {
	t.Helper()
	if !errors.Is(err, ErrInconsistentState) {
		t.Fatalf("want ErrInconsistentState, got %v", err)
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("error must mention %q: %v", s, err)
		}
	}
}

// TestRejectInconsistentStates walks every combination the state machine must
// refuse, including the empty-legacy-db litter an aborted legacy start
// leaves in a migrated directory.
func TestRejectInconsistentStates(t *testing.T) {
	t.Run("dual main", func(t *testing.T) {
		dir := t.TempDir()
		buildLegacyNamedDB(t, dir)
		if err := os.WriteFile(filepath.Join(dir, dbFileName), []byte("conflict"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		assertInconsistent(t, err, legacyDBFileName, dbFileName)
	})

	t.Run("aborted legacy start left empty legacy db", func(t *testing.T) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		if err := os.WriteFile(filepath.Join(dir, legacyDBFileName), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Open(dir)
		assertInconsistent(t, err, "0-byte")
	})

	t.Run("current main with legacy wal", func(t *testing.T) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		if err := os.WriteFile(filepath.Join(dir, legacyWalFileName), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Open(dir)
		assertInconsistent(t, err, legacyWalFileName)
	})

	t.Run("sidecars without a main", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, legacyWalFileName), []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		assertInconsistent(t, err, legacyWalFileName)
	})

	t.Run("same-kind sidecar duplicates", func(t *testing.T) {
		dir := t.TempDir()
		buildLegacyNamedDB(t, dir)
		// A crash mid-migration leaves the legacy WAL; then something else
		// recreates a current one: both kinds present, unresolvable.
		if err := os.WriteFile(filepath.Join(dir, legacyWalFileName), []byte("left"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dbWalFileName), []byte("dup"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		assertInconsistent(t, err, legacyWalFileName, dbWalFileName)
	})
}

// TestFreshDirUsesCurrentNames pins that a fresh install never creates
// legacy-named files.
func TestFreshDirUsesCurrentNames(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(filepath.Join(dir, dbFileName)); err != nil {
		t.Fatalf("%s missing: %v", dbFileName, err)
	}
	requireNoLegacyFiles(t, dir)
}

// TestRollbackAndReupgradeCycle pins the rollback procedure's end state: a
// stopped supacove instance whose files were renamed back must leave exactly
// what the state machine can migrate again, data intact.
func TestRollbackAndReupgradeCycle(t *testing.T) {
	dir := t.TempDir()
	snapshotLegacyWithOpenWAL(t, dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Operator rolls back to the legacy binary: stop, rename everything back.
	for _, pair := range [][2]string{
		{dbWalFileName, legacyWalFileName},
		{dbShmFileName, legacyShmFileName},
		{dbFileName, legacyDBFileName},
	} {
		if err := os.Rename(filepath.Join(dir, pair[0]), filepath.Join(dir, pair[1])); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}

	// The legacy generation must find a usable database after the rollback:
	// a pre-rename binary just opens supabackup.db by name, so a plain
	// sqlite open of that path (WAL replayed) stands in for it here.
	legacy, err := sql.Open("sqlite", "file:"+filepath.Join(dir, legacyDBFileName)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if !markerPresent(t, legacy, markerCheckpointed) || !markerPresent(t, legacy, markerWALOnly) {
		legacy.Close()
		t.Fatal("rollback must leave a legacy-named database with all data")
	}
	legacy.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-upgrade after rollback: %v", err)
	}
	defer s2.Close()
	if !markerPresent(t, s2.DB, markerCheckpointed) || !markerPresent(t, s2.DB, markerWALOnly) {
		t.Fatal("markers lost across rollback + re-upgrade")
	}
}
