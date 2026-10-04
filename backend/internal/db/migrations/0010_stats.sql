-- Phase 8: statistics persistence and security audit support.
-- +goose Up
CREATE TABLE backup_stats (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id          INTEGER NOT NULL REFERENCES jobs(id),
    database_name   TEXT NOT NULL,
    dump_size       INTEGER NOT NULL DEFAULT 0,
    artifact_size   INTEGER NOT NULL DEFAULT 0,
    duration_secs   REAL NOT NULL DEFAULT 0,
    verify_status   TEXT NOT NULL DEFAULT '',
    verify_tables   INTEGER NOT NULL DEFAULT 0,
    remote_committed INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);

CREATE INDEX idx_stats_created ON backup_stats(created_at);

-- +goose Down
DROP INDEX idx_stats_created;
DROP TABLE backup_stats;
