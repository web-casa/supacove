package server

import (
	"context"
	"errors"
	"strings"

	"filippo.io/age"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/jobs"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

// Phase 2 API surface: age status/recipient, database registrations,
// backup triggers and task inspection. All of these sit behind the
// authenticated guard (default-deny).

// ---- age (protocol B) ----

func (a *apiService) ageStatus(ctx context.Context) api.AgeStatus {
	recipient, _ := a.srv.recipientFor(ctx)
	if recipient == "" {
		return api.AgeStatus{Configured: false}
	}
	fingerprint := agekey.Fingerprint(recipient)
	return api.AgeStatus{
		Configured: true,
		Recipient:  &recipient,
		KeyId:      &fingerprint,
	}
}

func (a *apiService) GetAgeStatus(ctx context.Context, _ api.GetAgeStatusRequestObject) (api.GetAgeStatusResponseObject, error) {
	return api.GetAgeStatus200JSONResponse(a.ageStatus(ctx)), nil
}

func (a *apiService) PutAgeRecipient(ctx context.Context, request api.PutAgeRecipientRequestObject) (api.PutAgeRecipientResponseObject, error) {
	body := request.Body
	if body == nil || len(body.Recipient) < 20 {
		return api.PutAgeRecipient400JSONResponse{Code: "invalid_request", Message: "recipient is required"}, nil
	}
	rcp := strings.TrimSpace(body.Recipient)
	// Validate by parsing and confirming it is an X25519 age recipient.
	if _, err := age.ParseX25519Recipient(rcp); err != nil {
		return api.PutAgeRecipient400JSONResponse{Code: "invalid_recipient",
			Message: "not a valid age recipient (expected age1…)"}, nil
	}
	if err := a.srv.storeRecipient(ctx, rcp); err != nil {
		a.srv.log.Error("store recipient", "err", err)
		return api.PutAgeRecipient500JSONResponse{}, nil
	}
	a.srv.log.Info("age recipient configured", "key_id", agekey.Fingerprint(rcp))
	return api.PutAgeRecipient200JSONResponse(a.ageStatus(ctx)), nil
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
		out = append(out, a.dbToAPI(d))
	}
	return api.ListDatabases200JSONResponse{Databases: out}, nil
}

func (a *apiService) CreateDatabase(ctx context.Context, request api.CreateDatabaseRequestObject) (api.CreateDatabaseResponseObject, error) {
	body := request.Body
	if body == nil || strings.TrimSpace(body.Name) == "" || body.ConnectionUri == "" {
		return api.CreateDatabase400JSONResponse{Code: "invalid_request", Message: "name and connectionUri are required"}, nil
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > 100 {
		return api.CreateDatabase400JSONResponse{Code: "invalid_request", Message: "name must be at most 100 characters"}, nil
	}
	platform := "generic"
	if body.Platform != nil {
		platform = string(*body.Platform)
	}

	ci, err := pgclient.ParseURI(body.ConnectionUri)
	if err != nil {
		return api.CreateDatabase400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
	}
	test, err := pgclient.Test(ctx, ci)
	if err != nil {
		return api.CreateDatabase422JSONResponse{Code: "connection_test_failed",
			Message: "connection test failed: " + sanitizeForStore(err.Error())}, nil
	}

	enc, err := crypto.Encrypt(a.srv.key, []byte(body.ConnectionUri))
	if err != nil {
		a.srv.log.Error("encrypt credentials", "err", err)
		return api.CreateDatabase500JSONResponse{}, nil
	}

	var id int64
	err = a.srv.store.DB.QueryRowContext(ctx,
		`INSERT INTO databases (name, platform, env_tag, conn_encrypted, server_version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, strftime('%s','now'), strftime('%s','now'))
		 RETURNING id`,
		name, platform, envTagOrEmpty(body.EnvTag), enc, test.ServerVersion).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return api.CreateDatabase409JSONResponse{Code: "name_exists", Message: "a database with this name already exists"}, nil
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
	return api.CreateDatabase201JSONResponse(a.dbToAPI(d)), nil
}

func (a *apiService) GetDatabase(ctx context.Context, request api.GetDatabaseRequestObject) (api.GetDatabaseResponseObject, error) {
	d, err := jobs.GetDatabase(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.GetDatabase404JSONResponse{Code: "not_found", Message: "database not found"}, nil
	}
	if err != nil {
		a.srv.log.Error("get database", "err", err)
		return api.GetDatabase500JSONResponse{}, nil
	}
	return api.GetDatabase200JSONResponse(a.dbToAPI(d)), nil
}

func (a *apiService) DeleteDatabase(ctx context.Context, request api.DeleteDatabaseRequestObject) (api.DeleteDatabaseResponseObject, error) {
	var n int
	if err := a.srv.store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE database_id = ? AND status IN ('pending','running')`,
		request.Id).Scan(&n); err != nil {
		return api.DeleteDatabase500JSONResponse{}, nil
	}
	if n > 0 {
		return api.DeleteDatabase409JSONResponse{Code: "job_active",
			Message: "a job is pending or running for this database"}, nil
	}
	res, err := a.srv.store.DB.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, request.Id)
	if err != nil {
		return api.DeleteDatabase500JSONResponse{}, nil
	}
	if rn, _ := res.RowsAffected(); rn == 0 {
		return api.DeleteDatabase404JSONResponse{Code: "not_found", Message: "database not found"}, nil
	}
	return api.DeleteDatabase204Response{}, nil
}

// ---- backups / tasks ----

func (a *apiService) TriggerBackup(ctx context.Context, request api.TriggerBackupRequestObject) (api.TriggerBackupResponseObject, error) {
	if _, err := jobs.GetDatabase(a.srv.store.DB, request.Id); errors.Is(err, jobs.ErrNotFound) {
		return api.TriggerBackup404JSONResponse{Code: "not_found", Message: "database not found"}, nil
	}
	jobID, err := a.srv.runner.Enqueue(ctx, request.Id)
	if errors.Is(err, jobs.ErrAlreadyQueued) {
		return api.TriggerBackup409JSONResponse{Code: "already_queued",
			Message: "a job is already pending or running for this database"}, nil
	}
	if err != nil {
		a.srv.log.Error("enqueue", "err", err)
		return api.TriggerBackup500JSONResponse{}, nil
	}
	t, err := jobs.GetTask(a.srv.store.DB, jobID)
	if err != nil {
		return api.TriggerBackup500JSONResponse{}, nil
	}
	return api.TriggerBackup202JSONResponse(taskToAPI(t)), nil
}

func (a *apiService) ListTasks(ctx context.Context, _ api.ListTasksRequestObject) (api.ListTasksResponseObject, error) {
	tasks, err := jobs.ListTasks(a.srv.store.DB, 0, 50)
	if err != nil {
		a.srv.log.Error("list tasks", "err", err)
		return api.ListTasks500JSONResponse{}, nil
	}
	out := make([]api.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskToAPI(t))
	}
	return api.ListTasks200JSONResponse{Tasks: out}, nil
}

func (a *apiService) GetTask(ctx context.Context, request api.GetTaskRequestObject) (api.GetTaskResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.GetTask404JSONResponse{Code: "not_found", Message: "task not found"}, nil
	}
	if err != nil {
		return api.GetTask500JSONResponse{}, nil
	}
	return api.GetTask200JSONResponse(taskToAPI(t)), nil
}

func (a *apiService) CancelTask(ctx context.Context, request api.CancelTaskRequestObject) (api.CancelTaskResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		// The contract declares 202/409/500; unknown ids are not cancelable.
		return api.CancelTask409JSONResponse{Code: "not_found", Message: "task not found"}, nil
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
	return api.CancelTask202JSONResponse(taskToAPI(nt)), nil
}

// ---- mapping helpers ----

func (a *apiService) dbToAPI(d *jobs.Database) api.Database {
	out := api.Database{
		Id:            d.ID,
		Name:          d.Name,
		Platform:      api.DatabasePlatform(d.Platform),
		EnvTag:        d.EnvTag,
		ServerVersion: d.ServerVersion,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
	if d.LastTask != nil {
		t := taskToAPI(d.LastTask)
		out.LastTask = &t
	}
	return out
}

func taskToAPI(t *jobs.Task) api.Task {
	out := api.Task{
		Id:         t.ID,
		DatabaseId: t.DatabaseID,
		Status:     api.TaskStatus(t.Status),
		Attempt:    int(t.Attempt),
	}
	if t.ErrorClass != "" {
		ec := api.TaskErrorClass(t.ErrorClass)
		out.ErrorClass = &ec
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
	return out
}

// sanitizeForStore keeps failure messages credential-free end to end.
func envTagOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func sanitizeForStore(msg string) string {
	r := strings.NewReplacer("password=", "password=[REDACTED]")
	return r.Replace(msg)
}
