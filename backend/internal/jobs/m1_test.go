package jobs

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/crypto"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

// TestMain stops the shared PostgreSQL container after all tests.
func TestMain(m *testing.M) {
	code := m.Run()
	if pgContainer != "" {
		_ = exec.Command("docker", "rm", "-f", pgContainer).Run()
	}
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Real-PostgreSQL integration harness. These tests drive docker directly;
// they SKIP when docker or the pg_dump client binary is unavailable (the CI
// backend job runs them wherever docker exists; unit coverage elsewhere is
// socket-free).
// ---------------------------------------------------------------------------

const testPW = "CANARY-integration-P@ssw0rd"

var pgOnce sync.Once
var pgAddr string // host:port reachable from the host
var pgContainer string
var pgErr error

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_restore client not available")
	}
}

// startTestPostgres launches a throwaway PostgreSQL 18 and seeds one table.
func startTestPostgres(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		name := fmt.Sprintf("sb-test-pg-%d", time.Now().UnixNano()%1e9)
		cmd := exec.Command("docker", "run", "-d", "--name", name,
			"-e", "POSTGRES_PASSWORD="+testPW, "-e", "POSTGRES_DB=appdb",
			"-p", "127.0.0.1::5432", "postgres:18-alpine")
		out, err := cmd.Output()
		if err != nil {
			pgAddr = ""
			pgErr = fmt.Errorf("docker run postgres: %v: %s", err, out)
			return
		}
		pgContainer = name
		// Discover the mapped port.
		portOut, err := exec.Command("docker", "port", name, "5432").Output()
		if err != nil {
			pgErr = fmt.Errorf("docker port: %v", err)
			return
		}
		parts := strings.Split(strings.TrimSpace(string(portOut)), ":")
		pgAddr = "127.0.0.1:" + parts[len(parts)-1]
	})
	if pgErr != nil {
		t.Skipf("postgres container unavailable: %v", pgErr)
	}
	if pgAddr == "" {
		t.Skip("postgres container unavailable")
	}
	// The container lives for the whole test binary (TestMain stops it);
	// per-test cleanup would break sibling tests through the sync.Once.
	// Wait for readiness.
	uri := fmt.Sprintf("postgres://postgres:%s@%s/appdb?sslmode=disable", testPW, pgAddr)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := pgclient.Test(context.Background(), mustConn(t, uri)); err == nil {
			return uri
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("postgres never became ready")
	return ""
}

func mustConn(t *testing.T, uri string) *pgclient.ConnInfo {
	t.Helper()
	ci, err := pgclient.ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	return ci
}

// seed inserts known rows so the M1 restore comparison has data to verify.
func seedTestTable(t *testing.T, uri string, rows int) {
	t.Helper()
	dockerID := pgContainer
	sql := fmt.Sprintf(`
		DROP TABLE IF EXISTS m1test;
		CREATE TABLE m1test(id int primary key, payload text);
		INSERT INTO m1test SELECT g, md5(g::text) FROM generate_series(1,%d) g;`, rows)
	if out, err := exec.Command("docker", "exec", dockerID, "psql", "-U", "postgres", "-d", "appdb",
		"-c", sql).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
}

func countRows(t *testing.T, uri string) int {
	t.Helper()
	ci := mustConn(t, uri)
	if _, err := pgclient.Test(context.Background(), ci); err != nil {
		t.Fatalf("connect for count: %v", err)
	}
	// The TARGET database name comes from the URI — never a hardcoded source
	// db (round-2 review P1-14: the previous version verified the SOURCE).
	out, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-d", ci.DBName,
		"-At", "-c", "SELECT count(*) FROM m1test").CombinedOutput()
	if err != nil {
		t.Fatalf("count: %v: %s", err, out)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
		t.Fatalf("parse count %q: %v", out, err)
	}
	return n
}

// verifyRestoredContent checks actual payload values in the restored db
// (round-2 review P1-14: count alone is a false positive).
func verifyRestoredContent(t *testing.T, verifyURI string) {
	t.Helper()
	out, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-d", mustConn(t, verifyURI).DBName,
		"-At", "-c",
		"SELECT id, payload FROM m1test WHERE id IN (1, 250, 500) ORDER BY id").CombinedOutput()
	if err != nil {
		t.Fatalf("content probe: %v: %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("content probe returned %d lines, want 3", len(lines))
	}
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			t.Fatalf("malformed probe row: %q", line)
		}
		var id int
		fmt.Sscanf(parts[0], "%d", &id)
		want := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%d", id))))
		if parts[1] != want {
			t.Fatalf("content mismatch at id %d: got %q want %q", id, parts[1], want)
		}
	}
}

// TestM1_FullKernelChain is the M1 gate: register → backup → take ONLY the
// ciphertext + offline identity → restore into a fresh database → data
// matches. The application container and its SQLite are NOT involved in the
// restore half.
func TestM1_FullKernelChain(t *testing.T) {
	requireDocker(t)
	uri := startTestPostgres(t)
	seedTestTable(t, uri, 500)
	wantRows := 500

	// --- the application side ---
	dataDir := t.TempDir()
	cfg := &config.Config{
		DataDir: dataDir, SecretFile: filepath.Join(dataDir, "secret.key"),
		SessionTTL: time.Hour, BootstrapTokenTTL: time.Minute, InsecureCookie: true,
	}
	key, err := config.LoadOrCreateSecret(cfg.SecretFile)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	// age keypair: the identity is shown once and saved "offline" by the test.
	identity, recipient, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(
		`INSERT INTO settings (key, value) VALUES ('age_recipient', ?), ('age_key_id', ?)`,
		recipient, agekey.Fingerprint(recipient)); err != nil {
		t.Fatal(err)
	}

	enc, err := encryptForTest(key, uri)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, server_version, created_at, updated_at)
		 VALUES ('m1-db', 'generic', '', ?, '', strftime('%s','now'), strftime('%s','now'))`, enc); err != nil {
		t.Fatal(err)
	}
	var dbID int64
	if err := store.DB.QueryRow(`SELECT id FROM databases WHERE name='m1-db'`).Scan(&dbID); err != nil {
		t.Fatal(err)
	}

	stagingDir := filepath.Join(dataDir, "staging")
	runner := NewRunner(store, key, stagingDir, func(ctx context.Context) (string, error) {
		var r string
		_ = store.DB.QueryRow(`SELECT value FROM settings WHERE key='age_recipient'`).Scan(&r)
		return r, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if n, err := runner.RecoverInterrupted(context.Background()); err != nil || n != 0 {
		t.Fatalf("recover: %d %v", n, err)
	}
	runner.Start(context.Background())
	defer runner.Stop()

	jobID, err := runner.Enqueue(context.Background(), dbID)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for success.
	deadline := time.Now().Add(90 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		tk, err := GetTask(store.DB, jobID)
		if err != nil {
			t.Fatal(err)
		}
		status = tk.Status
		if status == StatusSucceeded || status == StatusFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status != StatusSucceeded {
		tk, _ := GetTask(store.DB, jobID)
		t.Fatalf("job status = %s, want succeeded (class=%q msg=%q)",
			status, taskStr(tk, "errorClass"), taskStr(tk, "errorMessage"))
	}
	tk, _ := GetTask(store.DB, jobID)
	if tk.ArtifactSHA256 == "" || tk.ArtifactSize == 0 || !tk.HasManifest {
		t.Fatalf("success record incomplete: %+v", tk)
	}

	// --- the restore half: NO supabackup, ONLY artifact + offline identity + PG tools ---
	artifact, err := os.ReadFile(filepath.Join(stagingDir, fmt.Sprintf("backup-job%d.dump.age", jobID)))
	if err != nil {
		t.Fatal(err)
	}
	var plainDump strings.Builder
	if err := agekey.DecryptStream(identity, strings.NewReader(string(artifact)), &plainDump); err != nil {
		t.Fatalf("decrypt with the OFFLINE identity: %v", err)
	}
	dumpPath := filepath.Join(t.TempDir(), "restored.dump")
	if err := os.WriteFile(dumpPath, []byte(plainDump.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("decrypt: ciphertext=%d bytes, plaintext=%d bytes", len(artifact), len(plainDump.String()))

	// Create a fresh database and restore the plaintext dump into it.
	if out, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-c", "DROP DATABASE IF EXISTS verifydb").CombinedOutput(); err != nil {
		t.Fatalf("drop verifydb: %v: %s", err, out)
	}
	if out, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-c", "CREATE DATABASE verifydb").CombinedOutput(); err != nil {
		t.Fatalf("create verifydb: %v: %s", err, out)
	}
	if out, err := exec.Command("docker", "cp", dumpPath, pgContainer+":/tmp/restored.dump").CombinedOutput(); err != nil {
		t.Fatalf("docker cp: %v: %s", err, out)
	}
	if out, err := exec.Command("docker", "exec", pgContainer, "sh", "-c",
		"pg_restore -U postgres --exit-on-error --no-owner -d verifydb /tmp/restored.dump").CombinedOutput(); err != nil {
		t.Fatalf("pg_restore: %v: %s", err, out)
	}

	verifyURI := strings.Replace(uri, "/appdb", "/verifydb", 1)
	gotRows := countRows(t, verifyURI)
	if gotRows != wantRows {
		t.Fatalf("restored rows = %d, want %d", gotRows, wantRows)
	}
	verifyRestoredContent(t, verifyURI)

	// Manifest sanity (protocol E): key ID, versions, no secrets.
	mb, err := os.ReadFile(filepath.Join(stagingDir, fmt.Sprintf("backup-job%d.dump.age.manifest.json", jobID)))
	if err != nil {
		t.Fatal(err)
	}
	mtxt := string(mb)
	for _, want := range []string{`"formatVersion": 1`, `"type": "age"`, `"mode": "full"`, agekey.Fingerprint(recipient)} {
		if !strings.Contains(mtxt, want) {
			t.Fatalf("manifest missing %s", want)
		}
	}
	if strings.Contains(mtxt, testPW) {
		t.Fatal("manifest leaked the password")
	}
}

// TestInterruptedRecoveryAtStartup: a crash mid-run is recorded as
// interrupted at startup, never as success, and the worker is usable after.
func TestInterruptedRecoveryAtStartup(t *testing.T) {
	requireDocker(t)
	uri := startTestPostgres(t)
	dataDir := t.TempDir()
	key, err := config.LoadOrCreateSecret(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, rcp, _ := agekey.Generate()
	enc, _ := encryptForTest(key, uri)
	if _, err := store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('db1','generic','',?,'0','0')`, enc); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		VALUES ((SELECT id FROM databases WHERE name='db1'), 'running', 0, 0)`); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, key, filepath.Join(dataDir, "staging"), func(ctx context.Context) (string, error) {
		return rcp, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if n, err := runner.RecoverInterrupted(context.Background()); err != nil || n != 1 {
		t.Fatalf("recover interrupted: %d %v", n, err)
	}
	tk, _ := GetTask(store.DB, 1)
	if tk.Status != StatusInterrupted {
		t.Fatalf("status after recovery = %s, want interrupted", tk.Status)
	}
}

func encryptForTest(key []byte, uri string) (string, error) {
	return crypto.Encrypt(key, []byte(uri))
}

func taskStr(t *Task, field string) string {
	if t == nil {
		return "<no task>"
	}
	switch field {
	case "errorClass":
		return t.ErrorClass
	case "errorMessage":
		return t.ErrorMessage
	}
	return ""
}

// TestConcurrentEnqueueExactlyOne (round-1 review P1-05): the partial unique
// index makes concurrent enqueues produce exactly one pending job.
func TestConcurrentEnqueueExactlyOne(t *testing.T) {
	requireDocker(t)
	uri := startTestPostgres(t)
	dataDir := t.TempDir()
	key, err := config.LoadOrCreateSecret(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	enc, _ := crypto.Encrypt(key, []byte(uri))
	if _, err := store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('cdb','generic','',?,'0','0')`, enc); err != nil {
		t.Fatal(err)
	}
	var dbID int64
	_ = store.DB.QueryRow(`SELECT id FROM databases WHERE name='cdb'`).Scan(&dbID)
	_, rcp, _ := agekey.Generate()
	runner := NewRunner(store, key, filepath.Join(dataDir, "staging"), func(ctx context.Context) (string, error) {
		return rcp, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var wg sync.WaitGroup
	okCount, queuedCount := 0, 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.Enqueue(context.Background(), dbID)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okCount++
			} else if errors.Is(err, ErrAlreadyQueued) {
				queuedCount++
			} else {
				t.Logf("unexpected enqueue error: %v", err)
			}
		}()
	}
	wg.Wait()
	if okCount != 1 || queuedCount != 7 {
		t.Fatalf("want exactly 1 enqueue + 7 rejections, got %d/%d", okCount, queuedCount)
	}
}

// TestCancelPendingJobReachable (round-1 review P1-04): `canceled` is a real
// terminal state for pending jobs.
func TestCancelPendingJobReachable(t *testing.T) {
	requireDocker(t)
	uri := startTestPostgres(t)
	dataDir := t.TempDir()
	key, err := config.LoadOrCreateSecret(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	enc, _ := crypto.Encrypt(key, []byte(uri))
	if _, err := store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('cancel-db','generic','',?,'0','0')`, enc); err != nil {
		t.Fatal(err)
	}
	var dbID int64
	_ = store.DB.QueryRow(`SELECT id FROM databases WHERE name='cancel-db'`).Scan(&dbID)
	_, rcp, _ := agekey.Generate()
	runner := NewRunner(store, key, filepath.Join(dataDir, "staging"), func(ctx context.Context) (string, error) {
		return rcp, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// NOTE: worker NOT started — the job stays pending.
	jobID, err := runner.Enqueue(context.Background(), dbID)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Cancel(jobID); err != nil {
		t.Fatal(err)
	}
	runner.settleCanceledPending()
	tk, err := GetTask(store.DB, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusCanceled {
		t.Fatalf("status = %s, want canceled", tk.Status)
	}
}
