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

// dbOpenForCLIWithOverlay opens a CLI store whose migration set contains a
// pending extra migration (simulating an upgrade in progress).
func dbOpenForCLIWithOverlay(t *testing.T, dir string) (*Store, error) {
	t.Helper()
	s, err := OpenForCLI(dir)
	if err != nil {
		return nil, err
	}
	s.migrations = legacyMigrations{
		"0001_auth.sql":         &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0009_test_pending.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n\n-- +goose Down\nSELECT 1;\n")},
	}
	return s, nil
}

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

// TestCLIReloadsPendingSchema (review round 4, P1-06): a database with
// PENDING migrations (the owning server died mid-upgrade) must be refused for
// CLI writes even though its version is not newer.
func TestCLIReloadsPendingSchema(t *testing.T) {
	// Build a legacy v1 database, then attach with a migrations FS that also
	// contains a pending v2: the CLI must refuse to operate.
	oldSQL := readEmbedded(t, "0001_auth.sql")
	dir := t.TempDir()
	buildLegacyDB(t, dir, legacyMigrations{"0001_auth.sql": &fstest.MapFile{Data: []byte(oldSQL)}})

	s, err := dbOpenForCLIWithOverlay(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.EnsureFreshSchemaForCLI(context.Background())
	if err == nil {
		t.Fatal("pending migrations must block CLI writes")
	}
}

// original0004 is the 0004_backup_kernel.sql exactly as shipped at 13a26c1,
// BEFORE duration_secs was retro-edited into its Up section (a violation of
// the historical-migration convention this test guards against). Databases
// built from it migrate to the current schema via the conditional 0013
// repair, which must leave duration_secs present and usable by stats.
const original0004 = `-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE databases (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL UNIQUE,
    platform       TEXT    NOT NULL DEFAULT 'generic',
    env_tag        TEXT    NOT NULL DEFAULT '',
    conn_encrypted TEXT    NOT NULL,
    server_version TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE jobs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    database_id    INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    status         TEXT    NOT NULL
                   CHECK (status IN ('pending','running','succeeded','failed','canceled','interrupted')),
    attempt        INTEGER NOT NULL DEFAULT 1,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    error_class    TEXT    NOT NULL DEFAULT '',
    error_message  TEXT    NOT NULL DEFAULT '',
    artifact_path  TEXT    NOT NULL DEFAULT '',
    artifact_sha256 TEXT   NOT NULL DEFAULT '',
    artifact_size  INTEGER NOT NULL DEFAULT 0,
    manifest_path  TEXT    NOT NULL DEFAULT '',
    scheduled_at   INTEGER NOT NULL,
    started_at     INTEGER,
    finished_at    INTEGER,
    created_at     INTEGER NOT NULL
);

CREATE INDEX idx_jobs_database_created ON jobs(database_id, created_at DESC);
CREATE INDEX idx_jobs_status ON jobs(status) WHERE status IN ('pending','running');

-- +goose Down
DROP INDEX idx_jobs_status;
DROP INDEX idx_jobs_database_created;
DROP TABLE jobs;
DROP TABLE databases;
DROP TABLE settings;
`

// TestUpgradeFromOriginal0004 (review round-2 migration audit): databases
// built by the ORIGINAL 0004 (without duration_secs) upgrade to the current
// schema and END UP WITH duration_secs — the conditional 0013 repair closes
// the "no such column" failure stats queries hit on legacy upgrades.
func TestUpgradeFromOriginal0004(t *testing.T) {
	dir := t.TempDir()
	legacy := legacyMigrations{
		"0001_auth.sql":          &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0004_backup_kernel.sql": &fstest.MapFile{Data: []byte(original0004)},
	}
	buildLegacyDB(t, dir, legacy)

	// Fixture sanity: the legacy jobs table must NOT have duration_secs.
	ro, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := ro.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'duration_secs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	ro.Close()
	if n != 0 {
		t.Fatal("fixture unexpectedly has duration_secs — the fixture is not the pre-edit 0004")
	}

	// Upgrade with the CURRENT migration set.
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	if err := store.DB.QueryRow(
		`SELECT COALESCE(MAX(duration_secs), 0) FROM jobs`).Err(); err != nil {
		t.Fatalf("duration_secs missing after legacy upgrade: %v", err)
	}
}

// TestUpgradeWebhookVocabulary (phase-7 round-1 review P1-02): databases
// with the Phase-4 subscription vocabulary ("failure,expired") upgrade to
// the new event names, so existing receivers keep receiving events after
// the outbox switchover — the empty-targets path must never mask this.
func TestUpgradeWebhookVocabulary(t *testing.T) {
	dir := t.TempDir()
	legacy := legacyMigrations{
		"0001_auth.sql":          &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0004_backup_kernel.sql": &fstest.MapFile{Data: []byte(original0004)},
		"0009_scheduling.sql":    &fstest.MapFile{Data: []byte(readEmbedded(t, "0009_scheduling.sql"))},
	}
	buildLegacyDB(t, dir, legacy)
	// A Phase-4-era webhook row with the OLD default vocabulary.
	ro, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec(
		`INSERT INTO webhooks (name, url, events, created_at) VALUES ('ops', 'https://old.example/hook', 'failure,expired', 0)`); err != nil {
		t.Fatal(err)
	}
	ro.Close()

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate legacy webhook row: %v", err)
	}
	var events string
	if err := store.DB.QueryRow(`SELECT events FROM webhooks WHERE name = 'ops'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != "backup_failed,backup_expired" {
		t.Fatalf("legacy vocabulary not converted: %q", events)
	}
}

// TestUpgradeDuplicateWebhookNames (phase-7 round-1 review P2-04): legacy
// duplicate LIVE names are deterministically renamed (never deleted) so the
// unique index can land and the upgrade is not blocked.
func TestUpgradeDuplicateWebhookNames(t *testing.T) {
	dir := t.TempDir()
	legacy := legacyMigrations{
		"0001_auth.sql":          &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0004_backup_kernel.sql": &fstest.MapFile{Data: []byte(original0004)},
		"0009_scheduling.sql":    &fstest.MapFile{Data: []byte(readEmbedded(t, "0009_scheduling.sql"))},
	}
	buildLegacyDB(t, dir, legacy)
	ro, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "supabackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ro.Exec(
			`INSERT INTO webhooks (name, url, events, created_at) VALUES ('dup', 'https://old.example/h', 'failure', 0)`); err != nil {
			t.Fatal(err)
		}
	}
	ro.Close()

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate with duplicate live names: %v", err)
	}
	var n int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("live webhook rows = %d, want 3 (renamed, never deleted)", n)
	}
	var distinct int
	if err := store.DB.QueryRow(`SELECT COUNT(DISTINCT name) FROM webhooks WHERE deleted_at IS NULL`).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 3 {
		t.Fatalf("distinct names = %d, want 3", distinct)
	}
}
