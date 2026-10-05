// Package jobs — restore verification pipeline (phase-5 review P1-04/05/06,
// P2-02). ONE worker goroutine owns every verification run, fed by a
// bounded queue, bound to the Runner lifecycle: Stop cancels the in-flight
// run and joins the worker before returning. The queue entry holds the
// artifact lease from ENQUEUE time (not just execution), so retention and
// local pruning can never delete a verification input — queued or running —
// and the lease check is a single map operation under the runner mutex
// (round-2 P1-09: check-then-start TOCTOU closed by construction).
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/outbox"
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

// SetHeartbeatURL configures the FALLBACK dead-man switch URL for databases
// without their own heartbeat config (Phase 7: per-database config wins).
// Must be called before Start. Empty string disables the fallback.
func (r *Runner) SetHeartbeatURL(url string) { r.heartbeatURL = url }

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

// initialVerifyState returns the verification state a freshly succeeded job
// starts with, given the verifier configuration. Shared by runJob and the
// restart success path so no observer can ever see a succeeded job without
// a verification state (P2-02).
func (r *Runner) initialVerifyState() (status, detail string) {
	if r.verifier == nil {
		return "skipped", "restore verification is not enabled on this instance (SB_VERIFY_ENABLED; see ADR-004)"
	}
	if r.verifyIdentity == "" {
		return "skipped", "verification enabled but no age identity provided to the instance (SB_VERIFY_IDENTITY_FILE); artifacts cannot be decrypted for automatic restore checks"
	}
	return "pending", ""
}

// acquireVerifyLease registers the artifact lease and marks the job
// 'pending'. Ownership rules (round-3 R3-P1-02/R3-P2-01): a job that is
// ALREADY leased is refused — duplicate enqueues never enter the queue, so
// an overflow release can never drop somebody else's lease; a failed state
// write rolls the lease back instead of leaking an ownerless block.
func (r *Runner) acquireVerifyLease(jobID int64) bool {
	r.mu.Lock()
	if r.verifyInFlight[jobID] {
		r.mu.Unlock()
		r.log.Warn("verification already queued/running for this job; ignoring duplicate enqueue", "job", jobID)
		return false
	}
	r.verifyInFlight[jobID] = true
	r.mu.Unlock()
	_, err := r.authDB.Exec(`
		UPDATE jobs SET verify_status = 'pending', verify_detail = ''
		WHERE id = ? AND (verify_status = '' OR verify_status = 'pending' OR verify_status = 'running')`, jobID)
	if err != nil {
		r.releaseVerifyLease(jobID) // roll back: no ownerless lease
		r.log.Error("verify pending state write failed", "job", jobID, "err", err)
		return false
	}
	return true
}

// releaseVerifyLease drops the artifact lease.
func (r *Runner) releaseVerifyLease(jobID int64) {
	r.mu.Lock()
	delete(r.verifyInFlight, jobID)
	r.mu.Unlock()
}

// enqueueVerification acquires the lease and queues the job. It NEVER
// blocks the backup path: when the queue is full the job lands in
// 'skipped' with the reason (and the lease is released). With no verifier
// configured it writes the explicit skip state instead of queueing into
// the void. Caller is the runJob success path.
func (r *Runner) enqueueVerification(jobID int64, redact func(string) string) {
	if r.verifier == nil {
		r.finishVerification(jobID, "skipped",
			"restore verification is not enabled on this instance (SB_VERIFY_ENABLED; see ADR-004)", redact, nil)
		return
	}
	if !r.acquireVerifyLease(jobID) {
		return
	}
	select {
	case r.verifyQueue <- verifyRequest{jobID: jobID, redact: redact}:
	default:
		r.releaseVerifyLease(jobID)
		r.finishVerification(jobID, "skipped", "verification queue full; run a new backup or verify manually with the recovery kit", redact, nil)
	}
}

// ResumePendingVerifications re-queues verifications that were pending or
// running when the process stopped (P1-06 restart convergence). Called at
// startup after the worker is running. With no verifier configured, the
// transitional states are settled to an explicit skip instead of lingering
// forever (round-2 P2-02).
func (r *Runner) ResumePendingVerifications(ctx context.Context) {
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

	if r.verifier == nil || r.verifyIdentity == "" {
		reason := "restore verification is not enabled on this instance (SB_VERIFY_ENABLED; see ADR-004)"
		if r.verifier != nil {
			reason = "verification enabled but no age identity provided (SB_VERIFY_IDENTITY_FILE)"
		}
		for _, id := range ids {
			r.finishVerification(id, "skipped", reason, pgclientSanitize, nil)
		}
		return
	}
	for _, id := range ids {
		r.enqueueVerification(id, pgclientSanitize)
	}
}

// pgclientSanitize is the redactor for verifications resumed at startup,
// where the originating run's secret list is gone: the shared message
// scrubber still removes credential-shaped substrings.
func pgclientSanitize(msg string) string { return pgclient.SanitizeMessage(msg) }

// fetchRemoteArtifact retrieves the committed remote ciphertext when the
// local staged copy is gone (bucket-only backup), so the verification chain
// "remote read → hash check → decrypt → restore" can run from the bucket
// alone (round-2 P1-05 remainder). Returns the local path (caller removes
// it) or an error.
func (r *Runner) fetchRemoteArtifact(ctx context.Context, jobID int64) (string, error) {
	var destID sql.NullInt64
	var objKey, sha string
	if err := r.authDB.QueryRow(`
		SELECT destination_id, COALESCE(remote_object_key,''), COALESCE(artifact_sha256,'')
		FROM jobs WHERE id = ?`, jobID).Scan(&destID, &objKey, &sha); err != nil {
		return "", err
	}
	if !destID.Valid || destID.Int64 == 0 || objKey == "" || sha == "" {
		return "", errors.New("no remote object reference to fetch")
	}
	backend, err := r.BuildBackendByID(ctx, destID.Int64)
	if err != nil {
		return "", fmt.Errorf("build backend for remote fetch: %w", err)
	}
	rc, _, err := backend.Get(ctx, objKey)
	if err != nil {
		return "", fmt.Errorf("remote read %s: %w", objKey, err)
	}
	defer rc.Close()
	local := filepath.Join(r.stagingDir, fmt.Sprintf("verify-fetch-job%d.dump.age", jobID))
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(local)
		return "", fmt.Errorf("remote download: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(local)
		return "", err
	}
	return local, nil
}

// fetchRemoteManifest downloads the committed remote manifest to a temp
// file under staging for bucket-only verification runs. Caller removes the
// returned file.
func (r *Runner) fetchRemoteManifest(ctx context.Context, jobID int64) (string, error) {
	var destID sql.NullInt64
	var manKey string
	if err := r.authDB.QueryRow(`
		SELECT destination_id, COALESCE(remote_manifest_key,'')
		FROM jobs WHERE id = ?`, jobID).Scan(&destID, &manKey); err != nil {
		return "", err
	}
	if !destID.Valid || destID.Int64 == 0 || manKey == "" {
		return "", errors.New("no remote manifest reference")
	}
	backend, err := r.BuildBackendByID(ctx, destID.Int64)
	if err != nil {
		return "", fmt.Errorf("build backend for manifest fetch: %w", err)
	}
	rc, _, err := backend.Get(ctx, manKey)
	if err != nil {
		return "", fmt.Errorf("remote manifest read %s: %w", manKey, err)
	}
	defer rc.Close()
	local := filepath.Join(r.stagingDir, fmt.Sprintf("verify-fetch-job%d.manifest.json", jobID))
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(local)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(local)
		return "", err
	}
	return local, nil
}

// settleOrRequeue records a pre-run failure: a shutdown becomes a requeue
// ('pending'), anything else an explicit skip with the lease released
// (round-3 R3-P2-02: shutdown must never end in a terminal state).
func (r *Runner) settleOrRequeue(lifeCtx context.Context, req verifyRequest, detail string) {
	if lifeCtx.Err() != nil {
		r.finishVerification(req.jobID, "pending",
			"verification interrupted by instance shutdown; re-queued for the next startup", req.redact, nil)
		return
	}
	r.releaseVerifyLease(req.jobID)
	r.finishVerification(req.jobID, "skipped", detail, req.redact, nil)
}

// runVerification executes one queued verification and persists the result.
func (r *Runner) runVerification(lifeCtx context.Context, req verifyRequest) {
	jobID := req.jobID
	// Atomic claim: only the request that flips pending→running owns the
	// run. A duplicate or already-terminal job affects 0 rows and must NOT
	// re-execute the engine or overwrite a terminal state (round-3 R3-P1-02).
	claim, err := r.authDB.Exec(`
		UPDATE jobs SET verify_status = 'running', verify_detail = ''
		WHERE id = ? AND verify_status = 'pending'`, jobID)
	if err != nil {
		r.log.Error("verify running state write failed", "job", jobID, "err", err)
		r.releaseVerifyLease(jobID)
		return
	}
	if n, _ := claim.RowsAffected(); n == 0 {
		r.releaseVerifyLease(jobID)
		return
	}

	// The whole run — loading, remote fetch included — lives under ONE
	// deadline (round-3 R3-P2-02: the fetch previously escaped the budget).
	vCtx, cancel := context.WithTimeout(lifeCtx, r.verifyTimeout)
	defer cancel()

	ciphertextPath, sha, manifestPath, _, err := r.loadVerifyFacts(jobID)
	if err != nil {
		r.settleOrRequeue(lifeCtx, req, "verification inputs unreadable: "+pgclient.SanitizeMessage(err.Error()))
		return
	}
	fetched := false
	if ciphertextPath == "" || !regularFileExists(ciphertextPath) {
		// Local staging copy missing (pruned, or the reference points at a
		// vanished file): fall back to the committed remote object so
		// bucket-only backups still verify (round-2/3 P1-05).
		local, ferr := r.fetchRemoteArtifact(vCtx, jobID)
		if ferr != nil {
			if lifeCtx.Err() != nil {
				// Shutdown during load/fetch: inconclusive, requeue.
				r.finishVerification(jobID, "pending",
					"verification interrupted by instance shutdown; re-queued for the next startup", req.redact, nil)
				return
			}
			r.releaseVerifyLease(jobID)
			r.finishVerification(jobID, "skipped",
				"staged artifact absent and remote fetch impossible: "+req.redact(ferr.Error()), req.redact, nil)
			return
		}
		ciphertextPath = local
		fetched = true
	}
	defer func() {
		if fetched {
			os.Remove(ciphertextPath)
		}
		r.releaseVerifyLease(jobID)
	}()

	// The manifest is the ONLY source for the expected-object baseline. If
	// the local copy is missing, fetch the committed remote manifest before
	// giving up (round-3 P1-05: the bucket holds both objects). A manifest
	// that is still missing/unreadable records an honest skip — never a
	// silent downgrade of the checks (round-2 P1-08).
	mb, merr := os.ReadFile(manifestPath)
	if merr != nil {
		if local, ferr := r.fetchRemoteManifest(vCtx, jobID); ferr == nil {
			if mb2, rerr := os.ReadFile(local); rerr == nil {
				mb = mb2
				merr = nil
				defer os.Remove(local)
			}
		}
	}
	if merr != nil {
		r.finishVerification(jobID, "skipped",
			"manifest unreadable ("+pgclient.SanitizeMessage(merr.Error())+"); expected-object baseline unavailable — restore the manifest or verify manually with the recovery kit", req.redact, nil)
		return
	}
	m, perr := manifest.Unmarshal(mb)
	if perr != nil {
		r.finishVerification(jobID, "skipped",
			"manifest unparseable ("+pgclient.SanitizeMessage(perr.Error())+"); expected-object baseline unavailable", req.redact, nil)
		return
	}
	expectedTables := int64(-1)
	if manifest.HasTableCount(mb) {
		expectedTables = m.Dependencies.TableCount
	}
	var expectedExts []string
	for _, e := range m.Dependencies.Extensions {
		expectedExts = append(expectedExts, e.Name)
	}

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
	if res.Pass() || (res.Status != verifier.StatusFailed && res.Status != verifier.StatusUnsupported) {
		r.finishVerification(jobID, string(res.Status), res.Detail, req.redact, &res)
	} else if !r.finishVerificationWithNotify(jobID, string(res.Status), res.Detail, req.redact, &res) {
		// The terminal state did not commit: the next startup sweep re-queues
		// the verification and the reconciliation covers the notification —
		// never emit a notification for state that was not persisted (P1-03).
		return
	}

	if res.Pass() {
		r.log.Info("restore verification passed", "job", jobID, "tables", res.TablesFound, "profile", verifyProfileOf(res))
	} else {
		r.log.Warn("restore verification did not pass", "job", jobID, "status", res.Status, "detail", req.redact(res.Detail))
	}
}

// verifyProfileOf derives the persisted verify_profile from the result's
// server version evidence — never a constant (round-2 P1-08).
func verifyProfileOf(res verifier.Result) string {
	return verifier.ProfileName(res.ServerVersion)
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
// The write retries a bounded number of times; a persistent failure leaves
// the job in its transitional state, which startup convergence re-queues
// (round-2 P2-02: write-failure convergence).
func (r *Runner) finishVerification(jobID int64, status, detail string, redact func(string) string, res *verifier.Result) {
	detail = redact(detail)
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	var q string
	var args []any
	if res == nil {
		q = `UPDATE jobs SET verify_status = ?, verify_detail = ? WHERE id = ?`
		args = []any{status, detail, jobID}
	} else {
		q = `UPDATE jobs SET verify_status = ?, verify_detail = ?,
		  verify_tables = ?, verify_duration_secs = ?, verify_profile = ?
		WHERE id = ?`
		args = []any{status, detail, res.TablesFound, res.Duration.Seconds(),
			verifyProfileOf(*res), jobID}
	}
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err = r.authDB.Exec(q, args...); err == nil {
			return
		}
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	r.log.Error("verify state write failed after retries; job stays transitional for startup convergence",
		"job", jobID, "wanted_status", status, "err", err)
}

// finishVerificationWithNotify persists a terminal verification failure and
// its notification in ONE transaction (round-1 review P1-03: separate writes
// left a crash window with a failed state and no notification, and let a
// notification be emitted for state that never persisted). Returns false
// when the transaction did not commit.
func (r *Runner) finishVerificationWithNotify(jobID int64, status, detail string, redact func(string) string, res *verifier.Result) bool {
	detail = redact(detail)
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	tx, err := r.authDB.Begin()
	if err != nil {
		r.log.Error("verify failure tx begin failed", "job", jobID, "err", err)
		return false
	}
	defer tx.Rollback()
	var dbID int64
	var dbName string
	if err := tx.QueryRow(`
		SELECT j.database_id, COALESCE(d.name, '') FROM jobs j
		LEFT JOIN databases d ON d.id = j.database_id
		WHERE j.id = ?`, jobID).Scan(&dbID, &dbName); err != nil {
		r.log.Error("verify failure tx lookup failed", "job", jobID, "err", err)
		return false
	}
	if _, err := tx.Exec(`
		UPDATE jobs SET verify_status = ?, verify_detail = ?,
		  verify_tables = ?, verify_duration_secs = ?, verify_profile = ?
		WHERE id = ?`, status, detail, res.TablesFound, res.Duration.Seconds(),
		verifyProfileOf(*res), jobID); err != nil {
		r.log.Error("verify failure state write failed", "job", jobID, "err", err)
		return false
	}
	if err := outbox.EnqueueTx(context.Background(), tx, outbox.Event{
		EventID:      fmt.Sprintf("verification_failed:job:%d", jobID),
		EventType:    outbox.EventVerificationFailed,
		DatabaseID:   dbID,
		DatabaseName: dbName,
		Payload: map[string]any{
			"event":         "verification_failed",
			"job_id":        jobID,
			"database":      dbName,
			"verify_status": status,
			"detail":        truncate(detail, 300),
		},
	}, time.Now()); err != nil {
		r.log.Error("outbox enqueue for verification failure failed", "job", jobID, "err", err)
		return false
	}
	if err := tx.Commit(); err != nil {
		r.log.Error("verify failure tx commit failed", "job", jobID, "err", err)
		return false
	}
	return true
}

// verifyBusy reports whether a verification for the job may be reading its
// staged artifact right now OR is queued to do so — local pruning and
// remote retention must skip such jobs (P1-09 file lease).
func (r *Runner) verifyBusy(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verifyInFlight[jobID]
}
