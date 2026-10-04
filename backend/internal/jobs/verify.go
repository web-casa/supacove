// Package jobs — restore verification pipeline (phase-5 review P1-04/05/06,
// P2-02). ONE worker goroutine owns every verification run, fed by a
// bounded queue, bound to the Runner lifecycle: Stop cancels the in-flight
// run and joins the worker before returning. Queue entries that never run
// (shutdown, overflow) end in a distinguishable persistent state — never an
// empty status.
package jobs

import (
	"context"
	"os"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	"github.com/cloudfan/supabackup/backend/internal/verifier"
)

// VerifyEngine is the runner-facing verification capability. An interface
// so tests can inject a fake engine with real lifecycle semantics.
type VerifyEngine interface {
	Verify(ctx context.Context, input verifier.Input) verifier.Result
}

// verifyQueueCap bounds the pending-verification queue. Backup concurrency
// is 1, so this only buffers bursts across successive backups; overflow
// degrades to an honest 'skipped' state, never an unbounded backlog.
const verifyQueueCap = 4

// Default wall-time budget for one verification run.
const defaultVerifyTimeout = 5 * time.Minute

// verifyRequest is one queued verification: the job to verify plus the
// secret redactor captured from the originating run (phase-5 review P2-02:
// async error details pass the same redaction chain as synchronous ones).
type verifyRequest struct {
	jobID  int64
	redact func(string) string
}

// SetVerifier wires the embedded restore verification engine (nil disables).
func (r *Runner) SetVerifier(v VerifyEngine) { r.verifier = v }

// SetVerifyIdentity provides the age identity used to decrypt artifacts for
// verification. Empty (default) means verification cannot decrypt and is
// recorded as skipped with that reason — never a silent no-op.
func (r *Runner) SetVerifyIdentity(identity string) { r.verifyIdentity = identity }

// SetVerifyTimeout overrides the per-run verification budget (test hook).
func (r *Runner) SetVerifyTimeout(d time.Duration) {
	if d > 0 {
		r.verifyTimeout = d
	}
}

// startVerifyWorker launches the single verification worker. Called from
// Start() after the runner lifetime context exists; the worker joins the
// runner WaitGroup so Stop waits for it (P1-06).
func (r *Runner) startVerifyWorker(lifeCtx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-lifeCtx.Done():
				return
			case req := <-r.verifyQueue:
				r.runVerification(lifeCtx, req)
			}
		}
	}()
}

// enqueueVerification marks the job 'pending' and queues it. It NEVER
// blocks the backup path: when the queue is full the job lands in
// 'skipped' with the reason. With no verifier configured it writes the
// explicit skip state instead of queueing into the void. Caller is the
// runJob success path.
func (r *Runner) enqueueVerification(jobID int64, redact func(string) string) {
	if r.verifier == nil {
		r.finishVerification(jobID, "skipped",
			"restore verification is not enabled on this instance (SB_VERIFY_ENABLED; see ADR-004)", redact, nil)
		return
	}
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET verify_status = 'pending', verify_detail = ''
		WHERE id = ? AND verify_status = ''`, jobID); err != nil {
		r.log.Error("verify pending state write failed", "job", jobID, "err", err)
	}
	select {
	case r.verifyQueue <- verifyRequest{jobID: jobID, redact: redact}:
	default:
		r.finishVerification(jobID, "skipped", "verification queue full; run a new backup or verify manually with the recovery kit", redact, nil)
	}
}

// ResumePendingVerifications re-queues verifications that were pending or
// running when the process stopped (P1-06 restart convergence). Called at
// startup after the worker is running.
func (r *Runner) ResumePendingVerifications(ctx context.Context) {
	if r.verifier == nil {
		return
	}
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id FROM jobs
		WHERE status = 'succeeded' AND verify_status IN ('pending','running')
		ORDER BY id`)
	if err != nil {
		r.log.Error("resume verification query failed", "err", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		r.enqueueVerification(id, pgclientSanitize)
	}
}

// pgclientSanitize is the redactor for verifications resumed at startup,
// where the originating run's secret list is gone: the shared message
// scrubber still removes credential-shaped substrings.
func pgclientSanitize(msg string) string { return pgclient.SanitizeMessage(msg) }

func sanitizeNoSecrets(msg string) string { return pgclient.SanitizeMessage(msg) }

// runVerification executes one queued verification and persists the result.
func (r *Runner) runVerification(lifeCtx context.Context, req verifyRequest) {
	jobID := req.jobID
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET verify_status = 'running', verify_detail = ''
		WHERE id = ? AND verify_status = 'pending'`, jobID); err != nil {
		r.log.Error("verify running state write failed", "job", jobID, "err", err)
	}

	r.mu.Lock()
	r.verifyInFlight[jobID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.verifyInFlight, jobID)
		r.mu.Unlock()
	}()

	ciphertextPath, sha, manifestPath, _, err := r.loadVerifyFacts(jobID)
	if err != nil {
		r.finishVerification(jobID, "skipped", "verification inputs unreadable: "+sanitizeNoSecrets(err.Error()), req.redact, nil)
		return
	}
	if ciphertextPath == "" {
		r.finishVerification(jobID, "skipped",
			"staged artifact no longer present locally (pruned or deleted); re-verify by downloading the remote object and restoring manually with the recovery kit",
			req.redact, nil)
		return
	}

	mb, merr := os.ReadFile(manifestPath)
	expectedTables := int64(-1)
	var expectedExts []string
	if merr == nil {
		if m, perr := manifest.Unmarshal(mb); perr == nil {
			if manifest.HasTableCount(mb) {
				expectedTables = m.Dependencies.TableCount
			}
			for _, e := range m.Dependencies.Extensions {
				expectedExts = append(expectedExts, e.Name)
			}
		} else {
			r.log.Warn("verify: manifest unreadable, expected-object checks degraded", "job", jobID, "err", perr)
		}
	}

	vCtx, cancel := context.WithTimeout(lifeCtx, r.verifyTimeout)
	defer cancel()
	res := r.verifier.Verify(vCtx, verifier.Input{
		CiphertextPath:     ciphertextPath,
		SHA256Hex:          sha,
		Identity:           r.verifyIdentity,
		ExpectedTables:     expectedTables,
		ExpectedExtensions: expectedExts,
		Timeout:            r.verifyTimeout,
	})
	// A run cut short by INSTANCE SHUTDOWN (not by its own timeout) is
	// inconclusive: record it back to 'pending' so the next startup re-queues
	// it, instead of leaving a misleading failed/verified state (P1-06
	// restart convergence).
	if vCtx.Err() != nil && lifeCtx.Err() != nil {
		r.finishVerification(jobID, "pending",
			"verification interrupted by instance shutdown; re-queued for the next startup", req.redact, nil)
		return
	}
	r.finishVerification(jobID, string(res.Status), res.Detail, req.redact, &res)

	if res.Pass() {
		r.log.Info("restore verification passed", "job", jobID, "tables", res.TablesFound, "profile", "embedded-local")
	} else {
		r.log.Warn("restore verification did not pass", "job", jobID, "status", res.Status, "detail", req.redact(res.Detail))
	}
}

// loadVerifyFacts reads what the verifier needs from the job row.
func (r *Runner) loadVerifyFacts(jobID int64) (ciphertextPath, sha, manifestPath string, kitPath string, err error) {
	err = r.authDB.QueryRow(`
		SELECT COALESCE(artifact_path,''), COALESCE(artifact_sha256,''),
		       COALESCE(manifest_path,''), COALESCE(recovery_kit_path,'')
		FROM jobs WHERE id = ?`, jobID).
		Scan(&ciphertextPath, &sha, &manifestPath, &kitPath)
	return
}

// finishVerification persists a terminal verification state. Detail is
// redacted through the originating run's secret list before storage
// (P2-02). A nil res keeps the numeric columns untouched (skipped states).
func (r *Runner) finishVerification(jobID int64, status, detail string, redact func(string) string, res *verifier.Result) {
	detail = redact(detail)
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	if res == nil {
		if _, err := r.authDB.Exec(`
			UPDATE jobs SET verify_status = ?, verify_detail = ?
			WHERE id = ?`, status, detail, jobID); err != nil {
			r.log.Error("verify state write failed", "job", jobID, "err", err)
		}
		return
	}
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET verify_status = ?, verify_detail = ?,
		  verify_tables = ?, verify_duration_secs = ?, verify_profile = ?
		WHERE id = ?`, status, detail, res.TablesFound, res.Duration.Seconds(),
		"embedded-local", jobID); err != nil {
		r.log.Error("verify state write failed", "job", jobID, "err", err)
	}
}

// verifyBusy reports whether a verification for the job may be reading its
// staged artifact right now — local pruning must skip such jobs (P1-09
// file lease).
func (r *Runner) verifyBusy(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verifyInFlight[jobID]
}
