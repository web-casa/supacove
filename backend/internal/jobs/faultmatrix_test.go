package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/web-casa/supacove/backend/internal/agekey"
	"github.com/web-casa/supacove/backend/internal/dumper"
	"github.com/web-casa/supacove/backend/internal/pgclient"
	"github.com/web-casa/supacove/backend/internal/storage"
)

// Fault matrix (overall review, quality-plan row set): every intermediate
// failure must leave an unambiguous state and never a fake success. These
// five rows had no coverage before this file.

// Row: rename cannot publish over an occupied final path — the run fails
// ClassDisk, no committed artifact appears, and the temp file is cleaned.
func TestCommitFailsWhenFinalPathOccupied(t *testing.T) {
	p := newPhase3Runner(t)
	// Occupy the would-be final path with a DIRECTORY: os.Rename then fails
	// deterministically (the tmp file cannot replace a directory).
	if err := os.MkdirAll(filepath.Join(p.dataDir, "staging", "backup-job1.dump.age"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := dumper.Config{StagingDir: filepath.Join(p.dataDir, "staging"), BinDirOverride: fakeDumpDir(t)}
	_, err := cfg.Run(context.Background(), 1, dumper.Target{
		Conn:        mustParse(t, "postgres://u:p@127.0.0.1:1/db?sslmode=disable"),
		Recipient:   testRecipient(t),
		ServerMajor: 99, // matches the fake pg_dump in fakeDumpDir
	})
	if err == nil {
		t.Fatal("run must fail when the final path is occupied")
	}
	var cl *dumper.Classified
	if !errors.As(err, &cl) || cl.Class != "disk" {
		t.Fatalf("error = %v, want classified disk", err)
	}
	entries, _ := os.ReadDir(filepath.Join(p.dataDir, "staging"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".inprogress") {
			t.Errorf("temp file leaked after failed commit: %s", e.Name())
		}
	}
}

// Row: object committed remotely but the local bookkeeping lost the
// reference (the SQLite update never landed). Startup reconciliation must
// restore the reference — the artifact stays traceable, never deleted and
// never published as a new backup.
func TestReconcileRestoresLostArtifactReference(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "recdb")
	staging := filepath.Join(p.dataDir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := p.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at)
		VALUES (?, 'interrupted', 0, 0) RETURNING id`, dbID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(staging, fmt.Sprintf("backup-job%d.dump.age", jobID))
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT-LOST-REF"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := p.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}

	var path, state string
	if err := p.store.DB.QueryRow(
		`SELECT artifact_path, artifact_state FROM jobs WHERE id = ?`, jobID).Scan(&path, &state); err != nil {
		t.Fatal(err)
	}
	if path != artifact || state != "committed" {
		t.Fatalf("reference not restored: path=%q state=%q", path, state)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Errorf("reconciled artifact must survive: %v", err)
	}

	// Idempotent: a second recovery pass must not stack another annotation.
	if _, err := p.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	var msg string
	_ = p.store.DB.QueryRow(`SELECT COALESCE(error_message,'') FROM jobs WHERE id = ?`, jobID).Scan(&msg)
	if n := strings.Count(msg, "[recovery: unreferenced ciphertext restored"); n != 1 {
		t.Errorf("annotation count = %d, want exactly 1 (msg %q)", n, msg)
	}
}

// Row: multipart-style upload interrupted — a failed Put leaves NO remote
// object; and a "response lost" commit (object stored, error returned) is
// reconcilable: the next resume re-commits idempotently without duplicating
// or deleting the object.
func TestUploadInterruptLeavesNoPartialAndResumesIdempotently(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 10)
	dbID := p.addDatabase(t, destID, "updb")
	dest, err := p.GetDestination(context.Background(), destID)
	if err != nil {
		t.Fatal(err)
	}

	// (a) Put dies mid-stream AFTER a part landed (multipart interrupted):
	// nothing may be promoted to a complete object, and the interrupted
	// session's part must be observable so "no leaked parts" is a real
	// assertion rather than a fake that never models parts.
	jobID := p.seedCommittedJob(t, dbID, destID, "running", "")
	p.backend.failPutMidStream = true
	upload := uploadFromSeed(t, p, jobID)
	if uerr := p.uploadAndCommitRemote(context.Background(), jobID, dbID, upload, dest); uerr == nil {
		t.Fatal("injected mid-stream failure must surface")
	}
	if len(p.backend.objects) != 0 {
		t.Errorf("interrupted multipart left %d complete objects, want none", len(p.backend.objects))
	}
	objKey := dest.StorageConfig().BackupKey(backupUUIDOf(t, p, jobID))
	if p.backend.parts[objKey] == 0 {
		t.Fatal("mid-stream fault must leave an in-flight part to assert cleanup against")
	}
	var remote string
	_ = p.store.DB.QueryRow(`SELECT remote_state FROM jobs WHERE id = ?`, jobID).Scan(&remote)
	if remote == "committed" {
		t.Error("failed upload must not mark committed")
	}

	// (b) Resume over the interrupted session: the completing upload
	// consumes the parts (complete) — no part may survive, no duplicate
	// object may appear.
	p.backend.failPutMidStream = false
	uploadR := uploadFromSeed(t, p, jobID)
	if uerr := p.uploadAndCommitRemote(context.Background(), jobID, dbID, uploadR, dest); uerr != nil {
		t.Fatalf("resume after interrupted multipart: %v", uerr)
	}
	if p.backend.parts[objKey] != 0 {
		t.Errorf("parts leaked after completing resume: %v", p.backend.parts)
	}

	// (c) Response lost: object stored, error returned. The job stays
	// resumable; the next resume must re-commit over the SAME objects.
	p.backend.failAnyPut = false
	job2 := p.seedCommittedJob(t, dbID, destID, "interrupted", "uploading")
	p.backend.losePutResponse = true
	upload2 := uploadFromSeed(t, p, job2)
	if uerr := p.uploadAndCommitRemote(context.Background(), job2, dbID, upload2, dest); uerr == nil {
		t.Fatal("lost response must surface as an error")
	}
	// The lost response happened on job2's FIRST Put (ciphertext): jobID's
	// completed resume holds 2 objects, job2 exactly 1 — and job2 must still
	// look resumable.
	if storedKeys := len(p.backend.objects); storedKeys != 3 {
		t.Fatalf("objects stored = %d, want 3 (resume committed 2 + lost-response 1)", storedKeys)
	}
	p.backend.losePutResponse = false
	upload3 := uploadFromSeed(t, p, job2)
	if uerr := p.uploadAndCommitRemote(context.Background(), job2, dbID, upload3, dest); uerr != nil {
		t.Fatalf("resume over existing objects must succeed: %v", uerr)
	}
	if n := len(p.backend.objects); n != 4 {
		t.Errorf("objects after both resumes = %d, want 4 (no duplicates)", n)
	}
	var remote2 string
	_ = p.store.DB.QueryRow(`SELECT remote_state FROM jobs WHERE id = ?`, job2).Scan(&remote2)
	if remote2 != "committed" {
		t.Errorf("remote_state = %q, want committed", remote2)
	}
}

// Row: manual trigger racing the scheduler — the per-database active guard
// admits exactly one job; the rest get ErrAlreadyQueued, never a duplicate
// running row.
func TestConcurrentEnqueueAdmitsSingleJob(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "racedb")

	const racers = 16
	var wg sync.WaitGroup
	ok := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Enqueue(context.Background(), dbID)
			ok <- err
		}()
	}
	wg.Wait()
	close(ok)
	var accepted, dupes int
	for err := range ok {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrAlreadyQueued) {
			dupes++
		} else {
			t.Fatalf("unexpected enqueue error: %v", err)
		}
	}
	if accepted != 1 || dupes != racers-1 {
		t.Fatalf("accepted=%d dupes=%d, want 1/%d", accepted, dupes, racers-1)
	}
	var running int
	if err := p.store.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE database_id = ? AND status IN ('pending','running')`, dbID).
		Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 1 {
		t.Errorf("active rows = %d, want 1", running)
	}
}

// Row: a NEW failed backup must never let retention touch the protected
// anchor — local prune and remote retention both keep the last good backup.
func TestRetentionKeepsAnchorAcrossNewFailure(t *testing.T) {
	p := newPhase3Runner(t)
	// KeepDays=1 with both generations backdated two days makes EVERY
	// committed generation "too old" — including the anchor itself. That
	// is the configuration where the anchor guard is distinguishable from
	// the keep<N> branch (GLM r5: the old setup conflated them, so deleting
	// the anchor guard passed the suite).
	destID := p.addDestinationKeepDays(t, 1, 1) // keep=1 AND everything older than 1 day
	dbID := p.addDatabase(t, destID, "anchor-db")

	good := p.seedCommittedJob(t, dbID, destID, "succeeded", "committed")
	older := p.seedCommittedJob(t, dbID, destID, "succeeded", "committed")
	olderPath := artifactPathOf(t, p, older)
	// Make `older` strictly older; good keeps the higher id (the anchor).
	if _, err := p.store.DB.Exec(`UPDATE jobs SET id = id - 10000 WHERE id = ?`, older); err != nil {
		t.Fatal(err)
	}
	// Backdate BOTH generations past the KeepDays cutoff: without the anchor
	// guard the retention sweep would delete the anchor too.
	if _, err := p.store.DB.Exec(
		`UPDATE jobs SET uploaded_at = strftime('%s','now') - 86400*2 WHERE database_id = ?`, dbID); err != nil {
		t.Fatal(err)
	}
	// A newer, failed job with a committed LOCAL artifact (upload died).
	bad := p.seedCommittedJob(t, dbID, destID, "failed", "uploading")

	p.SetLocalKeep(1)
	p.pruneLocalArtifacts(context.Background(), dbID, 1)
	goodPath := artifactPathOf(t, p, good)
	badPath := artifactPathOf(t, p, bad)
	if _, err := os.Stat(goodPath); err != nil {
		t.Errorf("anchor artifact deleted by local prune: %v", err)
	}
	// KeepDays=1 + keep=1: the anchor survives even though it is itself
	// "too old", while the OLDER succeeded generation is collected.
	if _, err := os.Stat(olderPath); err == nil {
		t.Error("older non-anchor generation survived keep=1 pruning")
	}
	// The failed job's artifact is NOT a retention anchor; prune may take it
	// (reclamation path), but the good one must survive with keep=1.
	_ = badPath

	// Remote retention: only committed generations are candidates, and the
	// newest committed one is the anchor.
	dest, err := p.GetDestination(context.Background(), destID)
	if err != nil {
		t.Fatal(err)
	}
	p.backendFactory = func(ctx context.Context, d *Destination) (storage.Backend, error) {
		return p.backend, nil
	}
	p.runRemoteRetention(context.Background(), dest, p.backend, dbID)
	uuid := backupUUIDOf(t, p, good)
	if _, ok := p.backend.objects[dest.StorageConfig().BackupKey(uuid)]; !ok {
		t.Error("remote retention deleted the newest committed backup")
	}
}

// ---- helpers ----

func artifactPathOf(t *testing.T, p *phase3Runner, jobID int64) string {
	t.Helper()
	var path string
	if err := p.store.DB.QueryRow(`SELECT COALESCE(artifact_path,'') FROM jobs WHERE id = ?`, jobID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	return path
}

func backupUUIDOf(t *testing.T, p *phase3Runner, jobID int64) string {
	t.Helper()
	var uuid string
	if err := p.store.DB.QueryRow(`SELECT backup_uuid FROM jobs WHERE id = ?`, jobID).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	return uuid
}

func uploadFromSeed(t *testing.T, p *phase3Runner, jobID int64) *uploadedArtifact {
	t.Helper()
	var path, manifest string
	if err := p.store.DB.QueryRow(
		`SELECT artifact_path, manifest_path FROM jobs WHERE id = ?`, jobID).Scan(&path, &manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return &uploadedArtifact{
		artifactPath:  path,
		artifactSize:  int64(len(data)),
		sha256Hex:     sha256Of(data),
		manifestPath:  manifest,
		manifestBytes: mb,
	}
}

func mustParse(t *testing.T, uri string) *pgclient.ConnInfo {
	t.Helper()
	ci, err := pgclient.ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	return ci
}

func testRecipient(t *testing.T) string {
	t.Helper()
	_, recipient, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return recipient
}
