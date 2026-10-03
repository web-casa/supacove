package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/jobs"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	"github.com/cloudfan/supabackup/backend/internal/redact"
)

// Phase 3 API surface: destinations (BYOS), reconciliation and downloads.
// All authenticated by the default-deny guard.

// fullDestToAPI maps a decrypted destination to its API-safe view.
func fullDestToAPI(d *jobs.Destination) api.Destination {
	v := d.View()
	return destViewToAPI(v)
}

func destViewToAPI(d *jobs.DestinationView) api.Destination {
	endpoint := d.Endpoint
	region := d.Region
	prefix := d.Prefix
	return api.Destination{
		Id:             d.ID,
		Name:           d.Name,
		Platform:       api.DestinationPlatform(d.Platform),
		Endpoint:       &endpoint,
		Region:         &region,
		Bucket:         d.Bucket,
		Prefix:         &prefix,
		VerifyReadback: &d.VerifyReadback,
		KeepRemote:     d.KeepRemote,
		KeepDays:       d.KeepDays,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

func (a *apiService) ListDestinations(ctx context.Context, _ api.ListDestinationsRequestObject) (api.ListDestinationsResponseObject, error) {
	dests, err := a.srv.runner.ListDestinations(ctx)
	if err != nil {
		a.srv.log.Error("list destinations", "err", err)
		return api.ListDestinations500JSONResponse{}, nil
	}
	out := make([]api.Destination, 0, len(dests))
	for _, d := range dests {
		out = append(out, destViewToAPI(d))
	}
	return api.ListDestinations200JSONResponse{Destinations: out}, nil
}

func (a *apiService) CreateDestination(ctx context.Context, request api.CreateDestinationRequestObject) (api.CreateDestinationResponseObject, error) {
	body := request.Body
	if body == nil {
		return api.CreateDestination400JSONResponse{Code: "invalid_request", Message: "request body required"}, nil
	}
	platform := "s3"
	if body.Platform != nil {
		platform = string(*body.Platform)
	}
	in := jobs.Destination{
		Name:           body.Name,
		Platform:       platform,
		Endpoint:       derefString(body.Endpoint),
		Region:         derefString(body.Region),
		Bucket:         body.Bucket,
		Prefix:         derefString(body.Prefix),
		AccessKey:      body.AccessKey,
		SecretKey:      body.SecretKey,
		VerifyReadback: derefBool(body.VerifyReadback),
		KeepRemote:     derefInt(body.KeepRemote, 10),
		KeepDays:       derefInt(body.KeepDays, 0),
	}

	id, err := a.srv.runner.CreateDestination(ctx, in, true)
	switch {
	case err == nil:
	case errors.Is(err, jobs.ErrDestinationNameExists):
		return api.CreateDestination409JSONResponse{Code: "name_exists", Message: "a destination with this name already exists"}, nil
	default:
		// Live-test or validation failure. The error text may contain the
		// endpoint (not secret) — but the secret key is redacted from any
		// wrapped provider error before returning.
		msg := redact.Secrets([]string{in.SecretKey, in.AccessKey}, err.Error())
		if strings.Contains(msg, "diagnostic test failed") {
			return api.CreateDestination422JSONResponse{Code: "diagnostic_test_failed",
				Message: msg}, nil
		}
		return api.CreateDestination400JSONResponse{Code: "invalid_request", Message: msg}, nil
	}

	dest, err := a.srv.runner.GetDestination(ctx, id)
	if err != nil {
		return api.CreateDestination500JSONResponse{}, nil
	}
	a.srv.log.Info("destination created", "id", id, "name", dest.Name,
		"platform", dest.Platform, "bucket", dest.Bucket)
	return api.CreateDestination201JSONResponse(fullDestToAPI(dest)), nil
}

func (a *apiService) DeleteDestination(ctx context.Context, request api.DeleteDestinationRequestObject) (api.DeleteDestinationResponseObject, error) {
	err := a.srv.runner.DeleteDestination(ctx, request.Id)
	switch {
	case err == nil:
		return api.DeleteDestination204Response{}, nil
	case errors.Is(err, jobs.ErrDestinationNotFound):
		return api.DeleteDestination404JSONResponse{Code: "not_found", Message: "destination not found"}, nil
	default:
		return api.DeleteDestination409JSONResponse{Code: "upload_in_flight", Message: err.Error()}, nil
	}
}

func (a *apiService) TestDestination(ctx context.Context, request api.TestDestinationRequestObject) (api.TestDestinationResponseObject, error) {
	dest, err := a.srv.runner.GetDestination(ctx, request.Id)
	if errors.Is(err, jobs.ErrDestinationNotFound) {
		return api.TestDestination404JSONResponse{Code: "not_found", Message: "destination not found"}, nil
	}
	if err != nil {
		return api.TestDestination500JSONResponse{}, nil
	}
	diag, ok := a.srv.runner.DiagnosticTester(ctx, dest)
	if !ok {
		return api.TestDestination500JSONResponse{}, nil
	}
	if err := diag.DiagnosticTest(ctx); err != nil {
		return api.TestDestination422JSONResponse{Code: "diagnostic_test_failed",
			Message: redact.Secrets(dest.Secrets(), err.Error())}, nil
	}
	return api.TestDestination200JSONResponse{Status: api.Ok}, nil
}

func (a *apiService) ReconcileDestination(ctx context.Context, request api.ReconcileDestinationRequestObject) (api.ReconcileDestinationResponseObject, error) {
	report, err := a.srv.runner.Reconcile(ctx, request.Id)
	if errors.Is(err, jobs.ErrDestinationNotFound) {
		return api.ReconcileDestination404JSONResponse{Code: "not_found", Message: "destination not found"}, nil
	}
	if err != nil {
		a.srv.log.Error("reconcile", "err", err)
		return api.ReconcileDestination500JSONResponse{}, nil
	}
	return api.ReconcileDestination200JSONResponse{
		DestinationId: report.DestinationID,
		GeneratedAt:   report.GeneratedAt.Unix(),
		RemoteObjects: report.RemoteObjects,
		Matched:       report.Matched,
		Orphaned:      &report.Orphaned,
		Missing:       &report.Missing,
		Uncommitted:   &report.Uncommitted,
	}, nil
}

func (a *apiService) AssignDatabaseDestination(ctx context.Context, request api.AssignDatabaseDestinationRequestObject) (api.AssignDatabaseDestinationResponseObject, error) {
	body := request.Body
	var destID int64
	if body != nil && body.DestinationId != nil {
		destID = *body.DestinationId
	}
	if err := a.srv.runner.AssignDestination(ctx, request.Id, destID); err != nil {
		if strings.Contains(err.Error(), "not found or a job is active") {
			return api.AssignDatabaseDestination409JSONResponse{Code: "not_assignable", Message: err.Error()}, nil
		}
		if errors.Is(err, jobs.ErrDestinationNotFound) {
			return api.AssignDatabaseDestination409JSONResponse{Code: "not_assignable", Message: "destination not found"}, nil
		}
		a.srv.log.Error("assign destination", "err", err)
		return api.AssignDatabaseDestination500JSONResponse{}, nil
	}
	return api.AssignDatabaseDestination200JSONResponse{Status: api.Ok}, nil
}

// GetTaskDownloadURL returns a short-lived presigned GET URL for a
// remotely-committed backup. The URL is a bearer secret: it is returned to
// the authenticated caller and never logged.
func (a *apiService) GetTaskDownloadURL(ctx context.Context, request api.GetTaskDownloadURLRequestObject) (api.GetTaskDownloadURLResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.GetTaskDownloadURL404JSONResponse{Code: "not_found", Message: "task not found"}, nil
	}
	if err != nil {
		return api.GetTaskDownloadURL500JSONResponse{}, nil
	}
	if t.RemoteState != "committed" || t.RemoteObjectKey == "" {
		return api.GetTaskDownloadURL409JSONResponse{Code: "not_remotely_committed",
			Message: "this backup is not committed to a remote destination"}, nil
	}
	backend, err := a.srv.runner.BuildBackendByID(ctx, t.DestinationID)
	if err != nil {
		a.srv.log.Error("download backend", "err", err)
		return api.GetTaskDownloadURL500JSONResponse{}, nil
	}
	const ttl = 15 * time.Minute
	url, err := backend.Presign(ctx, t.RemoteObjectKey, ttl)
	if err != nil {
		a.srv.log.Error("presign failed", "err", err)
		return api.GetTaskDownloadURL500JSONResponse{}, nil
	}
	return api.GetTaskDownloadURL200JSONResponse{
		Url:       url,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}, nil
}

// DownloadTask streams a LOCAL staged artifact for local-only backups
// (remote-committed backups use presigned URLs). The path must resolve
// under the staging directory.
func (a *apiService) DownloadTask(ctx context.Context, request api.DownloadTaskRequestObject) (api.DownloadTaskResponseObject, error) {
	t, err := jobs.GetTask(a.srv.store.DB, request.Id)
	if errors.Is(err, jobs.ErrNotFound) {
		return api.DownloadTask404JSONResponse{Code: "not_found", Message: "task not found"}, nil
	}
	if err != nil {
		return api.DownloadTask500JSONResponse{}, nil
	}
	if t.Status != "succeeded" {
		return api.DownloadTask404JSONResponse{Code: "not_available", Message: "no local artifact for this task"}, nil
	}
	artifactPath, err := a.srv.runner.TaskArtifactPath(ctx, request.Id)
	if err != nil {
		return api.DownloadTask500JSONResponse{}, nil
	}
	if artifactPath == "" {
		return api.DownloadTask404JSONResponse{Code: "not_available",
			Message: "no local artifact for this task"}, nil
	}
	stagingAbs, err := filepath.Abs(a.srv.stagingDir)
	if err != nil {
		return api.DownloadTask500JSONResponse{}, nil
	}
	artifactAbs, err := filepath.Abs(artifactPath)
	if err != nil {
		return api.DownloadTask500JSONResponse{}, nil
	}
	if !strings.HasPrefix(artifactAbs, stagingAbs+string(os.PathSeparator)) {
		a.srv.log.Error("download path escaped staging", "job", request.Id)
		return api.DownloadTask500JSONResponse{}, nil
	}
	f, err := os.Open(artifactAbs)
	if err != nil {
		return api.DownloadTask404JSONResponse{Code: "not_available", Message: "artifact file no longer present"}, nil
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return api.DownloadTask404JSONResponse{Code: "not_available", Message: "artifact file no longer present"}, nil
	}
	return api.DownloadTask200ApplicationoctetStreamResponse{
		Body:          f,
		ContentLength: st.Size(),
	}, nil
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func derefBool(v *bool) bool {
	return v != nil && *v
}

func derefInt(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

var _ = pgclient.ClassNetwork
