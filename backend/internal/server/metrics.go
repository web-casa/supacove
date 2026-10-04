package server

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// handleMetrics serves a minimal Prometheus-format /metrics endpoint.
// Labels contain no task/backup/table IDs or free-form error text
// (dev-plan Phase 4 task 5).
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
}
