package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/web-casa/supacove/backend/internal/db"
)

func testEvent(id string) Event {
	return Event{
		EventID:      id,
		EventType:    EventBackupFailed,
		DatabaseID:   7,
		DatabaseName: "testdb",
		Payload:      map[string]any{"event": "backup_failed", "job_id": 42},
	}
}

// TestEnqueueDedup: the same event_id enqueued repeatedly produces exactly
// ONE row (polling detectors may fire every tick).
func TestEnqueueDedup(t *testing.T) {
	db := testOpen(t)
	ctx := context.Background()
	now := time.Now()
	for range 3 {
		if err := Enqueue(ctx, db, testEvent("evt-1"), now); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(ctx, db, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("rows = %d, want 1 (dedup by event_id)", len(entries))
	}
	if entries[0].EventID != "evt-1" || entries[0].State != StatePending {
		t.Fatalf("unexpected row: %+v", entries[0])
	}
}

// TestDeliverySuccessAndRetry: a healthy receiver gets the payload; a
// failing receiver drives attempts up through backoff into 'dead'.
func TestDeliverySuccessAndRetry(t *testing.T) {
	db := testOpen(t)
	ctx := context.Background()

	var hits atomic.Int32
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("payload decode: %v", err)
		}
		if payload["job_id"] == nil {
			t.Error("payload missing job_id")
		}
		// The rename transition sends both header generations; the stable
		// event id must be identical across them, and the values must be
		// the actual event (not merely non-empty).
		if r.Header.Get("X-Supacove-Event") != "backup_failed" {
			t.Errorf("X-Supacove-Event = %q, want backup_failed", r.Header.Get("X-Supacove-Event"))
		}
		if r.Header.Get("X-Supacove-Event-ID") != "evt-ok" {
			t.Errorf("X-Supacove-Event-ID = %q, want evt-ok", r.Header.Get("X-Supacove-Event-ID"))
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
	defer okServer.Close()

	seedWebhook(t, db, "hook", okServer.URL, "backup_failed")
	if err := Enqueue(ctx, db, testEvent("evt-ok"), time.Now()); err != nil {
		t.Fatal(err)
	}

	n := New(db, testLogger(), 50*time.Millisecond)
	n.Start(ctx)
	t.Cleanup(n.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := List(ctx, db, 10)
		if len(entries) == 1 && entries[0].State == StateDelivered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries, _ := List(ctx, db, 10)
	if entries[0].State != StateDelivered {
		t.Fatalf("state = %q, want delivered", entries[0].State)
	}
	if entries[0].DeliveredAt == nil {
		t.Fatal("delivered_at not recorded")
	}
	if hits.Load() == 0 {
		t.Fatal("receiver never got the payload")
	}
}

// TestRetryUntilDead: a permanently failing receiver exhausts MaxAttempts
// and lands in 'dead' — visible, never silently dropped.
func TestRetryUntilDead(t *testing.T) {
	db := testOpen(t)
	ctx := context.Background()

	var hits atomic.Int32
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer badServer.Close()

	seedWebhook(t, db, "hook", badServer.URL, "backup_failed")
	if err := Enqueue(ctx, db, testEvent("evt-bad"), time.Now()); err != nil {
		t.Fatal(err)
	}

	// Tiny interval and zero backoff so the RETRY SCHEDULE (not the test
	// wall clock) is the subject under test.
	n := New(db, testLogger(), 20*time.Millisecond)
	n.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	n.Start(ctx)
	t.Cleanup(n.Stop)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := List(ctx, db, 10)
		if len(entries) == 1 && entries[0].State == StateDead {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	entries, _ := List(ctx, db, 10)
	if entries[0].State != StateDead {
		t.Fatalf("state = %q, want dead (attempts=%d hits=%d)", entries[0].State, entries[0].Attempts, hits.Load())
	}
	if entries[0].Attempts < MaxAttempts {
		t.Fatalf("attempts = %d, want >= %d", entries[0].Attempts, MaxAttempts)
	}
	if entries[0].LastError == "" {
		t.Fatal("dead entry must record the last error")
	}
}

// TestNoTargetsIsSuccess: events with zero live webhooks settle as
// delivered (recorded, nothing to deliver).
func TestNoTargetsIsSuccess(t *testing.T) {
	db := testOpen(t)
	ctx := context.Background()
	if err := Enqueue(ctx, db, testEvent("evt-none"), time.Now()); err != nil {
		t.Fatal(err)
	}
	n := New(db, testLogger(), 20*time.Millisecond)
	n.Start(ctx)
	t.Cleanup(n.Stop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := List(ctx, db, 10)
		if len(entries) == 1 && entries[0].State == StateDelivered {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("entry with no targets never settled as delivered")
}

// TestRecoverStuck: entries stranded in 'delivering' by a crash go back to
// 'pending' at startup and get re-delivered (at-least-once).
func TestRecoverStuck(t *testing.T) {
	db := testOpen(t)
	ctx := context.Background()
	if err := Enqueue(ctx, db, testEvent("evt-stuck"), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash mid-delivery.
	if _, err := db.Exec(`UPDATE notification_outbox SET state = 'delivering'`); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	seedWebhook(t, db, "hook", server.URL, "backup_failed")

	n := New(db, testLogger(), 20*time.Millisecond)
	n.Start(ctx)
	t.Cleanup(n.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stuck delivering entry was not recovered and re-delivered")
}

// TestLinkLocalDenied: the delivery client refuses link-local destinations
// (cloud metadata protection) even when explicitly configured.
func TestLinkLocalDenied(t *testing.T) {
	client := DeliveryClient(3 * time.Second)
	req, err := http.NewRequest(http.MethodPost, "http://169.254.169.254/latest/meta-data/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("link-local metadata destination must be refused")
	}
}

// --- test helpers ---

func testOpen(t *testing.T) *sql.DB {
	t.Helper()
	// The REAL store open path: full migration set, per-connection PRAGMAs —
	// the notifier must behave against the same schema shape as production.
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store.DB
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func seedWebhook(t *testing.T, db *sql.DB, name, url, events string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO webhooks (name, url, events, created_at) VALUES (?, ?, ?, 0)`,
		name, url, events); err != nil {
		t.Fatal(err)
	}
}
