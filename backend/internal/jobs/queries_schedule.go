// Package jobs — Phase 7 data access: schedule/heartbeat configuration,
// webhook CRUD, and the overview protection-state computation.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ScheduleConfig is the per-database automation policy: cron schedule,
// freshness threshold, and dead-man switch configuration.
type ScheduleConfig struct {
	DatabaseID           int64
	CronExpr             string
	CronTZ               string
	MaxAgeHours          int
	Paused               bool
	HeartbeatURL         string
	HeartbeatPeriodHours int
	HeartbeatGraceHours  int
	LastScheduledAt      int64
	LastHeartbeatAt      int64
}

// GetSchedule loads one database's automation policy.
func GetSchedule(ctx context.Context, dbh *sql.DB, dbID int64) (*ScheduleConfig, error) {
	var c ScheduleConfig
	var lastSched, lastHB sql.NullInt64
	err := dbh.QueryRowContext(ctx, `
		SELECT id, COALESCE(NULLIF(cron_expr,''),''), COALESCE(NULLIF(cron_tz,''),'UTC'),
		       max_age_hours, schedule_paused,
		       COALESCE(NULLIF(heartbeat_url,''),''), heartbeat_period_hours, heartbeat_grace_hours,
		       last_scheduled_at, last_heartbeat_at
		FROM databases WHERE id = ? AND deleted_at IS NULL`, dbID).
		Scan(&c.DatabaseID, &c.CronExpr, &c.CronTZ, &c.MaxAgeHours, &c.Paused,
			&c.HeartbeatURL, &c.HeartbeatPeriodHours, &c.HeartbeatGraceHours,
			&lastSched, &lastHB)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDatabaseNotFound
	}
	if err != nil {
		return nil, err
	}
	if lastSched.Valid {
		c.LastScheduledAt = lastSched.Int64
	}
	if lastHB.Valid {
		c.LastHeartbeatAt = lastHB.Int64
	}
	return &c, nil
}

// UpdateSchedule persists the automation policy. Fields are written
// explicitly so a partial update (zero values) is intentional.
func UpdateSchedule(ctx context.Context, dbh *sql.DB, c *ScheduleConfig) error {
	res, err := dbh.ExecContext(ctx, `
		UPDATE databases SET
		  cron_expr = ?, cron_tz = ?, max_age_hours = ?, schedule_paused = ?,
		  heartbeat_url = ?, heartbeat_period_hours = ?, heartbeat_grace_hours = ?,
		  updated_at = strftime('%s','now')
		WHERE id = ? AND deleted_at IS NULL`,
		c.CronExpr, c.CronTZ, c.MaxAgeHours, boolToInt(c.Paused),
		c.HeartbeatURL, c.HeartbeatPeriodHours, c.HeartbeatGraceHours,
		c.DatabaseID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrDatabaseNotFound
	}
	return nil
}

// --- webhooks ---

// WebhookRecord is a stored webhook target.
type WebhookRecord struct {
	ID        int64
	Name      string
	URL       string
	Events    []string
	CreatedAt int64
}

var ErrWebhookNameExists = errors.New("a webhook with this name already exists")

// ValidEventTypes is the subscription vocabulary.
var ValidEventTypes = map[string]bool{
	"backup_failed":       true,
	"backup_expired":      true,
	"verification_failed": true,
}

// CreateWebhook inserts a webhook target (name unique among live rows).
func CreateWebhook(ctx context.Context, dbh *sql.DB, name, url string, events []string) (*WebhookRecord, error) {
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return nil, errors.New("name must be 1-100 characters")
	}
	if len(events) == 0 {
		events = []string{"backup_failed", "backup_expired"}
	}
	for _, e := range events {
		if !ValidEventTypes[e] {
			return nil, errors.New("unknown event type: " + e)
		}
	}
	res, err := dbh.ExecContext(ctx, `
		INSERT INTO webhooks (name, url, events, created_at)
		VALUES (?, ?, ?, strftime('%s','now'))`, name, url, strings.Join(events, ","))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: webhooks.name") {
			return nil, ErrWebhookNameExists
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &WebhookRecord{ID: id, Name: name, URL: url, Events: events, CreatedAt: time.Now().Unix()}, nil
}

// ListWebhooks returns all live webhook targets.
func ListWebhooks(ctx context.Context, dbh *sql.DB) ([]WebhookRecord, error) {
	rows, err := dbh.QueryContext(ctx, `
		SELECT id, name, url, events, created_at FROM webhooks
		WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookRecord
	for rows.Next() {
		var w WebhookRecord
		var events string
		if err := rows.Scan(&w.ID, &w.Name, &w.URL, &events, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.Events = strings.Split(events, ",")
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWebhook soft-deletes a target.
func DeleteWebhook(ctx context.Context, dbh *sql.DB, id int64) error {
	res, err := dbh.ExecContext(ctx,
		`UPDATE webhooks SET deleted_at = strftime('%s','now') WHERE id = ? AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- overview (Phase 7 task 4) ---

// OverviewEntry is one database's protection state for the homepage.
type OverviewEntry struct {
	DatabaseID             int64
	Name                   string
	Platform               string
	State                  string // fresh | expired | never
	LastSuccessAt          int64  // 0 = none (snapshot instant, not finish)
	LastSuccessAgeHours    float64
	LastSuccessVerifyState string
	LastJobStatus          string
	SchedulePaused         bool
	ScheduleExpr           string
	ScheduleDue            bool
	MaxAgeHours            int
}

// Overview computes the per-database protection states. The state derives
// from the last SUCCESS only — a failed retry never makes a database
// "fresh" (dev-plan Phase 7: 失败重试中不显示为受保护); it is visible via
// LastJobStatus instead.
func Overview(ctx context.Context, dbh *sql.DB, now time.Time, dueFn func(expr string, lastScheduled int64, now time.Time) bool) ([]OverviewEntry, error) {
	rows, err := dbh.QueryContext(ctx, `
		SELECT d.id, d.name, d.platform, d.max_age_hours, d.schedule_paused,
		       COALESCE(NULLIF(d.cron_expr,''),''),
		       (SELECT j.started_at FROM jobs j WHERE j.database_id = d.id AND j.status = 'succeeded' ORDER BY j.id DESC LIMIT 1),
		       (SELECT COALESCE(j.verify_status,'') FROM jobs j WHERE j.database_id = d.id AND j.status = 'succeeded' ORDER BY j.id DESC LIMIT 1),
		       (SELECT COALESCE(j.status,'') FROM jobs j WHERE j.database_id = d.id ORDER BY j.id DESC LIMIT 1)
		FROM databases d
		WHERE d.deleted_at IS NULL
		ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OverviewEntry
	for rows.Next() {
		var e OverviewEntry
		var paused int
		var lastSuccess sql.NullInt64
		var verifyState, lastJob sql.NullString
		if err := rows.Scan(&e.DatabaseID, &e.Name, &e.Platform, &e.MaxAgeHours, &paused,
			&e.ScheduleExpr, &lastSuccess, &verifyState, &lastJob); err != nil {
			return nil, err
		}
		e.SchedulePaused = paused != 0
		if verifyState.Valid {
			e.LastSuccessVerifyState = verifyState.String
		}
		if lastJob.Valid {
			e.LastJobStatus = lastJob.String
		}
		if lastSuccess.Valid && lastSuccess.Int64 > 0 {
			e.LastSuccessAt = lastSuccess.Int64
			age := now.Sub(time.Unix(lastSuccess.Int64, 0))
			e.LastSuccessAgeHours = age.Hours()
			if e.MaxAgeHours > 0 && age > time.Duration(e.MaxAgeHours)*time.Hour {
				e.State = "expired"
			} else {
				e.State = "fresh"
			}
		} else {
			e.State = "never"
		}
		if e.ScheduleExpr != "" && !e.SchedulePaused {
			e.ScheduleDue = dueFn(e.ScheduleExpr, 0, now)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
