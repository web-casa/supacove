package db

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	_ "github.com/cloudfan/supabackup/backend/internal/db/migrations"
)

// legacyMigrations is an fs.FS serving one historical migration file.
type legacyMigrations fstest.MapFS

func (m legacyMigrations) Open(name string) (fs.File, error) {
	return fstest.MapFS(m).Open(name)
}

func readEmbedded(t *testing.T, name string) string {
	t.Helper()
	data, err := embeddedMigrations.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// buildLegacyDB creates a database migrated ONLY by the supplied migration
// source (the historical 0001), using goose itself — exactly how the previous
// binary built it.
func buildLegacyDB(t *testing.T, dir string, legacy legacyMigrations) {
	t.Helper()
	ro, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Disable the global Go-migration registry: the OLD binary that built this
	// database did not know about migration v3, so the fixture must not run it
	// either (review round 3, R3-P2-02).
	provider, err := goose.NewProvider(goose.DialectSQLite3, ro, legacy,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("legacy goose up: %v", err)
	}
	if _, err := ro.Exec(
		`INSERT INTO users (username, password_hash, created_at) VALUES ('legacy', '$argon2id$v=19$m=65536,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', 0)`); err != nil {
		t.Fatal(err)
	}
	ro.Close()
}

// assertLegacyFixture verifies the pre-upgrade fixture is really at v1 with
// the expected column state — the tests are meaningless if the fixture was
// already migrated by newer code.
func assertLegacyFixture(t *testing.T, dir string, wantColumn bool) {
	t.Helper()
	ro, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var v int64
	if err := ro.QueryRow(
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("fixture must be at schema v1, got v%d", v)
	}
	var n int
	if err := ro.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'auth_generation'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if wantColumn && n != 1 {
		t.Fatal("fixture expected auth_generation present")
	}
	if !wantColumn && n != 0 {
		t.Fatal("fixture must NOT have auth_generation yet")
	}
}

// TestUpgradeFromOriginal0001 (review round 2, R2-P1-01): a database built by
// the ORIGINAL 0001 (as shipped at 2388a46, without auth_generation) must
// upgrade cleanly: Migrate succeeds and the column exists.
func TestUpgradeFromOriginal0001(t *testing.T) {
	oldSQL := readEmbedded(t, "0001_auth.sql")
	// The committed 0001 must be the ORIGINAL definition (no auth_generation):
	// already-applied databases are never re-run, so shape changes belong in
	// new migrations only.
	if bytes.Contains([]byte(oldSQL), []byte("auth_generation")) {
		t.Fatal("0001 must not contain auth_generation; it was already applied by older binaries")
	}
	dir := t.TempDir()
	buildLegacyDB(t, dir, legacyMigrations{"0001_auth.sql": &fstest.MapFile{Data: []byte(oldSQL)}})
	assertLegacyFixture(t, dir, false)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("upgrade migrate: %v", err)
	}
	var gen int
	if err := s.DB.QueryRow(
		`SELECT auth_generation FROM users WHERE username = 'legacy'`).Scan(&gen); err != nil {
		t.Fatalf("auth_generation missing after upgrade: %v", err)
	}
	if gen != 0 {
		t.Fatalf("generation default = %d, want 0", gen)
	}
}

// TestUpgradeFromModified0001 covers the intermediate shape: databases built
// while 0001 briefly contained auth_generation must migrate without a
// duplicate-column failure.
func TestUpgradeFromModified0001(t *testing.T) {
	current := readEmbedded(t, "0001_auth.sql")
	augmented := bytes.Replace([]byte(current),
		[]byte("    created_at    INTEGER NOT NULL"),
		[]byte("    auth_generation INTEGER NOT NULL DEFAULT 0,\n    created_at    INTEGER NOT NULL"), 1)
	dir := t.TempDir()
	buildLegacyDB(t, dir, legacyMigrations{"0001_auth.sql": &fstest.MapFile{Data: augmented}})
	assertLegacyFixture(t, dir, true)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate over already-augmented 0001: %v", err)
	}
	var gen int
	if err := s.DB.QueryRow(
		`SELECT auth_generation FROM users WHERE username = 'legacy'`).Scan(&gen); err != nil {
		t.Fatal(err)
	}
}

// The database files of both tests must remain readable/writable afterwards
// (guard against the upgrade leaving the store unusable).
func TestUpgradedStoreStillWorks(t *testing.T) {
	oldSQL := readEmbedded(t, "0001_auth.sql")
	dir := t.TempDir()
	buildLegacyDB(t, dir, legacyMigrations{"0001_auth.sql": &fstest.MapFile{Data: []byte(oldSQL)}})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "supabackup.db")); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.SchemaReady(); err != nil || !ready {
		t.Fatalf("schema ready after upgrade: %v err=%v", ready, err)
	}
}
