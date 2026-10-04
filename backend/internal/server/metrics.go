package server

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// handleMetrics serves a minimal Prometheus-format /metrics endpoint.
// Authenticated by the default-deny guard; labels contain no
// high-cardinality values (dev-plan Phase 4 task 5).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	uptime := int64(time.Since(s.started).Seconds())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP supabackup_uptime_seconds Uptime in seconds.\n")
	fmt.Fprintf(w, "# TYPE supabackup_uptime_seconds gauge\n")
	fmt.Fprintf(w, "supabackup_uptime_seconds %d\n", uptime)

	fmt.Fprintf(w, "# HELP supabackup_go_goroutines Number of goroutines.\n")
	fmt.Fprintf(w, "# TYPE supabackup_go_goroutines gauge\n")
	fmt.Fprintf(w, "supabackup_go_goroutines %d\n", runtime.NumGoroutine())

	fmt.Fprintf(w, "# HELP supabackup_heap_alloc_bytes Heap allocation in bytes.\n")
	fmt.Fprintf(w, "# TYPE supabackup_heap_alloc_bytes gauge\n")
	fmt.Fprintf(w, "supabackup_heap_alloc_bytes %d\n", ms.HeapAlloc)

	var n int
	s.store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jobs WHERE status = 'succeeded'`).Scan(&n)
	fmt.Fprintf(w, "# HELP supabackup_jobs_succeeded Total succeeded backup jobs.\n")
	fmt.Fprintf(w, "# TYPE supabackup_jobs_succeeded gauge\n")
	fmt.Fprintf(w, "supabackup_jobs_succeeded %d\n", n)

	s.store.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jobs WHERE status = 'failed'`).Scan(&n)
	fmt.Fprintf(w, "# HELP supabackup_jobs_failed Total failed backup jobs.\n")
	fmt.Fprintf(w, "# TYPE supabackup_jobs_failed gauge\n")
	fmt.Fprintf(w, "supabackup_jobs_failed %d\n", n)
}
