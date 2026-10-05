-- Phase 8: the FIRST of the three volume metrics (dev-plan §0): the source
-- database's physical size at dump time (pg_database_size), recorded on the
-- job for capacity statistics alongside the dump and ciphertext sizes.
-- +goose Up
ALTER TABLE jobs ADD COLUMN source_db_bytes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN source_db_bytes;
