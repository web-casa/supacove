package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/api"
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
		return api.GetDatabaseSchedule404JSONResponse{Code: "not_found", Message: "database not found"}, nil
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
		return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "request body required"}, nil
	}
	c, err := jobs.GetSchedule(ctx, a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrDatabaseNotFound) {
		return api.PutDatabaseSchedule404JSONResponse{Code: "not_found", Message: "database not found"}, nil
	}
	if err != nil {
		a.srv.log.Error("get schedule for update", "err", err)
		return api.PutDatabaseSchedule500JSONResponse{}, nil
	}

	if body.CronExpr != nil {
		expr := strings.TrimSpace(*body.CronExpr)
		if len(expr) > 100 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "cronExpr too long"}, nil
		}
		if err := scheduler.ValidateCronExpr(expr); err != nil {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
		}
		c.CronExpr = expr
	}
	if body.CronTz != nil {
		tz := strings.TrimSpace(*body.CronTz)
		if tz == "" {
			tz = "UTC"
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "unknown IANA timezone: " + tz}, nil
		}
		c.CronTZ = tz
	}
	if body.MaxAgeHours != nil {
		if *body.MaxAgeHours < 0 || *body.MaxAgeHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "maxAgeHours must be 0-8760"}, nil
		}
		c.MaxAgeHours = *body.MaxAgeHours
	}
	if body.Paused != nil {
		c.Paused = *body.Paused
	}
	if body.HeartbeatUrl != nil {
		hb := strings.TrimSpace(*body.HeartbeatUrl)
		if hb != "" {
			if len(hb) > 500 {
				return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "heartbeatUrl too long"}, nil
			}
			if err := validateWebhookURL(hb); err != nil {
				return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "heartbeatUrl: " + err.Error()}, nil
			}
		}
		c.HeartbeatURL = hb
	}
	if body.HeartbeatPeriodHours != nil {
		if *body.HeartbeatPeriodHours < 0 || *body.HeartbeatPeriodHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "heartbeatPeriodHours must be 0-8760"}, nil
		}
		c.HeartbeatPeriodHours = *body.HeartbeatPeriodHours
	}
	if body.HeartbeatGraceHours != nil {
		if *body.HeartbeatGraceHours < 0 || *body.HeartbeatGraceHours > 8760 {
			return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request", Message: "heartbeatGraceHours must be 0-8760"}, nil
		}
		c.HeartbeatGraceHours = *body.HeartbeatGraceHours
	}
	// A heartbeat URL without a period has no silence semantics.
	if c.HeartbeatURL != "" && c.HeartbeatPeriodHours == 0 {
		return api.PutDatabaseSchedule400JSONResponse{Code: "invalid_request",
			Message: "heartbeatPeriodHours is required when a heartbeatUrl is set (the dead-man switch needs an expected period)"}, nil
	}

	if err := jobs.UpdateSchedule(ctx, a.srv.store.DB, c); err != nil {
		if errors.Is(err, jobs.ErrDatabaseNotFound) {
			return api.PutDatabaseSchedule404JSONResponse{Code: "not_found", Message: "database not found"}, nil
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
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: "request body required"}, nil
	}
	if err := validateWebhookURL(body.Url); err != nil {
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: "url: " + err.Error()}, nil
	}
	w, err := jobs.CreateWebhook(ctx, a.srv.store.DB, body.Name, body.Url, webhookEventsFromAPI(body.Events))
	switch {
	case err == nil:
	case errors.Is(err, jobs.ErrWebhookNameExists):
		return api.CreateWebhook409JSONResponse{Code: "name_exists", Message: err.Error()}, nil
	default:
		return api.CreateWebhook400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
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
		return api.DeleteWebhook404JSONResponse{Code: "not_found", Message: "webhook not found"}, nil
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
		return api.TestWebhook400JSONResponse{Code: "invalid_request", Message: "url is required"}, nil
	}
	if err := validateWebhookURL(body.Url); err != nil {
		return api.TestWebhook400JSONResponse{Code: "invalid_request", Message: "url: " + err.Error()}, nil
	}
	name := body.Name
	if name == "" {
		name = "test"
	}
	payload := fmt.Sprintf(`{"event":"webhook_test","name":%q,"sent_at":%d}`, name, time.Now().Unix())
	delivered, detail := a.srv.testWebhookDelivery(ctx, body.Url, payload)
	res := api.WebhookTestResult{Delivered: delivered}
	if detail != "" {
		res.Detail = &detail
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
func (s *Server) testWebhookDelivery(ctx context.Context, url, payload string) (bool, string) {
	client := outbox.DeliveryClient(10 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supabackup-Event", "webhook_test")
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
