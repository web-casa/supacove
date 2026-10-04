-- Phase 5 review follow-up: persist which verification profile produced a
-- verify result (embedded throwaway PostgreSQL instance today), so a
-- 'verified' status always states HOW it was proven.
-- +goose Up
ALTER TABLE jobs ADD COLUMN verify_profile TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN verify_profile;
