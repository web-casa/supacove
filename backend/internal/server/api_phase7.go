package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/i18n"
	"github.com/cloudfan/supabackup/backend/internal/jobs"
	"github.com/cloudfan/supabackup/backend/internal/outbox"
	"github.com/cloudfan/supabackup/backend/internal/scheduler"
)

// Phase 7 API surface: schedule/heartbeat configuration, webhook CRUD with
// a synchronous test delivery, the notification outbox view, and the
// overview protection states. All authenticated by the default-deny guard.

// validateWebhookURL enforces the SSRF boundary at configuration time:
// http/https only, no link-local literals (cloud metadata endpoints).
// Delivery-time dial control (outbox.New) re-checks every resolution.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("URL must include a host")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return errors.New("link-local addresses are not allowed (cloud metadata protection)")
		}
	}
	if strings.EqualFold(host, "metadata.google.internal") {
		return errors.New("metadata endpoints are not allowed")
	}
	return nil
}

// --- schedule / heartbeat ---

func scheduleToAPI(c *jobs.ScheduleConfig) api.ScheduleConfig {
	out := api.ScheduleConfig{
		DatabaseId:           c.DatabaseID,
		CronTz:               c.CronTZ,
		MaxAgeHours:          c.MaxAgeHours,
		Paused:               c.Paused,
		HeartbeatPeriodHours: c.HeartbeatPeriodHours,
		HeartbeatGraceHours:  c.HeartbeatGraceHours,
	}
	if c.CronExpr != "" {
		out.CronExpr = &c.CronExpr
	}
	if c.HeartbeatURL != "" {
		out.HeartbeatUrl = &c.HeartbeatURL
	}
	if c.LastScheduledAt > 0 {
		v := c.LastScheduledAt
		out.LastScheduledAt = &v
	}
	if c.LastHeartbeatAt > 0 {
		v := c.LastHeartbeatAt
		out.LastHeartbeatAt = &v
	}
	return out
}

func (a *apiService) GetDatabaseSchedule(ctx context.Context, request api.GetDatabaseScheduleRequestObject) (api.GetDatabaseScheduleResponseObject, error) {
	c, err := jobs.GetSchedule(ctx, a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrDatabaseNotFound) {
		return api.GetDatabaseSchedule404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
	}
	if err != nil {
		a.srv.log.Error("get schedule", "err", err)
		return api.GetDatabaseSchedule500JSONResponse{}, nil
	}
	return api.GetDatabaseSchedule200JSONResponse(scheduleToAPI(c)), nil
}

func (a *apiService) PutDatabaseSchedule(ctx context.Context, request api.PutDatabaseScheduleRequestObject) (api.PutDatabaseScheduleResponseObject, error) {
	body := request.Body
	if body == nil {
		return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "request body required", "缺少请求体")}, nil
	}
	c, err := jobs.GetSchedule(ctx, a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrDatabaseNotFound) {
		return api.PutDatabaseSchedule404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
	}
	if err != nil {
		a.srv.log.Error("get schedule for update", "err", err)
		return api.PutDatabaseSchedule500JSONResponse{}, nil
	}

	if body.CronExpr != nil {
		expr := strings.TrimSpace(*body.CronExpr)
		if len(expr) > 100 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "cronExpr too long", "cronExpr 过长")}, nil
		}
		if err := scheduler.ValidateCronExpr(expr); err != nil {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: cronMsg(ctx, err)}, nil
		}
		c.CronExpr = expr
	}
	if body.CronTz != nil {
		tz := strings.TrimSpace(*body.CronTz)
		if tz == "" {
			tz = "UTC"
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "unknown IANA timezone: ", "未知 IANA 时区：") + tz}, nil
		}
		c.CronTZ = tz
	}
	if body.MaxAgeHours != nil {
		if *body.MaxAgeHours < 0 || *body.MaxAgeHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "maxAgeHours must be 0-8760", "maxAgeHours 需在 0–8760 之间")}, nil
		}
		c.MaxAgeHours = *body.MaxAgeHours
	}
	if body.Paused != nil {
		c.Paused = *body.Paused
	}
	if body.HeartbeatUrl != nil {
		hb := strings.TrimSpace(*body.HeartbeatUrl)
		// "-" is the reserved explicit-DISABLE marker (review round-2: the
		// API previously rejected it as an invalid URL, so the documented
		// disable path was unreachable). It needs no period and no
		// URL validation; everything else is a real URL.
		if hb != "-" && hb != "" {
			if len(hb) > 500 {
				return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "heartbeatUrl too long", "heartbeatUrl 过长")}, nil
			}
			if err := validateWebhookURL(hb); err != nil {
				return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "heartbeatUrl: ", "heartbeatUrl：") + webhookURLMsg(ctx, err)}, nil
			}
		}
		c.HeartbeatURL = hb
	}
	if body.HeartbeatPeriodHours != nil {
		if *body.HeartbeatPeriodHours < 0 || *body.HeartbeatPeriodHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "heartbeatPeriodHours must be 0-8760", "heartbeatPeriodHours 需在 0–8760 之间")}, nil
		}
		c.HeartbeatPeriodHours = *body.HeartbeatPeriodHours
	}
	if body.HeartbeatGraceHours != nil {
		if *body.HeartbeatGraceHours < 0 || *body.HeartbeatGraceHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "heartbeatGraceHours must be 0-8760", "heartbeatGraceHours 需在 0–8760 之间")}, nil
		}
		c.HeartbeatGraceHours = *body.HeartbeatGraceHours
	}
	// A real heartbeat URL without a period has no silence semantics
	// (the "-" disable marker and empty inherit are exempt).
	if c.HeartbeatURL != "" && c.HeartbeatURL != "-" && c.HeartbeatPeriodHours == 0 {
		return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request",
			Message: i18n.T(ctx, "heartbeatPeriodHours is required when a heartbeatUrl is set (the dead-man switch needs an expected period)", "设置了心跳 URL 时必须提供 heartbeatPeriodHours（死人开关需要期望周期）")}, nil
	}

	if err := jobs.UpdateSchedule(ctx, a.srv.store.DB, c); err != nil {
		if errors.Is(err, jobs.ErrDatabaseNotFound) {
			return api.PutDatabaseSchedule404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
		}
		a.srv.log.Error("update schedule", "err", err)
		return api.PutDatabaseSchedule500JSONResponse{}, nil
	}
	a.srv.log.Info("schedule updated", "database_id", request.Id,
		"cron", c.CronExpr != "", "max_age", c.MaxAgeHours, "heartbeat", c.HeartbeatURL != "")
	return api.PutDatabaseSchedule200JSONResponse(scheduleToAPI(c)), nil
}

// --- webhooks ---

func webhookToAPI(w *jobs.WebhookRecord) api.Webhook {
	events := make([]api.WebhookEvents, 0, len(w.Events))
	for _, e := range w.Events {
		events = append(events, api.WebhookEvents(e))
	}
	return api.Webhook{
		Id:        w.ID,
		Name:      w.Name,
		Url:       w.URL,
		Events:    events,
		CreatedAt: w.CreatedAt,
	}
}

func (a *apiService) ListWebhooks(ctx context.Context, _ api.ListWebhooksRequestObject) (api.ListWebhooksResponseObject, error) {
	ws, err := jobs.ListWebhooks(ctx, a.srv.store.DB)
	if err != nil {
		a.srv.log.Error("list webhooks", "err", err)
		return api.ListWebhooks500JSONResponse{}, nil
	}
	out := make([]api.Webhook, 0, len(ws))
	for i := range ws {
		out = append(out, webhookToAPI(&ws[i]))
	}
	return api.ListWebhooks200JSONResponse{Webhooks: out}, nil
}

func webhookEventsFromAPI(items *[]api.WebhookCreateEvents) []string {
	if items == nil {
		return nil
	}
	out := make([]string, 0, len(*items))
	for _, e := range *items {
		out = append(out, string(e))
	}
	return out
}

func (a *apiService) CreateWebhook(ctx context.Context, request api.CreateWebhookRequestObject) (api.CreateWebhookResponseObject, error) {
	body := request.Body
	if body == nil {
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "request body required", "缺少请求体")}, nil
	}
	if err := validateWebhookURL(body.Url); err != nil {
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "url: ", "url：") + webhookURLMsg(ctx, err)}, nil
	}
	w, err := jobs.CreateWebhook(ctx, a.srv.store.DB, body.Name, body.Url, webhookEventsFromAPI(body.Events))
	switch {
	case err == nil:
	case errors.Is(err, jobs.ErrWebhookNameExists):
		// The sentinel's text is fixed English; localize the response here.
		return api.CreateWebhook409JSONResponse{Code: "name_exists", Message: i18n.T(ctx, "a webhook with this name already exists", "同名 webhook 已存在")}, nil
	default:
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: webhookCreateMsg(ctx, err)}, nil
	}
	a.srv.log.Info("webhook created", "id", w.ID, "name", w.Name)
	return api.CreateWebhook201JSONResponse(webhookToAPI(w)), nil
}

func (a *apiService) DeleteWebhook(ctx context.Context, request api.DeleteWebhookRequestObject) (api.DeleteWebhookResponseObject, error) {
	err := jobs.DeleteWebhook(ctx, a.srv.store.DB, request.Id)
	switch {
	case err == nil:
		return api.DeleteWebhook204Response{}, nil
	case errors.Is(err, jobs.ErrNotFound):
		return api.DeleteWebhook404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "webhook not found", "webhook 不存在")}, nil
	default:
		a.srv.log.Error("delete webhook", "err", err)
		return api.DeleteWebhook500JSONResponse{}, nil
	}
}

// TestWebhook delivers a test event SYNCHRONOUSLY so the admin sees the
// result immediately. It uses the same URL validation and the same
// link-local-denying transport as real deliveries.
func (a *apiService) TestWebhook(ctx context.Context, request api.TestWebhookRequestObject) (api.TestWebhookResponseObject, error) {
	body := request.Body
	if body == nil || body.Url == "" {
		return api.TestWebhook400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "url is required", "url 为必填项")}, nil
	}
	if err := validateWebhookURL(body.Url); err != nil {
		return api.TestWebhook400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "url: ", "url：") + webhookURLMsg(ctx, err)}, nil
	}
	name := body.Name
	if name == "" {
		name = "test"
	}
	payloadBytes, err := json.Marshal(map[string]any{
		"event": "webhook_test", "name": name, "sent_at": time.Now().Unix(),
	})
	if err != nil {
		return api.TestWebhook400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "name produced invalid JSON", "name 生成的 JSON 无效")}, nil
	}
	delivered, detail := a.srv.testWebhookDelivery(ctx, body.Url, "webhook_test", "test-"+time.Now().UTC().Format("20060102T150405.000000000"), string(payloadBytes))
	res := api.WebhookTestResult{Delivered: delivered}
	if detail != "" {
		localized := deliveryDetailMsg(ctx, detail)
		res.Detail = &localized
	}
	if !delivered {
		return api.TestWebhook422JSONResponse(res), nil
	}
	return api.TestWebhook200JSONResponse(res), nil
}

// --- notifications (outbox view) ---

func (a *apiService) ListNotifications(ctx context.Context, request api.ListNotificationsRequestObject) (api.ListNotificationsResponseObject, error) {
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	entries, err := outbox.List(ctx, a.srv.store.DB, limit)
	if err != nil {
		a.srv.log.Error("list notifications", "err", err)
		return api.ListNotifications500JSONResponse{}, nil
	}
	out := make([]api.Notification, 0, len(entries))
	for _, e := range entries {
		n := api.Notification{
			Id:           e.ID,
			EventId:      e.EventID,
			EventType:    api.NotificationEventType(e.EventType),
			DatabaseName: e.DatabaseName,
			State:        api.NotificationState(e.State),
			Attempts:     e.Attempts,
			CreatedAt:    e.CreatedAt,
		}
		if e.LastError != "" {
			le := e.LastError
			n.LastError = &le
		}
		if e.DeliveredAt != nil {
			n.DeliveredAt = e.DeliveredAt
		}
		out = append(out, n)
	}
	return api.ListNotifications200JSONResponse{Notifications: out}, nil
}

// --- overview (Phase 7 task 4) ---

func (a *apiService) GetOverview(ctx context.Context, _ api.GetOverviewRequestObject) (api.GetOverviewResponseObject, error) {
	now := time.Now()
	entries, err := jobs.Overview(ctx, a.srv.store.DB, now,
		func(expr string, lastScheduled int64, now time.Time) bool {
			return scheduler.DueNow(expr, lastScheduled, now)
		})
	if err != nil {
		a.srv.log.Error("overview", "err", err)
		return api.GetOverview500JSONResponse{}, nil
	}
	out := make([]api.OverviewEntry, 0, len(entries))
	for _, e := range entries {
		o := api.OverviewEntry{
			DatabaseId: e.DatabaseID,
			Name:       e.Name,
			Platform:   api.OverviewEntryPlatform(e.Platform),
			State:      api.OverviewEntryState(e.State),
		}
		paused := e.SchedulePaused
		o.SchedulePaused = &paused
		maxAge := e.MaxAgeHours
		o.MaxAgeHours = &maxAge
		if e.LastSuccessAt > 0 {
			v := e.LastSuccessAt
			o.LastSuccessAt = &v
			age := e.LastSuccessAgeHours
			o.LastSuccessAgeHours = &age
		}
		if e.LastSuccessVerifyState != "" {
			v := api.OverviewEntryLastSuccessVerifyStatus(e.LastSuccessVerifyState)
			o.LastSuccessVerifyStatus = &v
		}
		if e.LastJobStatus != "" {
			v := api.OverviewEntryLastJobStatus(e.LastJobStatus)
			o.LastJobStatus = &v
		}
		out = append(out, o)
	}
	return api.GetOverview200JSONResponse{Databases: out}, nil
}

// testWebhookDelivery performs one synchronous webhook delivery with the
// shared SSRF-bounded client. Returns (delivered, humanDetail); the detail
// never embeds the URL.
func (s *Server) testWebhookDelivery(ctx context.Context, url, eventType, eventID, payload string) (bool, string) {
	client := outbox.DeliveryClient(10 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supabackup-Event", eventType)
	req.Header.Set("X-Supabackup-Event-ID", eventID)
	resp, err := client.Do(req)
	if err != nil {
		s.log.Error("webhook test delivery failed", "err_type", fmt.Sprintf("%T", err))
		return false, "delivery failed (network or refused destination; see server log)"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("receiver returned status %d", resp.StatusCode)
	}
	return true, ""
}

// GetStats serves the Phase-8 dashboard statistics: job outcomes, success
// rate, the three volume metrics (source DB physical size, dump archive,
// age ciphertext), and entity counts. The source-size total sums the NEWEST
// known value per database (older backups of the same database would double
// count a size that only has a current meaning).
func (a *apiService) GetStats(ctx context.Context, _ api.GetStatsRequestObject) (api.GetStatsResponseObject, error) {
	var total, succeeded, failed, canceled int64
	var avgDur sql.NullFloat64
	var totalArtifact, totalDump int64
	err := a.srv.store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 END), 0),
		       AVG(CASE WHEN status = 'succeeded' AND duration_secs > 0 THEN duration_secs END),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN artifact_size END), 0)
		FROM jobs WHERE status NOT IN ('pending','running')`).
		Scan(&total, &succeeded, &failed, &canceled, &avgDur, &totalArtifact)
	if err != nil {
		a.srv.log.Error("stats query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}
	// The compressed-archive total lives in backup_stats (the jobs table
	// never carried it) — round-2 review R2-P1-01: this was a deterministic
	// "no such column: dump_size" 500.
	err = a.srv.store.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(dump_size), 0) FROM backup_stats WHERE dump_size > 0`).
		Scan(&totalDump)
	if err != nil {
		a.srv.log.Error("stats dump-size query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}
	rate := func(part, whole int64) *float64 {
		if whole <= 0 {
			return nil // no denominator: report unknown, never a fake 0/100
		}
		v := float64(part) / float64(whole) * 100
		return &v
	}
	successRate := rate(succeeded, total)

	// Segmented success rates (Phase 8): each stage gets its own honest
	// denominator over TERMINAL jobs.
	//   export: dump phase succeeded = succeeded jobs PLUS upload-phase
	//     failures (the dump itself was fine; only the upload failed).
	//     Canceled/interrupted jobs never attempted a dump — excluded.
	//   remote: committed (or deleted after committed life) vs upload failures.
	//   verify: verified vs failed/unsupported among SUCCEEDED backups
	//     (matches the contract wording exactly).
	var exportOK, remoteOK, remoteFailed, vOK, vBad, terminal int64
	err = a.srv.store.DB.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN status = 'succeeded' OR
		                    (status = 'failed' AND error_class = 'storage_upload') THEN 1 END), 0),
		  COALESCE(SUM(CASE WHEN remote_state IN ('committed','deleted') THEN 1 END), 0),
		  COALESCE(SUM(CASE WHEN status = 'failed' AND error_class = 'storage_upload' THEN 1 END), 0),
		  COALESCE(SUM(CASE WHEN status = 'succeeded' AND verify_status = 'verified' THEN 1 END), 0),
		  COALESCE(SUM(CASE WHEN status = 'succeeded' AND verify_status IN ('failed','unsupported') THEN 1 END), 0),
		  COUNT(*)
		FROM jobs WHERE status NOT IN ('pending','running')`).
		Scan(&exportOK, &remoteOK, &remoteFailed, &vOK, &vBad, &terminal)
	if err != nil {
		a.srv.log.Error("stats segment query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}

	// Notification delivery segment (Phase 7 outbox): delivered vs dead.
	var nDelivered, nDead int64
	if err := a.srv.store.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN state = 'delivered' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN state = 'dead' THEN 1 END), 0)
		FROM notification_outbox`).Scan(&nDelivered, &nDead); err != nil {
		a.srv.log.Error("stats notify segment query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}

	var lastSuccess int64
	if err := a.srv.store.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(finished_at),0) FROM jobs WHERE status = 'succeeded'`).Scan(&lastSuccess); err != nil {
		a.srv.log.Error("stats last-success query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}

	var dbCount, destCount int
	if err := a.srv.store.DB.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM databases WHERE deleted_at IS NULL),
		        (SELECT COUNT(*) FROM destinations WHERE deleted_at IS NULL)`).
		Scan(&dbCount, &destCount); err != nil {
		a.srv.log.Error("stats counts query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}

	// Newest KNOWN physical size per database — GROUP BY first (linear plan),
	// then join the winning row (round-1 review P2-02: the correlated
	// MAX(id) subquery was quadratic in job history).
	var totalSource int64
	if err := a.srv.store.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(j2.source_db_bytes), 0)
		FROM (SELECT database_id, MAX(id) AS mid FROM jobs
		      WHERE status = 'succeeded' AND source_db_bytes > 0
		      GROUP BY database_id) m
		JOIN jobs j2 ON j2.id = m.mid`).Scan(&totalSource); err != nil {
		a.srv.log.Error("stats source-size query", "err", err)
		return api.GetStats500JSONResponse{}, nil
	}

	var avg *float64
	if avgDur.Valid {
		v := avgDur.Float64
		avg = &v // no measured rows → null (unknown), never a fake 0.0s
	}
	last := lastSuccess
	uptime := int64(time.Since(a.srv.started).Seconds())
	out := api.GetStats200JSONResponse{
		TotalJobs:          total,
		Succeeded:          succeeded,
		Failed:             failed,
		Canceled:           canceled,
		SuccessRate:        successRate,
		AvgDurationSecs:    avg,
		TotalArtifactBytes: totalArtifact,
		TotalDumpBytes:     &totalDump,
		TotalSourceBytes:   &totalSource,
		LastSuccessAt:      &last,
		Databases:          dbCount,
		Destinations:       destCount,
		UptimeSeconds:      &uptime,
	}
	out.ExportSuccessRate = rate(exportOK, terminal)
	out.RemoteSuccessRate = rate(remoteOK, remoteOK+remoteFailed)
	out.VerifySuccessRate = rate(vOK, vOK+vBad)
	out.NotifySuccessRate = rate(nDelivered, nDelivered+nDead)
	return out, nil
}
