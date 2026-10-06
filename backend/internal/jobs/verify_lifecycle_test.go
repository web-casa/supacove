package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/manifest"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	redactpkg "github.com/cloudfan/supabackup/backend/internal/redact"
	"github.com/cloudfan/supabackup/backend/internal/verifier"
)

// fakeVerifyEngine records inputs and can block until released, letting
// tests prove Stop joins an in-flight verification (P1-06). With
// hangUntilRelease it ignores context cancellation — simulating a slow
// unwinding — so tests can observe Stop actually waiting.
type fakeVerifyEngine struct {
	mu               sync.Mutex
	started          chan struct{}
	release          chan struct{}
	inputs           []verifier.Input
	result           verifier.Result
	hangUntilRelease bool
}

func newFakeVerifyEngine(result verifier.Result) *fakeVerifyEngine {
	return &fakeVerifyEngine{
		started: make(chan struct{}),
		release: make(chan struct{}),
		result:  result,
	}
}

func (f *fakeVerifyEngine) Verify(ctx context.Context, in verifier.Input) verifier.Result {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	if f.hangUntilRelease {
		<-f.release // ignores ctx: proves Stop WAITS for the worker to unwind
		return f.result
	}
	// Release wins over cancellation when both fire (Stop closes the
	// release channel while the runner context is being torn down).
	select {
	case <-f.release:
		return f.result
	default:
	}
	select {
	case <-f.release:
		return f.result
	case <-ctx.Done():
		select {
		case <-f.release:
			return f.result
		default:
		}
		return verifier.Result{Status: verifier.StatusFailed, Detail: "context canceled"}
	}
}

func (f *fakeVerifyEngine) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

// addLocalDatabase registers a database with NO destination (local-only).
func (p *phase3Runner) addLocalDatabase(t *testing.T, name string) int64 {
	t.Helper()
	enc, err := encryptForTest(p.key, "postgres://app:pw@localhost:1/appdb?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, destination_id, created_at, updated_at)
		 VALUES (?, 'generic', '', ?, NULL, '0', '0')`, name, enc); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := p.store.DB.QueryRow(`SELECT id FROM databases WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// seedVerifiedReadyJob inserts a succeeded job with real staged artifact +
// manifest files, ready for verification.
func seedVerifiedReadyJob(t *testing.T, r *phase3Runner, dbID int64, verifyStatus string) int64 {
	t.Helper()
	var jobID int64
	if err := r.store.DB.QueryRow(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at,
		                  artifact_state, verify_status)
		VALUES (?, 'succeeded', 0, 0, 'committed', ?)
		RETURNING id`, dbID, verifyStatus).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(r.stagingDir, fmt.Sprintf("backup-job%d.dump.age", jobID))
	if err := os.MkdirAll(r.stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("CIPHERTEXT-"+fmt.Sprint(jobID)), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		BackupID: fmt.Sprintf("job-%d", jobID),
		Database: manifest.Database{Name: "vdb"},
		Archive: manifest.Archive{
			FileName:  filepath.Base(artifact),
			SHA256:    sha256Of([]byte("CIPHERTEXT-" + fmt.Sprint(jobID))),
			SizeBytes: int64(len("CIPHERTEXT-" + fmt.Sprint(jobID))),
			Encryption: manifest.Encryption{
				Type: "age", KeyID: "keyid123", Recipient: "age1recipient",
			},
		},
		Dependencies: manifest.Deps{TableCount: 3},
	}
	mb, err := manifest.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := artifact + ".manifest.json"
	if err := os.WriteFile(manifestPath, mb, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.DB.Exec(`
		UPDATE jobs SET artifact_path = ?, artifact_sha256 = ?, artifact_size = ?,
		  manifest_path = ?
		WHERE id = ?`, artifact, m.Archive.SHA256, m.Archive.SizeBytes, manifestPath, jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func verifyRow(t *testing.T, r *phase3Runner, jobID int64) (status, detail, profile string, tables int64) {
	t.Helper()
	if err := r.store.DB.QueryRow(`
		SELECT COALESCE(verify_status,''), COALESCE(verify_detail,''),
		       COALESCE(verify_profile,''), verify_tables
		FROM jobs WHERE id = ?`, jobID).
		Scan(&status, &detail, &profile, &tables); err != nil {
		t.Fatal(err)
	}
	return
}

// TestVerifyResumeRunsAtStartup: a job left verify_status='pending' by a
// previous shutdown is re-queued when the runner starts (P1-06 convergence).
func TestVerifyResumeRunsAtStartup(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "vdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")

	engine := newFakeVerifyEngine(verifier.Result{
		Status: verifier.StatusVerified, TablesFound: 3,
		Detail: "restored 3 user tables",
	})
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")
	// Release the engine as soon as it is entered so the run completes and
	// Stop can join (a still-blocked engine would hang Stop).
	go func() {
		select {
		case <-engine.started:
			close(engine.release)
		case <-time.After(5 * time.Second):
		}
	}()
	p.Start(context.Background())
	defer p.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _, tables := verifyRow(t, p, jobID)
		if status == "verified" {
			if tables != 3 {
				t.Fatalf("tables = %d, want 3", tables)
			}
			// The engine got the identity and the ciphertext path.
			engine.mu.Lock()
			in := engine.inputs[0]
			engine.mu.Unlock()
			if in.Identity != "AGE-SECRET-KEY-TEST" || in.ExpectedTables != 3 {
				t.Fatalf("verifier input wrong: identity=%q tables=%d", in.Identity, in.ExpectedTables)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, detail, _, _ := verifyRow(t, p, jobID)
	t.Fatalf("verification never completed: status=%q detail=%q calls=%d", status, detail, engine.calls())
}

// TestStopCancelsAndRequeues: Stop cancels the in-flight verification and
// returns only after the worker has unwound (its final state write is
// visible). A shutdown-interrupted run lands back on 'pending' so the next
// startup re-queues it — never a misleading failed/verified (P1-06).
func TestStopCancelsAndRequeues(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "vdb2")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")

	engine := newFakeVerifyEngine(verifier.Result{Status: verifier.StatusVerified})
	engine.hangUntilRelease = true
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")
	p.Start(context.Background())

	// Wait until the engine is inside Verify.
	select {
	case <-engine.started:
	case <-time.After(5 * time.Second):
		t.Fatal("verification never started")
	}

	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()

	// While the fake is blocked, Stop must NOT complete.
	select {
	case <-stopped:
		t.Fatal("Stop returned while verification was still running")
	case <-time.After(300 * time.Millisecond):
	}

	// Release: the run unwinds, the worker exits, Stop joins.
	close(engine.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the verification finished")
	}

	// The interrupted run must be back on 'pending' (requeued), not failed
	// and not (worse) verified — and that write is visible exactly because
	// Stop waited for the worker.
	status, detail, _, _ := verifyRow(t, p, jobID)
	if status != "pending" {
		t.Fatalf("status after shutdown = %q (detail %q), want pending (requeued)", status, detail)
	}
}

// TestVerifyQueueOverflowIsHonest: when the bounded queue is full, the
// verification is marked skipped with the reason — never silently dropped
// (P1-06/P2-02). No worker is running here, so the buffer fills up.
func TestVerifyQueueOverflowIsHonest(t *testing.T) {
	p := newPhase3Runner(t)
	p.SetVerifier(newFakeVerifyEngine(verifier.Result{Status: verifier.StatusVerified}))
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")

	var ids []int64
	for i := 0; i <= verifyQueueCap; i++ { // cap+1 enqueues: the last must overflow
		dbID := p.addLocalDatabase(t, fmt.Sprintf("odb%d", i))
		ids = append(ids, seedVerifiedReadyJob(t, p, dbID, ""))
		p.enqueueVerification(ids[i], func(s string) string { return s })
	}
	status, detail, _, _ := verifyRow(t, p, ids[verifyQueueCap])
	if status != "skipped" {
		t.Fatalf("overflow status = %q, want skipped", status)
	}
	if detail == "" {
		t.Fatal("skipped overflow must carry a reason")
	}
}

// TestNoVerifierMeansExplicitSkip: enqueueing verification on an instance
// without a verifier writes a distinguishable skipped state with the
// reason — never an empty status, never a queue entry into the void.
func TestNoVerifierMeansExplicitSkip(t *testing.T) {
	p := newPhase3Runner(t) // no SetVerifier: verifier stays nil
	dbID := p.addLocalDatabase(t, "nvdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "")
	p.enqueueVerification(jobID, func(s string) string { return s })
	status, detail, _, _ := verifyRow(t, p, jobID)
	if status != "skipped" || detail == "" {
		t.Fatalf("explicit skip not recorded: %q / %q", status, detail)
	}
}

// TestPruneSkipsVerificationLease: the lease is held from ENQUEUE through
// execution and released when the run completes — pruning must respect it
// in both phases (round-2 P1-09 gaps 2+4: queued protection and the
// check-then-start TOCTOU are closed by acquiring the lease at enqueue).
func TestPruneSkipsVerificationLease(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "pdb")
	// Seed order matters: the leased job must be OLDER than the anchor.
	// Seed with 'pending' so the startup sweep enqueues it.
	leasedJob := seedVerifiedReadyJob(t, p, dbID, "pending")
	_ = seedVerifiedReadyJob(t, p, dbID, "skipped") // newest = anchor

	// A FAILED verification keeps the job outside the last-verified anchor
	// protection, isolating the lease behavior under test.
	engine := newFakeVerifyEngine(verifier.Result{
		Status: verifier.StatusFailed, Detail: "stub failure", ServerVersion: "PostgreSQL 18.4 (test)",
	})
	engine.hangUntilRelease = true
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")
	p.Start(context.Background())
	defer p.Stop()

	// Wait until the queued verification is RUNNING (lease held).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _, _ := verifyRow(t, p, leasedJob)
		if status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, _, _, _ := verifyRow(t, p, leasedJob); status != "running" {
		t.Fatal("verification never started")
	}

	// While running (lease held), pruning must skip the job.
	p.pruneLocalArtifacts(context.Background(), dbID, 1)
	var artifact string
	if err := p.store.DB.QueryRow(`SELECT artifact_path FROM jobs WHERE id = ?`, leasedJob).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact == "" {
		t.Fatal("prune deleted the artifact while the verification lease was held")
	}

	// Complete the verification: the run finishes, the lease is released.
	close(engine.release)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _, _ := verifyRow(t, p, leasedJob)
		if status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, _, _, _ := verifyRow(t, p, leasedJob); status != "failed" {
		t.Fatal("verification never completed")
	}
	// Release wait: the worker unwinds (deferred lease release) before the
	// status became visible? The lease release happens right after
	// finishVerification in runVerification — give it a beat.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !p.verifyBusy(leasedJob) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p.verifyBusy(leasedJob) {
		t.Fatal("lease not released after completion")
	}

	// Now pruning proceeds.
	p.pruneLocalArtifacts(context.Background(), dbID, 1)
	if err := p.store.DB.QueryRow(`SELECT artifact_path FROM jobs WHERE id = ?`, leasedJob).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact != "" {
		t.Fatalf("artifact reference kept after lease release: %q", artifact)
	}
}

// TestQueuedVerificationProtectedFromPrune: a QUEUED (pending) job holds
// the lease too (round-2 P1-09 gap 2).
func TestQueuedVerificationProtectedFromPrune(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "qdb")
	queuedJob := seedVerifiedReadyJob(t, p, dbID, "")
	_ = seedVerifiedReadyJob(t, p, dbID, "skipped") // anchor

	engine := newFakeVerifyEngine(verifier.Result{Status: verifier.StatusVerified})
	engine.hangUntilRelease = true
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")

	// Queue without starting the worker: the entry sits in the bounded
	// queue, but the LEASE is already held.
	p.enqueueVerification(queuedJob, pgclientSanitize)
	if !p.verifyBusy(queuedJob) {
		t.Fatal("enqueue did not acquire the lease")
	}
	p.pruneLocalArtifacts(context.Background(), dbID, 1)
	var artifact string
	if err := p.store.DB.QueryRow(`SELECT artifact_path FROM jobs WHERE id = ?`, queuedJob).Scan(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact == "" {
		t.Fatal("prune deleted the artifact of a QUEUED verification")
	}
}

// TestNoVerifierSweepSettlesStaleStates: startup with a disabled verifier
// settles historical pending/running verify states to an explicit skip
// instead of leaving them transitional forever (round-2 P2-02).
func TestNoVerifierSweepSettlesStaleStates(t *testing.T) {
	p := newPhase3Runner(t) // no verifier
	dbID := p.addLocalDatabase(t, "sdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")

	p.Start(context.Background())
	defer p.Stop()

	deadline := time.Now().Add(5 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		status, _, _, _ = verifyRow(t, p, jobID)
		if status == "skipped" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "skipped" {
		t.Fatalf("stale pending state not settled: %q", status)
	}
	_, detail, _, _ := verifyRow(t, p, jobID)
	if detail == "" {
		t.Fatal("settled skip must carry the reason")
	}
}

// TestProfilePersistedFromServerVersion: verify_profile records the actual
// server version evidence, not a constant (round-2 P1-08).
func TestProfilePersistedFromServerVersion(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "prdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")

	engine := newFakeVerifyEngine(verifier.Result{
		Status: verifier.StatusVerified, TablesFound: 3,
		ServerVersion: "PostgreSQL 18.4 (Debian) on aarch64",
	})
	close(engine.release)
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")

	p.runVerification(context.Background(), verifyRequest{jobID: jobID, redact: pgclientSanitize})

	_, _, profile, _ := verifyRow(t, p, jobID)
	if profile != "embedded-local:18.4" {
		t.Fatalf("verify_profile = %q, want embedded-local:18.4", profile)
	}
}

// TestManifestMissingIsHonestSkip: a succeeded job whose manifest is gone
// must NOT be verified with degraded checks — it records an honest skip
// (round-2 P1-08: no silent downgrade of the expected-object baseline).
func TestManifestMissingIsHonestSkip(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "mdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")
	// Remove the manifest file the seed wrote.
	var manifestPath string
	if err := p.store.DB.QueryRow(`SELECT manifest_path FROM jobs WHERE id = ?`, jobID).Scan(&manifestPath); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(manifestPath) // TEST PRECONDITION: manufactures the missing-manifest state this case asserts (errcheck tolerated)

	engine := newFakeVerifyEngine(verifier.Result{Status: verifier.StatusVerified})
	close(engine.release)
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")

	p.runVerification(context.Background(), verifyRequest{jobID: jobID, redact: pgclientSanitize})
	status, detail, _, _ := verifyRow(t, p, jobID)
	if status != "skipped" {
		t.Fatalf("status = %q, want skipped (manifest missing)", status)
	}
	if detail == "" {
		t.Fatal("skip must explain the missing manifest")
	}
}

// TestRetentionProtectsLastVerified: with keep=1, the newest committed
// backup is the anchor and an OLDER verified backup must survive while the
// anchor is unverified (P1-09).
func TestRetentionProtectsLastVerified(t *testing.T) {
	p := newPhase3Runner(t)
	destID := p.addDestination(t, 1) // keep_remote = 1
	dbID := p.addDatabase(t, destID, "rdb")

	oldVerified := p.seedCommittedJob(t, dbID, destID, "succeeded", "committed")
	newAnchor := p.seedCommittedJob(t, dbID, destID, "succeeded", "committed")
	if _, err := p.store.DB.Exec(`UPDATE jobs SET verify_status = 'verified' WHERE id = ?`, oldVerified); err != nil {
		t.Fatal(err)
	}

	dest, err := p.GetDestination(context.Background(), destID)
	if err != nil {
		t.Fatal(err)
	}
	p.runRemoteRetention(context.Background(), dest, p.backend, dbID)

	var oldState, anchorState string
	if err := p.store.DB.QueryRow(`SELECT remote_state FROM jobs WHERE id = ?`, oldVerified).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err := p.store.DB.QueryRow(`SELECT remote_state FROM jobs WHERE id = ?`, newAnchor).Scan(&anchorState); err != nil {
		t.Fatal(err)
	}
	if oldState != "committed" {
		t.Fatalf("the last VERIFIED backup was deleted (remote_state=%q) — P1-09 regression", oldState)
	}
	if anchorState != "committed" {
		t.Fatalf("anchor was deleted (remote_state=%q)", anchorState)
	}
}

// TestBackfillRecoveryKits: a succeeded job with a manifest but a missing
// kit gets one regenerated at startup (P1-09).
func TestBackfillRecoveryKits(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "kdb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "skipped")
	// The seed wrote no kit; simulate a stale reference pointing nowhere.
	if _, err := p.store.DB.Exec(`UPDATE jobs SET recovery_kit_path = '/gone/kit.sh' WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}

	p.BackfillRecoveryKits(context.Background())

	var kitPath string
	if err := p.store.DB.QueryRow(`SELECT recovery_kit_path FROM jobs WHERE id = ?`, jobID).Scan(&kitPath); err != nil {
		t.Fatal(err)
	}
	if kitPath == "" || kitPath == "/gone/kit.sh" {
		t.Fatalf("kit reference not backfilled: %q", kitPath)
	}
	b, err := os.ReadFile(kitPath)
	if err != nil {
		t.Fatalf("backfilled kit unreadable: %v", err)
	}
	if len(b) == 0 || b[0] != '#' {
		t.Fatal("backfilled kit is not a script")
	}
	// A second run is a no-op (idempotent).
	p.BackfillRecoveryKits(context.Background())
}

// TestVerifyDetailRedacted: a failing verification's detail goes through
// the originating run's redaction chain before persistence (P2-02). The
// queue entry carries the redactor captured at enqueue time — exactly the
// runJob path.
func TestVerifyDetailRedacted(t *testing.T) {
	p := newPhase3Runner(t)
	dbID := p.addLocalDatabase(t, "redb")
	jobID := seedVerifiedReadyJob(t, p, dbID, "pending")

	engine := newFakeVerifyEngine(verifier.Result{
		Status: verifier.StatusFailed,
		Detail: "pg_restore failed: the secret is hunter2",
	})
	close(engine.release) // the engine completes immediately
	p.SetVerifier(engine)
	p.SetVerifyIdentity("AGE-SECRET-KEY-TEST")

	secret := "hunter2"
	redact := func(msg string) string {
		return pgclient.SanitizeMessage(redactpkg.Secrets([]string{secret}, msg))
	}
	p.mu.Lock()
	p.verifyInFlight[jobID] = true
	p.mu.Unlock()
	p.runVerification(context.Background(), verifyRequest{jobID: jobID, redact: redact})

	status, detail, _, _ := verifyRow(t, p, jobID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if strings.Contains(detail, secret) {
		t.Fatalf("detail persisted unredacted: %q", detail)
	}
	p.mu.Lock()
	_, busy := p.verifyInFlight[jobID]
	p.mu.Unlock()
	if busy {
		t.Fatal("in-flight lease not released after runVerification returned")
	}
}
