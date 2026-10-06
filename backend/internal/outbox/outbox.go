// Package outbox implements the transactional notification outbox
// (dev-plan Phase 7 task 1, review P1-12): notifications are recorded in
// the SAME SQLite transaction as the state change that produced them, then
// delivered asynchronously with bounded exponential backoff. A crash after
// commit never loses a notification; a webhook outage never blocks or
// rolls back a backup.
//
// Delivery semantics:
//   - event_id is the dedup key (UNIQUE): re-enqueueing the same event is a
//     no-op, so polling detectors (freshness checks) can fire every tick.
//   - At-least-once delivery to every webhook subscribed to the event type;
//     receivers must tolerate duplicates.
//   - attempts cap → state 'dead' (visible, never silently dropped).
//   - Rows stuck in 'delivering' after a crash are reset to 'pending' at
//     startup (recovery convergence).
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/netguard"
)

// Event types (also the webhook subscription keys in webhooks.events).
const (
	EventBackupFailed       = "backup_failed"
	EventBackupExpired      = "backup_expired"
	EventVerificationFailed = "verification_failed"
)

// States.
const (
	StatePending    = "pending"
	StateDelivering = "delivering"
	StateDelivered  = "delivered"
	StateDead       = "dead"
)

// MaxAttempts bounds delivery retries before an entry goes dead.
const MaxAttempts = 5

// Event is one notification to deliver.
type Event struct {
	EventID      string // dedup key, e.g. "backup_failed:job:42"
	EventType    string // one of the Event* constants
	DatabaseID   int64
	DatabaseName string
	Payload      map[string]any
}

// dbExec is the shared *sql.DB / *sql.Tx interface (same-transaction enqueue).
type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// EnqueueTx records a notification within the CALLER's transaction, so the
// notification commits atomically with the state change that caused it.
// Duplicate event_ids are silently ignored (idempotent detectors).
func EnqueueTx(ctx context.Context, dbh dbExec, e Event, now time.Time) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = dbh.ExecContext(ctx, `
		INSERT INTO notification_outbox
		      (event_id, event_type, database_id, database_name, payload,
		       state, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(event_id) DO NOTHING`,
		e.EventID, e.EventType, nullID(e.DatabaseID), e.DatabaseName,
		string(payload), StatePending, now.Unix(), now.Unix())
	return err
}

// Enqueue is the non-transactional convenience form for callers outside a
// state-changing transaction (e.g. manual test events).
func Enqueue(ctx context.Context, dbh *sql.DB, e Event, now time.Time) error {
	return EnqueueTx(ctx, dbh, e, now)
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// Backoff returns the delay before attempt n+1 (n starts at 1): 30s, 60s,
// 120s, 240s, capped at 1h.
func Backoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= time.Hour {
			return time.Hour
		}
	}
	return d
}

// WebhookTarget is one delivery destination. Events is the subscription CSV.
type WebhookTarget struct {
	ID     int64
	Name   string
	URL    string
	Events string
}

// Notifier delivers outbox entries to the configured webhooks.
type Notifier struct {
	store    *sql.DB
	log      *slog.Logger
	interval time.Duration
	client   *http.Client
	// backoff computes the delay after attempt n (n starts at 1). A field so
	// tests can accelerate the schedule; production uses Backoff.
	backoff func(attempts int) time.Duration
	stopCh  chan struct{}
	done    chan struct{}
}

// New creates a Notifier. The HTTP client denies link-local destinations
// (cloud metadata endpoints) at dial time; loopback/private targets stay
// allowed — self-hosted receivers on the LAN are legitimate (dev-plan P5
// SSRF boundary: 默认拒绝 metadata/link-local).
func New(store *sql.DB, log *slog.Logger, interval time.Duration) *Notifier {
	return &Notifier{
		store:    store,
		log:      log,
		interval: interval,
		client:   DeliveryClient(10 * time.Second),
		backoff:  Backoff,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// DeliveryClient returns the shared webhook HTTP client: bounded timeout
// and a dial-time control that refuses link-local destinations (cloud
// metadata endpoints) no matter what a DNS name resolves to.
func DeliveryClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
				Control: func(network, address string, _ syscall.RawConn) error {
					host, _, err := net.SplitHostPort(address)
					if err != nil {
						return err
					}
					ips, err := net.LookupIP(host)
					if err != nil {
						return err
					}
					for _, ip := range ips {
						if err := netguard.Check(ip); err != nil {
							return fmt.Errorf("%w for webhooks", err)
						}
					}
					return nil
				},
			}).DialContext,
		},
	}
}

// Start launches the delivery loop and performs crash convergence:
// entries stuck in 'delivering' from a previous process go back to
// 'pending' so they are re-delivered (at-least-once).
func (n *Notifier) Start(ctx context.Context) {
	n.recoverStuck(ctx)
	go func() {
		defer close(n.done)
		defer func() {
			if rec := recover(); rec != nil {
				n.log.Error("notifier panic recovered", "panic", rec)
			}
		}()
		ticker := time.NewTicker(n.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-n.stopCh:
				return
			case <-ticker.C:
				n.deliverDue(ctx)
			}
		}
	}()
}

// Stop terminates the loop and waits for the current pass to finish.
func (n *Notifier) Stop() {
	close(n.stopCh)
	<-n.done
}

// recoverStuck resets 'delivering' rows to 'pending' (crash convergence).
func (n *Notifier) recoverStuck(ctx context.Context) {
	res, err := n.store.ExecContext(ctx, `
		UPDATE notification_outbox SET state = 'pending',
		       next_attempt_at = strftime('%s','now')
		WHERE state = 'delivering'`)
	if err != nil {
		n.log.Error("outbox stuck-state recovery failed", "err", err)
		return
	}
	if rn, _ := res.RowsAffected(); rn > 0 {
		n.log.Warn("outbox: reset entries stuck in delivering from a previous crash", "count", rn)
	}
}

// deliverDue claims and delivers up to a bounded batch of due entries.
// The claim (pending→delivering) is a single conditional UPDATE ... RETURNING,
// so concurrent passes cannot double-deliver.
func (n *Notifier) deliverDue(ctx context.Context) {
	n.recoverExpiredLeases(ctx)
	for range 10 {
		claimed, err := n.claimOne(ctx)
		if err != nil {
			n.log.Error("outbox claim failed", "err", err)
			return
		}
		if claimed == nil {
			return // nothing due
		}
		n.deliver(ctx, claimed)
	}
}

// entry is one claimed outbox row.
type entry struct {
	id        int64
	eventID   string
	eventType string
	payload   []byte
	attempts  int
}

func (n *Notifier) claimOne(ctx context.Context) (*entry, error) {
	tx, err := n.store.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE notification_outbox SET state = 'delivering',
		       next_attempt_at = strftime('%s','now') + ?1
		WHERE id = (
			SELECT id FROM notification_outbox
			WHERE state = 'pending' AND next_attempt_at <= strftime('%s','now')
			ORDER BY id LIMIT 1
		)
		RETURNING id, event_id, event_type, payload, attempts`, int64(deliveryLease.Seconds()))
	e := &entry{}
	var payload string
	if err := row.Scan(&e.id, &e.eventID, &e.eventType, &payload, &e.attempts); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	e.payload = []byte(payload)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return e, nil
}

// deliver posts the entry to every webhook subscribed to its event type.
// Zero targets = instant success (the event is recorded, delivery is moot).
func (n *Notifier) deliver(ctx context.Context, e *entry) {
	targets, err := n.targets(ctx, e.eventType)
	if err != nil {
		n.log.Error("outbox target query failed", "err", err)
		n.fail(ctx, e, "target query failed")
		return
	}
	if len(targets) == 0 {
		n.succeed(ctx, e)
		return
	}
	var failures []string
	for _, t := range targets {
		if err := n.post(ctx, t, e.eventType, e.eventID, string(e.payload)); err != nil {
			// Never log the URL (bearer credential); name + category only.
			n.log.Error("webhook delivery failed", "name", t.Name,
				"event", e.eventType, "err_type", fmt.Sprintf("%T", err))
			failures = append(failures, t.Name+": "+errCategory(err))
		}
	}
	if len(failures) > 0 {
		n.fail(ctx, e, strings.Join(failures, "; "))
		return
	}
	n.succeed(ctx, e)
}

// post sends one delivery carrying the event type and the STABLE event id
// (round-1 review P2-01: receivers dedup retries/replays by event id; the
// legacy X-Supabackup-Event header is preserved for existing receivers).
// The response body is drained with a hard cap (never trust a receiver to
// be small).
func (n *Notifier) post(ctx context.Context, t WebhookTarget, eventType, eventID, payload string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supabackup-Event", eventType)
	req.Header.Set("X-Supabackup-Event-ID", eventID)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain with a cap so a hostile receiver cannot balloon memory.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("receiver returned status %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) succeed(ctx context.Context, e *entry) {
	execRetry(ctx, n.store, `
		UPDATE notification_outbox SET state = 'delivered',
		       delivered_at = strftime('%s','now'), last_error = ''
		WHERE id = ? AND state = 'delivering'`, []any{e.id},
		func(err error) { n.log.Error("outbox delivered-state write failed", "id", e.id, "err", err) })
}

// fail schedules the retry with backoff, or kills the entry after the cap.
func (n *Notifier) fail(ctx context.Context, e *entry, errMsg string) {
	attempts := e.attempts + 1
	truncated := errMsg
	if len(truncated) > 300 {
		truncated = truncated[:300]
	}
	if attempts >= MaxAttempts {
		execRetry(ctx, n.store, `
			UPDATE notification_outbox SET state = 'dead', attempts = ?, last_error = ?
			WHERE id = ? AND state = 'delivering'`, []any{attempts, truncated, e.id},
			func(err error) { n.log.Error("outbox dead-state write failed", "id", e.id, "err", err) })
		n.log.Error("notification went DEAD after retries", "event", e.eventType,
			"attempts", attempts, "err", truncated)
		return
	}
	next := time.Now().Add(n.backoff(attempts))
	execRetry(ctx, n.store, `
		UPDATE notification_outbox SET state = 'pending', attempts = ?,
		       next_attempt_at = ?, last_error = ?
		WHERE id = ? AND state = 'delivering'`, []any{attempts, next.Unix(), truncated, e.id},
		func(err error) { n.log.Error("outbox retry-state write failed", "id", e.id, "err", err) })
}

// deliveryLease is how long a 'delivering' claim may run before the runtime
// sweep treats it as crashed and re-queues it. Generous: a pass serially
// delivers to every receiver with a 10s client timeout each.
const deliveryLease = 15 * time.Minute

// recoverExpiredLeases re-queues 'delivering' rows whose lease expired.
// Safe under the single-instance protocol (OS advisory lock): no second
// owner can be mid-delivery on the same row.
func (n *Notifier) recoverExpiredLeases(ctx context.Context) {
	if _, err := n.store.ExecContext(ctx, `
		UPDATE notification_outbox SET state = 'pending'
		WHERE state = 'delivering' AND next_attempt_at < strftime('%s','now')`); err != nil {
		n.log.Error("outbox lease recovery failed", "err", err)
	}
}

// execRetry performs a bounded retry for a result-state write so a transient
// SQLite failure cannot strand a row in 'delivering' forever (round-1 review
// P2-02); the lease sweep is the final net.
func execRetry(ctx context.Context, db *sql.DB, query string, args []any, onFail func(error)) {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err = db.ExecContext(ctx, query, args...); err == nil {
			return
		}
		if attempt == 3 {
			break // never sleep after the final attempt
		}
		select {
		case <-ctx.Done():
			onFail(ctx.Err())
			return
		case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
		}
	}
	onFail(err)
}

func (n *Notifier) targets(ctx context.Context, eventType string) ([]WebhookTarget, error) {
	rows, err := n.store.QueryContext(ctx, `
		SELECT id, name, url, events FROM webhooks
		WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookTarget
	for rows.Next() {
		var t WebhookTarget
		if err := rows.Scan(&t.ID, &t.Name, &t.URL, &t.Events); err != nil {
			return nil, err
		}
		for ev := range strings.SplitSeq(t.Events, ",") {
			if strings.TrimSpace(ev) == eventType {
				out = append(out, t)
				break
			}
		}
	}
	return out, rows.Err()
}

// errCategory reduces an error to its type name (never the message, which
// may embed the URL).
func errCategory(err error) string {
	s := fmt.Sprintf("%T", err)
	s = strings.TrimPrefix(s, "*")
	return strings.ReplaceAll(s, "net/url.Error", "url_error")
}

// EntryView is the API-safe outbox row.
type EntryView struct {
	ID           int64  `json:"id"`
	EventID      string `json:"eventId"`
	EventType    string `json:"eventType"`
	DatabaseName string `json:"databaseName"`
	State        string `json:"state"`
	Attempts     int    `json:"attempts"`
	LastError    string `json:"lastError,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	DeliveredAt  *int64 `json:"deliveredAt,omitempty"`
	Payload      string `json:"-"`
}

// List returns recent outbox entries, newest first.
func List(ctx context.Context, dbh *sql.DB, limit int) ([]EntryView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := dbh.QueryContext(ctx, `
		SELECT id, event_id, event_type, database_name, state, attempts,
		       last_error, created_at, delivered_at
		FROM notification_outbox ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryView
	for rows.Next() {
		var e EntryView
		var delivered sql.NullInt64
		if err := rows.Scan(&e.ID, &e.EventID, &e.EventType, &e.DatabaseName,
			&e.State, &e.Attempts, &e.LastError, &e.CreatedAt, &delivered); err != nil {
			return nil, err
		}
		if delivered.Valid {
			v := delivered.Int64
			e.DeliveredAt = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Counts returns pending and dead entry counts for /metrics.
func Counts(ctx context.Context, dbh *sql.DB) (pending, dead int64, err error) {
	err = dbh.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN state = 'pending' THEN 1 END), 0),
		  COALESCE(SUM(CASE WHEN state = 'dead' THEN 1 END), 0)
		FROM notification_outbox`).Scan(&pending, &dead)
	return
}
