// Package jobs — data-access helpers shared by the API handlers.
package jobs

import (
	"database/sql"
	"errors"
)

// Task is the API-safe view of a job row.
type Task struct {
	ID              int64  `json:"id"`
	DatabaseID      int64  `json:"databaseId"`
	Status          string `json:"status"`
	Attempt         int64  `json:"attempt"`
	ErrorClass      string `json:"errorClass,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	ArtifactSHA256  string `json:"artifactSha256,omitempty"`
	ArtifactSize    int64  `json:"artifactSize,omitempty"`
	HasManifest     bool   `json:"hasManifest"`
	CancelRequested bool   `json:"cancelRequested"`
	RemoteState     string `json:"remoteState,omitempty"`
	RemoteObjectKey string `json:"-"`
	ArtifactPath    string `json:"-"`
	DestinationID   int64  `json:"-"`
	ScheduledAt     int64  `json:"scheduledAt"`
	StartedAt       *int64 `json:"startedAt,omitempty"`
	FinishedAt      *int64 `json:"finishedAt,omitempty"`
}

var ErrNotFound = errors.New("not found")

// scanTask reads the shared task column order.
func scanTask(scan func(dest ...any) error) (*Task, error) {
	var t Task
	var startedAt, finishedAt sql.NullInt64
	var errMsg, sha, cls string
	var size int64
	var hasManifestPath string
	var destinationID sql.NullInt64
	var remoteState, remoteKey string
	if err := scan(&t.ID, &t.DatabaseID, &t.Status, &t.Attempt, &t.CancelRequested,
		&cls, &errMsg, &sha, &size, &hasManifestPath,
		&t.ScheduledAt, &startedAt, &finishedAt, &destinationID, &remoteState, &remoteKey); err != nil {
		return nil, err
	}
	if destinationID.Valid {
		t.DestinationID = destinationID.Int64
	}
	t.RemoteState = remoteState
	t.RemoteObjectKey = remoteKey
	t.ErrorClass = cls
	t.ErrorMessage = errMsg
	t.ArtifactSHA256 = sha
	t.ArtifactSize = size
	t.HasManifest = hasManifestPath != ""
	if startedAt.Valid {
		v := startedAt.Int64
		t.StartedAt = &v
	}
	if finishedAt.Valid {
		v := finishedAt.Int64
		t.FinishedAt = &v
	}
	return &t, nil
}

const taskColumns = `id, database_id, status, attempt, cancel_requested,
	error_class, error_message, artifact_sha256, artifact_size, manifest_path,
	scheduled_at, started_at, finished_at, destination_id, remote_state, remote_object_key`

// GetTask loads one job.
func GetTask(dbh *sql.DB, id int64) (*Task, error) {
	t, err := scanTask(func(dest ...any) error {
		return dbh.QueryRow(`SELECT `+taskColumns+` FROM jobs WHERE id = ?`, id).Scan(dest...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListTasks returns the newest jobs, optionally filtered by database.
func ListTasks(dbh *sql.DB, databaseID int64, limit int) ([]*Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + taskColumns + ` FROM jobs`
	args := []any{}
	if databaseID > 0 {
		q += ` WHERE database_id = ?`
		args = append(args, databaseID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := dbh.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Database is the API-safe view (never exposes encrypted credentials).
type Database struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	EnvTag        string `json:"envTag"`
	ServerVersion string `json:"serverVersion"`
	SSLMode       string `json:"sslMode"` // persisted TLS choice; visible status (round-1 P1-09)
	LastTask      *Task  `json:"lastTask,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// GetDatabase loads one database plus its latest job.
func GetDatabase(dbh *sql.DB, id int64) (*Database, error) {
	d := &Database{}
	err := dbh.QueryRow(`SELECT id, name, platform, env_tag, server_version, sslmode, created_at, updated_at
		FROM databases WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&d.ID, &d.Name, &d.Platform, &d.EnvTag, &d.ServerVersion, &d.SSLMode, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	lt, err := latestTask(dbh, id)
	if err == nil {
		d.LastTask = lt
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return d, nil
}

// ListDatabases returns all registrations, newest job attached.
func ListDatabases(dbh *sql.DB) ([]*Database, error) {
	rows, err := dbh.Query(`SELECT id, name, platform, env_tag, server_version, sslmode, created_at, updated_at
		FROM databases WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Database
	for rows.Next() {
		d := &Database{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.EnvTag, &d.ServerVersion, &d.SSLMode, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, d := range out {
		if lt, err := latestTask(dbh, d.ID); err == nil {
			d.LastTask = lt
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return out, nil
}

func latestTask(dbh *sql.DB, databaseID int64) (*Task, error) {
	t, err := scanTask(func(dest ...any) error {
		return dbh.QueryRow(`SELECT `+taskColumns+` FROM jobs WHERE database_id = ?
			ORDER BY id DESC LIMIT 1`, databaseID).Scan(dest...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}
