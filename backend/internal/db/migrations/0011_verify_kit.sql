-- Phase 5+6: recovery kit generation and restore verification state.
-- +goose Up
ALTER TABLE jobs ADD COLUMN verify_status TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN verify_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN verify_tables INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN verify_duration_secs REAL NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN recovery_kit_path TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN platform TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN platform;
ALTER TABLE jobs DROP COLUMN recovery_kit_path;
ALTER TABLE jobs DROP COLUMN verify_duration_secs;
ALTER TABLE jobs DROP COLUMN verify_tables;
ALTER TABLE jobs DROP COLUMN verify_detail;
ALTER TABLE jobs DROP COLUMN verify_status;
