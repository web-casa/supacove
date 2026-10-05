package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPhase8GetStatsRegression (round-2 review R2-P1-01): GetStats must
// serve against the REAL production schema — the two deterministic 500s
// (SUM over a non-existent jobs.dump_size column and a Scan-count mismatch)
// were invisible to build/vet and the API-contract check. Empty database
// AND populated database must both return 200.
func TestPhase8GetStatsRegression(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	// Empty database: every segment denominator is zero — the rates must be
	// null and the avg duration unknown (never a fake 0).
	code, body := getJSON(t, env, "/api/stats")
	if code != 200 {
		t.Fatalf("empty-db stats status = %d (%v)", code, body)
	}
	b, _ := json.Marshal(body)
	for _, want := range []string{
		`"successRate":null`,
		`"exportSuccessRate":null`,
		`"remoteSuccessRate":null`,
		`"verifySuccessRate":null`,
		`"notifySuccessRate":null`,
		`"avgDurationSecs":null`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("empty-db stats missing %s: %s", want, b)
		}
	}

	// Populate MIXED states: one succeeded/verified/committed with a real
	// measurement + stats row; one upload-phase failure (dump was fine); one
	// pre-dump failure (network); one verify-failed success.
	if _, err := env.store.DB.Exec(`
		INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		VALUES ('sdb', 'generic', '', 'x', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	var dbID int64
	_ = env.store.DB.QueryRow(`SELECT id FROM databases WHERE name='sdb'`).Scan(&dbID)
	if _, err := env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at,
		                  artifact_size, duration_secs, source_db_bytes, verify_status, remote_state, error_class)
		VALUES (?, 'succeeded', 0, 0, 100, 110, 405, 10.0, 873, 'verified', 'committed', '')`, dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at, error_class)
		VALUES (?, 'failed', 0, 0, 120, 'storage_upload')`, dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at, error_class)
		VALUES (?, 'failed', 0, 0, 130, 'network')`, dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at,
		                  artifact_size, verify_status)
		VALUES (?, 'succeeded', 0, 0, 140, 100, 'failed')`, dbID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(`
		INSERT INTO backup_stats (job_id, database_name, dump_size, artifact_size,
		                          duration_secs, verify_status, remote_committed, created_at)
		VALUES (1, 'sdb', 300, 405, 10.0, 'verified', 1, 0)`); err != nil {
		t.Fatal(err)
	}

	code, body = getJSON(t, env, "/api/stats")
	if code != 200 {
		t.Fatalf("populated stats status = %d (%v)", code, body)
	}
	b, _ = json.Marshal(body)
	// terminal = 4; export OK = succeeded(2) + upload-failure(1) = 3 → 75%.
	// remote OK = 1 committed vs 1 failure → 50%. verify: 1/2 → 50%.
	for _, want := range []string{
		`"totalJobs":4`,
		`"totalDumpBytes":300`,
		`"totalArtifactBytes":505`,
		`"totalSourceBytes":873`,
		`"exportSuccessRate":75`,
		`"remoteSuccessRate":50`,
		`"verifySuccessRate":50`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("stats missing %s: %s", want, b)
		}
	}
	if !strings.Contains(string(b), `"avgDurationSecs":10`) {
		t.Fatalf("avgDurationSecs must be the measured 10s: %s", b)
	}
}
