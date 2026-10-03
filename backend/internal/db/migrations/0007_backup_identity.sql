-- Phase 3 review round-1 P0-01: object keys must be built from a globally
-- unique, immutable identity — SQLite auto-increment IDs restart when the
-- metadata DB is rebuilt, and a fresh instance reusing the same bucket/prefix
-- would silently overwrite existing backups.
--
-- backup_uuid is generated (random 128-bit hex) at enqueue time, persisted
-- before any remote side effect, and is the ONLY component used for remote
-- object keys. Rebuilding SQLite produces new UUIDs, so old objects can never
-- be overwritten.
-- +goose Up
ALTER TABLE jobs ADD COLUMN backup_uuid TEXT NOT NULL DEFAULT '';

-- Backfill: jobs upgraded from previous versions get a deterministic
-- placeholder (legacy-<jobid>) so they remain addressable; fresh instances
-- always mint real UUIDs at enqueue.
UPDATE jobs SET backup_uuid = 'legacy-' || printf('%012x', id)
WHERE backup_uuid = '';

CREATE UNIQUE INDEX idx_jobs_backup_uuid ON jobs(backup_uuid) WHERE backup_uuid != '';

-- +goose Down
DROP INDEX idx_jobs_backup_uuid;
ALTER TABLE jobs DROP COLUMN backup_uuid;
