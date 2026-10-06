package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/storage"
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
	if class != ClassNetwork {
		t.Errorf("class = %q, want %q", class, ClassNetwork)
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
		                  artifact_state, artifact_path, manifest_path, remote_state, remote_object_key, remote_manifest_key)
		VALUES (?, 'interrupted', 0, 0, ?, 'committed', 'PENDING', 'PENDING', 'uploading', 'PENDING', 'PENDING')
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

	// The resume runs on a signal context that is already dead (SIGTERM
	// before the pass finished).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
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
	if n := p.outboxEvents(t, "backup_failed"); n != 0 {
		t.Errorf("shutdown resume produced %d backup_failed notifications, want 0", n)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Errorf("artifact must survive for the next resume: %v", err)
	}
}
