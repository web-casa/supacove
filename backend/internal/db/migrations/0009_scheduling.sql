-- Phase 4: scheduling configuration and freshness tracking.
-- +goose Up
ALTER TABLE databases ADD COLUMN cron_expr TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN cron_tz TEXT NOT NULL DEFAULT 'UTC';
ALTER TABLE databases ADD COLUMN max_age_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN schedule_paused INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN last_scheduled_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE jobs ADD COLUMN freshness_hours REAL NOT NULL DEFAULT -1;

CREATE TABLE webhooks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    url         TEXT    NOT NULL,
    events      TEXT    NOT NULL DEFAULT 'failure,expired',
    created_at  INTEGER NOT NULL,
    deleted_at  INTEGER
);

-- +goose Down
ALTER TABLE databases DROP COLUMN last_scheduled_at;
ALTER TABLE databases DROP COLUMN schedule_paused;
ALTER TABLE databases DROP COLUMN max_age_hours;
ALTER TABLE databases DROP COLUMN cron_tz;
ALTER TABLE databases DROP COLUMN cron_expr;
ALTER TABLE jobs DROP COLUMN freshness_hours;
DROP TABLE webhooks;
