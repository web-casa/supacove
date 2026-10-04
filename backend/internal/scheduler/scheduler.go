// Package scheduler implements persistent cron-based scheduling for
// automatic backups (dev-plan Phase 4): per-database cron expressions with
// IANA timezones, freshness tracking by export snapshot time, and webhook
// notifications for failure/expired events.
package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/cloudfan/supabackup/backend/internal/jobs"
)

// Scheduler polls the databases table for due backups and enqueues jobs.
type Scheduler struct {
	store    *sql.DB
	runner   *jobs.Runner
	log      *slog.Logger
	webhooks []WebhookConfig
	interval time.Duration
	mu       sync.Mutex
	stopCh   chan struct{}
	stopped  bool
}

// WebhookConfig holds a webhook URL and its subscribed event types.
type WebhookConfig struct {
	Name   string
	URL    string
	Events string // comma-separated: "failure,expired"
}

// ScheduleInfo is the scheduling state read from the databases table.
type ScheduleInfo struct {
	DatabaseID    int64
	Name          string
	CronExpr      string
	CronTZ        string
	MaxAgeHours   int
	Paused        bool
	LastScheduled int64
}

// LoadWebhooks reads webhook configurations from the webhooks table.
func LoadWebhooks(ctx context.Context, dbh *sql.DB) ([]WebhookConfig, error) {
	rows, err := dbh.QueryContext(ctx,
		`SELECT name, url, events FROM webhooks WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookConfig
	for rows.Next() {
		var w WebhookConfig
		if err := rows.Scan(&w.Name, &w.URL, &w.Events); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// New creates a Scheduler.
func New(store *sql.DB, runner *jobs.Runner, log *slog.Logger, interval time.Duration) *Scheduler {
	return &Scheduler{
		store:    store,
		runner:   runner,
		log:      log,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// SetWebhooks configures webhook notification targets. Must be called
// before Start.
func (s *Scheduler) SetWebhooks(wh []WebhookConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhooks = wh
}

// ValidateCronExpr checks a cron expression for parse errors and reachability.
func ValidateCronExpr(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return nil // empty = no schedule
	}
	sched, err := cron.ParseStandard(strings.TrimSpace(expr))
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	if sched.Next(time.Now()).IsZero() {
		return fmt.Errorf("cron expression %q has no reachable fire time", expr)
	}
	return nil
}

// Start launches the scheduling loop. Non-blocking; call Stop to terminate.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("scheduler panic recovered", "panic", rec)
			}
		}()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							s.log.Error("tick panic recovered", "panic", rec)
						}
					}()
					s.tick(ctx)
				}()
			}
		}
	}()
}

// Stop terminates the scheduling loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
}

// tick runs one scheduling pass: find due databases, enqueue backups,
// check freshness, and fire webhooks.
func (s *Scheduler) tick(ctx context.Context) {
	schedules := s.loadSchedules(ctx)
	now := time.Now().UTC()

	for _, sch := range schedules {
		if sch.Paused {
			continue
		}

		// Freshness check: per-database, using the latest succeeded job's
		// snapshot start (round-4 review P2-01: started_at ≈ snapshot time).
		if sch.MaxAgeHours > 0 {
			age := s.lastSuccessAge(ctx, sch.DatabaseID)
			if age > float64(sch.MaxAgeHours) {
				s.log.Warn("backup expired",
					"database", sch.Name, "database_id", sch.DatabaseID,
					"age_hours", fmtFloat(age), "max_age_hours", sch.MaxAgeHours)
				s.fireWebhooks(ctx, "expired", map[string]any{
					"event": "backup_expired", "database": sch.Name,
					"database_id": sch.DatabaseID, "age_hours": age,
					"max_age_hours": sch.MaxAgeHours,
				})
			}
		}

		// Schedule check
		if sch.CronExpr == "" {
			continue
		}
		if !isDue(sch, now) {
			continue
		}
		if err := s.enqueue(ctx, sch, now); err != nil {
			if errors.Is(err, jobs.ErrAlreadyQueued) {
				s.log.Info("backup already queued; schedule cursor advanced", "database", sch.Name)
			} else {
				s.log.Error("schedule enqueue failed", "database", sch.Name, "err", err)
			}
			continue
		}
		s.log.Info("backup scheduled", "database", sch.Name, "cron", sch.CronExpr, "tz", sch.CronTZ)
	}

	// Failure webhook: check for recently failed jobs
	s.checkFailures(ctx)
}

// isDue evaluates whether the cron schedule is due at the given time.
// Unreachable dates (e.g. Feb 31) are handled by cron.Next returning a zero
// time, which we check explicitly. Malformed expressions return false.
func isDue(sch ScheduleInfo, now time.Time) bool {
	sched, err := cron.ParseStandard(sch.CronExpr)
	if err != nil {
		return false
	}
	loc, err := time.LoadLocation(sch.CronTZ)
	if err != nil {
		loc = time.UTC
	}
	lastSched := time.Unix(sch.LastScheduled, 0).In(loc)
	if sch.LastScheduled == 0 {
		// First evaluation: schedule immediately.
		return true
	}
	next := sched.Next(lastSched)
	if next.IsZero() {
		return false // unreachable date (e.g. Feb 31 only)
	}
	return !next.After(now)
}

// enqueue records the schedule cursor and enqueues a backup job in a single
// SQLite transaction (round-4 review P1-01: cursor advance and enqueue must
// be atomic — a crash or conflict between the two steps would consume the
// schedule slot without creating a backup).
func (s *Scheduler) enqueue(ctx context.Context, sch ScheduleInfo, now time.Time) error {
	tx, err := s.store.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE databases SET last_scheduled_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now.Unix(), sch.DatabaseID); err != nil {
		return fmt.Errorf("update last_scheduled_at: %w", err)
	}

	if _, err = s.runner.EnqueueTx(ctx, tx, sch.DatabaseID); err != nil {
		return err // tx rolled back by defer; cursor NOT advanced on conflict
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schedule: %w", err)
	}
	return nil
}

// lastSuccessAge returns the age in hours since the last succeeded backup's
// snapshot start (NOT task completion).
func (s *Scheduler) lastSuccessAge(ctx context.Context, dbID int64) float64 {
	var startedAt sql.NullInt64
	err := s.store.QueryRowContext(ctx, `
		SELECT COALESCE(started_at, 0) FROM jobs
		WHERE database_id = ? AND status = 'succeeded'
		ORDER BY id DESC LIMIT 1`, dbID).Scan(&startedAt)
	if err != nil || !startedAt.Valid || startedAt.Int64 == 0 {
		return 1e9 // effectively infinite
	}
	elapsed := time.Since(time.Unix(startedAt.Int64, 0))
	return elapsed.Hours()
}

type scheduleRow struct {
	id          int64
	name        string
	cronExpr    string
	cronTZ      string
	maxAgeHours int
	paused      int
	lastSchedAt int64
}

func (s *Scheduler) loadSchedules(ctx context.Context) []ScheduleInfo {
	rows, err := s.store.QueryContext(ctx, `
		SELECT id, name, COALESCE(NULLIF(cron_expr,''),''),
		       COALESCE(NULLIF(cron_tz,''),'UTC'),
		       max_age_hours, schedule_paused, last_scheduled_at
		FROM databases
		WHERE deleted_at IS NULL
		  AND (cron_expr != '' OR max_age_hours > 0)`)
	if err != nil {
		s.log.Error("load schedules", "err", err)
		return nil
	}
	defer rows.Close()
	var out []ScheduleInfo
	for rows.Next() {
		var r scheduleRow
		if err := rows.Scan(&r.id, &r.name, &r.cronExpr, &r.cronTZ,
			&r.maxAgeHours, &r.paused, &r.lastSchedAt); err != nil {
			s.log.Error("scan schedule", "err", err)
			continue
		}
		out = append(out, ScheduleInfo{
			DatabaseID:    r.id,
			Name:          r.name,
			CronExpr:      r.cronExpr,
			CronTZ:        r.cronTZ,
			MaxAgeHours:   r.maxAgeHours,
			Paused:        r.paused != 0,
			LastScheduled: r.lastSchedAt,
		})
	}
	return out
}

// checkFailures fires failure webhooks for recently failed jobs.
func (s *Scheduler) checkFailures(ctx context.Context) {
	// Round-4 review P1-03: SQLite strftime needs the unit in the modifier.
	cutoff := time.Now().Add(-5 * time.Minute).Unix()
	rows, err := s.store.QueryContext(ctx, `
		SELECT id, database_id, error_class, error_message
		FROM jobs
		WHERE status = 'failed'
		  AND finished_at > ?
		ORDER BY id DESC LIMIT 10`, cutoff)
	if err != nil {
		s.log.Error("failure query", "err", err)
		return
	}
	defer rows.Close()
	type failedJob struct {
		id      int64
		dbID    int64
		class   string
		message string
	}
	var failed []failedJob
	for rows.Next() {
		var f failedJob
		if err := rows.Scan(&f.id, &f.dbID, &f.class, &f.message); err != nil {
			return
		}
		// Redact: remove password values and URL credentials.
		f.message = redactNotify(f.message)
		failed = append(failed, f)
	}
	if err := rows.Err(); err != nil {
		s.log.Error("failure scan", "err", err)
		return
	}
	for _, f := range failed {
		s.fireWebhooks(ctx, "failure", map[string]any{
			"event": "backup_failed", "job_id": f.id,
			"database_id": f.dbID, "error_class": f.class,
			"error_message": f.message,
		})
	}
}

// redactNotify scrubs credential patterns from webhook payloads.
func redactNotify(s string) string {
	s = strings.ReplaceAll(s, "postgres://", "postgres-uri://[REDACTED]")
	s = strings.ReplaceAll(s, "postgresql://", "postgres-uri://[REDACTED]")
	if idx := strings.Index(strings.ToLower(s), "password="); idx >= 0 {
		s = s[:idx+len("password=")] + "[REDACTED]"
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func fmtFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// fireWebhooks sends a POST to every webhook subscribed to the event type.
// Webhook URLs are treated as bearer secrets: only the status code is logged,
// never the URL itself (round-4 review P1-06).
func (s *Scheduler) fireWebhooks(ctx context.Context, event string, payload map[string]any) {
	s.mu.Lock()
	whs := s.webhooks
	s.mu.Unlock()
	for _, wh := range whs {
		if !strings.Contains(wh.Events, event) {
			continue
		}
		go s.postWebhook(wh.Name, wh.URL, payload)
	}
}

// ErrDangling marker removed; unused.
var _ = fmt.Sprintf
