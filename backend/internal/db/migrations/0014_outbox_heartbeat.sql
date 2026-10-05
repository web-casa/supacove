-- Phase 7: notification outbox (dev-plan P7-1) and per-database dead-man
-- switch configuration (P7-2). Includes the UPGRADE path for the Phase-4
-- webhook vocabulary (round-1 review P1-02/P2-04): legacy subscriptions
-- "failure,expired" are converted to the new event names, and legacy
-- duplicate live names are deterministically renamed before the unique
-- index lands.
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

-- Legacy subscription vocabulary (0009 default "failure,expired") must keep
-- delivering after the upgrade: exact new-vocabulary matching would silently
-- zero every target and the empty-target path would mark events delivered.
-- The guard makes the conversion TOKEN-EXACT and re-entrant (round-2 review
-- R2-P1-01: a naive REPLACE re-hits 'expired' inside 'backup_expired' on a
-- Down→Up cycle, producing backup_backup_expired).
UPDATE webhooks
SET events = REPLACE(REPLACE(events, 'failure', 'backup_failed'), 'expired', 'backup_expired')
WHERE deleted_at IS NULL
  AND events NOT LIKE '%backup_failed%'
  AND events NOT LIKE '%backup_expired%'
  AND events NOT LIKE '%verification_failed%'
  AND (events LIKE '%failure%' OR events LIKE '%expired%');

-- Live webhook names are unique among LIVE rows — enforced at the API layer
-- with an atomic INSERT..WHERE NOT EXISTS (round-2 review P2-04: a unique
-- index required renaming legacy duplicates, and generated names can
-- themselves collide, blocking upgrades; legacy duplicates were legal data
-- and keep working as receivers).
CREATE INDEX idx_webhooks_live_name ON webhooks(name) WHERE deleted_at IS NULL;

ALTER TABLE databases ADD COLUMN heartbeat_url TEXT NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN heartbeat_period_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN heartbeat_grace_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN last_heartbeat_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- Restore the LEGACY vocabulary: the binary this rollback lands on matches
-- subscriptions with the old event words ('failure', 'expired'); leaving
-- the converted names would silently stop failure notifications after a
-- rollback (round-2 review R2-P1-01). Rows created with the new
-- 'verification_failed' event have no old equivalent and stay as-is.
DROP INDEX IF EXISTS idx_webhooks_live_name;
UPDATE webhooks
SET events = REPLACE(REPLACE(events, 'backup_failed', 'failure'), 'backup_expired', 'expired')
WHERE deleted_at IS NULL
  AND (events LIKE '%backup_failed%' OR events LIKE '%backup_expired%');
ALTER TABLE databases DROP COLUMN last_heartbeat_at;
ALTER TABLE databases DROP COLUMN heartbeat_grace_hours;
ALTER TABLE databases DROP COLUMN heartbeat_period_hours;
ALTER TABLE databases DROP COLUMN heartbeat_url;
DROP TABLE notification_outbox;

