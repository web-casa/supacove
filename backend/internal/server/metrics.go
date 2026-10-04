package server

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// handleMetrics serves a minimal Prometheus-format /metrics endpoint.
// Labels contain no task/backup/table IDs or free-form error text
// (dev-plan Phase 4 task 5). Authenticated by the default-deny guard.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	uptime := time.Since(s.started)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP supabackup_uptime_seconds Uptime in seconds.\n")
	fmt.Fprintf(w, "# TYPE supabackup_uptime_seconds gauge\n")
	fmt.Fprintf(w, "supabackup_uptime_seconds %d\n", int64(uptime.Seconds()))

	fmt.Fprintf(w, "# HELP supabackup_go_goroutines Number of goroutines.\n")
	fmt.Fprintf(w, "# TYPE supabackup_go_goroutines gauge\n")
	fmt.Fprintf(w, "supabackup_go_goroutines %d\n", runtime.NumGoroutine())

	fmt.Fprintf(w, "# HELP supabackup_heap_alloc_bytes Heap allocation in bytes.\n")
	fmt.Fprintf(w, "# TYPE supabackup_heap_alloc_bytes gauge\n")
	fmt.Fprintf(w, "supabackup_heap_alloc_bytes %d\n", ms.HeapAlloc)

	// Business metrics: backup job counts by status.
	statuses := []string{"succeeded", "failed", "pending", "running", "canceled", "interrupted"}
	for _, status := range statuses {
		var n int
		s.store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = ?`, status).Scan(&n)
		fmt.Fprintf(w, "# HELP supabackup_jobs_total Total backup jobs by status.\n")
		fmt.Fprintf(w, "# TYPE supabackup_jobs_total gauge\n")
		fmt.Fprintf(w, "supabackup_jobs_total{status=%q} %d\n", status, n)
	}

	// Last successful backup age per the newest succeeded job.
	var lastSuccess int64
	s.store.DB.QueryRow(`SELECT COALESCE(MAX(finished_at),0) FROM jobs WHERE status = 'succeeded'`).Scan(&lastSuccess)
	if lastSuccess > 0 {
		age := time.Now().Unix() - lastSuccess
		fmt.Fprintf(w, "# HELP supabackup_last_success_age_seconds Seconds since last successful backup.\n")
		fmt.Fprintf(w, "# TYPE supabackup_last_success_age_seconds gauge\n")
		fmt.Fprintf(w, "supabackup_last_success_age_seconds %d\n", age)
	}

	// Database and destination counts.
	var dbCount, destCount int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM databases WHERE deleted_at IS NULL`).Scan(&dbCount)
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM destinations WHERE deleted_at IS NULL`).Scan(&destCount)
	fmt.Fprintf(w, "# HELP supabackup_databases_active Active database registrations.\n")
	fmt.Fprintf(w, "# TYPE supabackup_databases_active gauge\n")
	fmt.Fprintf(w, "supabackup_databases_active %d\n", dbCount)
	fmt.Fprintf(w, "# HELP supabackup_destinations_active Active storage destinations.\n")
	fmt.Fprintf(w, "# TYPE supabackup_destinations_active gauge\n")
	fmt.Fprintf(w, "supabackup_destinations_active %d\n", destCount)
}
