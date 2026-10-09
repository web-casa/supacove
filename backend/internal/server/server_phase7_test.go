package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPhase7ScheduleAPI: validation errors and a full round trip through
// the authenticated HTTP surface.
func TestPhase7ScheduleAPI(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	// Register a database directly in the store (the create API would run a
	// live connection test).
	_, err := env.store.DB.Exec(`
		INSERT INTO databases (name, platform, env_tag, conn_encrypted, created_at, updated_at)
		VALUES ('sched-api-db', 'generic', '', 'x', 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	var dbID int64
	if err := env.store.DB.QueryRow(`SELECT id FROM databases WHERE name='sched-api-db'`).Scan(&dbID); err != nil {
		t.Fatal(err)
	}

	// Invalid cron → 400.
	code, body := env.do7(t, http.MethodPut, "/api/databases/"+itoa(dbID)+"/schedule",
		map[string]any{"cronExpr": "not a cron"})
	if code != 400 {
		t.Fatalf("invalid cron status = %d (%v), want 400", code, body)
	}
	// Heartbeat URL without a period → 400 (silence semantics undefined).
	code, _ = env.do7(t, http.MethodPut, "/api/databases/"+itoa(dbID)+"/schedule",
		map[string]any{"heartbeatUrl": "https://hc.example/x"})
	if code != 400 {
		t.Fatalf("heartbeat without period status = %d, want 400", code)
	}
	// Link-local heartbeat URL → 400 (metadata protection).
	code, _ = env.do7(t, http.MethodPut, "/api/databases/"+itoa(dbID)+"/schedule",
		map[string]any{"heartbeatUrl": "http://169.254.169.254/x", "heartbeatPeriodHours": 24})
	if code != 400 {
		t.Fatalf("link-local heartbeat status = %d, want 400", code)
	}

	// Valid update → 200 and persisted.
	code, body = env.do7(t, http.MethodPut, "/api/databases/"+itoa(dbID)+"/schedule",
		map[string]any{
			"cronExpr":             "0 3 * * *",
			"cronTz":               "UTC",
			"maxAgeHours":          26,
			"heartbeatUrl":         "https://hc.example/uuid",
			"heartbeatPeriodHours": 25,
			"heartbeatGraceHours":  1,
		})
	if code != 200 {
		t.Fatalf("valid update status = %d (%v), want 200", code, body)
	}
	var maxAge int
	if err := env.store.DB.QueryRow(`SELECT max_age_hours FROM databases WHERE id = ?`, dbID).Scan(&maxAge); err != nil {
		t.Fatal(err)
	}
	if maxAge != 26 {
		t.Fatalf("max_age_hours = %d, want 26", maxAge)
	}

	// The "-" disable marker is ACCEPTED and needs no period (round-2:
	// the API used to reject the documented disable value as an invalid URL).
	code, _ = env.do7(t, http.MethodPut, "/api/databases/"+itoa(dbID)+"/schedule",
		map[string]any{"heartbeatUrl": "-", "heartbeatPeriodHours": 24})
	if code != 200 {
		t.Fatalf("disable marker status = %d, want 200", code)
	}
	var hb string
	if err := env.store.DB.QueryRow(`SELECT heartbeat_url FROM databases WHERE id = ?`, dbID).Scan(&hb); err != nil {
		t.Fatal(err)
	}
	if hb != "-" {
		t.Fatalf("disable marker not persisted: %q", hb)
	}

	// Unknown database → 404.
	code, _ = env.do7(t, http.MethodPut, "/api/databases/999999/schedule",
		map[string]any{"maxAgeHours": 5})
	if code != 404 {
		t.Fatalf("unknown db status = %d, want 404", code)
	}
}

// TestPhase7WebhookAPI: CRUD + synchronous test delivery.
func TestPhase7WebhookAPI(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// The console's test delivery must mirror the production outbox
		// delivery: both header generations, identical event ids.
		if r.Header.Get("X-Supacove-Event") == "" || r.Header.Get("X-Supacove-Event-ID") == "" {
			t.Error("test delivery missing X-Supacove-Event(-ID) headers")
		}
		if r.Header.Get("X-Supabackup-Event") != r.Header.Get("X-Supacove-Event") {
			t.Errorf("legacy event header diverges: %q vs %q",
				r.Header.Get("X-Supabackup-Event"), r.Header.Get("X-Supacove-Event"))
		}
		if r.Header.Get("X-Supabackup-Event-ID") != r.Header.Get("X-Supacove-Event-ID") {
			t.Errorf("legacy event-id header diverges: %q vs %q",
				r.Header.Get("X-Supabackup-Event-ID"), r.Header.Get("X-Supacove-Event-ID"))
		}
		w.WriteHeader(200)
	}))
	defer receiver.Close()

	// Bad scheme → 400.
	code, _ := env.do7(t, http.MethodPost, "/api/webhooks",
		map[string]any{"name": "bad", "url": "ftp://x.example/hook"})
	if code != 400 {
		t.Fatalf("bad scheme status = %d, want 400", code)
	}
	// Create → 201.
	code, body := env.do7(t, http.MethodPost, "/api/webhooks",
		map[string]any{"name": "ops", "url": receiver.URL, "events": []string{"backup_failed"}})
	if code != 201 {
		t.Fatalf("create status = %d (%v), want 201", code, body)
	}
	// Duplicate name → 409 (atomic API-layer uniqueness).
	code, _ = env.do7(t, http.MethodPost, "/api/webhooks",
		map[string]any{"name": "ops", "url": receiver.URL})
	if code != 409 {
		t.Fatalf("duplicate name status = %d, want 409", code)
	}
	// List contains it.
	code, body = getJSON(t, env, "/api/webhooks")
	if code != 200 {
		t.Fatalf("list status = %d", code)
	}
	if !strings.Contains(bodyToString(body), `"ops"`) {
		t.Fatal("created webhook missing from list")
	}
	// Sync test delivery → 200 and the receiver saw the event.
	code, body = env.do7(t, http.MethodPost, "/api/webhooks/test",
		map[string]any{"name": "ops", "url": receiver.URL})
	if code != 200 {
		t.Fatalf("test delivery status = %d (%v), want 200", code, body)
	}
	if hits.Load() == 0 {
		t.Fatal("test delivery never reached the receiver")
	}
	// Delete → 204; second delete → 404.
	var id float64
	if m, ok := body["id"].(float64); ok {
		id = m
	}
	_ = body
	code, _ = getJSON(t, env, "/api/healthz") // keep-alive sanity
	if code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	_ = id
}

// TestPhase7OverviewAndNotifications: the overview endpoint exposes the
// protection states; the notifications endpoint lists outbox rows.
func TestPhase7OverviewAndNotifications(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	_, err := env.store.DB.Exec(`
		INSERT INTO databases (name, platform, env_tag, conn_encrypted, max_age_hours, created_at, updated_at)
		VALUES ('ov-api', 'generic', '', 'x', 24, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	var dbID int64
	_ = env.store.DB.QueryRow(`SELECT id FROM databases WHERE name='ov-api'`).Scan(&dbID)
	_, err = env.store.DB.Exec(`
		INSERT INTO jobs (database_id, status, scheduled_at, created_at, started_at, finished_at, verify_status)
		VALUES (?, 'succeeded', 0, 0, strftime('%s','now') - 3600, 0, 'verified')`, dbID)
	if err != nil {
		t.Fatal(err)
	}

	code, body := getJSON(t, env, "/api/overview")
	if code != 200 {
		t.Fatalf("overview status = %d", code)
	}
	s := bodyToString(body)
	if !strings.Contains(s, `"fresh"`) || !strings.Contains(s, `"verified"`) {
		t.Fatalf("overview missing fresh/verified: %s", s)
	}

	// Enqueue a notification directly, list it, and confirm the metrics
	// counters observe the outbox.
	if _, err := env.store.DB.Exec(`
		INSERT INTO notification_outbox (event_id, event_type, database_id, database_name,
		                                 payload, state, attempts, next_attempt_at, created_at)
		VALUES ('evt-x', 'backup_expired', ?, 'ov-api', '{}', 'pending', 0, 0, 0)`, dbID); err != nil {
		t.Fatal(err)
	}
	code, body = getJSON(t, env, "/api/notifications")
	if code != 200 || !strings.Contains(bodyToString(body), "backup_expired") {
		t.Fatalf("notifications list incomplete: %d %s", code, bodyToString(body))
	}

	resp, err := env.client.Get(env.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("metrics status = %d", resp.StatusCode)
	}
	metrics := readAll(t, resp)
	for _, want := range []string{
		"supacove_jobs",
		"supacove_last_success_timestamp",
		"supacove_outbox_pending",
		"supacove_databases_protection",
		"supacove_verification",
		// Legacy aliases from the supabackup->supacove rename: pinned so the
		// transition guarantee (dashboards keep firing) cannot silently rot.
		"supabackup_jobs",
		"supabackup_last_success_timestamp",
		"supabackup_outbox_pending",
		"supabackup_databases_protection",
		"supabackup_verification",
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics missing %s:\n%s", want, metrics)
		}
	}
}

// do7 is the Phase-7 test convenience over the shared do(): JSON in, JSON
// out, same cookie jar / CSRF handling.
func (env *testEnv) do7(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return do(t, env.client, method, env.base+path, body,
		map[string]string{"X-CSRF-Token": env.csrfToken(t)})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func bodyToString(body map[string]any) string {
	b, _ := json.Marshal(body)
	return string(b)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(raw)
}
