package server

import (
	"context"
	"errors"
	"strings"

	"filippo.io/age"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/i18n"
	"github.com/cloudfan/supabackup/backend/internal/jobs"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
	"github.com/cloudfan/supabackup/backend/internal/redact"
)

// Phase 2 API surface: age status/recipient, database registrations,
// backup triggers and task inspection. All of these sit behind the
// authenticated guard (default-deny).

// ---- age (protocol B) ----

func (a *apiService) ageStatus(ctx context.Context) (api.AgeStatus, error) {
	recipient, err := a.srv.recipientFor(ctx)
	if err != nil {
		return api.AgeStatus{}, err
	}
	if recipient == "" {
		return api.AgeStatus{Configured: false}, nil
	}
	fingerprint := agekey.Fingerprint(recipient)
	return api.AgeStatus{
		Configured: true,
		Recipient:  &recipient,
		KeyId:      &fingerprint,
	}, nil
}

func (a *apiService) GetAgeStatus(ctx context.Context, _ api.GetAgeStatusRequestObject) (api.GetAgeStatusResponseObject, error) {
	st, err := a.ageStatus(ctx)
	if err != nil {
		a.srv.log.Error("age status", "err", err)
		return api.GetAgeStatus500JSONResponse{}, nil
	}
	return api.GetAgeStatus200JSONResponse(st), nil
}

func (a *apiService) PutAgeRecipient(ctx context.Context, request api.PutAgeRecipientRequestObject) (api.PutAgeRecipientResponseObject, error) {
	body := request.Body
	if body == nil || len(body.Recipient) < 20 {
		return api.PutAgeRecipient400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "recipient is required", "recipient 为必填项")}, nil
	}
	rcp := strings.TrimSpace(body.Recipient)
	// Validate by parsing and confirming it is an X25519 age recipient.
	if _, err := age.ParseX25519Recipient(rcp); err != nil {
		return api.PutAgeRecipient400JSONResponse{Code: "invalid_recipient",
			Message: i18n.T(ctx, "not a valid age recipient (expected age1…)", "不是有效的 age recipient（应为 age1…）")}, nil
	}
	if err := a.srv.storeRecipient(ctx, rcp); err != nil {
		a.srv.log.Error("store recipient", "err", err)
		return api.PutAgeRecipient500JSONResponse{}, nil
	}
	a.srv.log.Info("age recipient configured", "key_id", agekey.Fingerprint(rcp))
	st, err := a.ageStatus(ctx)
	if err != nil {
		return api.PutAgeRecipient500JSONResponse{}, nil
	}
	return api.PutAgeRecipient200JSONResponse(st), nil
}

// ---- databases ----

func (a *apiService) ListDatabases(ctx context.Context, _ api.ListDatabasesRequestObject) (api.ListDatabasesResponseObject, error) {
	dbs, err := jobs.ListDatabases(a.srv.store.DB)
	if err != nil {
		a.srv.log.Error("list databases", "err", err)
		return api.ListDatabases500JSONResponse{}, nil
	}
	out := make([]api.Database, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, a.dbToAPI(d, i18n.FromContext(ctx)))
	}
	return api.ListDatabases200JSONResponse{Databases: out}, nil
}

func (a *apiService) CreateDatabase(ctx context.Context, request api.CreateDatabaseRequestObject) (api.CreateDatabaseResponseObject, error) {
	body := request.Body
	if body == nil || strings.TrimSpace(body.Name) == "" || body.ConnectionUri == "" {
		return api.CreateDatabase400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "name and connectionUri are required", "name 和 connectionUri 为必填项")}, nil
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > 100 {
		return api.CreateDatabase400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "name must be at most 100 characters", "name 不能超过 100 个字符")}, nil
	}
	platform := "generic"
	if body.Platform != nil {
		platform = string(*body.Platform)
	}

	ci, err := pgclient.ParseURI(body.ConnectionUri)
	if err != nil {
		// Deliberately FIXED-TEXT: url.Error and friends embed the full URI
		// with credentials — never echo parse failures back (round-1 P1-02).
		return api.CreateDatabase400JSONResponse{Code: "invalid_request",
			Message: i18n.T(ctx, "connectionUri is not a valid, supported postgres:// URI (", "connectionUri 不是有效且受支持的 postgres:// URI（") + pgURIMsg(ctx, err) + i18n.T(ctx, ")", "）")}, nil
	}
	test, err := pgclient.Test(ctx, ci)
	if err != nil {
		// FIXED TEXT ONLY: connection errors can quote connection parameters
		// (host/db/user and even the password in libpq messages); echoing
		// them back leaks credentials no matter how thorough the redaction
		// (round-8 review — the last unfiltered API exit for the secret).
		// The full classified error goes to the server log, redacted.
		// ONE redacted error field only — the raw err field leaked the
		// credential in combination-escaped forms (round-9 review R9-P1-01).
		// Order matters (phase-8 review P1-01): whole-secret removal FIRST,
		// syntax-aware scrubbing second — the reverse order truncated the
		// secret and broke the whole-secret matcher downstream.
		a.srv.log.Error("connection test failed", "name", name,
			"keyword_view", ci.KeywordView(),
			"err", pgclient.SanitizeMessage(redact.Secrets([]string{ci.Password}, err.Error())))
		return api.CreateDatabase422JSONResponse{Code: "connection_test_failed",
			Message: i18n.T(ctx, "connection test failed — verify host, port, credentials and TLS mode", "连接测试失败——请检查主机、端口、凭据和 TLS 模式")}, nil
	}

	enc, err := crypto.Encrypt(a.srv.key, []byte(body.ConnectionUri))
	if err != nil {
		a.srv.log.Error("encrypt credentials", "err", err)
		return api.CreateDatabase500JSONResponse{}, nil
	}

	var id int64
	err = a.srv.store.DB.QueryRowContext(ctx,
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, server_version, sslmode, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%s','now'), strftime('%s','now'))
		 RETURNING id`,
		name, platform, envTagOrEmpty(body.EnvTag), enc, test.ServerVersion, ci.SSLMode).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return api.CreateDatabase409JSONResponse{Code: "name_exists", Message: i18n.T(ctx, "a database with this name already exists", "同名数据库已存在")}, nil
		}
		a.srv.log.Error("create database", "err", err)
		return api.CreateDatabase500JSONResponse{}, nil
	}
	a.srv.log.Info("database registered", "id", id, "name", name,
		"host", ci.Host, "dbname", ci.DBName, "sslmode", ci.SSLMode, "server", test.ServerVersion)

	d, err := jobs.GetDatabase(a.srv.store.DB, id)
	if err != nil {
		return api.CreateDatabase500JSONResponse{}, nil
	}
	// Surface the pooled-endpoint warning at registration time (P2-01): the
	// wizard moment is where the user can still pick the direct endpoint.
	lang := i18n.FromContext(ctx)
	out := a.dbToAPI(d, lang)
	if hint := platformpkg.PoolingHint(ci.Host, ci.Port); !hint.Empty() {
		text := hint.T(lang)
		out.PoolingWarning = &text
		a.srv.log.Warn("pooled endpoint registered", "id", id, "hint", hint.En)
	}
	return api.CreateDatabase201JSONResponse(out), nil
}

func (a *apiService) GetDatabase(ctx context.Context, request api.GetDatabaseRequestObject) (api.GetDatabaseResponseObject, error) {
	d, err := jobs.GetDatabase(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.GetDatabase404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
	}
	if err != nil {
		a.srv.log.Error("get database", "err", err)
		return api.GetDatabase500JSONResponse{}, nil
	}
	return api.GetDatabase200JSONResponse(a.dbToAPI(d, i18n.FromContext(ctx))), nil
}

func (a *apiService) DeleteDatabase(ctx context.Context, request api.DeleteDatabaseRequestObject) (api.DeleteDatabaseResponseObject, error) {
	// Soft delete in ONE atomic statement: the NOT EXISTS guard closes the
	// check-then-delete TOCTOU with Enqueue (round-1 review P1-06), and the
	// registration row (plus job history) survives for audit.
	res, err := a.srv.store.DB.ExecContext(ctx, `
		UPDATE databases SET
		  name = name || ' (deleted #' || id || '-' || lower(hex(randomblob(6))) || ')',
		  deleted_at = strftime('%s','now'), updated_at = strftime('%s','now')
		WHERE id = ? AND deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM jobs
		                  WHERE database_id = databases.id AND status IN ('pending','running'))`,
		request.Id)
	if err != nil {
		return api.DeleteDatabase500JSONResponse{}, nil
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "unknown" from "active job" for a precise status code.
		var n int
		if err := a.srv.store.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM databases WHERE id = ? AND deleted_at IS NULL`, request.Id).Scan(&n); err == nil && n == 1 {
			return api.DeleteDatabase409JSONResponse{Code: "job_active",
				Message: i18n.T(ctx, "a job is pending or running for this database", "该数据库已有任务在排队或运行中")}, nil
		}
		return api.DeleteDatabase404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
	}
	return api.DeleteDatabase204Response{}, nil
}

// ---- backups / tasks ----

func (a *apiService) TriggerBackup(ctx context.Context, request api.TriggerBackupRequestObject) (api.TriggerBackupResponseObject, error) {
	if _, err := jobs.GetDatabase(a.srv.store.DB, request.Id); errors.Is(err, jobs.ErrNotFound) {
		return api.TriggerBackup404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "database not found", "数据库不存在")}, nil
	}
	jobID, err := a.srv.runner.Enqueue(ctx, request.Id)
	if errors.Is(err, jobs.ErrAlreadyQueued) {
		return api.TriggerBackup409JSONResponse{Code: "already_queued",
			Message: i18n.T(ctx, "a job is already pending or running for this database", "该数据库已有任务在排队或运行中")}, nil
	}
	if err != nil {
		a.srv.log.Error("enqueue", "err", err)
		return api.TriggerBackup500JSONResponse{}, nil
	}
	t, err := jobs.GetTask(a.srv.store.DB, jobID)
	if err != nil {
		return api.TriggerBackup500JSONResponse{}, nil
	}
	return api.TriggerBackup202JSONResponse(taskToAPI(t, i18n.FromContext(ctx))), nil
}

func (a *apiService) ListTasks(ctx context.Context, _ api.ListTasksRequestObject) (api.ListTasksResponseObject, error) {
	tasks, err := jobs.ListTasks(a.srv.store.DB, 0, 50)
	if err != nil {
		a.srv.log.Error("list tasks", "err", err)
		return api.ListTasks500JSONResponse{}, nil
	}
	out := make([]api.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskToAPI(t, i18n.FromContext(ctx)))
	}
	return api.ListTasks200JSONResponse{Tasks: out}, nil
}

func (a *apiService) GetTask(ctx context.Context, request api.GetTaskRequestObject) (api.GetTaskResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.GetTask404JSONResponse{Code: "not_found", Message: i18n.T(ctx, "task not found", "任务不存在")}, nil
	}
	if err != nil {
		return api.GetTask500JSONResponse{}, nil
	}
	return api.GetTask200JSONResponse(taskToAPI(t, i18n.FromContext(ctx))), nil
}

func (a *apiService) CancelTask(ctx context.Context, request api.CancelTaskRequestObject) (api.CancelTaskResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		// The contract declares 202/409/500; unknown ids are not cancelable.
		return api.CancelTask409JSONResponse{Code: "not_found", Message: i18n.T(ctx, "task not found", "任务不存在")}, nil
	}
	if err != nil {
		return api.CancelTask500JSONResponse{}, nil
	}
	_ = t
	if err := a.srv.runner.Cancel(request.Id); err != nil {
		return api.CancelTask409JSONResponse{Code: "not_cancelable", Message: err.Error()}, nil
	}
	nt, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if err != nil {
		return api.CancelTask500JSONResponse{}, nil
	}
	return api.CancelTask202JSONResponse(taskToAPI(nt, i18n.FromContext(ctx))), nil
}

// ---- mapping helpers ----

func (a *apiService) dbToAPI(d *jobs.Database, lang i18n.Lang) api.Database {
	out := api.Database{
		Id:            d.ID,
		Name:          d.Name,
		Platform:      api.DatabasePlatform(d.Platform),
		EnvTag:        d.EnvTag,
		ServerVersion: d.ServerVersion,
		SslMode:       d.SSLMode,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
	if d.LastTask != nil {
		t := taskToAPI(d.LastTask, lang)
		out.LastTask = &t
	}
	return out
}

// ranVerification reports whether the job reached a verifier outcome, so a
// legitimate zero (tables) is not omitted from the response (round-2 P2-02:
// verified-with-zero-tables must serialize its 0).
func ranVerification(status string) bool {
	return status == "verified" || status == "failed" || status == "unsupported"
}

func taskToAPI(t *jobs.Task, lang i18n.Lang) api.Task {
	out := api.Task{
		Id:         t.ID,
		DatabaseId: t.DatabaseID,
		Status:     api.TaskStatus(t.Status),
		Attempt:    int(t.Attempt),
	}
	if t.ErrorClass != "" {
		ec := api.TaskErrorClass(t.ErrorClass)
		out.ErrorClass = &ec
		if t.Status == "failed" {
			rem := jobs.RemediationMsg(t.ErrorClass).T(lang)
			out.Remediation = &rem
		}
	}
	if t.ErrorMessage != "" {
		out.ErrorMessage = &t.ErrorMessage
	}
	if t.ArtifactSHA256 != "" {
		out.ArtifactSha256 = &t.ArtifactSHA256
	}
	if t.ArtifactSize > 0 {
		out.ArtifactSize = &t.ArtifactSize
	}
	hm := t.HasManifest
	out.HasManifest = &hm
	cr := t.CancelRequested
	out.CancelRequested = &cr
	out.ScheduledAt = &t.ScheduledAt
	if t.StartedAt != nil {
		out.StartedAt = t.StartedAt
	}
	if t.FinishedAt != nil {
		out.FinishedAt = t.FinishedAt
	}
	// Phase 5/6: verification state machine and kit availability — the API
	// reads the authoritative jobs row, never the stats snapshot (P2-02).
	if t.Platform != "" {
		p := api.TaskPlatform(t.Platform)
		out.Platform = &p
	}
	if t.VerifyStatus != "" {
		vs := api.TaskVerifyStatus(t.VerifyStatus)
		out.VerifyStatus = &vs
	}
	if t.VerifyDetail != "" {
		vd := t.VerifyDetail
		out.VerifyDetail = &vd
	}
	if t.VerifyTables > 0 || ranVerification(t.VerifyStatus) {
		vt := t.VerifyTables
		out.VerifyTables = &vt
	}
	if t.VerifyDurationSecs > 0 {
		vdur := t.VerifyDurationSecs
		out.VerifyDurationSecs = &vdur
	}
	if t.VerifyProfile != "" {
		vp := t.VerifyProfile
		out.VerifyProfile = &vp
	}
	hrk := t.HasRecoveryKit
	out.HasRecoveryKit = &hrk
	if t.RemoteState != "" {
		rs := api.TaskRemoteState(t.RemoteState)
		out.RemoteState = &rs
	}
	return out
}

// sanitizeForStore keeps failure messages credential-free end to end.
func envTagOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}
