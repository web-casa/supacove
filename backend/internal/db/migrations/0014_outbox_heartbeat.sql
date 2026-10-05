-- Phase 7: notification outbox (dev-plan P7-1) and per-database dead-man
-- switch configuration (P7-2). Includes the UPGRADE path for the Phase-4
-- webhook vocabulary (round-1 review P1-02): legacy subscriptions like
-- "failure,expired" are converted to the new event names with a per-token,
-- idempotent replacement. Live-name uniqueness is enforced at the API layer
-- (a plain partial index here; legacy duplicates stay legal).
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
--
-- The conversion is PER-TOKEN (round-3 review on R2-P1-01): each CSV token
-- is delimited with commas before replacement, so
--   * it is idempotent ('expired' inside 'backup_expired' can never match
--     the delimited token ',expired,'),
--   * mixed subscriptions (backup events + verification_failed) convert the
--     backup tokens while verification_failed — which has no old equivalent —
--     passes through untouched,
--   * re-running Up after a Down re-converts cleanly.
UPDATE webhooks
SET events = TRIM(
    REPLACE(REPLACE(
      ',' || events || ',',
    ',failure,', ',backup_failed,'), ',expired,', ',backup_expired,'),
  ',')
WHERE deleted_at IS NULL;

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
-- Restore the LEGACY vocabulary with the same per-token technique: the
-- binary this rollback lands on matches subscriptions with the old event
-- words ('failure', 'expired'); leaving the converted names would silently
-- stop failure notifications after a rollback (round-2 review R2-P1-01).
-- verification_failed has no old equivalent and stays as-is.
DROP INDEX IF EXISTS idx_webhooks_live_name;
UPDATE webhooks
SET events = TRIM(
    REPLACE(REPLACE(
      ',' || events || ',',
    ',backup_failed,', ',failure,'), ',backup_expired,', ',expired,'),
  ',')
WHERE deleted_at IS NULL;
ALTER TABLE databases DROP COLUMN last_heartbeat_at;
ALTER TABLE databases DROP COLUMN heartbeat_grace_hours;
ALTER TABLE databases DROP COLUMN heartbeat_period_hours;
ALTER TABLE databases DROP COLUMN heartbeat_url;
DROP TABLE notification_outbox;

