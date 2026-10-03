-- Phase 2 review round 1 (codex-phase2-review-1):
--   P1-05  atomic Enqueue      → partial unique index on active jobs
--   P1-06  soft delete         → databases.deleted_at keeps history; no cascade loss
--   P1-12  artifact states     → jobs.artifact_state tracks the commit pipeline
--   P2-05  error model         → jobs.error_code + jobs.retryable
-- +goose Up
-- Converge duplicate active rows that older versions (without the unique
-- index) could produce: keep the OLDEST active job per database as pending,
-- demote the rest to interrupted with an explanatory class. Deleting audit
-- history is not an option (round-2 review R2-P1-02).
UPDATE jobs SET status = 'interrupted',
    error_class = 'unknown',
    error_message = 'superseded: duplicate active job collapsed during upgrade to the no-overlap guarantee',
    finished_at = strftime('%s','now')
WHERE status IN ('pending','running')
  AND id NOT IN (
    SELECT MIN(id) FROM jobs WHERE status IN ('pending','running') GROUP BY database_id
  );

CREATE UNIQUE INDEX idx_jobs_active_per_database
    ON jobs(database_id) WHERE status IN ('pending','running');

ALTER TABLE databases ADD COLUMN deleted_at INTEGER;
ALTER TABLE databases ADD COLUMN sslmode TEXT NOT NULL DEFAULT '';

-- Name uniqueness applies to LIVE registrations only: soft-deleted names are
-- free for reuse (round-2 review R2-P2-01). SQLite cannot drop a constraint,
-- so the table is rebuilt without the table-wide UNIQUE, then a partial
-- unique index enforces live-name uniqueness.
CREATE TABLE databases_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL,
    platform       TEXT    NOT NULL DEFAULT 'generic',
    env_tag        TEXT    NOT NULL DEFAULT '',
    conn_encrypted TEXT    NOT NULL,
    server_version TEXT    NOT NULL DEFAULT '',
    sslmode        TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    deleted_at     INTEGER
);
INSERT INTO databases_new (id, name, platform, env_tag, conn_encrypted, server_version, sslmode, created_at, updated_at, deleted_at)
    SELECT id, name, platform, env_tag, conn_encrypted, server_version,
           COALESCE(sslmode,''), created_at, updated_at, deleted_at FROM databases;
DROP TABLE databases;
ALTER TABLE databases_new RENAME TO databases;
CREATE UNIQUE INDEX idx_databases_name_live
    ON databases(name) WHERE deleted_at IS NULL;

ALTER TABLE jobs ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN retryable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN artifact_state TEXT NOT NULL DEFAULT '';
-- artifact_state values:
--   ''                  no artifact yet
--   'committed'         ciphertext renamed into place (protocol A complete)
--   'committed_no_manifest'  ciphertext committed but manifest write failed

-- +goose Down
DROP TABLE IF EXISTS jobs_backup_idx_guard;
ALTER TABLE jobs DROP COLUMN artifact_state;
ALTER TABLE jobs DROP COLUMN retryable;
ALTER TABLE jobs DROP COLUMN error_code;
ALTER TABLE databases DROP COLUMN sslmode;
ALTER TABLE databases DROP COLUMN deleted_at;
DROP INDEX idx_jobs_active_per_database;
