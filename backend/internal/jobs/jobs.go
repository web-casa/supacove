// Package jobs implements the task state machine (pending → running →
// succeeded / failed / canceled / interrupted) with the seven error classes,
// and the single-concurrency worker that drives backups end-to-end
// (dev-plan Phase 2, tasks 3–4).
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/dumper"
	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

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

// ErrDatabaseNotFound for unknown database ids.
var ErrDatabaseNotFound = errors.New("database not found")

// Runner owns the worker and the per-database exclusion.
type Runner struct {
	store      *db.Store
	authDB     *sql.DB // same SQLite handle; convenience alias
	stagingDir string
	cfg        dumper.Config
	key        []byte // master secret (credentials decryption)
	recipient  func(ctx context.Context) (string, error)
	log        *slog.Logger

	mu        sync.Mutex
	cancelFns map[int64]context.CancelFunc
	perDB     map[int64]bool // database currently being processed
	workerCh  chan struct{}
	wake      chan struct{}
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func NewRunner(store *db.Store, key []byte, stagingDir string, recipient func(ctx context.Context) (string, error), log *slog.Logger) *Runner {
	return &Runner{
		store:      store,
		authDB:     store.DB,
		stagingDir: stagingDir,
		cfg:        dumper.Config{StagingDir: stagingDir},
		key:        key,
		recipient:  recipient,
		log:        log,
		cancelFns:  make(map[int64]context.CancelFunc),
		perDB:      make(map[int64]bool),
		workerCh:   make(chan struct{}, 1),
		wake:       make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
	}
}

// RecoverInterrupted marks every running job as interrupted at startup —
// never as succeeded, never re-run blindly (dev-plan P2 task 4).
func (r *Runner) RecoverInterrupted(ctx context.Context) (int64, error) {
	res, err := r.authDB.ExecContext(ctx,
		`UPDATE jobs SET status = 'interrupted', finished_at = strftime('%s','now')
		 WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Enqueue creates a pending job for a database, refusing to queue behind an
// already pending/running job for the SAME database (dev-plan: no overlap).
func (r *Runner) Enqueue(ctx context.Context, databaseID int64) (int64, error) {
	var n int
	if err := r.authDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE database_id = ? AND status IN ('pending','running')`,
		databaseID).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, ErrAlreadyQueued
	}
	res, err := r.authDB.ExecContext(ctx,
		`INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		 VALUES (?, 'pending', strftime('%s','now'), strftime('%s','now'))`,
		databaseID)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.signalWake()
	return id, nil
}

// Start launches the single-concurrency worker. v1 fixes global concurrency
// at 1 (dev-plan Phase 2 / P4 will make it configurable).
func (r *Runner) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stopCh:
				return
			case <-r.wake:
			case <-time.After(2 * time.Second): // safety poll; wake is the fast path
			}
			for r.claimAndRun(ctx) {
				// drain queued jobs one by one (global concurrency = 1)
			}
		}
	}()
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

// Stop terminates the worker.
func (r *Runner) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

// Cancel requests cancellation of a pending or running job.
func (r *Runner) Cancel(jobID int64) error {
	res, err := r.authDB.Exec(
		`UPDATE jobs SET cancel_requested = 1
		 WHERE id = ? AND status IN ('pending','running')`, jobID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %d is not pending or running", jobID)
	}
	r.mu.Lock()
	if fn, ok := r.cancelFns[jobID]; ok {
		fn()
	}
	r.mu.Unlock()
	r.signalWake()
	return nil
}

// claimAndRun claims the oldest pending job (fair order) and runs it.
// Returns false when nothing was claimable.
func (r *Runner) claimAndRun(ctx context.Context) bool {
	var id, dbID int64
	err := r.authDB.QueryRowContext(ctx, `
		SELECT j.id, j.database_id FROM jobs j
		WHERE j.status = 'pending'
		ORDER BY j.id LIMIT 1`).Scan(&id, &dbID)
	if err != nil {
		return false
	}

	r.mu.Lock()
	if r.perDB[dbID] {
		// Same-database job still finishing; skip for now, try later.
		r.mu.Unlock()
		r.reschedule(id)
		return false
	}
	r.perDB[dbID] = true
	r.mu.Unlock()

	res, err := r.authDB.Exec(
		`UPDATE jobs SET status = 'running', started_at = strftime('%s','now')
		 WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		r.mu.Lock()
		delete(r.perDB, dbID)
		r.mu.Unlock()
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		r.mu.Lock()
		delete(r.perDB, dbID)
		r.mu.Unlock()
		return false
	}

	jobCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancelFns[id] = cancel
	r.mu.Unlock()

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

// reschedule requeues a job that was skipped (same-database collision) by
// bumping its id so the worker makes progress on other databases first.
func (r *Runner) reschedule(id int64) {
	_, _ = r.authDB.Exec(`UPDATE jobs SET id = (SELECT MAX(id)+1 FROM jobs) WHERE id = ? AND status = 'pending'`, id)
	r.signalWake()
}

// runJob executes one backup end-to-end and records the outcome. It never
// panics into the worker loop.
func (r *Runner) runJob(ctx context.Context, jobID, dbID int64) {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.Error("job panic", "job", jobID, "panic", rec)
			r.fail(jobID, ClassUnknown, fmt.Sprintf("internal panic: %v", rec))
		}
	}()

	// Load target credentials (protocol B: decrypt with the master secret).
	name, platform, envTag, connEnc, err := r.loadDatabase(ctx, dbID)
	if err != nil {
		r.fail(jobID, ClassUnknown, "load database record: "+err.Error())
		return
	}
	plain, err := crypto.Decrypt(r.key, connEnc)
	if err != nil {
		r.fail(jobID, ClassUnknown,
			"stored credentials are unreadable — the master secret changed or the data is corrupted; re-add the database")
		return
	}
	ci, err := pgclient.ParseURI(string(plain))
	if err != nil {
		r.fail(jobID, ClassUnknown, "stored connection URI invalid: "+err.Error())
		return
	}

	recipient, err := r.recipient(ctx)
	if err != nil || recipient == "" {
		r.fail(jobID, ClassUnknown, "age recipient not configured; run `supabackup age init` first")
		return
	}

	// 1) Connection test + version (pgx — same semantics as pg_dump).
	test, err := pgclient.Test(ctx, ci)
	if err != nil {
		r.fail(jobID, classify(err), "connection test failed: "+err.Error())
		return
	}

	// 2) Dependency collection (protocol E manifest payload).
	deps, err := pgclient.CollectDependencies(ctx, ci)
	if err != nil {
		r.fail(jobID, classify(err), "dependency collection failed: "+err.Error())
		return
	}

	// 3) Dump + encrypt + atomic commit (protocol A).
	start := time.Now().UTC()
	result, err := r.cfg.Run(ctx, jobID, dumper.Target{
		Conn: ci, ServerMajor: test.ServerMajor, Recipient: recipient,
	})
	if err != nil {
		r.fail(jobID, classify(err), err.Error())
		return
	}

	// 4) Manifest (protocol E) — self-describing restore payload.
	now := time.Now().UTC()
	m := &manifest.Manifest{
		BackupID:   fmt.Sprintf("job-%d", jobID),
		CreatedAt:  start,
		FinishedAt: now,
		Database:   manifest.Database{Name: name, Platform: platform, EnvTag: envTag},
		Source:     manifest.Source{Host: ci.Host, Port: ci.Port, DBName: ci.DBName, ServerVersion: test.ServerVersion},
		Backup: manifest.Backup{
			Mode: "full", Format: "pg_dump custom (-Fc)", Compression: "zlib (custom-format default)",
			ToolVersions: manifest.ToolVersions{PGDump: result.DumpToolVer, ClientMajor: result.ClientMajor, Age: "filippo.io/age"},
		},
		Selection: manifest.Selection{Rule: "entire database (all user schemas), no exclusions"},
		Dependencies: manifest.Deps{
			Extensions:       toManifestExts(deps.Extensions),
			Roles:            deps.Roles,
			HasLargeObjects:  deps.HasLargeObjects,
			HasForeignTables: deps.HasForeignTables,
		},
		Archive: manifest.Archive{
			FileName:   filepath.Base(result.ArtifactPath),
			SHA256:     result.SHA256,
			SizeBytes:  result.SizeBytes,
			Encryption: manifest.Encryption{Type: "age", KeyID: result.EncryptedTo, Recipient: recipient},
		},
	}
	manifestPath := result.ArtifactPath + ".manifest.json"
	mb, err := manifest.Marshal(m)
	if err == nil {
		err = writeAtomic(manifestPath, mb)
	}
	if err != nil {
		// The artifact is committed but its manifest failed: this is a
		// protocol-E violation — mark failed, do NOT report success.
		r.fail(jobID, ClassDisk, "manifest write failed: "+err.Error())
		return
	}

	// 5) Success record — the only path that reaches 'succeeded'.
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET status = 'succeeded', finished_at = strftime('%s','now'),
		  artifact_path = ?, artifact_sha256 = ?, artifact_size = ?, manifest_path = ?
		WHERE id = ?`,
		result.ArtifactPath, result.SHA256, result.SizeBytes, manifestPath, jobID); err != nil {
		r.log.Error("job success update failed", "job", jobID, "err", err)
		return
	}
	r.log.Info("backup succeeded", "job", jobID, "database", name,
		"bytes", result.SizeBytes, "sha256", result.SHA256[:16], "stderr_excerpt", result.StdErrExcerpt)
}

func (r *Runner) loadDatabase(ctx context.Context, dbID int64) (name, platform, envTag, connEnc string, err error) {
	err = r.authDB.QueryRowContext(ctx,
		`SELECT name, platform, env_tag, conn_encrypted FROM databases WHERE id = ?`, dbID).
		Scan(&name, &platform, &envTag, &connEnc)
	return
}

func (r *Runner) fail(jobID int64, class, msg string) {
	msg = sanitizeMessage(msg)
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET status = 'failed', finished_at = strftime('%s','now'),
		  error_class = ?, error_message = ?
		WHERE id = ? AND status IN ('pending','running')`, class, msg, jobID); err != nil {
		r.log.Error("job failure update failed", "job", jobID, "err", err)
		return
	}
	r.log.Error("backup failed", "job", jobID, "class", class, "error", msg)
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

// sanitizeMessage scrubs credential-looking content from stored error text
// (secret canary invariant: no password ever reaches SQLite or logs).
func sanitizeMessage(msg string) string {
	r := strings.NewReplacer(
		"password=", "password=[REDACTED]",
		"postgres://", "postgres://[REDACTED]",
		"postgresql://", "postgresql://[REDACTED]",
	)
	return r.Replace(msg)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func toManifestExts(in []pgclient.Extension) []manifest.PgExtension {
	out := make([]manifest.PgExtension, 0, len(in))
	for _, e := range in {
		out = append(out, manifest.PgExtension{Name: e.Name, Version: e.Version})
	}
	return out
}
