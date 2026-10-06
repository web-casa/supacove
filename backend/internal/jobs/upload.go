// Package jobs — protocol C (remote commit) implementation: intent →
// upload (reusing the staged ciphertext, never re-dumping) → verify
// (streamed read-back hash) → remote manifest publish → local commit.
// See dev-plan §0.5 protocol C and C.1.
package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/storage"
)

// uploadAttempts is the number of full-upload attempts with the SAME staged
// ciphertext (protocol C: "上传重试复用同一 artifact，绝不重新 dump").
const uploadAttempts = 3

var errVerifyMismatch = errors.New("read-back verification mismatch")

// uploadAndCommitRemote runs the remote half of protocol C for a committed
// local artifact. destSnapshot is the destination resolved at enqueue/claim
// time — this function NEVER re-reads the current binding (round-3 phase-3
// review P1-02: resume after un-assign must not silently commit to a new
// bucket). On any failure the job is failed with class storage_upload; the
// local staged artifact and the upload intent survive so nothing is lost
// and nothing needs a re-dump.
func (r *Runner) uploadAndCommitRemote(ctx context.Context, jobID, dbID int64, result *uploadedArtifact, destSnapshot *Destination) error {
	dest := destSnapshot
	if dest == nil {
		return nil // local-only mode: no remote phase
	}

	backend, err := r.BuildBackend(ctx, dest)
	if err != nil {
		return fmt.Errorf("build storage backend: %w", err)
	}

	destCfg := dest.StorageConfig()
	objKey := destCfg.BackupKey(r.jobUUID(ctx, jobID))
	manKey := destCfg.ManifestKey(r.jobUUID(ctx, jobID))

	// INTENT before any side effect (protocol C): a crash after this point
	// leaves a traceable 'uploading' state; a crash before it leaves a
	// purely local backup.
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET destination_id = ?, remote_state = 'uploading',
		  remote_object_key = ?, remote_manifest_key = ?
		WHERE id = ?`, dest.ID, objKey, manKey, jobID); err != nil {
		return fmt.Errorf("record upload intent: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Upload the ciphertext (streamed from the staged file; re-opened per
	// attempt, same artifact every time).
	var lastErr error
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, oerr := os.Open(result.artifactPath)
		if oerr != nil {
			return fmt.Errorf("open staged artifact for upload: %w", oerr)
		}
		upErr := backend.Put(ctx, objKey, f, -1)
		f.Close()
		if upErr == nil {
			lastErr = nil
			break
		}
		lastErr = upErr
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < uploadAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("upload ciphertext: %w", lastErr)
	}

	// C.1 verification is MANDATORY (round-3 review P1-01): it is the only
	// integrity check that catches provider-side corruption, same-size
	// replacement and truncated writes. The VerifyReadback flag controls
	// restore-verification scheduling (a different protocol), never whether
	// the ciphertext read-back happens.
	if vErr := verifyRemote(ctx, backend, objKey, result.sha256Hex, result.artifactSize); vErr != nil {
		// Network/read failures are not proof of corruption: keep the
		// remote ciphertext and do NOT delete (round-3 review P1-02:
		// "暂时读失败也会删除可能完好的密文"). Only confirmed hash
		// mismatch (provably our content, wrong bytes) triggers cleanup.
		if errors.Is(vErr, errVerifyMismatch) {
			if dErr := backend.Delete(ctx, objKey); dErr != nil {
				return fmt.Errorf("%w; ALSO failed to delete the corrupt remote object %s: %w", vErr, objKey, dErr)
			}
		}
		return vErr
	}

	// Publish the remote manifest — the REMOTE COMMIT MARK (protocol C),
	// only after verification succeeded.
	if err := backend.Put(ctx, manKey, bytes.NewReader(result.manifestBytes), int64(len(result.manifestBytes))); err != nil {
		return fmt.Errorf("publish remote manifest: %w", err)
	}

	if _, err := r.authDB.Exec(`
		UPDATE jobs SET remote_state = 'committed', remote_verified = 1, uploaded_at = strftime('%s','now')
		WHERE id = ?`, jobID); err != nil {
		return fmt.Errorf("record remote commit: %w", err)
	}
	return nil
}

// verifyRemote streams the remote object to EOF computing SHA-256, and
// checks both the size and the digest (protocol C.1).
func verifyRemote(ctx context.Context, backend storage.Backend, key, wantSHA string, wantSize int64) error {
	rc, _, err := backend.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("verification read-back: %w", err)
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(h, rc)
	if err != nil {
		return fmt.Errorf("verification read: %w", err)
	}
	if wantSize >= 0 && n != wantSize {
		return fmt.Errorf("%w: remote size %d, want %d", errVerifyMismatch, n, wantSize)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA {
		return fmt.Errorf("%w: remote sha256 %s, want %s", errVerifyMismatch, got, wantSHA)
	}
	return nil
}

// DiagnosticTester returns the diagnostic-test capability for a destination
// when the backend provides it (the S3-compatible store does; fakes may not).
func (r *Runner) DiagnosticTester(ctx context.Context, dest *Destination) (interface {
	DiagnosticTest(ctx context.Context) error
}, bool) {
	b, err := r.BuildBackend(ctx, dest)
	if err != nil {
		return nil, false
	}
	dt, ok := b.(interface {
		DiagnosticTest(ctx context.Context) error
	})
	return dt, ok
}

// TaskArtifactPath returns the local artifact path for a job (server-side
// use only; never serialized). Returns "" when absent.
func (r *Runner) TaskArtifactPath(ctx context.Context, jobID int64) (string, error) {
	var p string
	err := r.authDB.QueryRowContext(ctx,
		`SELECT COALESCE(artifact_path,'') FROM jobs WHERE id = ?`, jobID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p, err
}

// BuildBackendByID resolves the cached backend for a destination ID.
func (r *Runner) BuildBackendByID(ctx context.Context, destID int64) (storage.Backend, error) {
	dest, err := r.GetDestination(ctx, destID)
	if err != nil {
		return nil, err
	}
	return r.BuildBackend(ctx, dest)
}

// jobUUID resolves the backup UUID for a job (immutable identity for remote
// keys; P0-01 fix).
func (r *Runner) jobUUID(ctx context.Context, jobID int64) string {
	var u string
	_ = r.authDB.QueryRowContext(ctx,
		`SELECT backup_uuid FROM jobs WHERE id = ?`, jobID).Scan(&u)
	return u
}

// uploadedArtifact carries the local-commit facts the upload phase needs.
type uploadedArtifact struct {
	artifactPath  string
	artifactSize  int64
	sha256Hex     string
	manifestPath  string
	manifestBytes []byte
}

// stringsReader was removed: the AWS SDK for S3 requires a seekable body
// for content-hash computation on HTTP endpoints. Use bytes.NewReader.

// runRemoteRetention prunes remote objects per protocol D (beta simple
// form) after a successful remote commit. Never fails the job: retention
// failures are logged, and the anchor is untouchable.
func (r *Runner) runRemoteRetention(ctx context.Context, dest *Destination, backend storage.Backend, dbID int64) {
	keep := clampKeep(dest.KeepRemote, 1)
	cutoff := time.Time{}
	if dest.KeepDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -dest.KeepDays)
	}

	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, remote_object_key, remote_manifest_key,
		       COALESCE(uploaded_at, finished_at, created_at), COALESCE(verify_status,'')
		FROM jobs
		WHERE database_id = ? AND destination_id = ? AND remote_state = 'committed'
		ORDER BY id DESC`, dbID, dest.ID)
	if err != nil {
		r.log.Error("retention query failed", "database", dbID, "err", err)
		return
	}
	type candidate struct {
		id          int64
		objKey      string
		manKey      string
		at          int64
		verifyState string
	}
	var committed []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.objKey, &c.manKey, &c.at, &c.verifyState); err != nil {
			rows.Close() //nolint:sqlclosecheck // rows are fully consumed and closed BEFORE the write loop (round-2 P2-02): holding a read cursor across writes is the hazard this rule misses
			r.log.Error("retention scan failed", "database", dbID, "err", err)
			return
		}
		committed = append(committed, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		r.log.Error("retention iteration", "database", dbID, "err", err)
		return
	}

	// The ANCHOR (newest committed backup for this database+destination) is
	// untouchable, even when keep_days says otherwise (protocol D: "保护最新
	// 完整备份" and "无锚点时禁止破坏性自动清理" — the anchor always exists
	// here because we just committed one).
	// The newest VERIFIED backup is protected too (phase-5 review P1-09):
	// when the newer backups are unverified / failed verification / still
	// pending, deleting the last restore-proven copy would silently downgrade
	// the database's worst-case recovery guarantee.
	lastVerifiedIdx := -1
	for i, c := range committed {
		if c.verifyState == string(verifierStatusVerified) {
			lastVerifiedIdx = i
			break
		}
	}
	for i, c := range committed {
		if i == 0 || i == lastVerifiedIdx {
			continue // anchor and last verified restore proof
		}
		if c.verifyState == "pending" || c.verifyState == "running" {
			continue // a queued/in-flight verification still needs its inputs
		}
		tooMany := i >= keep
		tooOld := !cutoff.IsZero() && time.Unix(c.at, 0).Before(cutoff)
		if !tooMany && !tooOld {
			continue
		}
		r.deleteRemoteBackup(ctx, backend, dest.ID, c.id, c.objKey, c.manKey)
	}
}

// verifyStatusVerified is the job-row verify_status value marking a
// restore-proven backup (string form avoids importing the verifier package
// into the retention path).
const verifierStatusVerified = "verified"

// deleteRemoteBackup removes one backup's remote objects (per-object error
// handling; a partial delete is recorded, never silent) and its local
// staged files, then marks the job remote_state='deleted'. Jobs whose
// artifact a verification is reading or about to read (queued) are SKIPPED:
// deleting the local ciphertext/manifest under a verification destroys its
// input (round-2 P1-09 gap 3).
func (r *Runner) deleteRemoteBackup(ctx context.Context, backend storage.Backend, destID, jobID int64, objKey, manKey string) {
	if r.verifyBusy(jobID) {
		r.log.Info("retention skipped: verification holds this job's artifact lease", "job", jobID)
		return
	}
	for _, key := range []string{objKey, manKey} {
		if key == "" {
			continue
		}
		if err := r.deleteWithRetry(ctx, backend, key); err != nil {
			// Keep the job in 'committed': a failed delete must not look
			// like a successful cleanup (protocol D: partial delete states
			// are recorded, never silent).
			r.log.Error("retention delete failed; job stays committed for retry",
				"job", jobID, "key", key, "err", err)
			return
		}
	}
	// Remote objects are gone; drop the local staged copies too (the kit is
	// removed with them — without the backup it describes it is dead weight;
	// its reference is cleared in the same update).
	var artifactPath, manifestPath, kitPath string
	if err := r.authDB.QueryRow(
		`SELECT artifact_path, manifest_path, COALESCE(recovery_kit_path,'') FROM jobs WHERE id = ?`, jobID).
		Scan(&artifactPath, &manifestPath, &kitPath); err == nil {
		for _, p := range []string{artifactPath, manifestPath, kitPath} {
			if p != "" && strings.HasPrefix(p, r.stagingDir+string(os.PathSeparator)) {
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					r.log.Error("retention local cleanup failed", "job", jobID, "path", p, "err", err)
				}
			}
		}
	}
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET remote_state = 'deleted',
		  artifact_path = '', manifest_path = '', recovery_kit_path = '',
		  remote_object_key = '', remote_manifest_key = ''
		WHERE id = ? AND remote_state = 'committed'`, jobID); err != nil {
		r.log.Error("retention state update failed", "job", jobID, "err", err)
		return
	}
	r.log.Info("retention deleted backup", "job", jobID, "destination", destID)
}

// deleteWithRetry performs up to three delete attempts (deletes are
// idempotent; absent objects are success).
func (r *Runner) deleteWithRetry(ctx context.Context, backend storage.Backend, key string) error {
	var lastErr error
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		lastErr = backend.Delete(ctx, key)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
		}
	}
	return lastErr
}

// pruneLocalArtifacts trims local staged ciphertexts for a database beyond
// the newest keep (protocol D local half; the newest succeeded job's local
// artifact is always protected). Jobs whose artifact a verification is
// reading or about to read (queued lease) are skipped (phase-5 review
// P1-09 file lease), as is the newest VERIFIED backup when newer backups
// are unverified (round-2 P1-09: the local restore proof is an anchor
// too). Recovery-kit files are never pruned here — they are tiny text and
// PruneExpiredArtifacts reclaims staging space from jobs that can no longer
// use their committed artifact: failed and canceled jobs are never resumed,
// and shutdown-interrupted jobs get a fresh resume attempt at every startup
// BEFORE this sweep runs (main calls ResumeRemotePhase first), so an
// artifact still un-committed remotely after the TTL is dead weight.
// Without this sweep a long destination outage fills staging and — with a
// quota configured — permanently locks every database's backups (overall
// review P1-K1). A TTL of 0 disables the sweep (keep everything).
func (r *Runner) PruneExpiredArtifacts(ctx context.Context) {
	if r.failedArtifactTTL <= 0 {
		return
	}
	cutoff := time.Now().Add(-r.failedArtifactTTL).Unix()
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, artifact_path, manifest_path FROM jobs
		WHERE artifact_state = 'committed' AND artifact_path != ''
		  AND status IN ('failed','canceled','interrupted')
		  AND finished_at IS NOT NULL AND finished_at < ?`, cutoff)
	if err != nil {
		r.log.Error("expired-artifact sweep query", "err", err)
		return
	}
	type reclaim struct {
		id                 int64
		artifact, manifest string
	}
	var list []reclaim
	for rows.Next() {
		var rc reclaim
		if err := rows.Scan(&rc.id, &rc.artifact, &rc.manifest); err != nil {
			r.log.Error("expired-artifact sweep scan", "err", err)
			rows.Close() //nolint:sqlclosecheck // rows are fully consumed and closed BEFORE the write loop (round-2 P2-02): holding a read cursor across writes is the hazard this rule misses
			return
		}
		list = append(list, rc)
	}
	if err := rows.Err(); err != nil {
		r.log.Error("expired-artifact sweep rows", "err", err)
		rows.Close()
		return
	}
	rows.Close()
	for _, rc := range list {
		for _, path := range []string{rc.artifact, rc.manifest} {
			if path == "" {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				r.log.Error("expired-artifact remove", "job", rc.id, "path", path, "err", err)
			}
		}
		if _, err := r.authDB.ExecContext(ctx, `
			UPDATE jobs SET artifact_path = '', artifact_sha256 = '', artifact_size = 0,
			  manifest_path = '', artifact_state = ''
			WHERE id = ? AND status IN ('failed','canceled','interrupted')`, rc.id); err != nil {
			r.log.Error("expired-artifact clear", "job", rc.id, "err", err)
		} else {
			r.log.Info("expired artifact reclaimed", "job", rc.id)
		}
	}
	if len(list) > 0 {
		r.log.Info("expired artifacts reclaimed", "count", len(list))
	}
}

// pruneLocalArtifacts enforces protocol D's local half: keep the newest
// `keep` committed artifacts per database, delete the rest — the newest is
// always protected, and anything remotely committed stays downloadable.
func (r *Runner) pruneLocalArtifacts(ctx context.Context, dbID int64, keep int) {
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, artifact_path, manifest_path, remote_state, COALESCE(verify_status,'')
		FROM jobs
		WHERE database_id = ? AND status = 'succeeded'
		ORDER BY id DESC`, dbID)
	if err != nil {
		r.log.Error("local prune query failed", "database", dbID, "err", err)
		return
	}
	type job struct {
		id                 int64
		artifact, manifest string
		remoteState        string
		verifyState        string
	}
	var succeeded []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.artifact, &j.manifest, &j.remoteState, &j.verifyState); err != nil {
			rows.Close() //nolint:sqlclosecheck // rows are fully consumed and closed BEFORE the write loop (round-2 P2-02): holding a read cursor across writes is the hazard this rule misses
			return
		}
		succeeded = append(succeeded, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		r.log.Error("local artifact prune iteration", "err", err)
		return
	}

	// The newest verified backup keeps its local artifact even when it is
	// outside the keep window: until a newer backup passes verification it
	// is the only restore-proven copy (round-2 P1-09 gap 1).
	lastVerifiedIdx := -1
	for i, j := range succeeded {
		if j.verifyState == "verified" {
			lastVerifiedIdx = i
			break
		}
	}
	for i, j := range succeeded {
		if i == 0 || i < keep || i == lastVerifiedIdx {
			continue // anchor, within-keep, and last verified restore proof
		}
		if j.remoteState == "uploading" {
			continue // never delete a file an upload may still reference
		}
		if j.verifyState == "pending" || j.verifyState == "running" || r.verifyBusy(j.id) {
			continue // a queued or in-flight verification needs this artifact
		}
		for _, p := range []string{j.artifact, j.manifest} {
			if p == "" || !strings.HasPrefix(p, r.stagingDir+string(os.PathSeparator)) {
				continue
			}
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				r.log.Error("local prune failed", "job", j.id, "path", p, "err", err)
			}
		}
		if _, err := r.authDB.Exec(`
			UPDATE jobs SET artifact_path = '', manifest_path = ''
			WHERE id = ? AND status = 'succeeded'`, j.id); err != nil {
			r.log.Error("local prune state update failed", "job", j.id, "err", err)
		}
	}
}
