package db

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// TestWebhookVocabularyDownUpCycle (phase-7 round-2 review R2-P1-01): the
// legacy-vocabulary conversion must be re-entrant — a Down (which restores
// the OLD vocabulary so the rolled-back binary keeps matching) followed by
// another Up must NOT double-convert ('expired' inside 'backup_expired').
func TestWebhookVocabularyDownUpCycle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "supabackup.db")

	// Legacy base at v9 (0001+0004+0009), one webhook with the old words.
	legacy := legacyMigrations{
		"0001_auth.sql":          &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0004_backup_kernel.sql": &fstest.MapFile{Data: []byte(original0004)},
		"0009_scheduling.sql":    &fstest.MapFile{Data: []byte(readEmbedded(t, "0009_scheduling.sql"))},
	}
	buildLegacyDB(t, dir, legacy)
	ro, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec(
		`INSERT INTO webhooks (name, url, events, created_at) VALUES ('ops', 'https://old.example/h', 'failure,expired', 0)`); err != nil {
		t.Fatal(err)
	}
	ro.Close()

	eventsAt := func() string {
		t.Helper()
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var events string
		if err := db.QueryRow(`SELECT events FROM webhooks WHERE name='ops'`).Scan(&events); err != nil {
			t.Fatal(err)
		}
		return events
	}

	// Full current migration set: Up (via Open+Migrate).
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := eventsAt(); got != "backup_failed,backup_expired" {
		t.Fatalf("after Up: %q", got)
	}
	store.Close()

	// Down one version (14→13) with a dedicated provider over the same dir.
	fs := os.DirFS(filepath.Join(".", "migrations"))
	ro, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, ro, fs,
		goose.WithDisableGlobalRegistry(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(context.Background()); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := eventsAt(); got != "failure,expired" {
		t.Fatalf("after Down: %q, want restored legacy vocabulary", got)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	ro.Close()
	if got := eventsAt(); got != "backup_failed,backup_expired" {
		t.Fatalf("after Down→Up: %q, want single clean conversion (no backup_backup_expired)", got)
	}
}

// TestLegacyDuplicateWebhookNamesSurvive (phase-7 round-2 review P2-04):
// legacy duplicate live names are LEGAL data — the upgrade must not rename
// or delete them (uniqueness is enforced at the API layer instead).
func TestLegacyDuplicateWebhookNamesSurvive(t *testing.T) {
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
	// A row that a naive rename would collide with.
	if _, err := ro.Exec(
		`INSERT INTO webhooks (name, url, events, created_at) VALUES ('dup #2', 'https://old.example/x', 'failure', 0)`); err != nil {
		t.Fatal(err)
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
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM webhooks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("webhook rows = %d, want 4 (all preserved untouched)", n)
	}
}

// TestWebhookVocabularyAllCombinationsCycle (phase-7 round-3 closure): every
// non-empty combination of the three supported events must survive a full
// Up→Down→Up cycle with its subscription set semantically intact — backup
// tokens convert both directions; verification_failed (no old equivalent)
// passes through untouched. This pins the per-token conversion against the
// mixed-subscription gap the two-token fixtures could not expose.
func TestWebhookVocabularyAllCombinationsCycle(t *testing.T) {
	type combo struct {
		name string
		evs  []string
	}
	all := []combo{
		{"backup_failed", []string{"backup_failed"}},
		{"backup_expired", []string{"backup_expired"}},
		{"verification_failed", []string{"verification_failed"}},
		{"bf+be", []string{"backup_failed", "backup_expired"}},
		{"bf+vf", []string{"backup_failed", "verification_failed"}},
		{"be+vf", []string{"backup_expired", "verification_failed"}},
		{"all-three", []string{"backup_failed", "backup_expired", "verification_failed"}},
	}
	// legacyOf is the pre-migration subscription a combo would have upgraded
	// FROM (backup events had old names; verification_failed has no old
	// equivalent and can only exist in new-vocabulary rows).
	legacyOf := func(evs []string) string {
		var parts []string
		for _, e := range evs {
			switch e {
			case "backup_failed":
				parts = append(parts, "failure")
			case "backup_expired":
				parts = append(parts, "expired")
			default:
				parts = append(parts, e)
			}
		}
		return strings.Join(parts, ",")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "supabackup.db")

	legacy := legacyMigrations{
		"0001_auth.sql":          &fstest.MapFile{Data: []byte(readEmbedded(t, "0001_auth.sql"))},
		"0004_backup_kernel.sql": &fstest.MapFile{Data: []byte(original0004)},
		"0009_scheduling.sql":    &fstest.MapFile{Data: []byte(readEmbedded(t, "0009_scheduling.sql"))},
	}
	buildLegacyDB(t, dir, legacy)
	ro, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if _, err := ro.Exec(
			`INSERT INTO webhooks (name, url, events, created_at) VALUES (?, 'https://old.example/h', ?, 0)`,
			c.name, legacyOf(c.evs)); err != nil {
			t.Fatal(err)
		}
	}
	ro.Close()

	readAll := func() map[string]string {
		t.Helper()
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		out := map[string]string{}
		rows, err := db.Query(`SELECT name, events FROM webhooks`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, events string
			if err := rows.Scan(&name, &events); err != nil {
				t.Fatal(err)
			}
			out[name] = events
		}
		return out
	}

	// Up: every combination converts to its exact new-vocabulary set.
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterUp := readAll()
	for _, c := range all {
		if got := afterUp[c.name]; got != strings.Join(c.evs, ",") {
			t.Fatalf("after Up %s: %q, want %q", c.name, got, strings.Join(c.evs, ","))
		}
	}
	store.Close()

	// Down: backup tokens restore, verification_failed stays.
	mfs := os.DirFS(filepath.Join(".", "migrations"))
	ro, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, ro, mfs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(context.Background()); err != nil {
		t.Fatalf("down: %v", err)
	}
	afterDown := readAll()
	for _, c := range all {
		want := map[string]bool{}
		for _, e := range c.evs {
			switch e {
			case "backup_failed":
				want["failure"] = true
			case "backup_expired":
				want["expired"] = true
			case "verification_failed":
				want["verification_failed"] = true
			}
		}
		var parts []string
		for _, e := range strings.Split(afterDown[c.name], ",") {
			parts = append(parts, e)
		}
		if len(parts) != len(want) {
			t.Fatalf("after Down %s: %q, want %d tokens", c.name, afterDown[c.name], len(want))
		}
		for _, p := range parts {
			if !want[p] {
				t.Fatalf("after Down %s: unexpected token %q in %q", c.name, p, afterDown[c.name])
			}
		}
	}

	// Up again: exact restoration (idempotent, no double conversion).
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	ro.Close()
	afterReUp := readAll()
	for _, c := range all {
		if got := afterReUp[c.name]; got != strings.Join(c.evs, ",") {
			t.Fatalf("after Down→Up %s: %q, want %q", c.name, got, strings.Join(c.evs, ","))
		}
	}
}
