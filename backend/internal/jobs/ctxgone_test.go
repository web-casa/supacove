package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/web-casa/supacove/backend/internal/stats"
	"github.com/web-casa/supacove/backend/internal/storage"
)

// Harness for the ctx-gone semantics tests: a runner whose lifeCtx we own,
// a database row whose connection points at a refused port (every runJob
// path after loadDatabase fails fast, deterministically, without docker).
type ctxGoneHarness struct {
	*Runner
	dbID int64
}

func newCtxGoneHarness(t *testing.T) *ctxGoneHarness {
	t.Helper()
	p := newPhase3Runner(t)
	enc, err := encryptForTest(p.key, "postgres://app:pw@127.0.0.1:1/appdb?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		 VALUES ('ctxgone', 'generic', '', ?, '0', '0')`, enc); err != nil {
		t.Fatal(err)
	}
	var dbID int64
	_ = p.store.DB.QueryRow(`SELECT id FROM databases WHERE name='ctxgone'`).Scan(&dbID)
	// Start() sets lifeCtx; the worker loop stays idle because nothing is
	// enqueued (we drive runJob directly).
	p.Start(context.Background())
	h := &ctxGoneHarness{Runner: p.Runner, dbID: dbID}
	t.Cleanup(p.Stop)
	return h
}

func (h *ctxGoneHarness) seedRunningJob(t *testing.T) int64 {
	t.Helper()
	var jobID int64
	if err := h.store.DB.QueryRow(
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		 VALUES (?, 'running', 0, 0) RETURNING id`, h.dbID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func (h *ctxGoneHarness) jobState(t *testing.T, jobID int64) (status, class, msg string) {
	t.Helper()
	if err := h.store.DB.QueryRow(
		`SELECT status, COALESCE(error_class,''), COALESCE(error_message,'')
		 FROM jobs WHERE id = ?`, jobID).Scan(&status, &class, &msg); err != nil {
		t.Fatal(err)
	}
	return status, class, msg
}

func (p *phase3Runner) outboxEvents(t *testing.T, event string) int {
	t.Helper()
	var n int
	if err := p.store.DB.QueryRow(
		`SELECT COUNT(*) FROM notification_outbox WHERE event_type = ?`, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *ctxGoneHarness) outboxCount(t *testing.T, event string) int {
	t.Helper()
	var n int
	if err := h.store.DB.QueryRow(
		`SELECT COUNT(*) FROM notification_outbox WHERE event_type = ?`, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// P1-R1: a context ending from instance shutdown (lifeCtx done, no cancel
// request) must land the job as `interrupted` — silently: no failure
// notification, no /fail heartbeat, no false `failed`.
func TestShutdownLandsInterruptedSilently(t *testing.T) {
	h := newCtxGoneHarness(t)
	jobID := h.seedRunningJob(t)

	ctx, cancel := context.WithCancel(h.lifeCtx)
	defer cancel()
	h.lifeCancel() // instance shutdown: the child context dies with the runner
	h.runJob(ctx, jobID, h.dbID)

	status, _, msg := h.jobState(t, jobID)
	if status != "interrupted" {
		t.Fatalf("status = %q, want interrupted (msg %q)", status, msg)
	}
	if n := h.outboxCount(t, "backup_failed"); n != 0 {
		t.Errorf("shutdown produced %d backup_failed notifications, want 0", n)
	}
}

// A user cancel keeps its existing destination: `canceled`.
func TestUserCancelStillLandsCanceled(t *testing.T) {
	h := newCtxGoneHarness(t)
	jobID := h.seedRunningJob(t)
	if _, err := h.store.DB.Exec(
		`UPDATE jobs SET cancel_requested = 1 WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(h.lifeCtx)
	cancel()
	h.runJob(ctx, jobID, h.dbID)

	if status, _, _ := h.jobState(t, jobID); status != "canceled" {
		t.Fatalf("status = %q, want canceled", status)
	}
}

// P1-K2: an expired job budget (WithTimeout on the job context alone — the
// runner is healthy) is a REAL failure with an explicit reason, never an
// interruption.
func TestExpiredBudgetLandsFailedWithReason(t *testing.T) {
	h := newCtxGoneHarness(t)
	h.SetJobTimeout(50 * time.Millisecond)
	jobID := h.seedRunningJob(t)

	ctx, cancel := context.WithTimeout(h.lifeCtx, 50*time.Millisecond)
	defer cancel()
	time.Sleep(120 * time.Millisecond) // let the budget die while lifeCtx lives
	h.runJob(ctx, jobID, h.dbID)

	status, class, msg := h.jobState(t, jobID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if class != ClassUnknown {
		t.Errorf("class = %q, want %q", class, ClassUnknown)
	}
	if !strings.Contains(msg, "execution budget") {
		t.Errorf("message must name the budget: %q", msg)
	}
	// A real failure notifies (the outbox is the delivery guarantee).
	if n := h.outboxCount(t, "backup_failed"); n != 1 {
		t.Errorf("budget failure produced %d backup_failed notifications, want 1", n)
	}
}

// A healthy context is never treated as ended.
func TestSettleCtxGoneHealthyContextIsNoop(t *testing.T) {
	h := newCtxGoneHarness(t)
	if h.settleCtxGone(context.Background(), 1) {
		t.Error("settleCtxGone must return false for a live context")
	}
}

// P1-K1: failed/canceled/interrupted artifacts are reclaimed after the TTL;
// recent ones stay; TTL 0 keeps everything.
func TestPruneExpiredArtifacts(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := func(t *testing.T, status string, finishedAgo time.Duration) (int64, string, string) {
		t.Helper()
		var jobID int64
		fin := int64(0)
		if finishedAgo >= 0 {
			fin = time.Now().Add(-finishedAgo).Unix()
		}
		if err := p.store.DB.QueryRow(`
			INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at,
			                  artifact_state, artifact_path, manifest_path)
			VALUES (?, ?, 0, 0, ?, 'committed', 'PENDING', 'PENDING')
			RETURNING id`, p.addDatabase(t, destID, "pdb-"+status+"-"+fmt.Sprint(finishedAgo)), status, fin).
			Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
		manifest := artifact + ".manifest.json"
		if err := os.WriteFile(artifact, []byte("CIPHERTEXT"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.store.DB.Exec(
			`UPDATE jobs SET artifact_path = ?, manifest_path = ? WHERE id = ?`,
			artifact, manifest, jobID); err != nil {
			t.Fatal(err)
		}
		return jobID, artifact, manifest
	}

	oldFailed, oldFailedArt, oldFailedMan := seed(t, "failed", defaultFailedArtifactTTL+time.Hour)
	oldInterrupted, oldIntArt, _ := seed(t, "interrupted", defaultFailedArtifactTTL+2*time.Hour)
	recentFailed, recentArt, _ := seed(t, "failed", time.Hour)

	p.SetFailedArtifactTTL(defaultFailedArtifactTTL)
	p.PruneExpiredArtifacts(context.Background())

	if _, err := os.Stat(oldFailedArt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired failed artifact must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(oldFailedMan); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired failed manifest must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(oldIntArt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired interrupted artifact must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(recentArt); err != nil {
		t.Errorf("recent failed artifact must stay, stat err = %v", err)
	}
	var cleared int
	if err := p.store.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE id IN (?, ?) AND artifact_path = '' AND artifact_state = ''`,
		oldFailed, oldInterrupted).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if cleared != 2 {
		t.Errorf("cleared references = %d, want 2", cleared)
	}
	var kept string
	if err := p.store.DB.QueryRow(
		`SELECT artifact_path FROM jobs WHERE id = ?`, recentFailed).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept == "" {
		t.Error("recent artifact reference must survive")
	}

	// TTL 0 disables the sweep entirely.
	p.SetFailedArtifactTTL(0)
	p.PruneExpiredArtifacts(context.Background())
	if _, err := os.Stat(recentArt); err != nil {
		t.Errorf("TTL 0 must keep artifacts, stat err = %v", err)
	}
}

// ctxRefusingBackend fails every Put whose context is already dead — the
// real object stores honor ctx; the plain fake does not.
type ctxRefusingBackend struct {
	storage.Backend
}

func (b ctxRefusingBackend) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Backend.Put(ctx, key, r, size)
}

// P1-R1 remainder (fix review reliability P2-2): a SIGTERM during the
// synchronous startup resume must restore `interrupted` — silently — instead
// of a false `failed` that would notify, ping /fail and permanently retire a
// resumable job.
func TestResumePhaseShutdownRestoresInterruptedSilently(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	dbID := p.addDatabase(t, destID, "resumedb")
	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	// Interrupted job with a committed local artifact and an upload intent —
	// exactly what a shutdown mid-upload leaves behind.
	var jobID int64
	if err := p.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, destination_id,
		                  artifact_size, artifact_state, artifact_path, manifest_path, remote_state, remote_object_key, remote_manifest_key)
		VALUES (?, 'interrupted', 0, 0, ?, 10, 'committed', 'PENDING', 'PENDING', 'uploading', 'PENDING', 'PENDING')
		RETURNING id`, dbID, destID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
	manifest := artifact + ".manifest.json"
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`UPDATE jobs SET artifact_path = ?, manifest_path = ? WHERE id = ?`,
		artifact, manifest, jobID); err != nil {
		t.Fatal(err)
	}

	// SIGTERM lands MID-UPLOAD (Q6): the first Put fires the hook that
	// cancels the signal context, so the repair path — restore to
	// interrupted, context-free state writes — is actually exercised.
	ctx, cancel := context.WithCancel(context.Background())
	p.backend.onFirstPut = cancel
	p.SetBackendFactory(func(ctx context.Context, dest *Destination) (storage.Backend, error) {
		return ctxRefusingBackend{p.backend}, nil
	})
	p.ResumeRemotePhase(ctx)

	var status, remote string
	if err := p.store.DB.QueryRow(
		`SELECT status, remote_state FROM jobs WHERE id = ?`, jobID).Scan(&status, &remote); err != nil {
		t.Fatal(err)
	}
	if status != "interrupted" {
		t.Fatalf("status = %q, want interrupted (artifact stays resumable)", status)
	}
	if remote != "uploading" {
		t.Errorf("remote_state = %q, want uploading (intent intact)", remote)
	}
	if p.backend.putCount == 0 {
		t.Fatal("the upload never started: the resume repair path was not exercised")
	}
	if n := p.outboxEvents(t, "backup_failed"); n != 0 {
		t.Errorf("shutdown resume produced %d backup_failed notifications, want 0", n)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Errorf("artifact must survive for the next resume: %v", err)
	}
}

// Q2 regression: an unlink failure must keep the reference so the next sweep
// retries — the bytes must never leak with a cleared row.
func TestPruneKeepsReferenceWhenUnlinkFails(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	dbID := p.addDatabase(t, destID, "unlink-fail-db")
	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	fin := time.Now().Add(-(defaultFailedArtifactTTL + time.Hour)).Unix()
	if err := p.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at,
		                  artifact_state, artifact_path, manifest_path)
		VALUES (?, 'failed', 0, 0, ?, 'committed', 'PENDING', 'PENDING')
		RETURNING id`, dbID, fin).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT"), 0o400); err != nil { // read-only file
		t.Fatal(err)
	}
	manifest := artifact + ".manifest.json"
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`UPDATE jobs SET artifact_path = ?, manifest_path = ? WHERE id = ?`,
		artifact, manifest, jobID); err != nil {
		t.Fatal(err)
	}
	// Make the parent directory read-only so os.Remove fails. Under root the
	// permission bit does not restrict unlink, so skip explicitly
	// (fresh-review P3-1: a chmod-result heuristic misreads root as "ok").
	if os.Geteuid() == 0 {
		t.Skip("chmod cannot restrict root; unlink-failure path untestable here")
	}
	if err := os.Chmod(staging, 0o500); err != nil {
		t.Skipf("cannot restrict staging dir: %v", err)
	}
	defer func() { _ = os.Chmod(staging, 0o700) }()

	p.SetFailedArtifactTTL(defaultFailedArtifactTTL)
	p.PruneExpiredArtifacts(context.Background())

	var path, state string
	if err := p.store.DB.QueryRow(
		`SELECT artifact_path, artifact_state FROM jobs WHERE id = ?`, jobID).Scan(&path, &state); err != nil {
		t.Fatal(err)
	}
	if path == "" || state == "" {
		t.Fatalf("reference cleared although the file still exists (path=%q state=%q)", path, state)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("artifact vanished unexpectedly: %v", err)
	}

	// Permission restored → the next sweep reclaims it.
	if err := os.Chmod(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	p.PruneExpiredArtifacts(context.Background())
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Errorf("artifact still present after permission restore: %v", err)
	}
	if err := p.store.DB.QueryRow(
		`SELECT artifact_path FROM jobs WHERE id = ?`, jobID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("reference must clear after successful reclaim, got %q", path)
	}
}

// Q3 regression: committed_no_manifest artifacts are reclaimable too.
func TestPruneReclaimsCommittedNoManifest(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	dbID := p.addDatabase(t, destID, "noman-db")
	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	fin := time.Now().Add(-(defaultFailedArtifactTTL + time.Hour)).Unix()
	if err := p.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at,
		                  artifact_state, artifact_path, manifest_path)
		VALUES (?, 'failed', 0, 0, ?, 'committed_no_manifest', 'PENDING', '')
		RETURNING id`, dbID, fin).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`UPDATE jobs SET artifact_path = ? WHERE id = ?`, artifact, jobID); err != nil {
		t.Fatal(err)
	}
	p.SetFailedArtifactTTL(defaultFailedArtifactTTL)
	p.PruneExpiredArtifacts(context.Background())
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Errorf("committed_no_manifest artifact must be reclaimed, stat err = %v", err)
	}
}

// Fresh-review P2-01/P3-01: a resumed remote commit is a COMPLETED backup —
// it must ping the dead-man switch (anchored on the original dump start),
// record a stats row, and clear the recovery annotation. Mutating any of the
// three side effects away must turn this red.
func TestResumeSuccessCarriesSuccessSideEffects(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	dbID := p.addDatabase(t, destID, "resume-side-db")
	// heartbeat config on the database: a positive period so a fresh enough
	// snapshot pings; the original dump start is "now" so the age gate passes.
	if _, err := p.store.DB.Exec(`
		UPDATE databases SET heartbeat_url = '-', heartbeat_period_hours = 24 WHERE id = ?`, dbID); err != nil {
		t.Fatal(err)
	}
	// '-' disables pings: use a real loopback receiver instead.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if _, err := p.store.DB.Exec(`UPDATE databases SET heartbeat_url = ? WHERE id = ?`, srv.URL, dbID); err != nil {
		t.Fatal(err)
	}
	p.SetStatsRecorder(stats.New(p.store.DB))

	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	started := time.Now().Add(-time.Hour).Unix() // original dump start: age gate must see THIS
	if err := p.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at,
		                  artifact_size, artifact_sha256, artifact_state, artifact_path, manifest_path, remote_state,
		                  remote_object_key, remote_manifest_key, destination_id,
		                  error_message)
		VALUES (?, 'interrupted', 0, 0, ?, 10, ?, 'committed', 'PENDING', 'PENDING', 'uploading', 'PENDING', 'PENDING', ?,
		          ' [recovery: artifact committed before interruption — restore manually or re-run]')
		RETURNING id`, dbID, started, sha256Of([]byte("CIPHERTEXT")), destID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
	manifest := artifact + ".manifest.json"
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`UPDATE jobs SET artifact_path = ?, manifest_path = ? WHERE id = ?`, artifact, manifest, jobID); err != nil {
		t.Fatal(err)
	}

	p.ResumeRemotePhase(context.Background())

	var status, errMsg string
	var finished int64
	if err := p.store.DB.QueryRow(
		`SELECT status, error_message, COALESCE(finished_at,0) FROM jobs WHERE id = ?`, jobID).
		Scan(&status, &errMsg, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		var cls, emsg string
		_ = p.store.DB.QueryRow(`SELECT COALESCE(error_class,''), COALESCE(error_message,'') FROM jobs WHERE id = ?`, jobID).Scan(&cls, &emsg)
		t.Fatalf("status = %q, want succeeded (class=%s msg=%s)", status, cls, emsg)
	}
	if errMsg != "" {
		t.Errorf("recovery annotation survived a successful resume: %q", errMsg)
	}

	// stats row recorded (artifact size known, dump size unknown by design)
	var dumpSize, artifactSize int64
	if err := p.store.DB.QueryRow(
		`SELECT dump_size, artifact_size FROM backup_stats WHERE job_id = ?`, jobID).
		Scan(&dumpSize, &artifactSize); err != nil {
		t.Fatalf("stats row missing after resumed success: %v", err)
	}
	if artifactSize != int64(len("CIPHERTEXT")) {
		t.Errorf("stats artifact_size = %d, want %d", artifactSize, len("CIPHERTEXT"))
	}

	// dead-man switch pinged, anchored on the ORIGINAL dump start: the
	// snapshot is 1h old, period 24h → fresh → success ping allowed. The
	// ping is a detached goroutine, so poll briefly for the timestamp.
	var hbAt int64
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := p.store.DB.QueryRow(`SELECT last_heartbeat_at FROM databases WHERE id = ?`, dbID).Scan(&hbAt); err != nil {
			t.Fatal(err)
		}
		if hbAt != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hbAt == 0 {
		t.Error("resume success did not ping the dead-man switch")
	}
}
