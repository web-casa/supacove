-- Phase 2: backup execution kernel (dev-plan Phase 2, protocols A/B/E).
--
-- settings:    instance-wide key/value state (age recipient, fingerprints).
-- databases:   registered backup targets; connection URIs are stored
--              AES-GCM-encrypted with the application master secret
--              (dev-plan §0.5 protocol B) and are never returned by the API.
-- jobs:        task state machine — pending → running → succeeded /
--              failed / canceled / interrupted, with the seven error
--              classes and retryable flag (dev-plan P2 task 4).
-- +goose Up
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE databases (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL UNIQUE,
    platform       TEXT    NOT NULL DEFAULT 'generic',
    env_tag        TEXT    NOT NULL DEFAULT '',
    conn_encrypted TEXT    NOT NULL,
    server_version TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE jobs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    database_id    INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    status         TEXT    NOT NULL
                   CHECK (status IN ('pending','running','succeeded','failed','canceled','interrupted')),
    attempt        INTEGER NOT NULL DEFAULT 1,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    error_class    TEXT    NOT NULL DEFAULT '',
    error_message  TEXT    NOT NULL DEFAULT '',
    artifact_path  TEXT    NOT NULL DEFAULT '',
    artifact_sha256 TEXT   NOT NULL DEFAULT '',
    artifact_size  INTEGER NOT NULL DEFAULT 0,
    manifest_path  TEXT    NOT NULL DEFAULT '',
    scheduled_at   INTEGER NOT NULL,
    started_at     INTEGER,
    finished_at    INTEGER,
    created_at     INTEGER NOT NULL
);

CREATE INDEX idx_jobs_database_created ON jobs(database_id, created_at DESC);
CREATE INDEX idx_jobs_status ON jobs(status) WHERE status IN ('pending','running');

-- +goose Down
DROP INDEX idx_jobs_status;
DROP INDEX idx_jobs_database_created;
DROP TABLE jobs;
DROP TABLE databases;
DROP TABLE settings;
