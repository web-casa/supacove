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
// local artifact. On any failure the job is failed with class
// storage_upload; the local staged artifact and the upload intent survive
// so nothing is lost and nothing needs a re-dump.
func (r *Runner) uploadAndCommitRemote(ctx context.Context, jobID, dbID int64, result *uploadedArtifact) error {
	dest, err := r.DestinationForDatabase(ctx, dbID)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
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
				return fmt.Errorf("%w; ALSO failed to delete the corrupt remote object %s: %v", vErr, objKey, dErr)
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

// stringsReader is a tiny helper to avoid importing strings only for this.
type byteReader struct {
	b []byte
	i int
}

func stringsReader(b []byte) io.Reader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

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
		SELECT id, remote_object_key, remote_manifest_key, COALESCE(uploaded_at, finished_at, created_at)
		FROM jobs
		WHERE database_id = ? AND destination_id = ? AND remote_state = 'committed'
		ORDER BY id DESC`, dbID, dest.ID)
	if err != nil {
		r.log.Error("retention query failed", "database", dbID, "err", err)
		return
	}
	type candidate struct {
		id     int64
		objKey string
		manKey string
		at     int64
	}
	var committed []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.objKey, &c.manKey, &c.at); err != nil {
			rows.Close()
			r.log.Error("retention scan failed", "database", dbID, "err", err)
			return
		}
		committed = append(committed, c)
	}
	rows.Close()

	// The ANCHOR (newest committed backup for this database+destination) is
	// untouchable, even when keep_days says otherwise (protocol D: "保护最新
	// 完整备份" and "无锚点时禁止破坏性自动清理" — the anchor always exists
	// here because we just committed one).
	for i, c := range committed {
		if i == 0 {
			continue // anchor
		}
		tooMany := i >= keep
		tooOld := !cutoff.IsZero() && time.Unix(c.at, 0).Before(cutoff)
		if !tooMany && !tooOld {
			continue
		}
		r.deleteRemoteBackup(ctx, backend, dest.ID, c.id, c.objKey, c.manKey)
	}
}

// deleteRemoteBackup removes one backup's remote objects (per-object error
// handling; a partial delete is recorded, never silent) and its local
// staged files, then marks the job remote_state='deleted'.
func (r *Runner) deleteRemoteBackup(ctx context.Context, backend storage.Backend, destID, jobID int64, objKey, manKey string) {
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
	// Remote objects are gone; drop the local staged copies too.
	var artifactPath, manifestPath string
	if err := r.authDB.QueryRow(
		`SELECT artifact_path, manifest_path FROM jobs WHERE id = ?`, jobID).
		Scan(&artifactPath, &manifestPath); err == nil {
		for _, p := range []string{artifactPath, manifestPath} {
			if p != "" && strings.HasPrefix(p, r.stagingDir+string(os.PathSeparator)) {
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					r.log.Error("retention local cleanup failed", "job", jobID, "path", p, "err", err)
				}
			}
		}
	}
	if _, err := r.authDB.Exec(`
		UPDATE jobs SET remote_state = 'deleted',
		  artifact_path = '', manifest_path = '',
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
// artifact is always protected).
func (r *Runner) pruneLocalArtifacts(ctx context.Context, dbID int64, keep int) {
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, artifact_path, manifest_path, remote_state
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
	}
	var succeeded []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.artifact, &j.manifest, &j.remoteState); err != nil {
			rows.Close()
			return
		}
		succeeded = append(succeeded, j)
	}
	rows.Close()

	for i, j := range succeeded {
		if i == 0 || i < keep {
			continue // anchor and within-keep jobs keep their local files
		}
		if j.remoteState == "uploading" {
			continue // never delete a file an upload may still reference
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
