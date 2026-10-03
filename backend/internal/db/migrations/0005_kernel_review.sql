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
-- freed by RENAMING the row at soft-delete time (round-4 review R4-P1-01 —
-- rebuilding the table would cascade-drop every job via the FK). The
-- table-wide UNIQUE stays; the server layer always renames on delete, so
-- "name + deleted_at IS NULL" uniqueness is preserved in practice, and a
-- deleted tombstone never collides because it carries a deleted-<id> prefix.

ALTER TABLE jobs ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN retryable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN artifact_state TEXT NOT NULL DEFAULT '';
