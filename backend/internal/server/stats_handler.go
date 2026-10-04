package server

import (
	"fmt"
	"net/http"
	"time"
)

// handleStats serves the aggregate statistics endpoint (Phase 8).
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var total, succeeded, failed, canceled int64
	var avgDur float64
	var totalArtifact int64
	err := s.store.DB.QueryRowContext(r.Context(), `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 END), 0),
		       COALESCE(AVG(CASE WHEN status = 'succeeded' THEN duration_secs END), 0),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN artifact_size END), 0)
		FROM jobs WHERE status NOT IN ('pending','running')`).
		Scan(&total, &succeeded, &failed, &canceled, &avgDur, &totalArtifact)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	successRate := 0.0
	if total > 0 {
		successRate = float64(succeeded) / float64(total) * 100
	}

	var lastSuccess int64
	s.store.DB.QueryRowContext(r.Context(),
		`SELECT COALESCE(MAX(finished_at),0) FROM jobs WHERE status = 'succeeded'`).Scan(&lastSuccess)

	var dbCount, destCount int
	s.store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM databases WHERE deleted_at IS NULL`).Scan(&dbCount)
	s.store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM destinations WHERE deleted_at IS NULL`).Scan(&destCount)

	uptime := int64(time.Since(s.started).Seconds())

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"totalJobs":%d,"succeeded":%d,"failed":%d,"canceled":%d,`+
		`"successRate":%.1f,"avgDurationSecs":%.1f,"totalArtifactBytes":%d,`+
		`"lastSuccessAt":%d,"databases":%d,"destinations":%d,"uptimeSeconds":%d}`,
		total, succeeded, failed, canceled, successRate, avgDur, totalArtifact,
		lastSuccess, dbCount, destCount, uptime)
}
