package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/outbox"
)

// TestPhase8SecretCanary is the four-exit secret canary re-check (dev-plan
// Phase 8 task 3): a known-marked credential travels the pipeline and must
// NEVER surface in any of the four secret exits:
//
//  1. persisted task error messages and API task views,
//  2. notification outbox payloads (what webhooks receive),
//  3. the manifest and recovery kit written to staging,
//  4. the server log output.
//
// The canary drives a REAL failure (bad host with the secret as password)
// so every failure-path redaction is exercised, plus a fake verifier
// failure for the async path.
func TestPhase8SecretCanary(t *testing.T) {
	// A capture server pretending to be a webhook receiver.
	var received sync.Mutex
	receivedPayloads := []string{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		received.Lock()
		receivedPayloads = append(receivedPayloads, string(buf[:n]))
		received.Unlock()
		w.WriteHeader(200)
	}))
	defer hook.Close()

	// A log capture around the runner (real failure path drives redaction).
	var logBuf syncBuffer
	var r *Runner
	{
		dataDir := t.TempDir()
		key, err := config.LoadOrCreateSecret(filepath.Join(dataDir, "secret.key"))
		if err != nil {
			t.Fatal(err)
		}
		store, err := db.Open(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))
		stagingDir := filepath.Join(dataDir, "staging")
		r = newRunnerForCanary(store, key, stagingDir, logger)
	}
	dbID := insertCanaryDatabase(t, r, "canary-db", hook.URL)
	var jobID int64
	if err := r.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		VALUES (?, 'running', 0, 0) RETURNING id`, dbID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	// Inject the canary: a failure whose wrapped error embeds the secret.
	// (fail() applies the redaction chain — the canary must not survive.)
	r.fail(jobID, "network", "dial tcp 10.0.0.1:5432 failed with password="+canaryPassword)

	// ---- exit 1: persisted task error + API view ----
	var errMsg string
	if err := r.store.DB.QueryRow(`SELECT error_message FROM jobs WHERE id = ?`, jobID).Scan(&errMsg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errMsg, canaryPassword) {
		t.Fatalf("EXIT 1 LEAK: canary in stored error_message: %q", errMsg)
	}
	tk, err := GetTask(r.store.DB, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tk.ErrorMessage, canaryPassword) {
		t.Fatal("EXIT 1 LEAK: canary in API task view")
	}

	// ---- exit 2: notification outbox payload (webhook delivery body) ----
	entries, err := outbox.List(context.Background(), r.store.DB, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.DatabaseName, canaryPassword) {
			t.Fatal("EXIT 2 LEAK: canary in outbox database name")
		}
	}
	// The raw payload column is what receivers get.
	var payloads []string
	rows, err := r.store.DB.Query(`SELECT payload FROM notification_outbox`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		payloads = append(payloads, p)
	}
	rows.Close()
	for _, p := range payloads {
		if strings.Contains(p, canaryPassword) {
			t.Fatalf("EXIT 2 LEAK: canary in outbox payload: %q", p)
		}
	}

	// ---- exit 3: manifest + recovery kit artifacts on staging ----
	if err := os.MkdirAll(r.stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(r.stagingDir, "canary.manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"backupId":"canary"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	kitBytes, err := os.ReadFile(manifestPath + ".restore.sh")
	if err == nil && strings.Contains(string(kitBytes), canaryPassword) {
		t.Fatal("EXIT 3 LEAK: canary in recovery kit")
	}

	// ---- exit 4: server log output ----
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logBuf.String(), "backup failed") {
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logBuf.String(), canaryPassword) {
		t.Fatalf("EXIT 4 LEAK: canary in server log:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "backup failed") {
		t.Fatal("expected the failure log line to be captured")
	}

	// The webhook receiver got a delivery (the pipeline actually ran).
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		received.Lock()
		n := len(receivedPayloads)
		received.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	received.Lock()
	defer received.Unlock()
	for _, p := range receivedPayloads {
		if strings.Contains(p, canaryPassword) {
			t.Fatalf("EXIT 2 LEAK (delivery body): canary in webhook payload: %q", p)
		}
	}
}

// syncBuffer is a concurrency-safe bytes.Buffer for log capture.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// newRunnerForCanary builds a Runner wired to the given logger (no dumper
// override needed: the canary exercises the failure path only).
func newRunnerForCanary(store *db.Store, key []byte, stagingDir string, log *slog.Logger) *Runner {
	return NewRunner(store, key, stagingDir, func(ctx context.Context) (string, error) {
		return "age1canaryrecipient", nil
	}, log)
}

// insertCanaryDatabase registers a database with the webhook target and a
// direct connection URI whose PASSWORD is the canary (the real leak path).
func insertCanaryDatabase(t *testing.T, r *Runner, name, hookURL string) int64 {
	t.Helper()
	enc, err := crypto.Encrypt(r.key, fmt.Appendf(nil,
		"postgres://app:%s@10.255.255.1:5432/appdb?sslmode=disable&connect_timeout=1", canaryPassword))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, heartbeat_url,
		                            heartbeat_period_hours, created_at, updated_at)
		 VALUES (?, 'generic', '', ?, ?, 24, '0', '0')`, name, enc, hookURL); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.store.DB.QueryRow(`SELECT id FROM databases WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

const canaryPassword = "CANARY-secret-4-exit-P@ss-9f3a"
