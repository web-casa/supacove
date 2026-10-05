package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// seedHeartbeatDB registers a database with heartbeat configuration.
func seedHeartbeatDB(t *testing.T, r *phase3Runner, name, url string, period, grace int) int64 {
	t.Helper()
	enc, err := encryptForTest(r.key, "postgres://app:pw@localhost:1/appdb?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.DB.Exec(
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, heartbeat_url,
		                            heartbeat_period_hours, heartbeat_grace_hours, created_at, updated_at)
		 VALUES (?, 'generic', '', ?, ?, ?, ?, '0', '0')`, name, enc, url, period, grace); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.store.DB.QueryRow(`SELECT id FROM databases WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestHeartbeatFreshnessGate pins the Phase 7 contract: a success ping fires
// only for fresh snapshots of remotely committed (or local-only) backups; a
// stale snapshot MUST NOT ping (旧密文晚上传不得消除超期).
func TestHeartbeatFreshnessGate(t *testing.T) {
	var pings, fails atomic.Int32
	hb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			fails.Add(1)
			return
		}
		pings.Add(1)
	}))
	defer hb.Close()

	r := newPhase3Runner(t)
	if err := r.store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshDB := seedHeartbeatDB(t, r, "hb-fresh", hb.URL, 24, 1)
	staleDB := seedHeartbeatDB(t, r, "hb-stale", hb.URL, 24, 1)
	noperiodDB := seedHeartbeatDB(t, r, "hb-noperiod", hb.URL, 0, 0)

	// Fresh snapshot: pings.
	dec := r.heartbeatFor(freshDB, time.Now().Add(-time.Hour), true, true)
	if dec.skip != "" {
		t.Fatalf("fresh snapshot must ping, got skip=%q", dec.skip)
	}
	r.pingSuccess(freshDB, dec)

	// Stale snapshot beyond period+grace: MUST skip.
	dec = r.heartbeatFor(staleDB, time.Now().Add(-48*time.Hour), true, true)
	if dec.skip == "" {
		t.Fatal("stale snapshot must not ping (the monitor must alert)")
	}
	r.pingSuccess(staleDB, dec) // must be a no-op

	// No period configured: no age gate (ping fires).
	dec = r.heartbeatFor(noperiodDB, time.Now().Add(-100*time.Hour), true, true)
	if dec.skip != "" {
		t.Fatalf("no-period config must not gate: %q", dec.skip)
	}

	// Uncommitted backup with a destination: must not vouch.
	dec = r.heartbeatFor(freshDB, time.Now(), false, true)
	if dec.skip == "" {
		t.Fatal("an uncommitted backup must not ping the dead-man switch")
	}

	// Wait for the async pings, then assert exactly one success ping.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pings.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pings.Load() != 1 {
		t.Fatalf("success pings = %d, want exactly 1 (stale/uncommitted skipped)", pings.Load())
	}

	// Failure signal goes to url+"/fail".
	r.pingFail(freshDB)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fails.Load() >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fail ping never reached url+/fail")
}

// TestOverviewStates pins the homepage state machine: state derives from the
// last SUCCESS; a failed retry shows in LastJobStatus without downgrading.
func TestOverviewStates(t *testing.T) {
	r := newPhase3Runner(t)
	now := time.Now()

	neverDB := seedHeartbeatDB(t, r, "ov-never", "", 0, 0)
	freshDB := seedHeartbeatDB(t, r, "ov-fresh", "", 24, 0)
	expiredDB := seedHeartbeatDB(t, r, "ov-expired", "", 1, 0)
	// The freshness threshold is a separate column: a 1h max age.
	if _, err := r.store.DB.Exec(`UPDATE databases SET max_age_hours = 1 WHERE id = ?`, expiredDB); err != nil {
		t.Fatal(err)
	}
	retryDB := seedHeartbeatDB(t, r, "ov-retry", "", 24, 0)

	seedSuccess := func(dbID int64, startedAgo time.Duration, verify string) {
		if _, err := r.store.DB.Exec(`
			INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at, verify_status)
			VALUES (?, 'succeeded', 0, 0, ?, 0, ?)`,
			dbID, now.Add(-startedAgo).Unix(), verify); err != nil {
			t.Fatal(err)
		}
	}
	seedSuccess(freshDB, 2*time.Hour, "verified")
	seedSuccess(expiredDB, 5*time.Hour, "skipped")
	seedSuccess(retryDB, time.Hour, "pending")
	// A failed retry AFTER the success must not change the state source.
	if _, err := r.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, finished_at, error_class)
		VALUES (?, 'failed', 0, 0, 0, 'network')`, retryDB); err != nil {
		t.Fatal(err)
	}
	_ = neverDB

	entries, err := Overview(context.Background(), r.store.DB, now, func(string, int64, time.Time) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]OverviewEntry{}
	for _, e := range entries {
		byID[e.DatabaseID] = e
	}
	if got := byID[freshDB].State; got != "fresh" {
		t.Fatalf("fresh db state = %q, want fresh", got)
	}
	if got := byID[freshDB].LastSuccessVerifyState; got != "verified" {
		t.Fatalf("fresh db verify = %q, want verified", got)
	}
	if got := byID[expiredDB].State; got != "expired" {
		t.Fatalf("expired db state = %q (age %.1f > max 1h), want expired", got, byID[expiredDB].LastSuccessAgeHours)
	}
	if got := byID[neverDB].State; got != "never" {
		t.Fatalf("never db state = %q, want never", got)
	}
	if got := byID[retryDB]; got.State != "fresh" || got.LastJobStatus != "failed" {
		t.Fatalf("retry db = %q/%q, want fresh state with failed lastJob", got.State, got.LastJobStatus)
	}
}

// TestScheduleConfigRoundTrip: get → update → get persists every field.
func TestScheduleConfigRoundTrip(t *testing.T) {
	r := newPhase3Runner(t)
	dbID := seedHeartbeatDB(t, r, "sched-db", "", 0, 0)

	c, err := GetSchedule(context.Background(), r.store.DB, dbID)
	if err != nil {
		t.Fatal(err)
	}
	c.CronExpr = "0 3 * * *"
	c.CronTZ = "Europe/Berlin"
	c.MaxAgeHours = 26
	c.Paused = true
	c.HeartbeatURL = "https://hc-ping.example/uuid"
	c.HeartbeatPeriodHours = 25
	c.HeartbeatGraceHours = 2
	if err := UpdateSchedule(context.Background(), r.store.DB, c); err != nil {
		t.Fatal(err)
	}
	got, err := GetSchedule(context.Background(), r.store.DB, dbID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CronExpr != "0 3 * * *" || got.CronTZ != "Europe/Berlin" || !got.Paused ||
		got.MaxAgeHours != 26 || got.HeartbeatURL != "https://hc-ping.example/uuid" ||
		got.HeartbeatPeriodHours != 25 || got.HeartbeatGraceHours != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// Unknown database → not found.
	if _, err := GetSchedule(context.Background(), r.store.DB, 999999); err != ErrDatabaseNotFound {
		t.Fatalf("unknown id err = %v, want ErrDatabaseNotFound", err)
	}
}
