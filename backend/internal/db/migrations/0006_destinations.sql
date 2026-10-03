-- Phase 3: destinations (BYOS), remote commit state (protocol C) and
-- retention (protocol D, beta simple form) — dev-plan Phase 3.
-- +goose Up
CREATE TABLE destinations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    platform        TEXT    NOT NULL DEFAULT 's3',
    endpoint        TEXT    NOT NULL DEFAULT '',
    region          TEXT    NOT NULL DEFAULT '',
    bucket          TEXT    NOT NULL,
    prefix          TEXT    NOT NULL DEFAULT '',
    access_key      TEXT    NOT NULL,
    secret_encrypted TEXT   NOT NULL,
    verify_readback INTEGER NOT NULL DEFAULT 1,
    keep_remote     INTEGER NOT NULL DEFAULT 10,
    keep_days       INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    deleted_at      INTEGER
);

-- Live-name uniqueness only: soft-deleted names are freed by rename at
-- delete time (same strategy as databases, round-4 phase-2 review).
CREATE UNIQUE INDEX idx_destinations_name_live
    ON destinations(name) WHERE deleted_at IS NULL;

ALTER TABLE databases ADD COLUMN destination_id INTEGER REFERENCES destinations(id);

ALTER TABLE jobs ADD COLUMN destination_id INTEGER;
-- remote_state: '' (local only), 'uploading' (intent recorded, not yet
-- remotely committed), 'committed' (remote manifest published + verified),
-- 'deleted' (retention removed the remote objects).
ALTER TABLE jobs ADD COLUMN remote_state TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN remote_object_key TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN remote_manifest_key TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN remote_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN uploaded_at INTEGER;

-- +goose Down
ALTER TABLE jobs DROP COLUMN uploaded_at;
ALTER TABLE jobs DROP COLUMN remote_verified;
ALTER TABLE jobs DROP COLUMN remote_manifest_key;
ALTER TABLE jobs DROP COLUMN remote_object_key;
ALTER TABLE jobs DROP COLUMN remote_state;
ALTER TABLE jobs DROP COLUMN destination_id;
ALTER TABLE databases DROP COLUMN destination_id;
DROP INDEX idx_destinations_name_live;
DROP TABLE destinations;
