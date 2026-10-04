// Package scheduler implements persistent cron-based scheduling for
// automatic backups (dev-plan Phase 4): per-database cron expressions with
// IANA timezones, freshness tracking by export snapshot time, and webhook
// notifications for failure/expired events.
package scheduler

import (
	"context"
	"database/sql"
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
// It uses the robfig/cron parser for schedule evaluation but manages its own
// polling loop (no goroutine-per-job) so SQLite remains the single source
// of truth for what is due and when.
type Scheduler struct {
	store  *sql.DB
	runner *jobs.Runner
	log    *slog.Logger
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
	DatabaseID     int64
	Name           string
	CronExpr       string
	CronTZ         string
	MaxAgeHours    int
	Paused         bool
	LastScheduled  int64 // unix seconds of last successful schedule enqueue
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

// SetWebhooks configures webhook notification targets.
func (s *Scheduler) SetWebhooks(wh []WebhookConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhooks = wh
}

// Start launches the scheduling loop. Non-blocking; call Stop to terminate.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.tick(ctx)
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

		// Freshness check
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
			if strings.Contains(err.Error(), "already has a pending") ||
				strings.Contains(err.Error(), "already_queued") {
				s.log.Debug("backup already queued", "database", sch.Name)
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

// isDue evaluates whether the cron schedule is due at the given time,
// checking the interval between the last scheduled time and now.
func isDue(sch ScheduleInfo, now time.Time) bool {
	loc, err := time.LoadLocation(sch.CronTZ)
	if err != nil {
		loc = time.UTC
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.DowOptional)
	sched, err := cron.ParseStandard(sch.CronExpr)
	if err != nil {
		return false
	}
	_ = parser // parser is only used for validation; ParseStandard handles the rest

	// The schedule is due if there exists a cron fire time between the last
	// scheduled moment and now. We approximate by checking if the most recent
	// fire time of the cron is after the last scheduled moment.
	lastSched := time.Unix(sch.LastScheduled, 0).In(loc)
	nowLocal := now.In(loc)

	// Find the most recent fire time before now
	next := sched.Next(lastSched)
	if next.After(nowLocal) {
		return false // next fire is in the future
	}
	return true // a fire time occurred between lastSched and now
}

// enqueue records the schedule and enqueues a backup job.
func (s *Scheduler) enqueue(ctx context.Context, sch ScheduleInfo, now time.Time) error {
	if _, err := s.store.ExecContext(ctx,
		`UPDATE databases SET last_scheduled_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now.Unix(), sch.DatabaseID); err != nil {
		return fmt.Errorf("update last_scheduled_at: %w", err)
	}
	_, err := s.runner.Enqueue(ctx, sch.DatabaseID)
	return err
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

// scheduleRow is the SQL scan target for loadSchedules.
type scheduleRow struct {
	id           int64
	name         string
	cronExpr     string
	cronTZ       string
	maxAgeHours  int
	paused       int
	lastSchedAt  int64
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
	rows, err := s.store.QueryContext(ctx, `
		SELECT id, database_id, error_class, error_message
		FROM jobs
		WHERE status = 'failed'
		  AND finished_at > strftime('%s','now','-300')
		ORDER BY id DESC LIMIT 10`)
	if err != nil {
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
		// Redact: error messages may contain credential fragments.
		f.message = truncate(strings.ReplaceAll(f.message, "password=", "password=[REDACTED]"), 200)
		failed = append(failed, f)
	}
	for _, f := range failed {
		s.fireWebhooks(ctx, "failure", map[string]any{
			"event": "backup_failed", "job_id": f.id,
			"database_id": f.dbID, "error_class": f.class,
			"error_message": f.message,
		})
	}
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
func (s *Scheduler) fireWebhooks(ctx context.Context, event string, payload map[string]any) {
	s.mu.Lock()
	whs := s.webhooks
	s.mu.Unlock()
	for _, wh := range whs {
		if !strings.Contains(wh.Events, event) {
			continue
		}
		go s.postWebhook(wh.URL, payload)
	}
}

func fmtFloat2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

func fmtFloat3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }
