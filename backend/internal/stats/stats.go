// Package stats implements Phase 8 statistics persistence and querying:
// per-job backup metrics recorded after each completed job, queryable via
// the API for dashboard display and capacity planning (dev-plan Phase 8
// task 1: "three volume metrics + segmented success rate").
package stats

import (
	"context"
	"database/sql"
)

// Recorder persists backup statistics.
type Recorder struct {
	db *sql.DB
}

// New creates a stats Recorder.
func New(db *sql.DB) *Recorder { return &Recorder{db: db} }

// Record persists one backup's metrics. Called from runJob after success.
func (r *Recorder) Record(ctx context.Context, entry Entry) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO backup_stats (job_id, database_name, dump_size, artifact_size,
		    duration_secs, verify_status, verify_tables, remote_committed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, strftime('%s','now'))`,
		entry.JobID, entry.DatabaseName, entry.DumpSize, entry.ArtifactSize,
		entry.DurationSecs, entry.VerifyStatus, entry.VerifyTables,
		btoi(entry.RemoteCommitted))
	// Volume metric 1 lives on the authoritative jobs row (the stats table is
	// a dashboard snapshot; the overview reads jobs.source_db_bytes directly).
	return err
}

// Entry is one backup's recorded metrics.
type Entry struct {
	JobID int64
	// SourceDBBytes is volume metric 1 (dev-plan §0): the source database's
	// physical size as reported by the server at dump time. 0 = unknown.
	SourceDBBytes   int64
	DatabaseName    string
	DumpSize        int64 // volume metric 2 (pg_dump archive, compressed)
	ArtifactSize    int64 // volume metric 3 (age ciphertext)
	DurationSecs    float64
	VerifyStatus    string
	VerifyTables    int64
	RemoteCommitted bool
}

// Summary is the aggregate response for GET /api/stats.
type Summary struct {
	TotalJobs       int64             `json:"totalJobs"`
	Succeeded       int64             `json:"succeeded"`
	Failed          int64             `json:"failed"`
	Canceled        int64             `json:"canceled"`
	SuccessRate     float64           `json:"successRate"`
	AvgDurationSecs float64           `json:"avgDurationSecs"`
	TotalArtifact   int64             `json:"totalArtifactBytes"`
	Databases       []DatabaseSummary `json:"databases"`
	Recent          []RecentEntry     `json:"recent"`
}

// DatabaseSummary aggregates stats for one database.
type DatabaseSummary struct {
	Name          string  `json:"name"`
	TotalBackups  int64   `json:"totalBackups"`
	LastSuccessAt int64   `json:"lastSuccessAt"`
	AvgDuration   float64 `json:"avgDurationSecs"`
	TotalArtifact int64   `json:"totalArtifactBytes"`
}

// RecentEntry is one recent backup for the dashboard.
type RecentEntry struct {
	JobID        int64   `json:"jobId"`
	DatabaseName string  `json:"databaseName"`
	Status       string  `json:"status"`
	ArtifactSize int64   `json:"artifactSize"`
	Duration     float64 `json:"durationSecs"`
	FinishedAt   int64   `json:"finishedAt"`
}

// Summary computes aggregate statistics for the dashboard.
func (r *Recorder) Summary(ctx context.Context) (*Summary, error) {
	s := &Summary{}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 END), 0),
		       COALESCE(AVG(CASE WHEN status = 'succeeded' THEN duration_secs END), 0),
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN artifact_size END), 0)
		FROM jobs WHERE status != 'pending' AND status != 'running'`).
		Scan(&s.TotalJobs, &s.Succeeded, &s.Failed, &s.Canceled,
			&s.AvgDurationSecs, &s.TotalArtifact); err != nil {
		return nil, err
	}
	if s.TotalJobs > 0 {
		s.SuccessRate = float64(s.Succeeded) / float64(s.TotalJobs) * 100
	}

	dbRows, err := r.db.QueryContext(ctx, `
		SELECT d.name, COUNT(j.id),
		       COALESCE(MAX(j.finished_at), 0),
		       COALESCE(AVG(j.duration_secs), 0),
		       COALESCE(SUM(j.artifact_size), 0)
		FROM databases d
		JOIN jobs j ON j.database_id = d.id AND j.status = 'succeeded'
		WHERE d.deleted_at IS NULL
		GROUP BY d.id, d.name ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()
	for dbRows.Next() {
		var ds DatabaseSummary
		if err := dbRows.Scan(&ds.Name, &ds.TotalBackups, &ds.LastSuccessAt,
			&ds.AvgDuration, &ds.TotalArtifact); err != nil {
			return nil, err
		}
		s.Databases = append(s.Databases, ds)
	}
	if err := dbRows.Err(); err != nil {
		return nil, err
	}

	recentRows, err := r.db.QueryContext(ctx, `
		SELECT j.id, d.name, j.status, COALESCE(j.artifact_size, 0),
		       COALESCE(j.duration_secs, 0), COALESCE(j.finished_at, 0)
		FROM jobs j JOIN databases d ON d.id = j.database_id
		WHERE j.status IN ('succeeded','failed')
		ORDER BY j.id DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var re RecentEntry
		if err := recentRows.Scan(&re.JobID, &re.DatabaseName, &re.Status,
			&re.ArtifactSize, &re.Duration, &re.FinishedAt); err != nil {
			return nil, err
		}
		s.Recent = append(s.Recent, re)
	}
	return s, recentRows.Err()
}

func btoi(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
