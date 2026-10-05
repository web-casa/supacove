-- Phase 7: notification outbox (dev-plan P7-1) and per-database dead-man
-- switch configuration (P7-2).
-- +goose Up
CREATE TABLE notification_outbox (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        TEXT    NOT NULL UNIQUE,
    event_type      TEXT    NOT NULL,
    database_id     INTEGER,
    database_name   TEXT    NOT NULL DEFAULT '',
    payload         TEXT    NOT NULL,
    state           TEXT    NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending','delivering','delivered','dead')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    delivered_at    INTEGER
);
CREATE INDEX idx_outbox_due ON notification_outbox(state, next_attempt_at);

-- Webhook names are unique among LIVE rows (soft-deleted names free up).
CREATE UNIQUE INDEX idx_webhooks_live_name ON webhooks(name) WHERE deleted_at IS NULL;

ALTER TABLE databases ADD COLUMN heartbeat_url TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN heartbeat_period_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN heartbeat_grace_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN last_heartbeat_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
DROP INDEX IF EXISTS idx_webhooks_live_name;
ALTER TABLE databases DROP COLUMN last_heartbeat_at;
ALTER TABLE databases DROP COLUMN heartbeat_grace_hours;
ALTER TABLE databases DROP COLUMN heartbeat_period_hours;
ALTER TABLE databases DROP COLUMN heartbeat_url;
DROP TABLE notification_outbox;
