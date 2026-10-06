// Package jobs implements the task state machine (pending → running →
// succeeded / failed / canceled / interrupted) with the seven error classes,
// and the single-concurrency worker that drives backups end-to-end
// (dev-plan Phase 2, tasks 3–4).
package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/dumper"
	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/outbox"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
	"github.com/cloudfan/supabackup/backend/internal/recovery"
	redactpkg "github.com/cloudfan/supabackup/backend/internal/redact"
	"github.com/cloudfan/supabackup/backend/internal/stats"
	"github.com/cloudfan/supabackup/backend/internal/storage"
)

// dbExec is the minimal SQL execution interface shared by *sql.DB and *sql.Tx.
type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Error classes (dev-plan seven classes). Stored verbatim in jobs.error_class.
const (
	ClassNetwork    = string(pgclient.ClassNetwork)
	ClassAuth       = string(pgclient.ClassAuth)
	ClassPermission = string(pgclient.ClassPermission)
	ClassClientVer  = string(pgclient.ClassClientVer)
	ClassDisk       = string(pgclient.ClassDisk)
	ClassStorageUp  = string(pgclient.ClassStorageUp)
	ClassVerify     = string(pgclient.ClassVerify)
	ClassUnknown    = string(pgclient.ClassUnknown)
)

// State machine statuses.
const (
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
	StatusInterrupted = "interrupted"
)

var ErrAlreadyQueued = errors.New("this database already has a pending or running job")

// heartbeatClient bounds dead-man switch pings (Phase 7) and reuses the
// webhook delivery transport: link-local (cloud metadata) destinations are
// refused at dial time no matter what a DNS name resolves to (round-1
// review P1-01 — configuration-time literal checks alone are bypassable).
var heartbeatClient = outbox.DeliveryClient(5 * time.Second)

// ErrDatabaseNotFound for unknown database ids.
var ErrDatabaseNotFound = errors.New("database not found")

// Runner owns the worker and the per-database exclusion.
type Runner struct {
	store      *db.Store
	authDB     *sql.DB // same SQLite handle; convenience alias
	stagingDir string
	localKeep  int
	cfg        dumper.Config
	key        []byte // master secret (credentials decryption)
	recipient  func(ctx context.Context) (string, error)
	log        *slog.Logger

	mu             sync.Mutex
	cancelFns      map[int64]context.CancelFunc
	perDB          map[int64]bool // database currently being processed
	wake           chan struct{}
	lifeCtx        context.Context
	lifeCancel     context.CancelFunc
	wg             sync.WaitGroup
	startOnce      sync.Once
	stopOnce       sync.Once
	stopDone       chan struct{}
	heartbeatURL   string
	destMu         sync.Mutex
	destBackends   map[int64]storage.Backend
	backendFactory func(ctx context.Context, dest *Destination) (storage.Backend, error) // initialized in NewRunner
	statsRecorder  *stats.Recorder
	verifier       VerifyEngine
	// verifyQueue feeds the single verification worker (bounded; see
	// jobs/verify.go).
	verifyQueue    chan verifyRequest
	verifyInFlight map[int64]bool // job IDs whose artifact a verification is reading
	verifyIdentity string
	verifyTimeout  time.Duration
	// jobTimeout is the wall-clock budget for one whole backup job (dump,
	// upload, read-back); 0 disables it. failedArtifactTTL is how long a
	// failed/canceled/interrupted job's committed staging artifact survives
	// before reclamation; 0 keeps everything.
	jobTimeout        time.Duration
	failedArtifactTTL time.Duration
}

// Defaults for the two knobs above; overridable per instance
// (SB_JOB_TIMEOUT / SB_FAILED_ARTIFACT_TTL_HOURS, setters for tests).
const (
	defaultJobTimeout        = 6 * time.Hour
	defaultFailedArtifactTTL = 72 * time.Hour
)

func NewRunner(store *db.Store, key []byte, stagingDir string, recipient func(ctx context.Context) (string, error), log *slog.Logger) *Runner {
	return &Runner{
		store:             store,
		authDB:            store.DB,
		stagingDir:        stagingDir,
		cfg:               dumper.Config{StagingDir: stagingDir},
		key:               key,
		recipient:         recipient,
		log:               log,
		cancelFns:         make(map[int64]context.CancelFunc),
		perDB:             make(map[int64]bool),
		wake:              make(chan struct{}, 1),
		stopDone:          make(chan struct{}),
		destBackends:      make(map[int64]storage.Backend),
		verifyQueue:       make(chan verifyRequest, verifyQueueCap),
		verifyInFlight:    make(map[int64]bool),
		verifyTimeout:     defaultVerifyTimeout,
		jobTimeout:        defaultJobTimeout,
		failedArtifactTTL: defaultFailedArtifactTTL,
		backendFactory: func(ctx context.Context, d *Destination) (storage.Backend, error) {
			return storage.New(ctx, d.StorageConfig(), log)
		},
	}
}

// RecoverInterrupted marks every running job as interrupted at startup —
// never as succeeded, never re-run blindly (dev-plan P2 task 4) — then
// reconciles committed artifacts against job references (round-1 review
// P1-12): every ciphertext gets a traceable owner, temp files are cleaned.
func (r *Runner) RecoverInterrupted(ctx context.Context) (int64, error) {
	res, err := r.authDB.ExecContext(ctx,
		`UPDATE jobs SET status = 'interrupted', finished_at = strftime('%s','now')
		 WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	// Interrupted jobs WITH a committed artifact keep it; those without one
	// but showing an artifact path get the contradiction recorded.
	if _, err := r.authDB.ExecContext(ctx, `
		UPDATE jobs SET error_message = error_message ||
		  ' [recovery: artifact committed before interruption — restore manually or re-run]'
		WHERE status = 'interrupted' AND artifact_state IN ('committed','committed_no_manifest')
		  AND COALESCE(error_message,'') NOT LIKE '%[recovery:%'`); err != nil {
		return n, err
	}
	if _, err := r.authDB.ExecContext(ctx, `
		UPDATE jobs SET artifact_path = '', artifact_sha256 = '', artifact_size = 0,
		  manifest_path = ''
		WHERE status = 'interrupted' AND artifact_state = '' AND artifact_path != ''`); err != nil {
		return n, err
	}

	// FILE RECONCILIATION (round-5 review P1-12): scanning beats SQL-only
	// bookkeeping. Every committed ciphertext on disk must be referenced by
	// an interrupted/succeeded/failed job; every reference claiming a file
	// that is missing must be recorded. Nothing is deleted here.
	entries, rerr := os.ReadDir(r.stagingDir)
	if os.IsNotExist(rerr) {
		return n, nil // no staging dir yet: nothing to reconcile
	}
	if rerr != nil {
		return n, rerr
	}
	artRe := regexp.MustCompile(`^backup-job(\d+)\.dump\.age$`)
	for _, e := range entries {
		m := artRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		var jobID int64
		fmt.Sscanf(m[1], "%d", &jobID)
		var path, state string
		qerr := r.authDB.QueryRowContext(ctx,
			`SELECT artifact_path, artifact_state FROM jobs WHERE id = ?`, jobID).
			Scan(&path, &state)
		if errors.Is(qerr, sql.ErrNoRows) {
			r.log.Error("orphaned staging artifact with no job record", "file", e.Name())
			continue
		}
		if qerr != nil {
			return n, qerr
		}
		if path == "" {
			// Job lost its reference but the ciphertext survived: restore the
			// reference so the artifact is traceable and NOT deleted.
			if _, uerr := r.authDB.ExecContext(ctx, `
				UPDATE jobs SET artifact_path = ?, artifact_state = 'committed',
				  error_message = COALESCE(NULLIF(error_message,''),'') ||
				    ' [recovery: unreferenced ciphertext restored to this job]'
				WHERE id = ?`, filepath.Join(r.stagingDir, e.Name()), jobID); uerr != nil {
				r.log.Error("artifact reference restore failed", "job", jobID, "err", uerr)
			} else {
				r.log.Warn("recovered unreferenced ciphertext", "job", jobID, "file", e.Name())
			}
		}
		_ = state
	}
	// Same reconciliation for manifest files: a committed/failed job with an
	// empty manifest_path but an existing manifest on disk gets it restored.
	for _, e := range entries {
		mm := manifestRefRe.FindStringSubmatch(e.Name())
		if mm == nil {
			continue
		}
		var jobID int64
		fmt.Sscanf(mm[1], "%d", &jobID)
		var mpath string
		qerr := r.authDB.QueryRowContext(ctx,
			`SELECT COALESCE(manifest_path,'') FROM jobs WHERE id = ?`, jobID).Scan(&mpath)
		if qerr == nil && mpath == "" {
			full := filepath.Join(r.stagingDir, e.Name())
			if _, uerr := r.authDB.ExecContext(ctx,
				`UPDATE jobs SET manifest_path = ? WHERE id = ?`, full, jobID); uerr == nil {
				r.log.Warn("recovered unreferenced manifest", "job", jobID, "file", e.Name())
			}
		}
	}

	// References claiming files that no longer exist get flagged.
	rows, rerr := r.authDB.QueryContext(ctx, `
		SELECT id, artifact_path FROM jobs
		WHERE artifact_path != '' AND status IN ('interrupted','succeeded','failed')`)
	if rerr != nil {
		return n, rerr
	}
	defer rows.Close()
	type missing struct {
		id   int64
		path string
	}
	var missingList []missing
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			return n, err
		}
		if _, serr := os.Stat(p); serr != nil {
			missingList = append(missingList, missing{id, p})
		}
	}
	rows.Close()
	for _, mm := range missingList {
		if _, uerr := r.authDB.ExecContext(ctx, `
			UPDATE jobs SET error_message = COALESCE(NULLIF(error_message,''),'') ||
			  ' [recovery: referenced artifact file is MISSING from staging]'
			WHERE id = ? AND COALESCE(error_message,'') NOT LIKE '%[recovery:%'`, mm.id); uerr != nil {
			r.log.Error("missing-artifact annotation failed", "job", mm.id, "err", uerr)
		} else {
			r.log.Warn("referenced artifact missing from staging", "job", mm.id, "path", mm.path)
		}
	}
	return n, nil
}

// Enqueue creates a pending job for a database. The partial unique index
// (idx_jobs_active_per_database) makes the no-overlap promise atomic: two
// concurrent enqueues cannot both succeed (round-1 review P1-05).
func (r *Runner) Enqueue(ctx context.Context, databaseID int64) (int64, error) {
	return r.EnqueueTx(ctx, r.authDB, databaseID)
}

// EnqueueTx creates a pending job within the given DB handle, enabling the
// scheduler to make cursor advance and job creation atomic (round-4 review
// P1-01).
func (r *Runner) EnqueueTx(ctx context.Context, dbh dbExec, databaseID int64) (int64, error) {
	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		return 0, fmt.Errorf("generate backup uuid: %w", err)
	}
	backupUUID := hex.EncodeToString(uuidBytes)
	res, err := dbh.ExecContext(ctx,
		`INSERT INTO jobs (database_id, backup_uuid, status, scheduled_at, created_at)
		 VALUES (?, ?, 'pending', strftime('%s','now'), strftime('%s','now'))`,
		databaseID, backupUUID)
	if err != nil {
		// SQLite reports partial-unique violations against either the index
		// name or the underlying column, depending on the planner path.
		if strings.Contains(err.Error(), "idx_jobs_active_per_database") ||
			strings.Contains(err.Error(), "UNIQUE constraint failed: jobs.database_id") {
			return 0, ErrAlreadyQueued
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.signalWake()
	return id, nil
}

// ResumeRemotePhase re-runs the remote upload/commit for interrupted jobs
// that have a committed local artifact and an upload intent (protocol C
// restart convergence: "重启不能补提交" — round-3 review P1-03). Runs
// synchronously at startup, before the worker starts claiming new jobs.
func (r *Runner) ResumeRemotePhase(ctx context.Context) {
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, database_id, artifact_path, artifact_sha256, artifact_size, manifest_path, destination_id
		FROM jobs
		WHERE status = 'interrupted' AND artifact_state IN ('committed','committed_no_manifest')
		  AND remote_state IN ('uploading','committed')`)
	if err != nil {
		r.log.Error("resume remote phase query", "err", err)
		return
	}
	type job struct {
		id, dbID     int64
		artifactPath string
		sha256       string
		size         int64
		manifestPath string
		destID       sql.NullInt64
	}
	var jobsList []job
	for rows.Next() {
		var j job
		var d sql.NullInt64
		if err := rows.Scan(&j.id, &j.dbID, &j.artifactPath, &j.sha256, &j.size, &j.manifestPath, &d); err != nil {
			rows.Close()
			r.log.Error("resume scan", "err", err)
			return
		}
		j.destID = d
		jobsList = append(jobsList, j)
	}
	rows.Close()

	for _, j := range jobsList {
		if !j.destID.Valid {
			continue
		}
		if _, err := os.Stat(j.artifactPath); err != nil {
			r.log.Warn("resume: staged artifact gone, job stays interrupted", "job", j.id, "path", j.artifactPath)
			continue
		}
		mb, merr := os.ReadFile(j.manifestPath)
		if merr != nil {
			r.log.Error("resume: manifest unreadable", "job", j.id, "err", merr)
			continue
		}
		dest, derr := r.GetDestination(ctx, j.destID.Int64)
		if derr != nil {
			r.log.Error("resume: destination gone", "job", j.id, "err", derr)
			continue
		}
		// Restore to running so the remote phase can execute with the
		// production contract, then re-run it. State repairs use the
		// context-free handle ON PURPOSE: a SIGTERM during this synchronous
		// startup pass cancels ctx, and the repairs below must still land
		// (fix review, reliability P2-2/P2-3).
		if _, err := r.authDB.Exec(
			`UPDATE jobs SET status = 'running' WHERE id = ? AND cancel_requested = 0`, j.id); err != nil {
			r.log.Error("resume: restore running", "job", j.id, "err", err)
			continue
		}
		var cancelReq int
		if err := r.authDB.QueryRowContext(ctx,
			`SELECT cancel_requested FROM jobs WHERE id = ?`, j.id).Scan(&cancelReq); err == nil && cancelReq == 1 {
			r.finalizeCanceled(j.id)
			continue
		}
		upload := &uploadedArtifact{
			artifactPath:  j.artifactPath,
			artifactSize:  j.size,
			sha256Hex:     j.sha256,
			manifestPath:  j.manifestPath,
			manifestBytes: mb,
		}
		// A hung destination must not pin the synchronous startup pass
		// before the HTTP listener exists (overall review P1-K2 remainder):
		// each resume carries the same budget as a normal job.
		resumeCtx := ctx
		if r.jobTimeout > 0 {
			var cancelResume context.CancelFunc
			resumeCtx, cancelResume = context.WithTimeout(ctx, r.jobTimeout)
			defer cancelResume()
		}
		if uerr := r.uploadAndCommitRemote(resumeCtx, j.id, j.dbID, upload, dest); uerr != nil {
			r.log.Error("resume: remote phase failed", "job", j.id, "err", uerr)
			// A dead context here is shutdown (parent signal ctx) or the
			// resume budget — NEVER a verdict on the backup itself. Restore
			// `interrupted` with the artifact and upload intent intact so
			// the next startup retries, instead of a false `failed` that
			// would notify, ping /fail and permanently retire the job
			// (overall review P1-R1 remainder; fix review reliability P2-2).
			if resumeCtx.Err() != nil {
				if _, ierr := r.authDB.Exec(`
					UPDATE jobs SET status = 'interrupted'
					WHERE id = ? AND status = 'running' AND cancel_requested = 0`, j.id); ierr != nil {
					r.log.Error("resume: shutdown restore failed", "job", j.id, "err", ierr)
				}
				return
			}
			r.fail(j.id, ClassStorageUp, pgclient.SanitizeMessage(uerr.Error()))
			continue
		}
		// Success record — mirrors runJob's terminal write: platform and
		// the initial verification state ride along so a resumed commit
		// never produces a succeeded job with an empty verify status
		// (round-2 P2-02). Its verification is queued by the startup
		// sweep (ResumePendingVerifications) once the worker is running.
		verifyStatus, verifyDetail := r.initialVerifyState()
		detected := platformpkg.Generic
		if _, _, _, connEnc, lerr := r.loadDatabase(ctx, j.dbID); lerr == nil {
			if plainPlain, derr := crypto.Decrypt(r.key, connEnc); derr == nil {
				if ci, perr := pgclient.ParseURI(string(plainPlain)); perr == nil {
					detected = platformpkg.Detect(ci.Host)
				}
			}
		}
		if _, uerr := r.authDB.Exec(`
			UPDATE jobs SET status = 'succeeded', finished_at = strftime('%s','now'),
			  manifest_path = ?, platform = ?, verify_status = ?, verify_detail = ?
			WHERE id = ? AND status = 'running' AND cancel_requested = 0`,
			j.manifestPath, string(detected), verifyStatus, verifyDetail, j.id); uerr != nil {
			r.log.Error("resume: success update failed", "job", j.id, "err", uerr)
		} else {
			r.log.Info("resumed remote commit completed", "job", j.id)
		}
	}
}

// Start launches the single-concurrency worker and — when a verifier is
// configured — the single verification worker plus startup convergence for
// verifications that were pending at shutdown (phase-5 review P1-06).
func (r *Runner) Start(parent context.Context) {
	r.startOnce.Do(func() {
		r.lifeCtx, r.lifeCancel = context.WithCancel(parent)
		r.wg.Go(func() {
			for {
				select {
				case <-r.lifeCtx.Done():
					return
				case <-r.wake:
				case <-time.After(2 * time.Second): // safety poll; wake is the fast path
				}
				for r.claimAndRun(r.lifeCtx) {
					// drain queued jobs one by one (global concurrency = 1)
				}
			}
		})
		if r.verifier != nil {
			r.startVerifyWorker(r.lifeCtx)
		}
		// The startup sweep runs even without a verifier: it settles stale
		// pending/running states to an explicit skip in that case. It joins
		// the WaitGroup, so Stop waits for its writes (round-2 P1-06/P2-02).
		r.wg.Go(func() {
			r.ResumePendingVerifications(r.lifeCtx)
		})
		// Periodic staging reclamation (overall review P1-K1): the startup
		// call happens in main right after ResumeRemotePhase; this ticker
		// keeps long-running instances from accumulating dead artifacts.
		r.wg.Go(func() {
			tick := time.NewTicker(6 * time.Hour)
			defer tick.Stop()
			for {
				select {
				case <-r.lifeCtx.Done():
					return
				case <-tick.C:
					r.PruneExpiredArtifacts(r.lifeCtx)
				}
			}
		})
	})
}

// signalWake non-blockingly nudges the worker loop.
func (r *Runner) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// SetClientOverride points the dumper at a specific pg_dump directory
// (test hook for fault-injecting fakes).
func (r *Runner) SetClientOverride(dir string) { r.cfg.BinDirOverride = dir }

// SetQuota wires the staging hard budget into the dumper (round-1 review
// P1-13: the quota must actually reach the kernel, not just the config).
func (r *Runner) SetQuota(bytes int64) { r.cfg.QuotaBytes = bytes }

// SetLocalKeep configures how many local staged artifacts per database are
// retained (protocol D local half; newest is always protected).
func (r *Runner) SetLocalKeep(n int) { r.localKeep = clampKeep(n, 1) }

// SetJobTimeout overrides the per-job wall-clock budget (test hook;
// SB_JOB_TIMEOUT in production). 0 disables the budget.
func (r *Runner) SetJobTimeout(d time.Duration) { r.jobTimeout = d }

// SetFailedArtifactTTL overrides the grace window before failed/canceled/
// interrupted artifacts are reclaimed from staging (test hook;
// SB_FAILED_ARTIFACT_TTL_HOURS in production). 0 keeps everything.
func (r *Runner) SetFailedArtifactTTL(d time.Duration) { r.failedArtifactTTL = d }

// SetStatsRecorder wires the statistics persistence layer.
func (r *Runner) SetStatsRecorder(sr *stats.Recorder) { r.statsRecorder = sr }

// Stop terminates the worker.
func (r *Runner) Stop() {
	// "Cancel once" and "every caller waits for completion" are two separate
	// requirements (round-3 review R3-P2-01): concurrent Stops must all join
	// before returning.
	r.stopOnce.Do(func() {
		if r.lifeCancel != nil {
			r.lifeCancel()
		}
		r.wg.Wait()
		close(r.stopDone)
	})
	<-r.stopDone
}

// Cancel requests cancellation of a pending or running job. A PENDING job
// transitions to `canceled` atomically right here; a RUNNING job is flipped
// to canceled at its final update if cancellation wins the race (round-2
// review P1-04).
func (r *Runner) Cancel(jobID int64) error {
	res, err := r.authDB.Exec(
		`UPDATE jobs SET status = 'canceled', cancel_requested = 1,
		    finished_at = strftime('%s','now')
		 WHERE id = ? AND status = 'pending'`, jobID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil // pending job settled without ever running
	}
	res, err = r.authDB.Exec(
		`UPDATE jobs SET cancel_requested = 1
		 WHERE id = ? AND status = 'running'`, jobID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %d is not pending or running", jobID)
	}
	// ALWAYS unlock before invoking the callback: the cancelled worker
	// re-enters Runner methods while unwinding, and holding r.mu across fn()
	// deadlocked the only worker plus Stop (round-3 review R3-P1-01).
	r.mu.Lock()
	fn, ok := r.cancelFns[jobID]
	delete(r.cancelFns, jobID)
	r.mu.Unlock()
	if ok {
		fn()
		return nil
	}
	// Registration handshake window: cancel landed before the worker
	// registered its context. Poll briefly, managed by the runner lifetime.
	r.mu.Lock()
	lc := r.lifeCtx
	r.mu.Unlock()
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for range 40 {
			select {
			case <-lc.Done():
				return
			case <-tick.C:
			}
			r.mu.Lock()
			fn, ok := r.cancelFns[jobID]
			delete(r.cancelFns, jobID)
			r.mu.Unlock()
			if ok {
				fn()
				return
			}
		}
	}()
	return nil
}

// claimAndRun claims the oldest pending job that is not for a database
// currently being processed (round-2 review P2-01: skip, never rekey).
// Returns false when nothing was claimable.
func (r *Runner) claimAndRun(ctx context.Context) bool {
	// Scan ALL pending candidates in order so a busy database does not block
	// other runnable databases (round-2 review P2-01).
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT j.id, j.database_id FROM jobs j
		WHERE j.status = 'pending' AND j.cancel_requested = 0
		ORDER BY j.id LIMIT 50`)
	if err != nil {
		return false
	}
	type cand struct{ id, dbID int64 }
	var candidates []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.dbID); err != nil {
			rows.Close()
			return false
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if len(candidates) == 0 {
		// Nothing claimable — settle any cancel-requested pending jobs so
		// `canceled` is actually reachable (round-1 review P1-04).
		r.settleCanceledPending()
		return false
	}

	r.mu.Lock()
	var id, dbID int64
	found := false
	for _, c := range candidates {
		if !r.perDB[c.dbID] {
			id, dbID = c.id, c.dbID
			found = true
			break
		}
	}
	if !found {
		r.mu.Unlock()
		return false
	}
	r.perDB[dbID] = true
	r.mu.Unlock()

	res, err := r.authDB.Exec(
		`UPDATE jobs SET status = 'running', started_at = strftime('%s','now')
		 WHERE id = ? AND status = 'pending' AND cancel_requested = 0`, id)
	if err != nil {
		r.mu.Lock()
		delete(r.perDB, dbID)
		r.mu.Unlock()
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Lost a race with Cancel — settle it and move on.
		r.settleCanceledPending()
		r.mu.Lock()
		delete(r.perDB, dbID)
		r.mu.Unlock()
		return true
	}

	jobCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancelFns[id] = cancel
	r.mu.Unlock()
	// Handshake: the cancel flag may have been set between claim and
	// registration — re-read and honor it now.
	var cancelReq int
	if err := r.authDB.QueryRow(`SELECT cancel_requested FROM jobs WHERE id = ?`, id).Scan(&cancelReq); err == nil && cancelReq == 1 {
		cancel()
	}

	r.runJob(jobCtx, id, dbID)

	r.mu.Lock()
	delete(r.cancelFns, id)
	delete(r.perDB, dbID)
	r.mu.Unlock()
	cancel()

	// If cancel was requested but the job finished successfully first, honor
	// the explicit user intent? No: protocol A says a COMPLETED commit is a
	// completed backup. Cancel only affects pending/running work.
	return true
}

// settleCanceledPending flips pending jobs with cancel_requested=1 to
// canceled — the only place 'canceled' is written (round-1 review P1-04).
func (r *Runner) settleCanceledPending() {
	_, _ = r.authDB.Exec(`
		UPDATE jobs SET status = 'canceled', finished_at = strftime('%s','now')
		WHERE status = 'pending' AND cancel_requested = 1`)
}

// runJob executes one backup end-to-end and records the outcome. It never
// panics into the worker loop.
func (r *Runner) runJob(ctx context.Context, jobID, dbID int64) {
	// Wall-clock budget over the WHOLE job — dump, upload and read-back (the
	// restore-verification phase carries its own). Global concurrency is 1,
	// so one hung endpoint would otherwise pin every database's backups
	// forever (overall review P1-K2). 0 = unlimited.
	if r.jobTimeout > 0 {
		var cancelBudget context.CancelFunc
		ctx, cancelBudget = context.WithTimeout(ctx, r.jobTimeout)
		defer cancelBudget()
	}

	// Known secrets, populated once the credentials are decrypted; every
	// failure path redacts them from stored/logged text (round-3 review
	// P1-02 remainder — labeled-but-kept values still leak).
	var knownSecrets []string
	redact := func(msg string) string {
		return pgclient.SanitizeMessage(redactpkg.Secrets(knownSecrets, msg))
	}
	defer func() {
		if rec := recover(); rec != nil {
			r.log.Error("job panic", "job", jobID, "panic", redact(fmt.Sprint(rec)))
			// A panic racing the shutdown must not land a false `failed`
			// either (fix review, reliability P3-1): settleCtxGone first,
			// real failure only when the context is healthy.
			if !r.settleCtxGone(ctx, jobID) {
				r.fail(jobID, ClassUnknown, redact(fmt.Sprintf("internal panic: %v", rec)))
			}
		}
	}()

	// Load target credentials (protocol B: decrypt with the master secret).
	name, platform, envTag, connEnc, err := r.loadDatabase(ctx, dbID)
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassUnknown, "load database record: "+err.Error())
		return
	}
	plain, err := crypto.Decrypt(r.key, connEnc)
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassUnknown,
			"stored credentials are unreadable — the master secret changed or the data is corrupted; re-add the database")
		return
	}
	if r.settleCtxGone(ctx, jobID) {
		return
	}
	ci, err := pgclient.ParseURI(string(plain))
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassUnknown, "stored connection URI invalid: "+err.Error())
		return
	}
	knownSecrets = append(knownSecrets, ci.Password)
	// Platform detection is host-characteristic only (never re-detected from
	// untrusted input later); it drives the recovery kit and is persisted on
	// the job so API consumers see the same classification the kit used.
	detectedPlatform := platformpkg.Detect(ci.Host)
	if hint := platformpkg.PoolingHint(ci.Host, ci.Port); !hint.Empty() {
		r.log.Warn("pooling hint", "job", jobID, "database", name, "hint", hint.En)
	}

	// Resolve the destination EARLY (before the dump): the snapshot is used
	// for the remote phase and MUST NOT be re-read after the dump completes
	// (round-3 phase-3 review P1-02: a concurrent un-assign could redirect
	// the upload or cause the job to be wrongly reported as succeeded).
	destSnapshot, destErr := r.DestinationForDatabase(ctx, dbID)
	if destErr != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassUnknown, redact("resolve destination: "+destErr.Error()))
		return
	}
	if destSnapshot != nil {
		knownSecrets = append(knownSecrets, destSnapshot.Secrets()...)
	}

	recipient, err := r.recipient(ctx)
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassUnknown, redact("age recipient lookup failed: "+err.Error()))
		return
	}
	if recipient == "" {
		r.fail(jobID, ClassUnknown, redact("age recipient not configured; run `supabackup age init` first"))
		return
	}

	// 1) Connection test + version (pgx — same semantics as pg_dump).
	test, err := pgclient.Test(ctx, ci)
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, classify(err), redact("connection test failed: "+err.Error()))
		return
	}

	// 2) Dependency collection (protocol E manifest payload).
	deps, err := pgclient.CollectDependencies(ctx, ci)
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, classify(err), redact("dependency collection failed: "+err.Error()))
		return
	}

	// 3) Dump + encrypt + atomic commit (protocol A).
	// (A user cancel that lands mid-dump is finalized as `canceled`; instance
	// shutdown lands as `interrupted` right here — no failure notification,
	// no /fail heartbeat. An expired job budget is a real failure, classified
	// by settleCtxGone.)
	dumpStart := time.Now().UTC()
	result, err := r.cfg.Run(ctx, jobID, dumper.Target{
		Conn: ci, ServerMajor: test.ServerMajor, Recipient: recipient,
	})
	if err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, classify(err), redact(err.Error()))
		return
	}

	// The ciphertext is COMMITTED (protocol A done). Persist its reference
	// immediately, before anything else can fail (round-1 review P1-12):
	// a later manifest/db failure leaves a traceable, restorable artifact.
	manifestPath := result.ArtifactPath + ".manifest.json"
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET artifact_path = ?, artifact_sha256 = ?, artifact_size = ?,
		  artifact_state = 'committed'
		WHERE id = ?`, result.ArtifactPath, result.SHA256, result.SizeBytes, jobID); err != nil {
		r.fail(jobID, ClassDisk, redact("artifact reference could not be persisted: "+err.Error()))
		return
	}

	// 4) Manifest (protocol E) — self-describing restore payload. Fields are
	// honest about what v1 does NOT verify (round-1 review P1-10).
	now := time.Now().UTC()
	m := &manifest.Manifest{
		BackupID:      fmt.Sprintf("job-%d", jobID),
		DumpStartedAt: dumpStart,
		FinishedAt:    now,
		Database:      manifest.Database{Name: name, Platform: platform, EnvTag: envTag},
		Source: manifest.Source{Host: ci.Host, Port: ci.Port, DBName: ci.DBName, ServerVersion: test.ServerVersion,
			PhysicalSizeBytes: deps.SourceDBBytes},
		Backup: manifest.Backup{
			Mode: "full", Format: "pg_dump custom (-Fc)",
			Compression:  "zlib/gzip (custom-format default; algorithm set by the pg_dump build)",
			ToolVersions: manifest.ToolVersions{PGDump: result.DumpToolVer, ClientMajor: result.ClientMajor, Age: "filippo.io/age"},
			RestoreNote: fmt.Sprintf(
				"restore into PostgreSQL >= %d; restoring into OLDER majors than the dumping client major (%d) is NOT guaranteed by PostgreSQL and has NOT been verified by this tool",
				result.ClientMajor, result.ClientMajor),
		},
		Selection: manifest.Selection{Rule: "entire database (all user schemas); foreign-table DATA is NOT included (definitions only); large objects included"},
		Dependencies: manifest.Deps{
			Extensions:       toManifestExts(deps.Extensions),
			Roles:            deps.Roles,
			HasLargeObjects:  deps.HasLargeObjects,
			HasForeignTables: deps.HasForeignTables,
			ServerEncoding:   deps.ServerEncoding,
			TableCount:       deps.TableCount,
		},
		Verification: manifest.Verification{
			RestoreVerified: false,
			// Snapshot semantics (P2-02): this file is written at backup
			// time, BEFORE the asynchronous restore verification runs. The
			// authoritative, live verification state lives on the job record
			// (GET /api/tasks/{id}) — never in this snapshot.
			Note: "verification SNAPSHOT AT BACKUP TIME: restore-verification had not run when this manifest was written. Check the job's verifyStatus via the API/UI for the authoritative, possibly newer outcome; use the recovery kit and record the drill result.",
		},
		Archive: manifest.Archive{
			FileName:   filepath.Base(result.ArtifactPath),
			SHA256:     result.SHA256,
			SizeBytes:  result.SizeBytes,
			Encryption: manifest.Encryption{Type: "age", KeyID: result.EncryptedTo, Recipient: recipient},
		},
	}
	mb, err := manifest.Marshal(m)
	if err == nil {
		err = durableWriteFile(manifestPath, mb)
	}
	if err != nil {
		// The artifact is committed but its manifest failed: keep the
		// artifact reference (it is restorable manually), flag the state,
		// and mark the job failed — never report success (round-1 P1-12).
		if _, uerr := r.authDB.Exec(`
			UPDATE jobs SET artifact_state = 'committed_no_manifest' WHERE id = ?`, jobID); uerr != nil {
			r.log.Error("artifact state update failed", "job", jobID, "err", uerr)
		}
		r.fail(jobID, ClassDisk,
			"manifest write failed; the CIPHERTEXT IS committed and restorable manually (artifact reference retained), but this job is marked failed: "+err.Error())
		return
	}

	// 5) Remote upload phase (protocol C) — when a destination is assigned.
	// Success of this phase is recorded via remote_state; failure fails the
	// job with class storage_upload while keeping the local artifact and the
	// upload intent (never re-dump).
	upload := &uploadedArtifact{
		artifactPath:  result.ArtifactPath,
		artifactSize:  result.SizeBytes,
		sha256Hex:     result.SHA256,
		manifestPath:  manifestPath,
		manifestBytes: mb,
	}
	if err := r.uploadAndCommitRemote(ctx, jobID, dbID, upload, destSnapshot); err != nil {
		if r.settleCtxGone(ctx, jobID) {
			return
		}
		r.fail(jobID, ClassStorageUp, redact(err.Error()))
		return
	}

	// 6) Success record — the only path that reaches 'succeeded'. The final
	// update is conditional (cancellation arbitration) and checks
	// RowsAffected (round-1 reviews P1-12/P1-04). The detected platform and
	// the initial verification state are persisted in the SAME update so no
	// observer ever sees a succeeded job without a verification state
	// (phase-5 review P2-02).
	verifyStatus, verifyDetail := r.initialVerifyState()
	dumpDuration := time.Since(dumpStart).Seconds()
	successSQL := `
		UPDATE jobs SET status = 'succeeded', finished_at = strftime('%s','now'),
		  artifact_path = ?, artifact_sha256 = ?, artifact_size = ?, manifest_path = ?,
		  platform = ?, verify_status = ?, verify_detail = ?, source_db_bytes = ?,
		  duration_secs = ?
		WHERE id = ? AND status = 'running' AND cancel_requested = 0`
	res, err := r.authDB.Exec(successSQL,
		result.ArtifactPath, result.SHA256, result.SizeBytes, manifestPath,
		string(detectedPlatform), verifyStatus, verifyDetail, deps.SourceDBBytes,
		dumpDuration, jobID)
	if err != nil {
		// SQLITE_BUSY/ENOSPC here would leave a committed artifact with a
		// 'running' job — bounded retry, then a loud failure record.
		time.Sleep(500 * time.Millisecond)
		res, err = r.authDB.Exec(successSQL,
			result.ArtifactPath, result.SHA256, result.SizeBytes, manifestPath,
			string(detectedPlatform), verifyStatus, verifyDetail, deps.SourceDBBytes,
			dumpDuration, jobID)
	}
	if err != nil {
		r.log.Error("job success update failed twice", "job", jobID, "err", err)
		r.fail(jobID, ClassDisk, redact("backup completed but the success record could not be written: "+err.Error()))
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		// Cancellation won the race: the committed ciphertext stays, but the
		// job is honored as canceled, NOT succeeded.
		if _, uerr := r.authDB.Exec(`
			UPDATE jobs SET status = 'canceled', finished_at = strftime('%s','now')
			WHERE id = ? AND status = 'running' AND cancel_requested = 1`, jobID); uerr != nil {
			r.log.Error("cancel finalization failed", "job", jobID, "err", uerr)
		}
		r.log.Info("job canceled after commit; artifact retained", "job", jobID)
		return
	}
	r.log.Info("backup succeeded", "job", jobID, "database", name,
		"bytes", result.SizeBytes, "sha256", result.SHA256[:16],
		"stderr_excerpt", redactpkg.Secrets(knownSecrets, result.StdErrExcerpt))
	// Dead-man switch (Phase 7): bound to REAL freshness — only a remotely
	// committed backup whose SNAPSHOT is still within period+grace pings.
	// uploadAndCommitRemote returned nil above: the ciphertext is either
	// remotely committed (destination assigned) or local-only by design.
	remoteCommitted := true
	dec := r.heartbeatFor(dbID, dumpStart, remoteCommitted, destSnapshot != nil)
	r.pingSuccess(dbID, dec)

	// Recovery kit (Phase 5): platform-aware restore.sh, persisted and
	// referenced on the job so the API can serve it (phase-5 review P1-09).
	// Generation failure is logged, never fails the committed backup; the
	// startup backfill regenerates missing kits.
	kitPath := manifestPath + ".restore.sh"
	kitScript := recovery.GenerateRestoreScriptWithTables(recovery.KitInput{
		BackupUUID:       fmt.Sprintf("job-%d", jobID),
		Platform:         detectedPlatform,
		ArtifactFileName: filepath.Base(result.ArtifactPath),
		SHA256:           result.SHA256,
		KeyID:            result.EncryptedTo,
	}, deps.TableCount)
	if kwerr := durableWriteFile(kitPath, []byte(kitScript)); kwerr != nil {
		r.log.Error("recovery kit generation failed", "job", jobID, "err", kwerr)
	} else if _, uerr := r.authDB.Exec(`
		UPDATE jobs SET recovery_kit_path = ? WHERE id = ?`, kitPath, jobID); uerr != nil {
		r.log.Error("recovery kit reference write failed", "job", jobID, "err", uerr)
	}

	// Async restore verification (Phase 6): queued to the single bound
	// worker; states pending → running → verified/failed/unsupported, or an
	// explicit skipped reason. No goroutine escapes the Runner lifecycle.
	if r.verifier != nil && verifyStatus == "pending" {
		r.enqueueVerification(jobID, redact)
	}

	// Record statistics (Phase 8): best-effort, never affects the job. The
	// verify status recorded here is the state AT RECORD TIME (pending or an
	// explicit skip reason) — the API serves the authoritative live value
	// from the jobs row, never this snapshot (phase-5 review P2-02).
	if r.statsRecorder != nil {
		if err := r.statsRecorder.Record(ctx, stats.Entry{
			JobID: jobID, DatabaseName: name,
			SourceDBBytes: deps.SourceDBBytes,
			DumpSize:      result.PlaintextArc, ArtifactSize: result.SizeBytes,
			DurationSecs: dumpDuration,
			VerifyStatus: verifyStatus,
		}); err != nil {
			r.log.Error("stats recording failed", "job", jobID, "err", err)
		}
	}

	// 7) Retention (protocol D, beta simple form) — runs only after a
	// successful commit; failures are logged and never affect the job.
	r.pruneLocalArtifacts(ctx, dbID, r.localKeep)
	if dest, derr := r.DestinationForDatabase(ctx, dbID); derr == nil && dest != nil {
		if backend, berr := r.BuildBackend(ctx, dest); berr == nil {
			r.runRemoteRetention(ctx, dest, backend, dbID)
		} else {
			r.log.Error("retention: build backend failed", "err", berr)
		}
	}
}

func (r *Runner) loadDatabase(ctx context.Context, dbID int64) (name, platform, envTag, connEnc string, err error) {
	err = r.authDB.QueryRowContext(ctx,
		`SELECT name, platform, env_tag, conn_encrypted FROM databases
		 WHERE id = ? AND deleted_at IS NULL`, dbID).
		Scan(&name, &platform, &envTag, &connEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", ErrDatabaseNotFound
	}
	return
}

// finalizeCanceled writes the `canceled` terminal state for a running job
// whose context died from a user cancel (round-1 review P1-04).
func (r *Runner) finalizeCanceled(jobID int64) {
	if _, uerr := r.authDB.Exec(`
		UPDATE jobs SET status = 'canceled', finished_at = strftime('%s','now')
		WHERE id = ? AND status = 'running' AND cancel_requested = 1`, jobID); uerr != nil {
		r.log.Error("cancel finalization failed", "job", jobID, "err", uerr)
	}
}

// ranToCancellation reports whether this context died from a user cancel
// (cancel_requested set) as opposed to instance shutdown.
func (r *Runner) ranToCancellation(jobID int64) bool {
	var flag int
	_ = r.authDB.QueryRow(`SELECT cancel_requested FROM jobs WHERE id = ?`, jobID).Scan(&flag)
	return flag == 1
}

// interruptForShutdown writes the `interrupted` state for a running job whose
// context died from instance shutdown (docker stop / upgrade / host reboot):
// no failure notification, no /fail heartbeat. Startup convergence still
// applies — ResumeRemotePhase re-uploads committed artifacts, and the next
// cron tick produces a fresh backup regardless. The conditional UPDATE keeps
// cancel arbitration on the cancel path (overall review P1-R1: graceful
// shutdown previously landed here as a false `failed`).
func (r *Runner) interruptForShutdown(jobID int64) {
	res, err := r.authDB.Exec(`
		UPDATE jobs SET status = 'interrupted', finished_at = strftime('%s','now')
		WHERE id = ? AND status = 'running' AND cancel_requested = 0`, jobID)
	if err != nil {
		r.log.Error("shutdown interruption failed", "job", jobID, "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.log.Info("job interrupted by instance shutdown", "job", jobID)
		return
	}
	// A user Cancel landed between our ranToCancellation probe and the
	// conditional UPDATE: finish the arbitration it requested instead of
	// leaving a zombie `running` row that would block this database's queue
	// until the next restart (fix review, reliability P2-1).
	r.finalizeCanceled(jobID)
}

// settleCtxGone finalizes a job whose execution context has ended and
// reports whether the caller must return without further failure handling.
// Three endings, three destinations:
//   - user cancel (cancel_requested set) → `canceled`
//   - instance shutdown (lifeCtx done)  → `interrupted`, silently
//   - the job's own budget (WithTimeout) → a real `failed` with an explicit
//     reason: a hung stage must be visible, never silently resumed
func (r *Runner) settleCtxGone(ctx context.Context, jobID int64) bool {
	if ctx.Err() == nil {
		return false
	}
	if r.ranToCancellation(jobID) {
		r.finalizeCanceled(jobID)
		return true
	}
	if r.lifeCtx != nil && r.lifeCtx.Err() != nil {
		r.interruptForShutdown(jobID)
		return true
	}
	r.fail(jobID, ClassNetwork,
		fmt.Sprintf("backup exceeded its execution budget (SB_JOB_TIMEOUT=%s); a stage hung past the deadline and the job was terminated", r.jobTimeout))
	return true
}

func (r *Runner) fail(jobID int64, class, msg string) {
	msg = pgclient.SanitizeMessage(msg)

	var dbID int64
	var dbName string
	// Attempt 1: state + notification in ONE transaction (Phase 7 outbox) —
	// a crash between them can never lose the notification.
	err := r.commitFailure(jobID, class, msg, true)
	if err != nil {
		// Attempt 2: state-only write (no notification inside the tx). The
		// scheduler reconciliation sweep (no time window, event_id-deduped)
		// provides the durable compensation for the missing notification.
		r.log.Error("atomic failure commit failed; falling back to state-only", "job", jobID, "err", err)
		err = r.commitFailure(jobID, class, msg, false)
		if err != nil {
			r.log.Error("job failure STATE WRITE failed twice; job stays active", "job", jobID, "err", err)
			return
		}
		// Compensation enqueue, best effort outside the tx.
		_ = r.authDB.QueryRow(`
			SELECT j.database_id, COALESCE(d.name, '')
			FROM jobs j LEFT JOIN databases d ON d.id = j.database_id
			WHERE j.id = ?`, jobID).Scan(&dbID, &dbName)
		if enqErr := outbox.Enqueue(context.Background(), r.authDB, r.failureEvent(jobID, dbID, dbName, class, msg), time.Now()); enqErr != nil {
			r.log.Error("failure notification compensation enqueue FAILED; the sweep must recover it", "job", jobID, "err", enqErr)
		}
	}
	// The failure is committed: tell the dead-man switch immediately so a
	// failing backup is visible without waiting for silence (P1-04).
	_ = r.authDB.QueryRow(`
		SELECT j.database_id, COALESCE(d.name, '')
		FROM jobs j LEFT JOIN databases d ON d.id = j.database_id
		WHERE j.id = ?`, jobID).Scan(&dbID, &dbName)
	r.pingFail(dbID)

	r.log.Error("backup failed", "job", jobID, "class", class, "error", msg)
}

// failureEvent builds the deduped backup_failed notification.
func (r *Runner) failureEvent(jobID, dbID int64, dbName, class, msg string) outbox.Event {
	return outbox.Event{
		EventID:      fmt.Sprintf("backup_failed:job:%d", jobID),
		EventType:    outbox.EventBackupFailed,
		DatabaseID:   dbID,
		DatabaseName: dbName,
		Payload: map[string]any{
			"event":         "backup_failed",
			"job_id":        jobID,
			"database_id":   dbID,
			"database":      dbName,
			"error_class":   class,
			"error_message": truncate(msg, 300),
		},
	}
}

// commitFailure writes the terminal failure state; withNotify also enqueues
// the notification in the SAME transaction (rolls back together on error).
func (r *Runner) commitFailure(jobID int64, class, msg string, withNotify bool) error {
	tx, err := r.authDB.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		UPDATE jobs SET status = 'failed', finished_at = strftime('%s','now'),
		  error_class = ?, error_message = ?
		WHERE id = ? AND status IN ('pending','running')`, class, msg, jobID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %d not in an active state", jobID)
	}
	if withNotify {
		var dbID int64
		var dbName string
		_ = tx.QueryRow(`
			SELECT j.database_id, COALESCE(d.name, '')
			FROM jobs j LEFT JOIN databases d ON d.id = j.database_id
			WHERE j.id = ?`, jobID).Scan(&dbID, &dbName)
		if err := outbox.EnqueueTx(context.Background(), tx, r.failureEvent(jobID, dbID, dbName, class, msg), time.Now()); err != nil {
			return fmt.Errorf("outbox enqueue: %w", err)
		}
	}
	return tx.Commit()
}

// classify maps kernel errors to the seven classes.
func classify(err error) string {
	var cls *pgclient.Classified
	if errors.As(err, &cls) {
		return string(cls.Class)
	}
	var dc *dumper.Classified
	if errors.As(err, &dc) {
		return string(dc.Class)
	}
	return ClassUnknown
}

// truncate bounds a string for notification payloads.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func toManifestExts(in []pgclient.Extension) []manifest.PgExtension {
	out := make([]manifest.PgExtension, 0, len(in))
	for _, e := range in {
		out = append(out, manifest.PgExtension{Name: e.Name, Version: e.Version})
	}
	return out
}

// manifestRefRe matches staging manifest files: backup-job<id>.dump.age.manifest.json
var manifestRefRe = regexp.MustCompile(`^backup-job(\d+)\.dump\.age\.manifest\.json$`)
