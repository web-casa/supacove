package jobs

import (
	"context"
	"fmt"
	"io"
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
	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/outbox"
	"github.com/cloudfan/supabackup/backend/internal/recovery"
)

// The canary secret travels the production failure entry; the four exits
// are checked for its ABSENCE, and every exit must also produce POSITIVE
// evidence (a delivered outbox entry with a fully-read webhook body, a real
// generated kit and manifest on disk) so an empty pipeline can never pass
// silently (phase-8 review P1-02). The full backup-pipeline canary (register
// → real dump → kit) runs in TestPhase6RealVerificationEndToEnd.
const canaryPassword = "CANARY-secret-4-exit-P@ss-9f3a"

func TestPhase8SecretCanary(t *testing.T) {
	// Receiver: reads the FULL body (io.ReadAll — not a single Read).
	var mu sync.Mutex
	receivedBodies := [][]byte{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("receiver read: %v", err)
		}
		mu.Lock()
		receivedBodies = append(receivedBodies, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer hook.Close()

	var logBuf syncBuffer
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
	r := newRunnerForCanary(store, key, filepath.Join(dataDir, "staging"), logger)

	// REAL webhook subscription + REAL notifier worker.
	if _, err := CreateWebhook(context.Background(), store.DB, "canary-hook",
		hook.URL, []string{"backup_failed"}); err != nil {
		t.Fatal(err)
	}
	notifier := outbox.New(store.DB, discardLogger(), 20*time.Millisecond)
	notifier.Start(context.Background())
	t.Cleanup(notifier.Stop)

	dbID := insertCanaryDatabase(t, r, "canary-db", hook.URL)
	var jobID int64
	if err := store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		VALUES (?, 'running', 0, 0) RETURNING id`, dbID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	// The production failure entry (the redaction chain runs inside).
	r.fail(jobID, "network", "dial tcp 10.0.0.1:5432 failed with password="+canaryPassword)

	// ---- exit 1: persisted error + API task view ----
	var errMsg string
	if err := store.DB.QueryRow(`SELECT error_message FROM jobs WHERE id = ?`, jobID).Scan(&errMsg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errMsg, canaryPassword) {
		t.Fatalf("EXIT 1 LEAK: canary in stored error_message: %q", errMsg)
	}
	tk, err := GetTask(store.DB, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tk.ErrorMessage, canaryPassword) {
		t.Fatal("EXIT 1 LEAK: canary in API task view")
	}

	// ---- exit 3: REAL generated artifacts (must exist and be non-empty) ----
	m := &manifest.Manifest{BackupID: "canary", Database: manifest.Database{Name: "canary-db"}}
	mb, err := manifest.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(r.stagingDir, "canary.manifest.json")
	if werr := durableWriteFile(manifestPath, mb); werr != nil {
		t.Fatalf("manifest write: %v", werr)
	}
	written, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("exit 3 positive evidence missing: empty manifest")
	}
	if strings.Contains(string(written), canaryPassword) {
		t.Fatal("EXIT 3 LEAK: canary in written manifest")
	}
	kitScript := recovery.GenerateRestoreScriptWithTables(recovery.KitInput{
		BackupUUID:       "canary",
		ArtifactFileName: "canary.dump.age",
		SHA256:           "0000",
		KeyID:            "canary-key-id",
	}, 3)
	kitPath := manifestPath + ".restore.sh"
	if werr := durableWriteFile(kitPath, []byte(kitScript)); werr != nil {
		t.Fatal(werr)
	}
	kitBytes, err := os.ReadFile(kitPath)
	if err != nil {
		t.Fatalf("exit 3 positive evidence missing (kit unreadable): %v", err)
	}
	if len(kitBytes) == 0 {
		t.Fatal("exit 3 positive evidence missing (empty kit)")
	}
	if strings.Contains(string(kitBytes), canaryPassword) {
		t.Fatal("EXIT 3 LEAK: canary in generated recovery kit")
	}

	// ---- exit 2 (delivery): DELIVERED entry + fully-read body REQUIRED.
	deadline := time.Now().Add(5 * time.Second)
	var entries []outbox.EntryView
	for time.Now().Before(deadline) {
		entries, err = outbox.List(context.Background(), store.DB, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 0 && entries[0].State == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(entries) == 0 {
		t.Fatal("exit 2 positive evidence missing: no outbox entry at all")
	}
	if entries[0].State != "delivered" {
		t.Fatalf("exit 2 positive evidence missing: state=%q (want delivered)", entries[0].State)
	}
	mu.Lock()
	bodies := append([][]byte{}, receivedBodies...)
	mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("exit 2 positive evidence missing: receiver got no request")
	}
	empty := true
	eventMatched := false
	for _, body := range bodies {
		if len(body) > 0 {
			empty = false
		}
		// The received payload must carry the expected event identity —
		// correlating delivery with the enqueued event, not just any traffic.
		if strings.Contains(string(body), `"event":"backup_failed"`) {
			eventMatched = true
		}
		if strings.Contains(string(body), canaryPassword) {
			t.Fatalf("EXIT 2 LEAK: canary in delivered webhook body: %s", body)
		}
	}
	if empty {
		t.Fatal("exit 2 positive evidence missing: all received bodies empty")
	}
	if !eventMatched {
		t.Fatal("exit 2 positive evidence missing: no received body carries the backup_failed event")
	}
	rows, err := store.DB.Query(`SELECT payload FROM notification_outbox`)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(payloads) == 0 {
		t.Fatal("exit 2 positive evidence missing: no payload rows")
	}
	for _, p := range payloads {
		if strings.Contains(p, canaryPassword) {
			t.Fatalf("EXIT 2 LEAK: canary in outbox payload: %q", p)
		}
	}

	// ---- exit 4: server log ----
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logBuf.String(), "backup failed") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logBuf.String(), "backup failed") {
		t.Fatal("exit 4 positive evidence missing: failure log line not captured")
	}
	if strings.Contains(logBuf.String(), canaryPassword) {
		t.Fatalf("EXIT 4 LEAK: canary in server log:\n%s", logBuf.String())
	}
}

// insertCanaryDatabase registers a database whose connection URI carries
// the canary as its PASSWORD (the real leak surface).
func insertCanaryDatabase(t *testing.T, r *Runner, name, hookURL string) int64 {
	t.Helper()
	enc, err := crypto.Encrypt(r.key, []byte(fmt.Sprintf(
		"postgres://app:%s@10.255.255.1:5432/appdb?sslmode=disable&connect_timeout=1", canaryPassword)))
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

// newRunnerForCanary builds a Runner wired to the given logger (the canary
// exercises the failure path only).
func newRunnerForCanary(store *db.Store, key []byte, stagingDir string, log *slog.Logger) *Runner {
	return NewRunner(store, key, stagingDir, func(ctx context.Context) (string, error) {
		return "age1canaryrecipient", nil
	}, log)
}

// syncBuffer is a concurrency-safe byte sink for log capture.
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
