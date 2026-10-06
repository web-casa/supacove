-- Phase 3 review round-1 fixes:
--   P0-01/P1-01: immutable backup identity (backup_uuid already exists from 0007)
--   P1-06: version IDs for B2/versioned S3
--   P1-12: artifact states for failed uploads
--   P2-01: destinations FK nullable (already nullable, just fixing jobs)
-- +goose Up
ALTER TABLE jobs ADD COLUMN artifact_version_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN manifest_version_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN artifact_version_id;
ALTER TABLE jobs DROP COLUMN manifest_version_id;

-- duration_secs is NOT added here: 0004 already carries the column (it was
-- completed there in a later fix). A former trailing ALTER in this Down
-- section made every rollback below v8 fail with "duplicate column name"
-- (overall review P2-R1).
