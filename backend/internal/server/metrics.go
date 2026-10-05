package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/outbox"
)

// handleMetrics serves the Prometheus-format /metrics endpoint.
// Authenticated by the default-deny guard. Labels are deliberately
// low-cardinality: job/verification STATUS values, database NAMES (bounded
// by the number of registrations an admin creates), never job IDs, hosts or
// object keys (dev-plan Phase 7 task 5).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	uptime := int64(time.Since(s.started).Seconds())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	gauge := func(name, help string, value any) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, value)
	}

	gauge("supabackup_uptime_seconds", "Uptime in seconds.", uptime)
	gauge("supabackup_go_goroutines", "Number of goroutines.", runtime.NumGoroutine())
	gauge("supabackup_heap_alloc_bytes", "Heap allocation in bytes.", ms.HeapAlloc)

	// Job outcomes (Phase 7: 任务数 per status).
	rows, err := s.store.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err == nil {
		type kv struct {
			status string
			n      int64
		}
		var kvs []kv
		for rows.Next() {
			var x kv
			if rows.Scan(&x.status, &x.n) == nil {
				kvs = append(kvs, x)
			}
		}
		rows.Close()
		fmt.Fprintf(w, "# HELP supabackup_jobs_total Backup jobs by terminal/active status.\n")
		fmt.Fprintf(w, "# TYPE supabackup_jobs_total gauge\n")
		for _, x := range kvs {
			fmt.Fprintf(w, "supabackup_jobs_total{status=%q} %d\n", x.status, x.n)
		}
	}

	// Remote commit inputs (protocol C success rate).
	var committed, uploadFailed int64
	_ = s.store.DB.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN remote_state IN ('committed','deleted') THEN 1 END), 0),
		   COALESCE(SUM(CASE WHEN status = 'failed' AND error_class = 'storage_upload' THEN 1 END), 0)
		 FROM jobs`).Scan(&committed, &uploadFailed)
	gauge("supabackup_remote_commits_total", "Backups remotely committed (or deleted after a committed lifetime).", committed)
	gauge("supabackup_remote_upload_failures_total", "Jobs failed in the storage_upload class.", uploadFailed)

	// Verification status distribution over succeeded backups.
	vrows, err := s.store.DB.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(verify_status,''),'none'), COUNT(*)
		FROM jobs WHERE status = 'succeeded' GROUP BY 1`)
	if err == nil {
		type kv struct {
			status string
			n      int64
		}
		var kvs []kv
		for vrows.Next() {
			var x kv
			if vrows.Scan(&x.status, &x.n) == nil {
				kvs = append(kvs, x)
			}
		}
		vrows.Close()
		fmt.Fprintf(w, "# HELP supabackup_verification_total Restore-verification states of succeeded backups.\n")
		fmt.Fprintf(w, "# TYPE supabackup_verification_total gauge\n")
		for _, x := range kvs {
			fmt.Fprintf(w, "supabackup_verification_total{status=%q} %d\n", x.status, x.n)
		}
	}

	// Last successful snapshot per database (what the dead-man switch
	// watches; snapshot instant, not completion).
	lrows, err := s.store.DB.QueryContext(ctx, `
		SELECT d.name, MAX(j.started_at) FROM databases d
		JOIN jobs j ON j.database_id = d.id AND j.status = 'succeeded'
		WHERE d.deleted_at IS NULL
		GROUP BY d.id, d.name`)
	if err == nil {
		type kv struct {
			name string
			at   int64
		}
		var kvs []kv
		for lrows.Next() {
			var x kv
			if lrows.Scan(&x.name, &x.at) == nil {
				kvs = append(kvs, x)
			}
		}
		lrows.Close()
		fmt.Fprintf(w, "# HELP supabackup_last_success_timestamp Last successful backup snapshot per database (unix seconds).\n")
		fmt.Fprintf(w, "# TYPE supabackup_last_success_timestamp gauge\n")
		for _, x := range kvs {
			fmt.Fprintf(w, "supabackup_last_success_timestamp{database=%q} %d\n", x.name, x.at)
		}
	}

	// Staging disk usage (bounded by retention; best-effort walk).
	gauge("supabackup_staging_bytes", "Bytes currently staged in the local staging directory.", s.stagingBytes())

	// Notification outbox health.
	pending, dead, err := outbox.Counts(ctx, s.store.DB)
	if err == nil {
		gauge("supabackup_outbox_pending", "Notification outbox entries awaiting delivery.", pending)
		gauge("supabackup_outbox_dead", "Notification outbox entries that exhausted retries.", dead)
	}

	// Overview protection states (Phase 7 four-state view).
	orows, err := s.store.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.max_age_hours,
		       (SELECT j.started_at FROM jobs j WHERE j.database_id = d.id AND j.status = 'succeeded' ORDER BY j.id DESC LIMIT 1)
		FROM databases d WHERE d.deleted_at IS NULL`)
	if err == nil {
		counts := map[string]int64{}
		now := time.Now()
		for orows.Next() {
			var id, maxAge int64
			var name string
			var started *int64
			if orows.Scan(&id, &name, &maxAge, &started) != nil {
				continue
			}
			state := "never"
			if started != nil && *started > 0 {
				if maxAge > 0 && now.Sub(time.Unix(*started, 0)) > time.Duration(maxAge)*time.Hour {
					state = "expired"
				} else {
					state = "fresh"
				}
			}
			counts[state]++
		}
		orows.Close()
		fmt.Fprintf(w, "# HELP supabackup_databases_protection Databases by protection state (fresh/expired/never).\n")
		fmt.Fprintf(w, "# TYPE supabackup_databases_protection gauge\n")
		for _, state := range []string{"fresh", "expired", "never"} {
			fmt.Fprintf(w, "supabackup_databases_protection{state=%q} %d\n", state, counts[state])
		}
	}
}

// stagingBytes walks the staging directory summing regular file sizes.
func (s *Server) stagingBytes() int64 {
	if s.stagingDir == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(s.stagingDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries/dirs; best-effort metric
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
